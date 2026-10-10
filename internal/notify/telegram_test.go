package notify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

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
