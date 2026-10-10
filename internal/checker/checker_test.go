package checker

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prettyleaf/remnanode-manager/internal/notify"
	"github.com/prettyleaf/remnanode-manager/internal/settings"
	"github.com/prettyleaf/remnanode-manager/internal/store"
)

const (
	nodeA = "11111111-1111-1111-1111-111111111111"
	nodeB = "22222222-2222-2222-2222-222222222222"
	hostA = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	hostB = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	hostC = "cccccccc-cccc-cccc-cccc-cccccccccccc"
	tplID = "dddddddd-dddd-dddd-dddd-dddddddddddd"
)

const tplYAML = `proxy-groups:
  - name: Fixed
    type: select
    remnawave:
      include-proxies: false
    proxies:
      - 🇩🇪 DE
      - 🇳🇱 NL

rules:
  - DOMAIN-SUFFIX,example.com,🇩🇪 DE
  - MATCH,Fixed
`

// fakePanel emulates the subset of the Remnawave API used by the checker.
// legacy switches host payloads to the 2.7.x shape ("tag" instead of "tags").
type fakePanel struct {
	mu        sync.Mutex
	nodes     map[string]bool // uuid -> connected
	hosts     map[string]map[string]any
	template  string
	legacy    bool
	bulkCalls []string
}

func newFakePanel(legacy bool) *fakePanel {
	p := &fakePanel{
		nodes:    map[string]bool{nodeA: true, nodeB: true},
		template: tplYAML,
		legacy:   legacy,
		hosts: map[string]map[string]any{
			hostA: {"uuid": hostA, "remark": "🇩🇪 DE", "isDisabled": false, "isHidden": true, "nodes": []string{nodeA}},
			hostB: {"uuid": hostB, "remark": "🇳🇱 NL", "isDisabled": false, "isHidden": false, "nodes": []string{nodeB}},
			// Admin-disabled host bound to the same node must never be touched.
			hostC: {"uuid": hostC, "remark": "Manual", "isDisabled": true, "isHidden": false, "nodes": []string{nodeA}},
		},
	}
	for _, h := range p.hosts {
		if legacy {
			h["tag"] = "LEGACY"
		} else {
			h["tags"] = []string{"NEW"}
		}
	}
	return p
}

