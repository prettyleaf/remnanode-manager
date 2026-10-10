package server

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/prettyleaf/remnanode-manager/internal/buildinfo"
	"github.com/prettyleaf/remnanode-manager/internal/checker"
	"github.com/prettyleaf/remnanode-manager/internal/config"
	"github.com/prettyleaf/remnanode-manager/internal/notify"
	"github.com/prettyleaf/remnanode-manager/internal/settings"
	"github.com/prettyleaf/remnanode-manager/internal/store"
)

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	tg := notify.NewTelegram(func() settings.Telegram { return st.Settings().Telegram }, log)
	ch := checker.New(st, tg, log, "test")
	ui := fstest.MapFS{"index.html": {Data: []byte("<html>ui</html>")}}
	srv := httptest.NewServer(New(config.Config{CookieSecure: "false"}, st, ch, buildinfo.NewUpdater(false, log), log, ui).Handler())
	t.Cleanup(srv.Close)
	return srv
}

func newClient(t *testing.T) *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar}
}

func postJSON(t *testing.T, c *http.Client, url, body string) *http.Response {
	t.Helper()
	resp, err := c.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp
}

func TestAuthFlow(t *testing.T) {
	srv := newTestServer(t)
	c := newClient(t)

	if resp, _ := c.Get(srv.URL + "/api/status"); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status without session: %d", resp.StatusCode)
	}
	if resp := postJSON(t, c, srv.URL+"/api/auth/setup", `{"username":"admin","password":"short"}`); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("weak password accepted: %d", resp.StatusCode)
	}
	if resp := postJSON(t, c, srv.URL+"/api/auth/setup", `{"username":"admin","password":"password123"}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("setup failed: %d", resp.StatusCode)
	}
	if resp, _ := c.Get(srv.URL + "/api/status"); resp.StatusCode != http.StatusOK {
		t.Fatalf("status after setup: %d", resp.StatusCode)
	}

	// A second setup must be refused even for a new client.
	other := newClient(t)
	if resp := postJSON(t, other, srv.URL+"/api/auth/setup", `{"username":"evil","password":"password123"}`); resp.StatusCode != http.StatusConflict {
		t.Fatalf("second setup allowed: %d", resp.StatusCode)
	}
	if resp := postJSON(t, other, srv.URL+"/api/auth/login", `{"username":"admin","password":"wrong-password"}`); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong password accepted: %d", resp.StatusCode)
	}
	if resp := postJSON(t, other, srv.URL+"/api/auth/login", `{"username":"admin","password":"password123"}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("login failed: %d", resp.StatusCode)
	}

	// Form-encoded (cross-site capable) mutations are rejected.
	resp, err := other.Post(srv.URL+"/api/check", "application/x-www-form-urlencoded", strings.NewReader("a=b"))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("form post accepted: %d", resp.StatusCode)
	}

	if resp := postJSON(t, other, srv.URL+"/api/auth/logout", `{}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("logout failed: %d", resp.StatusCode)
	}
	if resp, _ := other.Get(srv.URL + "/api/status"); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("session still valid after logout: %d", resp.StatusCode)
	}
}

func TestSettingsSecretsAreMasked(t *testing.T) {
	srv := newTestServer(t)
	c := newClient(t)
	postJSON(t, c, srv.URL+"/api/auth/setup", `{"username":"admin","password":"password123"}`)

	body := `{"panel":{"url":"http://remnawave:3000","token":"secret-token","headers":{}},
		"checker":{"enabled":true,"intervalSeconds":30,"downMinutes":5,"recoveryMinutes":2,"policy":"all","autoEnable":true},
		"mihomo":{"templateTypes":["MIHOMO"]},"telegram":{"apiUrl":"https://api.telegram.org","botToken":"bot-secret"},
		"general":{"eventRetentionDays":30}}`
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/api/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("save failed: %d %s", resp.StatusCode, raw)
	}
	if strings.Contains(string(raw), "secret-token") || strings.Contains(string(raw), "bot-secret") {
		t.Fatalf("secrets leaked: %s", raw)
	}
	if !strings.Contains(string(raw), `"panelToken":true`) {
		t.Fatalf("secret flag missing: %s", raw)
	}
}

func TestSPAFallback(t *testing.T) {
	srv := newTestServer(t)
	resp, err := http.Get(srv.URL + "/settings")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(raw), "ui") {
		t.Fatalf("SPA fallback failed: %d %s", resp.StatusCode, raw)
	}
	if resp, _ := http.Get(srv.URL + "/assets/missing.js"); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing asset: %d", resp.StatusCode)
	}
}
