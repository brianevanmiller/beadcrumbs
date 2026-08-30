package ledger_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/brianevanmiller/beadcrumbs/internal/ledger"
)

// Sampling is tested against the real Dolt fixture, because most of what these
// tests assert is enforced jointly by the ledger and the schema: the caps on a
// relayed grant are Go's alone (ck_aut_mandatory_human cannot fire on a row
// stamped human), but the provenance constraint, the foreign keys, and the
// append-only histories they compose with are the database's.

// sampler builds a second Ledger over the same store. A relay is by definition
// two invocations — a human answered, an agent typed — and the actor, the
// clock, and the repository policy are all per-invocation in production.
func (f *fixture) sampler(actor ledger.Provenance, opts ...func(*ledger.Options)) *ledger.Ledger {
	f.t.Helper()
	o := ledger.Options{Actor: actor, Redactor: mustRedactor(f), Config: f.L.Config()}
	for _, fn := range opts {
		fn(&o)
	}
	return ledger.New(f.Store, o)
}

func at(when time.Time) func(*ledger.Options) {
	return func(o *ledger.Options) { o.Now = func() time.Time { return when } }
}

func relayActor() ledger.Provenance {
	return ledger.Provenance{
		ActorID: "claude", ActorKind: ledger.ActorAgent,
		ActorModel: "claude-opus-5", SessionID: "relay-1",
	}
}

// blockedProposal records a proposal that needs a human decision and has not
// had one. The capability is what makes it need one, which is the case a
// sampled nudge is allowed to unblock; class `policy` and a requested
// `mandatory` are the two it is not.
func (f *fixture) blockedProposal(t *testing.T, class string, requested ledger.AuthorityLevel,
	caps ...ledger.Capability) ledger.ProposalID {
	t.Helper()
	crumb := f.capture("the engine is opened once per command", 0.6)
	synthesis, err := f.L.CompleteHarvest(context.Background(), ledger.CompleteHarvest{
		Mode: ledger.HarvestManual, Crumbs: []ledger.CrumbID{crumb.ID},
		Title: "one engine per command", Content: "body", Class: "learning", Confidence: 0.6,
	})
	if err != nil {
		t.Fatalf("synthesising: %v", err)
	}
	res, err := f.L.ProposePromotion(context.Background(), ledger.ProposePromotion{
		InsightID: synthesis.Insight.ID, Class: class,
		Destination:        docsTo("docs/"+class+"-"+string(requested)+".md", caps...),
		Content:            "what would be written",
		Confidence:         0.6,
		RequestedAuthority: requested,
	})
	if err != nil {
		t.Fatalf("proposing: %v", err)
	}
	return res.Proposal.ID
}

func (f *fixture) deliver(t *testing.T, led *ledger.Ledger, who ledger.PromptRespondent) ledger.DeliverResult {
	t.Helper()
	res, err := led.DeliverAsks(context.Background(), ledger.DeliverQuery{Respondent: who})
	if err != nil {
		t.Fatalf("delivering to %s: %v", who, err)
	}
	return res
}

// The registry ships with three questions and they are the ones the skill and
// the answer paths name. A seed that failed to parse would make every enqueue a
// not-found, which is a silent feature outage rather than a failure.
func TestSeededPromptsAreActiveAndParse(t *testing.T) {
	f := newFixtureWith(t, nil, humanActor())
	prompts, err := f.L.Prompts(context.Background(), ledger.PromptQuery{ActiveOnly: true})
	if err != nil {
		t.Fatalf("listing prompts: %v", err)
	}
	want := map[string]ledger.AnswerKind{
		"authority-nudge": ledger.AnswerKindChoice,
		"calibration":     ledger.AnswerKindChoice,
		"context-flush":   ledger.AnswerKindShortText,
	}
	if len(prompts) != len(want) {
		t.Fatalf("the registry holds %d active prompts, want %d", len(prompts), len(want))
	}
	for _, p := range prompts {
		kind, ok := want[p.Key]
		if !ok {
			t.Fatalf("unexpected seeded prompt %q", p.Key)
		}
		if p.AnswerKind != kind {
			t.Fatalf("%s is a %s prompt, want %s", p.Key, p.AnswerKind, kind)
		}
		if p.Version != 1 || p.Origin != ledger.PromptOriginCurated {
			t.Fatalf("%s seeded as version %d origin %s", p.Key, p.Version, p.Origin)
		}
		if p.AnswerKind == ledger.AnswerKindChoice && len(p.Options) < 2 {
			t.Fatalf("%s is a choice with %d option(s)", p.Key, len(p.Options))
		}
	}
}

