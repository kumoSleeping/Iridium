# Iridium

A lightweight Go rewrite of Rhodium: one web page (plus the `ir` CLI) that hosts and discovers the local Claude Code, Codex, pi and DeepSeek Harness (dsh) sessions. The plan is `docs/plan.md` (Chinese); read it first.

## Rules

- **Language.** Docs in Chinese, short. Code, identifiers, comments, commit messages in English. Commits end with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
- **Reference.** Rhodium's source (TypeScript/Rust) is at `/home/iridium/kumo/rhodium-src/Rhodium`: protocol facts in `docs/architecture.md`, recorded agent traffic in `test/fixtures/`, the web app in `web/src/`. Reuse its findings and fixtures; do not import its code at runtime.
- **One binary.** `cmd/iridium` is the daemon; invoked as `ir` it is the CLI; `iridium hold` is the holder. Go 1.27 (`~/.local/bin/go`), pure Go (no cgo). Frontend in `web/` (React + Tailwind 4 + Base UI, built with Node 24 / npm), embedded into the binary.
- **Don't touch the live setup.** Never edit anything under `~/.claude`, `~/.codex`, `~/.pi`, `~/.config` that the user owns; read transcripts only. Leave the running Rhodium instance, its gateway and the system `cloudflared` alone. Never kill processes you did not start.
- **Nodes.** The main node is this account: it runs release builds only (https://ir.kumo.ltd, `127.0.0.1:7810`), updated through a GitHub release and the settings page, never by `deploy.sh` or a pushed build. Claude (the user's own login) runs only there. The test node is the account `ir` on this machine (https://ir-test.kumo.ltd, `127.0.0.1:7811`): `scripts/push-test.sh` builds the tree and installs it there. It has no real Claude: its Claude Code runs against DeepSeek (user, 2026-11-06).
- **Isolation.** Tests use temp dirs for state and fake agents/fixtures. systemd units created by tests are named `iridium-test-*`; tests use random ports.
- **Quota.** Day-to-day tests use fixtures and fake agents. Real runs go to the test node; there Claude Code talks to DeepSeek. On the main node, only the cheapest models (user, 2026-11-05): Claude `haiku`; pi via opencode-go with DeepSeek Flash (never a Claude model through pi); Codex with ChatGPT's Luna model at the lowest effort. Look up the exact model ids with each CLI's own model list. Claude must run under the user's own login, unmodified, never with `--bare`.
- **Security.** Never print tokens, cookies or passkey material. Public exposure requires the Passkey gate to be on.
- **Tests.** `go test ./...` and `go vet ./...` must pass before a commit; frontend: `npm run check` in `web/`.
