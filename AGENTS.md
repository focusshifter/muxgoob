# Repository Guidelines

## Project Structure & Module Organization
- `muxgoob.go`: main entrypoint (Telegram bot using Telebot).
- `plugins/`: feature plugins (each package registers itself via `init()`); tests live next to code as `*_test.go`.
- `registry/`: plugin/bot interfaces, config loading, shared state.
- `database/`: SQLite setup and helpers; DB file lives in `db/muxgoob.sqlite` (folder ignored by Git).
- `cmd/`: auxiliary commands (e.g., `migrate`, `import`, `selfprompt`, `checkdb`).
- `config.yml`: local config (ignored). See `config.yml.dist` for fields.

## Build, Test, and Development Commands
- Build: `go build -o muxgoob ./` — produces the bot binary.
- Run locally: `cp config.yml.dist config.yml && ./muxgoob` — fill secrets first.
- Tests: `go test ./...` — runs all package tests.
- Vet/format: `go vet ./...` and `go fmt ./...` — keep code idiomatic.
- Tools: Go 1.19+ (per `go.mod`). Optional: Dev Container in `.devcontainer/`.
- Utilities:
  - Import Telegram JSON: `go run ./cmd/import -input /path/to/json -db db/muxgoob.sqlite`
  - Legacy → SQLite: `go run ./cmd/migrate`
  - Self-prompt batches: `go run ./cmd/selfprompt -chat <id> -config config.yml`

## Coding Style & Naming Conventions
- Go formatting is mandatory (`go fmt`). Prefer `go vet` before PRs.
- Package and file names are lower-case; tests end with `_test.go`.
- Keep functions small; avoid global state outside `registry` and `database`.
- Plugins implement `registry.MuxPlugin` with `Start(any)` and `Process(*telebot.Message)`; register in `init()` and add a blank import in `muxgoob.go`.

## Testing Guidelines
- Use the standard `testing` package. Name tests `TestXxx` in `*_test.go`.
- For DB interactions, prefer `utils/testutils.SetupTestDB(t)` to isolate state.
- Add tests for new behavior and edge cases; ensure `go test ./...` passes.
- Always run `go test ./...` after making changes.

## Commit & Pull Request Guidelines
- Commits: short, imperative messages (e.g., “Add spotify review history”).
- Before PR: run `go fmt`, `go vet`, `go test ./...` and include a brief description, reproduction steps, and screenshots/logs if UI-like output is relevant.
- Keep PRs focused; link related issues. Do not commit `config.yml` or files under `db/` (both are ignored).

## Security & Configuration Tips
- Never commit secrets. Copy `config.yml.dist` → `config.yml` and fill `telegram_key`, API keys, and plugin settings.
- SQLite WAL is enabled; keep `db/` on a local disk for best reliability.
