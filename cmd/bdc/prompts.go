package main

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/brianevanmiller/beadcrumbs/internal/ledger"
)

// promptsListData is `{prompts}` and promptData is `{prompt}`. Listing includes
// disabled versions: "we stopped asking this" is part of what the registry
// records, and a listing that hid it would make a disabled key look like a key
// that never existed.
type promptsListData struct {
	Prompts []ledger.Prompt `json:"prompts"`
}

type promptData struct {
	Prompt ledger.Prompt `json:"prompt"`
}

func (a *app) newPromptsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "prompts",
		Short: "Inspect and curate the sampling question registry",
		Long: "The registry is the closed set of questions `bdc ask` may put to a human or an " +
			"agent. Enqueue names a key; it never carries prose.\n\n" +
			"Questions are versioned and never rewritten: editing one adds a version, so an " +
			"answer stays interpretable beside the exact words that produced it. Disabling is " +
			"per key rather than per version, because \"stop asking this\" is a statement about " +
			"the question.\n\n" +
			"An agent may not register a human-track prompt. Being able to phrase the question " +
			"you are graded on is being able to phrase it so the answer you want is the easy one.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return usageError(fmt.Errorf("prompts needs a subcommand; run `bdc prompts --help`"))
		},
	}
	cmd.AddCommand(
		a.newPromptsListCommand(),
		a.newPromptsShowCommand(),
		a.newPromptsAddCommand(),
		a.newPromptsDisableCommand(),
	)
	return cmd
}

func (a *app) newPromptsListCommand() *cobra.Command {
	var (
		respondent string
		activeOnly bool
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List registered questions, newest version last",
		Args:  cobra.NoArgs,
	}
	cmd.Flags().StringVar(&respondent, "respondent", "", "only human, agent, or both prompts")
	cmd.Flags().BoolVar(&activeOnly, "active", false, "only questions still being asked")

	cmd.RunE = a.handleLedger(func(cmd *cobra.Command, _ []string, led *ledger.Ledger) (result, error) {
		prompts, err := led.Prompts(cmd.Context(), ledger.PromptQuery{ActiveOnly: activeOnly})
		if err != nil {
			return result{}, err
		}
		if respondent != "" {
			filtered := make([]ledger.Prompt, 0, len(prompts))
			for _, p := range prompts {
				if string(p.Respondent) == respondent {
					filtered = append(filtered, p)
				}
			}
			prompts = filtered
		}
		return result{Data: promptsListData{Prompts: prompts}, Human: func(w io.Writer) {
			if len(prompts) == 0 {
				fmt.Fprintln(w, "no registered questions")
				return
			}
			tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "KEY\tVERSION\tRESPONDENT\tKIND\tACTIVE\tTRIGGER")
			for _, p := range prompts {
				fmt.Fprintf(tw, "%s\t%d\t%s\t%s\t%s\t%s\n",
					p.Key, p.Version, p.Respondent, p.AnswerKind, yesNo(p.Active), p.TriggerClass)
			}
			_ = tw.Flush()
		}}, nil
	})
	return cmd
}

func (a *app) newPromptsShowCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show <key-or-id>",
		Short: "Show one registered question and its options",
		Args:  cobra.ExactArgs(1),
	}
	cmd.RunE = a.handleLedger(func(cmd *cobra.Command, args []string, led *ledger.Ledger) (result, error) {
		prompt, err := led.Prompt(cmd.Context(), args[0])
		if err != nil {
			return result{}, err
		}
		return result{Data: promptData{Prompt: prompt}, Human: func(w io.Writer) {
			renderPrompt(w, prompt)
		}}, nil
	})
	return cmd
}

