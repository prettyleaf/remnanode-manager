// Package checker watches Remnawave nodes and disables hosts bound to nodes
// that stay offline for too long, re-enabling them once the nodes recover.
package checker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prettyleaf/remnanode-manager/internal/notify"
	"github.com/prettyleaf/remnanode-manager/internal/remnawave"
	"github.com/prettyleaf/remnanode-manager/internal/settings"
	"github.com/prettyleaf/remnanode-manager/internal/store"
)

const (
	StatusUp       = "up"
	StatusDown     = "down"
	StatusDisabled = "disabled"

	// A node must stay down this long before a "node down" alert is sent,
	// which keeps short reconnects quiet.
	nodeDownDebounce = time.Minute
	// Consecutive failed polls before the panel is reported unreachable.
	panelFailThreshold = 3
	metadataRefresh    = 10 * time.Minute
)

type Checker struct {
	store   *store.Store
	tg      *notify.Telegram
	log     *slog.Logger
	version string

	trigger chan struct{}
	opMu    sync.Mutex // serializes cycles and manual actions
	now     func() time.Time

	mu       sync.RWMutex
	snapshot Snapshot

	panelFails     int
	panelNotified  bool
	guardNotified  bool
	panelVersion   string
	versionFetched time.Time
	lastPrune      time.Time
	nextCheck      time.Time
}

func New(st *store.Store, tg *notify.Telegram, log *slog.Logger, version string) *Checker {
	return &Checker{
		store:   st,
		tg:      tg,
		log:     log,
		version: version,
		trigger: make(chan struct{}, 1),
		now:     time.Now,
	}
}

// Trigger schedules an immediate check.
func (c *Checker) Trigger() {
	select {
	case c.trigger <- struct{}{}:
	default:
	}
}

func (c *Checker) Run(ctx context.Context) {
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		case <-c.trigger:
		}
		c.runCycle(ctx)
		interval := time.Duration(c.store.Settings().Checker.IntervalSeconds) * time.Second
		c.mu.Lock()
		c.nextCheck = time.Now().Add(interval)
		c.snapshot.NextCheckAt = c.nextCheck
		c.mu.Unlock()
		timer.Reset(interval)
	}
}

func (c *Checker) client(cfg settings.Settings) (*remnawave.Client, error) {
	return remnawave.New(remnawave.Config{
		BaseURL:     cfg.Panel.URL,
		Token:       cfg.Panel.Token,
		Headers:     cfg.Panel.Headers,
		InsecureTLS: cfg.Panel.InsecureTLS,
		UserAgent:   "remnanode-manager/" + c.version,
	})
}

func (c *Checker) runCycle(ctx context.Context) {
	c.opMu.Lock()
	defer c.opMu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	cfg := c.store.Settings()
	if time.Since(c.lastPrune) > 6*time.Hour {
		c.lastPrune = time.Now()
		if err := c.store.Prune(ctx, time.Duration(cfg.General.EventRetentionDays)*24*time.Hour); err != nil {
			c.log.Warn("prune failed", "err", err)
		}
	}

	if !cfg.PanelConfigured() {
		c.setSnapshotError("not_configured", "")
		return
	}
	client, err := c.client(cfg)
	if err != nil {
		c.setSnapshotError("config_error", err.Error())
		return
	}

	nodes, err := client.Nodes(ctx)
	var hosts []remnawave.Host
	if err == nil {
		hosts, err = client.Hosts(ctx)
	}
	if err != nil {
		c.panelFailure(ctx, err)
		return
	}
	c.panelSuccess(ctx, client)

	if err := c.evaluate(ctx, client, cfg, nodes, hosts); err != nil {
		c.log.Error("check cycle failed", "err", err)
		c.event(ctx, store.Event{Level: "error", Type: "error", Message: "Check cycle failed: " + err.Error(),
			Meta: map[string]any{"error": err.Error()}})
	}
}

