package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/prettyleaf/remnanode-manager/internal/settings"
)

// Message is rendered into an event template. Text values are HTML-escaped,
// HTML values are inserted as is (they must be built with Esc).
type Message struct {
	Type string
	Text map[string]string
	HTML map[string]string
}

type job struct {
	typ  string
	cfg  settings.Telegram
	text string
}

type Telegram struct {
	settings func() settings.Telegram
	log      *slog.Logger
	queue    chan job
	onFail   func(typ string, err error)
}

func NewTelegram(get func() settings.Telegram, log *slog.Logger) *Telegram {
	return &Telegram{settings: get, log: log, queue: make(chan job, 200)}
}

func Esc(s string) string { return html.EscapeString(s) }

// OnFailure registers a callback for event messages that could not be
// delivered. It must be set before Run is started.
func (t *Telegram) OnFailure(fn func(typ string, err error)) { t.onFail = fn }

// Run delivers queued messages until ctx is cancelled.
func (t *Telegram) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case j := <-t.queue:
			var errs []error
			for _, chat := range j.cfg.ChatIDs {
				if err := send(ctx, j.cfg, chat, j.text); err != nil {
					t.log.Warn("telegram delivery failed", "type", j.typ, "chat", chat, "err", err)
					errs = append(errs, fmt.Errorf("chat %s: %w", chat, err))
				}
				select {
				case <-ctx.Done():
					return
				case <-time.After(100 * time.Millisecond):
				}
			}
			if len(errs) > 0 && t.onFail != nil {
				t.onFail(j.typ, errors.Join(errs...))
			}
		}
	}
}

// Notify renders the event template and queues it if the event is enabled.
func (t *Telegram) Notify(m Message) {
	cfg := t.settings()
	if !cfg.Enabled || cfg.BotToken == "" || len(cfg.ChatIDs) == 0 {
		t.log.Debug("telegram notification skipped: notifications are off or not configured", "type", m.Type)
		return
	}
	ev, ok := cfg.Events[m.Type]
	if !ok || !ev.Enabled {
		t.log.Debug("telegram notification skipped: event is off", "type", m.Type)
		return
	}
	text := Render(ev.Template, m)
	if strings.TrimSpace(text) == "" {
		return
	}
	select {
	case t.queue <- job{typ: m.Type, cfg: cfg, text: text}:
	default:
		t.log.Warn("telegram queue is full, dropping message", "type", m.Type)
	}
}

// SendTest sends text synchronously using cfg (which may be unsaved).
func SendTest(ctx context.Context, cfg settings.Telegram, text string) error {
	if cfg.BotToken == "" {
		return errors.New("bot token is empty")
	}
	if len(cfg.ChatIDs) == 0 {
		return errors.New("no chat IDs configured")
	}
	var errs []error
	for _, chat := range cfg.ChatIDs {
		if err := send(ctx, cfg, chat, text); err != nil {
			errs = append(errs, fmt.Errorf("chat %s: %w", chat, err))
		}
	}
	return errors.Join(errs...)
}

var placeholder = regexp.MustCompile(`\{([a-z_]+)\}`)

func Render(tpl string, m Message) string {
	return placeholder.ReplaceAllStringFunc(tpl, func(match string) string {
		key := match[1 : len(match)-1]
		if v, ok := m.HTML[key]; ok {
			return v
		}
		if v, ok := m.Text[key]; ok {
			return Esc(v)
		}
		return match
	})
}

var tagRe = regexp.MustCompile(`<[^>]*>`)

type apiResponse struct {
	OK          bool   `json:"ok"`
	Description string `json:"description"`
	Parameters  struct {
		RetryAfter int `json:"retry_after"`
	} `json:"parameters"`
}

func send(ctx context.Context, cfg settings.Telegram, chat, text string) error {
	text = limit(text, 4000)
	err := sendOnce(ctx, cfg, chat, text, "HTML")
	if err != nil && strings.Contains(err.Error(), "can't parse entities") {
		// A broken custom template should still deliver the alert.
		plain := html.UnescapeString(tagRe.ReplaceAllString(text, ""))
		return sendOnce(ctx, cfg, chat, plain, "")
	}
	return err
}

func sendOnce(ctx context.Context, cfg settings.Telegram, chat, text, parseMode string) error {
	body := map[string]any{
		"chat_id":                  chat,
		"text":                     text,
		"disable_web_page_preview": true,
		"disable_notification":     cfg.Silent,
	}
	if parseMode != "" {
		body["parse_mode"] = parseMode
	}
	if cfg.ThreadID != "" {
		if id, err := strconv.ParseInt(cfg.ThreadID, 10, 64); err == nil {
			body["message_thread_id"] = id
		}
	}
	payload, _ := json.Marshal(body)

	client, err := httpClient(cfg)
	if err != nil {
		return err
	}
	base := cfg.APIURL
	if base == "" {
		base = "https://api.telegram.org"
	}
	endpoint := strings.TrimRight(base, "/") + "/bot" + cfg.BotToken + "/sendMessage"

	for attempt := 0; attempt < 3; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			// Never leak the bot token through the URL in error messages.
			return errors.New(strings.ReplaceAll(err.Error(), cfg.BotToken, "***"))
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()

		var r apiResponse
		_ = json.Unmarshal(raw, &r)
		if resp.StatusCode == http.StatusTooManyRequests && r.Parameters.RetryAfter > 0 && r.Parameters.RetryAfter <= 60 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(r.Parameters.RetryAfter) * time.Second):
			}
			continue
		}
		if resp.StatusCode != http.StatusOK || !r.OK {
			if r.Description == "" {
				r.Description = resp.Status
			}
			return errors.New(r.Description)
		}
		return nil
	}
	return errors.New("telegram rate limit")
}

func httpClient(cfg settings.Telegram) (*http.Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if cfg.Proxy != "" {
		u, err := url.Parse(cfg.Proxy)
		if err != nil {
			return nil, fmt.Errorf("invalid proxy: %w", err)
		}
		transport.Proxy = http.ProxyURL(u)
	}
	return &http.Client{Timeout: 15 * time.Second, Transport: transport}, nil
}

func limit(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n]) + "…"
}
