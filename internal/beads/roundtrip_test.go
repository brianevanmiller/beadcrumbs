package beads_test

import (
	"strings"
	"testing"

	"github.com/brianevanmiller/beadcrumbs/internal/beads"
)

// The nine commands against the real binary, in the order a promotion uses
// them. Its point is the shapes: `dependencies[]` is hydrated related issues on
// show and edge records on list, which is why one struct cannot serve both.
func TestRoundTripAgainstRealBeads(t *testing.T) {
	repo := workspace(t)
	adapter, av := beads.Detect(ctx(), repo)
	if adapter == nil {
		t.Fatalf("no adapter: %+v", av)
	}

	parent, err := adapter.Create(ctx(), beads.NewIssue{Title: "Round trip parent", Type: "task"})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	child, err := adapter.Create(ctx(), beads.NewIssue{
		Title:       "Round trip child",
		Description: "a body that never touches argv",
		Type:        "decision",
		Labels:      []string{"beadcrumbs"},
		ExternalRef: "bdc:ins-1@2",
		Deps:        []beads.Dep{{Relation: "discovered-from", ID: parent.ID}},
	})
	if err != nil {
		t.Fatalf("create child: %v", err)
	}
	if child.ExternalRef != "bdc:ins-1@2" || !strings.Contains(child.Description, "never touches argv") {
		t.Fatalf("create did not record what it was given: %+v", child)
	}
	if child.CreatedAt.IsZero() {
		t.Fatal("create returned no created_at")
	}

	shown, err := adapter.Resolve(ctx(), child.ID)
	if err != nil {
		t.Fatalf("show: %v", err)
	}
	if len(shown.Dependencies) != 1 {
		t.Fatalf("show hydrated %d relations, expected 1: %+v", len(shown.Dependencies), shown.Dependencies)
	}
	if rel := shown.Dependencies[0]; rel.ID != parent.ID || rel.Relation != "discovered-from" || rel.Title == "" {
		t.Fatalf("show's relation is not the hydrated shape: %+v", rel)
	}
	if shown.UpdatedAt.IsZero() {
		t.Fatal("show returned no updated_at")
	}

	listed, err := adapter.List(ctx(), beads.Filter{IDs: []string{child.ID}, Limit: 5})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("list returned %d issues for one id", len(listed))
	}
	if len(listed[0].Dependencies) != 1 {
		t.Fatalf("list returned %d edges, expected 1", len(listed[0].Dependencies))
	}
	if edge := listed[0].Dependencies[0]; edge.IssueID != child.ID || edge.DependsOnID != parent.ID || edge.Type != "discovered-from" {
		t.Fatalf("list's relation is not the edge shape: %+v", edge)
	}

	comment, err := adapter.AddComment(ctx(), child.ID, "promotion receipt\nsecond line")
	if err != nil {
		t.Fatalf("comment: %v", err)
	}
	comments, err := adapter.Comments(ctx(), child.ID)
	if err != nil {
		t.Fatalf("comments: %v", err)
	}
	found := false
	for _, c := range comments {
		// Comment ids are UUIDv7, which is what makes <issue>/<comment> a
		// durable receipt locator.
		if c.ID == comment.ID && strings.Contains(c.Text, "second line") {
			found = true
		}
	}
	if !found {
		t.Fatalf("the comment just written is not in the thread: %+v", comments)
	}

	spawned, err := adapter.Create(ctx(), beads.NewIssue{Title: "Round trip spawned work", Type: "task"})
	if err != nil {
		t.Fatalf("create spawned: %v", err)
	}
	if err := adapter.Link(ctx(), spawned.ID, child.ID, "discovered-from"); err != nil {
		t.Fatalf("link: %v", err)
	}
	if err := adapter.Link(ctx(), spawned.ID, child.ID, "caused-by"); err == nil {
		t.Fatal("an unsupported dependency type reached bd")
	}

	ws, err := adapter.Workspace(ctx())
	if err != nil {
		t.Fatalf("context: %v", err)
	}
	if ws.ProjectID == "" || ws.DoltMode == "" {
		t.Fatalf("bd context returned no workspace identity: %+v", ws)
	}
	if adapter.Availability().ProjectID != ws.ProjectID {
		t.Fatal("availability did not pick up the workspace identity")
	}

	label, meta, fetchedAt, err := adapter.Enrich(ctx(), child.ID, ws.ProjectID)
	if err != nil {
		t.Fatalf("enrich: %v", err)
	}
	if label != "Round trip child" {
		t.Fatalf("the observed label is %q", label)
	}
	if !strings.Contains(string(meta), `"status":"open"`) || strings.Contains(string(meta), "never touches argv") {
		t.Fatalf("the observed cache is the wrong shape: %s", meta)
	}
	if fetchedAt.IsZero() {
		t.Fatal("enrichment reported no observation time")
	}
}
