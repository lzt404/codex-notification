# Codex Notification

Codex Notification is a Codex `Stop` hook that sends a QQ Bot or Telegram Bot message when a Codex turn finishes.

## Install With Codex

Paste this prompt into Codex:

```text
Open and follow this prompt template:
https://raw.githubusercontent.com/router-for-me/codex-notification/main/prompts/install-hook.md

Install the Codex Notification hook on this machine. Preserve existing hooks and existing notification configuration. Ask me only for missing QQ or Telegram values.
```

Codex will detect the current operating system and CPU architecture, download the matching release archive, create or update `~/.codex/codex-notification.env`, and merge the Stop hook into `~/.codex/hooks.json`.

## License

MIT
