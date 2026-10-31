package checker

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/prettyleaf/remnanode-manager/internal/notify"
	"github.com/prettyleaf/remnanode-manager/internal/remnawave"
	"github.com/prettyleaf/remnanode-manager/internal/settings"
	"github.com/prettyleaf/remnanode-manager/internal/store"
)

const (
	// Online history that defines the usual level of a node.
	blockWindow = time.Hour
	// A node that has just come up or restarted Xray is still gathering its
	// users back, so its online is not judged yet.
	blockWarmup = 10 * time.Minute
)

// detectBlocks records the online of nodes and watches it on nodes that stay
// connected to the panel: when their users vanish while the panel still
// reaches them, the address was most likely blocked for clients. Outages are
// left to the usual down logic. It returns the current and the usual online
// of the nodes that have them.
func (c *Checker) detectBlocks(ctx context.Context, cfg settings.Settings, nodes []remnawave.Node, states map[string]*store.NodeState,
	boundHosts map[string][]remnawave.Host, excluded map[string]bool, now time.Time) (current, usual map[string]int, err error) {
	history, err := c.store.OnlineSamples(ctx, now.Add(-blockWindow))
	if err != nil {
		return nil, nil, err
	}
	current, usual = map[string]int{}, map[string]int{}
	for _, n := range nodes {
		if v, ok := usualOnline(history[n.UUID], now); ok {
			usual[n.UUID] = v
		}
		if online, ok := n.Online(); ok && states[n.UUID].Status == StatusUp {
			current[n.UUID] = online
		}
	}
	// The history is kept while the feature is off, so it is ready once
	// turned on.
	if err := c.store.AddOnlineSamples(ctx, now, current, now.Add(-blockWindow)); err != nil {
		return nil, nil, err
	}

	b := cfg.Block
	if !b.Enabled {
		for _, st := range states {
			st.ClearBlock()
		}
		c.blockGuardNotified = false
		return current, usual, nil
	}

	confirm := time.Duration(b.ConfirmMinutes) * time.Minute
	watched := 0
	var dropping []remnawave.Node
	for _, n := range nodes {
		st := states[n.UUID]
		if excluded[n.UUID] || st.Status == StatusDisabled {
			st.ClearBlock()
			continue
		}
		online, known := current[n.UUID]
		if !known || !settled(n, st, now) || !servesUsers(boundHosts[n.UUID]) {
			// Down, warming up or with every host off: the online says
			// nothing new, a reported block stays as it is.
			st.RiseSince = nil
			if st.BlockedSince == nil {
				st.DropSince, st.Usual = nil, 0
			}
			continue
		}

		if st.BlockedSince != nil {
			if dropped(online, st.Usual, b.DropPercent) {
				st.RiseSince = nil
				continue
			}
			if st.RiseSince == nil {
				st.RiseSince = ptr(now)
			}
			if now.Sub(*st.RiseSince) >= confirm {
				c.nodeUnblocked(ctx, n, st, online)
				st.ClearBlock()
			}
			continue
		}

		if st.DropSince == nil {
			u, ok := usual[n.UUID]
			if !ok || u < b.MinUsers {
				continue
			}
			watched++
			if dropped(online, u, b.DropPercent) {
				st.DropSince, st.Usual = ptr(now), u
				dropping = append(dropping, n)
			}
			continue
		}
		watched++
		if !dropped(online, st.Usual, b.DropPercent) {
			// A short dip.
			st.DropSince, st.Usual = nil, 0
			continue
		}
		dropping = append(dropping, n)
	}

	// Users leaving many nodes at once point to a common cause: a mobile
	// internet shutdown, broken panel statistics. Drops wait until it ends.
	if b.GuardPercent > 0 && watched >= 2 && len(dropping)*100 >= b.GuardPercent*watched {
		if !c.blockGuardNotified {
			c.blockGuardNotified = true
			c.event(ctx, store.Event{Level: "warn", Type: "block_guard",
				Message: fmt.Sprintf("Online dropped on %d of %d watched nodes at once: looks like a common problem, blocks are not reported", len(dropping), watched),
				Meta:    map[string]any{"drop": len(dropping), "total": watched}})
			c.tg.Notify(notify.Message{Type: settings.EventBlockGuard, Text: map[string]string{
				"drop_nodes": fmt.Sprint(len(dropping)), "total_nodes": fmt.Sprint(watched),
			}})
		}
		return current, usual, nil
	}
	c.blockGuardNotified = false
	for _, n := range dropping {
		st := states[n.UUID]
		if now.Sub(*st.DropSince) >= confirm {
			st.BlockedSince = ptr(now)
			c.nodeBlocked(ctx, cfg, n, st, current[n.UUID], boundHosts[n.UUID])
		}
	}
	return current, usual, nil
}