func (c *Checker) evaluate(ctx context.Context, client *remnawave.Client, cfg settings.Settings, nodes []remnawave.Node, hosts []remnawave.Host) error {
	now := c.now()
	states, err := c.store.NodeStates(ctx)
	if err != nil {
		return err
	}
	excludedNodes := toSet(cfg.Checker.ExcludedNodes)
	excludedHosts := toSet(cfg.Checker.ExcludedHosts)

	boundHosts := map[string][]remnawave.Host{}
	for _, h := range hosts {
		for _, n := range h.Nodes {
			boundHosts[n] = append(boundHosts[n], h)
		}
	}

	// 1. Update node states.
	seen := map[string]bool{}
	for _, n := range nodes {
		seen[n.UUID] = true
		st := states[n.UUID]
		if st == nil {
			st = &store.NodeState{UUID: n.UUID}
			states[n.UUID] = st
		}
		status := classify(n, cfg.Checker.TreatDisabledNodeAsDown)
		prev := st.Status
		st.Name = n.Name
		if n.LastStatusMessage != nil {
			st.LastMessage = *n.LastStatusMessage
		}

		switch status {
		case StatusDown:
			if prev != StatusDown || st.DownSince == nil {
				st.DownSince = ptr(now)
				st.UpSince = nil
				st.Notified = false
			}
			if !st.Notified && now.Sub(*st.DownSince) >= nodeDownDebounce && !excludedNodes[n.UUID] {
				st.Notified = true
				c.nodeDown(ctx, cfg, n, st, boundHosts[n.UUID])
			}
		case StatusUp:
			if prev != StatusUp || st.UpSince == nil {
				if prev == StatusDown && st.Notified && st.DownSince != nil {
					c.nodeUp(ctx, n, now.Sub(*st.DownSince))
				}
				st.UpSince = ptr(now)
				st.DownSince = nil
				st.Notified = false
			}
		default:
			st.DownSince = nil
			st.UpSince = nil
			st.Notified = false
		}
		st.Status = status
	}
	for id := range states {
		if !seen[id] {
			delete(states, id)
		}
	}
	if err := c.store.SaveNodeStates(ctx, states); err != nil {
		return err
	}

	usage := make(map[string]trafficUsage, len(nodes))
	for _, n := range nodes {
		usage[n.UUID] = trafficUsage{used: n.TrafficUsed(), limit: n.TrafficLimit()}
	}
	ev := evaluator{cfg: cfg.Checker, traffic: cfg.Traffic, states: states, usage: usage, excluded: excludedNodes, now: now}
	managed, err := c.store.ManagedHosts(ctx)
	if err != nil {
		return err
	}

	// 2. Find hosts that must be disabled.
	var toDisable []candidate
	for _, h := range hosts {
		if managed[h.UUID] != nil || h.IsDisabled || excludedHosts[h.UUID] {
			continue
		}
		bound := ev.bound(h)
		if len(bound) == 0 {
			continue
		}
		if ev.failed(bound) {
			toDisable = append(toDisable, candidate{host: h, nodes: bound, reason: ev.reason(bound)})
		}
	}

	down, total := ev.downRatio()
	guard := cfg.Checker.MassDownGuardPercent
	guardActive := guard > 0 && total > 0 && down*100 >= guard*total
	if !guardActive {
		c.guardNotified = false
	}

	if len(toDisable) > 0 && cfg.Checker.Enabled {
		if guardActive {
			// The guard is about outages; a traffic limit is reached by one
			// node at a time and is never a symptom of panel trouble.
			n := len(toDisable)
			toDisable = slices.DeleteFunc(toDisable, func(cand candidate) bool { return cand.reason == store.ReasonNodesDown })
			if len(toDisable) < n && !c.guardNotified {
				c.guardNotified = true
				percent := down * 100 / total
				c.event(ctx, store.Event{Level: "warn", Type: "guard_triggered",
					Message: fmt.Sprintf("Mass-down guard: %d of %d nodes are down (%d%%), hosts were not disabled", down, total, percent),
					Meta:    map[string]any{"down": down, "total": total, "percent": percent}})
				c.tg.Notify(notify.Message{Type: settings.EventGuardTriggered, Text: map[string]string{
					"down_nodes": fmt.Sprint(down), "total_nodes": fmt.Sprint(total), "percent": fmt.Sprint(percent),
				}})
			}
		}
		if len(toDisable) > 0 {
			disabled := c.disableHosts(ctx, client, cfg, toDisable, hosts, ev)
			setDisabled(hosts, disabled, true)
		}
	}

	// 3. Reconcile hosts managed by this service.
	hostByUUID := map[string]remnawave.Host{}
	for _, h := range hosts {
		hostByUUID[h.UUID] = h
	}
	managed, err = c.store.ManagedHosts(ctx)
	if err != nil {
		return err
	}
	var toEnable []*store.ManagedHost
	for _, m := range sortedManaged(managed) {
		h, ok := hostByUUID[m.UUID]
		if !ok {
			c.release(ctx, m, "host_deleted", "Host was deleted in the panel, tracking stopped")
			continue
		}
		bound := ev.bound(h)
		switch m.State {
		case store.HostStateDryRun:
			// Leaving dry run drops these records so real actions can start.
			if !cfg.Checker.DryRun {
				_ = c.store.DeleteManagedHost(ctx, m.UUID)
				continue
			}
			if len(bound) == 0 || ev.recovered(bound) {
				c.event(ctx, store.Event{Level: "info", Type: "dry_run_enable", HostUUID: m.UUID,
					Message: fmt.Sprintf("[dry run] Host %q would be enabled", m.Remark),
					Meta:    map[string]any{"host": m.Remark}})
				c.tg.Notify(notify.Message{Type: settings.EventHostsEnabled,
					HTML: map[string]string{"hosts": hostLine(m.Remark, nil), "dry_run": dryRunMark},
					Text: map[string]string{"count": "1"}})
				_ = c.store.DeleteManagedHost(ctx, m.UUID)
			}
		case store.HostStateOverridden:
			if len(bound) == 0 || ev.recovered(bound) {
				c.release(ctx, m, "host_released", "Nodes recovered, tracking of manually enabled host stopped")
			}
		case store.HostStateDisabled:
			if !h.IsDisabled {
				// An admin enabled the host by hand: respect that until the
				// nodes recover, and put template references back.
				m.State = store.HostStateOverridden
				c.restoreTemplates(ctx, client, m)
				m.Mihomo = nil
				_ = c.store.SaveManagedHost(ctx, m)
				c.event(ctx, store.Event{Level: "warn", Type: "host_overridden", HostUUID: m.UUID,
					Message: fmt.Sprintf("Host %q was enabled manually in the panel; it will not be disabled again until its nodes recover", m.Remark),
					Meta:    map[string]any{"host": m.Remark}})
				continue
			}
			if cfg.Checker.Enabled && cfg.Checker.AutoEnable && len(bound) > 0 && ev.recovered(bound) {
				toEnable = append(toEnable, m)
			} else if ev.failed(bound) && ev.reason(bound) != m.Reason {
				// E.g. the node came back online but has no traffic left.
				m.Reason = ev.reason(bound)
				_ = c.store.SaveManagedHost(ctx, m)
			}
		}
	}
	if len(toEnable) > 0 {
		if err := c.enableHosts(ctx, client, toEnable, false); err != nil {
			c.log.Warn("enable hosts failed", "err", err)
		} else {
			enabled := map[string]bool{}
			for _, m := range toEnable {
				enabled[m.UUID] = true
			}
			setDisabled(hosts, enabled, false)
		}
	}

	managed, err = c.store.ManagedHosts(ctx)
	if err != nil {
		return err
	}
	c.buildSnapshot(cfg, nodes, hosts, states, managed, ev)
	return nil
}

