package ledger

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Sampling — asking for the judgement the ledger cannot derive.
//
// An Ask is one registered question, frozen at the moment it was minted, aimed
// at one respondent and optionally at one record. Four rules hold here:
//
//   - An answer is a Crumb. There is no second knowledge pipeline: where the
//     prompt names a revision or a proposal the answer also appends a
//     validation or a capped grant, and asks.crumb_id is the join back. Nothing
//     stores the answer twice.
//   - The process actor is the transport; the respondent is the author. An
//     agent relaying a human's reply writes a human Crumb and stays on the Ask
//     row as actor_* plus via_session. It never becomes the human.
//   - A relayed tap is not a signature. `grant-default` may append a working
//     default and nothing stronger: never mandatory, never on a `policy`-class
//     proposal, and never a terminal `promote reject`. ck_aut_mandatory_human
//     cannot backstop this — the relayed row is stamped human — so the caps in
//     AnswerAsk are the only gate, and the tests that pin them are named so a
//     later phase cannot quietly simplify them away.
//   - Skipping is free and costs nothing. An unanswered question expires; it
//     never blocks, never retries harder, and never becomes a record.
//
// Every operation reads, then prepares (which is where redaction runs), then
// writes. Preparation outside the transaction is what keeps an unresolvable
// finding from ever touching the journal; re-reading the row inside the write
// is what keeps two clones from answering the same ask twice.

const (
	// maxDeliverBatch bounds one presentation. It matches the structured
	// question surfaces a harness offers — one to four options, one to four
	// questions — and it is a constant rather than configuration because a
	// repository that could raise it would be a repository that could nag.
	maxDeliverBatch = 4

	// Answer confidence is fixed by track rather than flagged. A human tap is a
	// deliberate judgement; an agent's context flush is a hypothesis, and the
	// number has to say so or a later harvest will weigh them the same.
	humanAnswerConfidence = 0.9
	agentAnswerConfidence = 0.6

	// defaultRespondentID names a relayed human who did not give a name. It is
	// a literal rather than the process actor's id: attributing the answer to
	// the agent would be the exact laundering this file exists to prevent.
	defaultRespondentID = "human"

	maxSkipReasonChars = 255
)

// promptTargetKinds constrains the seeded keys whose answers materialise more
// than a Crumb. authority-nudge grants on a proposal and calibration judges a
// revision, so an ask minted against anything else is a question whose answer
// could not be applied — better refused at enqueue than half-applied later.
var promptTargetKinds = map[string][]RecordKind{
	PromptAuthorityNudge: {KindProposal},
	PromptCalibration:    {KindRevision},
}

// askOpenStates are the states an ask can still be answered or delivered in.
var askOpenStates = []AskState{AskPending, AskDelivered}

// Ask is one question put to one respondent. The provenance quartet is the
// process that minted it and is never rewritten; the answer's own provenance
// lives on the Crumb, and via_session is how a relay is reconstructed.
//
// Absent optional values are omitted rather than emitted empty, as on Crumb.
// The three that are not — target, latency_ms, and the two timestamps — render
// as null, because `omitempty` does not apply to a struct or a pointer and null
// is the honest shape for "this has not happened yet".
type Ask struct {
	ID               AskID            `json:"id"`
	PromptID         PromptID         `json:"prompt_id"`
	PromptKey        string           `json:"prompt_key"`
	PromptVersion    int              `json:"prompt_version"`
	Respondent       PromptRespondent `json:"respondent"`
	Target           RecordRef        `json:"target"`
	State            AskState         `json:"state"`
	QuestionSnapshot string           `json:"question_snapshot"`
	Options          []AskOption      `json:"options,omitempty"`
	EnqueueSessionID string           `json:"enqueue_session_id,omitempty"`
	ViaSession       string           `json:"via_session,omitempty"`
	CrumbID          CrumbID          `json:"crumb_id,omitempty"`
	ValidationID     ValidationID     `json:"validation_id,omitempty"`
	AuthorityID      AuthorityID      `json:"authority_id,omitempty"`
	ChoiceID         string           `json:"choice_id,omitempty"`
	AnswerText       string           `json:"answer_text,omitempty"`
	SkipReason       string           `json:"skip_reason,omitempty"`
	LatencyMS        *int             `json:"latency_ms"`
	CreatedAt        time.Time        `json:"created_at"`
	DeliveredAt      *time.Time       `json:"delivered_at"`
	ResolvedAt       *time.Time       `json:"resolved_at"`
	ExpiresAt        time.Time        `json:"expires_at"`
	Provenance
}

// Open reports whether the Ask can still be delivered or answered.
func (a Ask) Open() bool { return slices.Contains(askOpenStates, a.State) }

