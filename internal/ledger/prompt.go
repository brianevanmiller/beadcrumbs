package ledger

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// The prompt registry — the closed set of questions sampling is allowed to ask.
//
// Three rules hold across this file and ask.go:
//
//   - A question is curated, not composed. Enqueue names a registered key; it
//     never carries prose. An agent that could phrase the question would be
//     writing its own exam, which is why AddPrompt refuses a human-track prompt
//     from an agent actor and why `prompts propose` is not in this build.
//   - A prompt is versioned and never rewritten. Editing a question means
//     adding a version; asks already minted keep the text they were minted
//     from, because an answer is only interpretable beside the exact words that
//     produced it.
//   - Disabling is per key, not per row. "Stop asking this" is a statement
//     about the question, not about one version of it.
type (
	PromptRespondent string
	AnswerKind       string
	PromptOrigin     string
	AskState         string
)

const (
	PromptRespondentHuman PromptRespondent = "human"
	PromptRespondentAgent PromptRespondent = "agent"
	PromptRespondentBoth  PromptRespondent = "both"

	AnswerKindChoice AnswerKind = "choice"
	// AnswerKindScale is in the schema so a later phase adds rows rather than a
	// migration. This build refuses to register one: an ask nothing can answer
	// is worse than a column nothing writes.
	AnswerKindScale     AnswerKind = "scale"
	AnswerKindShortText AnswerKind = "short-text"

	PromptOriginCurated PromptOrigin = "curated"
	// PromptOriginAgentProposed is likewise reserved. The tracer writes curated
	// only; there is no propose path.
	PromptOriginAgentProposed PromptOrigin = "agent-proposed"

	AskPending   AskState = "pending"
	AskDelivered AskState = "delivered"
	AskAnswered  AskState = "answered"
	AskSkipped   AskState = "skipped"
	AskExpired   AskState = "expired"
)

// The seeded keys. They are named here because two of them carry behaviour —
// an answer materialises a validation or a capped grant — and that behaviour is
// keyed on the string, so the string belongs beside the code that reads it.
const (
	PromptAuthorityNudge = "authority-nudge"
	PromptCalibration    = "calibration"
	PromptContextFlush   = "context-flush"
)

const (
	maxPromptKeyChars     = 64
	maxPromptQuestionRune = 4096
	maxTriggerClassChars  = 32
	maxOptionIDChars      = 64
)

var promptRespondents = []PromptRespondent{
	PromptRespondentHuman, PromptRespondentAgent, PromptRespondentBoth,
}

var answerKinds = []AnswerKind{AnswerKindChoice, AnswerKindScale, AnswerKindShortText}

// AskOption is one selectable answer. The id is an identity token a caller
// passes back on `--choice`; the label is the prose a respondent reads.
type AskOption struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// Prompt is one registered question at one version. Options are decoded from
// options_json so no caller outside the store handles the encoded form.
type Prompt struct {
	ID               PromptID         `json:"id"`
	Key              string           `json:"prompt_key"`
	Version          int              `json:"prompt_version"`
	Respondent       PromptRespondent `json:"respondent"`
	QuestionTemplate string           `json:"question_template"`
	AnswerKind       AnswerKind       `json:"answer_kind"`
	Options          []AskOption      `json:"options,omitempty"`
	TriggerClass     string           `json:"trigger_class"`
	Origin           PromptOrigin     `json:"origin"`
	Active           bool             `json:"active"`
	CreatedAt        time.Time        `json:"created_at"`
	Provenance
}

// AddPrompt registers a new question, or a new version of an existing one.
type AddPrompt struct {
	Key          string
	Respondent   PromptRespondent
	Question     string
	AnswerKind   AnswerKind
	Options      []AskOption
	TriggerClass string
}