// An agent must not phrase the exam it is graded on. This is the whole reason
// `prompts propose` is not in this build, and the check has to live in the
// ledger: a CLI-only rule would be one an SDK caller never meets.
func TestAddPromptHumanTrackRequiresHumanActor(t *testing.T) {
	f := newFixture(t) // agent actor
	ctx := context.Background()
	for _, who := range []ledger.PromptRespondent{ledger.PromptRespondentHuman, ledger.PromptRespondentBoth} {
		_, _, err := f.L.AddPrompt(ctx, ledger.AddPrompt{
			Key: "agent-authored-" + string(who), Respondent: who,
			Question: "did I do well?", AnswerKind: ledger.AnswerKindShortText,
			TriggerClass: "manual",
		})
		var le *ledger.Error
		if !errors.As(err, &le) || le.Code != "invalid_ask" {
			t.Fatalf("an agent registering a %s prompt returned %v, want invalid_ask", who, err)
		}
	}
	// The agent track is its own business.
	if _, _, err := f.L.AddPrompt(ctx, ledger.AddPrompt{
		Key: "agent-authored-agent", Respondent: ledger.PromptRespondentAgent,
		Question: "what did you not write down?", AnswerKind: ledger.AnswerKindShortText,
		TriggerClass: "event",
	}); err != nil {
		t.Fatalf("an agent may register an agent-track prompt: %v", err)
	}
}

// Disabling is per key. Leaving version 1 active while version 2 is disabled
// would silently resume asking the older wording, which is not what anyone
// means by "stop asking this".
func TestDisablePromptDeactivatesEveryVersion(t *testing.T) {
	f := newFixtureWith(t, nil, humanActor())
	ctx := context.Background()
	add := func(question string) ledger.Prompt {
		p, _, err := f.L.AddPrompt(ctx, ledger.AddPrompt{
			Key: "friction", Respondent: ledger.PromptRespondentHuman,
			Question: question, AnswerKind: ledger.AnswerKindShortText, TriggerClass: "manual",
		})
		if err != nil {
			t.Fatalf("adding: %v", err)
		}
		return p
	}
	if v := add("what slowed you down?"); v.Version != 1 {
		t.Fatalf("first version is %d", v.Version)
	}
	if v := add("what slowed you down today?"); v.Version != 2 {
		t.Fatalf("second version is %d", v.Version)
	}
	if _, err := f.L.DisablePrompt(ctx, "friction"); err != nil {
		t.Fatalf("disabling: %v", err)
	}
	rows, err := f.L.Prompts(ctx, ledger.PromptQuery{Keys: []string{"friction"}})
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("disabling rewrote the registry: %d row(s)", len(rows))
	}
	for _, p := range rows {
		if p.Active {
			t.Fatalf("version %d is still active", p.Version)
		}
	}
	if _, err := f.L.EnqueueAsk(ctx, ledger.EnqueueAsk{PromptKey: "friction"}); !errors.Is(err, ledger.ErrNotFound) {
		t.Fatalf("a disabled key must not enqueue, got %v", err)
	}
}

// One open ask per target and respondent, whatever the session. A blocked
// proposal is nudged once: the point of the question is the decision, and the
// decision does not become more pending because a new session started.
func TestEnqueueRefusesASecondOpenAskForOneTarget(t *testing.T) {
	f := newFixtureWith(t, nil, humanActor())
	ctx := context.Background()
	proposal := f.blockedProposal(t, "learning", ledger.AuthorityAdvisory, ledger.CapRequiresHumanAuthority)
	target := ledger.RecordRef{Kind: ledger.KindProposal, ID: string(proposal)}

	if _, err := f.L.EnqueueAsk(ctx, ledger.EnqueueAsk{PromptKey: "authority-nudge", Target: target}); err != nil {
		t.Fatalf("first enqueue: %v", err)
	}
	_, err := f.L.EnqueueAsk(ctx, ledger.EnqueueAsk{PromptKey: "authority-nudge", Target: target})
	var le *ledger.Error
	if !errors.As(err, &le) || le.Code != "ask_already_open" {
		t.Fatalf("second enqueue returned %v, want ask_already_open", err)
	}
	// A different session is still the same decision.
	other := f.sampler(ledger.Provenance{
		ActorID: "other", ActorKind: ledger.ActorHuman,
	})
	if _, err := other.EnqueueAsk(ctx, ledger.EnqueueAsk{PromptKey: "authority-nudge", Target: target}); err == nil {
		t.Fatal("a second session opened a second ask for one proposal")
	}
}