// AskQuestion is one delivered question as a respondent sees it. It is a
// separate shape from Ask because delivery publishes the question and nothing
// about who minted it or what an answer later produced.
type AskQuestion struct {
	ID         AskID            `json:"id"`
	PromptKey  string           `json:"prompt_key"`
	Respondent PromptRespondent `json:"respondent"`
	Question   string           `json:"question"`
	AnswerKind AnswerKind       `json:"answer_kind"`
	Options    []AskOption      `json:"options,omitempty"`
	Target     RecordRef        `json:"target"`
	ExpiresAt  time.Time        `json:"expires_at"`
}

// EnqueueAsk mints one ask. Respondent is optional: an unset one follows the
// process actor, which is what makes `bdc ask enqueue --prompt context-flush`
// mean the obvious thing inside an agent session.
type EnqueueAsk struct {
	PromptKey  string
	Target     RecordRef
	Respondent PromptRespondent
}

// DeliverQuery selects whose queue to present.
type DeliverQuery struct {
	Respondent PromptRespondent
}

// DeliverResult is `{questions[]}`. An empty list is success: no pending
// question is a state, not an error.
type DeliverResult struct {
	Questions []AskQuestion `json:"questions"`
}

// AnswerAsk resolves one ask. Exactly one of ChoiceID and Text carries the
// answer, decided by the prompt's answer kind.
type AnswerAsk struct {
	AskID        AskID
	ChoiceID     string
	Text         string
	Note         string
	RespondentID string
}

// AnswerResult is `{ask, crumb, validation, authority}`. The two judgement
// records are pointers so an answer that produced neither says so with null
// rather than with a zero-valued row that reads like one.
type AnswerResult struct {
	Ask        Ask         `json:"ask"`
	Crumb      Crumb       `json:"crumb"`
	Validation *Validation `json:"validation"`
	Authority  *Authority  `json:"authority"`
	Findings   []Finding   `json:"-"`
	Notices    []Notice    `json:"-"`
}

// SkipAsk declines one ask. Skipping is first-class: a question that cannot be
// waved away is a question that will be answered carelessly.
type SkipAsk struct {
	AskID  AskID
	Reason string
}

// EnqueueAsk resolves the active prompt, renders the question against its
// target, and mints one open ask. Rendering happens here and is frozen: a
// prompt revised tomorrow does not retroactively change what was asked today.
func (l *Ledger) EnqueueAsk(ctx context.Context, c EnqueueAsk) (Ask, error) {
	if err := l.actor.Validate(); err != nil {
		return Ask{}, err
	}
	respondent, err := l.resolveRespondent(c.Respondent)
	if err != nil {
		return Ask{}, err
	}

	var prepared Ask
	if err := l.store.Read(ctx, func(snap Snapshot) error {
		prepared, err = l.prepareAsk(snap, c.PromptKey, c.Target, respondent)
		return err
	}); err != nil {
		return Ask{}, err
	}

	if err := l.store.Write(ctx, func(tx Tx) error {
		open, err := openAskExists(tx, prepared)
		if err != nil {
			return err
		}
		if open {
			return errAskAlreadyOpen(prepared)
		}
		return tx.InsertAsk(prepared)
	}); err != nil {
		return Ask{}, err
	}
	return prepared, nil
}

// prepareAsk builds one ask without writing it: resolve the prompt, render and
// redact the snapshot, freeze the options, and answer the open-ask invariant
// against the snapshot it was given. The caller re-asks that last question
// inside the transaction, because a concurrent enqueue can slip between them.
func (l *Ledger) prepareAsk(snap Snapshot, key string, target RecordRef, respondent PromptRespondent) (Ask, error) {
	prompt, err := activePrompt(snap, key)
	if err != nil {
		return Ask{}, err
	}
	if prompt.Respondent != PromptRespondentBoth && prompt.Respondent != respondent {
		return Ask{}, Fail(ErrInvalidInput, "invalid_ask",
			"prompt %s is a %s-track question and cannot be asked of a %s",
			prompt.Key, prompt.Respondent, respondent)
	}

	facts := map[string]string{}
	switch {
	case !target.Zero():
		if kinds, ok := promptTargetKinds[prompt.Key]; ok && !slices.Contains(kinds, target.Kind) {
			return Ask{}, Fail(ErrInvalidInput, "invalid_ask",
				"prompt %s asks about %s, not %s", prompt.Key, joinNames(kinds), target.Kind)
		}
		if facts, err = targetFacts(snap, target); err != nil {
			return Ask{}, err
		}
	case templateNeedsTarget(prompt.QuestionTemplate):
		return Ask{}, Fail(ErrInvalidInput, "invalid_ask",
			"prompt %s asks about a record; pass --target", prompt.Key)
	case l.actor.SessionID == "":
		// A session-scoped ask keyed on nothing would be minted once per
		// invocation forever. Refusing is honest: the uniqueness rule has no
		// value to key on.
		return Ask{}, Fail(ErrInvalidInput, "invalid_ask",
			"prompt %s is session-scoped and this invocation carries no session id", prompt.Key)
	}

	question, _, err := l.redactField("question", renderTemplate(prompt.QuestionTemplate, facts))
	if err != nil {
		return Ask{}, err
	}
	l.assertRedacted("asks.question_snapshot", question)

	options := make([]AskOption, 0, len(prompt.Options))
	for _, o := range prompt.Options {
		label, _, err := l.redactField("option label", o.Label)
		if err != nil {
			return Ask{}, err
		}
		l.assertRedacted("asks.options_snapshot", label)
		options = append(options, AskOption{ID: o.ID, Label: label})
	}

	now := l.clock()
	return Ask{
		ID: NewAskID(), PromptID: prompt.ID, PromptKey: prompt.Key, PromptVersion: prompt.Version,
		Respondent: respondent, Target: target, State: AskPending,
		QuestionSnapshot: question, Options: options,
		EnqueueSessionID: l.actor.SessionID,
		CreatedAt:        now, ExpiresAt: now.Add(l.config.AskExpireAfter),
		Provenance: l.actor,
	}, nil
}