type candidate struct {
	host   remnawave.Host
	nodes  []string
	reason string
}

func classify(n remnawave.Node, disabledAsDown bool) string {
	if n.IsDisabled && !disabledAsDown {
		return StatusDisabled
	}
	if n.IsConnected && !n.IsDisabled {
		return StatusUp
	}
	return StatusDown
}

type evaluator struct {
	cfg      settings.Checker
	traffic  settings.Traffic
	states   map[string]*store.NodeState
	usage    map[string]trafficUsage
	excluded map[string]bool
	now      time.Time
}

// trafficUsage is a node's traffic counter against its panel limit;
// limit is 0 when the node has none.
type trafficUsage struct {
	used, limit int64
}

func (u trafficUsage) percent() int64 {
	if u.limit <= 0 {
		return 0
	}
	return u.used * 100 / u.limit
}

// String renders usage for Telegram messages.
func (u trafficUsage) String() string {
	return fmt.Sprintf("%d%% · %s / %s", u.percent(), FormatBytes(u.used), FormatBytes(u.limit))
}

// bound returns the host's nodes that take part in decisions: existing,
// not excluded and not disabled by an admin.
func (e evaluator) bound(h remnawave.Host) []string {
	out := make([]string, 0, len(h.Nodes))
	for _, id := range h.Nodes {
		st := e.states[id]
		if st == nil || e.excluded[id] || st.Status == StatusDisabled {
			continue
		}
		out = append(out, id)
	}
	return out
}