// An empty queue is success. `bdc ask deliver` runs after every prime, and a
// state that is normal must not look like a failure to the caller that scripts it.
func TestDeliverWithNothingPendingIsSuccess(t *testing.T) {
	f := newFixtureWith(t, nil, humanActor())
	res := f.deliver(t, f.L, ledger.PromptRespondentHuman)
	if len(res.Questions) != 0 {
		t.Fatalf("an empty ledger delivered %d question(s)", len(res.Questions))
	}
}

// The dead-letter service: a proposal waiting on a human is exactly a question
// the ledger cannot answer for itself, so delivering to a human materialises it
// — once, not once per deliver.
func TestDeliverMaterialisesOneNudgePerBlockedProposal(t *testing.T) {
	f := newFixtureWith(t, nil, humanActor())
	proposal := f.blockedProposal(t, "policy", ledger.AuthorityAdvisory)

	first := f.deliver(t, f.L, ledger.PromptRespondentHuman)
	if len(first.Questions) != 1 {
		t.Fatalf("delivered %d question(s), want the one nudge", len(first.Questions))
	}
	q := first.Questions[0]
	if q.PromptKey != "authority-nudge" || q.Target.ID != string(proposal) {
		t.Fatalf("delivered %+v", q)
	}
	if !strings.Contains(q.Question, string(proposal)) {
		t.Fatalf("the snapshot does not name the proposal: %q", q.Question)
	}

	// A second deliver mints nothing: the proposal already has an open ask, and
	// one blocked decision is one question however many sessions pass through.
	// The delivered ask is not re-presented either — delivered_at is when it was
	// shown, and latency is measured from it — but it stays answerable until it
	// expires.
	second := f.deliver(t, f.L, ledger.PromptRespondentHuman)
	if len(second.Questions) != 0 {
		t.Fatalf("a second deliver re-presented %+v", second.Questions)
	}
	if n := f.count(`SELECT COUNT(*) FROM asks`); n != 1 {
		t.Fatalf("%d ask rows exist; the nudge was materialised more than once", n)
	}
	if state := f.askState(t, q.ID); state != ledger.AskDelivered {
		t.Fatalf("the presented ask is %s, want delivered", state)
	}
}

// Four is the presentation batch whatever the queue holds. A surface that can
// present ten questions is a surface that will be ignored.
func TestDeliverPresentsAtMostTheBatchCap(t *testing.T) {
	f := newFixtureWith(t, nil, humanActor())
	ctx := context.Background()
	if _, _, err := f.L.AddPrompt(ctx, ledger.AddPrompt{
		Key: "per-crumb", Respondent: ledger.PromptRespondentHuman,
		Question: "anything more about {target}?", AnswerKind: ledger.AnswerKindShortText,
		TriggerClass: "manual",
	}); err != nil {
		t.Fatalf("adding: %v", err)
	}
	for i := 0; i < 10; i++ {
		crumb := f.capture(fixtureText(i), 0.5)
		if _, err := f.L.EnqueueAsk(ctx, ledger.EnqueueAsk{
			PromptKey: "per-crumb",
			Target:    ledger.RecordRef{Kind: ledger.KindCrumb, ID: string(crumb.ID)},
		}); err != nil {
			t.Fatalf("enqueue %d: %v", i, err)
		}
	}
	if cap := f.L.Config().AskMaxPerDeliver; cap != 0 {
		t.Fatalf("this test needs the uncapped default, got %d", cap)
	}
	res := f.deliver(t, f.L, ledger.PromptRespondentHuman)
	if len(res.Questions) != 4 {
		t.Fatalf("delivered %d question(s), want 4", len(res.Questions))
	}
}

