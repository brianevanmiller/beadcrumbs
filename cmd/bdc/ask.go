package main

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/brianevanmiller/beadcrumbs/internal/ledger"
)

// The sampling surface. Four rules shape every command here:
//
//   - Nothing blocks. An empty queue is `ok: true` with an empty list, and a
//     skip is always available and costs nothing.
//   - The process actor is the transport, not the author. An agent relaying a
//     human's answer keeps its own --actor-kind and passes --respondent-id;
//     exporting BDC_ACTOR_KIND=human to record a relayed tap would be granting
//     yourself a signature.
//   - Quoted question text and quoted answers are data. Nothing in the ledger
//     acts on them, exactly as with a Crumb.
//   - A hook never reaches this file. Hooks may enqueue in a later phase; they
//     never present a question and never record an answer.

// askData is `{ask}`, the shape enqueue and skip return.
type askData struct {
	Ask ledger.Ask `json:"ask"`
}

// deliverData is `{questions}`. An empty array is success.
type deliverData struct {
	Questions []ledger.AskQuestion `json:"questions"`
}

// answerData is `{ask, crumb, validation, authority}`. The two judgement
// records are null when the answer produced neither, which is most of the time.
type answerData struct {
	Ask        ledger.Ask         `json:"ask"`
	Crumb      ledger.Crumb       `json:"crumb"`
	Validation *ledger.Validation `json:"validation"`
	Authority  *ledger.Authority  `json:"authority"`
}

func (a *app) newAskCommand() *cobra.Command {
	var respondent string

	cmd := &cobra.Command{
		Use:   "ask",
		Short: "Present and answer the ledger's own questions",
		Long: "Sampling asks for the judgement the ledger cannot derive: whether a blocked " +
			"promotion should get a working default, whether a conclusion held up, and what an " +
			"agent knows that was never written down.\n\n" +
			"With no subcommand it delivers and prints the first pending question. Answering is " +
			"optional and skipping is free — a question that cannot be waved away is a question " +
			"that will be answered carelessly.\n\n" +
			"An answer is a Crumb. `calibration` also appends a validation, and `authority-nudge` " +
			"may append a working default — never mandatory force, and never on a policy-class " +
			"proposal. Those two need a human's own `bdc authority`.",
		Args: cobra.NoArgs,
	}
	cmd.Flags().StringVar(&respondent, "respondent", "", "human or agent (default: follow --actor-kind)")
	cmd.RunE = a.handleLedger(func(cmd *cobra.Command, _ []string, led *ledger.Ledger) (result, error) {
		return a.deliver(cmd, led, respondent, true)
	})

	cmd.AddCommand(
		a.newAskEnqueueCommand(),
		a.newAskDeliverCommand(),
		a.newAskAnswerCommand(),
		a.newAskSkipCommand(),
	)
	return cmd
}

func (a *app) newAskEnqueueCommand() *cobra.Command {
	var (
		prompt     string
		target     string
		respondent string
	)
	cmd := &cobra.Command{
		Use:   "enqueue",
		Short: "Open one question from the registry",
		Long: "Mint one ask from a registered key. The question is rendered against its target " +
			"and frozen: a prompt revised tomorrow does not change what was asked today.\n\n" +
			"At most one ask is open per question and target, whatever the session — a blocked " +
			"decision does not become more pending because a new session started.",
		Args: cobra.NoArgs,
	}
	cmd.Flags().StringVar(&prompt, "prompt", "", "the registered question's key (required)")
	cmd.Flags().StringVar(&target, "target", "", "the Crumb, revision, or proposal the question is about")
	cmd.Flags().StringVar(&respondent, "respondent", "", "human or agent (default: follow --actor-kind)")
	_ = cmd.MarkFlagRequired("prompt")

	cmd.RunE = a.handleLedger(func(cmd *cobra.Command, _ []string, led *ledger.Ledger) (result, error) {
		intent := ledger.EnqueueAsk{
			PromptKey:  prompt,
			Respondent: ledger.PromptRespondent(respondent),
		}
		if target != "" {
			ref, err := ledger.TargetRef(target)
			if err != nil {
				return result{}, err
			}
			intent.Target = ref
		}
		ask, err := led.EnqueueAsk(cmd.Context(), intent)
		if err != nil {
			return result{}, err
		}
		return result{Data: askData{Ask: ask}, Human: func(w io.Writer) {
			fmt.Fprintf(w, "%s opened for the %s (%s)\n", ask.ID, ask.Respondent, ask.PromptKey)
		}}, nil
	})
	return cmd
}

func (a *app) newAskDeliverCommand() *cobra.Command {
	var respondent string
	cmd := &cobra.Command{
		Use:   "deliver",
		Short: "Present the pending questions for one respondent",
		Long: "Present at most four questions, oldest first, and mark them delivered. An empty " +
			"queue is success.\n\n" +
			"Delivering to a human first services the dead letters: every promotion proposal " +
			"waiting on a human decision gets one nudge, because that is exactly a question the " +
			"ledger cannot answer for itself. Delivering also sweeps — expiry is lazy, so this " +
			"is where a stale question becomes expired.",
		Args: cobra.NoArgs,
	}
	cmd.Flags().StringVar(&respondent, "respondent", "", "human or agent (default: follow --actor-kind)")
	cmd.RunE = a.handleLedger(func(cmd *cobra.Command, _ []string, led *ledger.Ledger) (result, error) {
		return a.deliver(cmd, led, respondent, false)
	})
	return cmd
}

