package checker

import (
	"context"
	"testing"
	"time"

	"github.com/prettyleaf/remnanode-manager/internal/settings"
	"github.com/prettyleaf/remnanode-manager/internal/store"
)

func blockSetup(t *testing.T, mutate func(*settings.Settings)) (*Checker, *fakePanel, *clock, *store.Store) {
	t.Helper()
	return setup(t, false, func(s *settings.Settings) {
		s.Mihomo.Enabled = false
		s.Block.Enabled = true
		if mutate != nil {
			mutate(s)
		}
	})
}

// cycles runs one check per minute.
func cycles(c *Checker, clk *clock, minutes int) {
	for range minutes {
		clk.advance(time.Minute)
		c.runCycle(context.Background())
	}
}

func eventCount(t *testing.T, st *store.Store, typ string) int {
	t.Helper()
	events, err := st.Events(context.Background(), store.EventFilter{Type: typ})
	if err != nil {
		t.Fatal(err)
	}
	return len(events)
}

func nodeView(c *Checker, id string) NodeView {
	for _, n := range c.Snapshot().Nodes {
		if n.UUID == id {
			return n
		}
	}
	return NodeView{}
}

func TestBlockNotifyOnly(t *testing.T) {
	c, panel, clk, st := blockSetup(t, nil)
	cycles(c, clk, 40)
	if v := nodeView(c, nodeA); v.Block.Usual != 100 || v.Online == nil || *v.Online != 100 {
		t.Fatalf("unexpected online view: %+v", v)
	}

	// The drop is seen on the next check and reported ConfirmMinutes later.
	panel.setOnline(nodeA, 3)
	cycles(c, clk, 5)
	if v := nodeView(c, nodeA); v.Block.DropSince == nil || v.Block.BlockedSince != nil {
		t.Fatalf("drop not tracked or reported too early: %+v", v.Block)
	}
	cycles(c, clk, 1)
	if eventCount(t, st, "node_blocked") != 1 {
		t.Fatal("block not reported")
	}
	if v := nodeView(c, nodeA); v.Block.BlockedSince == nil || v.Block.Usual != 100 {
		t.Fatalf("unexpected block view: %+v", v.Block)
	}
	if c.Snapshot().Stats.NodesBlocked != 1 {
		t.Fatalf("expected one blocked node: %+v", c.Snapshot().Stats)
	}

	// Disabling hosts is off: notifications only.
	cycles(c, clk, 60)
	if panel.hostDisabled(hostA) || c.Snapshot().Stats.HostsAtRisk != 0 {
		t.Fatal("host touched while only notifications are on")
	}
	if eventCount(t, st, "node_blocked") != 1 {
		t.Fatal("block reported more than once")
	}

	panel.setOnline(nodeA, 90)
	cycles(c, clk, 5)
	if eventCount(t, st, "node_unblocked") != 0 {
		t.Fatal("recovery reported before the online held")
	}
	cycles(c, clk, 1)
	if eventCount(t, st, "node_unblocked") != 1 || nodeView(c, nodeA).Block.BlockedSince != nil {
		t.Fatal("recovery not reported")
	}
}

func TestBlockIgnoresNoise(t *testing.T) {
	c, panel, clk, st := blockSetup(t, nil)
	// Too few users to judge, see the end.
	panel.setOnline(nodeB, 10)

	// Not enough history yet.
	cycles(c, clk, 20)
	panel.setOnline(nodeA, 0)
	cycles(c, clk, 6)
	panel.setOnline(nodeA, 100)
	cycles(c, clk, 40)

	// A short dip.
	panel.setOnline(nodeA, 0)
	cycles(c, clk, 3)
	panel.setOnline(nodeA, 100)
	cycles(c, clk, 3)

	// Xray restart: users reconnect, the node is not judged meanwhile.
	panel.setUptime(nodeA, 60)
	panel.setOnline(nodeA, 0)
	cycles(c, clk, 6)
	if v := nodeView(c, nodeA); v.Block.DropSince != nil {
		t.Fatalf("drop tracked during Xray warm-up: %+v", v.Block)
	}
	panel.setUptime(nodeA, 86400)
	panel.setOnline(nodeA, 100)

	// An outage is the down logic's business.
	panel.setNode(nodeA, false)
	panel.setOnline(nodeA, 0)
	cycles(c, clk, 10)
	panel.setNode(nodeA, true)
	cycles(c, clk, 3)
	panel.setOnline(nodeA, 100)
	cycles(c, clk, 30)

	panel.setOnline(nodeB, 0)
	cycles(c, clk, 10)

	if n := eventCount(t, st, "node_blocked"); n != 0 {
		t.Fatalf("noise reported as a block %d times", n)
	}
}

