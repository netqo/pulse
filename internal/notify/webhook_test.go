package notify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWebhookSenderSend(t *testing.T) {
	ctx := context.Background()

	t.Run("posts a text payload and accepts 2xx", func(t *testing.T) {
		var got webhookPayload
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
				t.Errorf("decode body: %v", err)
			}
			w.WriteHeader(http.StatusNoContent)
		}))
		defer srv.Close()

		if err := NewWebhookSender(srv.Client()).Send(ctx, srv.URL, "fired"); err != nil {
			t.Fatalf("Send: %v", err)
		}
		if got.Text != "fired" {
			t.Errorf("payload = %+v, want text=fired", got)
		}
	})

	t.Run("non-2xx is an error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer srv.Close()

		err := NewWebhookSender(srv.Client()).Send(ctx, srv.URL, "fired")
		if err == nil || !strings.Contains(err.Error(), "500") {
			t.Errorf("err = %v, want an error mentioning 500", err)
		}
	})
}

func TestDiscordSenderSend(t *testing.T) {
	ctx := context.Background()

	var got discordPayload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode body: %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	if err := NewDiscordSender(srv.Client()).Send(ctx, srv.URL, "fired"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got.Content != "fired" {
		t.Errorf("payload = %+v, want content=fired", got)
	}
}
