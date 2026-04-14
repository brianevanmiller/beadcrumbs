# Changelog

## v0.10.0 — 2026-04-14

- Architecture improvements inspired by beads 1.0: storage interface extraction, read-only mode for query commands, content hashing for insight dedup, `--json` output on all commands, and `bdc doctor` health check command
- Fix content_hash not being read back from DB in query paths
- Fix pre-existing macOS symlink test failure in TestFindBeadcrumbsDir_Present

## v0.9.0 — 2026-02-24

- Add version and upgrade commands
- GitHub PR comment integration with shared summary formatter
