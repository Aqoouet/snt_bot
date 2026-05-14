package model_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

const callTimeout = 240 * time.Second

// chatMsg is one turn in the conversation.
type chatMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// chatCompletionReq follows the OpenAI /v1/chat/completions schema.
// ResponseFormat is intentionally omitted: llama.cpp crashes when json_schema
// grammar is combined with Qwen3 thinking tokens (</think> contains '/' which
// causes "Unexpected empty grammar stack" in llama_grammar_accept_token).
// Qwen3.6-35B is a thinking-only model — enable_thinking cannot be disabled.
// MaxTokens must be >= 5000 to allow thinking tokens before the JSON output.
type chatCompletionReq struct {
	Model       string    `json:"model"`
	Messages    []chatMsg `json:"messages"`
	MaxTokens   int       `json:"max_tokens"`
	Temperature float64   `json:"temperature"`
	TopK        int       `json:"top_k,omitempty"`
	TopP        float64   `json:"top_p,omitempty"`
}

type respFields struct {
	Date        *string     `json:"date"`
	Direction   *string     `json:"direction"`
	Amount      interface{} `json:"amount"`
	PaymentType *string     `json:"payment_type"`
	Plot        *string     `json:"plot"`
	Category    *string     `json:"category"`
	Note        *string     `json:"note"`
}

type modelResp struct {
	Status  string     `json:"status"`
	Message string     `json:"message"`
	Fields  respFields `json:"fields"`
}

// responseFormat instructs the model to return a validated JSON structure.
var responseFormat = map[string]interface{}{
	"type": "json_schema",
	"json_schema": map[string]interface{}{
		"name":   "extraction_response",
		"strict": true,
		"schema": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"status":  map[string]interface{}{"type": "string", "enum": []string{"extracting", "ready", "abort"}},
				"message": map[string]interface{}{"type": "string"},
				"fields": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"date":         map[string]interface{}{"type": []interface{}{"string", "null"}},
						"direction":    map[string]interface{}{"type": []interface{}{"string", "null"}},
						"amount":       map[string]interface{}{"type": []interface{}{"number", "null"}},
						"payment_type": map[string]interface{}{"type": []interface{}{"string", "null"}},
						"plot":         map[string]interface{}{"type": []interface{}{"string", "null"}},
						"category":     map[string]interface{}{"type": []interface{}{"string", "null"}},
						"note":         map[string]interface{}{"type": []interface{}{"string", "null"}},
					},
					"required":             []string{"date", "direction", "amount", "payment_type", "plot", "category", "note"},
					"additionalProperties": false,
				},
			},
			"required":             []string{"status", "message", "fields"},
			"additionalProperties": false,
		},
	},
}

var (
	chatURL   string
	modelName string
	sysPrmpt  string
	todayStr  string
	yesterStr string
)

func TestMain(m *testing.M) {
	// Date injection: set TEST_DATE=DD.MM.YYYY in CI to avoid midnight-spanning flakes.
	if d := os.Getenv("TEST_DATE"); d != "" {
		parsed, err := time.Parse("02.01.2006", d)
		if err != nil {
			fmt.Fprintf(os.Stderr, "invalid TEST_DATE %q: %v\n", d, err)
			os.Exit(1)
		}
		todayStr = d
		yesterStr = parsed.AddDate(0, 0, -1).Format("02.01.2006")
	} else {
		now := time.Now()
		todayStr = now.Format("02.01.2006")
		yesterStr = now.AddDate(0, 0, -1).Format("02.01.2006")
	}

	envRaw, err := os.ReadFile("../.env")
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot read .env: %v\n", err)
		os.Exit(1)
	}
	var env map[string]json.RawMessage
	if err := json.Unmarshal(envRaw, &env); err != nil {
		fmt.Fprintf(os.Stderr, "cannot parse .env: %v\n", err)
		os.Exit(1)
	}

	baseURL := strings.Trim(string(env["OPENAI_BASE_URL"]), `"`)
	if baseURL == "" {
		baseURL = "http://127.0.0.1:8080"
	}
	chatURL = baseURL + "/v1/chat/completions"
	modelName = strings.Trim(string(env["OPENAI_MODEL"]), `"`)

	// Health probe — exit cleanly (not failure) if server is not available.
	hctx, hcancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer hcancel()
	hreq, _ := http.NewRequestWithContext(hctx, "GET", baseURL+"/health", nil)
	hresp, herr := http.DefaultClient.Do(hreq)
	if herr != nil || hresp.StatusCode != 200 {
		fmt.Fprintln(os.Stderr, "llama server unavailable — skipping all model tests")
		os.Exit(0)
	}
	hresp.Body.Close()

	tpl, err := os.ReadFile("../prompts/extraction_agent.md")
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot read prompt: %v\n", err)
		os.Exit(1)
	}
	sysPrmpt = buildPrompt(string(tpl), env, todayStr, yesterStr)
	os.Exit(m.Run())
}

