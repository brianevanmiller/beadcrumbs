# Supply-chain audit: the SQLite → Dolt dependency swap

**Date**: 2026-08-28

**Scope**: the v1.0.0 release gate "dependency changes get a supply-chain audit"
([plan §6, Release](../agent-docs/2026-08-28-dolt-reasoning-ledger-v1-plan.md)). Measured on the
`feat/dolt-reasoning-ledger-v1` branch with go1.27.0 on darwin/arm64.

**Verdict**: ship it. Zero reachable vulnerabilities, no copyleft obligation that
survives static linking, and one deliberate accepted cost — the graph is 7× larger.

## What changed

| | 0.10.0 (`main`) | 1.0.0 |
|---|---|---|
| Store | `modernc.org/sqlite` v1.28.0 (pure Go) | `github.com/dolthub/driver` v1.88.1 (embedded Dolt, CGO) |
| Go floor | 1.22 | 1.26.2 — imposed by `dolthub/driver`, not chosen |
| Modules in `go.mod` | 21 | 159 |
| Modules linked into the binary | — | 154 |
| Direct requires | 2 | 5 (`dolthub/driver`, `dolthub/dolt/go`, `spf13/cobra`, `google/uuid`, `cenkalti/backoff/v4`) |
| Binary | 13.5 MB, no CGO | ~135 MB stripped with `-tags icu_static` |

The whole increase is `dolthub/dolt/go`, which vendors go-mysql-server, its own
storage layer, and their transitive graph. There is no smaller embedded Dolt: the
driver is the front door to the full engine. That was weighed and accepted when
the operating model was chosen
([Dolt operating model research](2026-08-28-dolt-operating-model-research.md)), and
this audit does not reopen it.

`modernc.org/*` is gone entirely — the SQLite graph and the Dolt graph share
nothing, so the swap is a replacement rather than an addition.

## Vulnerabilities

`govulncheck` v1.7.0 against `vuln.go.dev` (db last modified 2026-08-28), symbol
scan over `./...`.

**The first run failed the gate**: 5 vulnerabilities the binary can actually reach,
in three modules Dolt pulls in.

| ID | Module | Fixed in |
|---|---|---|
| GO-2026-4559 (CVE-2026-27141) | `golang.org/x/net` | v0.51.0 |
| GO-2026-4918 (CVE-2026-33814) | `golang.org/x/net` | v0.53.0 |
| GO-2026-5026 (CVE-2026-39821) | `golang.org/x/net` | v0.55.0 |
| GO-2026-5970 (CVE-2026-56852) | `golang.org/x/text` | v0.39.0 |
| GO-2026-6061 (GHSA-hrxh-6v49-42gf) | `google.golang.org/grpc` | v1.82.1 |

Every one is an HTTP/2 or server-side path that `bdc` — which opens an embedded
engine and never listens on a socket — has no plausible way to exercise. That
argument was not accepted as a disposition. Reachability by static analysis is the
gate; "we believe it is unreachable at runtime" is the reasoning that ships CVEs.

The modules were bumped instead:

```
golang.org/x/net    v0.50.0 → v0.57.0
golang.org/x/text   v0.35.0 → v0.41.0
golang.org/x/crypto v0.48.0 → v0.55.0
google.golang.org/grpc          v1.79.3 → v1.82.1
go.opentelemetry.io/otel        v1.43.0 → v1.44.0
github.com/klauspost/compress   v1.18.0 → v1.18.7
```

All indirect; no direct requirement moved and no API changed.
`go build ./... && go vet ./... && go test ./...` and `go test -race ./...` are green
on the bumped graph, and the re-scan reports **0 reachable and 0 imported**
vulnerabilities.

One finding remains and is accepted: **GO-2026-5932**, `golang.org/x/crypto/openpgp`
is unmaintained and has no fixed version. It is present in a required module and
neither imported nor called. Nothing can be done about it short of Dolt dropping the
dependency, and there is no exposure to accept away.

## Licenses

154 linked modules, classified from each module's `LICENSE` file in the module cache.

| License | Modules |
|---|---|
| Apache-2.0 | 76 |
| MIT | 40 |
| BSD (2- and 3-clause) | 31 |
| MPL-2.0 | 3 |
| LGPL-3.0 with a linking exception | 1 |
| CC0-1.0 / Unlicense / WTFPL | 3 |

Nothing is GPL. Two entries need a named disposition rather than a count:

- **`github.com/dolthub/fslock` — LGPL-3.0.** The only copyleft license in the
  graph, and the one that would matter, because a 135 MB statically linked binary
  is exactly the "Combined Work" LGPL3 §4 constrains. Its LICENSE carries an
  explicit exception: *"the copyright holders of this Library give you permission
  to convey to a third party a Combined Work that links statically or dynamically
  to this Library without providing any Minimal Corresponding Source"*. Distributing
  the release binaries therefore carries no relinking or source-provision
  obligation. Beadcrumbs is MIT and stays MIT.
- **MPL-2.0** (`go-sql-driver/mysql`, `hashicorp/golang-lru` v1 and v2) is
  file-level copyleft. None of those files is modified, so the obligation is
  satisfied by not modifying them.

ICU4C is statically linked into the release binary and is not a Go module. It ships
under the Unicode license, which is permissive and requires attribution only.

## What is enforced, and where

- `.github/workflows/ci.yml`, job `supply-chain`: `govulncheck ./...` on every push
  and pull request. A reachable vulnerability fails the build; a module-only one is
  reported. The same job prints the `go list -m all` delta against the PR base, so a
  dependency change is reviewable as a diff rather than as a claim.
- The swap itself is one reviewable commit per slice, with `go.mod`/`go.sum` moving
  only in S1 (the swap) and S10 (this audit's bumps).