// AddPrompt appends a version. It never rewrites an existing row: asks already
// minted from version N stay interpretable beside the words that produced them.
//
// An agent may not register a human-track prompt. The sampled human is being
// asked to judge the agent's work, and an actor that can phrase that question
// can phrase it so the answer it wants is the easy one.
func (l *Ledger) AddPrompt(ctx context.Context, c AddPrompt) (Prompt, []Finding, error) {
	if err := l.actor.Validate(); err != nil {
		return Prompt{}, nil, err
	}
	if err := validatePromptKey(c.Key); err != nil {
		return Prompt{}, nil, err
	}
	if !slices.Contains(promptRespondents, c.Respondent) {
		return Prompt{}, nil, Fail(ErrInvalidInput, "invalid_ask",
			"%q is not a respondent; expected one of %s", c.Respondent, joinNames(promptRespondents))
	}
	if c.Respondent != PromptRespondentAgent && l.actor.ActorKind == ActorAgent {
		return Prompt{}, nil, Fail(ErrInvalidInput, "invalid_ask",
			"an agent may not register a %s-track prompt; a human must add the questions a human is asked",
			c.Respondent)
	}
	if err := validateAnswerKind(c.AnswerKind, c.Options); err != nil {
		return Prompt{}, nil, err
	}
	if err := validateTriggerClass(c.TriggerClass); err != nil {
		return Prompt{}, nil, err
	}
	// The key and the trigger class are identity and classification tokens,
	// never rewritten for the same reason an option id is not.
	if err := l.rejectSecrets("prompt key", c.Key); err != nil {
		return Prompt{}, nil, err
	}
	if err := l.rejectSecrets("trigger class", c.TriggerClass); err != nil {
		return Prompt{}, nil, err
	}

	question := strings.TrimSpace(c.Question)
	switch {
	case question == "":
		return Prompt{}, nil, Fail(ErrInvalidInput, "invalid_content", "a prompt needs a question")
	case utf8.RuneCountInString(question) > maxPromptQuestionRune:
		return Prompt{}, nil, Fail(ErrInvalidInput, "invalid_content_size",
			"a question is at most %d characters", maxPromptQuestionRune)
	}
	question, findings, err := l.redactField("question", question)
	if err != nil {
		return Prompt{}, nil, err
	}
	l.assertRedacted("prompts.question_template", question)

	options := make([]AskOption, 0, len(c.Options))
	for _, o := range c.Options {
		// An option id is an identity value: it is passed back on --choice and
		// frozen onto every ask minted from this prompt, so redacting it would
		// silently change which option an answer names. Identity rejects, the
		// same rule refs.locator follows.
		if err := l.rejectSecrets("option id", o.ID); err != nil {
			return Prompt{}, nil, err
		}
		label, more, err := l.redactField("option label", strings.TrimSpace(o.Label))
		if err != nil {
			return Prompt{}, nil, err
		}
		l.assertRedacted("prompts.options_json", label)
		findings = append(findings, more...)
		options = append(options, AskOption{ID: o.ID, Label: label})
	}

	prompt := Prompt{
		ID: NewPromptID(), Key: c.Key, Respondent: c.Respondent,
		QuestionTemplate: question, AnswerKind: c.AnswerKind, Options: options,
		TriggerClass: c.TriggerClass, Origin: PromptOriginCurated, Active: true,
		CreatedAt: l.clock(), Provenance: l.actor,
	}
	err = l.store.Write(ctx, func(tx Tx) error {
		existing, err := tx.Prompts(PromptQuery{Keys: []string{c.Key}})
		if err != nil {
			return err
		}
		prompt.Version = 1
		for _, p := range existing {
			if p.Version >= prompt.Version {
				prompt.Version = p.Version + 1
			}
		}
		return tx.InsertPrompt(prompt)
	})
	if err != nil {
		return Prompt{}, nil, err
	}
	return prompt, findings, nil
}

// DisablePrompt deactivates every version of one key. Disabling is a statement
// about the question rather than about a row: leaving version 1 active while
// version 2 is disabled would silently resume asking the older wording.
func (l *Ledger) DisablePrompt(ctx context.Context, keyOrID string) (Prompt, error) {
	if err := l.actor.Validate(); err != nil {
		return Prompt{}, err
	}
	var out Prompt
	err := l.store.Write(ctx, func(tx Tx) error {
		resolved, err := resolvePrompt(tx, keyOrID)
		if err != nil {
			return err
		}
		versions, err := tx.Prompts(PromptQuery{Keys: []string{resolved.Key}})
		if err != nil {
			return err
		}
		for _, p := range versions {
			if !p.Active {
				continue
			}
			if err := tx.SetPromptActive(p.ID, false); err != nil {
				return err
			}
		}
		resolved.Active = false
		out = resolved
		return nil
	})
	if err != nil {
		return Prompt{}, err
	}
	return out, nil
}

// Prompts is the registry read `bdc prompts list` and `bdc prompts show` share.
func (l *Ledger) Prompts(ctx context.Context, q PromptQuery) ([]Prompt, error) {
	var out []Prompt
	err := l.store.Read(ctx, func(snap Snapshot) error {
		rows, err := snap.Prompts(q)
		out = rows
		return err
	})
	if err != nil {
		return nil, err
	}
	if out == nil {
		out = []Prompt{}
	}
	return out, nil
}

// Prompt resolves one prompt by key or by id, for `bdc prompts show`.
func (l *Ledger) Prompt(ctx context.Context, keyOrID string) (Prompt, error) {
	var out Prompt
	err := l.store.Read(ctx, func(snap Snapshot) error {
		p, err := resolvePrompt(snap, keyOrID)
		out = p
		return err
	})
	return out, err
}

// resolvePrompt reads `bdc prompts show authority-nudge` and
// `bdc prompts show pmt_…` through one path. A key resolves to its highest
// version — the active one if any version is active, so a disabled registry
// entry is still inspectable rather than becoming a not-found.
func resolvePrompt(snap Snapshot, keyOrID string) (Prompt, error) {
	arg := strings.TrimSpace(keyOrID)
	if arg == "" {
		return Prompt{}, Fail(ErrInvalidInput, "invalid_usage", "name a prompt key or id")
	}
	if strings.HasPrefix(arg, PrefixPrompt) {
		id, err := ParseID(PrefixPrompt, arg)
		if err != nil {
			return Prompt{}, err
		}
		rows, err := snap.Prompts(PromptQuery{IDs: []PromptID{PromptID(id)}})
		if err != nil {
			return Prompt{}, err
		}
		if len(rows) == 0 {
			return Prompt{}, NotFound("prompt", id)
		}
		return rows[0], nil
	}
	rows, err := snap.Prompts(PromptQuery{Keys: []string{arg}})
	if err != nil {
		return Prompt{}, err
	}
	if len(rows) == 0 {
		return Prompt{}, NotFound("prompt", arg)
	}
	best := rows[0]
	for _, p := range rows[1:] {
		if (p.Active && !best.Active) || (p.Active == best.Active && p.Version > best.Version) {
			best = p
		}
	}
	return best, nil
}