func TestBlockGuard(t *testing.T) {
	c, panel, clk, st := blockSetup(t, nil)
	cycles(c, clk, 40)

	panel.setOnline(nodeA, 0)
	panel.setOnline(nodeB, 1)
	cycles(c, clk, 10)
	if eventCount(t, st, "node_blocked") != 0 {
		t.Fatal("blocks reported while every node dropped")
	}
	if eventCount(t, st, "block_guard") != 1 {
		t.Fatal("guard not reported once")
	}

	// Off: each drop counts.
	cfg := st.Settings()
	cfg.Block.GuardPercent = 0
	if err := st.SaveSettings(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	cycles(c, clk, 1)
	if eventCount(t, st, "node_blocked") != 2 {
		t.Fatal("blocks not reported with the guard off")
	}
}

func TestBlockDisablesHosts(t *testing.T) {
	ctx := context.Background()
	c, panel, clk, st := blockSetup(t, func(s *settings.Settings) {
		s.Block.DisableHosts = true
		s.Block.DisableMinutes = 10
	})
	cycles(c, clk, 40)

	panel.setOnline(nodeA, 0)
	cycles(c, clk, 6)
	if eventCount(t, st, "node_blocked") != 1 {
		t.Fatal("block not reported")
	}
	if c.Snapshot().Stats.HostsAtRisk != 1 {
		t.Fatalf("host of the blocked node not at risk: %+v", c.Snapshot().Stats)
	}
	cycles(c, clk, 9)
	if panel.hostDisabled(hostA) {
		t.Fatal("host disabled before the delay ended")
	}
	cycles(c, clk, 1)
	if !panel.hostDisabled(hostA) {
		t.Fatal("host of the blocked node not disabled")
	}
	if panel.hostDisabled(hostB) || !panel.hostDisabled(hostC) {
		t.Fatal("unrelated hosts changed")
	}
	m, err := st.ManagedHost(ctx, hostA)
	if err != nil || m.Reason != store.ReasonBlocked {
		t.Fatalf("expected blocked record, got %+v %v", m, err)
	}

	// Nobody can reach the node now, so the block holds.
	cycles(c, clk, 120)
	if !panel.hostDisabled(hostA) {
		t.Fatal("host enabled while the node is still blocked")
	}

	// The admin checked the node: the host stays on until users return.
	if err := c.EnableNow(ctx, hostA); err != nil {
		t.Fatal(err)
	}
	cycles(c, clk, 30)
	if panel.hostDisabled(hostA) {
		t.Fatal("manually enabled host was disabled again")
	}
	if m, err := st.ManagedHost(ctx, hostA); err != nil || m.State != store.HostStateOverridden {
		t.Fatalf("expected overridden state, got %+v %v", m, err)
	}

	panel.setOnline(nodeA, 80)
	cycles(c, clk, 6)
	if eventCount(t, st, "node_unblocked") != 1 {
		t.Fatal("recovery not reported")
	}
	if _, err := st.ManagedHost(ctx, hostA); err == nil {
		t.Fatal("tracking not released after the online came back")
	}
}

func TestBlockDryRun(t *testing.T) {
	c, panel, clk, st := blockSetup(t, func(s *settings.Settings) {
		s.Checker.DryRun = true
		s.Block.DisableHosts = true
		s.Block.DisableMinutes = 5
	})
	cycles(c, clk, 40)
	panel.setOnline(nodeA, 0)
	cycles(c, clk, 15)
	if panel.hostDisabled(hostA) || len(panel.bulkCalls) != 0 {
		t.Fatal("dry run modified the panel")
	}
	m, err := st.ManagedHost(context.Background(), hostA)
	if err != nil || m.State != store.HostStateDryRun || m.Reason != store.ReasonBlocked {
		t.Fatalf("expected dry-run blocked record, got %+v %v", m, err)
	}
}
