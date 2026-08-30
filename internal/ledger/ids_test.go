package ledger

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestReferenceIDForIsDeterministicAndFitsParsers(t *testing.T) {
	a := ReferenceIDFor("docs", "internal/parse.go", "")
	b := ReferenceIDFor("docs", "internal/parse.go", "")
	if a != b {
		t.Fatalf("the same identity minted two ids: %s and %s", a, b)
	}
	if got, err := ParseID(PrefixReference, string(a)); err != nil || ReferenceID(got) != a {
		t.Fatalf("ParseID rejected a minted Reference id: %q (%v)", a, err)
	}
	if _, err := uuid.Parse(string(a)[len(PrefixReference):]); err != nil {
		t.Fatalf("the id is not UUID-shaped: %s", a)
	}
	if len(a) != 40 {
		t.Fatalf("id length %d, want CHAR(40)", len(a))
	}

	scoped := ReferenceIDFor("docs", "internal/parse.go", "other")
	if scoped == a {
		t.Fatal("a workspace-scoped locator collapsed onto the unscoped id")
	}
	upper := ReferenceIDFor("docs", "docs/Foo.md", "")
	lower := ReferenceIDFor("docs", "docs/foo.md", "")
	if upper == lower {
		t.Fatal("case-variant locators produced one id; identity is byte-exact")
	}
	if !strings.HasPrefix(string(a), PrefixReference) {
		t.Fatalf("id %q is missing the ref_ prefix", a)
	}
}

func TestValidateReferenceIdentityRejectsCanonicalDelimiter(t *testing.T) {
	if err := ValidateReferenceIdentity("docs", "internal\u001fparse.go", ""); err == nil {
		t.Fatal("an identity containing the canonical delimiter was accepted")
	}
}
