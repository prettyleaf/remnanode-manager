package notify

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prettyleaf/remnanode-manager/internal/settings"
)

func TestRenderEscapesText(t *testing.T) {
	got := Render("<b>{node}</b> {hosts} {unknown}", Message{
		Text: map[string]string{"node": "<script>&"},
		HTML: map[string]string{"hosts": "<i>ok</i>"},
	})
	want := "<b>&lt;script&gt;&amp;</b> <i>ok</i> {unknown}"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestSendFallsBackToPlainText(t *testing.T) {
	var calls []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/botTOKEN/sendMessage" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		calls = append(calls, body)
		if body["parse_mode"] == "HTML" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"ok":false,"description":"Bad Request: can't parse entities"}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	cfg := settings.Telegram{BotToken: "TOKEN", ChatIDs: []string{"-100"}, ThreadID: "7", APIURL: srv.URL}
	if err := SendTest(context.Background(), cfg, "<b>broken"); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 || calls[1]["text"] != "broken" || calls[1]["message_thread_id"] != float64(7) {
		t.Fatalf("unexpected calls: %v", calls)
	}
}

func TestNotifyRespectsSwitches(t *testing.T) {
	cfg := settings.Default().Telegram
	cfg.BotToken = "TOKEN"
	cfg.ChatIDs = []string{"-100"}
	tg := NewTelegram(func() settings.Telegram { return cfg }, slog.New(slog.NewTextHandler(io.Discard, nil)))

	tg.Notify(Message{Type: settings.EventNodeDown})
	if len(tg.queue) != 0 {
		t.Fatal("message queued while notifications are off")
	}
	cfg.Enabled = true
	tg.Notify(Message{Type: settings.EventMihomoUpdated})
	if len(tg.queue) != 0 {
		t.Fatal("message queued for a disabled event")
	}
	tg.Notify(Message{Type: settings.EventNodeDown, Text: map[string]string{"node": "de-1"}})
	if len(tg.queue) != 1 {
		t.Fatal("message was not queued")
	}
}

func TestRunReportsDeliveryFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"ok":false,"description":"Forbidden: bot was kicked from the group chat"}`))
	}))
	defer srv.Close()

	cfg := settings.Default().Telegram
	cfg.Enabled = true
	cfg.BotToken = "TOKEN"
	cfg.ChatIDs = []string{"-100"}
	cfg.APIURL = srv.URL
	tg := NewTelegram(func() settings.Telegram { return cfg }, slog.New(slog.NewTextHandler(io.Discard, nil)))
	type failure struct {
		typ string
		err error
	}
	failed := make(chan failure, 1)
	tg.OnFailure(func(typ string, err error) { failed <- failure{typ, err} })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go tg.Run(ctx)
	tg.Notify(Message{Type: settings.EventNodeDown})

	select {
	case f := <-failed:
		if f.typ != settings.EventNodeDown || !strings.Contains(f.err.Error(), "chat -100: Forbidden: bot was kicked") {
			t.Fatalf("unexpected failure: %s %v", f.typ, f.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("delivery failure was not reported")
	}
}
