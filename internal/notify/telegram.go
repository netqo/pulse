package notify

import (
	"context"
	"fmt"
	"net/http"
)

// defaultTelegramAPI is the base URL of the Telegram Bot API. It is a field on
// the sender so tests can point it at a local server.
const defaultTelegramAPI = "https://api.telegram.org"

// TelegramSender delivers messages through the Telegram Bot API. The target of a
// Send is a chat id (a user, group or channel the bot can post to).
type TelegramSender struct {
	client  *http.Client
	baseURL string
	token   string
}

// NewTelegramSender builds a TelegramSender for the given bot token. A nil client
// falls back to http.DefaultClient; callers should supply one with a timeout.
func NewTelegramSender(client *http.Client, token string) *TelegramSender {
	return &TelegramSender{
		client:  httpClient(client),
		baseURL: defaultTelegramAPI,
		token:   token,
	}
}

// telegramMessage is the sendMessage request payload. Messages are sent as plain
// text (no parse_mode) so a rendered value never needs Markdown or HTML escaping.
type telegramMessage struct {
	ChatID string `json:"chat_id"`
	Text   string `json:"text"`
}

// Send posts message to the chat identified by target via the Bot API.
func (s *TelegramSender) Send(ctx context.Context, target, message string) error {
	url := fmt.Sprintf("%s/bot%s/sendMessage", s.baseURL, s.token)
	if err := postJSON(ctx, s.client, url, telegramMessage{ChatID: target, Text: message}); err != nil {
		return fmt.Errorf("notify: telegram: %w", err)
	}
	return nil
}
