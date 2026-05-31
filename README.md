# Codex Notification

Codex Notification is a Codex `Stop` hook that sends a plain-text QQ Bot, Telegram Bot, or WeChat message when a Codex turn finishes.

WeChat scan binding is available as a manual setup helper. It captures the WeChat conversation context token needed for delivery and saves the captured values into `~/.codex/codex-notification.env`. The normal `Stop` hook path only sends the completion text notification.

## Install With Codex

Paste this prompt into Codex:

```text
Open and follow this prompt template:
https://raw.githubusercontent.com/lzt404/codex-notification/main/prompts/install-hook.md

Install the Codex Notification hook on this machine. Preserve existing hooks and existing notification configuration. Ask me only for missing QQ, Telegram, or WeChat values.
```

Codex will detect the current operating system and CPU architecture, download the matching release archive, create or update `~/.codex/codex-notification.env`, and merge the Stop hook into `~/.codex/hooks.json`.

Restart Codex after setup finishes so the updated hook and notification configuration are loaded.

## License

MIT
