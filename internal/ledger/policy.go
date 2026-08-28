package ledger

import "slices"

// Authority policy: who may establish what, and what a promotion has to hold
// before it may be applied.
//
// Two questions live here because both are policy rather than data, and both
// are asked from more than one write path:
//
//   - Who may grant a level. An agent may always grant advisory, may grant
//     default only where the repository's configuration permits it, and may
//     never grant mandatory. A mandatory attempt by an agent is a typed error,
//     never a silent downgrade; ck_aut_mandatory_human is the live assertion
//     that stays behind this check if it is ever bypassed.
//   - What a promotion requires. The effective requirement is the strictest of
//     the semantic class's, the destination's, and the requested level's, and
//     the `policy` class always requires a human regardless of destination.
//
// Authority is one of four independent axes and is derived from none of the
// others. No amount of confidence, evidence, or supporting validation raises a
// level or satisfies a requirement here.

// AuthorityRequirement is the strength of actor a promotion needs before it may
// be applied. It is an ordered scale so "the stricter of two requirements" is a
// max rather than a table of pairs.
type AuthorityRequirement string

const (
	// RequireNone means any actor may apply the proposal on its own authority.
	RequireNone AuthorityRequirement = "none"

	// RequireHuman means a human has to have decided: either a human is the
	// actor proposing or recording, or a human granted the proposal — or the
	// Insight revision behind it — an authority level above advisory.
	RequireHuman AuthorityRequirement = "human"
)

func (r AuthorityRequirement) rank() int {
	if r == RequireHuman {
		return 1
	}
	return 0
}

// humanAuthorityClasses are the semantic classes that require a human whatever
// the destination declares. `policy` is the whole list: a policy record's force
// is over the repository's own rules, which is exactly the thing an agent may
// not establish alone. Invariant §2.5.4.
var humanAuthorityClasses = []string{"policy"}

// AuthorityRequiredFor resolves the effective requirement for one proposal:
// max(class, destination, requested). Each input is an independent reason a
// human might be needed, and taking the strictest is what keeps a permissive
// destination from relaxing a strict class.
//
// The requested level is in the max because requesting mandatory force is
// asking for something only a human may grant (see mayGrant): letting an agent
// propose it and a second agent apply it would route around that rule.
func AuthorityRequiredFor(class string, caps []Capability, requested AuthorityLevel) AuthorityRequirement {
	req := RequireNone
	stricter := func(r AuthorityRequirement) {
		if r.rank() > req.rank() {
			req = r
		}
	}
	if slices.Contains(humanAuthorityClasses, class) {
		stricter(RequireHuman)
	}
	if slices.Contains(caps, CapRequiresHumanAuthority) {
		stricter(RequireHuman)
	}
	if requested == AuthorityMandatory {
		stricter(RequireHuman)
	}
	return req
}

// mayGrant answers whether this invocation's actor may grant a level. The three
// answers are different in kind, so they are three branches rather than a
// permission table: advisory is always allowed, default is a repository policy
// decision, and mandatory is a property of the actor.
func (l *Ledger) mayGrant(level AuthorityLevel) error {
	switch level {
	case AuthorityAdvisory:
		return nil
	case AuthorityDefault:
		if l.actor.ActorKind == ActorHuman || l.config.AgentMaySetDefault {
			return nil
		}
		return Fail(ErrAuthorityDenied, "authority_denied",
			"this repository does not permit an agent to grant a working default; "+
				"set %s=1 in repo_config, or have a human grant it",
			ConfigAgentMaySetDefault).
			WithDetails(map[string]any{"level": string(level), "actor_kind": string(l.actor.ActorKind)})
	case AuthorityMandatory:
		if l.actor.ActorKind == ActorHuman {
			return nil
		}
		// Never downgraded to advisory: an agent that asked for mandatory and
		// silently got advisory would believe it established something binding.
		return Fail(ErrAuthorityDenied, "authority_denied",
			"only a human may grant mandatory authority; the request was not downgraded").
			WithDetails(map[string]any{"level": string(level), "actor_kind": string(l.actor.ActorKind)})
	default:
		return Fail(ErrInvalidInput, "invalid_authority",
			"%q is not an authority level; expected advisory, default, or mandatory", level)
	}
}

// humanAuthorityHeld reports whether a human has granted any of these targets a
// level above advisory. Advisory does not count: it is informational by
// definition — cite it, do not act on it as settled — so treating it as
// approval would make the weakest grant satisfy the strictest requirement.
func humanAuthorityHeld(snap Snapshot, targets ...RecordRef) (bool, error) {
	events, err := snap.Events(EventQuery{Targets: targets})
	if err != nil {
		return false, err
	}
	for _, e := range events {
		if e.Kind != EventAuthority || e.ActorKind != ActorHuman {
			continue
		}
		switch AuthorityLevel(e.Summary) {
		case AuthorityDefault, AuthorityMandatory:
			return true, nil
		}
	}
	return false, nil
}

// Notice is the ledger's non-fatal channel: a condition a caller has to see but
// that does not make the write wrong. The CLI renders each as one warnings[]
// entry, so Code is what a script matches and Message is for a human.
//
// It lives here because every producer of one is a policy or invariant decision
// — an unmet evidence expectation, an idempotent hit that diverged, a receipt
// whose destination cannot prove durability — rather than a storage event.
type Notice struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
