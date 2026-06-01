# Codex Notification Hook Install Prompt

You are installing the Codex Notification Stop hook on this machine.

Repository: `router-for-me/codex-notification`
Latest release page: `https://github.com/router-for-me/codex-notification/releases/latest`
Releases list page: `https://github.com/router-for-me/codex-notification/releases`
Expanded assets page pattern: `https://github.com/router-for-me/codex-notification/releases/expanded_assets/<resolved-tag>`
Optional latest release API: `https://api.github.com/repos/router-for-me/codex-notification/releases/latest`
Environment file: `~/.codex/codex-notification.env`
Hooks file: `~/.codex/hooks.json`
Install directory:
- macOS: `~/.codex/codex-notification`
- Windows: `%USERPROFILE%\.codex\codex-notification`
Wrapper command:
- macOS: `<install-dir>/scripts/run-hook`
- Windows: `powershell.exe -NoProfile -ExecutionPolicy Bypass -File "<install-dir>\scripts\run-hook.ps1"`
Binary path:
- macOS: `<install-dir>/bin/codex-notification`
- Windows: `<install-dir>\bin\codex-notification.exe`
Release asset name templates:
- macOS Intel: `codex-notification_<resolved-tag>_macos_amd64.tar.gz`
- macOS Apple Silicon: `codex-notification_<resolved-tag>_macos_arm64.tar.gz`
- Windows Intel/AMD 64-bit: `codex-notification_<resolved-tag>_windows_amd64.zip`
- Windows ARM64: `codex-notification_<resolved-tag>_windows_arm64.zip`

Use only the paths, release pages, commands, and asset templates defined above. In the steps below, refer to them by name instead of repeating literal paths or release URLs. Do not hard-code, recommend, or ask for a specific release version; always resolve the latest release automatically.

Follow these steps in order. Guide the user one step at a time. Do not ask for all credentials at once.

Safety rules:

- Preserve existing hooks and existing notification configuration.
- When editing the configured hooks file, always write valid UTF-8 JSON without a byte order mark (BOM). Codex hook parsing can fail at line 1 column 1 when the file starts with `EF BB BF`.
- On Windows PowerShell 5, do not write the configured hooks file with `Set-Content -Encoding UTF8`, because that writes a UTF-8 BOM. Use .NET UTF-8 without BOM instead.
- For interactive capture helpers that print a QR code or wait for user messages, open a visible local terminal window by default. Do not hide the terminal, redirect the QR output to a file, or stream the QR through the Codex conversation unless opening a local terminal is impossible.

1. Detect the current operating system and CPU architecture.
2. Resolve the latest release tag automatically, then download the release archive that matches the current platform:
   - Use the releases list page first. Prefer the newest release entry marked `Latest`; if that marker is not easy to parse, use the first release tag shown on the releases list.
   - Optionally cross-check the optional latest release API when it is available. Use `tag_name`, but do not stop if the unauthenticated API is rate limited.
   - The latest release page may also be used, but only trust the redirect location or final URL to determine the tag. Do not trust page title or body text from that page, because cached or tool-rendered HTML can lag behind the releases list.
   - Verify the resolved tag by opening the expanded assets page for that tag and finding the exact platform asset link before constructing the download URL.
   - If release sources disagree, prefer the releases list entry marked `Latest`, then the highest semver tag whose expanded assets page contains the required platform asset.
   - Do not guess an asset URL until the matching asset name has been confirmed from the expanded assets page.
3. Extract the archive into the configured install directory for the current platform.
   - After extraction, normalize the layout so the install directory directly contains `bin`, `scripts`, and `codex-notification.env.example`. If the archive extracts into a single top-level directory, move that directory's contents up one level.
4. Create the configured environment file if it does not exist. Use `codex-notification.env.example` as the template.
5. Preserve any existing values in the configured environment file.
6. Configure notification providers interactively:
   - First ask whether the user wants to enable QQ Bot notifications, Telegram Bot notifications, WeChat notifications, or more than one provider.
   - If a provider is already fully configured, show only that it is configured; do not print secret values.
   - Never guess credentials. Ask only for missing values.
7. Configure QQ Bot if the user enables it:
   - Explain that the QQ Bot platform is `https://q.qq.com/qqbot/openclaw/login.html`.
   - Ask the user to create or open a QQ Bot application there.
   - Ask for `APP_ID` if it is missing.
   - Ask for `APP_SECRET` if it is missing.
   - If `TARGET_OPENID` is missing, help the user capture it:
     1. Save `APP_ID` and `APP_SECRET` to the configured environment file first.
     2. Run the configured wrapper command with `capture-openid`.
     3. Tell the user to send `/openid` as a private message to the QQ Bot from the QQ account that should receive notifications.
     4. Wait for the helper to report that `TARGET_OPENID` was saved to the configured environment file.
     5. Tell the user to restart Codex after setup completes so the updated notification configuration can take effect.
   - If capture times out, ask the user to run capture again and send `/openid` again. Also ask them to confirm that the message was sent to the bot application that owns the configured `APP_ID`.
