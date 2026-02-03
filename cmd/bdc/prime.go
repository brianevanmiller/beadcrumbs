package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

var primeExportMode bool

var primeCmd = &cobra.Command{
	Use:   "prime",
	Short: "Output AI-optimized beadcrumbs workflow context",
	Long: `Output essential beadcrumbs workflow context in AI-optimized markdown format.

Designed for Claude Code hooks (SessionStart, PreCompact) to inject
beadcrumbs workflow instructions into AI agent context automatically.

When .beadcrumbs/ is not found, exits silently (exit 0, no output).
This enables safe cross-project hook integration.

Place a .beadcrumbs/PRIME.md file to override the default output entirely.
Use --export to dump the default content for customization.`,
	Run: func(cmd *cobra.Command, args []string) {
		bcDir := findBeadcrumbsDir()
		if bcDir == "" {
			// Not in a beadcrumbs project -- silent exit
			// CRITICAL: No stderr output, exit 0
			os.Exit(0)
		}

		// Check for custom PRIME.md override (unless --export flag)
		if !primeExportMode {
			primePath := filepath.Join(bcDir, "PRIME.md")
			if content, err := os.ReadFile(primePath); err == nil {
				fmt.Print(string(content))
				return
			}
		}

		// Output default workflow context
		outputPrimeContext(os.Stdout)
	},
}

// findBeadcrumbsDir checks for .beadcrumbs/ in the current directory.
// Returns the path if found, empty string otherwise.
func findBeadcrumbsDir() string {
	if info, err := os.Stat(".beadcrumbs"); err == nil && info.IsDir() {
		return ".beadcrumbs"
	}
	return ""
}

// outputPrimeContext outputs beadcrumbs workflow context in markdown format.
func outputPrimeContext(w io.Writer) {
	context := `# Beadcrumbs Insight Tracker Active

> **Context Recovery**: Run ` + "`bdc prime`" + ` after compaction, clear, or new session
> Hooks auto-call this in Claude Code when .beadcrumbs/ detected

## Core Rules
- **Use bdc (beadcrumbs)** for ALL reasoning and understanding tracking
- **Use bd (beads)** for task tracking -- they are complementary tools
- bd tracks *what* you're doing; bdc tracks *why*
- Always use ` + "`--thread`" + ` to associate captures with context
- Always use ` + "`--author cc:<model>`" + ` for AI agent attribution (e.g., ` + "`cc:opus-4.5`" + `)
- Do NOT capture routine tool calls, simple acknowledgments, or mechanical steps

## Session Protocol

**Session start:**
` + "```bash" + `
bdc thread new "Brief description of this session's goal"
bdc capture --thread <ref> --hypothesis "Initial approach" --author cc:<model>
` + "```" + `

**During session** (capture as reasoning evolves):
` + "```bash" + `
bdc capture --thread <ref> --hypothesis "Weighing approach X" --author cc:<model>
bdc capture --thread <ref> --discovery "Found evidence for Y" --author cc:<model>
bdc capture --thread <ref> --question "Should we use Z?" --author cc:<model>
bdc capture --thread <ref> --feedback "Human adjusted specs" --author brian
bdc capture --thread <ref> --pivot "Switching approach because..." --author cc:<model>
bdc capture --thread <ref> --decision "Committed to approach" --author cc:<model>
` + "```" + `

**Session end:**
` + "```bash" + `
bdc capture --thread <ref> --decision "Final outcome summary" --author cc:<model>
bdc thread close <thread-id>
` + "```" + `

## Insight Types

| Type | When to Use | Symbol |
|------|------------|--------|
| hypothesis | Speculation before evidence | ` + "`--hypothesis`" + ` |
| discovery | Evidence-based finding | ` + "`--discovery`" + ` |
| question | Open uncertainty | ` + "`--question`" + ` |
| feedback | External/human input received | ` + "`--feedback`" + ` |
| pivot | Direction changed | ` + "`--pivot`" + ` |
| decision | Committed to approach | ` + "`--decision`" + ` |

## Essential Commands

### Threads
- ` + "`bdc thread new \"<title>\"`" + ` - Start a narrative thread
- ` + "`bdc thread list --status=active`" + ` - See open threads
- ` + "`bdc thread close <id>`" + ` - Conclude a thread

### Capturing
- ` + "`bdc capture --thread <ref> --<type> \"...\" --author cc:<model>`" + `
- Thread ref accepts: thread ID (thr-xxx), bead ID (bd-xxx), or external ref (linear:ENG-456)

### Viewing
- ` + "`bdc timeline [thread-id]`" + ` - Chronological view
- ` + "`bdc decisions [thread-id]`" + ` - Filter to decisions only
- ` + "`bdc questions --unresolved`" + ` - Open questions needing answers
- ` + "`bdc list --thread=<id> --type=<type>`" + ` - Filtered insight list

### Beads Integration
- ` + "`bdc link <id> --spawns=<bead-id>`" + ` - Link insight to task it spawned
- ` + "`bdc trace <bead-id>`" + ` - Trace reasoning chain for a task
- ` + "`bdc spawn <insight-id> --title=\"...\"`" + ` - Create task from insight

## What to Capture

| Scenario | Type | Author |
|----------|------|--------|
| AI weighs a possible approach | --hypothesis | --author cc:<model> |
| AI finds evidence for/against something | --discovery | --author cc:<model> |
| Open uncertainty or question | --question | either |
| Human explains reasoning or adjusts specs | --feedback | --author brian |
| Direction changes | --pivot | whoever initiated it |
| Committed approach or final call | --decision | whoever made the call |

## What NOT to Capture
- Routine tool calls (file reads, grep, glob, running builds)
- Minor formatting or whitespace changes
- Simple acknowledgments ("OK", "got it", "done")
- Restating what the user said without adding new reasoning
- Mechanical steps unless the result reveals something unexpected
- Single-line bug fixes with obvious cause and solution
`
	_, _ = fmt.Fprint(w, context)
}

func init() {
	primeCmd.Flags().BoolVar(&primeExportMode, "export", false, "Output default content (ignores PRIME.md override)")
	rootCmd.AddCommand(primeCmd)
}
