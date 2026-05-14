# SNT Finance Bot — SPEC v2 (Conversational AI Architecture)

Replaces SPEC v1. All domain logic preserved. Architecture paradigm changed: multi-step FSM → conversational AI extraction + deterministic Go processing.

---

## 1. Goals & Scope

- Collect SNT financial operations via Telegram in structured form.
- Store operations in **SQLite** (source of truth).
- Maintain one global running balance across all operations.
- Produce **CSV only via Export** scenario.
- Use **OpenAI-compatible API** for conversational field extraction.

---

## 2. Architecture Overview

Two strictly separated layers:

```
User message
     ↓
┌─────────────────────────────────┐
│  AI EXTRACTION LAYER (Go+LLM)  │
│  - multi-turn conversation      │
│  - fuzzy→canonical mapping      │
│  - outputs: structured JSON     │
│  - status: extracting/ready/    │
│            abort                │
└───────────────┬─────────────────┘
                │ status=ready → Go shows confirmation UI
                │ user confirms
                ↓
┌─────────────────────────────────┐
│  DETERMINISTIC GO LAYER        │
│  - validates struct             │
│  - runs distribution algorithm  │
│  - writes N rows to SQLite      │
│  - returns summary text         │
└─────────────────────────────────┘
```

**Hard rule:** AI never writes to DB directly. AI never executes scripts. AI outputs a validated field struct; Go owns all writes.

---

## 3. OpenAI-Compatible API — Sufficiency Assessment

| Feature needed | OpenAI-compat support | Notes |
|---|---|---|
| Multi-turn conversation (messages array) | ✅ universal | Go maintains message history per user session |
| JSON structured output | ✅ via `json_object` mode | Same fallback chain as `report_checking` |
| System prompt injection | ✅ universal | Config values injected at runtime |
| Tool calls / function calling | ❌ not required | We use JSON response protocol instead |
| Streaming | ❌ not needed | Single response per turn |

**Conclusion: existing OpenAI-compatible infrastructure is sufficient.** No new API features required. The `json_schema → json_object → no-format` fallback chain from `report_checking` applies here unchanged.

**Model requirement:** Must reliably produce structured JSON AND handle multi-turn context AND do fuzzy canonical matching. GPT-4o, GPT-4-turbo, Claude-via-proxy all qualify. Weak/small models risk JSON schema drift — note in deployment docs.

---

## 4. Fields & Semantics

Operation record fields (order in CSV):

**Членство · Дата · Приход · Расход · Тип платежа · Участок · Категория · Остаток · Примечание**

| Field | Source | Values |
|---|---|---|
| `Членство` | Auto-computed from plot | `Член` / `Индивидуал` / `-` |
| `Дата` | User input | `DD.MM.YYYY` |
| `Приход` / `Расход` | User input | positive amount or empty |
| `Тип платежа` | User input → AI canonical | from `PAYMENT_TYPES` in `.env` |
| `Участок` | User input → AI canonical | from `PLOTS` in `.env` |
| `Категория` | User input → AI canonical | from `CATEGORIES_INCOME` or `CATEGORIES_EXPENSE` |
| `Остаток` | Computed | running balance after operation |
| `Примечание` | User input | free text |

---

## 5. Payer Groups & Contribution Logic

### 5.1 Payer Types

- **Член СНТ** — legal member.
- **Садовод-индивидуал** — non-member, equivalent payment structure.
- Type derived from `PLOT_MEMBERSHIP` dict in `.env`.

### 5.2 Contribution Identifiers

Stored in `CONTRIBUTION_TYPES` in `.env`:

- Members: `MEMBER_REGULAR`, `TARGET_ROAD`, `TARGET_FLOOD`, `TARGET_DITCH`
- Individuals: `INDIV_CURRENT`, `TARGET_ROAD`, `TARGET_FLOOD`, `TARGET_DITCH`

### 5.3 Payment Distribution Priority

- Member: `CONTRIBUTION_PRIORITY_MEMBER` array in `.env`
- Individual: `CONTRIBUTION_PRIORITY_INDIVIDUAL` array in `.env`
- Waterfall: pay top priority first, remainder to next.
- Partial payment → partial allocation row.
- Overpayment beyond current year → advance to next year, same priority order, additional rows.

---

## 6. Tech Stack

| Component | Choice |
|---|---|
| Language | Go |
| Telegram API | `go-telegram-bot-api/v5` |
| Storage | SQLite |
| AI client | HTTP client to OpenAI-compatible API (base URL, model, key from `.env`) |
| Conversation state | In-memory map `userID → ConversationState` (message history + partial fields) |
| System prompt | Single file `prompts/extraction_agent.md` (replaces per-field prompts) |
| Per-field prompts | Removed — absorbed into extraction agent system prompt |

