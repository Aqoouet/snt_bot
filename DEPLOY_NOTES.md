# Deploy Notes

## Current deployment

- Host: `hostkey_us`
- Remote host name: `19455.example.us`
- App directory: `/opt/snt-bot`
- Service: `snt-bot`
- Service unit: `/etc/systemd/system/snt-bot.service`
- Working directory: `/opt/snt-bot`
- Config path: `/opt/snt-bot/.env`

## Runtime files

The deployed runtime consists of:

- `snt-bot` binary (prompts embedded via `//go:embed`)
- `.env`

## Build and upload

> **The binary is built locally and committed to the repository. Do not compile on the server.**
> The `snt-bot` linux/amd64 binary is tracked in git so the server only needs to receive the pre-built artifact.

Build locally (cross-compile for linux/amd64):

```bash
env GOCACHE="$PWD/.gocache" GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o "$PWD/snt-bot" ./
```

Upload the binary and prompt, then restart:

```bash
tar -czf - snt-bot prompts/extraction_agent.md | ssh hostkey_us 'mkdir -p /opt/snt-bot/prompts && cd /opt/snt-bot && tar -xzf - && systemctl restart snt-bot'
```

`.env` is not committed to git. Upload it separately on first deploy or when it changes:

```bash
scp .env hostkey_us:/opt/snt-bot/.env
```

## Systemd service

Installed unit:

```ini
[Unit]
Description=SNT Telegram Bot
After=network.target

[Service]
Type=simple
WorkingDirectory=/opt/snt-bot
Environment=CONFIG_PATH=/opt/snt-bot/.env
ExecStart=/opt/snt-bot/snt-bot
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
```

Install or refresh the service:

```bash
ssh hostkey_us "cat > /etc/systemd/system/snt-bot.service <<'EOF'
[Unit]
Description=SNT Telegram Bot
After=network.target

[Service]
Type=simple
WorkingDirectory=/opt/snt-bot
Environment=CONFIG_PATH=/opt/snt-bot/.env
ExecStart=/opt/snt-bot/snt-bot
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload
systemctl enable --now snt-bot"
```

## Operations

Check status:

```bash
ssh hostkey_us 'systemctl status snt-bot --no-pager --full'
```

Restart:

```bash
ssh hostkey_us 'systemctl restart snt-bot'
```

Tail logs:

```bash
ssh hostkey_us 'journalctl -u snt-bot -f'
```

## Config notes

`.env` is JSON, not shell syntax.

Required fields for a working bot:

- `TELEGRAM_BOT_TOKEN`
- `TELEGRAM_ALLOWED_USER_IDS`
- `OPENAI_BASE_URL`
- `OPENAI_MODEL`

Current AI endpoint used by tests and deployment:

- `OPENAI_BASE_URL`: `http://10.8.0.4:8181`
- `OPENAI_MODEL`: `Qwen3.6-35B-A3B-Q4_K_M.gguf`
- `OPENAI_API_KEY`: empty string

Current bot-side model call timeout:

- `180s` per request in `internal/ai/client.go`

`OPENAI_API_KEY` is intentionally empty for this endpoint. Repo tests and direct probes to `/health` and `/v1/models` succeeded without authentication.

## Telegram setup

1. Create the bot in `@BotFather` with `/newbot`.
2. Copy the issued token into `TELEGRAM_BOT_TOKEN`.
3. Get the allowed Telegram user ID.
4. Put that numeric ID into `TELEGRAM_ALLOWED_USER_IDS`, for example:

```json
"TELEGRAM_ALLOWED_USER_IDS": [123456789]
```

5. Restart the service:

```bash
ssh hostkey_us 'systemctl restart snt-bot'
```

6. Open the bot in Telegram and send `/start`.

## Verification performed

- Local build completed for `linux/amd64`
- `go test ./...` passed with workspace-local `GOCACHE`
- Remote service installed and started successfully
- Remote endpoint `http://10.8.0.4:8181/health` returned `200`
- Remote endpoint `http://10.8.0.4:8181/v1/models` returned `200` without auth
- `vim` installed on `hostkey_us`
