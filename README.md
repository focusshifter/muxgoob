# Mux Goob

A telegram bot made with [Telebot](https://github.com/tucnak/telebot), heavily inspired by [Yatzie](https://github.com/go-telegram-bot/yatzie).

## Owner cron commands

Only the configured `owner_username` may manage jobs. Cron expressions use the global IANA `time_zone` in `config.yml` (for example `Europe/Moscow`), rather than the server's timezone. A scheduled command is dispatched to the target chat as if it were sent by the configured owner.

```text
!cron add <chat_id> "0 9 * * *" <alias> <command>
!cron remove <chat_id> <alias>
!cron reschedule <chat_id> <alias> "0 10 * * *"
!cron update <chat_id> <alias> <command>
```

Jobs are persisted in SQLite and restored after a bot restart. Aliases are unique within a chat; adding an existing alias replaces both its schedule and command.