---

## 7. Security & Access

- Allowlist of `telegram user_id` in `.env`.
- Any message from unknown user: short rejection, no internals exposed.
- No admin commands for user management — edit `.env` + restart.

---

## 8. Telegram UI & Navigation

### 8.1 Main Menu (always present)

Reply keyboard 2×2:

| | Left | Right |
|---|---|---|
| Top | **Добавить операцию** | **Баланс** |
| Bottom | **Экспорт** | **Отмена** |

### 8.2 Cancel semantics

**At any point** in any flow: **Отмена** → wipe conversation state → return to main menu 2×2.

### 8.3 Lists not shown in chat

Categories, payment types, plots — **never** shown as full lists. AI resolves them conversationally.

---

## 9. Add Operation Flow (Conversational)

Replaces v1 §7 FSM entirely.

### 9.1 Entry

User taps **Добавить операцию** → Go initializes `ConversationState` for user → sends opening prompt to AI extraction layer → forwards AI's first question to user.

### 9.2 Conversation loop

```
while state != ready AND state != abort:
    receive user message
    if message == "Отмена":
        clear state, return to main menu
    append message to conversation history
    call AI extraction layer
    parse AI response JSON
    if status == "extracting":
        send AI's message to user, continue loop
    if status == "ready":
        show confirmation UI (see §9.3)
    if status == "abort":
        clear state, return to main menu with explanation
```

### 9.3 Confirmation step (Go-owned, not AI)

When AI signals `status: "ready"`, Go (not AI):
1. Calls `ComputeDistribution(fields)` — **dry run, no DB writes** (see §11.1).
2. Renders full preview of all N rows that will be written:
   - Per row: contribution ID, amount, fiscal_year, membership, plot.
   - Total rows count.
   - Projected global balance after commit.
3. Shows inline buttons: **✓ Подтвердить** / **✗ Отмена**.
4. On confirm → call `CommitDistribution(rows)` (see §11.2) — atomic DB write.
5. On cancel → clear state, return to main menu. No DB touched.

This step is intentionally outside AI. User sees the exact DB change before it happens.

### 9.4 After successful Add

Text message only:
- Brief field summary.
- Current global balance after write.
- No CSV attachment.

---

## 10. AI Extraction Layer

### 10.1 Conversation state (per user)

```
ConversationState {
    messages:      []ChatMessage    // full history sent to API each turn
    partialFields: OperationFields  // accumulated so far (nullable per field)
}
```

History is ephemeral (in-memory). Cleared on: confirm, cancel, timeout.

### 10.2 System prompt (`prompts/extraction_agent.md`)

Injected once as `role: system`. Contains:

1. **Role**: "You are an SNT finance bot assistant. Extract operation fields from user messages."
2. **Valid values**: All canonical lists from `.env` injected at runtime — payment types, plots, categories (income + expense), membership dict. Same injection pattern as v1 prompts.
3. **Field definitions**: date format, direction enum, amount rules, what each field means.
4. **Conversation rules**:
   - Ask only about missing or ambiguous fields.
   - If user provides multiple fields in one message, extract all at once.
   - On ambiguous canonical match: propose the closest match and ask user to confirm it.
   - Never ask for `Членство` — it is computed, not user-input.
5. **Output schema**: Always respond with JSON (see §10.3).

### 10.3 AI response JSON schema

Every AI response must be valid JSON:

```json
{
  "status": "extracting" | "ready" | "abort",
  "message": "string — text to display to user",
  "fields": {
    "date":         "DD.MM.YYYY" | null,
    "direction":    "приход" | "расход" | null,
    "amount":       123.45 | null,
    "payment_type": "<canonical>" | null,
    "plot":         "<canonical>" | null,
    "category":     "<canonical>" | null,
    "note":         "string" | null
  }
}
```

- `status: "extracting"` — still gathering. `message` = next question to user. `fields` = whatever confirmed so far.
- `status: "ready"` — all fields confirmed and canonicalized. `fields` = complete. `message` = summary (Go may override display).
- `status: "abort"` — user expressed desire to cancel or irrecoverable ambiguity. `message` = reason.

### 10.4 Response parsing fallback chain

Same as `report_checking`:
1. Try `json_schema` response format.
2. Fall back to `json_object`.
3. Fall back to no format → extract JSON block from text.

If all fail → treat as `extracting` with generic retry message.

### 10.5 Go validation after `status: "ready"`