func (e evaluator) downLongEnough(id string) bool {
	st := e.states[id]
	return st != nil && st.Status == StatusDown && st.DownSince != nil &&
		e.now.Sub(*st.DownSince) >= time.Duration(e.cfg.DownMinutes)*time.Minute
}

func (e evaluator) upLongEnough(id string) bool {
	st := e.states[id]
	return st != nil && st.Status == StatusUp && st.UpSince != nil &&
		e.now.Sub(*st.UpSince) >= time.Duration(e.cfg.RecoveryMinutes)*time.Minute
}

// overLimit reports whether the node used up the configured share of its
// traffic limit. Nodes without a limit never reach it.
func (e evaluator) overLimit(id string) bool {
	u := e.usage[id]
	return e.traffic.Enabled && u.limit > 0 && u.used*100 >= u.limit*int64(e.traffic.DisablePercent)
}

// unavailable: the node cannot serve the host, because it is down or out of traffic.
func (e evaluator) unavailable(id string) bool {
	return e.downLongEnough(id) || e.overLimit(id)
}

func (e evaluator) available(id string) bool {
	return e.upLongEnough(id) && !e.overLimit(id)
}

// matches applies the host policy to a node condition: with "all" every
// bound node must meet it, with "any" a single one is enough.
func (e evaluator) matches(nodes []string, fn func(string) bool) bool {
	if e.cfg.Policy == settings.PolicyAny {
		return slices.ContainsFunc(nodes, fn)
	}
	return len(nodes) > 0 && !slices.ContainsFunc(nodes, func(id string) bool { return !fn(id) })
}

func (e evaluator) failed(nodes []string) bool {
	return e.matches(nodes, e.unavailable)
}

// reason tells whether the traffic limit alone is enough to fail the host.
func (e evaluator) reason(nodes []string) string {
	if e.matches(nodes, e.overLimit) {
		return store.ReasonTraffic
	}
	return store.ReasonNodesDown
}

func (e evaluator) recovered(nodes []string) bool {
	if e.cfg.Policy == settings.PolicyAny {
		return len(nodes) > 0 && !slices.ContainsFunc(nodes, func(id string) bool { return !e.available(id) })
	}
	return slices.ContainsFunc(nodes, e.available)
}

// disableAt predicts when a host bound to these nodes will be disabled.
func (e evaluator) disableAt(nodes []string) *time.Time {
	grace := time.Duration(e.cfg.DownMinutes) * time.Minute
	var times []time.Time
	for _, id := range nodes {
		st := e.states[id]
		switch {
		case e.overLimit(id):
			times = append(times, e.now)
		case st != nil && st.Status == StatusDown && st.DownSince != nil:
			times = append(times, st.DownSince.Add(grace))
		case e.cfg.Policy == settings.PolicyAll:
			return nil
		}
	}
	if len(times) == 0 {
		return nil
	}
	var t time.Time
	if e.cfg.Policy == settings.PolicyAll {
		t = slices.MaxFunc(times, time.Time.Compare)
	} else {
		t = slices.MinFunc(times, time.Time.Compare)
	}
	return &t
}

