package ledger

import (
	"context"
	"fmt"
	"slices"
	"strings"
)

// Validation — the review axis.
//
// A validation is an append-only judgement about a record. Three rules hold
// across this file:
//
//   - Nothing is ever rewritten. Recording a verdict appends a row; the earlier
//     verdict and its rationale stay readable exactly as they were written, so
//     "this was disputed and then supported" is a fact the ledger keeps rather
//     than a state it overwrites.
//   - Absence means unreviewed. A record with no validation row is unreviewed,
//     which is why `unreviewed` is also a verdict a reviewer may record
//     deliberately: "I looked and I am not ready to say" is different from
//     nobody having looked.
//   - A rationale is always required. A verdict with no stated reason is an
//     assertion a later reader cannot weigh, and the whole point of keeping the
//     history is that it can be weighed.
//
// Validation is independent of Crumb review state, of confidence, and of
// authority. Both a Crumb review and a validation can spell "rejected" and they
// mean different things: one is a Crumb's lifecycle, the other is a judgement
// about a record's content.

var verdicts = []Verdict{
	VerdictUnreviewed, VerdictSupported, VerdictDisputed, VerdictRejected, VerdictSuperseded,
}

// evidenceExpectedVerdicts are the verdicts that should cite something. The
// design says evidence is required "when one exists", which is not
// machine-checkable, so the absence is a Notice and never an error — §2.5.8.
var evidenceExpectedVerdicts = []Verdict{VerdictDisputed, VerdictRejected, VerdictSuperseded}

// validationTargets are the record kinds a verdict applies to, matching
// validations.target_kind. A validation about a validation is not a thing the
// ledger models.
var validationTargets = []RecordKind{KindCrumb, KindRevision, KindProposal}

// supersessionTargets are the kinds that may supersede something. A Crumb never
// supersedes: it is a captured fragment, not a conclusion that replaces one.
var supersessionTargets = []RecordKind{KindRevision, KindProposal}

// RecordValidation is `bdc validate <target-id>`. Evidence is attached to the
// validation itself rather than to the record it judges: the reason a reviewer
// gave for a verdict belongs to that verdict, and a later contradicting verdict
// must be able to cite different evidence without rewriting the first.
type RecordValidation struct {
	Target       RecordRef
	Verdict      Verdict
	Rationale    string
	Evidence     []RefSpec
	SupersededBy RecordRef
}

// ValidationResult is the `{validation, effective_verdict}` the CLI contract
// promises. EffectiveVerdict is read back after the append rather than assumed
// to be the verdict just written: the current verdict is the latest row, and
// only the store can say which row that is.
type ValidationResult struct {
	Validation       Validation `json:"validation"`
	EffectiveVerdict Verdict    `json:"effective_verdict"`
	Findings         []Finding  `json:"-"`
	Notices          []Notice   `json:"-"`
}