func buildPrompt(tpl string, env map[string]json.RawMessage, today, yesterday string) string {
	sub := func(key string) string {
		raw, ok := env[key]
		if !ok {
			return key
		}
		return string(raw)
	}
	r := tpl
	r = strings.ReplaceAll(r, "{{PAYMENT_TYPES}}", sub("PAYMENT_TYPES"))
	r = strings.ReplaceAll(r, "{{PLOTS}}", sub("PLOTS"))
	r = strings.ReplaceAll(r, "{{CATEGORIES_INCOME}}", sub("CATEGORIES_INCOME"))
	r = strings.ReplaceAll(r, "{{CATEGORIES_EXPENSE}}", sub("CATEGORIES_EXPENSE"))
	r = strings.ReplaceAll(r, "{{TODAY}}", today)
	r = strings.ReplaceAll(r, "{{YESTERDAY}}", yesterday)
	return r
}

// callModel sends history to /v1/chat/completions with the system prompt prepended.
// Uses context with timeout; does not use client.Timeout.
// MaxTokens is set high enough for Qwen3 thinking tokens + JSON output.
func callModel(t *testing.T, history []chatMsg) modelResp {
	t.Helper()

	messages := make([]chatMsg, 0, len(history)+1)
	messages = append(messages, chatMsg{Role: "system", Content: sysPrmpt})
	messages = append(messages, history...)

	payload := chatCompletionReq{
		Model:       modelName,
		Messages:    messages,
		MaxTokens:   5000,
		Temperature: 0.1,
		TopK:        20,
		TopP:        0.95,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", chatURL, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("model call failed: %v", err)
	}
	defer res.Body.Close()

	raw, _ := io.ReadAll(res.Body)

	var outer struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &outer); err != nil || len(outer.Choices) == 0 {
		t.Fatalf("bad /v1/chat/completions response (HTTP %d): %s",
			res.StatusCode, truncateForLog(raw, 400))
	}

	content := strings.TrimSpace(outer.Choices[0].Message.Content)
	// Extract the JSON object: find the first '{' and the last '}'.
	// The model may wrap the output in markdown code fences or add a short prefix.
	startIdx := strings.Index(content, "{")
	endIdx := strings.LastIndex(content, "}")
	if startIdx < 0 || endIdx < startIdx {
		t.Fatalf("no JSON object in model response\ncontent: %q", content)
	}
	content = content[startIdx : endIdx+1]
	var resp modelResp
	if err := json.Unmarshal([]byte(content), &resp); err != nil {
		t.Fatalf("JSON parse failed: %v\ncontent: %q", err, content)
	}
	return resp
}

// truncateForLog caps log output and never prints known secret values.
func truncateForLog(data []byte, maxLen int) string {
	s := string(data)
	if len(s) > maxLen {
		s = s[:maxLen] + "...[truncated]"
	}
	return s
}

// assertField checks that a nullable string field has the expected value.
func assertField(t *testing.T, name string, got *string, want string) {
	t.Helper()
	if got == nil {
		t.Errorf("field %s: nil, want %q", name, want)
		return
	}
	if *got != want {
		t.Errorf("field %s: got %q, want %q", name, *got, want)
	}
}

// assertAmount checks that the amount field (typed as interface{} due to JSON) equals want.
func assertAmount(t *testing.T, got interface{}, want float64) {
	t.Helper()
	if got == nil {
		t.Errorf("amount: nil, want %v", want)
		return
	}
	v, ok := got.(float64)
	if !ok {
		t.Errorf("amount not a number: %T %v", got, got)
		return
	}
	if v != want {
		t.Errorf("amount: got %v, want %v", v, want)
	}
}