func (e evaluator) downRatio() (down, total int) {
	for id, st := range e.states {
		if e.excluded[id] || st.Status == StatusDisabled {
			continue
		}
		total++
		if st.Status == StatusDown {
			down++
		}
	}
	return down, total
}

const dryRunMark = " <i>(тестовый режим)</i>"

// disableHosts returns the UUIDs that were actually disabled in the panel.
func (c *Checker) disableHosts(ctx context.Context, client *remnawave.Client, cfg settings.Settings, list []candidate, allHosts []remnawave.Host, ev evaluator) map[string]bool {
	nodeNames := func(ids []string) []string {
		out := make([]string, 0, len(ids))
		for _, id := range ids {
			if st := ev.states[id]; st != nil {
				out = append(out, st.Name)
			}
		}
		return out
	}
	// Telegram lines show how much traffic the nodes over the limit used.
	lines := map[string][]string{}
	addLine := func(m *store.ManagedHost) {
		names := make([]string, 0, len(m.Nodes))
		for _, id := range m.Nodes {
			st := ev.states[id]
			if st == nil {
				continue
			}
			if m.Reason == store.ReasonTraffic && ev.overLimit(id) {
				names = append(names, fmt.Sprintf("%s (%s)", st.Name, ev.usage[id]))
			} else {
				names = append(names, st.Name)
			}
		}
		lines[m.Reason] = append(lines[m.Reason], hostLine(m.Remark, names))
	}
	eventMeta := func(m *store.ManagedHost, names []string) map[string]any {
		meta := map[string]any{"host": m.Remark, "nodes": names, "reason": m.Reason}
		if m.Reason == store.ReasonTraffic {
			meta["percent"] = cfg.Traffic.DisablePercent
		}
		return meta
	}

	if cfg.Checker.DryRun {
		for _, cand := range list {
			m := &store.ManagedHost{UUID: cand.host.UUID, Remark: cand.host.Remark, Nodes: cand.nodes,
				State: store.HostStateDryRun, Reason: cand.reason, DisabledAt: time.Now()}
			if err := c.store.SaveManagedHost(ctx, m); err != nil {
				c.log.Error("save managed host", "err", err)
				continue
			}
			names := nodeNames(cand.nodes)
			addLine(m)
			c.event(ctx, store.Event{Level: "warn", Type: "dry_run_disable", HostUUID: m.UUID,
				Message: fmt.Sprintf("[dry run] Host %q would be disabled (%s, nodes: %s)", m.Remark, m.Reason, strings.Join(names, ", ")),
				Meta:    eventMeta(m, names)})
		}
		c.notifyDisabled(cfg, lines, dryRunMark)
		return nil
	}

	disabling := map[string]bool{}
	for _, cand := range list {
		disabling[cand.host.UUID] = true
	}

	var records []*store.ManagedHost
	for _, cand := range list {
		m := &store.ManagedHost{UUID: cand.host.UUID, Remark: cand.host.Remark, Nodes: cand.nodes,
			State: store.HostStateDisabled, Reason: cand.reason, DisabledAt: time.Now(), Mihomo: []store.TemplatePatch{}}
		if cfg.Mihomo.Enabled {
			m.Mihomo = c.removeFromTemplates(ctx, client, cfg, cand.host, allHosts, disabling)
		}
		// Saved before the panel call so a crash cannot leave a host
		// disabled without a record to re-enable it later.
		if err := c.store.SaveManagedHost(ctx, m); err != nil {
			c.log.Error("save managed host", "err", err)
			c.restoreTemplates(ctx, client, m)
			continue
		}
		records = append(records, m)
	}
	if len(records) == 0 {
		return nil
	}

	uuids := make([]string, 0, len(records))
	for _, m := range records {
		uuids = append(uuids, m.UUID)
	}
	if err := client.SetHostsDisabled(ctx, uuids, true); err != nil {
		for _, m := range records {
			c.restoreTemplates(ctx, client, m)
			_ = c.store.DeleteManagedHost(ctx, m.UUID)
		}
		c.failure(ctx, "Failed to disable hosts", err)
		return nil
	}

	for _, m := range records {
		names := nodeNames(m.Nodes)
		addLine(m)
		meta := eventMeta(m, names)
		msg := fmt.Sprintf("Host %q disabled: nodes %s used %d%% of their traffic limit", m.Remark, strings.Join(names, ", "), cfg.Traffic.DisablePercent)
		if m.Reason != store.ReasonTraffic {
			msg = fmt.Sprintf("Host %q disabled: nodes %s are down for more than %d min", m.Remark, strings.Join(names, ", "), cfg.Checker.DownMinutes)
			meta["minutes"] = cfg.Checker.DownMinutes
		}
		c.event(ctx, store.Event{Level: "warn", Type: "host_disabled", HostUUID: m.UUID, Message: msg, Meta: meta})
	}
	c.notifyDisabled(cfg, lines, "")

	disabled := make(map[string]bool, len(records))
	for _, m := range records {
		disabled[m.UUID] = true
	}
	return disabled
}

