package redact_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/brianevanmiller/beadcrumbs/internal/ledger"
	"github.com/brianevanmiller/beadcrumbs/internal/redact"
)

// The fixtures below are fake, but they are shaped like the real thing on
// purpose: a redactor is only proven by values its own rules actually match.
// That shape is also exactly what secret scanners look for, and GitHub push
// protection rejects a push over a scanner-shaped literal in source — even a
// fake one, even in a test. So every fixture is assembled at run time from
// fragments that individually match nothing. The assembled value is identical
// to the literal, so the rules are tested at full strength.
func join(parts ...string) string { return strings.Join(parts, "") }

var (
	awsKeyID     = join("AKIA", "IOSFODNN7EXAMPLE")
	githubToken  = join("gh", "p", "_016C7e42F292c6912E7710c838347Ae178B4a")
	githubPAT    = join("github", "_pat", "_11ABCDEFG0abcdefghijkl_ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789ab")
	slackToken   = join("xo", "xb", "-2334300-2340492034-fjaklfjaklfjkla")
	googleAPIKey = join("AI", "za", "SyD-1234567890abcdefghijklmnopqrstu")
	stripeKey    = join("sk", "_live", "_4eC39HqLyjWDarjtT1zdp7dc")
	anthropicKey = join("sk", "-ant-", "api03-AAAAAAAAAAAAAAAAAAAAAAAAAA")
	npmToken     = join("npm", "_", "abcdefghijklmnopqrstuvwxyz0123456789")
	jwtToken     = join("ey", "J", "hbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dBjftJeZ4CVPmB92K27uhbUJU1p1r_wW1gFWFOEjXk")
	slackHookURL = join("https://hooks", ".slack", ".com/services/")
	rsaKeyBegin  = join("-----BEGIN ", "RSA PRIVATE KEY", "-----")
	rsaKeyEnd    = join("-----END ", "RSA PRIVATE KEY", "-----")
	sshKeyBegin  = join("-----BEGIN ", "OPENSSH PRIVATE KEY", "-----")
)

func newRedactor(t *testing.T, patterns ...string) *redact.Redactor {
	t.Helper()
	r, err := redact.New(redact.Config{Version: "1", Patterns: patterns})
	if err != nil {
		t.Fatalf("building the redactor: %v", err)
	}
	return r
}

// The secret shapes this build claims to detect. Each case states the secret
// separately from the text around it and the text is assembled from the two, so
// the assertion can be "the secret is gone" — the only property that matters,
// unlike "the output equals a golden string", which would pass while leaving
// the secret in a different position.
func TestRedactsKnownSecretShapes(t *testing.T) {
	cases := []struct {
		name   string
		before string
		secret string
		after  string
		rule   string
	}{
		{
			name:   "aws access key id",
			before: "the deploy used ",
			secret: awsKeyID,
			after:  " and then failed",
			rule:   "aws-access-key-id",
		},
		{
			name:   "github personal access token",
			before: "gh auth login --with-token ",
			secret: githubToken,
			rule:   "github-token",
		},
		{
			name:   "github fine-grained pat",
			before: "token ",
			secret: githubPAT,
			rule:   "github-pat",
		},
		{
			name:   "slack bot token",
			before: "SLACK=",
			secret: slackToken,
			rule:   "slack-token",
		},
		{
			name:   "google api key",
			before: "maps key ",
			secret: googleAPIKey,
			after:  " done",
			rule:   "gcp-api-key",
		},
		{
			name:   "stripe live key",
			before: "charge with ",
			secret: stripeKey,
			after:  " now",
			rule:   "stripe-key",
		},
		{
			name:   "anthropic key",
			before: "export ANTHROPIC=",
			secret: anthropicKey,
			rule:   "anthropic-key",
		},
		{
			name:   "npm token",
			before: "//registry.npmjs.org/:_authToken=",
			secret: npmToken,
			rule:   "npm-token",
		},
		{
			name:   "jwt",
			before: "Cookie: session=",
			secret: jwtToken,
			rule:   "jwt",
		},
		{
			name:   "bearer token",
			before: "curl -H 'Authorization: Bearer ",
			secret: "aG93ZHkgdGhlcmUgc3RyYW5nZXI9PT0",
			after:  "'",
			rule:   "bearer-token",
		},
		{
			name:   "database url credentials",
			before: "DATABASE_URL points at postgres://svc:",
			secret: "h4nd0ffPa55",
			after:  "@db.internal:5432/app",
			rule:   "url-credentials",
		},
		{
			name:   "assigned secret",
			before: "we set STRIPE_WEBHOOK_SECRET=",
			secret: "whsec_9f2b1c4d7a",
			after:  " and moved on",
			rule:   "assigned-secret",
		},
		{
			name:   "slack webhook url",
			before: "posts to " + slackHookURL,
			secret: "T00000000/B00000000/XXXXXXXXXXXXXXXXXXXXXXXX",
			rule:   "slack-webhook",
		},
		{
			name:   "private key block",
			before: "the key was\n" + rsaKeyBegin + "\n",
			// The base64 body is the part that must not survive.
			secret: "MIIBOgIBAAJBAKj34GkxFhD",
			after:  "\n" + rsaKeyEnd + "\nand it leaked",
			rule:   "private-key-block",
		},
	}

	r := newRedactor(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			text := tc.before + tc.secret + tc.after
			clean, findings, err := r.Redact(text)
			if err != nil {
				t.Fatalf("redacting: %v", err)
			}
			if strings.Contains(clean, tc.secret) {
				t.Fatalf("the secret survived redaction in %q", clean)
			}
			if len(findings) == 0 {
				t.Fatal("a redaction that changed the text reported no finding")
			}
			var rules []string
			for _, f := range findings {
				rules = append(rules, f.Rule)
			}
			if !contains(rules, tc.rule) {
				t.Fatalf("expected rule %q to fire, got %v", tc.rule, rules)
			}
		})
	}
}