func (a *app) newPromptsAddCommand() *cobra.Command {
	var (
		key        string
		respondent string
		question   string
		answerKind string
		options    []string
		trigger    string
	)
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Register a question, or a new version of one",
		Long: "Register a question. An existing key gains a version; nothing is rewritten.\n\n" +
			"A choice prompt needs at least two options, because one option is not a question. " +
			"The trigger class names why the answer is not observable — a question whose answer " +
			"is derivable from the repository, the diff, or the ledger is a question not worth " +
			"a person's attention.",
		Args: cobra.NoArgs,
	}
	cmd.Flags().StringVar(&key, "key", "", "the question's stable key (required)")
	cmd.Flags().StringVar(&respondent, "respondent", string(ledger.PromptRespondentHuman),
		"who is asked: human, agent, or both")
	cmd.Flags().StringVar(&question, "question", "", "the question text; {target}, {confidence}, "+
		"and {excerpt} are substituted from the record an ask names (required)")
	cmd.Flags().StringVar(&answerKind, "answer-kind", string(ledger.AnswerKindChoice),
		"choice or short-text")
	cmd.Flags().StringArrayVar(&options, "option", nil,
		"an option as id:label (repeatable; required for a choice)")
	cmd.Flags().StringVar(&trigger, "trigger-class", "", "why the answer is not observable (required)")
	_ = cmd.MarkFlagRequired("key")
	_ = cmd.MarkFlagRequired("question")
	_ = cmd.MarkFlagRequired("trigger-class")

	cmd.RunE = a.handleLedger(func(cmd *cobra.Command, _ []string, led *ledger.Ledger) (result, error) {
		parsed, err := parseOptions(options)
		if err != nil {
			return result{}, err
		}
		prompt, findings, err := led.AddPrompt(cmd.Context(), ledger.AddPrompt{
			Key:          key,
			Respondent:   ledger.PromptRespondent(respondent),
			Question:     question,
			AnswerKind:   ledger.AnswerKind(answerKind),
			Options:      parsed,
			TriggerClass: trigger,
		})
		if err != nil {
			return result{}, err
		}
		a.warnRedaction(findings)
		return result{Data: promptData{Prompt: prompt}, Human: func(w io.Writer) {
			fmt.Fprintf(w, "%s registered as %s version %d\n", prompt.ID, prompt.Key, prompt.Version)
			renderPrompt(w, prompt)
		}}, nil
	})
	return cmd
}

func (a *app) newPromptsDisableCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "disable <key-or-id>",
		Short: "Stop asking a question, every version of it",
		Long: "Deactivate every version of one key. Nothing is deleted and no ask already open " +
			"is touched; the key simply stops resolving for new enqueues.",
		Args: cobra.ExactArgs(1),
	}
	cmd.RunE = a.handleLedger(func(cmd *cobra.Command, args []string, led *ledger.Ledger) (result, error) {
		prompt, err := led.DisablePrompt(cmd.Context(), args[0])
		if err != nil {
			return result{}, err
		}
		return result{Data: promptData{Prompt: prompt}, Human: func(w io.Writer) {
			fmt.Fprintf(w, "%s is no longer asked\n", prompt.Key)
		}}, nil
	})
	return cmd
}

func renderPrompt(w io.Writer, p ledger.Prompt) {
	fmt.Fprintf(w, "%s v%d (%s, %s, %s, trigger %s)\n",
		p.Key, p.Version, p.Respondent, p.AnswerKind, activeWord(p.Active), p.TriggerClass)
	fmt.Fprintf(w, "  %s\n", p.QuestionTemplate)
	for i, o := range p.Options {
		fmt.Fprintf(w, "  %d) %s  [%s]\n", i+1, o.Label, o.ID)
	}
}

// parseOptions reads the repeatable `id:label` arguments. The id is everything
// before the first colon because a label routinely contains one.
func parseOptions(raw []string) ([]ledger.AskOption, error) {
	out := make([]ledger.AskOption, 0, len(raw))
	for _, arg := range raw {
		id, label, ok := strings.Cut(strings.TrimSpace(arg), ":")
		if !ok {
			return nil, ledger.Fail(ledger.ErrInvalidInput, "invalid_usage",
				"an option is id:label; this argument has no colon separating the id")
		}
		out = append(out, ledger.AskOption{ID: strings.TrimSpace(id), Label: strings.TrimSpace(label)})
	}
	return out, nil
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

func activeWord(v bool) string {
	if v {
		return "active"
	}
	return "disabled"
}