// usualOnline is the median online over the history, or false while the
// history is too short to trust.
func usualOnline(samples []store.OnlineSample, now time.Time) (int, bool) {
	if len(samples) < 3 || now.Sub(samples[0].Time) < blockWindow/2 {
		return 0, false
	}
	vals := make([]int, len(samples))
	for i, s := range samples {
		vals[i] = s.Online
	}
	slices.Sort(vals)
	return vals[len(vals)/2], true
}

// dropped: online at or below the drop share of the usual online.
func dropped(online, usual, percent int) bool {
	return online*100 <= usual*percent
}

// settled: the node has been up long enough for its users to come back after
// an outage or an Xray restart.
func settled(n remnawave.Node, st *store.NodeState, now time.Time) bool {
	if st.UpSince == nil || now.Sub(*st.UpSince) < blockWarmup {
		return false
	}
	up := n.XrayUptimeDuration()
	return up == 0 || up >= blockWarmup
}

// servesUsers: clients can get to the node through an enabled host, or no
// host is bound to it at all.
func servesUsers(hosts []remnawave.Host) bool {
	return len(hosts) == 0 || slices.ContainsFunc(hosts, func(h remnawave.Host) bool { return !h.IsDisabled })
}

func (c *Checker) nodeBlocked(ctx context.Context, cfg settings.Settings, n remnawave.Node, st *store.NodeState, online int, hosts []remnawave.Host) {
	active := 0
	for _, h := range hosts {
		if !h.IsDisabled {
			active++
		}
	}
	action := "Хосты автоматически не отключаются: проверьте ноду и при необходимости отключите их вручную."
	switch {
	case active == 0:
		action = ""
	case cfg.Block.DisableHosts && cfg.Checker.Enabled:
		action = fmt.Sprintf("Хосты ноды (%d) будут отключены через %d мин., если онлайн не вернётся.", active, cfg.Block.DisableMinutes)
	}
	c.event(ctx, store.Event{Level: "warn", Type: "node_blocked", NodeUUID: n.UUID,
		Message: fmt.Sprintf("Node %q looks blocked: %d users online, usually %d", n.Name, online, st.Usual),
		Meta:    map[string]any{"node": n.Name, "online": online, "usual": st.Usual, "hosts": active}})
	c.tg.Notify(notify.Message{Type: settings.EventNodeBlocked, Text: map[string]string{
		"node": n.Name, "node_address": n.Address, "online": fmt.Sprint(online), "usual": fmt.Sprint(st.Usual),
		"hosts_count": fmt.Sprint(active), "disable_minutes": fmt.Sprint(cfg.Block.DisableMinutes), "action": action,
	}})
}

func (c *Checker) nodeUnblocked(ctx context.Context, n remnawave.Node, st *store.NodeState, online int) {
	empty := st.RiseSince.Sub(*st.DropSince)
	c.event(ctx, store.Event{Level: "info", Type: "node_unblocked", NodeUUID: n.UUID,
		Message: fmt.Sprintf("Online on node %q is back: %d users, usually %d, after %s", n.Name, online, st.Usual, empty.Round(time.Second)),
		Meta:    map[string]any{"node": n.Name, "online": online, "usual": st.Usual, "downtimeSeconds": int(empty.Seconds())}})
	c.tg.Notify(notify.Message{Type: settings.EventNodeUnblocked, Text: map[string]string{
		"node": n.Name, "node_address": n.Address, "online": fmt.Sprint(online), "usual": fmt.Sprint(st.Usual),
		"duration": FormatDuration(empty),
	}})
}