// DeliverAsks presents the oldest open questions for one respondent and, for a
// human, first services the dead letters: a proposal blocked on a human
// authority grant is exactly a question the ledger cannot answer for itself.
//
// It also sweeps. Expiry is lazy — there is no daemon — so a deliver is the
// only moment a stale ask becomes `expired`, and it is also where two clones
// that each opened an ask for the same target are reconciled: present the
// oldest and expire its siblings.
func (l *Ledger) DeliverAsks(ctx context.Context, q DeliverQuery) (DeliverResult, error) {
	if err := l.actor.Validate(); err != nil {
		return DeliverResult{}, err
	}
	respondent, err := l.resolveRespondent(q.Respondent)
	if err != nil {
		return DeliverResult{}, err
	}

	var nudges []Ask
	if respondent == PromptRespondentHuman {
		if err := l.store.Read(ctx, func(snap Snapshot) error {
			nudges, err = l.prepareNudges(snap)
			return err
		}); err != nil {
			return DeliverResult{}, err
		}
	}

	out := DeliverResult{Questions: []AskQuestion{}}
	err = l.store.Write(ctx, func(tx Tx) error {
		out.Questions = []AskQuestion{}
		now := l.clock()
		if err := expireLapsed(tx, now); err != nil {
			return err
		}
		for _, nudge := range nudges {
			open, err := openAskExists(tx, nudge)
			if err != nil {
				return err
			}
			if open {
				continue
			}
			if err := tx.InsertAsk(nudge); err != nil {
				return err
			}
		}

		pending, err := tx.Asks(AskQuery{States: []AskState{AskPending}, Respondent: respondent})
		if err != nil {
			return err
		}
		sortAsksOldestFirst(pending)

		// A Dolt merge can leave two clones' asks for one target side by side.
		// Neither is wrong and neither can be reconciled by id, so the oldest
		// is the question and the rest expire — presenting both would ask the
		// same thing twice and record two answers to one decision.
		seen := map[string]bool{}
		var present []Ask
		for _, a := range pending {
			key := openAskKey(a)
			if seen[key] {
				a.State = AskExpired
				a.ResolvedAt = &now
				if err := tx.UpdateAsk(a); err != nil {
					return err
				}
				continue
			}
			seen[key] = true
			present = append(present, a)
		}

		limit := maxDeliverBatch
		if n := l.config.AskMaxPerDeliver; n > 0 && n < limit {
			limit = n
		}
		if len(present) > limit {
			present = present[:limit]
		}
		for _, a := range present {
			a.State = AskDelivered
			a.DeliveredAt = &now
			if err := tx.UpdateAsk(a); err != nil {
				return err
			}
			question, err := askQuestion(tx, a)
			if err != nil {
				return err
			}
			out.Questions = append(out.Questions, question)
		}
		return nil
	})
	if err != nil {
		return DeliverResult{}, err
	}
	return out, nil
}

// prepareNudges builds one authority-nudge per proposal that `bdc context`
// would report as authority_required. A proposal that cannot be prepared is
// skipped rather than failing the deliver: the queue is a convenience, and one
// unrenderable target must not cost a respondent every other question.
func (l *Ledger) prepareNudges(snap Snapshot) ([]Ask, error) {
	if _, err := activePrompt(snap, PromptAuthorityNudge); err != nil {
		// A repository that disabled the nudge is a repository that said stop.
		return nil, nil
	}
	promotions, err := narrativePromotions(snap, PromotionQuery{})
	if err != nil {
		return nil, err
	}
	var out []Ask
	for _, p := range promotions {
		if p.AuthorityRequired != RequireHuman || p.AuthorityHeld || !isOpen(p.Status) {
			continue
		}
		target := RecordRef{Kind: KindProposal, ID: string(p.ID)}
		ask, err := l.prepareAsk(snap, PromptAuthorityNudge, target, PromptRespondentHuman)
		if err != nil {
			continue
		}
		asked, err := nudgeAlreadyPut(snap, ask)
		if err != nil {
			return nil, err
		}
		if !asked {
			out = append(out, ask)
		}
	}
	return out, nil
}