// A Dolt merge can leave two clones' asks for one target side by side. Neither
// is wrong and neither can be reconciled by id, so the oldest is the question
// and the rest expire; presenting both would record two answers to one decision.
func TestDeliverExpiresMergeDuplicateAsks(t *testing.T) {
	f := newFixtureWith(t, nil, humanActor())
	ctx := context.Background()
	proposal := f.blockedProposal(t, "policy", ledger.AuthorityAdvisory)
	target := ledger.RecordRef{Kind: ledger.KindProposal, ID: string(proposal)}
	original, err := f.L.EnqueueAsk(ctx, ledger.EnqueueAsk{PromptKey: "authority-nudge", Target: target})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	// What a merge produces: a second open row the uniqueness check never saw.
	duplicate := original
	duplicate.ID = ledger.NewAskID()
	duplicate.CreatedAt = original.CreatedAt.Add(time.Second)
	f.write(func(tx ledger.Tx) error { return tx.InsertAsk(duplicate) })

	res := f.deliver(t, f.L, ledger.PromptRespondentHuman)
	if len(res.Questions) != 1 || res.Questions[0].ID != original.ID {
		t.Fatalf("deliver presented %+v, want only the older ask %s", res.Questions, original.ID)
	}
	if state := f.askState(t, duplicate.ID); state != ledger.AskExpired {
		t.Fatalf("the duplicate is %s, want expired", state)
	}
}

// Expiry is lazy because there is no daemon, and a lazy sweep that only ran on
// deliver would leave a stale question answerable forever by anyone holding its
// id. So the answer path expires too — records it, then refuses.
func TestAnswerPastExpiryIsRefusedEvenWithoutADeliver(t *testing.T) {
	f := newFixtureWith(t, nil, humanActor())
	ctx := context.Background()
	proposal := f.blockedProposal(t, "policy", ledger.AuthorityAdvisory)
	ask, err := f.L.EnqueueAsk(ctx, ledger.EnqueueAsk{
		PromptKey: "authority-nudge",
		Target:    ledger.RecordRef{Kind: ledger.KindProposal, ID: string(proposal)},
	})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	later := f.sampler(humanActor(), at(ask.ExpiresAt.Add(time.Minute)))
	_, err = later.AnswerAsk(ctx, ledger.AnswerAsk{AskID: ask.ID, ChoiceID: "wait"})
	var le *ledger.Error
	if !errors.As(err, &le) || le.Code != "invalid_ask" {
		t.Fatalf("answering a lapsed ask returned %v, want invalid_ask", err)
	}
	if state := f.askState(t, ask.ID); state != ledger.AskExpired {
		t.Fatalf("the lapsed ask is %s, want expired", state)
	}
	if n := f.count(`SELECT COUNT(*) FROM crumbs WHERE content LIKE '[ask %'`); n != 0 {
		t.Fatalf("a refused answer wrote %d Crumb(s)", n)
	}
	// And skip agrees: the same rule, from the other side.
	if _, err := later.SkipAsk(ctx, ledger.SkipAsk{AskID: ask.ID}); err == nil {
		t.Fatal("skipping a lapsed ask succeeded")
	}
}

