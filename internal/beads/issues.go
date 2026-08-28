package beads

import (
	"context"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/brianevanmiller/beadcrumbs/internal/ledger"
)

// Time is a `bd` timestamp: RFC3339 with optional fractional seconds, because
// precision differs by command — `create` and `comment` emit microseconds while
// `list`, `show`, and `comments` emit whole seconds, and the same edge has been
// observed with different offsets from two commands. Timestamps are therefore
// never compared across commands for equality.
type Time struct{ time.Time }

func (t *Time) UnmarshalJSON(b []byte) error {
	var raw any
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	s, ok := raw.(string)
	if !ok || s == "" {
		t.Time = time.Time{}
		return nil
	}
	parsed, err := time.Parse(time.RFC3339, s)
	if err != nil {
		// An unparseable timestamp is not worth failing an enrichment over: the
		// zero value already says "this adapter does not know when".
		t.Time = time.Time{}
		return nil
	}
	t.Time = parsed
	return nil
}

// IssueFields is the core payload both `bd show` and `bd list` return. It
// deliberately excludes the relation list, which is the one field whose shape
// differs between the two commands.
type IssueFields struct {
	ID              string   `json:"id"`
	Title           string   `json:"title"`
	Description     string   `json:"description,omitempty"`
	Status          string   `json:"status,omitempty"`
	Priority        int      `json:"priority"`
	Type            string   `json:"issue_type,omitempty"`
	Assignee        string   `json:"assignee,omitempty"`
	Owner           string   `json:"owner,omitempty"`
	CreatedAt       Time     `json:"created_at"`
	CreatedBy       string   `json:"created_by,omitempty"`
	UpdatedAt       Time     `json:"updated_at"`
	StartedAt       Time     `json:"started_at"`
	ClosedAt        Time     `json:"closed_at"`
	CloseReason     string   `json:"close_reason,omitempty"`
	ExternalRef     string   `json:"external_ref,omitempty"`
	Parent          string   `json:"parent,omitempty"`
	Labels          []string `json:"labels,omitempty"`
	DependencyCount int      `json:"dependency_count"`
	DependentCount  int      `json:"dependent_count"`
	CommentCount    int      `json:"comment_count"`
}

// Issue is `bd show`: its `dependencies[]` holds *hydrated* related issues,
// each carrying the related issue's own fields plus `dependency_type`.
type Issue struct {
	IssueFields
	Dependencies []Related `json:"dependencies,omitempty"`
}

// IssueSummary is `bd list`: its `dependencies[]` holds *edge records*. One
// struct cannot serve both commands, which is why there are two.
type IssueSummary struct {
	IssueFields
	Dependencies []Edge `json:"dependencies,omitempty"`
}

// Related is one hydrated relation from `bd show`.
type Related struct {
	ID       string `json:"id"`
	Title    string `json:"title,omitempty"`
	Status   string `json:"status,omitempty"`
	Type     string `json:"issue_type,omitempty"`
	Priority int    `json:"priority"`
	Relation string `json:"dependency_type,omitempty"`
}

// Edge is one relation record from `bd list`.
type Edge struct {
	IssueID     string `json:"issue_id"`
	DependsOnID string `json:"depends_on_id"`
	Type        string `json:"type,omitempty"`
	CreatedAt   Time   `json:"created_at"`
	CreatedBy   string `json:"created_by,omitempty"`
}

// Comment is one entry of the append-only discussion. Comment ids are UUIDv7,
// so `<issue-id>/<comment-id>` is a durable receipt locator.
type Comment struct {
	ID        string `json:"id"`
	IssueID   string `json:"issue_id"`
	Author    string `json:"author,omitempty"`
	Text      string `json:"text"`
	CreatedAt Time   `json:"created_at"`
}

// Relations are the five dependency types `bd link` accepts. `discovered-from`
// is the edge for Beadcrumbs-spawned work.
var Relations = []string{"blocks", "tracks", "related", "parent-child", "discovered-from"}

