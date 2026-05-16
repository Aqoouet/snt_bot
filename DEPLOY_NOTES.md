# Deploy Notes

## Current deploy

- host: `hostkey_us`
- remote name: `19455.example.us`
- app dir: `/opt/snt-bot`
- svc: `snt-bot`
- unit: `/etc/systemd/system/snt-bot.service`
- workdir: `/opt/snt-bot`
- config: `/opt/snt-bot/.env`

## Runtime files

Deploy runtime =:

- `snt-bot` binary
- `.env`

Prompt embedded by `//go:embed`. Keep `prompts/extraction_agent.md` in deploy artifact set if refresh process expects file copy too.

## Build + upload

Rule: build local. Commit binary. Do not compile on server.

Build linux/amd64:

```bash
env GOCACHE="$PWD/.gocache" GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o "$PWD/snt-bot" ./
```

Upload binary + prompt, then restart:

```bash
tar -czf - snt-bot prompts/extraction_agent.md | ssh hostkey_us 'mkdir -p /opt/snt-bot/prompts && cd /opt/snt-bot && tar -xzf - && systemctl restart snt-bot'
```

`.env` not in git. Upload first deploy, or when changed:

```bash
scp .env hostkey_us:/opt/snt-bot/.env
```

## Systemd unit

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

Install or refresh:

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

## Ops

Status:

```bash
ssh hostkey_us 'systemctl status snt-bot --no-pager --full'
```

Restart:

```bash
ssh hostkey_us 'systemctl restart snt-bot'
```

Logs:

```bash
ssh hostkey_us 'journalctl -u snt-bot -f'
```

## Config notes

`.env` uses JSON, not shell syntax.

Must-have keys:

- `TELEGRAM_BOT_TOKEN`
- `TELEGRAM_ALLOWED_USER_IDS`
- `OPENAI_BASE_URL`
- `OPENAI_MODEL`

Current AI endpoint:

- `OPENAI_BASE_URL`: `http://10.8.0.4:8181`
- `OPENAI_MODEL`: `Qwen3.6-35B-A3B-Q4_K_M.gguf`
- `OPENAI_API_KEY`: empty string

Current bot AI timeout:

- `180s` per req in `internal/ai/client.go`

Endpoint auth note:

- `OPENAI_API_KEY` intentionally empty
- tests + `/health` + `/v1/models` worked without auth

Model risk note:

- current endpoint model = Qwen thinking model
- use big token budget for extraction calls
- weak/small models may drift JSON schema, hurt extract reliability

## Telegram setup

1. Create bot in `@BotFather` via `/newbot`.
2. Put token into `TELEGRAM_BOT_TOKEN`.
3. Get allowed Telegram user ID.
4. Put numeric ID into `TELEGRAM_ALLOWED_USER_IDS`, example:

```json
"TELEGRAM_ALLOWED_USER_IDS": [123456789]
```

5. Restart svc:

```bash
ssh hostkey_us 'systemctl restart snt-bot'
```

6. Open bot, send `/start`.

## Verification done

- local `linux/amd64` build passed
- `go test ./...` passed with local `GOCACHE`
- remote svc installed + started
- remote `http://10.8.0.4:8181/health` -> `200`
- remote `http://10.8.0.4:8181/v1/models` -> `200`, no auth
- `vim` installed on `hostkey_us`