func (l *Ledger) RecordValidation(ctx context.Context, c RecordValidation) (ValidationResult, error) {
	if err := l.actor.Validate(); err != nil {
		return ValidationResult{}, err
	}
	if err := validateValidationTarget(c.Target); err != nil {
		return ValidationResult{}, err
	}
	if !slices.Contains(verdicts, c.Verdict) {
		return ValidationResult{}, Fail(ErrInvalidInput, "invalid_verdict",
			"%q is not a verdict; expected one of %s", c.Verdict, joinNames(verdicts))
	}
	if strings.TrimSpace(c.Rationale) == "" {
		return ValidationResult{}, Fail(ErrInvalidInput, "invalid_rationale",
			"a verdict needs a rationale; the history is kept so it can be weighed later")
	}
	if err := validateSupersession(c.Verdict, c.SupersededBy); err != nil {
		return ValidationResult{}, err
	}
	for _, ref := range c.Evidence {
		if err := ref.Validate(); err != nil {
			return ValidationResult{}, err
		}
		// refs.locator is a Reject column: redacting a locator would silently
		// change which record it names.
		if err := l.rejectSecrets("evidence locator", ref.Locator); err != nil {
			return ValidationResult{}, err
		}
	}

	rationale, findings, err := l.redactField("rationale", strings.TrimSpace(c.Rationale))
	if err != nil {
		return ValidationResult{}, err
	}
	l.assertRedacted("validations.rationale", rationale)

	out := ValidationResult{Findings: findings}
	if slices.Contains(evidenceExpectedVerdicts, c.Verdict) && len(c.Evidence) == 0 {
		out.Notices = append(out.Notices, Notice{
			Code: "validation_without_evidence",
			Message: fmt.Sprintf(
				"a %s verdict cites no evidence; attach one with --evidence when evidence exists", c.Verdict),
		})
	}

	validation := Validation{
		ID: NewValidationID(), Target: c.Target, Verdict: c.Verdict, Rationale: rationale,
		SupersededBy: c.SupersededBy, OccurredAt: l.clock(), Provenance: l.actor,
	}
	err = l.store.Write(ctx, func(tx Tx) error {
		// The ledger's half of validations.target_id, which is polymorphic and
		// carries no foreign key. Checked inside the transaction that writes
		// the row, because a target that exists at validation time and not at
		// write time is exactly the orphan this prevents.
		if err := assertTargetExists(tx, c.Target); err != nil {
			return err
		}
		if !c.SupersededBy.Zero() {
			if err := assertTargetExists(tx, c.SupersededBy); err != nil {
				return err
			}
		}
		if err := tx.AppendValidation(validation); err != nil {
			return err
		}
		record := RecordRef{Kind: KindValidation, ID: string(validation.ID)}
		for _, ref := range c.Evidence {
			id, err := tx.UpsertReference(Reference{
				ID: NewReferenceID(), Kind: ref.Kind, Locator: ref.Locator,
				Workspace: ref.Workspace, CreatedAt: validation.OccurredAt,
			})
			if err != nil {
				return err
			}
			if err := tx.LinkReference(record, id, ref.Relation); err != nil {
				return err
			}
		}
		out.Validation = validation
		out.EffectiveVerdict, err = effectiveVerdict(tx, c.Target)
		return err
	})
	if err != nil {
		return ValidationResult{}, err
	}
	return out, nil
}

// effectiveVerdict is the latest verdict for one target, read through the same
// fold every other caller uses.
func effectiveVerdict(snap Snapshot, target RecordRef) (Verdict, error) {
	verdicts, _, err := latestJudgements(snap, []RecordRef{target})
	if err != nil {
		return "", err
	}
	return verdicts[target.ID], nil
}

// validateSupersession pairs the verdict with its successor. Both directions
// are errors: `superseded` with nothing to point at is the ck_val_supersede
// constraint, and a successor on any other verdict is a caller who meant
// something the ledger would silently drop.
func validateSupersession(verdict Verdict, by RecordRef) error {
	if verdict != VerdictSuperseded {
		if by.Zero() {
			return nil
		}
		return Fail(ErrInvalidInput, "invalid_supersession",
			"a successor is meaningful only for a superseded verdict, not %q", verdict)
	}
	if by.Zero() {
		return Fail(ErrInvalidInput, "invalid_supersession",
			"a superseded verdict needs the record that supersedes it; pass --superseded-by")
	}
	if !slices.Contains(supersessionTargets, by.Kind) {
		return Fail(ErrInvalidInput, "invalid_supersession",
			"%q cannot supersede a record; expected an Insight revision (%s) or proposal (%s) id",
			by.Kind, PrefixRevision, PrefixProposal)
	}
	_, err := ParseID(targetPrefixes[by.Kind], by.ID)
	return err
}

func validateValidationTarget(ref RecordRef) error {
	if !slices.Contains(validationTargets, ref.Kind) {
		return Fail(ErrInvalidInput, "invalid_record_kind",
			"%q cannot be validated; expected a Crumb (%s), Insight revision (%s), or proposal (%s) id",
			ref.Kind, PrefixCrumb, PrefixRevision, PrefixProposal)
	}
	_, err := ParseID(targetPrefixes[ref.Kind], ref.ID)
	return err
}
