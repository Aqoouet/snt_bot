# CLAUDE.md — snt-bot

## Read First

Before planning, coding, testing, or deploying, read:

- `SPEC_v2.md` — current product + architecture spec
- `DEPLOY_NOTES.md` — current deploy/runtime/ops notes

## AI Endpoint (Remote 35B)

Remote endpoint: `http://10.8.0.4:8181` — Qwen3.6-35B-A3B-Q4_K_M (llama.cpp, WireGuard network).

- Thinking **cannot be disabled** for this model — it is a Qwen3 thinking variant.
- Always use `max_tokens >= 5000`. Model spends ~1000+ tokens on internal reasoning before producing output. Lower values yield empty `content`.
- RTK proxy intercepts plain `curl` and replaces response with a schema template. Use `rtk proxy curl` to get raw output.

### Test call example

```bash
rtk proxy curl -s --connect-timeout 30 http://10.8.0.4:8181/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "Qwen3.6-35B-A3B-Q4_K_M.gguf",
    "messages": [{"role": "user", "content": "твой вопрос"}],
    "max_tokens": 5000
  }'
```

## Sequential AI Calls (by design)

`Bot.handleAdding` blocks concurrent AI calls per user via the `busy` map. This is intentional: llama.cpp
crashes or produces garbage when hit with overlapping requests from the same session. Do not parallelize
AI calls within a single user flow.

## Model Tests (SLOW — special ask required)

`tests/model_test.go` makes real AI calls (240 s timeout each). **Do NOT run these tests by default.**
Only run when the user explicitly asks, e.g. "run the model tests" or "run tests/model_test.go".

```bash
# Only when explicitly requested:
go test -v -timeout 600s ./tests/ -run TestExtraction
```

## Distribution Tests (explicit request required)

`tests/distribution_test.go` tests contribution distribution logic. Run only when explicitly asked.

```bash
# Only when explicitly requested:
go test -v ./tests/ -run TestDistribution
```

## Project Structure

- `prompts/extraction_agent.md` — system prompt for AI extraction layer. Contains `{{PLACEHOLDERS}}` injected at runtime by Go.
- `.env` — JSON config with canonical lists (PLOTS, PAYMENT_TYPES, CATEGORIES_INCOME, CATEGORIES_EXPENSE, etc.).
- `SPEC_v2.md` — full architecture specification.
- `DEPLOY_NOTES.md` — current deployment, systemd, endpoint, and verification notes.