// Filter is the subset of `bd list` filters this adapter uses.
//
// Limit follows Go's zero value rather than bd's: 0 leaves bd's own default of
// 50 in place, a positive value caps the result, and a negative value means
// unlimited (`--limit 0`). An unset field must not silently mean "every issue
// in the tracker".
type Filter struct {
	IDs        []string
	Status     []string
	Labels     []string // AND: an issue must carry all of them
	LabelAny   []string // OR: an issue must carry at least one
	Parent     string
	Type       string
	Limit      int
	All        bool // include closed issues
	SkipLabels bool // skip label hydration; the labels field comes back empty
}

func (f Filter) args() ([]string, error) {
	if f.SkipLabels && (len(f.Labels) > 0 || len(f.LabelAny) > 0) {
		return nil, invalid("a Beads filter cannot both skip label hydration and filter on labels")
	}
	args := []string{"list"}
	add := func(flag string, values []string) {
		if len(values) > 0 {
			// Comma-separated, never repeated: measured, repeating -s silently
			// overwrites the previous value instead of accumulating.
			args = append(args, flag, strings.Join(values, ","))
		}
	}
	add("--id", f.IDs)
	add("--status", f.Status)
	add("--label", f.Labels)
	add("--label-any", f.LabelAny)
	if f.Parent != "" {
		args = append(args, "--parent", f.Parent)
	}
	if f.Type != "" {
		args = append(args, "--type", f.Type)
	}
	switch {
	case f.Limit < 0:
		args = append(args, "--limit", "0")
	case f.Limit > 0:
		args = append(args, "--limit", strconv.Itoa(f.Limit))
	}
	if f.All {
		args = append(args, "--all")
	}
	if f.SkipLabels {
		args = append(args, "--skip-labels")
	}
	return args, nil
}

// NewIssue is `bd create`. ExternalRef is a single opaque string on the Beads
// side, so a back-reference to Beadcrumbs is one token (`bdc:<insight>@<rev>`);
// anything richer belongs in Metadata.
type NewIssue struct {
	Title       string
	Description string
	Type        string // bug|feature|task|epic|chore|decision; empty leaves bd's default
	Priority    *int   // nil leaves bd's default
	Labels      []string
	ExternalRef string
	Parent      string
	Metadata    json.RawMessage
	Deps        []Dep
}

// Dep is one dependency declared at creation time, recorded in the same call
// that creates the issue.
type Dep struct {
	Relation string
	ID       string
}

// Resolve is `bd show --json <id>`, which returns an array even for one id.
func (a *Adapter) Resolve(ctx context.Context, id string) (Issue, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Issue{}, invalid("a Beads issue id is required")
	}
	out, err := a.run(ctx, read, nil, "show", id)
	if err != nil {
		return Issue{}, err
	}
	var issues []Issue
	if err := decode("show", out, &issues); err != nil {
		return Issue{}, err
	}
	if len(issues) == 0 {
		return Issue{}, ledger.Fail(ledger.ErrAdapter, "beads_no_such_issue",
			"the Beads workspace has no issue %s", id).
			WithDetails(map[string]any{"command": "bd show", "issue": id})
	}
	if issues[0].ID == "" {
		return Issue{}, missing("show", "id")
	}
	return issues[0], nil
}

// List is `bd list --json`.
func (a *Adapter) List(ctx context.Context, f Filter) ([]IssueSummary, error) {
	args, err := f.args()
	if err != nil {
		return nil, err
	}
	out, err := a.run(ctx, read, nil, args...)
	if err != nil {
		return nil, err
	}
	var issues []IssueSummary
	if err := decode("list", out, &issues); err != nil {
		return nil, err
	}
	for i := range issues {
		if issues[i].ID == "" {
			return nil, missing("list", "id")
		}
	}
	return issues, nil
}

// Comments is `bd comments <id> --json`.
func (a *Adapter) Comments(ctx context.Context, id string) ([]Comment, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, invalid("a Beads issue id is required")
	}
	out, err := a.run(ctx, read, nil, "comments", id)
	if err != nil {
		return nil, err
	}
	var comments []Comment
	if err := decode("comments", out, &comments); err != nil {
		return nil, err
	}
	for i := range comments {
		if comments[i].ID == "" {
			return nil, missing("comments", "id")
		}
	}
	return comments, nil
}