// assertExtracting verifies that a turn produced status=extracting with a valid follow-up question.
// Per SPEC §10.2: must ask only about missing/ambiguous fields; never ask for Членство.
func assertExtracting(t *testing.T, r modelResp, turn string) {
	t.Helper()
	if r.Status != "extracting" {
		t.Errorf("%s status: got %q, want extracting. message: %s", turn, r.Status, r.Message)
	}
	if r.Message == "" {
		t.Errorf("%s message: empty, expected a follow-up question", turn)
	}
	if strings.Contains(strings.ToLower(r.Message), "членство") {
		t.Errorf("%s message: must not ask for computed field Членство, got: %q", turn, r.Message)
	}
}

// mustMarshal encodes v as JSON and panics on error (test helper, never called with invalid input).
func mustMarshal(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("mustMarshal: %v", err))
	}
	return string(b)
}

// TestExtractionSingleTurn covers SC01, SC02, SC06–SC14, SC17–SC20 as table-driven cases.
// Each input contains all required fields in one message → status must be "ready".
func TestExtractionSingleTurn(t *testing.T) {
	str := func(s string) *string { return &s }
	flt := func(f float64) *float64 { return &f }

	type wantFields struct {
		status       string   // if empty, not checked
		dir          *string  // direction
		amount       *float64
		ptype        *string  // payment_type
		plot         *string
		category     *string
		date         *string
		noteNonEmpty bool
	}

	cases := []struct {
		name  string
		input string
		want  wantFields
	}{
		{
			name:  "SC01_full_income_nal",
			input: "5000 нал участок 15 членские сегодня",
			want: wantFields{
				status:   "ready",
				dir:      str("приход"),
				amount:   flt(5000),
				ptype:    str("Нал"),
				plot:     str("15"),
				category: str("Членские"),
				date:     &todayStr,
			},
		},
		{
			name:  "SC02_full_expense_karta",
			input: "расход 2500 карта участок 22 мусор 10.05.2026",
			want: wantFields{
				status:   "ready",
				dir:      str("расход"),
				amount:   flt(2500),
				ptype:    str("Карта"),
				plot:     str("22"),
				category: str("Мусор"),
				date:     str("10.05.2026"),
			},
		},
		{
			name:  "SC06_date_today",
			input: "1000 нал участок 5 приход Целевые сегодня",
			want:  wantFields{status: "ready", date: &todayStr},
		},
		{
			name:  "SC07_date_yesterday",
			input: "800 онлайн участок 9 приход Членские вчера",
			want:  wantFields{status: "ready", date: &yesterStr},
		},
		{
			name:  "SC08_date_explicit",
			input: "2000 карта участок 20 расход Председатель 01.03.2026",
			want: wantFields{
				status:   "ready",
				dir:      str("расход"),
				category: str("Председатель"),
				ptype:    str("Карта"),
				date:     str("01.03.2026"),
			},
		},
		{
			name:  "SC09_canonical_payment_nalichnymi",
			input: "5000 наличными участок 15 приход Членские сегодня",
			want:  wantFields{ptype: str("Нал")},
		},
		{
			name:  "SC10_canonical_payment_kartochkoy",
			input: "1500 карточкой участок 22 приход Целевые сегодня",
			want:  wantFields{ptype: str("Карта")},
		},
		{
			name:  "SC11_canonical_payment_perevod",
			input: "2000 перевод участок 5 приход Членские сегодня",
			want:  wantFields{ptype: str("Онлайн")},
		},
		{
			name:  "SC12_amount_text_pyat_tysyach",
			input: "пять тысяч нал участок 15 приход Членские сегодня",
			want:  wantFields{amount: flt(5000)},
		},
		{
			name:  "SC13_amount_text_tysyacha_pyatsot",
			input: "тысяча пятьсот онлайн участок 33 приход Целевые сегодня",
			want:  wantFields{amount: flt(1500)},
		},
		{
			name:  "SC14_note_field_populated",
			input: "5000 нал участок 15 приход Членские сегодня оплатил на общем собрании",
			want:  wantFields{noteNonEmpty: true},
		},
		{
			name:  "SC17_named_plot_vorobyev",
			input: "3000 нал Воробьев расход Рем_проезд сегодня",
			want: wantFields{
				status:   "ready",
				dir:      str("расход"),
				plot:     str("Воробьев"),
				category: str("Рем_проезд"),
			},
		},
		{
			name:  "SC18_category_synonym_proezdy",
			input: "2000 онлайн участок 5 приход проезды сегодня",
			want:  wantFields{category: str("Ц_Проезды")},
		},
		{
			name:  "SC19_expense_svet_schet",
			input: "15000 счет участок 1 расход свет 01.04.2026",
			want: wantFields{
				status:   "ready",
				dir:      str("расход"),
				category: str("Свет"),
				ptype:    str("Счет"),
				date:     str("01.04.2026"),
				plot:     str("1"),
			},
		},
		{
			name:  "SC20_named_plot_baklykova_with_spaces",
			input: "1000 нал Баклыкова ВВ приход Членские сегодня",
			want: wantFields{
				status:   "ready",
				dir:      str("приход"),
				plot:     str("Баклыкова ВВ"),
				category: str("Членские"),
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			r := callModel(t, []chatMsg{{Role: "user", Content: tc.input}})
			if tc.want.status != "" && r.Status != tc.want.status {
				t.Errorf("status: got %q, want %q. message: %s", r.Status, tc.want.status, r.Message)
			}
			if tc.want.dir != nil {
				assertField(t, "direction", r.Fields.Direction, *tc.want.dir)
			}
			if tc.want.amount != nil {
				assertAmount(t, r.Fields.Amount, *tc.want.amount)
			}
			if tc.want.ptype != nil {
				assertField(t, "payment_type", r.Fields.PaymentType, *tc.want.ptype)
			}
			if tc.want.plot != nil {
				assertField(t, "plot", r.Fields.Plot, *tc.want.plot)
			}
			if tc.want.category != nil {
				assertField(t, "category", r.Fields.Category, *tc.want.category)
			}
			if tc.want.date != nil {
				assertField(t, "date", r.Fields.Date, *tc.want.date)
			}
			if tc.want.noteNonEmpty {
				if r.Fields.Note == nil || *r.Fields.Note == "" {
					t.Error("note: expected non-empty note for extra context")
				}
			}
		})
	}
}

