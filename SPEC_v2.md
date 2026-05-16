# SNT Finance Bot — SPEC v2

Kill v1 FSM. Keep domain logic. New shape: chat AI extract -> Go verify/process.

---

## 1. Goal

- Telegram bot collect SNT finance ops.
- SQLite = truth.
- One global running balance.
- CSV only via **Export**.
- Use OpenAI-compat API for field extraction.

## 2. Arch

Two layers. Hard split.

```text
user msg
  ↓
AI extract layer
- multi-turn chat
- fuzzy -> canonical
- JSON out
- status: extracting | ready | abort
  ↓ ready
Go confirm UI
  ↓ confirm
Deterministic Go layer
- validate struct
- run distribution
- write N SQLite rows
- return summary
```

Hard rule:
- AI never touch DB.
- AI never run scripts.
- AI only emit field struct.
- Go own all writes.

## 3. OpenAI-Compat Sufficiency

Need / support / note:

- multi-turn msgs / yes / Go keep per-user history
- JSON output / yes / use `json_object`
- system prompt / yes / inject runtime config
- tool calls / no need / JSON protocol enough
- streaming / no need / one resp per turn

Conclusion: current OpenAI-compat infra enough. Keep `report_checking` fallback chain:

1. `json_schema`
2. `json_object`
3. raw text -> extract JSON

Model must:
- hold multi-turn ctx
- output stable JSON
- do fuzzy canonical match

Good: GPT-4o, GPT-4-turbo, Claude-via-proxy. Small/weak model -> schema drift risk. Note in deploy doc.

## 4. Fields

CSV order:

`Членство · Дата · Приход · Расход · Тип платежа · Участок · Категория · Остаток · Примечание`

Field map:

- `Членство`: auto from plot. `Член` / `Индивидуал` / `-`
- `Дата`: user, `DD.MM.YYYY`
- `Приход` / `Расход`: user, positive amount or empty
- `Тип платежа`: user -> AI canonical from `PAYMENT_TYPES`
- `Участок`: user -> AI canonical from `PLOTS`
- `Категория`: user -> AI canonical from `CATEGORIES_INCOME` or `CATEGORIES_EXPENSE`
- `Остаток`: computed running balance after op
- `Примечание`: free text

## 5. Payers + Contribution Logic

### 5.1 Payer type

- `Член СНТ` = legal member
- `Садовод-индивидуал` = non-member, same payment shape
- derive from `.env` `PLOT_MEMBERSHIP`

### 5.2 Contribution IDs

In `.env` `CONTRIBUTION_TYPES`:

- members: `MEMBER_REGULAR`, `TARGET_ROAD`, `TARGET_FLOOD`, `TARGET_DITCH`
- individuals: `INDIV_CURRENT`, `TARGET_ROAD`, `TARGET_FLOOD`, `TARGET_DITCH`

### 5.3 Distribution priority

- member order: `CONTRIBUTION_PRIORITY_MEMBER`
- individual order: `CONTRIBUTION_PRIORITY_INDIVIDUAL`
- waterfall: pay top debt first, then next
- partial pay -> partial row
- overpay current year -> push next year, same priority, extra rows

## 6. Stack

- Go
- Telegram: `go-telegram-bot-api/v5`
- SQLite
- AI client: HTTP -> OpenAI-compat (`base URL`, `model`, `key` from `.env`)
- state: in-memory `userID -> ConversationState`
- system prompt: `prompts/extraction_agent.md`
- per-field prompts: gone

## 7. Security

- allowlist `telegram user_id` in `.env`
- unknown user -> short reject, no internals
- no admin user-mgmt cmds; edit `.env` + restart

## 8. Telegram UI

### 8.1 Main menu

Reply kb 2x2:

- `Добавить операцию`
- `Баланс`
- `Экспорт`
- `Отмена`

### 8.2 Cancel

Anywhere: `Отмена` -> wipe convo state -> main menu.

### 8.3 Lists

Never dump full categories/payment types/plots in chat. AI resolves via convo.

## 9. Add Operation Flow

Replaces v1 FSM.

### 9.1 Entry

User taps `Добавить операцию` -> Go init `ConversationState` -> send opening ctx to AI -> forward AI first question.

### 9.2 Loop

```text
while status != ready && status != abort
  get user msg
  if "Отмена" -> clear state, main menu
  append history
  call AI
  parse JSON
  extracting -> send AI msg, continue
  ready -> show confirm UI
  abort -> clear state, main menu + reason
```

### 9.3 Confirm step

When AI returns `ready`, Go owns next step:

