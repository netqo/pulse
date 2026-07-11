package notify

import (
	"context"
	"errors"
	"testing"
)

// recordingSender captures the arguments of the last Send and returns a
// configurable error, so the Dispatcher can be tested without real I/O.
type recordingSender struct {
	target, message string
	called          bool
	err             error
}

func (s *recordingSender) Send(_ context.Context, target, message string) error {
	s.called = true
	s.target = target
	s.message = message
	return s.err
}

func TestDispatcher(t *testing.T) {
	ctx := context.Background()

	t.Run("routes to the sender registered for the channel", func(t *testing.T) {
		tg := &recordingSender{}
		d := NewDispatcher(map[string]Sender{"telegram": tg})

		if err := d.Dispatch(ctx, "telegram", "42", "hello"); err != nil {
			t.Fatalf("Dispatch: %v", err)
		}
		if !tg.called || tg.target != "42" || tg.message != "hello" {
			t.Errorf("sender got called=%v target=%q message=%q", tg.called, tg.target, tg.message)
		}
	})

	t.Run("unknown channel yields ErrUnknownChannel", func(t *testing.T) {
		d := NewDispatcher(map[string]Sender{"telegram": &recordingSender{}})
		err := d.Dispatch(ctx, "discord", "url", "hello")
		if !errors.Is(err, ErrUnknownChannel) {
			t.Errorf("err = %v, want ErrUnknownChannel", err)
		}
	})

	t.Run("propagates the sender's error", func(t *testing.T) {
		wantErr := errors.New("boom")
		d := NewDispatcher(map[string]Sender{"webhook": &recordingSender{err: wantErr}})
		if err := d.Dispatch(ctx, "webhook", "url", "hello"); !errors.Is(err, wantErr) {
			t.Errorf("err = %v, want %v", err, wantErr)
		}
	})

	t.Run("Supports reflects the registered channels", func(t *testing.T) {
		d := NewDispatcher(map[string]Sender{"telegram": &recordingSender{}})
		if !d.Supports("telegram") {
			t.Error("Supports(telegram) = false, want true")
		}
		if d.Supports("discord") {
			t.Error("Supports(discord) = true, want false")
		}
	})
}
