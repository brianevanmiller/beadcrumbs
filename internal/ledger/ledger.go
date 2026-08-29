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

// ParseRepoConfig reads the raw key/value rows. A value that is missing or does
// not parse is an integrity error rather than a default: silently treating
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

// parseFlag reads one boolean policy key. An absent key is as much an integrity
// error as an unparseable one: the seeds write every key, so a missing one means
// this is not a ledger this build initialised.
func parseFlag(kv map[string]string, key string) (bool, error) {
	raw, ok := kv[key]
	if !ok {
		return false, Fail(ErrIntegrity, "integrity_config_missing",
			"repo_config is missing %s; the ledger was not initialised by this build", key)
	}
	switch raw {
	case "1", "true":
		return true, nil
	case "0", "false":
		return false, nil
	default:
		return false, Fail(ErrIntegrity, "integrity_config_invalid",
			"repo_config %s is %q, which is not 0 or 1", key, raw)
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
// rather than per-call because one `bdc` run is one actor by construction.
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

// SetHarvestAuto records the per-repository automatic-harvest policy that
// `bdc hooks install --auto-harvest` opts into and `bdc hooks uninstall` opts
// out of. It changes policy only: nothing already captured, harvested, or
// promoted is touched. The cached config moves with the write, so a later
// Config() in the same process does not report the value it replaced.
func (l *Ledger) SetHarvestAuto(ctx context.Context, on bool) error {
	value := "0"
	if on {
		value = "1"
	}
	if err := l.store.Write(ctx, func(tx Tx) error {
		return tx.SetConfig(ConfigHarvestAuto, value)
	}); err != nil {
		return err
	}
	l.config.HarvestAuto = on
	return nil
}

// clock is UTC-truncated to microseconds, which is DATETIME(6)'s precision. A
// Go time.Time carries nanoseconds, so without this a value read back never
// equals the one written and every round-trip assertion becomes approximate.
func (l *Ledger) clock() time.Time { return l.now().UTC().Truncate(time.Microsecond) }
