package beads

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/brianevanmiller/beadcrumbs/internal/ledger"
)

// Enricher adapts a detected Adapter to the ledger's optional enrichment seam,
// and returns a nil interface for a nil Adapter. Detect returns (nil, absent)
// on every failure, and assigning that typed nil straight into
// ledger.Options.Enricher would produce a non-nil interface holding nil — an
// enricher the ledger believes it has and cannot call. This is the only
// supported way to wire one up.
func Enricher(a *Adapter) ledger.Enricher {
	if a == nil {
		return nil
	}
	return a
}

// Kind is the Reference kind this adapter refreshes. References of any other
// kind are skipped by the ledger and degrade to their stored locator.
func (a *Adapter) Kind() string { return refKind }

// Enrich re-observes one Reference. The label is the issue title and the
// metadata is a small observed cache — status, type, priority, labels, and the
// tracker's own updated_at — never the description, which is unbounded text the
// ledger has no use for.
//
// The returned time is when `bd` answered, which is what makes a cached value
// legible later as "this is what the tracker said at 14:02", not "this is true".
func (a *Adapter) Enrich(ctx context.Context, locator, workspace string) (string, []byte, time.Time, error) {
	locator = strings.TrimSpace(locator)
	if locator == "" {
		return "", nil, time.Time{}, invalid("a Beads Reference needs a locator")
	}
	if err := a.belongsHere(ctx, workspace); err != nil {
		return "", nil, time.Time{}, err
	}
	issue, err := a.Resolve(ctx, locator)
	if err != nil {
		return "", nil, time.Time{}, err
	}
	observed := struct {
		Status    string   `json:"status,omitempty"`
		Type      string   `json:"issue_type,omitempty"`
		Priority  int      `json:"priority"`
		Labels    []string `json:"labels,omitempty"`
		UpdatedAt string   `json:"updated_at,omitempty"`
	}{
		Status: issue.Status, Type: issue.Type, Priority: issue.Priority, Labels: issue.Labels,
	}
	if !issue.UpdatedAt.IsZero() {
		observed.UpdatedAt = issue.UpdatedAt.UTC().Format(time.RFC3339)
	}
	meta, err := json.Marshal(observed)
	if err != nil {
		return "", nil, time.Time{}, missing("show", "a representable status")
	}
	return issue.Title, meta, time.Now().UTC(), nil
}

// belongsHere refuses to answer for a Reference recorded against a different
// workspace. Identity is project_id — or the repository root, for a Reference
// stored before any `bd context` succeeded. is_worktree and is_redirected
// describe the directory `bd` ran in rather than the tracker and are never
// consulted here.
//
// An unprovable match is a refusal, not an assumption: enriching a Reference
// with another tracker's issue would cache a plausible, wrong title.
func (a *Adapter) belongsHere(ctx context.Context, workspace string) error {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" || workspace == a.repoRoot {
		return nil
	}
	ws, err := a.Workspace(ctx)
	if err != nil {
		return ledger.Fail(ledger.ErrAdapter, "beads_workspace_unknown",
			"this Reference names Beads workspace %q and `bd context` could not say which workspace is here",
			workspace)
	}
	if workspace == ws.ProjectID || workspace == ws.RepoRoot {
		return nil
	}
	return ledger.Fail(ledger.ErrAdapter, "beads_other_workspace",
		"this Reference belongs to Beads workspace %q, not the one in this repository", workspace).
		WithDetails(map[string]any{"reference_workspace": workspace, "project_id": ws.ProjectID})
}
