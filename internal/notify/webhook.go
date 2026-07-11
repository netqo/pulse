package notify

import (
	"context"
	"fmt"
	"net/http"
)

// WebhookSender delivers messages by POSTing a small JSON document to a
// caller-supplied URL, which is the Send target. It is the generic channel for
// integrations that accept a simple {"text": ...} payload.
type WebhookSender struct {
	client *http.Client
}

// NewWebhookSender builds a generic webhook sender. A nil client falls back to
// http.DefaultClient.
func NewWebhookSender(client *http.Client) *WebhookSender {
	return &WebhookSender{client: httpClient(client)}
}

// webhookPayload is the generic webhook body.
type webhookPayload struct {
	Text string `json:"text"`
}

// Send POSTs message as {"text": ...} to the URL given by target.
func (s *WebhookSender) Send(ctx context.Context, target, message string) error {
	if err := postJSON(ctx, s.client, target, webhookPayload{Text: message}); err != nil {
		return fmt.Errorf("notify: webhook: %w", err)
	}
	return nil
}

// DiscordSender delivers messages to a Discord incoming webhook URL (the Send
// target) using Discord's expected {"content": ...} payload.
type DiscordSender struct {
	client *http.Client
}

// NewDiscordSender builds a Discord webhook sender. A nil client falls back to
// http.DefaultClient.
func NewDiscordSender(client *http.Client) *DiscordSender {
	return &DiscordSender{client: httpClient(client)}
}

// discordPayload is the Discord incoming-webhook body.
type discordPayload struct {
	Content string `json:"content"`
}

// Send POSTs message as {"content": ...} to the Discord webhook URL given by
// target.
func (s *DiscordSender) Send(ctx context.Context, target, message string) error {
	if err := postJSON(ctx, s.client, target, discordPayload{Content: message}); err != nil {
		return fmt.Errorf("notify: discord: %w", err)
	}
	return nil
}