1. run `ComputeDistribution(fields)` dry-run, no DB write
2. render full preview of all rows:
   - each row: contribution ID, amount, fiscal year, membership, plot
   - total row count
   - projected global balance after commit
3. show inline buttons: `✓ Подтвердить` / `✗ Отмена`
4. confirm -> `CommitDistribution(rows)` atomic write
5. cancel -> clear state, main menu, no DB touch

User must see exact DB change before commit.

### 9.4 Success reply

Text only:

- short field summary
- current global balance
- no CSV

## 10. AI Extract Layer

### 10.1 Per-user state

```text
ConversationState {
  messages: []ChatMessage
  partialFields: OperationFields
}
```

Ephemeral. Clear on confirm, cancel, timeout.

### 10.2 System prompt

`prompts/extraction_agent.md`, injected once as system msg. Contains:

1. role: SNT finance bot extractor
2. canonical values from `.env`: payment types, plots, categories, membership map
3. field defs: date, direction, amount, meaning
4. convo rules:
   - ask only missing/ambiguous fields
   - if one user msg gives many fields, extract all
   - if fuzzy match ambiguous, propose closest + ask confirm
   - never ask `Членство`; Go computes it
5. output schema below

### 10.3 AI JSON schema

```json
{
  "status": "extracting | ready | abort",
  "message": "text for user",
  "fields": {
    "date": "DD.MM.YYYY | null",
    "direction": "приход | расход | null",
    "amount": "number | null",
    "payment_type": "canonical | null",
    "plot": "canonical | null",
    "category": "canonical | null",
    "note": "string | null"
  }
}
```

Meaning:

- `extracting`: still gather; `message` = next question; `fields` = confirmed so far
- `ready`: all fields done; `fields` complete; `message` can be summary
- `abort`: user wants cancel or ambiguity hopeless; `message` = reason

### 10.4 Parse fallback

Same as `report_checking`:

1. `json_schema`
2. `json_object`
3. raw text -> extract JSON

All fail -> treat as `extracting` with generic retry msg.

### 10.5 Go validate after `ready`

Before confirm UI, Go checks:

- `date` parses as `DD.MM.YYYY`
- `direction` in `{приход, расход}`
- `amount > 0`
- `payment_type` in `PAYMENT_TYPES`
- `plot` in `PLOTS`
- `category` in correct list for direction
- all fields non-null

Fail -> inject validation error back as system msg, continue convo.

AI never read DB or `.env` direct. Go validates vs in-memory config.

## 11. Deterministic Go Layer

AI never call direct.

### 11.1 `ComputeDistribution(fields) -> []DistributionRow`

Pure fn. No DB write. Called before confirm.

Steps:

1. find `membership` from `PLOT_MEMBERSHIP[plot]`
2. read outstanding debts for plot/fiscal year from DB, read-only
3. run waterfall:
   - walk payer priority list
   - alloc `min(remaining_payment, remaining_debt)`
   - if payment > current-year total, move next fiscal year, same priority
   - each alloc = one row
4. return exact rows that would be written

`DistributionRow`:
- `contribution_id`
- `amount`
- `fiscal_year`
- `membership`
- `plot`
- `payment_type`
- `op_date`
- `category`
- `note`
- `projected_balance_after`

Used for preview. Idempotent. Safe many calls.

### 11.2 `CommitDistribution(rows []DistributionRow) -> error`

Only after confirm. Writes DB.

1. open tx
2. insert all rows
3. set one shared `payment_group_id` UUID
4. set final `balance_after` per row
5. commit; on err rollback
6. return summary struct

### 11.3 Output

Go formats summary text -> send user.

## 12. Balance Flow

1. user taps `Баланс`
2. bot asks N = last ops count
3. user sends int N
4. bot replies text only:
   - current global balance
   - aggregate income/expense
   - last N ops: date, direction, amount, plot, category, membership
5. no AI

## 13. Export CSV Flow

1. user taps `Экспорт`
2. bot asks N = last row count
3. user sends int N; if `N >= total`, export all
4. bot sends `.csv`
5. no AI

### 13.1 CSV format

- UTF-8 BOM
- columns:
  `Членство, Дата, Приход, Расход, Тип платежа, Участок, Категория, Остаток, Примечание`
- sort oldest -> newest

## 14. Global Balance

- one numeric global balance across all ops
- `balance_after` per row = running sum through all prior rows
- initial from `.env` `INITIAL_BALANCE`
- shown in Balance flow + post-add summary

## 15. Data Model

Table `operations`. One row = one contribution allocation chunk.