// A relayed grant is the human respondent's record. The agent stays on the Ask
// as the transport, which is what makes the relay reconstructible without a
// via_session column on every provenance table.
func TestAnswerAskGrantDefaultSetsHumanProvenanceAndViaSessionOnAsk(t *testing.T) {
	f := newFixtureWith(t, nil, humanActor())
	ctx := context.Background()
	proposal := f.blockedProposal(t, "learning", ledger.AuthorityAdvisory, ledger.CapRequiresHumanAuthority)
	ask := f.deliver(t, f.L, ledger.PromptRespondentHuman).Questions[0]

	relay := f.sampler(relayActor())
	res, err := relay.AnswerAsk(ctx, ledger.AnswerAsk{
		AskID: ask.ID, ChoiceID: "grant-default", RespondentID: "brian",
	})
	if err != nil {
		t.Fatalf("relaying the answer: %v", err)
	}
	if res.Authority == nil {
		t.Fatal("a non-policy proposal that asked for advisory force got no working default")
	}
	if res.Authority.Level != ledger.AuthorityDefault || res.Authority.Scope != "" {
		t.Fatalf("granted %+v; a sampled grant is an unscoped working default", res.Authority)
	}
	if res.Authority.ActorKind != ledger.ActorHuman || res.Authority.ActorID != "brian" {
		t.Fatalf("the grant is attributed to %s/%s", res.Authority.ActorID, res.Authority.ActorKind)
	}
	if res.Crumb.ActorKind != ledger.ActorHuman || res.Crumb.SessionID != "" || res.Crumb.ActorModel != "" {
		t.Fatalf("the answer Crumb carries %+v; a human has no model and no session", res.Crumb.Provenance)
	}
	if res.Ask.ViaSession != "relay-1" {
		t.Fatalf("via_session = %q, want the relaying agent's session", res.Ask.ViaSession)
	}
	// asks.actor_* is who minted the row and is never rewritten; via_session is
	// how the relay is reconstructed, which is why no provenance table needed a
	// via_session column of its own.
	if res.Ask.ActorKind != ledger.ActorHuman {
		t.Fatalf("the Ask's provenance changed on answer: %+v", res.Ask.Provenance)
	}
	if res.Ask.CrumbID != res.Crumb.ID || res.Ask.AuthorityID != res.Authority.ID {
		t.Fatalf("the ask does not join to what the answer produced: %+v", res.Ask)
	}
	// The grant is what the question was for: the ledger stops reporting the
	// proposal as waiting on a human. Unblocking the dead letter is the entire
	// reason the human track exists.
	n, err := f.L.Narrative(ctx, ledger.NarrativeQuery{Mode: ledger.ModeContext})
	if err != nil {
		t.Fatalf("reading context: %v", err)
	}
	for _, q := range n.OpenQuestions {
		if q.Kind == "authority_required" && q.Subject.ID == string(proposal) {
			t.Fatalf("the proposal is still waiting on a human after the grant: %+v", q)
		}
	}
}

// The repository policy that stops an *agent* granting a working default has
// nothing to say about a human who granted one through an agent. Pinned by name
// so the reading stays a decision rather than an accident.
func TestAnswerAskGrantDefaultIgnoresAgentMaySetDefault(t *testing.T) {
	f := newFixtureWith(t, nil, humanActor())
	if f.L.Config().AgentMaySetDefault {
		t.Fatal("this test needs the seeded policy, which forbids an agent to grant a default")
	}
	f.blockedProposal(t, "learning", ledger.AuthorityAdvisory, ledger.CapRequiresHumanAuthority)
	ask := f.deliver(t, f.L, ledger.PromptRespondentHuman).Questions[0]

	res, err := f.sampler(relayActor()).AnswerAsk(context.Background(), ledger.AnswerAsk{
		AskID: ask.ID, ChoiceID: "grant-default",
	})
	if err != nil {
		t.Fatalf("the human's grant was refused because an agent typed it: %v", err)
	}
	if res.Authority == nil || res.Authority.ActorKind != ledger.ActorHuman {
		t.Fatalf("expected a human working default, got %+v", res.Authority)
	}
}

// The first cap. ck_aut_mandatory_human cannot fire here — the relayed row is
// stamped human — so this check in AnswerAsk is the only gate there is.
func TestAnswerAskDoesNotGrantMandatory(t *testing.T) {
	f := newFixtureWith(t, nil, humanActor())
	f.blockedProposal(t, "learning", ledger.AuthorityMandatory)
	ask := f.deliver(t, f.L, ledger.PromptRespondentHuman).Questions[0]

	res, err := f.sampler(relayActor()).AnswerAsk(context.Background(), ledger.AnswerAsk{
		AskID: ask.ID, ChoiceID: "grant-default",
	})
	if err != nil {
		t.Fatalf("the answer must be recorded even when the grant is capped: %v", err)
	}
	assertCapped(t, f, res)
}

