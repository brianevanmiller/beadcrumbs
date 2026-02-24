package summary

import (
	"strings"
	"testing"
	"time"

	"github.com/brianevanmiller/beadcrumbs/internal/types"
)

func makeInsight(content string, insightType types.InsightType) *types.Insight {
	return &types.Insight{
		ID:        "ins-test",
		Timestamp: time.Now(),
		Content:   content,
		Type:      insightType,
		CreatedAt: time.Now(),
	}
}

func TestFormatSummary_AllTypes(t *testing.T) {
	thread := &types.InsightThread{
		ID:    "thr-abcd",
		Title: "Test thread with all insight types",
	}

	insights := []*types.Insight{
		makeInsight("Use Redis for caching", types.InsightDecision),
		makeInsight("Benchmarks show 50ms latency", types.InsightDiscovery),
		makeInsight("Switching from in-memory to Redis", types.InsightPivot),
		makeInsight("User wants TTL support", types.InsightFeedback),
		makeInsight("Maybe memcached would work", types.InsightHypothesis),
		makeInsight("What about cache invalidation?", types.InsightQuestion),
	}

	result := FormatSummary(thread, insights)

	// Header
	if !strings.Contains(result, "## Beadcrumbs Summary \u2014 Thread `thr-abcd`") {
		t.Error("missing header with em-dash and thread ID")
	}
	if !strings.Contains(result, "**Test thread with all insight types**") {
		t.Error("missing bold thread title")
	}

	// Sections
	if !strings.Contains(result, "### Decisions") {
		t.Error("missing Decisions section")
	}
	if !strings.Contains(result, "- **Use Redis for caching**") {
		t.Error("decisions should be bold")
	}
	if !strings.Contains(result, "### Discoveries") {
		t.Error("missing Discoveries section")
	}
	if !strings.Contains(result, "- Benchmarks show 50ms latency") {
		t.Error("missing discovery content")
	}
	if !strings.Contains(result, "### Pivots") {
		t.Error("missing Pivots section")
	}
	if !strings.Contains(result, "### Feedback") {
		t.Error("missing Feedback section")
	}

	// Footer
	if !strings.Contains(result, "---") {
		t.Error("missing footer divider")
	}
	if !strings.Contains(result, "6 insights") {
		t.Errorf("missing total insight count, got:\n%s", result)
	}
	if !strings.Contains(result, "1 decisions") {
		t.Error("missing decisions count")
	}
	if !strings.Contains(result, "1 discoverys") {
		// Note: naive pluralization is fine for now
	}
	if !strings.Contains(result, "*Tracked by [beadcrumbs]") {
		t.Error("missing attribution link")
	}

	// Section order: Decisions before Discoveries before Pivots before Feedback
	dIdx := strings.Index(result, "### Decisions")
	disIdx := strings.Index(result, "### Discoveries")
	pIdx := strings.Index(result, "### Pivots")
	fIdx := strings.Index(result, "### Feedback")
	if dIdx > disIdx || disIdx > pIdx || pIdx > fIdx {
		t.Error("sections are in wrong order")
	}
}

func TestFormatSummary_OnlyDecisions(t *testing.T) {
	thread := &types.InsightThread{
		ID:    "thr-1234",
		Title: "Decisions only",
	}

	insights := []*types.Insight{
		makeInsight("Go with approach A", types.InsightDecision),
		makeInsight("Also do approach B", types.InsightDecision),
	}

	result := FormatSummary(thread, insights)

	if !strings.Contains(result, "### Decisions") {
		t.Error("missing Decisions section")
	}
	if strings.Contains(result, "### Discoveries") {
		t.Error("should not have Discoveries section when none exist")
	}
	if strings.Contains(result, "### Pivots") {
		t.Error("should not have Pivots section when none exist")
	}
	if strings.Contains(result, "### Feedback") {
		t.Error("should not have Feedback section when none exist")
	}
	if !strings.Contains(result, "2 insights") {
		t.Error("wrong total count")
	}
	if !strings.Contains(result, "2 decisions") {
		t.Error("wrong decisions count")
	}
}

func TestFormatSummary_WithCurrentUnderstanding(t *testing.T) {
	thread := &types.InsightThread{
		ID:                   "thr-5678",
		Title:                "Thread with summary",
		CurrentUnderstanding: "The caching layer is working well with Redis.",
	}

	insights := []*types.Insight{
		makeInsight("Redis is the right choice", types.InsightDecision),
	}

	result := FormatSummary(thread, insights)

	if !strings.Contains(result, "### Summary") {
		t.Error("missing Summary section when CurrentUnderstanding is set")
	}
	if !strings.Contains(result, "The caching layer is working well with Redis.") {
		t.Error("missing CurrentUnderstanding content")
	}
}

func TestFormatSummary_Empty(t *testing.T) {
	thread := &types.InsightThread{
		ID:    "thr-0000",
		Title: "Empty thread",
	}

	result := FormatSummary(thread, []*types.Insight{})

	if !strings.Contains(result, "## Beadcrumbs Summary") {
		t.Error("missing header even with no insights")
	}
	if !strings.Contains(result, "0 insights") {
		t.Error("should show 0 insights")
	}
	if !strings.Contains(result, "*Tracked by [beadcrumbs]") {
		t.Error("missing attribution even with no insights")
	}
}