// activePrompt is what an enqueue resolves: the highest active version of a
// key. A disabled key is a not-found rather than a silently older question.
func activePrompt(snap Snapshot, key string) (Prompt, error) {
	rows, err := snap.Prompts(PromptQuery{Keys: []string{key}, ActiveOnly: true})
	if err != nil {
		return Prompt{}, err
	}
	if len(rows) == 0 {
		return Prompt{}, NotFound("active prompt", key)
	}
	best := rows[0]
	for _, p := range rows[1:] {
		if p.Version > best.Version {
			best = p
		}
	}
	return best, nil
}

func validatePromptKey(key string) error {
	switch {
	case strings.TrimSpace(key) == "":
		return Fail(ErrInvalidInput, "invalid_ask", "a prompt needs a key")
	case key != strings.TrimSpace(key) || strings.ContainsAny(key, " \t\n"):
		return Fail(ErrInvalidInput, "invalid_ask",
			"a prompt key is a token with no whitespace, so it can be passed on a command line")
	case len(key) > maxPromptKeyChars:
		return Fail(ErrInvalidInput, "invalid_ask",
			"a prompt key is at most %d characters", maxPromptKeyChars)
	}
	return nil
}

// validateAnswerKind pairs the kind with its options, because the two are one
// decision: a choice with nothing to choose between cannot be answered, and
// options on a free-text prompt are options nothing would ever present.
func validateAnswerKind(kind AnswerKind, options []AskOption) error {
	if !slices.Contains(answerKinds, kind) {
		return Fail(ErrInvalidInput, "invalid_ask",
			"%q is not an answer kind; expected one of %s", kind, joinNames(answerKinds))
	}
	if kind == AnswerKindScale {
		return Fail(ErrInvalidInput, "invalid_ask",
			"this build presents choice and short-text prompts; a scale prompt could be "+
				"registered and never answered")
	}
	if kind != AnswerKindChoice {
		if len(options) > 0 {
			return Fail(ErrInvalidInput, "invalid_ask", "only a choice prompt carries options")
		}
		return nil
	}
	if len(options) < 2 {
		return Fail(ErrInvalidInput, "invalid_ask",
			"a choice prompt needs at least two options; one option is not a question")
	}
	seen := map[string]bool{}
	for _, o := range options {
		switch {
		case strings.TrimSpace(o.ID) == "" || strings.TrimSpace(o.Label) == "":
			return Fail(ErrInvalidInput, "invalid_ask", "an option is id:label, both non-empty")
		case len(o.ID) > maxOptionIDChars:
			return Fail(ErrInvalidInput, "invalid_ask",
				"an option id is at most %d characters", maxOptionIDChars)
		case strings.ContainsAny(o.ID, " \t\n"):
			return Fail(ErrInvalidInput, "invalid_ask",
				"an option id is a token with no whitespace; it is passed back on --choice")
		case seen[o.ID]:
			return Fail(ErrInvalidInput, "invalid_ask", "option id %q appears twice", o.ID)
		}
		seen[o.ID] = true
	}
	return nil
}

func validateTriggerClass(class string) error {
	switch {
	case strings.TrimSpace(class) == "":
		return Fail(ErrInvalidInput, "invalid_ask",
			"a prompt needs a trigger class naming why the answer is not observable")
	case len(class) > maxTriggerClassChars:
		return Fail(ErrInvalidInput, "invalid_ask",
			"a trigger class is at most %d characters", maxTriggerClassChars)
	case strings.ContainsAny(class, " \t\n"):
		return Fail(ErrInvalidInput, "invalid_ask", "a trigger class is a token with no whitespace")
	}
	return nil
}

// EncodeOptions renders an option set as the stored JSON. The store calls it so
// the encoded form has exactly one producer; nil encodes as SQL NULL, which is
// what a non-choice prompt holds.
func EncodeOptions(options []AskOption) (string, error) {
	if len(options) == 0 {
		return "", nil
	}
	b, err := json.Marshal(options)
	if err != nil {
		return "", FailWith(ErrIntegrity, "integrity_options", err, "cannot encode prompt options")
	}
	return string(b), nil
}

// DecodeOptions parses the stored JSON. A value that does not parse is an
// integrity error: it means a row was written by something that is not this
// build, and guessing at the options would present a question whose answers do
// not match the ones recorded.
func DecodeOptions(raw string) ([]AskOption, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var out []AskOption
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, FailWith(ErrIntegrity, "integrity_options", err,
			"stored prompt options are not a JSON array this build understands")
	}
	return out, nil
}
