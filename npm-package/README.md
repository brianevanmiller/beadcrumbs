# @beadcrumbs/bdc

npm distribution of **bdc** — a repository-local reasoning ledger for humans and coding agents.

```bash
npm install -g @beadcrumbs/bdc
```

Installing runs `scripts/postinstall.js`, which downloads the prebuilt release archive for your
platform, verifies its SHA-256 against the release's `checksums.txt`, and unpacks one binary.

**macOS and Linux only, on arm64 and amd64.** Windows is not supported. There is no source-build
fallback: `bdc` needs Go >= 1.26.2, CGO, and ICU4C, and a source build links ICU dynamically
against a prefix that will not exist on another machine. On an unsupported platform the install
fails with instructions instead.

The binary is ~135 MB — it embeds Dolt with statically linked ICU.

Documentation, source, and issues: <https://github.com/brianevanmiller/beadcrumbs>