Before showing confirmation UI, Go validates the AI-returned struct:
- `date` parses as `DD.MM.YYYY`.
- `direction` ∈ {приход, расход}.
- `amount > 0`.
- `payment_type` ∈ `PAYMENT_TYPES`.
- `plot` ∈ `PLOTS`.
- `category` ∈ correct list for direction.
- All fields non-null.

If validation fails → inject error back into conversation as system message, continue loop. AI does not access DB or `.env` directly — Go validates against in-memory config.

---

## 11. Deterministic Go Layer

Never called by AI directly. Two distinct phases, called separately.

### 11.1 `ComputeDistribution(fields) → []DistributionRow`

**Pure function. No DB writes. Called before confirmation.**

1. Look up `membership` from `PLOT_MEMBERSHIP[plot]`.
2. Read outstanding debts per contribution type for this plot/fiscal_year from DB (read-only).
3. Run waterfall distribution algorithm (§5.3):
   - Iterate priority list for payer type.
   - For each contribution: allocate `min(remaining_payment, remaining_debt)`.
   - If payment exceeds current year total → advance to next fiscal year, same priority.
   - Each allocation = one `DistributionRow`.
4. Return `[]DistributionRow` — the exact rows that would be written.

`DistributionRow` fields: `contribution_id`, `amount`, `fiscal_year`, `membership`, `plot`, `payment_type`, `op_date`, `category`, `note`, `projected_balance_after`.

Used by §9.3 to render preview. Idempotent — safe to call multiple times.

### 11.2 `CommitDistribution(rows []DistributionRow) → error`

**Called only after user confirms. Writes to DB.**

1. Open DB transaction.
2. Insert all rows from `[]DistributionRow`.
3. Set `payment_group_id` UUID (same for all rows in this call).
4. Set `balance_after` on each row (final, not projected).
5. Commit. On error → rollback, return error to caller.
6. Return summary struct.

### 11.3 Output

Summary struct → Go formats as text → sent to user.

---

## 12. Balance Scenario

1. User taps **Баланс**.
2. Bot asks: enter N (number of last operations).
3. User enters integer N.
4. Bot responds (text only):
   - Current global balance.
   - Aggregate income / expense totals.
   - Last N operations as text (date, direction, amount, plot, category, membership).
5. No AI involved in this flow.

---

## 13. Export CSV Scenario

1. User taps **Экспорт**.
2. Bot asks: enter N (number of last rows).
3. User enters integer N. If N ≥ total rows → export all.
4. Bot sends `.csv` file.
5. No AI involved.

### 13.1 CSV Format

- UTF-8 with BOM.
- Column order: `Членство, Дата, Приход, Расход, Тип платежа, Участок, Категория, Остаток, Примечание`.
- Sort: oldest → newest (for easy append into spreadsheet).

---

## 14. Global Balance

- One numeric global balance across all operations.
- `balance_after` stored per row = running sum through all prior rows.
- Initial value from `INITIAL_BALANCE` in `.env`.
- Displayed in: Balance scenario and post-Add summary.

---

## 15. Data Model

Table `operations` (each row = one contribution allocation chunk):

| Field | Type | Notes |
|---|---|---|
| `id` | INTEGER PK | Autoincrement |
| `created_at` | TIMESTAMP | UTC |
| `membership` | TEXT | Член / Индивидуал / - |
| `op_date` | TEXT | DD.MM.YYYY |
| `direction` | TEXT | приход / расход |
| `amount` | REAL | Amount for this specific contribution chunk |
| `payment_type` | TEXT | Canonical |
| `plot` | TEXT | Canonical |
| `fiscal_year` | INTEGER | Year this chunk applies to |
| `category` | TEXT | Contribution ID or expense category |
| `note` | TEXT | Shared across all chunks of one payment |
| `balance_after` | REAL | Running balance after this row |
| `payment_group_id` | TEXT | UUID — groups all rows from one user payment |

`payment_group_id` is new vs v1. Groups rows for Balance summary display.

Indexes: `op_date`, `created_at`, `(plot, fiscal_year)`.

---

## 16. `.env` Configuration (JSON)

