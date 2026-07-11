// Package notify delivers rendered alert notifications to external channels
// (Telegram, Discord, generic webhooks). It is deliberately decoupled from the
// alerting domain: a Sender receives a target and an already-rendered message
// string, so the package knows nothing about rules, thresholds or the database.
//
// A Dispatcher routes a message to the Sender registered for a channel name,
// which is how the Alerting service turns a rule's (channel, target) pair into a
// delivered notification.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// maxErrorBodyBytes bounds how much of a failed response body is quoted back in
// an error, so a large or hostile payload cannot bloat logs.
const maxErrorBodyBytes = 512

// ErrUnknownChannel is returned by Dispatch when no Sender is registered for the
// requested channel.
var ErrUnknownChannel = errors.New("notify: no sender configured for channel")

// Sender delivers a message to a single destination within one channel. target
// identifies the destination in that channel's terms (a Telegram chat id, a
// webhook URL); message is the fully rendered text to deliver.
type Sender interface {
	Send(ctx context.Context, target, message string) error
}

// Dispatcher routes a message to the Sender registered for a channel.
type Dispatcher struct {
	senders map[string]Sender
}

// NewDispatcher builds a Dispatcher over a channel-name to Sender mapping. The
// map is used as given; callers register only the channels they have configured
// (for example, Telegram only when a bot token is present).
func NewDispatcher(senders map[string]Sender) *Dispatcher {
	return &Dispatcher{senders: senders}
}

// Dispatch delivers message to target over channel, returning ErrUnknownChannel
// when no Sender is registered for that channel and otherwise whatever the
// Sender reports.
func (d *Dispatcher) Dispatch(ctx context.Context, channel, target, message string) error {
	sender, ok := d.senders[channel]
	if !ok {
		return fmt.Errorf("%w: %q", ErrUnknownChannel, channel)
	}
	return sender.Send(ctx, target, message)
}

// Supports reports whether a Sender is registered for channel, letting a caller
// reject a rule targeting an unconfigured channel before attempting delivery.
func (d *Dispatcher) Supports(channel string) bool {
	_, ok := d.senders[channel]
	return ok
}

// postJSON marshals payload as JSON and POSTs it to url, returning nil only when
// the response status is 2xx. A non-2xx response yields an error carrying the
// status and a bounded slice of the body to aid debugging.
func postJSON(ctx context.Context, client *http.Client, url string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("notify: marshal payload: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("notify: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("notify: send request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
		return fmt.Errorf("notify: unexpected status %d: %s", resp.StatusCode, bytes.TrimSpace(snippet))
	}
	// Drain the body so the connection can be reused by the client's pool.
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

// httpClient returns client, or http.DefaultClient when client is nil, so a
// Sender always has a usable client without forcing every caller to build one.
func httpClient(client *http.Client) *http.Client {
	if client == nil {
		return http.DefaultClient
	}
	return client
}