// The second cap. A policy record's force is over the repository's own rules,
// which is exactly the thing a one-tap answer may not establish.
func TestAnswerAskDoesNotGrantPolicyClass(t *testing.T) {
	f := newFixtureWith(t, nil, humanActor())
	f.blockedProposal(t, "policy", ledger.AuthorityAdvisory)
	ask := f.deliver(t, f.L, ledger.PromptRespondentHuman).Questions[0]

	res, err := f.sampler(relayActor()).AnswerAsk(context.Background(), ledger.AnswerAsk{
		AskID: ask.ID, ChoiceID: "grant-default",
	})
	if err != nil {
		t.Fatalf("the answer must be recorded even when the grant is capped: %v", err)
	}
	assertCapped(t, f, res)
}

func assertCapped(t *testing.T, f *fixture, res ledger.AnswerResult) {
	t.Helper()
	if res.Authority != nil {
		t.Fatalf("a capped answer granted %+v", res.Authority)
	}
	if n := f.count(`SELECT COUNT(*) FROM authorities`); n != 0 {
		t.Fatalf("%d authority row(s) exist after a capped answer", n)
	}
	if !hasNotice(res.Notices, "ask_grant_capped") {
		t.Fatalf("a capped grant produced no ask_grant_capped warning: %+v", res.Notices)
	}
	if res.Ask.State != ledger.AskAnswered || res.Crumb.ID == "" {
		t.Fatalf("the answer itself was not recorded: %+v", res.Ask)
	}
}

// Recommending rejection is not rejecting. The seeded option says "Recommend
// rejection" because a question must not promise an action its answer does not
// perform, and the warning names the command that would.
func TestAnswerAskRejectDoesNotRejectThePromotion(t *testing.T) {
	f := newFixtureWith(t, nil, humanActor())
	proposal := f.blockedProposal(t, "policy", ledger.AuthorityAdvisory)
	ask := f.deliver(t, f.L, ledger.PromptRespondentHuman).Questions[0]

	res, err := f.L.AnswerAsk(context.Background(), ledger.AnswerAsk{AskID: ask.ID, ChoiceID: "reject"})
	if err != nil {
		t.Fatalf("answering: %v", err)
	}
	if !hasNotice(res.Notices, "ask_reject_not_applied") {
		t.Fatalf("no ask_reject_not_applied warning: %+v", res.Notices)
	}
	if n := f.count(`SELECT COUNT(*) FROM promotions WHERE proposal_id = ?`, string(proposal)); n != 0 {
		t.Fatalf("a recommendation recorded %d promotion attempt(s)", n)
	}
	if res.Crumb.ID == "" {
		t.Fatal("the recommendation itself was not recorded")
	}
}

// "Keep waiting" is an answer, not a skip: a human looked and decided not yet,
// and that is a human-provenance record worth keeping.
func TestAnswerAskWaitRecordsACrumbAndNothingElse(t *testing.T) {
	f := newFixtureWith(t, nil, humanActor())
	f.blockedProposal(t, "policy", ledger.AuthorityAdvisory)
	ask := f.deliver(t, f.L, ledger.PromptRespondentHuman).Questions[0]

	res, err := f.L.AnswerAsk(context.Background(), ledger.AnswerAsk{AskID: ask.ID, ChoiceID: "wait"})
	if err != nil {
		t.Fatalf("answering: %v", err)
	}
	if res.Authority != nil || res.Validation != nil {
		t.Fatalf("wait produced %+v / %+v", res.Authority, res.Validation)
	}
	if res.Ask.State != ledger.AskAnswered || res.Ask.ChoiceID != "wait" {
		t.Fatalf("the ask records %+v", res.Ask)
	}
	if res.Crumb.Confidence != 0.9 {
		t.Fatalf("a human answer carries confidence %v, want 0.9", res.Crumb.Confidence)
	}
}

