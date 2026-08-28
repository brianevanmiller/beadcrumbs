package ledger

import (
	"context"
	"slices"
	"strings"
)

// Authority — the force axis.
//
// An authority grant says what an actor's decision establishes for a scope or a
// destination. Like validation it is append-only: granting appends a row, the
// current level is the latest row, and absence means advisory.
//
// The rule this file exists to hold is that only a human may grant mandatory.
// An agent that asks for it gets a typed error and is never silently downgraded
// to advisory — a downgrade would leave the agent believing it had established
// something binding. Who may grant what lives in policy.go, because the
// promotion gate asks the same question from the other side.

var authorityLevels = []AuthorityLevel{AuthorityAdvisory, AuthorityDefault, AuthorityMandatory}

// authorityTargets are the record kinds a grant applies to, matching
// authorities.target_kind. Crumbs are excluded because a fragment carries no
// force of its own: authority attaches to what was concluded from it.
var authorityTargets = []RecordKind{KindRevision, KindProposal}

// GrantAuthority is `bdc authority <target-id>`. Scope and Destination are
// independent narrowings: an empty scope is repository-wide, and an empty
// destination means the grant is not tied to one place.
type GrantAuthority struct {
	Target             RecordRef
	Level              AuthorityLevel
	Scope              string
	DestinationKind    string
	DestinationLocator string
	Rationale          string
}

// AuthorityResult is the `{authority, effective_level}` the CLI contract
// promises. EffectiveLevel is read back after the append rather than assumed to
// be the level just written: the current level is the latest row, and only the
// store can say which row that is.
type AuthorityResult struct {
	Authority      Authority      `json:"authority"`
	EffectiveLevel AuthorityLevel `json:"effective_level"`
	Findings       []Finding      `json:"-"`
}

func (l *Ledger) GrantAuthority(ctx context.Context, c GrantAuthority) (AuthorityResult, error) {
	if err := l.actor.Validate(); err != nil {
		return AuthorityResult{}, err
	}
	if err := validateAuthorityTarget(c.Target); err != nil {
		return AuthorityResult{}, err
	}
	if !slices.Contains(authorityLevels, c.Level) {
		return AuthorityResult{}, Fail(ErrInvalidInput, "invalid_authority",
			"%q is not an authority level; expected advisory, default, or mandatory", c.Level)
	}
	if err := l.mayGrant(c.Level); err != nil {
		return AuthorityResult{}, err
	}
	if strings.TrimSpace(c.Rationale) == "" {
		return AuthorityResult{}, Fail(ErrInvalidInput, "invalid_rationale",
			"a grant needs a rationale; what an actor established has to be explainable later")
	}
	if len(c.Scope) > 255 {
		return AuthorityResult{}, Fail(ErrInvalidInput, "invalid_scope",
			"an authority scope is at most 255 characters")
	}
	if err := validateOptionalDestination(c.DestinationKind, c.DestinationLocator); err != nil {
		return AuthorityResult{}, err
	}
	// authorities.destination_locator is a Reject column: an identity value is
	// never rewritten, because a redacted locator names a different place.
	if err := l.rejectSecrets("destination locator", c.DestinationLocator); err != nil {
		return AuthorityResult{}, err
	}

	rationale, findings, err := l.redactField("rationale", strings.TrimSpace(c.Rationale))
	if err != nil {
		return AuthorityResult{}, err
	}
	l.assertRedacted("authorities.rationale", rationale)

	grant := Authority{
		ID: NewAuthorityID(), Target: c.Target, Level: c.Level, Scope: c.Scope,
		DestinationKind: c.DestinationKind, DestinationLocator: c.DestinationLocator,
		Rationale: rationale, OccurredAt: l.clock(), Provenance: l.actor,
	}
	out := AuthorityResult{Findings: findings}
	err = l.store.Write(ctx, func(tx Tx) error {
		// authorities.target_id is polymorphic and carries no foreign key. It
		// cannot dangle today — revisions and proposals are never deleted — and
		// the check runs anyway, so adding a target kind does not silently open
		// a gap that `bdc doctor` then has to find.
		if err := assertTargetExists(tx, c.Target); err != nil {
			return err
		}
		if err := tx.AppendAuthority(grant); err != nil {
			return err
		}
		out.Authority = grant
		out.EffectiveLevel, err = effectiveAuthority(tx, c.Target)
		return err
	})
	if err != nil {
		return AuthorityResult{}, err
	}
	return out, nil
}

// effectiveAuthority is the latest level for a target. Events arrive oldest
// first, so the last authority row wins and no history means advisory.
func effectiveAuthority(snap Snapshot, target RecordRef) (AuthorityLevel, error) {
	events, err := snap.Events(EventQuery{Targets: []RecordRef{target}})
	if err != nil {
		return "", err
	}
	level := AuthorityAdvisory
	for _, e := range events {
		if e.Kind == EventAuthority {
			level = AuthorityLevel(e.Summary)
		}
	}
	return level, nil
}

func validateAuthorityTarget(ref RecordRef) error {
	if !slices.Contains(authorityTargets, ref.Kind) {
		return Fail(ErrInvalidInput, "invalid_record_kind",
			"%q cannot carry authority; expected an Insight revision (%s) or proposal (%s) id",
			ref.Kind, PrefixRevision, PrefixProposal)
	}
	_, err := ParseID(targetPrefixes[ref.Kind], ref.ID)
	return err
}

// validateOptionalDestination checks a destination that may be absent
// altogether, but never half-present: a kind with no locator names nothing, and
// a locator with no kind names nothing in particular.
func validateOptionalDestination(kind, locator string) error {
	switch {
	case kind == "" && locator == "":
		return nil
	case kind == "" || locator == "":
		return Fail(ErrInvalidInput, "invalid_destination",
			"a destination is a kind and a locator together; pass kind:locator")
	case len(locator) > 1024:
		return Fail(ErrInvalidInput, "invalid_destination",
			"destination locator is longer than 1024 characters")
	}
	return ValidateDestKind(kind)
}
