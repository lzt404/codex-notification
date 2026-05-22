# Codex Notification Hook Install Prompt

You are installing the Codex Notification Stop hook on this machine.

Repository: `router-for-me/codex-notification`
Latest release page: `https://github.com/router-for-me/codex-notification/releases/latest`
Configuration file: `~/.codex/codex-notification.env`

Follow these steps in order. Guide the user one step at a time. Do not ask for all credentials at once.

1. Detect the current operating system and CPU architecture.
2. Download the latest release archive that matches the current platform:
   - macOS Intel: `codex-notification_<tag>_macos_amd64.tar.gz`
   - macOS Apple Silicon: `codex-notification_<tag>_macos_arm64.tar.gz`
   - Windows Intel/AMD 64-bit: `codex-notification_<tag>_windows_amd64.zip`
   - Windows ARM64: `codex-notification_<tag>_windows_arm64.zip`
3. Extract the archive into a stable local install directory:
   - macOS: `~/.codex/codex-notification`
   - Windows: `%USERPROFILE%\.codex\codex-notification`
4. Create `~/.codex/codex-notification.env` if it does not exist. Use `codex-notification.env.example` as the template.
5. Preserve any existing values in `codex-notification.env`.
6. Configure notification providers interactively:
   - First ask whether the user wants to enable QQ Bot notifications, Telegram Bot notifications, or both.
   - If a provider is already fully configured, show only that it is configured; do not print secret values.
   - Never guess credentials. Ask only for missing values.
7. Configure QQ Bot if the user enables it:
   - Explain that the QQ Bot platform is `https://q.qq.com/qqbot/openclaw/login.html`.
   - Ask the user to create or open a QQ Bot application there.
   - Ask for `APP_ID` if it is missing.
   - Ask for `APP_SECRET` if it is missing.
   - If `TARGET_OPENID` is missing, help the user capture it:
     1. Save `APP_ID` and `APP_SECRET` to `codex-notification.env` first.
     2. Run the installed wrapper with `capture-openid`.
        - macOS: `<install-dir>/scripts/run-hook capture-openid`
        - Windows: `powershell.exe -NoProfile -ExecutionPolicy Bypass -File "<install-dir>\scripts\run-hook.ps1" capture-openid`
     3. Tell the user to send `/openid` as a private message to the QQ Bot from the QQ account that should receive notifications.
     4. Wait for the command output.
     5. Copy the printed `TARGET_OPENID=...` value into `codex-notification.env`.
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
     5. Run `getUpdates` with the configured token:
        - macOS: `curl "https://api.telegram.org/bot${TELEGRAM_BOT_TOKEN}/getUpdates"`
        - Windows PowerShell: `Invoke-RestMethod "https://api.telegram.org/bot$env:TELEGRAM_BOT_TOKEN/getUpdates"`
     6. Parse the response and find `message.chat.id`, `channel_post.chat.id`, or another `chat.id` in the newest relevant update.
     7. Save that value as `TELEGRAM_CHAT_ID`.
   - If `getUpdates` returns an empty `result` array:
     1. Ask the user to send a new message after the bot has been created.
     2. If the bot used a webhook before, run `deleteWebhook`:
        - macOS: `curl "https://api.telegram.org/bot${TELEGRAM_BOT_TOKEN}/deleteWebhook"`
        - Windows PowerShell: `Invoke-RestMethod "https://api.telegram.org/bot$env:TELEGRAM_BOT_TOKEN/deleteWebhook"`
     3. Ask the user to send another new message and run `getUpdates` again.
9. Configure notification filters:
   - Ask whether notifications should be limited to the current main model only.
   - If yes, set `NOTIFICATION_ALLOWED_MODELS` to the current Codex model name when it is available.
   - Keep `NOTIFICATION_BLOCKED_MODELS=mini,small,lite,flash,haiku,nano` unless the user asks for different filtering.
   - Keep `NOTIFICATION_ALLOW_SUBAGENTS=false` unless the user explicitly wants subagent notifications.
10. Update `~/.codex/hooks.json` without removing existing hooks:
   - Add a `Stop` command hook that points to `scripts/run-hook` on macOS.
   - Add a `Stop` command hook that runs `powershell.exe -NoProfile -ExecutionPolicy Bypass -File "<install-dir>\scripts\run-hook.ps1"` on Windows.
   - Do not duplicate the hook if an equivalent Codex Notification hook already exists.
11. On macOS, make sure `scripts/run-hook` is executable.
12. Verify the binary exists before installing the hook. The wrapper scripts require the release binary and do not run from source:
   - macOS: `bin/codex-notification`
   - Windows: `bin\codex-notification.exe`
13. Do not send a real QQ or Telegram notification unless the user explicitly asks for a live test.

Keep all user-facing conversation in the user's language. Keep any Git or GitHub text in English.