// nudgeAlreadyPut is why deliver cannot nag. A proposal whose nudge was
// answered — including answered "keep waiting" — or skipped has had the
// question put to a human, and putting it again every session is how a
// skippable surface becomes one people stop reading. Only an *expired* nudge is
// re-minted: nobody engaged with that one, so it was never really asked.
//
// An explicit `bdc ask enqueue` is deliberate and keeps the looser rule: it
// only refuses while an ask is still open.
func nudgeAlreadyPut(snap Snapshot, candidate Ask) (bool, error) {
	rows, err := snap.Asks(AskQuery{
		PromptKeys: []string{candidate.PromptKey},
		TargetID:   candidate.Target.ID,
		Respondent: candidate.Respondent,
	})
	if err != nil {
		return false, err
	}
	for _, a := range rows {
		if a.State != AskExpired {
			return true, nil
		}
	}
	return false, nil
}

// AnswerAsk records one answer as the respondent's own records, in one
// transaction. Nothing here calls CaptureCrumb, RecordValidation, or
// GrantAuthority: each stamps the process actor and opens its own transaction,
// and a half-written answer — the Crumb committed, the grant rolled back — is
// not a thing an append-only ledger can represent.
func (l *Ledger) AnswerAsk(ctx context.Context, c AnswerAsk) (AnswerResult, error) {
	if err := l.actor.Validate(); err != nil {
		return AnswerResult{}, err
	}
	ask, err := l.openAsk(ctx, c.AskID)
	if err != nil {
		return AnswerResult{}, err
	}

	respondent, err := l.respondentProvenance(ask, c.RespondentID)
	if err != nil {
		return AnswerResult{}, err
	}
	// The answer is redacted once, here, and the redacted form is what both the
	// Crumb and asks.answer_text hold. Storing the raw text in the column while
	// redacting the Crumb would put the secret in the ledger by the back door.
	text, findings, err := l.redactField("answer", strings.TrimSpace(c.Text))
	if err != nil {
		return AnswerResult{}, err
	}
	l.assertRedacted("asks.answer_text", text)
	answer, choice, err := resolveAnswer(ask, strings.TrimSpace(c.ChoiceID), text)
	if err != nil {
		return AnswerResult{}, err
	}

	confidence := humanAnswerConfidence
	if ask.Respondent == PromptRespondentAgent {
		confidence = agentAnswerConfidence
	}
	crumb, more, err := l.prepareCrumbAs(CaptureCrumb{
		Content:    answerContent(ask, answer, c.Note),
		Confidence: confidence,
	}, respondent)
	if err != nil {
		return AnswerResult{}, err
	}

	out := AnswerResult{Findings: append(findings, more...)}
	validation, extra, notices, err := l.prepareAnswerValidation(ask, choice, c.Note, respondent)
	if err != nil {
		return AnswerResult{}, err
	}
	out.Findings = append(out.Findings, extra...)
	out.Notices = append(out.Notices, notices...)

	authority, extra, notices, err := l.prepareAnswerAuthority(ctx, ask, choice, respondent)
	if err != nil {
		return AnswerResult{}, err
	}
	out.Findings = append(out.Findings, extra...)
	out.Notices = append(out.Notices, notices...)

	err = l.store.Write(ctx, func(tx Tx) error {
		now := l.clock()
		cur, err := loadAsk(tx, ask.ID)
		if err != nil {
			return err
		}
		if !cur.Open() {
			return errAskNotOpen(cur)
		}

		stored := crumb
		existing, duplicate, err := sessionDuplicate(tx, crumb.ContentHash, crumb.SessionID)
		if err != nil {
			return err
		}
		if duplicate {
			// The same answer already has a Crumb in this session. Linking it
			// is the honest record: two asks sharing a crumb_id still record
			// two answers, because the ask rows are what distinguish them.
			stored = existing
		} else if err := insertCrumb(tx, crumb, nil); err != nil {
			return err
		}

		if validation != nil {
			if err := appendValidation(tx, *validation, nil); err != nil {
				return err
			}
			cur.ValidationID = validation.ID
		}
		if authority != nil {
			if err := appendAuthority(tx, *authority); err != nil {
				return err
			}
			cur.AuthorityID = authority.ID
		}

		cur.State = AskAnswered
		cur.ResolvedAt = &now
		cur.ChoiceID = choice
		cur.AnswerText = text
		cur.CrumbID = stored.ID
		if l.actor.ActorKind == ActorAgent && ask.Respondent == PromptRespondentHuman {
			cur.ViaSession = l.actor.SessionID
		}
		if cur.DeliveredAt != nil {
			ms := int(now.Sub(*cur.DeliveredAt).Milliseconds())
			if ms < 0 {
				ms = 0
			}
			cur.LatencyMS = &ms
		}
		if err := tx.UpdateAsk(cur); err != nil {
			return err
		}
		out.Ask, out.Crumb = cur, stored
		out.Validation, out.Authority = validation, authority
		return nil
	})
	if err != nil {
		return AnswerResult{}, err
	}
	return out, nil
}

