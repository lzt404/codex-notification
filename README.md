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

Codex will detect the current operating system and CPU architecture, resolve the release tag, verify the matching archive from the release assets page, create or update `~/.codex/codex-notification.env`, and merge the Stop hook into `~/.codex/hooks.json`.

For WeChat setup, Codex should open the QR capture helper in a new visible local terminal window. Scanning from the local terminal is faster and avoids QR corruption from streamed conversation output.

Restart Codex after setup finishes so the updated hook and notification configuration are loaded.

## Troubleshooting

If Codex reports `failed to parse hooks config` at line 1 column 1 after installation, check whether `~/.codex/hooks.json` was written with a UTF-8 BOM. On Windows PowerShell 5, `Set-Content -Encoding UTF8` writes a BOM that Codex hook parsing may reject. Rewrite the file as valid UTF-8 without BOM while preserving its JSON content.

## License

MIT
