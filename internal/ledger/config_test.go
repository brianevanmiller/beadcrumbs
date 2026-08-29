package ledger

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// The seeds the schema migration writes must parse. If this drifts, every
// command that constructs a Redactor fails at startup with an integrity error.
func TestParseRepoConfigReadsTheSeededValues(t *testing.T) {
	cfg, err := ParseRepoConfig(map[string]string{
		ConfigHarvestAuto:        "0",
		ConfigAgentMaySetDefault: "0",
		ConfigPolicyVersion:      "1",
		ConfigRedactionVersion:   "1",
		ConfigRedactPatterns:     "[]",
		ConfigCreatedAt:          "2026-08-28 12:00:00.000000",
	})
	if err != nil {
		t.Fatalf("the seeded configuration must parse: %v", err)
	}
	if cfg.HarvestAuto {
		t.Fatal("automatic harvesting must be off by default")
	}
	if cfg.AgentMaySetDefault {
		t.Fatal("an agent must not be able to set a working default by default")
	}
	if cfg.PolicyVersion != "1" || cfg.RedactionVersion != "1" {
		t.Fatalf("versions parsed as %q/%q", cfg.PolicyVersion, cfg.RedactionVersion)
	}
}

// A value that cannot be read is an integrity error, never a default: treating
// an unparseable authority flag as false would be a policy decision made by a bug.
func TestParseRepoConfigRefusesToGuess(t *testing.T) {
	for name, kv := range map[string]map[string]string{
		"unreadable flag": {
			ConfigPolicyVersion: "1", ConfigRedactionVersion: "1",
			ConfigAgentMaySetDefault: "maybe",
		},
		"missing versions": {ConfigHarvestAuto: "0"},
		"absent flag": {
			ConfigPolicyVersion: "1", ConfigRedactionVersion: "1",
			ConfigHarvestAuto: "0",
		},
		"patterns are not a JSON array": {
			ConfigPolicyVersion: "1", ConfigRedactionVersion: "1",
			ConfigRedactPatterns: "not json",
		},
	} {
		if _, err := ParseRepoConfig(kv); !errors.Is(err, ErrIntegrity) {
			t.Fatalf("%s: expected an integrity error, got %v", name, err)
		}
	}
}

func TestParseIDRejectsTheWrongKind(t *testing.T) {
	crumb := string(NewCrumbID())
	if _, err := ParseID(PrefixCrumb, crumb); err != nil {
		t.Fatalf("a minted crumb id must parse: %v", err)
	}
	if _, err := ParseID(PrefixInsight, crumb); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("a crumb id must not parse as an insight id, got %v", err)
	}
	if _, err := ParseID(PrefixCrumb, PrefixCrumb+"not-a-uuid-at-all-not-even-close"); err == nil {
		t.Fatal("a malformed id must not parse")
	}
}

// Confidence is DECIMAL(4,3). A fourth decimal place would be silently rounded
// on write, and a value that changed between the command line and the ledger is
// worse than a rejected one.
func TestValidateConfidenceMatchesStoredPrecision(t *testing.T) {
	for _, ok := range []float64{0, 0.7, 0.125, 1} {
		if err := ValidateConfidence(ok); err != nil {
			t.Fatalf("%v must be accepted: %v", ok, err)
		}
	}
	for _, bad := range []float64{-0.001, 1.001, 0.7005} {
		if err := ValidateConfidence(bad); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("%v must be rejected, got %v", bad, err)
		}
	}
}

// Capabilities are stored in a SQL SET, so the encoded form must be canonical:
// two proposals that differ only in the order the flags were typed are the same
// proposal, and content_hash covers this field.
func TestCapabilityEncodingIsCanonical(t *testing.T) {
	a := EncodeCapabilities([]Capability{CapStableAnchor, CapAppendOnly})
	b := EncodeCapabilities([]Capability{CapAppendOnly, CapStableAnchor, CapAppendOnly})
	if a != b || a != "append-only,stable-anchor" {
		t.Fatalf("encoding is not canonical: %q vs %q", a, b)
	}
	back, err := DecodeCapabilities(a)
	if err != nil || len(back) != 2 {
		t.Fatalf("round trip lost capabilities: %v %v", back, err)
	}
	if _, err := DecodeCapabilities("append-only,invented"); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("an unknown stored capability must be an integrity error, got %v", err)
	}
}

// The JSON envelope's error.message is a stable contract, so it carries the
// ledger's own sentence and nothing else. The cause stays reachable for
// errors.Is; splicing it into the message would publish the engine's own text,
// which changes between upstream releases and interpolates key values.
func TestErrorMessageCarriesOnlyTheLedgersOwnText(t *testing.T) {
	cause := errors.New("duplicate primary key value (id='crb_1')")
	err := FailWith(ErrIntegrity, "integrity_example", cause, "the write could not be applied")
	if err.Error() != "the write could not be applied" {
		t.Fatalf("message = %q, want the ledger's sentence alone", err.Error())
	}
	if !errors.Is(err, cause) || !errors.Is(err, ErrIntegrity) {
		t.Fatal("the cause and the kind must both stay matchable")
	}
}

// A reference or destination argument is never echoed back. A mistyped --ref is
// routinely a token typed into the wrong flag, and --json output is logged and
// pasted; the error names the shape instead.
func TestParseDoesNotEchoTheArgument(t *testing.T) {
	const secret = "ghp_000000000000000000000000000000000000"
	for name, err := range map[string]error{
		"reference":   second(ParseRefSpec(secret, RelationSource)),
		"destination": second(ParseDestination(secret)),
	} {
		if err == nil {
			t.Fatalf("%s: an argument with no colon must not parse", name)
		}
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("%s: the error echoes its argument: %s", name, err)
		}
	}
}

func second[T any](_ T, err error) error { return err }

// A reference to no record renders as null. `omitempty` does not apply to
// structs, so without this an absent supersession is {"kind":"","id":""} —
// a record shape describing a record that does not exist.
func TestAbsentRecordRefMarshalsAsNull(t *testing.T) {
	encoded, err := json.Marshal(Validation{ID: "val_1", Target: RecordRef{Kind: KindCrumb, ID: "crb_1"}})
	if err != nil {
		t.Fatalf("marshalling: %v", err)
	}
	if !strings.Contains(string(encoded), `"superseded_by":null`) {
		t.Fatalf("absent supersession rendered as %s", encoded)
	}
	if !strings.Contains(string(encoded), `"target":{"kind":"crumb","id":"crb_1"}`) {
		t.Fatalf("a present reference must still render as an object: %s", encoded)
	}
}