// AddComment is `bd comment <id> --json --stdin`. The body goes over stdin
// rather than argv, which is what keeps a multi-line receipt out of shell
// quoting rules entirely.
func (a *Adapter) AddComment(ctx context.Context, id, body string) (Comment, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Comment{}, invalid("a Beads issue id is required")
	}
	if strings.TrimSpace(body) == "" {
		return Comment{}, invalid("a Beads comment needs a body")
	}
	out, err := a.run(ctx, write, []byte(body), "comment", id, "--stdin")
	if err != nil {
		return Comment{}, err
	}
	var comment Comment
	if err := decode("comment", out, &comment); err != nil {
		return Comment{}, err
	}
	if comment.ID == "" {
		return Comment{}, missing("comment", "id")
	}
	return comment, nil
}

// Create is `bd create --json`, with the description on stdin for the same
// reason AddComment uses it.
func (a *Adapter) Create(ctx context.Context, n NewIssue) (Issue, error) {
	if strings.TrimSpace(n.Title) == "" {
		return Issue{}, invalid("a Beads issue needs a title")
	}
	args := []string{"create", "--title", n.Title}
	if n.Description != "" {
		args = append(args, "--stdin")
	}
	if n.Type != "" {
		args = append(args, "--type", n.Type)
	}
	if n.Priority != nil {
		args = append(args, "--priority", strconv.Itoa(*n.Priority))
	}
	if len(n.Labels) > 0 {
		args = append(args, "--labels", strings.Join(n.Labels, ","))
	}
	if n.ExternalRef != "" {
		args = append(args, "--external-ref", n.ExternalRef)
	}
	if n.Parent != "" {
		args = append(args, "--parent", n.Parent)
	}
	if len(n.Metadata) > 0 {
		if !json.Valid(n.Metadata) {
			return Issue{}, invalid("Beads issue metadata must be JSON")
		}
		args = append(args, "--metadata", string(n.Metadata))
	}
	deps := make([]string, 0, len(n.Deps))
	for _, d := range n.Deps {
		if err := checkRelation(d.Relation); err != nil {
			return Issue{}, err
		}
		if strings.TrimSpace(d.ID) == "" {
			return Issue{}, invalid("a Beads dependency needs an issue id")
		}
		deps = append(deps, d.Relation+":"+d.ID)
	}
	if len(deps) > 0 {
		args = append(args, "--deps", strings.Join(deps, ","))
	}

	var stdin []byte
	if n.Description != "" {
		stdin = []byte(n.Description)
	}
	out, err := a.run(ctx, write, stdin, args...)
	if err != nil {
		return Issue{}, err
	}
	var issue Issue
	if err := decode("create", out, &issue); err != nil {
		return Issue{}, err
	}
	if issue.ID == "" {
		return Issue{}, missing("create", "id")
	}
	return issue, nil
}

// Link is `bd link <from> <to> --type <relation>`, the reciprocal a tracker
// records for Beadcrumbs-spawned work. Neither side is authoritative for the
// other.
func (a *Adapter) Link(ctx context.Context, from, to, relation string) error {
	from, to = strings.TrimSpace(from), strings.TrimSpace(to)
	if from == "" || to == "" {
		return invalid("linking Beads issues needs two issue ids")
	}
	if err := checkRelation(relation); err != nil {
		return err
	}
	out, err := a.run(ctx, write, nil, "link", from, to, "--type", relation)
	if err != nil {
		return err
	}
	var result struct {
		Status string `json:"status"`
	}
	if err := decode("link", out, &result); err != nil {
		return err
	}
	if result.Status == "" {
		return missing("link", "status")
	}
	return nil
}

func checkRelation(relation string) error {
	if !slices.Contains(Relations, relation) {
		return invalid("%q is not a Beads dependency type; expected one of %s",
			relation, strings.Join(Relations, ", "))
	}
	return nil
}
