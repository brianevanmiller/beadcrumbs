package ledger

import (
	"context"
	"encoding/json"
	"time"
)

// repo_config keys. The seeds are written by the schema migration, so a ledger
// always has a complete configuration and no read path has to invent a default.
const (
	ConfigHarvestAuto        = "harvest.auto"
	ConfigAgentMaySetDefault = "authority.agent_may_set_default"
	ConfigPolicyVersion      = "policy.version"
	ConfigRedactionVersion   = "redaction.version"
	ConfigRedactPatterns     = "redact.patterns"
	ConfigCreatedAt          = "ledger.created_at"
)

// RepoConfig is the per-repository policy the ledger enforces. It is data, not
// flags: every value is stored in the ledger it governs, so a clone of the
// repository carries its own answer.
type RepoConfig struct {
	PolicyVersion      string   `json:"policy_version"`
	RedactionVersion   string   `json:"redaction_version"`
	HarvestAuto        bool     `json:"harvest_auto"`
	AgentMaySetDefault bool     `json:"agent_may_set_default"`
	RedactPatterns     []string `json:"redact_patterns,omitempty"`
}

// ParseRepoConfig reads the raw key/value rows. A malformed value is an
// integrity error rather than a default: silently treating an unparseable
// `authority.agent_may_set_default` as false would be a policy decision made by
// a bug.
func ParseRepoConfig(kv map[string]string) (RepoConfig, error) {
	c := RepoConfig{
		PolicyVersion:    kv[ConfigPolicyVersion],
		RedactionVersion: kv[ConfigRedactionVersion],
	}
	if c.PolicyVersion == "" || c.RedactionVersion == "" {
		return c, Fail(ErrIntegrity, "integrity_config_missing",
			"repo_config is missing %s or %s; the ledger was not initialised by this build",
			ConfigPolicyVersion, ConfigRedactionVersion)
	}
	var err error
	if c.HarvestAuto, err = parseFlag(kv, ConfigHarvestAuto); err != nil {
		return c, err
	}
	if c.AgentMaySetDefault, err = parseFlag(kv, ConfigAgentMaySetDefault); err != nil {
		return c, err
	}
	if raw := kv[ConfigRedactPatterns]; raw != "" {
		if err := json.Unmarshal([]byte(raw), &c.RedactPatterns); err != nil {
			return c, FailWith(ErrIntegrity, "integrity_config_invalid", err,
				"repo_config %s is not a JSON array", ConfigRedactPatterns)
		}
	}
	return c, nil
}

func parseFlag(kv map[string]string, key string) (bool, error) {
	switch kv[key] {
	case "1", "true":
		return true, nil
	case "0", "false", "":
		return false, nil
	default:
		return false, Fail(ErrIntegrity, "integrity_config_invalid",
			"repo_config %s is %q, which is not 0 or 1", key, kv[key])
	}
}

// LoadRepoConfig reads the configuration from its own snapshot. It exists
// because the Redactor is constructed from this configuration and therefore
// before the Ledger that injects it.
func LoadRepoConfig(ctx context.Context, s Store) (RepoConfig, error) {
	var raw map[string]string
	if err := s.Read(ctx, func(snap Snapshot) error {
		kv, err := snap.Config()
		raw = kv
		return err
	}); err != nil {
		return RepoConfig{}, err
	}
	return ParseRepoConfig(raw)
}

// Options is everything one invocation's Ledger needs. Actor is per-invocation
// rather than per-call because one `bdc` run is one actor by construction, and
// threading it through every command struct would be eleven chances to forget it.
type Options struct {
	Actor    Provenance
	Redactor Redactor
	Enricher Enricher // optional; nil disables enrichment entirely
	Config   RepoConfig
	Now      func() time.Time // nil uses time.Now
}

// Ledger owns lifecycle invariants, append-only behavior, idempotency,
// provenance requirements, policy decisions, transaction boundaries, and
// redaction sequencing. Command handlers never issue SQL and never see a Dolt
// concept.
type Ledger struct {
	store    Store
	redactor Redactor
	enricher Enricher
	actor    Provenance
	config   RepoConfig
	now      func() time.Time
}

// New panics on a missing store or redactor. Both are programming errors, and a
// Ledger with no redactor would write unredacted text to a store whose committed
// history cannot be rewritten — the one failure mode with no recovery.
func New(s Store, o Options) *Ledger {
	if s == nil {
		panic("beadcrumbs: ledger.New called with no store")
	}
	if o.Redactor == nil {
		panic("beadcrumbs: ledger.New called with no redactor; redaction runs before every persist")
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return &Ledger{
		store:    s,
		redactor: o.Redactor,
		enricher: o.Enricher,
		actor:    o.Actor,
		config:   o.Config,
		now:      o.Now,
	}
}

// Config exposes the repository policy the CLI reports in `bdc doctor`.
func (l *Ledger) Config() RepoConfig { return l.config }

// Actor is the provenance every write this Ledger performs will carry.
func (l *Ledger) Actor() Provenance { return l.actor }

// clock is UTC-truncated to microseconds, which is DATETIME(6)'s precision. A
// Go time.Time carries nanoseconds, so without this a value read back never
// equals the one written and every round-trip assertion becomes approximate.
func (l *Ledger) clock() time.Time { return l.now().UTC().Truncate(time.Microsecond) }
