package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strconv"
	"time"
)

// ---- Telegram API types ----

type TGUpdate struct {
	UpdateID int64      `json:"update_id"`
	Message  *TGMessage `json:"message"`
}

type TGMessage struct {
	MessageID int64    `json:"message_id"`
	From      *TGUser  `json:"from"`
	Chat      *TGChat  `json:"chat"`
	Text      string   `json:"text"`
	Date      int64    `json:"date"`
}

type TGUser struct {
	ID        int64  `json:"id"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Username  string `json:"username"`
}

type TGChat struct {
	ID int64 `json:"id"`
}

type tgGetUpdatesResp struct {
	OK     bool       `json:"ok"`
	Result []TGUpdate `json:"result"`
}

type tgSendMessageReq struct {
	ChatID    int64  `json:"chat_id"`
	Text      string `json:"text"`
	ParseMode string `json:"parse_mode,omitempty"`
}

// ---- Bot ----

type Bot struct {
	token   string
	baseURL string
	client  *http.Client
}

func NewBot(token string) *Bot {
	return &Bot{
		token:   token,
		baseURL: "https://api.telegram.org/bot" + token,
		client:  &http.Client{Timeout: 60 * time.Second},
	}
}

func (b *Bot) GetUpdates(offset int64) ([]TGUpdate, error) {
	url := fmt.Sprintf("%s/getUpdates?offset=%d&timeout=30&allowed_updates=[\"message\"]",
		b.baseURL, offset)
	resp, err := b.client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result tgGetUpdatesResp
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	if !result.OK {
		return nil, fmt.Errorf("telegram getUpdates returned not ok")
	}
	return result.Result, nil
}

func (b *Bot) SendMessage(chatID int64, text string) error {
	payload := tgSendMessageReq{
		ChatID:    chatID,
		Text:      text,
		ParseMode: "HTML",
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	resp, err := b.client.Post(b.baseURL+"/sendMessage", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

func (b *Bot) SendDocument(chatID int64, filename string, data []byte) error {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)

	// chat_id field
	if err := w.WriteField("chat_id", strconv.FormatInt(chatID, 10)); err != nil {
		return err
	}

	// document file field
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="document"; filename="%s"`, filename))
	h.Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	part, err := w.CreatePart(h)
	if err != nil {
		return err
	}
	if _, err := io.Copy(part, bytes.NewReader(data)); err != nil {
		return err
	}
	w.Close()

	req, err := http.NewRequest("POST", b.baseURL+"/sendDocument", &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())

	resp, err := b.client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// Poll runs the long-polling loop and calls handler for each message.
func (b *Bot) Poll(handler func(msg *TGMessage)) {
	var offset int64
	for {
		updates, err := b.GetUpdates(offset)
		if err != nil {
			fmt.Println("getUpdates error:", err)
			time.Sleep(5 * time.Second)
			continue
		}
		for _, u := range updates {
			offset = u.UpdateID + 1
			if u.Message != nil && u.Message.From != nil {
				handler(u.Message)
			}
		}
	}
}

// DisplayName returns a human-readable name for a Telegram user.
func DisplayName(u *TGUser) string {
	if u == nil {
		return "unknown"
	}
	name := u.FirstName
	if u.LastName != "" {
		name += " " + u.LastName
	}
	if u.Username != "" {
		name += " (@" + u.Username + ")"
	}
	return name
}