// Text that merely looks like a secret must survive intact. A redactor that
// eats ordinary prose gets turned off, and a redactor that is off is the
// failure mode this whole package exists to prevent.
func TestLeavesOrdinaryTextAlone(t *testing.T) {
	r := newRedactor(t)
	for _, text := range []string{
		"the deploy failed because the migration ran twice",
		"PASSWORD=${DB_PASSWORD} is how the compose file refers to it",
		"set API_KEY=<your-key-here> in the example config",
		"the log showed PASSWORD=******** which told us nothing",
		"see docs/adr/0007-crumb-retention.md for the reasoning",
	} {
		clean, findings, err := r.Redact(text)
		if err != nil {
			t.Fatalf("redacting %q: %v", text, err)
		}
		if clean != text || len(findings) != 0 {
			t.Fatalf("ordinary text was rewritten:\n  in:  %q\n  out: %q\n  findings: %+v", text, clean, findings)
		}
	}
}

// A Finding names where a secret was and what replaced it. It must never carry
// the secret itself: findings travel in warnings[], in error messages, and in
// logs, and Dolt keeps committed history, so one quoted secret is permanent.
func TestFindingsCarryNoMatchedText(t *testing.T) {
	secret := githubToken
	r := newRedactor(t)
	_, findings, err := r.Redact("pushed with " + secret)
	if err != nil {
		t.Fatalf("redacting: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("expected one finding, got %d", len(findings))
	}
	f := findings[0]
	for _, field := range []string{f.Rule, f.Replacement} {
		if strings.Contains(field, secret) || strings.Contains(secret, field) {
			t.Fatalf("a finding quoted the secret: %q", field)
		}
	}
	if f.Offset != len("pushed with ") || f.Length != len(secret) {
		t.Fatalf("finding located the secret at %d+%d, want %d+%d",
			f.Offset, f.Length, len("pushed with "), len(secret))
	}
}

// A secret we can detect but cannot bound is the one case where the honest
// answer is to refuse. Partial redaction is never written.
func TestUnboundedSecretAbortsInsteadOfPartiallyRedacting(t *testing.T) {
	const body = "b3BlbnNzaC1rZXktdjEAAA"
	r := newRedactor(t)
	_, _, err := r.Redact("pasted half of it:\n" + sshKeyBegin + "\n" + body)
	if !errors.Is(err, ledger.ErrRedaction) {
		t.Fatalf("expected a redaction abort, got %v", err)
	}
	if strings.Contains(err.Error(), body) {
		t.Fatalf("the abort message quoted the secret: %v", err)
	}
}

// Redaction has to converge: whatever the rules leave behind is what gets
// written, and Dolt cannot rewrite it later. A configured pattern that matches
// its own replacement token is the reachable way to break that, and the answer
// is a loud abort rather than a silent loop.
func TestNonConvergentRuleAbortsTheWrite(t *testing.T) {
	r := newRedactor(t, "REDACTED|hunter2")
	if _, _, err := r.Redact("the password is hunter2"); !errors.Is(err, ledger.ErrRedaction) {
		t.Fatalf("expected a redaction abort, got %v", err)
	}
}

func TestConfiguredPatternsRedact(t *testing.T) {
	r := newRedactor(t, `EMP-[0-9]{6}`)
	clean, findings, err := r.Redact("raised by EMP-004417 in review")
	if err != nil {
		t.Fatalf("redacting: %v", err)
	}
	if strings.Contains(clean, "EMP-004417") {
		t.Fatalf("the configured pattern did not redact: %q", clean)
	}
	if len(findings) != 1 || findings[0].Rule != "configured-0" {
		t.Fatalf("expected one configured-0 finding, got %+v", findings)
	}
}

// An unparseable pattern is an integrity error, not a skipped rule: a
// repository that asked for a redaction and silently did not get one is exactly
// the failure this package prevents.
func TestInvalidConfiguredPatternIsAnIntegrityError(t *testing.T) {
	_, err := redact.New(redact.Config{Version: "1", Patterns: []string{"("}})
	if !errors.Is(err, ledger.ErrIntegrity) {
		t.Fatalf("expected an integrity error, got %v", err)
	}
}

// Redaction is idempotent: running it over already-clean text changes nothing.
// The ledger relies on this to assert, at the moment of writing, that the value
// it is about to persist is the redactor's own output.
func TestRedactionIsIdempotent(t *testing.T) {
	r := newRedactor(t)
	once, _, err := r.Redact("token " + githubToken + " and postgres://u:p4ssw0rd1@h/d")
	if err != nil {
		t.Fatalf("redacting: %v", err)
	}
	twice, findings, err := r.Redact(once)
	if err != nil {
		t.Fatalf("re-redacting: %v", err)
	}
	if twice != once || len(findings) != 0 {
		t.Fatalf("redaction is not idempotent:\n  once:  %q\n  twice: %q\n  findings: %+v", once, twice, findings)
	}
}

func contains(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}
