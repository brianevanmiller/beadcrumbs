package types

import (
	"strings"
	"testing"
	"time"
)

// TestGenerateID_PrefixFormat verifies the ID has the correct "prefix-xxxx" format.
func TestGenerateID_PrefixFormat(t *testing.T) {
	prefix := "ins"
	id := GenerateID(prefix)

	if !strings.HasPrefix(id, prefix+"-") {
		t.Errorf("expected ID to start with %q, got %q", prefix+"-", id)
	}
}

// TestGenerateID_Length verifies the total length is prefix + "-" + 4 hex chars.
func TestGenerateID_Length(t *testing.T) {
	tests := []struct {
		prefix      string
		wantLength  int
	}{
		{"ins", len("ins") + 1 + 4},
		{"thr", len("thr") + 1 + 4},
		{"x", len("x") + 1 + 4},
	}

	for _, tc := range tests {
		id := GenerateID(tc.prefix)
		if len(id) != tc.wantLength {
			t.Errorf("GenerateID(%q): expected length %d, got %d (id=%q)",
				tc.prefix, tc.wantLength, len(id), id)
		}
	}
}

// TestGenerateID_HexSuffix verifies the suffix after the dash is valid lowercase hex.
func TestGenerateID_HexSuffix(t *testing.T) {
	id := GenerateID("ins")
	parts := strings.SplitN(id, "-", 2)
	if len(parts) != 2 {
		t.Fatalf("expected one dash in ID %q", id)
	}
	suffix := parts[1]
	for _, ch := range suffix {
		if !((ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f')) {
			t.Errorf("non-hex character %q found in ID suffix %q", ch, suffix)
		}
	}
}

// TestGenerateID_Uniqueness verifies two calls produce different IDs.
func TestGenerateID_Uniqueness(t *testing.T) {
	id1 := GenerateID("ins")
	id2 := GenerateID("ins")
	if id1 == id2 {
		t.Errorf("expected two distinct IDs, both were %q", id1)
	}
}

// TestNewInsight verifies the fields set by NewInsight.
func TestNewInsight(t *testing.T) {
	before := time.Now()
	content := "This is a test hypothesis"
	insightType := InsightHypothesis
	ins := NewInsight(content, insightType)
	after := time.Now()

	if !strings.HasPrefix(ins.ID, "ins-") {
		t.Errorf("expected ID to start with \"ins-\", got %q", ins.ID)
	}
	if ins.Content != content {
		t.Errorf("expected Content %q, got %q", content, ins.Content)
	}
	if ins.Type != insightType {
		t.Errorf("expected Type %q, got %q", insightType, ins.Type)
	}
	if ins.Confidence != 1.0 {
		t.Errorf("expected Confidence 1.0, got %v", ins.Confidence)
	}
	if ins.Source.Type != "human" {
		t.Errorf("expected Source.Type \"human\", got %q", ins.Source.Type)
	}
	if ins.Timestamp.IsZero() {
		t.Error("expected Timestamp to be non-zero")
	}
	if ins.CreatedAt.IsZero() {
		t.Error("expected CreatedAt to be non-zero")
	}
	if ins.Timestamp.Before(before) || ins.Timestamp.After(after) {
		t.Errorf("Timestamp %v is outside the expected range [%v, %v]", ins.Timestamp, before, after)
	}
	if ins.CreatedAt.Before(before) || ins.CreatedAt.After(after) {
		t.Errorf("CreatedAt %v is outside the expected range [%v, %v]", ins.CreatedAt, before, after)
	}
}

// TestNewThread verifies the fields set by NewThread.
func TestNewThread(t *testing.T) {
	before := time.Now()
	title := "Understand the auth bug"
	thr := NewThread(title)
	after := time.Now()

	if !strings.HasPrefix(thr.ID, "thr-") {
		t.Errorf("expected ID to start with \"thr-\", got %q", thr.ID)
	}
	if thr.Title != title {
		t.Errorf("expected Title %q, got %q", title, thr.Title)
	}
	if thr.Status != ThreadActive {
		t.Errorf("expected Status %q, got %q", ThreadActive, thr.Status)
	}
	if thr.CreatedAt.IsZero() {
		t.Error("expected CreatedAt to be non-zero")
	}
	if thr.UpdatedAt.IsZero() {
		t.Error("expected UpdatedAt to be non-zero")
	}
	if thr.CreatedAt.Before(before) || thr.CreatedAt.After(after) {
		t.Errorf("CreatedAt %v is outside the expected range [%v, %v]", thr.CreatedAt, before, after)
	}
	if thr.UpdatedAt.Before(before) || thr.UpdatedAt.After(after) {
		t.Errorf("UpdatedAt %v is outside the expected range [%v, %v]", thr.UpdatedAt, before, after)
	}
}

// TestNewDependency verifies the fields set by NewDependency.
func TestNewDependency(t *testing.T) {
	from := "ins-aabb"
	to := "ins-ccdd"
	depType := DepBuildsOn

	before := time.Now()
	dep := NewDependency(from, to, depType)
	after := time.Now()

	if dep.From != from {
		t.Errorf("expected From %q, got %q", from, dep.From)
	}
	if dep.To != to {
		t.Errorf("expected To %q, got %q", to, dep.To)
	}
	if dep.Type != depType {
		t.Errorf("expected Type %q, got %q", depType, dep.Type)
	}
	if dep.CreatedAt.IsZero() {
		t.Error("expected CreatedAt to be non-zero")
	}
	if dep.CreatedAt.Before(before) || dep.CreatedAt.After(after) {
		t.Errorf("CreatedAt %v is outside the expected range [%v, %v]", dep.CreatedAt, before, after)
	}
}

// TestNewInsightWithTimestamp_ExplicitTimestamp verifies an explicit non-zero timestamp is used.
func TestNewInsightWithTimestamp_ExplicitTimestamp(t *testing.T) {
	explicit := time.Date(2024, 6, 15, 10, 30, 0, 0, time.UTC)
	ins := NewInsightWithTimestamp("content", InsightDecision, explicit)

	if !ins.Timestamp.Equal(explicit) {
		t.Errorf("expected Timestamp %v, got %v", explicit, ins.Timestamp)
	}
}

// TestNewInsightWithTimestamp_ZeroTimestamp verifies a zero timestamp falls back to current time.
func TestNewInsightWithTimestamp_ZeroTimestamp(t *testing.T) {
	before := time.Now()
	ins := NewInsightWithTimestamp("content", InsightDecision, time.Time{})
	after := time.Now()

	if ins.Timestamp.IsZero() {
		t.Error("expected Timestamp to be non-zero when zero is passed")
	}
	if ins.Timestamp.Before(before) || ins.Timestamp.After(after) {
		t.Errorf("Timestamp %v is outside the expected range [%v, %v]", ins.Timestamp, before, after)
	}
}

// TestInsightType_IsValid verifies all 6 valid types return true.
func TestInsightType_IsValid(t *testing.T) {
	valid := []InsightType{
		InsightHypothesis,
		InsightDiscovery,
		InsightQuestion,
		InsightFeedback,
		InsightPivot,
		InsightDecision,
	}
	for _, it := range valid {
		if !it.IsValid() {
			t.Errorf("expected InsightType %q to be valid", it)
		}
	}
}

// TestInsightType_IsValid_Invalid verifies invalid types return false.
func TestInsightType_IsValid_Invalid(t *testing.T) {
	invalid := []InsightType{
		"invalid",
		"",
		"random",
		"Hypothesis", // wrong case
		"DECISION",   // wrong case
	}
	for _, it := range invalid {
		if it.IsValid() {
			t.Errorf("expected InsightType %q to be invalid", it)
		}
	}
}

// TestValidInsightTypes verifies exactly 6 types are returned.
func TestValidInsightTypes(t *testing.T) {
	types := ValidInsightTypes()
	if len(types) != 6 {
		t.Errorf("expected 6 valid insight types, got %d", len(types))
	}
}

// TestValidInsightTypes_ContainsAllTypes verifies each expected type is present.
func TestValidInsightTypes_ContainsAllTypes(t *testing.T) {
	expected := map[InsightType]bool{
		InsightHypothesis: false,
		InsightDiscovery:  false,
		InsightQuestion:   false,
		InsightFeedback:   false,
		InsightPivot:      false,
		InsightDecision:   false,
	}

	for _, it := range ValidInsightTypes() {
		if _, ok := expected[it]; !ok {
			t.Errorf("unexpected type %q in ValidInsightTypes()", it)
		}
		expected[it] = true
	}

	for it, seen := range expected {
		if !seen {
			t.Errorf("type %q missing from ValidInsightTypes()", it)
		}
	}
}
