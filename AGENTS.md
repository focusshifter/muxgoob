# Muxgoob Agent Guide

Muxgoob is a Go 1.19 Telegram bot built on Telebot, with a plugin-first architecture, SQLite as the primary datastore, and a small layer of legacy StormDB support that still matters in a few paths.

## At a Glance

- `muxgoob.go` is the runtime entrypoint: it loads config, initializes SQLite, opens the legacy StormDB, creates the Telebot instance, starts plugins, and fans messages out to every plugin.
- `plugins/` is the product surface. Each plugin is its own package, self-registers in `init()`, and is activated via a blank import in `muxgoob.go`.
- `registry/` holds shared runtime state: plugin registration, config loading, DB-backed settings, and the bot wrapper used in tests.
- `database/` owns SQLite setup, connection behavior, retries, and schema bootstrap.
- `cmd/` contains utility entrypoints such as `migrate`, `import`, `selfprompt`, and `checkdb`.
- `utils/testutils/` provides test helpers, especially `SetupTestDB(t)` for isolated SQLite-backed tests.

## Working Model

- The bot token used at runtime comes from `config.yml` via `registry.Config.TelegramKey`; keep `config.yml.dist` aligned with any config changes.
- SQLite lives at `db/muxgoob.sqlite` and is opened with WAL mode plus a small connection pool for concurrent reads/writes.
- Legacy StormDB lives at `db/muxgoob.db`; keep `Start(...)` compatibility intact because some older plugin code still receives and uses that handle.
- Incoming text messages are persisted before plugin processing, so message-related features can often rely on DB state already existing.
- Plugin `Process(...)` calls are launched in goroutines; prefer small, concurrency-safe logic and avoid fragile shared mutable state.

## Repo Map

- `muxgoob.go` - boot sequence and Telebot wiring.
- `plugins/admin/` - admin-style commands.
- `plugins/birthdays/` - birthday reminders and yearly notification tracking.
- `plugins/dupelink/` - duplicate link detection.
- `plugins/logwrite/` - message logging, including legacy Storm-backed behavior.
- `plugins/nametrigger/` - username-triggered replies.
- `plugins/promptmgr/` and `plugins/selfprompt/` - AI prompt management flows.
- `plugins/spotify/` - Spotify review and preview features.
- `plugins/twitchstreams/` - Twitch stream monitoring.
- `plugins/version/` - simple version reporting.

## Plugin Rules

When adding or changing a plugin, follow the existing pattern:

1. Create or update `plugins/<name>/`.
2. Implement `registry.MuxPlugin`:
   - `Start(interface{})`
   - `Process(*telebot.Message)`
3. Register the plugin in `init()` with `registry.RegisterPlugin(...)`.
4. Add a blank import in `muxgoob.go` so the plugin is loaded.
5. If the plugin needs config, extend `registry.Configuration` and update `config.yml.dist`.
6. Add tests next to the plugin as `*_test.go`.

Prefer `database.DB` for new persistence work. Only lean on StormDB when touching code that already depends on it.

## Database Notes

- `database.Initialize()` is the source of truth for SQLite schema bootstrap.
- Schema changes should be idempotent; use `CREATE TABLE IF NOT EXISTS`, `CREATE INDEX IF NOT EXISTS`, and defensive `ALTER TABLE` patterns where needed.
- Do not commit local database files under `db/`.
- For tests, use `utils/testutils.SetupTestDB(t)` and create only the tables your test needs.

## Core Commands

- Build: `go build -o muxgoob ./`
- Run tests: `go test ./...`
- Format: `go fmt ./...`
- Vet: `go vet ./...`
- Local run: `cp config.yml.dist config.yml` then fill secrets and run `./muxgoob`
- Import Telegram JSON: `go run ./cmd/import -input /path/to/json -db db/muxgoob.sqlite`
- Legacy migration: `go run ./cmd/migrate`
- Self-prompt batch: `go run ./cmd/selfprompt -chat <id> -config config.yml`

## Coding Style

- Keep packages and filenames lower-case.
- Use standard Go formatting with `go fmt`.
- Keep functions focused and avoid adding new global state outside `registry` and `database`.
- Match existing naming and flow before introducing new abstractions.
- Add comments only when the code would otherwise be hard to reason about.

## Testing and Validation

Minimum bar after code changes:

- `go fmt ./...`
- `go test ./...`

Also run these when relevant:

- `go vet ./...` for broader correctness checks.
- `go build -o muxgoob ./` when imports, boot wiring, or plugin registration change.
- Targeted plugin tests while iterating, then finish with the full test suite.

## Safety Rails

- Never commit `config.yml`, secrets, API keys, or anything under `db/`.
- Treat `config.yml.dist` as documentation for required config shape.
- Preserve existing plugin registration and blank imports unless the task is explicitly removing a plugin.
- Be careful with schema edits and message-processing concurrency; subtle breakage here affects the whole bot.

## Release Notes

- When creating a new release tag, increment the patch version by default unless explicitly asked to bump minor or major.

## Change Checklist

Before wrapping up, verify the following:

- New or changed plugin is registered correctly.
- Any new config fields exist in both `registry.Configuration` and `config.yml.dist`.
- Tests live beside the affected package.
- Formatting and tests pass.
- No local secrets or DB artifacts are staged.
