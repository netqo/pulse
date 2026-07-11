package notify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTelegramSenderSend(t *testing.T) {
	ctx := context.Background()

	t.Run("posts chat_id and text to the bot method URL", func(t *testing.T) {
		var gotPath string
		var gotBody telegramMessage
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			if ct := r.Header.Get("Content-Type"); ct != "application/json" {
				t.Errorf("content-type = %q, want application/json", ct)
			}
			if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
				t.Errorf("decode body: %v", err)
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))
		}))
		defer srv.Close()

		sender := NewTelegramSender(srv.Client(), "TESTTOKEN")
		sender.baseURL = srv.URL

		if err := sender.Send(ctx, "42", "BTCUSDT below 25000"); err != nil {
			t.Fatalf("Send: %v", err)
		}
		if gotPath != "/botTESTTOKEN/sendMessage" {
			t.Errorf("path = %q, want /botTESTTOKEN/sendMessage", gotPath)
		}
		if gotBody.ChatID != "42" || gotBody.Text != "BTCUSDT below 25000" {
			t.Errorf("body = %+v", gotBody)
		}
	})

	t.Run("non-2xx response is an error carrying the status", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"ok":false,"description":"chat not found"}`))
		}))
		defer srv.Close()

		sender := NewTelegramSender(srv.Client(), "TESTTOKEN")
		sender.baseURL = srv.URL

		err := sender.Send(ctx, "42", "hello")
		if err == nil {
			t.Fatal("Send err = nil, want an error")
		}
		if !strings.Contains(err.Error(), "400") || !strings.Contains(err.Error(), "chat not found") {
			t.Errorf("err = %v, want it to mention the status and description", err)
		}
	})
}
