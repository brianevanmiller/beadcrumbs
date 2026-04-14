package store

import (
	"database/sql"
	"time"

	"github.com/brianevanmiller/beadcrumbs/internal/types"
)

// Storage defines the interface for all store operations used by commands.
// Both Store (read-write) and any future implementations satisfy this interface.
type Storage interface {
	// Close releases the underlying database connection.
	Close() error

	// DB returns the underlying *sql.DB for commands that need raw query access.
	DB() *sql.DB

	// Insight operations
	CreateInsight(insight *types.Insight) error
	GetInsight(id string) (*types.Insight, error)
	UpdateInsight(insight *types.Insight) error
	DeleteInsight(id string) error
	ListInsights(threadID string, insightType types.InsightType, since time.Time, sourceRef string) ([]*types.Insight, error)
	SearchInsights(query string) ([]*types.Insight, error)
	ListInsightsByAuthor(authorID string) ([]*types.Insight, error)
	UpsertInsight(insight *types.Insight) error

	// Thread operations
	CreateThread(thread *types.InsightThread) error
	GetThread(id string) (*types.InsightThread, error)
	UpdateThread(thread *types.InsightThread) error
	ListThreads(status types.ThreadStatus) ([]*types.InsightThread, error)
	UpsertThread(thread *types.InsightThread) error

	// Dependency operations
	AddDependency(dep *types.Dependency) error
	GetDependencies(fromID string) ([]*types.Dependency, error)
	GetDependents(toID string) ([]*types.Dependency, error)
	ListAllDependencies() ([]*types.Dependency, error)
	UpsertDependency(dep *types.Dependency) error

	// Config operations
	GetConfig(key string) (string, error)
	SetConfig(key, value string) error

	// External ref mapping operations
	CreateExternalRefMapping(m *ExternalRefMapping) error
	GetExternalRefMappingByRef(externalRef string) (*ExternalRefMapping, error)
	GetExternalRefMappingsByThread(threadID string) ([]*ExternalRefMapping, error)
	UpdateExternalRefMappingMetadata(externalRef, metadata string) error

	// Origin operations
	ListOrigins() ([]*OriginSummary, error)

	// Integrity
	Verify() error
}