// notifyDisabled sends one Telegram message per disable reason.
func (c *Checker) notifyDisabled(cfg settings.Settings, lines map[string][]string, dryRun string) {
	if l := lines[store.ReasonNodesDown]; len(l) > 0 {
		c.tg.Notify(notify.Message{Type: settings.EventHostsDisabled,
			HTML: map[string]string{"hosts": strings.Join(l, "\n"), "dry_run": dryRun},
			Text: map[string]string{"down_minutes": fmt.Sprint(cfg.Checker.DownMinutes), "count": fmt.Sprint(len(l))}})
	}
	if l := lines[store.ReasonTraffic]; len(l) > 0 {
		c.tg.Notify(notify.Message{Type: settings.EventTrafficLimit,
			HTML: map[string]string{"hosts": strings.Join(l, "\n"), "dry_run": dryRun},
			Text: map[string]string{"percent": fmt.Sprint(cfg.Traffic.DisablePercent), "count": fmt.Sprint(len(l))}})
	}
}

// setDisabled mirrors panel changes made during a cycle into the fetched list.
func setDisabled(hosts []remnawave.Host, ids map[string]bool, disabled bool) {
	for i := range hosts {
		if ids[hosts[i].UUID] {
			hosts[i].IsDisabled = disabled
		}
	}
}

func (c *Checker) enableHosts(ctx context.Context, client *remnawave.Client, list []*store.ManagedHost, manual bool) error {
	uuids := make([]string, 0, len(list))
	for _, m := range list {
		uuids = append(uuids, m.UUID)
	}
	if err := client.SetHostsDisabled(ctx, uuids, false); err != nil {
		c.failure(ctx, "Failed to enable hosts", err)
		return err
	}
	lines := make([]string, 0, len(list))
	for _, m := range list {
		c.restoreTemplates(ctx, client, m)
		if err := c.store.DeleteManagedHost(ctx, m.UUID); err != nil {
			c.log.Error("delete managed host", "err", err)
		}
		lines = append(lines, hostLine(m.Remark, nil))
		reason := "manual"
		switch {
		case manual:
		case m.Reason == store.ReasonTraffic:
			reason = "traffic below limit"
		default:
			reason = "nodes recovered"
		}
		c.event(ctx, store.Event{Level: "info", Type: "host_enabled", HostUUID: m.UUID,
			Message: fmt.Sprintf("Host %q enabled (%s)", m.Remark, reason),
			Meta:    map[string]any{"host": m.Remark, "reason": reason}})
	}
	c.tg.Notify(notify.Message{Type: settings.EventHostsEnabled,
		HTML: map[string]string{"hosts": strings.Join(lines, "\n"), "dry_run": ""},
		Text: map[string]string{"count": fmt.Sprint(len(list))}})
	return nil
}

// release stops tracking a host without touching its state in the panel.
func (c *Checker) release(ctx context.Context, m *store.ManagedHost, typ, msg string) {
	if err := c.store.DeleteManagedHost(ctx, m.UUID); err != nil {
		c.log.Error("delete managed host", "err", err)
	}
	c.event(ctx, store.Event{Level: "info", Type: typ, HostUUID: m.UUID,
		Message: fmt.Sprintf("%s: %q", msg, m.Remark), Meta: map[string]any{"host": m.Remark}})
}