func (p *fakePanel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer token" || r.Header.Get("X-Forwarded-Proto") != "https" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	reply := func(v any) {
		_ = json.NewEncoder(w).Encode(map[string]any{"response": v})
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/system/metadata":
		reply(map[string]any{"version": "2.7.4"})
	case r.Method == http.MethodGet && r.URL.Path == "/api/nodes":
		out := []map[string]any{}
		for id, ok := range p.nodes {
			out = append(out, map[string]any{"uuid": id, "name": "node-" + id[:1], "address": "1.1.1.1",
				"isConnected": ok, "isDisabled": false, "isConnecting": false, "lastStatusMessage": "connection refused"})
		}
		reply(out)
	case r.Method == http.MethodGet && r.URL.Path == "/api/hosts":
		out := []map[string]any{}
		for _, id := range []string{hostA, hostB, hostC} {
			out = append(out, p.hosts[id])
		}
		reply(out)
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/hosts/bulk/"):
		var body struct {
			UUIDs []string `json:"uuids"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		disable := strings.HasSuffix(r.URL.Path, "/disable")
		for _, id := range body.UUIDs {
			p.hosts[id]["isDisabled"] = disable
		}
		p.bulkCalls = append(p.bulkCalls, r.URL.Path)
		reply(map[string]any{"ok": true})
	case r.Method == http.MethodGet && r.URL.Path == "/api/subscription-templates":
		reply(map[string]any{"total": 1, "templates": []map[string]any{{"uuid": tplID, "name": "Default", "templateType": "MIHOMO"}}})
	case r.Method == http.MethodGet && r.URL.Path == "/api/subscription-templates/"+tplID:
		reply(map[string]any{"uuid": tplID, "name": "Default", "templateType": "MIHOMO",
			"encodedTemplateYaml": base64.StdEncoding.EncodeToString([]byte(p.template))})
	case r.Method == http.MethodPatch && r.URL.Path == "/api/subscription-templates":
		var body struct {
			EncodedTemplateYAML string `json:"encodedTemplateYaml"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		raw, _ := base64.StdEncoding.DecodeString(body.EncodedTemplateYAML)
		p.template = string(raw)
		reply(map[string]any{"uuid": tplID})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (p *fakePanel) hostDisabled(id string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.hosts[id]["isDisabled"].(bool)
}

func (p *fakePanel) setNode(id string, up bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.nodes[id] = up
}

func (p *fakePanel) tpl() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.template
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

func setup(t *testing.T, legacy bool, mutate func(*settings.Settings)) (*Checker, *fakePanel, *clock, *store.Store) {
	t.Helper()
	panel := newFakePanel(legacy)
	srv := httptest.NewServer(panel)
	t.Cleanup(srv.Close)

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	cfg := settings.Default()
	cfg.Panel.URL = srv.URL + "/api/"
	cfg.Panel.Token = "token"
	cfg.Checker.DownMinutes = 5
	cfg.Checker.RecoveryMinutes = 2
	cfg.Mihomo.Enabled = true
	if mutate != nil {
		mutate(&cfg)
	}
	if err := st.SaveSettings(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	tg := notify.NewTelegram(func() settings.Telegram { return st.Settings().Telegram }, log)
	c := New(st, tg, log, "test")
	clk := &clock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	c.now = clk.now
	return c, panel, clk, st
}

func TestDisableAndRecover(t *testing.T) {
	for _, legacy := range []bool{true, false} {
		name := "3.x"
		if legacy {
			name = "2.7"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			c, panel, clk, st := setup(t, legacy, nil)

			c.runCycle(ctx)
			if snap := c.Snapshot(); !snap.PanelOK || snap.Stats.NodesUp != 2 {
				t.Fatalf("unexpected snapshot: %+v", snap)
			}

			panel.setNode(nodeA, false)
			c.runCycle(ctx)
			clk.advance(4 * time.Minute)
			c.runCycle(ctx)
			if panel.hostDisabled(hostA) {
				t.Fatal("host disabled before the grace period ended")
			}
			if c.Snapshot().Stats.HostsAtRisk != 1 {
				t.Fatalf("expected one host at risk: %+v", c.Snapshot().Stats)
			}

			clk.advance(time.Minute + time.Second)
			c.runCycle(ctx)
			if !panel.hostDisabled(hostA) {
				t.Fatal("hidden host bound to the dead node was not disabled")
			}
			if panel.hostDisabled(hostB) {
				t.Fatal("host on a healthy node was disabled")
			}
			if tpl := panel.tpl(); strings.Contains(tpl, "- 🇩🇪 DE\n") || strings.Contains(tpl, "example.com") {
				t.Fatalf("template still references the host:\n%s", tpl)
			}
			if !strings.Contains(panel.tpl(), "      - 🇳🇱 NL\n\nrules:") {
				t.Fatalf("template formatting not preserved:\n%s", panel.tpl())
			}

			// Recovery needs the node to stay up for RecoveryMinutes.
			panel.setNode(nodeA, true)
			c.runCycle(ctx)
			if !panel.hostDisabled(hostA) {
				t.Fatal("host re-enabled before the recovery period")
			}
			clk.advance(2*time.Minute + time.Second)
			c.runCycle(ctx)
			if panel.hostDisabled(hostA) {
				t.Fatal("host not re-enabled after recovery")
			}
			if !panel.hostDisabled(hostC) {
				t.Fatal("admin-disabled host was enabled")
			}
			if panel.tpl() != tplYAML {
				t.Fatalf("template not restored exactly:\n%s", panel.tpl())
			}
			managed, _ := st.ManagedHosts(ctx)
			if len(managed) != 0 {
				t.Fatalf("managed hosts left: %v", managed)
			}
		})
	}
}

func TestManualEnableIsRespected(t *testing.T) {
	ctx := context.Background()
	c, panel, clk, st := setup(t, false, func(s *settings.Settings) { s.Mihomo.Enabled = false })

	panel.setNode(nodeA, false)
	c.runCycle(ctx)
	clk.advance(6 * time.Minute)
	c.runCycle(ctx)
	if !panel.hostDisabled(hostA) {
		t.Fatal("host was not disabled")
	}

	// Admin turns the host back on while the node is still down.
	panel.mu.Lock()
	panel.hosts[hostA]["isDisabled"] = false
	panel.mu.Unlock()
	c.runCycle(ctx)
	clk.advance(10 * time.Minute)
	c.runCycle(ctx)
	if panel.hostDisabled(hostA) {
		t.Fatal("manually enabled host was disabled again")
	}
	m, err := st.ManagedHost(ctx, hostA)
	if err != nil || m.State != store.HostStateOverridden {
		t.Fatalf("expected overridden state, got %+v %v", m, err)
	}

	panel.setNode(nodeA, true)
	c.runCycle(ctx)
	clk.advance(3 * time.Minute)
	c.runCycle(ctx)
	if _, err := st.ManagedHost(ctx, hostA); err == nil {
		t.Fatal("tracking not released after recovery")
	}
}

func TestDryRunDoesNotTouchPanel(t *testing.T) {
	ctx := context.Background()
	c, panel, clk, st := setup(t, false, func(s *settings.Settings) { s.Checker.DryRun = true })

	panel.setNode(nodeA, false)
	c.runCycle(ctx)
	clk.advance(6 * time.Minute)
	c.runCycle(ctx)
	if panel.hostDisabled(hostA) || len(panel.bulkCalls) != 0 || panel.tpl() != tplYAML {
		t.Fatal("dry run modified the panel")
	}
	m, err := st.ManagedHost(ctx, hostA)
	if err != nil || m.State != store.HostStateDryRun {
		t.Fatalf("expected dry-run record, got %+v %v", m, err)
	}
}

func TestMassDownGuard(t *testing.T) {
	ctx := context.Background()
	c, panel, clk, _ := setup(t, false, func(s *settings.Settings) { s.Checker.MassDownGuardPercent = 100 })

	panel.setNode(nodeA, false)
	panel.setNode(nodeB, false)
	c.runCycle(ctx)
	clk.advance(6 * time.Minute)
	c.runCycle(ctx)
	if panel.hostDisabled(hostA) || panel.hostDisabled(hostB) {
		t.Fatal("guard did not prevent disabling when every node is down")
	}
}

func TestPolicyAll(t *testing.T) {
	ctx := context.Background()
	c, panel, clk, _ := setup(t, false, func(s *settings.Settings) { s.Mihomo.Enabled = false })
	panel.mu.Lock()
	panel.hosts[hostA]["nodes"] = []string{nodeA, nodeB}
	panel.mu.Unlock()

	panel.setNode(nodeA, false)
	c.runCycle(ctx)
	clk.advance(10 * time.Minute)
	c.runCycle(ctx)
	if panel.hostDisabled(hostA) {
		t.Fatal("policy all: host disabled while another bound node is up")
	}

	panel.setNode(nodeB, false)
	c.runCycle(ctx)
	clk.advance(6 * time.Minute)
	c.runCycle(ctx)
	if !panel.hostDisabled(hostA) {
		t.Fatal("policy all: host not disabled when every bound node is down")
	}
}