8. Configure Telegram Bot if the user enables it:
   - Ask the user to open Telegram and start a chat with `@BotFather`.
   - If `TELEGRAM_BOT_TOKEN` is missing, guide the user through:
     1. Send `/newbot` to `@BotFather`.
     2. Enter a display name.
     3. Enter a username ending in `bot`.
     4. Copy the HTTP API token returned by `@BotFather`.
     5. Save it as `TELEGRAM_BOT_TOKEN`.
   - Set `TELEGRAM_API_BASE=https://api.telegram.org` unless the user explicitly uses a custom Telegram Bot API server or proxy endpoint.
   - If `TELEGRAM_CHAT_ID` is missing, help the user retrieve it:
     1. Ask whether the target is a private chat, group, or channel.
     2. For a private chat, tell the user to open the bot chat and send any new message, such as `test`.
     3. For a group, tell the user to add the bot to the group and send a new message or command in that group.
     4. For a channel, tell the user to add the bot as an administrator and publish a new channel post.
     5. Run `getUpdates` with the configured token.
     6. Parse the response and find `message.chat.id`, `channel_post.chat.id`, or another `chat.id` in the newest relevant update.
     7. Save that value as `TELEGRAM_CHAT_ID`.
   - If `getUpdates` returns an empty `result` array:
     1. Ask the user to send a new message after the bot has been created.
     2. If the bot used a webhook before, run Telegram `deleteWebhook` with the configured token.
     3. Ask the user to send another new message and run `getUpdates` again.
9. Configure WeChat if the user enables it:
   - Explain that WeChat notifications use the Weixin HTTP JSON API channel and send plain text only.
   - If `WECHAT_TOKEN`, `WECHAT_ACCOUNT_ID`, or `WECHAT_TARGET_USER_ID` is missing, run the configured wrapper command with `capture-wechat`.
   - Open `capture-wechat` in a new visible local terminal window by default, because QR codes render and scan much faster there than when streamed through the Codex conversation UI.
   - On macOS, open Terminal and pass the configured wrapper command with the `capture-wechat` argument. Quote paths correctly.
   - On Windows, open a visible PowerShell window with `-NoExit`, pass the configured wrapper command with the `capture-wechat` argument, and quote paths correctly.
   - If a visible terminal cannot be opened, run the configured wrapper command in the current terminal with `capture-wechat`.
   - If you must relay terminal output through the conversation, wait for the QR block to finish, then paste it as one fenced text block.
   - Do not manually construct a QR code from the raw `qrcode` login-status key. That key may scan as plain text. Use the QR code printed by the helper, which is generated from the terminal-printable login URL/content returned by WeChat.
   - Tell the user to scan the QR code printed in the terminal with WeChat, confirm login on the phone, then send any message to the WeChat bot so the helper can capture `WECHAT_CONTEXT_TOKEN`.
   - Wait for the helper to report that WeChat configuration was saved to the configured environment file. Do not ask the user to copy `WECHAT_*` values manually. Complete WeChat credentials enable the provider automatically; `WECHAT_ENABLED` is not required.
   - Tell the user to restart Codex after setup completes so the updated notification configuration can take effect.
   - If login values already exist but `WECHAT_CONTEXT_TOKEN` is missing, run the configured wrapper command with `capture-wechat-context` in a new visible local terminal window and ask the user to send any message to the WeChat bot.
   - On macOS, open Terminal and pass the configured wrapper command with the `capture-wechat-context` argument. Quote paths correctly.
   - On Windows, open a visible PowerShell window with `-NoExit`, pass the configured wrapper command with the `capture-wechat-context` argument, and quote paths correctly.
   - If a visible terminal cannot be opened, run the configured wrapper command in the current terminal with `capture-wechat-context`.
   - Wait for `capture-wechat-context` to report that `WECHAT_CONTEXT_TOKEN` was saved to the configured environment file.
   - Tell the user to restart Codex after setup completes so the updated notification configuration can take effect.
   - If the QR code expires, rerun `capture-wechat` to print a fresh QR code. Do not reuse old QR output or old login-status keys.
   - If login-status polling or WeChat message-context polling reports a transient timeout, let the helper keep retrying. If an older installed release exits immediately on a timeout, upgrade to the latest release or rerun the helper.
   - If WeChat capture or sending fails on Windows with `TLS handshake timeout` while the WeChat QR endpoint succeeds from the same machine with a standard HTTP client, set `GODEBUG=netdns=cgo` in the configured environment file and retry. Do not use `netdns=cgo+1` in the saved configuration because it prints diagnostic text.
   - Do not save a QR image file or a separate WeChat state JSON file; let the helper persist only the environment values in the configured environment file.
   - Do not run `capture-wechat` or `capture-wechat-context` from the normal Stop hook; they are only manual setup helpers.
10. Configure notification filters:
   - Ask whether notifications should be limited to the current main model only.
   - If yes, set `NOTIFICATION_ALLOWED_MODELS` to the current Codex model name when it is available.
   - Keep `NOTIFICATION_BLOCKED_MODELS=mini,small,lite,flash,haiku,nano` unless the user asks for different filtering.
   - Keep `NOTIFICATION_ALLOW_SUBAGENTS=false` unless the user explicitly wants subagent notifications.
11. Update the configured hooks file without removing existing hooks:
   - Add a `Stop` command hook that uses the configured wrapper command for the current platform.
   - Do not duplicate the hook if an equivalent Codex Notification hook already exists.
   - Preserve unrelated hook events and unrelated `Stop` hook entries.
   - After serializing the merged JSON, write the configured hooks file as UTF-8 without BOM.
   - Verify the file starts with `{`, not the UTF-8 BOM byte sequence `EF BB BF`, before telling the user to restart Codex.
12. On macOS, make sure the configured wrapper command is executable.
13. Verify the configured binary path exists before installing the hook. The wrapper scripts require the release binary and do not run from source.
14. After hooks and notification configuration are saved, tell the user to restart Codex for the updated hook and notification configuration to take effect.
15. Do not send a real QQ, Telegram, or WeChat notification unless the user explicitly asks for a live test.

Keep all user-facing conversation in the user's language. Keep any Git or GitHub text in English.