func (c *Checker) nodeDown(ctx context.Context, cfg settings.Settings, n remnawave.Node, st *store.NodeState, hosts []remnawave.Host) {
	active := 0
	for _, h := range hosts {
		if !h.IsDisabled {
			active++
		}
	}
	c.event(ctx, store.Event{Level: "warn", Type: "node_down", NodeUUID: n.UUID,
		Message: fmt.Sprintf("Node %q is down: %s", n.Name, st.LastMessage),
		Meta:    map[string]any{"node": n.Name, "message": st.LastMessage, "hosts": active}})
	c.tg.Notify(notify.Message{Type: settings.EventNodeDown, Text: map[string]string{
		"node": n.Name, "node_address": n.Address, "message": orDash(st.LastMessage),
		"hosts_count": fmt.Sprint(active), "down_minutes": fmt.Sprint(cfg.Checker.DownMinutes),
	}})
}

func (c *Checker) nodeUp(ctx context.Context, n remnawave.Node, downtime time.Duration) {
	c.event(ctx, store.Event{Level: "info", Type: "node_up", NodeUUID: n.UUID,
		Message: fmt.Sprintf("Node %q is back online after %s", n.Name, downtime.Round(time.Second)),
		Meta:    map[string]any{"node": n.Name, "downtimeSeconds": int(downtime.Seconds())}})
	c.tg.Notify(notify.Message{Type: settings.EventNodeUp, Text: map[string]string{
		"node": n.Name, "node_address": n.Address, "downtime": FormatDuration(downtime),
	}})
}

func (c *Checker) panelFailure(ctx context.Context, err error) {
	c.panelFails++
	c.setSnapshotError("panel_error", err.Error())
	c.log.Warn("panel request failed", "err", err, "attempt", c.panelFails)
	if c.panelFails >= panelFailThreshold && !c.panelNotified {
		c.panelNotified = true
		c.event(ctx, store.Event{Level: "error", Type: "panel_unreachable",
			Message: "Remnawave panel is unreachable: " + err.Error(), Meta: map[string]any{"error": err.Error()}})
		c.tg.Notify(notify.Message{Type: settings.EventPanelUnreachable, Text: map[string]string{"error": err.Error()}})
	}
}

func (c *Checker) panelSuccess(ctx context.Context, client *remnawave.Client) {
	c.panelFails = 0
	if c.panelVersion == "" || time.Since(c.versionFetched) > metadataRefresh || c.panelNotified {
		if meta, err := client.Metadata(ctx); err == nil {
			c.panelVersion = meta.Version
		}
		c.versionFetched = time.Now()
	}
	if c.panelNotified {
		c.panelNotified = false
		c.event(ctx, store.Event{Level: "info", Type: "panel_restored",
			Message: "Connection to the panel restored", Meta: map[string]any{"version": c.panelVersion}})
		c.tg.Notify(notify.Message{Type: settings.EventPanelRestored, Text: map[string]string{"version": orDash(c.panelVersion)}})
	}
}

func (c *Checker) failure(ctx context.Context, what string, err error) {
	c.log.Error(what, "err", err)
	c.event(ctx, store.Event{Level: "error", Type: "error", Message: what + ": " + err.Error(),
		Meta: map[string]any{"error": err.Error(), "context": what}})
	c.tg.Notify(notify.Message{Type: settings.EventError, Text: map[string]string{"error": what + ": " + err.Error()}})
}