- `id`: INTEGER PK autoincrement
- `created_at`: TIMESTAMP UTC
- `membership`: TEXT (`Член` / `Индивидуал` / `-`)
- `op_date`: TEXT `DD.MM.YYYY`
- `direction`: TEXT (`приход` / `расход`)
- `amount`: REAL for this chunk
- `payment_type`: TEXT canonical
- `plot`: TEXT canonical
- `fiscal_year`: INTEGER target year
- `category`: TEXT contribution ID or expense category
- `note`: TEXT shared across payment chunks
- `balance_after`: REAL running balance after row
- `payment_group_id`: TEXT UUID, groups rows from one user payment

New vs v1: `payment_group_id`.

Indexes:
- `op_date`
- `created_at`
- `(plot, fiscal_year)`

## 16. `.env` JSON Config

- `TELEGRAM_BOT_TOKEN`: bot token
- `TELEGRAM_ALLOWED_USER_IDS`: allowed Telegram IDs array
- `INITIAL_BALANCE`: starting balance
- `OPENAI_BASE_URL`: AI API base URL
- `OPENAI_API_KEY`: AI key
- `OPENAI_MODEL`: model name
- `CATEGORIES_INCOME`: income categories
- `CATEGORIES_EXPENSE`: expense categories
- `PAYMENT_TYPES`: payment types
- `PLOTS`: plot IDs
- `PLOT_MEMBERSHIP`: plot -> `Член` / `Индивидуал` / `-`
- `CONTRIBUTION_TYPES`: array of `{id, name, payer_type}`
- `CONTRIBUTION_PRIORITY_MEMBER`: ordered member contribution IDs
- `CONTRIBUTION_PRIORITY_INDIVIDUAL`: ordered individual contribution IDs
- `CONTRIBUTION_AMOUNTS`: `{contribution_id: amount}` annual charge map
- `DB_FILE`: SQLite path
- `STATE_TIMEOUT_MINUTES`: convo TTL
- `TEST_FIXTURES`: test amounts/object

New vs v1: `CONTRIBUTION_AMOUNTS`. Needed for debt math.

## 17. Non-Functional

- log AI + Telegram errs; no secret leak
- one active `ConversationState` per user
- new `Добавить операцию` while state alive -> warn, overwrite
- DB writes always transactional
- clear history on confirm, cancel, timeout (`STATE_TIMEOUT_MINUTES`)
- AI timeout configurable, default `180s`; retry once, then user-facing err

## 18. Tests

Distribution algo tests: 20 cases, same as v1 §14. Only deterministic Go layer. AI independent.

Inputs from `.env` `TEST_FIXTURES`:

- `TEST_FIXTURES.EXAMPLE_DUE_MEMBER`
- `TEST_FIXTURES.EXAMPLE_DUE_INDIVIDUAL`
- `TEST_FIXTURES.PAYMENTS.*`

Cases 1-20 unchanged. Distribution contract unchanged. Only entry path changed: convo, not FSM.

## 19. v1 -> v2 Diff

- field collection: 7-step FSM -> one AI convo loop
- AI role: per-field validator -> full extractor
- prompts: many files -> one system prompt
- validation: inline per-step -> AI extract, Go validate before confirm
- confirmation: implicit -> explicit Go-owned inline buttons
- DB writes: FSM end -> only after user confirm
- power user UX: always 7 prompts -> one msg can fill all
- novice UX: forced step-by-step -> AI asks only gaps
- distribution algo: unchanged
- data model: add `payment_group_id`
- config: add `CONTRIBUTION_AMOUNTS`
- prompts dir: single `prompts/extraction_agent.md`

## 20. Remote OpenAI-Compat Endpoint (35B)

Prod-grade extraction test endpoint over WireGuard:

- base URL: `http://10.8.0.4:8181`
- backend: `llama.cpp` on `stressii-wg`
- reach via SSH alias `hostkey_ru` + WireGuard
- model: `Qwen3.6-35B-A3B-Q4_K_M.gguf`

### Constraints

- Qwen thinking model. Internal reasoning cannot disable.
- Always use `max_tokens >= 5000`.
- Model may burn `1000+` tokens thinking before final content.
- `finish_reason: "length"` + empty `content` = budget died in thinking. Raise `max_tokens`.

### API paths

- `GET /v1/models`
- `POST /v1/chat/completions`

### Example

```bash
curl -s http://10.8.0.4:8181/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "Qwen3.6-35B-A3B-Q4_K_M.gguf",
    "messages": [
      {"role": "system", "content": "<system prompt here>"},
      {"role": "user", "content": "<user msg here>"}
    ],
    "max_tokens": 5000
  }'
```

### RTK note

RTK may hijack plain `curl` output, swap schema template. For raw JSON:

```bash
rtk proxy curl -s http://10.8.0.4:8181/v1/chat/completions ...
```