// SC03: multi-turn, first message missing date/category/direction.
// Verifies partial field preservation on extracting turns (SPEC §10.3).
func TestSC03_MultiTurnMissingFields(t *testing.T) {
	history := []chatMsg{
		{Role: "user", Content: "5000 нал участок 15"},
	}
	r1 := callModel(t, history)
	assertExtracting(t, r1, "turn1")
	// Fields confirmed so far must be preserved (SPEC §10.3 "whatever confirmed so far").
	assertAmount(t, r1.Fields.Amount, 5000)
	assertField(t, "payment_type", r1.Fields.PaymentType, "Нал")
	assertField(t, "plot", r1.Fields.Plot, "15")

	history = append(history,
		chatMsg{Role: "assistant", Content: mustMarshal(r1)},
		chatMsg{Role: "user", Content: "приход членские сегодня"},
	)
	r2 := callModel(t, history)
	if r2.Status != "ready" {
		t.Errorf("turn2 status: got %q, want ready. message: %s", r2.Status, r2.Message)
	}
	assertField(t, "direction", r2.Fields.Direction, "приход")
	assertField(t, "category", r2.Fields.Category, "Членские")
	assertField(t, "date", r2.Fields.Date, todayStr)
	assertAmount(t, r2.Fields.Amount, 5000)
	assertField(t, "payment_type", r2.Fields.PaymentType, "Нал")
	assertField(t, "plot", r2.Fields.Plot, "15")
}

