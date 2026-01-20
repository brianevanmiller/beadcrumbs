// Package types defines the core data structures for beadcrumbs.
package types

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

// InsightType represents the classification of an insight.
type InsightType string

const (
	InsightHypothesis InsightType = "hypothesis" // "I think..." — speculation before evidence
	InsightDiscovery  InsightType = "discovery"  // "I found..." — evidence-based finding
	InsightQuestion   InsightType = "question"   // "What about...?" — open uncertainty
	InsightFeedback   InsightType = "feedback"   // "They said..." — external input received
	InsightPivot      InsightType = "pivot"      // "Actually..." — direction changed
	InsightDecision   InsightType = "decision"   // "We'll do..." — committed to approach
)

// ValidInsightTypes returns all valid insight types.
func ValidInsightTypes() []InsightType {
	return []InsightType{
		InsightHypothesis,
		InsightDiscovery,
		InsightQuestion,
		InsightFeedback,
		InsightPivot,
		InsightDecision,
	}
}

// IsValid checks if the insight type is valid.
func (t InsightType) IsValid() bool {
	switch t {
	case InsightHypothesis, InsightDiscovery, InsightQuestion, InsightFeedback, InsightPivot, InsightDecision:
		return true
	default:
		return false
	}
}

// InsightSource represents the source of an insight.
type InsightSource struct {
	Type         string   `json:"type"`                    // ai-session|slack|git|human
	Ref          string   `json:"ref,omitempty"`           // Optional external reference
	Participants []string `json:"participants,omitempty"`  // Who was involved
}

// Insight is the atomic unit - a moment of understanding.
type Insight struct {
	ID        string    `json:"id"`        // e.g., "ins-7f2a"
	Timestamp time.Time `json:"timestamp"` // When understanding occurred

	// Content (self-contained, summarized)
	Content string `json:"content"` // The insight itself
	Summary string `json:"summary"` // One-line summary

	// Classification
	Type       InsightType `json:"type"`
	Confidence float32     `json:"confidence"` // 0.0-1.0

	// Source reference
	Source InsightSource `json:"source"`

	// Thread membership
	ThreadID string `json:"thread_id,omitempty"`

	// Author tracking
	AuthorID   string   `json:"author_id,omitempty"`   // Who captured/recorded this insight
	EndorsedBy []string `json:"endorsed_by,omitempty"` // Who endorsed this insight

	// Metadata
	Tags      []string  `json:"labels,omitempty"`
	CreatedBy string    `json:"created_by,omitempty"` // Legacy field for backwards compatibility
	CreatedAt time.Time `json:"created_at"`
}

// ThreadStatus represents the status of an insight thread.
type ThreadStatus string

const (
	ThreadActive    ThreadStatus = "active"
	ThreadConcluded ThreadStatus = "concluded"
	ThreadAbandoned ThreadStatus = "abandoned"
)

// InsightThread groups related insights into a narrative journey.
type InsightThread struct {
	ID     string       `json:"id"`     // e.g., "thr-9e1b"
	Title  string       `json:"title"`  // "Understanding the auth bug"
	Status ThreadStatus `json:"status"` // active|concluded|abandoned

	// AI-generated summary of current understanding
	CurrentUnderstanding string `json:"current_understanding,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// DependencyType represents the type of relationship between insights/beads.
type DependencyType string

const (
	// Insight → Insight relationships
	DepBuildsOn    DependencyType = "builds-on"    // Extends understanding
	DepSupersedes  DependencyType = "supersedes"   // Replaces/corrects
	DepContradicts DependencyType = "contradicts"  // Unresolved tension

	// Insight → Bead relationships (when beads present)
	DepSpawns DependencyType = "spawns" // Led to task creation

	// Bead → Insight relationships (when beads present)
	DepInformedBy DependencyType = "informed-by" // Task informed by insight
)

// Dependency represents a relationship between insights or between insights and beads.
type Dependency struct {
	From      string         `json:"from"`       // ins-xxx or bead-xxx
	To        string         `json:"to"`         // ins-yyy or bead-yyy
	Type      DependencyType `json:"type"`
	CreatedAt time.Time      `json:"created_at"`
}

// GenerateID generates a new random ID with the given prefix.
// For insights: "ins-xxxx", for threads: "thr-xxxx"
func GenerateID(prefix string) string {
	bytes := make([]byte, 4)
	rand.Read(bytes)
	return prefix + "-" + hex.EncodeToString(bytes)[:4]
}

// NewInsight creates a new Insight with generated ID and timestamps.
func NewInsight(content string, insightType InsightType) *Insight {
	now := time.Now()
	return &Insight{
		ID:         GenerateID("ins"),
		Timestamp:  now,
		Content:    content,
		Type:       insightType,
		Confidence: 1.0,
		Source:     InsightSource{Type: "human"},
		CreatedAt:  now,
	}
}

// NewThread creates a new InsightThread with generated ID and timestamps.
func NewThread(title string) *InsightThread {
	now := time.Now()
	return &InsightThread{
		ID:        GenerateID("thr"),
		Title:     title,
		Status:    ThreadActive,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

// NewDependency creates a new Dependency with current timestamp.
func NewDependency(from, to string, depType DependencyType) *Dependency {
	return &Dependency{
		From:      from,
		To:        to,
		Type:      depType,
		CreatedAt: time.Now(),
	}
}

// NewInsightWithTimestamp creates a new Insight with an explicit timestamp.
// If timestamp is zero, uses current time.
func NewInsightWithTimestamp(content string, insightType InsightType, timestamp time.Time) *Insight {
	now := time.Now()
	if timestamp.IsZero() {
		timestamp = now
	}
	return &Insight{
		ID:         GenerateID("ins"),
		Timestamp:  timestamp,
		Content:    content,
		Type:       insightType,
		Confidence: 1.0,
		Source:     InsightSource{Type: "human"},
		CreatedAt:  now,
	}
}