func (c *Checker) event(ctx context.Context, e store.Event) {
	level := slog.LevelInfo
	switch e.Level {
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	c.log.Log(ctx, level, e.Message, "type", e.Type)
	if err := c.store.AddEvent(ctx, e); err != nil {
		c.log.Error("store event", "err", err)
	}
}

func (c *Checker) setSnapshotError(code, msg string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.snapshot.CheckedAt = time.Now()
	c.snapshot.PanelOK = false
	c.snapshot.PanelError = msg
	c.snapshot.PanelErrorCode = code
}

// ---- manual actions ----

var ErrNotManaged = errors.New("host is not managed by remnanode-manager")

// EnableNow re-enables a managed host immediately.
func (c *Checker) EnableNow(ctx context.Context, uuid string) error {
	c.opMu.Lock()
	defer c.opMu.Unlock()
	m, err := c.store.ManagedHost(ctx, uuid)
	if errors.Is(err, store.ErrNotFound) {
		return ErrNotManaged
	}
	if err != nil {
		return err
	}
	if m.State != store.HostStateDisabled {
		return c.store.DeleteManagedHost(ctx, uuid)
	}
	client, err := c.client(c.store.Settings())
	if err != nil {
		return err
	}
	return c.enableHosts(ctx, client, []*store.ManagedHost{m}, true)
}

// Forget stops tracking a host and leaves it as it is in the panel.
func (c *Checker) Forget(ctx context.Context, uuid string) error {
	c.opMu.Lock()
	defer c.opMu.Unlock()
	m, err := c.store.ManagedHost(ctx, uuid)
	if errors.Is(err, store.ErrNotFound) {
		return ErrNotManaged
	}
	if err != nil {
		return err
	}
	c.release(ctx, m, "host_released", "Tracking stopped manually")
	return nil
}

// TestPanel checks connectivity with the given (possibly unsaved) settings.
func (c *Checker) TestPanel(ctx context.Context, cfg settings.Settings) (*PanelInfo, error) {
	client, err := c.client(cfg)
	if err != nil {
		return nil, err
	}
	meta, err := client.Metadata(ctx)
	if err != nil {
		return nil, err
	}
	nodes, err := client.Nodes(ctx)
	if err != nil {
		return nil, fmt.Errorf("nodes: %w", err)
	}
	hosts, err := client.Hosts(ctx)
	if err != nil {
		return nil, fmt.Errorf("hosts: %w", err)
	}
	info := &PanelInfo{Version: meta.Version, Nodes: len(nodes), Hosts: len(hosts)}
	if tpls, err := client.Templates(ctx); err == nil {
		info.Templates = len(tpls)
	} else {
		info.TemplatesError = err.Error()
	}
	return info, nil
}

type PanelInfo struct {
	Version        string `json:"version"`
	Nodes          int    `json:"nodes"`
	Hosts          int    `json:"hosts"`
	Templates      int    `json:"templates"`
	TemplatesError string `json:"templatesError,omitempty"`
}

// ---- helpers ----

func hostLine(remark string, nodes []string) string {
	line := "• <b>" + notify.Esc(remark) + "</b>"
	if len(nodes) > 0 {
		line += " — " + notify.Esc(strings.Join(nodes, ", "))
	}
	return line
}

func sortedManaged(m map[string]*store.ManagedHost) []*store.ManagedHost {
	out := make([]*store.ManagedHost, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	slices.SortFunc(out, func(a, b *store.ManagedHost) int { return strings.Compare(a.Remark, b.Remark) })
	return out
}

func toSet(list []string) map[string]bool {
	out := make(map[string]bool, len(list))
	for _, v := range list {
		out[v] = true
	}
	return out
}

func ptr[T any](v T) *T { return &v }

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}

// FormatDuration renders a duration in Russian for Telegram messages.
func FormatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("%d сек", int(d.Seconds()))
	}
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60
	var parts []string
	if days > 0 {
		parts = append(parts, fmt.Sprintf("%d д", days))
	}
	if hours > 0 {
		parts = append(parts, fmt.Sprintf("%d ч", hours))
	}
	if mins > 0 || len(parts) == 0 {
		parts = append(parts, fmt.Sprintf("%d мин", mins))
	}
	return strings.Join(parts, " ")
}

// FormatBytes renders a byte count in Russian with binary units, the way the
// panel counts node traffic.
func FormatBytes(b int64) string {
	units := []string{"Б", "КБ", "МБ", "ГБ", "ТБ", "ПБ"}
	v := float64(b)
	i := 0
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	num := strconv.FormatFloat(math.Round(v*10)/10, 'f', -1, 64)
	return strings.Replace(num, ".", ",", 1) + " " + units[i]
}
