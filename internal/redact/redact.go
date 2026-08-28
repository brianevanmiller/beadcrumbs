// Package redact removes secrets from free text before it reaches the ledger.
//
// It is the narrowest point at which "nothing unredacted reaches Dolt" can be
// asserted, and it matters more here than in most products: Dolt keeps
// committed history, so a secret that survives redaction is permanent. Nothing
// downstream can undo a miss, which is why an unresolvable finding aborts the
// write instead of degrading it.
//
// The package has no I/O. Its table tests are the whole proof.
package redact

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/brianevanmiller/beadcrumbs/internal/ledger"
)

// Finding is the ledger's finding type, not a parallel one. A Redactor must
// satisfy ledger.Redactor, whose signature already fixes this shape: rule id,
// offset, length, and replacement token — never the matched substring, which
// would defeat the redaction it reports.
type Finding = ledger.Finding

// Config is what a repository's own policy contributes. Version is recorded on
// every record whose content passed through this Redactor.
type Config struct {
	Version  string
	Patterns []string
}

// Redactor is immutable once built and safe to reuse across a whole command.
type Redactor struct {
	version string
	rules   []rule
}

// rule is one detector. group selects the submatch to replace, so a rule can
// match `PASSWORD=<secret>` for context while replacing only the secret.
// reject marks a shape we can detect but cannot bound — an unterminated private
// key block — where the only safe answer is to refuse the write.
type rule struct {
	id     string
	re     *regexp.Regexp
	group  int
	reject bool
	// admits lets a rule apply a second test to the captured group, which is how
	// `KEY=value` avoids redacting `${DB_PASSWORD}` and `PASSWORD=****`.
	admits func(string) bool
}

// builtins are high-confidence secret shapes: each one is a token format whose
// mere presence is the secret. The set grows; the sequence around it —
// detect, replace, re-scan, abort on anything left — is what the release gate
// tests, not this list.
func builtins() []rule {
	must := func(id, expr string, group int) rule {
		return rule{id: id, re: regexp.MustCompile(expr), group: group}
	}
	return []rule{
		// A terminated key block is bounded and replaceable.
		must("private-key-block", `(?s)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*?-----END [A-Z0-9 ]*PRIVATE KEY-----`, 0),
		// An unterminated one is not: we know a key starts here and cannot say
		// where it ends, so replacing anything would leave part of it behind.
		{id: "unbounded-private-key", re: regexp.MustCompile(`-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----`), reject: true},

		must("aws-access-key-id", `\b(?:AKIA|ASIA|ABIA|ACCA|AIDA|AROA|ANPA|ANVA|A3T[A-Z0-9])[A-Z0-9]{16}\b`, 0),
		must("gcp-api-key", `\bAIza[0-9A-Za-z_\-]{35}\b`, 0),
		must("github-token", `\bgh[pousr]_[A-Za-z0-9]{36,255}\b`, 0),
		must("github-pat", `\bgithub_pat_[A-Za-z0-9_]{22,255}\b`, 0),
		must("slack-token", `\bxox[baprse]-[A-Za-z0-9-]{10,}`, 0),
		must("slack-webhook", `https://hooks\.slack\.com/services/[A-Za-z0-9/+_-]{20,}`, 0),
		must("stripe-key", `\b[sr]k_(?:live|test)_[0-9A-Za-z]{16,}\b`, 0),
		must("npm-token", `\bnpm_[A-Za-z0-9]{36}\b`, 0),
		must("anthropic-key", `\bsk-ant-[A-Za-z0-9_\-]{20,}`, 0),
		must("openai-key", `\bsk-(?:proj-)?[A-Za-z0-9_\-]{32,}`, 0),
		must("jwt", `\beyJ[A-Za-z0-9_\-]{8,}\.[A-Za-z0-9_\-]{8,}\.[A-Za-z0-9_\-]{8,}`, 0),
		must("bearer-token", `(?i)\bbearer\s+([A-Za-z0-9._~+/=\-]{20,})`, 1),
		// Only the credential half of a URL is replaced: the scheme and host are
		// how a reader knows which system the secret belonged to.
		{
			id:    "url-credentials",
			re:    regexp.MustCompile(`\b[a-z][a-z0-9+.\-]*://[^\s/:@]+:([^\s/@]{1,256})@`),
			group: 1, admits: isSecretValue,
		},
		{
			id:    "assigned-secret",
			re:    regexp.MustCompile(`(?i)\b[A-Z0-9_]*(?:SECRET|TOKEN|PASSWORD|PASSWD|API_?KEY|ACCESS_KEY|PRIVATE_KEY|CREDENTIALS?)[A-Z0-9_]*\s*[:=]\s*["']?([^\s"'\n]{6,})`),
			group: 1, admits: isSecretValue,
		},
	}
}

// isSecretValue rejects the values that look like assignments but carry no
// secret: a template reference, a placeholder, or an already-masked value.
// Redacting those would fill the ledger with noise and teach readers to ignore
// the token that matters.
func isSecretValue(v string) bool {
	switch {
	case strings.HasPrefix(v, "${"), strings.HasPrefix(v, "$("), strings.HasPrefix(v, "<"):
		return false
	case strings.HasPrefix(v, "[REDACTED:"):
		return false
	}
	distinct := map[rune]struct{}{}
	for _, r := range v {
		distinct[r] = struct{}{}
	}
	// "****", "xxxxxx", "......" are placeholders, not credentials.
	return len(distinct) > 2
}