// SkipAsk declines one ask and writes nothing else. A skip is data — it is how
// habituation becomes visible — but it is not a Crumb: nobody said anything.
func (l *Ledger) SkipAsk(ctx context.Context, c SkipAsk) (Ask, error) {
	if err := l.actor.Validate(); err != nil {
		return Ask{}, err
	}
	ask, err := l.openAsk(ctx, c.AskID)
	if err != nil {
		return Ask{}, err
	}
	reason := strings.TrimSpace(c.Reason)
	if len(reason) > maxSkipReasonChars {
		return Ask{}, Fail(ErrInvalidInput, "invalid_content",
			"a skip reason is at most %d characters", maxSkipReasonChars)
	}
	reason, _, err = l.redactField("skip reason", reason)
	if err != nil {
		return Ask{}, err
	}
	l.assertRedacted("asks.skip_reason", reason)

	var out Ask
	err = l.store.Write(ctx, func(tx Tx) error {
		now := l.clock()
		cur, err := loadAsk(tx, ask.ID)
		if err != nil {
			return err
		}
		if !cur.Open() {
			return errAskNotOpen(cur)
		}
		cur.State = AskSkipped
		cur.SkipReason = reason
		cur.ResolvedAt = &now
		if cur.DeliveredAt != nil {
			ms := int(now.Sub(*cur.DeliveredAt).Milliseconds())
			if ms < 0 {
				ms = 0
			}
			cur.LatencyMS = &ms
		}
		if err := tx.UpdateAsk(cur); err != nil {
			return err
		}
		out = cur
		return nil
	})
	if err != nil {
		return Ask{}, err
	}
	return out, nil
}

// Asks is the read `bdc context` uses to surface pending human questions.
func (l *Ledger) Asks(ctx context.Context, q AskQuery) ([]Ask, error) {
	var out []Ask
	err := l.store.Read(ctx, func(snap Snapshot) error {
		rows, err := snap.Asks(q)
		out = rows
		return err
	})
	if err != nil {
		return nil, err
	}
	if out == nil {
		out = []Ask{}
	}
	return out, nil
}

// openAsk loads one ask and enforces expiry on the way past. A lapsed ask that
// no deliver has swept is expired here — recorded, then refused — because lazy
// expiry that only ever runs on deliver would leave a stale question answerable
// forever by anyone who kept its id.
func (l *Ledger) openAsk(ctx context.Context, id AskID) (Ask, error) {
	parsed, err := ParseID(PrefixAsk, string(id))
	if err != nil {
		return Ask{}, err
	}
	var ask Ask
	if err := l.store.Read(ctx, func(snap Snapshot) error {
		ask, err = loadAsk(snap, AskID(parsed))
		return err
	}); err != nil {
		return Ask{}, err
	}
	if !ask.Open() {
		return Ask{}, errAskNotOpen(ask)
	}
	if !lapsed(ask, l.clock()) {
		return ask, nil
	}
	if err := l.store.Write(ctx, func(tx Tx) error {
		return expireLapsed(tx, l.clock())
	}); err != nil {
		return Ask{}, err
	}
	ask.State = AskExpired
	return Ask{}, errAskNotOpen(ask)
}

// prepareAnswerValidation is calibration's half: a tap on a conclusion is a
// verdict about it. The verdicts are the three the question offers and nothing
// else — a sampled answer never supersedes, because supersession names the
// record that replaced this one and a tap names nothing.
func (l *Ledger) prepareAnswerValidation(ask Ask, choice, note string, actor Provenance) (*Validation, []Finding, []Notice, error) {
	if ask.PromptKey != PromptCalibration {
		return nil, nil, nil, nil
	}
	var verdict Verdict
	switch choice {
	case "right":
		verdict = VerdictSupported
	case "partly":
		verdict = VerdictDisputed
	case "wrong":
		verdict = VerdictRejected
	default:
		return nil, nil, nil, Fail(ErrInvalidInput, "invalid_ask",
			"%q is not a calibration answer; expected right, partly, or wrong", choice)
	}
	rationale := "sampled calibration"
	if note = strings.TrimSpace(note); note != "" {
		rationale += ": " + note
	}
	// The evidence notice on a negative verdict is expected and honest: a tap
	// is not a citation, and the caller should see that the ledger knows it.
	validation, findings, notices, err := l.prepareValidationAs(RecordValidation{
		Target: ask.Target, Verdict: verdict, Rationale: rationale,
	}, actor)
	if err != nil {
		return nil, nil, nil, err
	}
	return &validation, findings, notices, nil
}