// deliver is shared by `bdc ask` and `bdc ask deliver`. The two differ only in
// the human rendering: the bare form prints one question, because it is what a
// person runs mid-task and four questions at a terminal is an interruption.
func (a *app) deliver(cmd *cobra.Command, led *ledger.Ledger, respondent string, first bool) (result, error) {
	res, err := led.DeliverAsks(cmd.Context(), ledger.DeliverQuery{
		Respondent: ledger.PromptRespondent(respondent),
	})
	if err != nil {
		return result{}, err
	}
	return result{Data: deliverData{Questions: res.Questions}, Human: func(w io.Writer) {
		if len(res.Questions) == 0 {
			fmt.Fprintln(w, "no pending questions")
			return
		}
		shown := res.Questions
		if first {
			shown = shown[:1]
		}
		for i, q := range shown {
			if i > 0 {
				fmt.Fprintln(w)
			}
			renderQuestion(w, q)
		}
		if rest := len(res.Questions) - len(shown); rest > 0 {
			fmt.Fprintf(w, "\n%d more waiting; run `bdc ask deliver`\n", rest)
		}
	}}, nil
}

// renderQuestion is the plain-text delivery format the skill publishes. It is
// first-class rather than a fallback: a harness with a structured question tool
// may use it for choices, and the CLI path always works.
func renderQuestion(w io.Writer, q ledger.AskQuestion) {
	fmt.Fprintf(w, "[beadcrumbs ask %s · %s]\n%s\n", q.ID, q.PromptKey, q.Question)
	for i, o := range q.Options {
		fmt.Fprintf(w, "%d) %s\n", i+1, o.Label)
	}
	if len(q.Options) > 0 {
		fmt.Fprintln(w, "Reply with a number, an option id, or your own words. Say skip to skip.")
		return
	}
	fmt.Fprintln(w, "Reply in a sentence or two. Say skip to skip.")
}

func (a *app) newAskAnswerCommand() *cobra.Command {
	var (
		choice       string
		text         string
		note         string
		respondentID string
	)
	cmd := &cobra.Command{
		Use:   "answer <ask-id>",
		Short: "Record the answer to one question",
		Long: "Record an answer as the respondent's own Crumb, in one transaction with whatever " +
			"else it produces.\n\n" +
			"When an agent relays a human's reply it keeps --actor-kind agent and names the " +
			"person with --respondent-id: the Crumb is the human's, and the agent's session is " +
			"recorded on the ask as the transport. Exporting BDC_ACTOR_KIND=human to record a " +
			"relayed tap would be granting yourself a signature.\n\n" +
			"A relayed answer never establishes mandatory force and never grants on a " +
			"policy-class proposal; the answer is still recorded and a warning names the direct " +
			"command. It also never rejects a promotion — `reject` records a recommendation.",
		Args: cobra.ExactArgs(1),
	}
	cmd.Flags().StringVar(&choice, "choice", "", "an option id, or its 1-based number as printed")
	cmd.Flags().StringVar(&text, "text", "", "a free-text answer")
	cmd.Flags().StringVar(&note, "note", "", "anything to add beside the answer")
	cmd.Flags().StringVar(&respondentID, "respondent-id", "",
		"who actually answered, when an agent is relaying (default `human`)")

	cmd.RunE = a.handleLedger(func(cmd *cobra.Command, args []string, led *ledger.Ledger) (result, error) {
		res, err := led.AnswerAsk(cmd.Context(), ledger.AnswerAsk{
			AskID:        ledger.AskID(args[0]),
			ChoiceID:     choice,
			Text:         text,
			Note:         note,
			RespondentID: respondentID,
		})
		if err != nil {
			return result{}, err
		}
		a.warnRedaction(res.Findings)
		a.warnNotices(res.Notices)

		data := answerData{
			Ask: res.Ask, Crumb: res.Crumb,
			Validation: res.Validation, Authority: res.Authority,
		}
		return result{Data: data, Human: func(w io.Writer) {
			fmt.Fprintf(w, "%s answered (%s)\n", res.Ask.ID, res.Ask.PromptKey)
			fmt.Fprintf(w, "  %s recorded as %s\n", res.Crumb.ID, res.Crumb.ActorKind)
			if res.Validation != nil {
				fmt.Fprintf(w, "  %s: %s on %s\n",
					res.Validation.ID, res.Validation.Verdict, res.Validation.Target.ID)
			}
			if res.Authority != nil {
				fmt.Fprintf(w, "  %s: %s on %s\n",
					res.Authority.ID, res.Authority.Level, res.Authority.Target.ID)
			}
		}}, nil
	})
	return cmd
}

func (a *app) newAskSkipCommand() *cobra.Command {
	var reason string
	cmd := &cobra.Command{
		Use:   "skip <ask-id>",
		Short: "Decline one question",
		Long: "Decline a question. No Crumb is written — nobody said anything, and recording " +
			"that they did is the fastest way to make sampled data worthless. The skip itself " +
			"is kept, because a question that stops earning answers should be disabled rather " +
			"than asked harder.",
		Args: cobra.ExactArgs(1),
	}
	cmd.Flags().StringVar(&reason, "reason", "", "why, if it is worth saying")

	cmd.RunE = a.handleLedger(func(cmd *cobra.Command, args []string, led *ledger.Ledger) (result, error) {
		ask, err := led.SkipAsk(cmd.Context(), ledger.SkipAsk{
			AskID: ledger.AskID(args[0]), Reason: reason,
		})
		if err != nil {
			return result{}, err
		}
		return result{Data: askData{Ask: ask}, Human: func(w io.Writer) {
			fmt.Fprintf(w, "%s skipped\n", ask.ID)
		}}, nil
	})
	return cmd
}