// Calibration is the one prompt that writes a validation, and it writes the
// verdict the option means. It never grants authority: validation and authority
// are independent axes, and no amount of agreement is force.
func TestCalibrationDoesNotGrantAuthority(t *testing.T) {
	for choice, want := range map[string]ledger.Verdict{
		"right":  ledger.VerdictSupported,
		"partly": ledger.VerdictDisputed,
		"wrong":  ledger.VerdictRejected,
	} {
		t.Run(choice, func(t *testing.T) {
			f := newFixtureWith(t, nil, humanActor())
			ctx := context.Background()
			crumb := f.capture("reads state their freshness", 0.6)
			revision := f.seedInsight("freshness is stated", crumb.ID)
			ask, err := f.L.EnqueueAsk(ctx, ledger.EnqueueAsk{
				PromptKey: "calibration",
				Target:    ledger.RecordRef{Kind: ledger.KindRevision, ID: string(revision)},
			})
			if err != nil {
				t.Fatalf("enqueue: %v", err)
			}
			res, err := f.L.AnswerAsk(ctx, ledger.AnswerAsk{AskID: ask.ID, ChoiceID: choice})
			if err != nil {
				t.Fatalf("answering: %v", err)
			}
			if res.Validation == nil || res.Validation.Verdict != want {
				t.Fatalf("%s produced %+v, want %s", choice, res.Validation, want)
			}
			if res.Validation.ActorKind != ledger.ActorHuman {
				t.Fatalf("the verdict is attributed to %s", res.Validation.ActorKind)
			}
			if res.Authority != nil || f.count(`SELECT COUNT(*) FROM authorities`) != 0 {
				t.Fatal("calibration granted authority")
			}
			if res.Ask.ValidationID != res.Validation.ID {
				t.Fatalf("the ask does not join to its verdict: %+v", res.Ask)
			}
			// A tap is not a citation, and the ledger says so out loud.
			if want != ledger.VerdictSupported && !hasNotice(res.Notices, "validation_without_evidence") {
				t.Fatalf("a %s verdict cited nothing and warned about nothing: %+v", want, res.Notices)
			}
		})
	}
}

// An agent's context flush is a hypothesis. It becomes a Crumb with agent
// provenance and lower confidence, and it validates nothing: an agent answer
// must never satisfy a review of the agent's own work.
func TestContextFlushDoesNotWriteValidation(t *testing.T) {
	f := newFixture(t) // agent actor with a session
	ctx := context.Background()
	ask, err := f.L.EnqueueAsk(ctx, ledger.EnqueueAsk{PromptKey: "context-flush"})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	res, err := f.L.AnswerAsk(ctx, ledger.AnswerAsk{
		AskID: ask.ID, Text: "the lock is held for the whole command, not just the write",
	})
	if err != nil {
		t.Fatalf("answering: %v", err)
	}
	if res.Validation != nil || res.Authority != nil {
		t.Fatalf("an agent answer produced %+v / %+v", res.Validation, res.Authority)
	}
	if res.Crumb.ActorKind != ledger.ActorAgent {
		t.Fatalf("the flush Crumb is attributed to %s", res.Crumb.ActorKind)
	}
	if res.Crumb.Confidence != 0.6 {
		t.Fatalf("an agent hypothesis carries confidence %v, want 0.6", res.Crumb.Confidence)
	}
	if n := f.count(`SELECT COUNT(*) FROM validations`); n != 0 {
		t.Fatalf("%d validation(s) written", n)
	}
}

// The relay only runs one way. An agent answering an agent-track ask is the
// agent; nothing here can make it a human.
func TestAnswerAskWithAgentRespondentDoesNotWriteHumanProvenance(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	ask, err := f.L.EnqueueAsk(ctx, ledger.EnqueueAsk{PromptKey: "context-flush"})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	res, err := f.L.AnswerAsk(ctx, ledger.AnswerAsk{
		AskID: ask.ID, Text: "nothing the ledger does not already hold", RespondentID: "brian",
	})
	if err != nil {
		t.Fatalf("answering: %v", err)
	}
	if res.Crumb.ActorKind != ledger.ActorAgent || res.Crumb.ActorID == "brian" {
		t.Fatalf("--respondent-id promoted an agent answer to %+v", res.Crumb.Provenance)
	}
	if res.Ask.ViaSession != "" {
		t.Fatalf("an agent answering its own question is not a relay: via_session = %q", res.Ask.ViaSession)
	}
	// And the other direction: a human cannot sign for the agent either.
	human := f.sampler(humanActor())
	second, err := human.EnqueueAsk(ctx, ledger.EnqueueAsk{
		PromptKey: "context-flush", Respondent: ledger.PromptRespondentAgent,
	})
	if err == nil {
		if _, err := human.AnswerAsk(ctx, ledger.AnswerAsk{AskID: second.ID, Text: "on its behalf"}); err == nil {
			t.Fatal("a human answered an agent-track ask")
		}
	}
}