// prepareAnswerAuthority is authority-nudge's half, and the capped one.
//
// `grant-default` appends an unscoped repository-wide working default and
// nothing else. Unscoped because humanAuthorityHeld ignores a scoped grant, and
// unblocking the proposal is the entire reason the question was asked; a scoped
// grant would answer the question and leave it blocked.
//
// The two refusals are exact: a `policy`-class proposal and a proposal that
// requested `mandatory`. They are checked directly rather than through
// AuthorityRequiredFor, because that function answers "does this need a human",
// and a relayed human is a human by that measure — which is the whole problem.
func (l *Ledger) prepareAnswerAuthority(ctx context.Context, ask Ask, choice string, actor Provenance) (*Authority, []Finding, []Notice, error) {
	if ask.PromptKey != PromptAuthorityNudge {
		return nil, nil, nil, nil
	}
	switch choice {
	case "wait":
		return nil, nil, nil, nil
	case "reject":
		// Recommending rejection is not rejecting. The seeded option says
		// "Recommend rejection" for exactly this reason: a question must not
		// promise an action its answer does not perform.
		return nil, nil, []Notice{{
			Code: "ask_reject_not_applied",
			Message: fmt.Sprintf("the recommendation was recorded; close the proposal with "+
				"`bdc promote reject %s --rationale ...`", ask.Target.ID),
		}}, nil
	case "grant-default":
	default:
		return nil, nil, nil, Fail(ErrInvalidInput, "invalid_ask",
			"%q is not an authority-nudge answer; expected grant-default, reject, or wait", choice)
	}

	var proposal Proposal
	if err := l.store.Read(ctx, func(snap Snapshot) error {
		rows, err := snap.Proposals(PromotionQuery{IDs: []ProposalID{ProposalID(ask.Target.ID)}})
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return NotFound("promotion proposal", ask.Target.ID)
		}
		proposal = rows[0]
		return nil
	}); err != nil {
		return nil, nil, nil, err
	}

	if proposal.Class == "policy" || proposal.RequestedAuthority == AuthorityMandatory {
		return nil, nil, []Notice{{
			Code: "ask_grant_capped",
			Message: fmt.Sprintf("a sampled answer cannot establish this: %s asks for %s force on a "+
				"%s-class proposal; a human grants it directly with `bdc authority %s --level ... "+
				"--rationale ...`",
				proposal.ID, proposal.RequestedAuthority, proposal.Class, proposal.ID),
		}}, nil
	}

	grant, findings, err := l.prepareAuthorityAs(GrantAuthority{
		Target:    ask.Target,
		Level:     AuthorityDefault,
		Rationale: "sampled authority-nudge",
	}, actor)
	if err != nil {
		return nil, nil, nil, err
	}
	return &grant, findings, nil, nil
}

// respondentProvenance decides whose record the answer is. Three cases, and the
// third is the one this whole file is shaped around: an agent answering a
// human-track ask is relaying, so the row is the human's and the agent's
// session travels on the Ask as via_session.
func (l *Ledger) respondentProvenance(ask Ask, respondentID string) (Provenance, error) {
	if ask.Respondent == PromptRespondentAgent {
		if l.actor.ActorKind != ActorAgent {
			return Provenance{}, Fail(ErrInvalidInput, "invalid_ask",
				"ask %s is an agent-track question; a human cannot answer it on the agent's behalf", ask.ID)
		}
		return l.actor, nil
	}
	if l.actor.ActorKind == ActorHuman {
		return l.actor, nil
	}
	// Relay. Provenance.Validate has already refused an agent with no session,
	// so via_session is available by construction. The human carries no model
	// and no session: inventing one would fabricate a run that never happened.
	id := strings.TrimSpace(respondentID)
	if id == "" {
		id = defaultRespondentID
	}
	return Provenance{ActorID: id, ActorKind: ActorHuman, Harness: l.actor.Harness}, nil
}

