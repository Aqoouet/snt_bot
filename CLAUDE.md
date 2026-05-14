# CLAUDE.md — snt-bot

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

## Project Structure

- `prompts/extraction_agent.md` — system prompt for AI extraction layer. Contains `{{PLACEHOLDERS}}` injected at runtime by Go.
- `.env` — JSON config with canonical lists (PLOTS, PAYMENT_TYPES, CATEGORIES_INCOME, CATEGORIES_EXPENSE, etc.).
- `SPEC_v2.md` — full architecture specification.