// Provenance.Validate already refuses an agent with no session, which is what
// makes via_session available by construction on every relay. The test pins
// that the relay path inherits that refusal rather than working around it.
func TestRelayWithoutSessionRefused(t *testing.T) {
	f := newFixtureWith(t, nil, humanActor())
	ctx := context.Background()
	f.blockedProposal(t, "policy", ledger.AuthorityAdvisory)
	ask := f.deliver(t, f.L, ledger.PromptRespondentHuman).Questions[0]

	sessionless := f.sampler(ledger.Provenance{
		ActorID: "claude", ActorKind: ledger.ActorAgent, ActorModel: "claude-opus-5",
	})
	_, err := sessionless.AnswerAsk(ctx, ledger.AnswerAsk{AskID: ask.ID, ChoiceID: "wait"})
	var le *ledger.Error
	if !errors.As(err, &le) || le.Code != "invalid_provenance" {
		t.Fatalf("a sessionless relay returned %v, want invalid_provenance", err)
	}
	if state := f.askState(t, ask.ID); state != ledger.AskDelivered {
		t.Fatalf("the refused relay moved the ask to %s", state)
	}
}

// Skipping writes no Crumb. Nobody said anything, and recording that they did
// would be the fastest way to make sampled data worthless.
func TestSkipRecordsNoCrumb(t *testing.T) {
	f := newFixtureWith(t, nil, humanActor())
	f.blockedProposal(t, "policy", ledger.AuthorityAdvisory)
	ask := f.deliver(t, f.L, ledger.PromptRespondentHuman).Questions[0]

	before := f.count(`SELECT COUNT(*) FROM crumbs`)
	res, err := f.L.SkipAsk(context.Background(), ledger.SkipAsk{AskID: ask.ID, Reason: "mid-task"})
	if err != nil {
		t.Fatalf("skipping: %v", err)
	}
	if res.State != ledger.AskSkipped || res.SkipReason != "mid-task" {
		t.Fatalf("skip recorded %+v", res)
	}
	if after := f.count(`SELECT COUNT(*) FROM crumbs`); after != before {
		t.Fatalf("a skip wrote %d Crumb(s)", after-before)
	}
	if _, err := f.L.AnswerAsk(context.Background(), ledger.AnswerAsk{
		AskID: ask.ID, ChoiceID: "wait",
	}); err == nil {
		t.Fatal("a skipped ask was answered afterwards")
	}
}

// The answer Crumb is the record of the answer, so it cannot be pruned out from
// under the Ask that says it exists. asks.crumb_id is a real foreign key, and
// prune has to report that per id rather than discover it as a transaction-wide
// violation that loses every other answer in the batch.
func TestPruneRefusesACrumbThatAnswersAnAsk(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	ask, err := f.L.EnqueueAsk(ctx, ledger.EnqueueAsk{PromptKey: "context-flush"})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	res, err := f.L.AnswerAsk(ctx, ledger.AnswerAsk{AskID: ask.ID, Text: "the lock covers the whole command"})
	if err != nil {
		t.Fatalf("answering: %v", err)
	}
	pruned, err := f.L.PruneCrumbs(ctx, ledger.PruneCrumbs{
		IDs: []ledger.CrumbID{res.Crumb.ID}, Confirmed: true,
	})
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if pruned.Pruned != 0 || len(pruned.Blocked) != 1 || pruned.Blocked[0].Code != "answers_ask" {
		t.Fatalf("prune reported %+v", pruned)
	}
}

func (f *fixture) askState(t *testing.T, id ledger.AskID) ledger.AskState {
	t.Helper()
	rows, err := f.L.Asks(context.Background(), ledger.AskQuery{IDs: []ledger.AskID{id}})
	if err != nil {
		t.Fatalf("reading ask %s: %v", id, err)
	}
	if len(rows) != 1 {
		t.Fatalf("ask %s not found", id)
	}
	return rows[0].State
}

func fixtureText(i int) string {
	return "a fragment worth keeping, number " + string(rune('a'+i))
}