// resolveAnswer reads the answer out of the intent and returns the text a
// reader will see plus the chosen option id. A choice accepts the option id or
// the 1-based index as printed, because a respondent replying "2" to a numbered
// list is answering the question.
func resolveAnswer(ask Ask, raw, text string) (answer, choice string, err error) {
	if len(ask.Options) > 0 {
		switch {
		case raw == "" && text == "":
			return "", "", Fail(ErrInvalidInput, "invalid_usage",
				"ask %s offers options; answer it with --choice", ask.ID)
		case raw == "":
			return "", "", Fail(ErrInvalidInput, "invalid_usage",
				"ask %s offers options; --text does not answer it", ask.ID)
		case text != "":
			return "", "", Fail(ErrInvalidInput, "invalid_usage",
				"pass --choice or --text, not both")
		}
		option, err := selectOption(ask.Options, raw)
		if err != nil {
			return "", "", err
		}
		return option.Label, option.ID, nil
	}
	switch {
	case raw != "" && text != "":
		return "", "", Fail(ErrInvalidInput, "invalid_usage", "pass --choice or --text, not both")
	case raw != "":
		return "", "", Fail(ErrInvalidInput, "invalid_usage",
			"ask %s is a free-text question; answer it with --text", ask.ID)
	case text == "":
		// An empty answer is a skip that did not say so. Saying so is better
		// data: `skipped` and `answered with nothing` are different facts.
		return "", "", Fail(ErrInvalidInput, "invalid_content",
			"an answer needs text; decline it with `bdc ask skip %s` instead", ask.ID)
	}
	return text, "", nil
}

func selectOption(options []AskOption, raw string) (AskOption, error) {
	for _, o := range options {
		if o.ID == raw {
			return o, nil
		}
	}
	if n, err := strconv.Atoi(raw); err == nil {
		if n < 1 || n > len(options) {
			return AskOption{}, Fail(ErrInvalidInput, "invalid_usage",
				"option %d is out of range; this question offers %d", n, len(options))
		}
		return options[n-1], nil
	}
	ids := make([]string, len(options))
	for i, o := range options {
		ids[i] = o.ID
	}
	return AskOption{}, Fail(ErrInvalidInput, "invalid_ask",
		"%q is not an option; expected one of %s, or a number from 1 to %d",
		raw, strings.Join(ids, ", "), len(options))
}

// answerContent is the Crumb a sampled answer becomes. The shape is fixed so a
// later harvest can read it without parsing anything, and the question is
// quoted as an excerpt rather than in full: the ask row is the audit home of
// the whole snapshot, and a size refusal must never be able to destroy an
// answer that has already been given.
func answerContent(ask Ask, answer, note string) string {
	body := fmt.Sprintf("[ask %s] %s\n→ %s",
		ask.PromptKey, excerpt(ask.QuestionSnapshot, maxExcerpt), strings.TrimSpace(answer))
	if note = strings.TrimSpace(note); note != "" {
		body += "\n\n" + note
	}
	return body
}

// resolveRespondent narrows `human`/`agent`/unset to the one an ask carries. An
// ask is never `both`: `both` says who may be asked, not who was.
func (l *Ledger) resolveRespondent(r PromptRespondent) (PromptRespondent, error) {
	switch r {
	case PromptRespondentHuman, PromptRespondentAgent:
		return r, nil
	case "":
		if l.actor.ActorKind == ActorAgent {
			return PromptRespondentAgent, nil
		}
		return PromptRespondentHuman, nil
	default:
		return "", Fail(ErrInvalidInput, "invalid_ask",
			"a respondent is human or agent, got %q", r)
	}
}

// openAskKey is the tuple open-ask uniqueness is enforced on. A targeted ask is
// keyed on its target and not on a session, so a blocked proposal is nudged
// once however many sessions pass through it; a session-scoped ask is keyed on
// the session that opened it, because that is the only thing it is about.
func openAskKey(a Ask) string {
	scope := a.Target.ID
	if a.Target.Zero() {
		scope = "session:" + a.EnqueueSessionID
	}
	return a.PromptKey + "\x1f" + scope + "\x1f" + string(a.Respondent)
}

func openAskExists(snap Snapshot, candidate Ask) (bool, error) {
	q := AskQuery{
		States:     askOpenStates,
		Respondent: candidate.Respondent,
		PromptKeys: []string{candidate.PromptKey},
	}
	if candidate.Target.Zero() {
		q.SessionID = candidate.EnqueueSessionID
	} else {
		q.TargetID = candidate.Target.ID
	}
	rows, err := snap.Asks(q)
	if err != nil {
		return false, err
	}
	want := openAskKey(candidate)
	for _, a := range rows {
		if a.ID != candidate.ID && openAskKey(a) == want {
			return true, nil
		}
	}
	return false, nil
}

// expireLapsed sweeps every open ask past its expiry. It is called from deliver
// and from the answer path, which are the only two moments anything looks at
// the queue: there is no daemon, and inventing one would make a skippable
// surface into a background process.
func expireLapsed(tx Tx, now time.Time) error {
	open, err := tx.Asks(AskQuery{States: askOpenStates})
	if err != nil {
		return err
	}
	for _, a := range open {
		if !lapsed(a, now) {
			continue
		}
		a.State = AskExpired
		resolved := now
		a.ResolvedAt = &resolved
		if err := tx.UpdateAsk(a); err != nil {
			return err
		}
	}
	return nil
}