// New compiles the repository's configured patterns onto the builtin set. A
// pattern that does not compile is an integrity error rather than a skipped
// rule: a repository that asked for a redaction and silently did not get one is
// the failure this package exists to prevent.
func New(cfg Config) (*Redactor, error) {
	if cfg.Version == "" {
		return nil, ledger.Fail(ledger.ErrIntegrity, "integrity_config_missing",
			"a redaction version is required; it is recorded on every record this redactor clears")
	}
	rules := builtins()
	for i, pattern := range cfg.Patterns {
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, ledger.FailWith(ledger.ErrIntegrity, "integrity_config_invalid", err,
				"repo_config %s entry %d is not a valid regular expression", ledger.ConfigRedactPatterns, i)
		}
		rules = append(rules, rule{id: fmt.Sprintf("configured-%d", i), re: re})
	}
	return &Redactor{version: cfg.Version, rules: rules}, nil
}

func (r *Redactor) Version() string { return r.version }

// Redact replaces every detected secret with `[REDACTED:<rule>]` and returns the
// clean text with one Finding per replacement.
//
// The sequence is detect -> replace -> re-scan. The re-scan is a live assertion,
// not a belt-and-braces flourish: it is the only thing that can catch a rule
// whose replacement leaves the secret partly intact, and a configured pattern
// makes that reachable in production. Anything still matching afterwards means
// the redaction did not converge, and the caller must abort rather than persist.
func (r *Redactor) Redact(text string) (string, []Finding, error) {
	if text == "" {
		return "", nil, nil
	}
	spans, err := r.scan(text)
	if err != nil {
		return "", nil, err
	}
	if len(spans) == 0 {
		return text, nil, nil
	}

	var (
		b        strings.Builder
		findings []Finding
		cursor   int
	)
	for _, s := range spans {
		replacement := "[REDACTED:" + s.rule + "]"
		b.WriteString(text[cursor:s.start])
		b.WriteString(replacement)
		cursor = s.end
		findings = append(findings, Finding{
			Rule: s.rule, Offset: s.start, Length: s.end - s.start, Replacement: replacement,
		})
	}
	b.WriteString(text[cursor:])
	clean := b.String()

	residue, err := r.scan(clean)
	if err != nil {
		return "", nil, err
	}
	if len(residue) > 0 {
		return "", nil, ledger.Fail(ledger.ErrRedaction, "redaction_failed",
			"rule %q still matches after redaction, so the value cannot be confidently replaced; nothing was written",
			residue[0].rule)
	}
	return clean, findings, nil
}

// span is one replacement site in the original text.
type span struct {
	start, end int
	rule       string
}

// scan collects every rule's matches and resolves overlaps by taking the
// earliest, then the longest. Overlapping detections are the normal case — a
// private key block also contains base64 that other rules like — and picking the
// widest span is what guarantees the replacement covers the whole secret.
//
// Reject rules are evaluated last and only outside the covered spans. A
// terminated key block is replaced by the rule above it, so the BEGIN marker
// inside it is already handled; only a marker no span covers means we found a
// secret we cannot bound.
func (r *Redactor) scan(text string) ([]span, error) {
	var spans []span
	for _, rl := range r.rules {
		if rl.reject {
			continue
		}
		for _, m := range rl.re.FindAllStringSubmatchIndex(text, -1) {
			start, end := m[0], m[1]
			if rl.group > 0 {
				if 2*rl.group+1 >= len(m) || m[2*rl.group] < 0 {
					continue
				}
				start, end = m[2*rl.group], m[2*rl.group+1]
			}
			if rl.admits != nil && !rl.admits(text[start:end]) {
				continue
			}
			if start < end {
				spans = append(spans, span{start: start, end: end, rule: rl.id})
			}
		}
	}
	// Stable, so when two rules produce the same span the earlier rule in
	// builtins() names the finding and the output is deterministic.
	sort.SliceStable(spans, func(i, j int) bool {
		if spans[i].start != spans[j].start {
			return spans[i].start < spans[j].start
		}
		return spans[i].end > spans[j].end
	})

	var merged []span
	for _, s := range spans {
		if n := len(merged); n > 0 && s.start < merged[n-1].end {
			if s.end > merged[n-1].end {
				merged[n-1].end = s.end
			}
			continue
		}
		merged = append(merged, s)
	}

	for _, rl := range r.rules {
		if !rl.reject {
			continue
		}
		for _, m := range rl.re.FindAllStringIndex(text, -1) {
			if covered(merged, m[0]) {
				continue
			}
			return nil, ledger.Fail(ledger.ErrRedaction, "redaction_failed",
				"rule %q matched a secret this build cannot bound, so it cannot be confidently replaced; nothing was written",
				rl.id)
		}
	}
	return merged, nil
}

func covered(spans []span, offset int) bool {
	for _, s := range spans {
		if offset >= s.start && offset < s.end {
			return true
		}
	}
	return false
}

// Entropy is Shannon entropy in bits per character. It is exported because the
// transcript and secret heuristics in the ledger want the same measure, and two
// implementations of "how random is this string" would drift.
func Entropy(s string) float64 {
	if s == "" {
		return 0
	}
	counts := map[rune]int{}
	total := 0
	for _, r := range s {
		if unicode.IsSpace(r) {
			continue
		}
		counts[r]++
		total++
	}
	if total == 0 {
		return 0
	}
	var h float64
	for _, n := range counts {
		p := float64(n) / float64(total)
		h -= p * math.Log2(p)
	}
	return h
}
