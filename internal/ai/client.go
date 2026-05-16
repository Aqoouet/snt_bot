package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const modelCallTimeout = 300 * time.Second

type Msg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type Fields struct {
	Date        *string     `json:"date"`
	Direction   *string     `json:"direction"`
	Amount      interface{} `json:"amount"`
	PaymentType *string     `json:"payment_type"`
	Plot        *string     `json:"plot"`
	Category    *string     `json:"category"`
	Note        *string     `json:"note"`
}

func (f Fields) AmountFloat() float64 {
	if f.Amount == nil {
		return 0
	}
	v, ok := f.Amount.(float64)
	if !ok {
		return 0
	}
	return v
}

type Response struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	Fields  Fields `json:"fields"`
}

type PlotResponse struct {
	Status  string `json:"status"`
	Plot    string `json:"plot"`
	Message string `json:"message"`
}

// chatCompletionReq omits ResponseFormat: llama.cpp crashes when json_schema
// grammar combines with Qwen3 thinking tokens (</think> triggers grammar stack error).
type chatCompletionReq struct {
	Model       string  `json:"model"`
	Messages    []Msg   `json:"messages"`
	MaxTokens   int     `json:"max_tokens"`
	Temperature float64 `json:"temperature"`
	TopK        int     `json:"top_k,omitempty"`
	TopP        float64 `json:"top_p,omitempty"`
}

type Client struct {
	chatURL string
	baseURL string
	model   string
	apiKey  string
}

func NewClient(baseURL, model, apiKey string) *Client {
	return &Client{
		chatURL: baseURL + "/v1/chat/completions",
		baseURL: baseURL,
		model:   model,
		apiKey:  apiKey,
	}
}

func (c *Client) Healthy() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/health", nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == 200
}

func (c *Client) callRaw(ctx context.Context, sysPrompt string, history []Msg) (string, error) {
	messages := make([]Msg, 0, len(history)+1)
	messages = append(messages, Msg{Role: "system", Content: sysPrompt})
	messages = append(messages, history...)

	payload := chatCompletionReq{
		Model:       c.model,
		Messages:    messages,
		MaxTokens:   5000,
		Temperature: 0.1,
		TopK:        20,
		TopP:        0.95,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	callCtx, cancel := context.WithTimeout(ctx, modelCallTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(callCtx, "POST", c.chatURL, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("model call: %w", err)
	}
	defer res.Body.Close()

	raw, err := io.ReadAll(res.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	var outer struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if res.StatusCode != http.StatusOK {
		snippet := string(raw)
		if len(snippet) > 300 {
			snippet = snippet[:300]
		}
		return "", fmt.Errorf("bad completions response (HTTP %d): %s", res.StatusCode, snippet)
	}
	if err := json.Unmarshal(raw, &outer); err != nil || len(outer.Choices) == 0 {
		return "", fmt.Errorf("bad completions response (HTTP %d)", res.StatusCode)
	}

	content := strings.TrimSpace(outer.Choices[0].Message.Content)
	startIdx := strings.Index(content, "{")
	endIdx := strings.LastIndex(content, "}")
	if startIdx < 0 || endIdx < startIdx {
		return "", fmt.Errorf("no JSON object in model response")
	}
	return content[startIdx : endIdx+1], nil
}

func (c *Client) CallWithSysPrompt(ctx context.Context, sysPrompt string, history []Msg) (Response, error) {
	content, err := c.callRaw(ctx, sysPrompt, history)
	if err != nil && ctx.Err() == nil {
		content, err = c.callRaw(ctx, sysPrompt, history)
	}
	if err != nil {
		return Response{}, err
	}
	var resp Response
	if err := json.Unmarshal([]byte(content), &resp); err != nil {
		return Response{}, fmt.Errorf("parse model response: %w", err)
	}
	return resp, nil
}

func (c *Client) CallPlot(ctx context.Context, sysPrompt string, history []Msg) (PlotResponse, error) {
	content, err := c.callRaw(ctx, sysPrompt, history)
	if err != nil && ctx.Err() == nil {
		content, err = c.callRaw(ctx, sysPrompt, history)
	}
	if err != nil {
		return PlotResponse{}, err
	}
	var resp PlotResponse
	if err := json.Unmarshal([]byte(content), &resp); err != nil {
		return PlotResponse{}, fmt.Errorf("parse plot response: %w", err)
	}
	return resp, nil
}

// BuildPrompt substitutes {{PLACEHOLDERS}} in the system prompt template.
// Slices are JSON-encoded so the model sees proper JSON arrays.
func BuildPrompt(tpl string, paymentTypes, plots, categoriesIncome, categoriesExpense []string, today, yesterday string) string {
	marshal := func(v interface{}) string {
		b, _ := json.Marshal(v)
		return string(b)
	}
	r := tpl
	r = strings.ReplaceAll(r, "{{PAYMENT_TYPES}}", marshal(paymentTypes))
	r = strings.ReplaceAll(r, "{{PLOTS}}", marshal(plots))
	r = strings.ReplaceAll(r, "{{CATEGORIES_INCOME}}", marshal(categoriesIncome))
	r = strings.ReplaceAll(r, "{{CATEGORIES_EXPENSE}}", marshal(categoriesExpense))
	r = strings.ReplaceAll(r, "{{TODAY}}", today)
	r = strings.ReplaceAll(r, "{{YESTERDAY}}", yesterday)
	return r
}

// BuildPlotPrompt substitutes {{PLOTS}} in the plot extraction prompt template.
func BuildPlotPrompt(tpl string, plots []string) string {
	b, _ := json.Marshal(plots)
	return strings.ReplaceAll(tpl, "{{PLOTS}}", string(b))
}