func lapsed(a Ask, now time.Time) bool {
	return !a.ExpiresAt.IsZero() && !now.Before(a.ExpiresAt)
}

func loadAsk(snap Snapshot, id AskID) (Ask, error) {
	rows, err := snap.Asks(AskQuery{IDs: []AskID{id}})
	if err != nil {
		return Ask{}, err
	}
	if len(rows) == 0 {
		return Ask{}, NotFound("ask", string(id))
	}
	return rows[0], nil
}

// askQuestion renders one delivered ask. The answer kind comes from the prompt
// rather than from the ask, because the ask froze the words and the options and
// an answer kind is neither.
func askQuestion(snap Snapshot, a Ask) (AskQuestion, error) {
	kind := AnswerKindShortText
	if len(a.Options) > 0 {
		kind = AnswerKindChoice
	}
	rows, err := snap.Prompts(PromptQuery{IDs: []PromptID{a.PromptID}})
	if err != nil {
		return AskQuestion{}, err
	}
	if len(rows) > 0 {
		kind = rows[0].AnswerKind
	}
	return AskQuestion{
		ID: a.ID, PromptKey: a.PromptKey, Respondent: a.Respondent,
		Question: a.QuestionSnapshot, AnswerKind: kind, Options: a.Options,
		Target: a.Target, ExpiresAt: a.ExpiresAt,
	}, nil
}

// targetFacts are the substitutions a template may name. Every kind offers the
// same three, so a new prompt can quote a record without a new code path.
func targetFacts(snap Snapshot, ref RecordRef) (map[string]string, error) {
	facts := map[string]string{"target": ref.ID}
	switch ref.Kind {
	case KindCrumb:
		rows, err := snap.Crumbs(CrumbQuery{IDs: []CrumbID{CrumbID(ref.ID)}})
		if err != nil {
			return nil, err
		}
		if len(rows) == 0 {
			return nil, NotFound("crumb", ref.ID)
		}
		facts["confidence"] = fmt.Sprintf("%.2f", rows[0].Confidence)
		facts["excerpt"] = excerpt(rows[0].Content, maxExcerpt)
	case KindRevision:
		revisions, err := snap.Revisions()
		if err != nil {
			return nil, err
		}
		found := false
		for _, rev := range revisions {
			if string(rev.ID) != ref.ID {
				continue
			}
			facts["confidence"] = fmt.Sprintf("%.2f", rev.Confidence)
			facts["excerpt"] = excerpt(rev.Content, maxExcerpt)
			found = true
			break
		}
		if !found {
			return nil, NotFound("insight revision", ref.ID)
		}
	case KindProposal:
		rows, err := snap.Proposals(PromotionQuery{IDs: []ProposalID{ProposalID(ref.ID)}})
		if err != nil {
			return nil, err
		}
		if len(rows) == 0 {
			return nil, NotFound("promotion proposal", ref.ID)
		}
		facts["confidence"] = fmt.Sprintf("%.2f", rows[0].Confidence)
		facts["excerpt"] = excerpt(rows[0].Content, maxExcerpt)
	default:
		return nil, Fail(ErrInvalidInput, "invalid_ask",
			"%q is not a record an ask can be about", ref.Kind)
	}
	return facts, nil
}

// templatePlaceholders is the closed set a question template may substitute.
// Closed on purpose: a template that could name any column would be a template
// that could quote a secret the redactor never saw in that position.
var templatePlaceholders = []string{"target", "confidence", "excerpt"}

func templateNeedsTarget(template string) bool {
	for _, name := range templatePlaceholders {
		if strings.Contains(template, "{"+name+"}") {
			return true
		}
	}
	return false
}

func renderTemplate(template string, facts map[string]string) string {
	out := template
	for _, name := range templatePlaceholders {
		out = strings.ReplaceAll(out, "{"+name+"}", facts[name])
	}
	return strings.TrimSpace(out)
}

func sortAsksOldestFirst(asks []Ask) {
	sort.Slice(asks, func(i, j int) bool {
		if asks[i].CreatedAt.Equal(asks[j].CreatedAt) {
			return asks[i].ID < asks[j].ID
		}
		return asks[i].CreatedAt.Before(asks[j].CreatedAt)
	})
}

func errAskAlreadyOpen(a Ask) error {
	return Fail(ErrInvalidInput, "ask_already_open",
		"%s is already open for this target and respondent; answer or skip it first", a.PromptKey).
		WithDetails(map[string]any{"prompt_key": a.PromptKey, "respondent": string(a.Respondent)})
}

func errAskNotOpen(a Ask) error {
	return Fail(ErrInvalidInput, "invalid_ask",
		"ask %s is %s and can no longer be answered", a.ID, a.State).
		WithDetails(map[string]any{"state": string(a.State)})
}