| Key | Purpose |
|---|---|
| `TELEGRAM_BOT_TOKEN` | Bot token |
| `TELEGRAM_ALLOWED_USER_IDS` | Array of allowed `telegram user_id` |
| `INITIAL_BALANCE` | Starting balance |
| `OPENAI_BASE_URL` | AI API base URL |
| `OPENAI_API_KEY` | AI API key |
| `OPENAI_MODEL` | Model name |
| `CATEGORIES_INCOME` | Array of income category strings |
| `CATEGORIES_EXPENSE` | Array of expense category strings |
| `PAYMENT_TYPES` | Array of payment type strings |
| `PLOTS` | Array of plot identifiers |
| `PLOT_MEMBERSHIP` | Object: plot → `Член` / `Индивидуал` / `-` |
| `CONTRIBUTION_TYPES` | Array of `{id, name, payer_type}` |
| `CONTRIBUTION_PRIORITY_MEMBER` | Ordered array of contribution IDs for members |
| `CONTRIBUTION_PRIORITY_INDIVIDUAL` | Ordered array of contribution IDs for individuals |
| `CONTRIBUTION_AMOUNTS` | Object: `{contribution_id: amount}` — annual charge per contribution type |
| `DB_FILE` | SQLite file path |
| `STATE_TIMEOUT_MINUTES` | Conversation state TTL |
| `TEST_FIXTURES` | Object with test amounts (see §17) |

`CONTRIBUTION_AMOUNTS` is new vs v1 — required by distribution algorithm to compute debt per contribution per payer per year.

---

## 17. Non-Functional Requirements

- Log AI errors and Telegram errors without leaking secrets.
- One active `ConversationState` per user. New **Добавить операцию** tap while state exists: warn user, overwrite state.
- DB writes always transactional — partial distribution never committed.
- Conversation history cleared on: confirm, cancel, timeout (`STATE_TIMEOUT_MINUTES`).
- AI response timeout: configurable, default 15s; on timeout retry once, then show user error message.

---

## 18. Tests & Verification Scenarios

Distribution algorithm test cases — **20 cases**, identical to SPEC v1 §14. These test the Deterministic Go Layer only, independent of AI.

All inputs from `.env` `TEST_FIXTURES`:
- `TEST_FIXTURES.EXAMPLE_DUE_MEMBER` — member annual charges by contribution ID
- `TEST_FIXTURES.EXAMPLE_DUE_INDIVIDUAL` — individual annual charges by contribution ID
- `TEST_FIXTURES.PAYMENTS.*` — payment amounts keyed by name

Cases 1–20 from v1 §14.1 are unchanged. Distribution contract is identical; only the entry path changed (conversational vs FSM).

---

## 19. Key Differences from SPEC v1

| Aspect | v1 | v2 |
|---|---|---|
| Field collection | 7-step FSM, one field per step | Single conversational AI loop |
| AI role | Per-field validator (3 fields only) | Full extraction agent for all fields |
| Prompts | One file per field type | One system prompt, all fields |
| Validation | Inline per-step | AI extracts → Go validates struct before confirm |
| Confirmation | Implicit (last step completes) | Explicit inline button (Go-owned) |
| DB writes | Triggered at FSM completion | Triggered only after user confirmation |
| UX for power users | Always 7 prompts | One message can fill all fields |
| UX for novices | Guided step-by-step | AI asks only about gaps |
| Distribution algorithm | Deterministic Go | Unchanged |
| Data model | No `payment_group_id` | `payment_group_id` UUID added |
| Config new keys | — | `CONTRIBUTION_AMOUNTS` added |
| Prompts directory | `prompts/*.md` per field | `prompts/extraction_agent.md` single file |

---

## 21. Remote OpenAI-Compatible Endpoint (35B)

For production-quality extraction testing, a remote endpoint is available over WireGuard network.

- Base URL: `http://10.8.0.4:8181`
- Backend: `llama.cpp` server on host `stressii-wg` (reachable via SSH host alias `hostkey_ru` + WireGuard)
- Model: `Qwen3.6-35B-A3B-Q4_K_M.gguf`

### Key constraints

- This is a **Qwen3 thinking model** — internal reasoning cannot be disabled.
- Always set `max_tokens >= 5000`. Model spends ~1000+ tokens on reasoning before producing output; lower values yield empty `content` with non-empty `reasoning_content`.
- `finish_reason: "length"` with empty `content` = token budget exhausted during thinking. Increase `max_tokens`.

### API paths

- Models list: `GET /v1/models`
- Chat completions: `POST /v1/chat/completions`

### Example test call

```bash
curl -s http://10.8.0.4:8181/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "Qwen3.6-35B-A3B-Q4_K_M.gguf",
    "messages": [
      {"role": "system", "content": "<system prompt here>"},
      {"role": "user", "content": "<user message here>"}
    ],
    "max_tokens": 5000
  }'
```

### RTK proxy note

RTK intercepts plain `curl` output and replaces it with a schema template. Use `rtk proxy curl` to get raw JSON during testing:

```bash
rtk proxy curl -s http://10.8.0.4:8181/v1/chat/completions ...
```

