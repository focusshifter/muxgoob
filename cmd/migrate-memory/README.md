# Structured-memory migration

Run the migration while the bot is stopped so `prompts` and `person_facts` cannot change between backup, import, verification, and cutover.

## Procedure

```bash
# 1. Inspect candidates; this does not write migration data.
mise exec -- go run ./cmd/migrate-memory --db db/muxgoob.sqlite

# Optional: inspect one chat only.
mise exec -- go run ./cmd/migrate-memory --db db/muxgoob.sqlite --chat CHAT_ID

# 2. Back up, import, verify, and cut over verified chats.
mise exec -- go run ./cmd/migrate-memory --db db/muxgoob.sqlite --apply

# 3. Re-run verification independently.
mise exec -- go run ./cmd/migrate-memory --db db/muxgoob.sqlite --verify
```

`--apply` creates a timestamped SQLite backup before writing. It stores SHA-256-verified raw snapshots of every legacy prompt and person-fact row. Cutover changes only `memory_migration_scopes`; it does not rewrite or delete legacy rows.

## Rollback

Return one chat to legacy reads without deleting structured data:

```bash
mise exec -- go run ./cmd/migrate-memory --db db/muxgoob.sqlite --rollback --chat CHAT_ID
```

For a full database rollback, stop the bot and replace the database with the backup printed by `--apply`. Preserve the failed database separately for diagnosis.

## Exit behavior

- `--verify` exits with status 2 if migrated items are missing or snapshot hashes/counts disagree.
- Apply refuses cutover when verification fails.
- A repeated apply is idempotent: existing source-item mappings are reused.
