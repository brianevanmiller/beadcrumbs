package beads_test

import (
	"strings"
	"testing"

	"github.com/brianevanmiller/beadcrumbs/internal/beads"
)

// Detection must keep its three failures apart. Collapsing them is not a
// cosmetic loss: "install bd", "upgrade bd", and "this repository has no
// tracker" are three different things for a user to do about it.
func TestDetectionLadderSeparatesNotInstalledFromNoWorkspace(t *testing.T) {
	t.Run("not installed", func(t *testing.T) {
		repo := gitRepo(t)
		t.Setenv("PATH", t.TempDir())

		adapter, av := beads.Detect(ctx(), repo)
		if adapter != nil {
			t.Fatal("no bd on PATH, yet Detect returned an adapter")
		}
		if av.Present || av.Reason != beads.ReasonNotInstalled {
			t.Fatalf("expected %s, got %+v", beads.ReasonNotInstalled, av)
		}
	})

	t.Run("installed but no workspace", func(t *testing.T) {
		requireBD(t)
		repo := gitRepo(t)

		adapter, av := beads.Detect(ctx(), repo)
		if adapter != nil {
			t.Fatal("a repository with no Beads workspace produced an adapter")
		}
		if av.Present || av.Reason != beads.ReasonNoWorkspace {
			t.Fatalf("expected %s, got %+v", beads.ReasonNoWorkspace, av)
		}
		// Reaching rung 3 at all is the proof rung 2 did not run with -C:
		// `bd -C <no workspace> version` exits 1 and would have stopped here
		// with version_unreadable.
		if av.Version == "" {
			t.Fatal("rung 2 never reported a version, so the ladder short-circuited")
		}
	})

	t.Run("workspace present", func(t *testing.T) {
		repo := workspace(t)

		adapter, av := beads.Detect(ctx(), repo)
		if adapter == nil || !av.Present || av.Reason != beads.ReasonOK {
			t.Fatalf("expected a usable adapter, got %+v", av)
		}
		if av.Prefix != "tst" {
			t.Fatalf("detection read prefix %q, expected the workspace's own tst", av.Prefix)
		}
		if av.ProjectID != "" {
			t.Fatal("detection reported a project id it never asked bd context for")
		}
	})
}

// `bd -C <dir> version --json` exits 1 whenever <dir> has no Beads workspace,
// which is why version is the one command invoked without -C.
func TestVersionDetectionRunsWithoutDashC(t *testing.T) {
	recorded := argvLog(t)
	stubBD(t, `
version) echo '{"version":"1.2.2","schema_version":1}';;
where) echo '{"prefix":"tst","schema_version":1}';;`)

	_, av := beads.Detect(ctx(), "/fixture/repo")
	if !av.Present {
		t.Fatalf("detection failed against a healthy stub: %+v", av)
	}

	var version, where string
	for _, line := range recorded() {
		switch {
		case strings.HasSuffix(line, " version"):
			version = line
		case strings.HasSuffix(line, " where"):
			where = line
		}
	}
	if version == "" || where == "" {
		t.Fatalf("expected a version and a where invocation, recorded %q", recorded())
	}
	if strings.Contains(version, "-C") {
		t.Fatalf("version was invoked with -C (%q), which collapses three failures into one", version)
	}
	if !strings.Contains(where, "-C /fixture/repo") {
		t.Fatalf("where was not scoped to the repository root: %q", where)
	}
	if !strings.Contains(where, "--readonly") || strings.Contains(where, "--ignore-schema-skew") {
		t.Fatalf("where's standing flags are wrong: %q", where)
	}
}

// Prefix is display enrichment. A `bd where` that omits it still describes a
// workspace this adapter can use.
func TestMissingPrefixDoesNotDisableAdapter(t *testing.T) {
	stubBD(t, `
version) echo '{"version":"1.2.2"}';;
where) echo '{"path":"/fixture/repo/.beads","schema_version":1}';;`)

	adapter, av := beads.Detect(ctx(), "/fixture/repo")
	if adapter == nil || !av.Present {
		t.Fatalf("a missing prefix disabled the adapter: %+v", av)
	}
	if av.Prefix != "" {
		t.Fatalf("expected no prefix, got %q", av.Prefix)
	}
}

// is_worktree and is_redirected describe the directory bd ran in, not the
// tracker. project_id is the only workspace identity this adapter trusts.
func TestWorktreeFlagIsNotWorkspaceIdentity(t *testing.T) {
	t.Run("same tracker through a linked worktree", func(t *testing.T) {
		main := workspace(t)
		linked := t.TempDir() + "/linked"
		run(t, main, "git", "worktree", "add", "-q", linked, "-b", "linked-"+t.Name())
		t.Cleanup(func() { run(t, main, "git", "worktree", "remove", "--force", linked) })

		from := func(dir string) beads.WorkspaceContext {
			adapter, av := beads.Detect(ctx(), dir)
			if adapter == nil {
				t.Fatalf("no adapter for %s: %+v", dir, av)
			}
			ws, err := adapter.Workspace(ctx())
			if err != nil {
				t.Fatalf("bd context in %s: %v", dir, err)
			}
			return ws
		}
		here, there := from(main), from(linked)
		if here.ProjectID == "" || here.ProjectID != there.ProjectID {
			t.Fatalf("the same workspace reported two identities: %q and %q", here.ProjectID, there.ProjectID)
		}
		if here.BeadsDir != there.BeadsDir {
			t.Fatalf("the linked worktree resolved a different .beads: %q vs %q", here.BeadsDir, there.BeadsDir)
		}
	})

	// The real binary cannot be made to disagree on is_worktree on demand, so
	// the negative half — the flag differing must not change identity, and
	// identity differing must be refused — is checked against a stub.
	const issue = `[{"id":"tst-1","title":"Shared","status":"open"}]`
	cases := func(projectID, worktree string) string {
		return `
version) echo '{"version":"1.2.2"}';;
where) echo '{"prefix":"tst"}';;
context) echo '{"project_id":"` + projectID + `","is_worktree":` + worktree + `,"repo_root":"/fixture/repo"}';;
show) echo '` + issue + `';;`
	}

	t.Run("same identity, different flag", func(t *testing.T) {
		stubBD(t, cases("shared-project", "true"))
		adapter, _ := beads.Detect(ctx(), "/fixture/repo")
		if _, _, _, err := adapter.Enrich(ctx(), "tst-1", "shared-project"); err != nil {
			t.Fatalf("is_worktree changed the answer: %v", err)
		}
	})

	t.Run("different identity, same flag", func(t *testing.T) {
		stubBD(t, cases("other-project", "false"))
		adapter, _ := beads.Detect(ctx(), "/fixture/repo")
		_, _, _, err := adapter.Enrich(ctx(), "tst-1", "shared-project")
		if err == nil {
			t.Fatal("a Reference from another workspace was enriched from this one")
		}
		if !strings.Contains(err.Error(), "shared-project") {
			t.Fatalf("the refusal does not name the foreign workspace: %v", err)
		}
	})
}
