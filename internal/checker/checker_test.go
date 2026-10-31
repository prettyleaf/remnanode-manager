package checker

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
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
	traffic   map[string]*nodeTraffic
	online    map[string]int     // users online
	uptime    map[string]float64 // Xray uptime, seconds
}

type nodeTraffic struct {
	tracking    bool
	used, limit float64
}

func newFakePanel(legacy bool) *fakePanel {
	p := &fakePanel{
		nodes:    map[string]bool{nodeA: true, nodeB: true},
		traffic:  map[string]*nodeTraffic{},
		online:   map[string]int{nodeA: 100, nodeB: 100},
		uptime:   map[string]float64{nodeA: 86400, nodeB: 86400},
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
			n := map[string]any{"uuid": id, "name": "node-" + id[:1], "address": "1.1.1.1",
				"isConnected": ok, "isDisabled": false, "isConnecting": false, "lastStatusMessage": "connection refused",
				"isTrafficTrackingActive": false, "trafficLimitBytes": nil, "trafficUsedBytes": 0, "trafficResetDay": nil,
				"usersOnline": p.online[id], "xrayUptime": p.uptime[id]}
			if tr := p.traffic[id]; tr != nil {
				n["isTrafficTrackingActive"], n["trafficUsedBytes"], n["trafficLimitBytes"], n["trafficResetDay"] = tr.tracking, tr.used, tr.limit, 1
			}
			out = append(out, n)
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

func (p *fakePanel) setTraffic(id string, tracking bool, used, limit float64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.traffic[id] = &nodeTraffic{tracking: tracking, used: used, limit: limit}
}

func (p *fakePanel) setOnline(id string, users int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.online[id] = users
}

func (p *fakePanel) setUptime(id string, seconds float64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.uptime[id] = seconds
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

func TestDryRunNotifiesTelegram(t *testing.T) {
	var mu sync.Mutex
	var texts []string
	tgSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Text string `json:"text"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		texts = append(texts, body.Text)
		mu.Unlock()
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer tgSrv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c, panel, clk, _ := setup(t, false, func(s *settings.Settings) {
		s.Checker.DryRun = true
		s.Mihomo.Enabled = false
		s.Telegram.Enabled = true
		s.Telegram.BotToken = "TOKEN"
		s.Telegram.ChatIDs = []string{"1"}
		s.Telegram.APIURL = tgSrv.URL
	})
	go c.tg.Run(ctx)

	panel.setNode(nodeA, false)
	c.runCycle(ctx)
	clk.advance(6 * time.Minute)
	c.runCycle(ctx)
	panel.setNode(nodeA, true)
	c.runCycle(ctx)
	clk.advance(3 * time.Minute)
	c.runCycle(ctx)

	want := []string{"Нода недоступна", "Хосты отключены</b> <i>(тестовый режим)</i>", "Нода снова в сети", "Хосты включены обратно</b> <i>(тестовый режим)</i>"}
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		got := slices.Clone(texts)
		mu.Unlock()
		if len(got) >= len(want) {
			for i, w := range want {
				if !strings.Contains(got[i], w) {
					t.Fatalf("message %d: %q does not contain %q", i, got[i], w)
				}
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("got %d messages, want %d: %q", len(got), len(want), got)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestTrafficLimit(t *testing.T) {
	ctx := context.Background()
	c, panel, clk, st := setup(t, false, func(s *settings.Settings) {
		s.Traffic.Enabled = true
		s.Traffic.DisablePercent = 90
		// One of two nodes down trips the guard; traffic must not be held by it.
		s.Checker.MassDownGuardPercent = 50
	})
	var tb float64 = 1 << 40

	panel.setTraffic(nodeA, true, 0.89*tb, tb)
	panel.setNode(nodeB, false)
	c.runCycle(ctx)
	clk.advance(6 * time.Minute)
	c.runCycle(ctx)
	if panel.hostDisabled(hostA) {
		t.Fatal("host disabled below the traffic threshold")
	}
	if panel.hostDisabled(hostB) {
		t.Fatal("guard did not hold the host of the down node")
	}

	panel.setTraffic(nodeA, true, 0.91*tb, tb)
	c.runCycle(ctx)
	if !panel.hostDisabled(hostA) {
		t.Fatal("host not disabled after its node reached the traffic threshold")
	}
	if panel.hostDisabled(hostB) || !panel.hostDisabled(hostC) {
		t.Fatal("unrelated hosts changed")
	}
	if strings.Contains(panel.tpl(), "- 🇩🇪 DE\n") {
		t.Fatalf("template still references the host:\n%s", panel.tpl())
	}
	m, err := st.ManagedHost(ctx, hostA)
	if err != nil || m.Reason != store.ReasonTraffic {
		t.Fatalf("expected traffic record, got %+v %v", m, err)
	}
	snap := c.Snapshot()
	if snap.Stats.NodesOverLimit != 1 {
		t.Fatalf("expected one node over the limit: %+v", snap.Stats)
	}
	for _, n := range snap.Nodes {
		if n.UUID == nodeA && (!n.Traffic.OverLimit || n.Traffic.LimitBytes != int64(tb) || n.Traffic.UsedBytes != int64(0.91*tb)) {
			t.Fatalf("unexpected traffic view: %+v", n.Traffic)
		}
	}

	// The panel resets usage on the reset day: the node has been up all
	// along, so the host comes back on the next check.
	panel.setTraffic(nodeA, true, 0, tb)
	c.runCycle(ctx)
	if panel.hostDisabled(hostA) {
		t.Fatal("host not re-enabled after the traffic reset")
	}
	if panel.tpl() != tplYAML {
		t.Fatalf("template not restored exactly:\n%s", panel.tpl())
	}
	events, _ := st.Events(ctx, store.EventFilter{Type: "host_enabled"})
	if len(events) != 1 || events[0].Meta["reason"] != "traffic below limit" {
		t.Fatalf("unexpected enable events: %+v", events)
	}
}

func TestTrafficLimitIgnored(t *testing.T) {
	ctx := context.Background()
	c, panel, _, _ := setup(t, false, func(s *settings.Settings) { s.Mihomo.Enabled = false })

	// Feature off: a node past its limit keeps its hosts.
	panel.setTraffic(nodeA, true, 2, 1)
	c.runCycle(ctx)
	if panel.hostDisabled(hostA) {
		t.Fatal("host disabled while traffic limits are off")
	}

	cfg := c.store.Settings()
	cfg.Traffic.Enabled = true
	if err := c.store.SaveSettings(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	// Tracking off in the panel: the limit does not apply.
	panel.setTraffic(nodeA, false, 2, 1)
	c.runCycle(ctx)
	if panel.hostDisabled(hostA) {
		t.Fatal("host disabled although the panel does not track the node's traffic")
	}

	panel.setTraffic(nodeA, true, 2, 1)
	c.runCycle(ctx)
	if !panel.hostDisabled(hostA) {
		t.Fatal("host not disabled once tracking was turned on")
	}
}

func TestReasonFollowsNodes(t *testing.T) {
	ctx := context.Background()
	c, panel, clk, st := setup(t, false, func(s *settings.Settings) {
		s.Mihomo.Enabled = false
		s.Traffic.Enabled = true
	})

	panel.setNode(nodeA, false)
	c.runCycle(ctx)
	clk.advance(6 * time.Minute)
	c.runCycle(ctx)
	if m, err := st.ManagedHost(ctx, hostA); err != nil || m.Reason != store.ReasonNodesDown {
		t.Fatalf("expected nodes-down record, got %+v %v", m, err)
	}

	// The node is back but has no traffic left: the host stays off.
	panel.setNode(nodeA, true)
	panel.setTraffic(nodeA, true, 100, 100)
	c.runCycle(ctx)
	clk.advance(3 * time.Minute)
	c.runCycle(ctx)
	if !panel.hostDisabled(hostA) {
		t.Fatal("host re-enabled while its node is out of traffic")
	}
	if m, err := st.ManagedHost(ctx, hostA); err != nil || m.Reason != store.ReasonTraffic {
		t.Fatalf("expected the reason to switch to traffic, got %+v %v", m, err)
	}
}

func TestFormatBytes(t *testing.T) {
	for in, want := range map[int64]string{0: "0 Б", 1536: "1,5 КБ", 1 << 40: "1 ТБ", 960 << 30: "960 ГБ"} {
		if got := FormatBytes(in); got != want {
			t.Errorf("FormatBytes(%d) = %q, want %q", in, got, want)
		}
	}
}