// SC04: multi-turn, only amount given first.
// Verifies amount is preserved across turns.
func TestSC04_MultiTurnOnlyAmount(t *testing.T) {
	history := []chatMsg{
		{Role: "user", Content: "3000"},
	}
	r1 := callModel(t, history)
	assertExtracting(t, r1, "turn1")
	assertAmount(t, r1.Fields.Amount, 3000)

	history = append(history,
		chatMsg{Role: "assistant", Content: mustMarshal(r1)},
		chatMsg{Role: "user", Content: "онлайн участок 33 расход свет вчера"},
	)
	r2 := callModel(t, history)
	if r2.Status != "ready" {
		t.Errorf("turn2 status: got %q, want ready. message: %s", r2.Status, r2.Message)
	}
	assertAmount(t, r2.Fields.Amount, 3000)
	assertField(t, "payment_type", r2.Fields.PaymentType, "Онлайн")
	assertField(t, "plot", r2.Fields.Plot, "33")
	assertField(t, "direction", r2.Fields.Direction, "расход")
	assertField(t, "category", r2.Fields.Category, "Свет")
	assertField(t, "date", r2.Fields.Date, yesterStr)
}

// SC05: multi-turn, 3 turns filling fields one-by-one.
// Turn 2 must be status=extracting (date and category still missing after turn 2).
// All previously confirmed fields must be preserved at each extracting turn.
func TestSC05_MultiTurnThreeTurns(t *testing.T) {
	history := []chatMsg{
		{Role: "user", Content: "расход 800"},
	}
	r1 := callModel(t, history)
	assertExtracting(t, r1, "turn1")
	assertField(t, "direction", r1.Fields.Direction, "расход")
	assertAmount(t, r1.Fields.Amount, 800)

	history = append(history,
		chatMsg{Role: "assistant", Content: mustMarshal(r1)},
		chatMsg{Role: "user", Content: "карта участок 7"},
	)
	r2 := callModel(t, history)
	// Date and category still missing → must still be extracting.
	assertExtracting(t, r2, "turn2")
	// All fields from previous turns must be preserved.
	assertField(t, "direction", r2.Fields.Direction, "расход")
	assertAmount(t, r2.Fields.Amount, 800)
	assertField(t, "payment_type", r2.Fields.PaymentType, "Карта")
	assertField(t, "plot", r2.Fields.Plot, "7")

	history = append(history,
		chatMsg{Role: "assistant", Content: mustMarshal(r2)},
		chatMsg{Role: "user", Content: "почта 05.04.2026"},
	)
	r3 := callModel(t, history)
	if r3.Status != "ready" {
		t.Errorf("turn3 status: got %q, want ready. message: %s", r3.Status, r3.Message)
	}
	assertField(t, "direction", r3.Fields.Direction, "расход")
	assertAmount(t, r3.Fields.Amount, 800)
	assertField(t, "payment_type", r3.Fields.PaymentType, "Карта")
	assertField(t, "plot", r3.Fields.Plot, "7")
	assertField(t, "category", r3.Fields.Category, "Почта")
	assertField(t, "date", r3.Fields.Date, "05.04.2026")
}

// SC15: abort with "Отмена".
// Verifies partial fields preserved at extracting turn before abort.
func TestSC15_AbortOtmena(t *testing.T) {
	history := []chatMsg{
		{Role: "user", Content: "5000 нал"},
	}
	r1 := callModel(t, history)
	assertExtracting(t, r1, "turn1")
	assertAmount(t, r1.Fields.Amount, 5000)
	assertField(t, "payment_type", r1.Fields.PaymentType, "Нал")

	history = append(history,
		chatMsg{Role: "assistant", Content: mustMarshal(r1)},
		chatMsg{Role: "user", Content: "Отмена"},
	)
	r2 := callModel(t, history)
	if r2.Status != "abort" {
		t.Errorf("turn2 status: got %q, want abort", r2.Status)
	}
}

// SC16: abort with "стоп".
// Verifies partial fields preserved at extracting turn before abort.
func TestSC16_AbortStop(t *testing.T) {
	history := []chatMsg{
		{Role: "user", Content: "расход 1000"},
	}
	r1 := callModel(t, history)
	assertExtracting(t, r1, "turn1")
	assertField(t, "direction", r1.Fields.Direction, "расход")
	assertAmount(t, r1.Fields.Amount, 1000)

	history = append(history,
		chatMsg{Role: "assistant", Content: mustMarshal(r1)},
		chatMsg{Role: "user", Content: "стоп"},
	)
	r2 := callModel(t, history)
	if r2.Status != "abort" {
		t.Errorf("turn2 status: got %q, want abort", r2.Status)
	}
}
