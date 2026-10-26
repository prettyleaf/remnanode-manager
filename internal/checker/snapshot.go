package checker

import (
	"slices"
	"strings"
	"time"

	"github.com/prettyleaf/remnanode-manager/internal/remnawave"
	"github.com/prettyleaf/remnanode-manager/internal/settings"
	"github.com/prettyleaf/remnanode-manager/internal/store"
)

// Snapshot is the latest view of the panel shown in the web UI.
type Snapshot struct {
	CheckedAt      time.Time            `json:"checkedAt"`
	NextCheckAt    time.Time            `json:"nextCheckAt"`
	PanelOK        bool                 `json:"panelOk"`
	PanelError     string               `json:"panelError,omitempty"`
	PanelErrorCode string               `json:"panelErrorCode,omitempty"`
	PanelVersion   string               `json:"panelVersion,omitempty"`
	Nodes          []NodeView           `json:"nodes"`
	Hosts          []HostView           `json:"hosts"`
	Managed        []*store.ManagedHost `json:"managed"`
	Stats          Stats                `json:"stats"`
}

type Stats struct {
	NodesTotal    int `json:"nodesTotal"`
	NodesUp       int `json:"nodesUp"`
	NodesDown     int `json:"nodesDown"`
	NodesDisabled int `json:"nodesDisabled"`
	HostsTotal    int `json:"hostsTotal"`
	HostsBound    int `json:"hostsBound"`
	HostsManaged  int `json:"hostsManaged"`
	HostsAtRisk   int `json:"hostsAtRisk"`
	// Nodes whose hosts are taken down by the traffic limit.
	NodesOverLimit int `json:"nodesOverLimit"`
}

type NodeView struct {
	UUID        string      `json:"uuid"`
	Name        string      `json:"name"`
	Address     string      `json:"address"`
	CountryCode string      `json:"countryCode"`
	Status      string      `json:"status"`
	Excluded    bool        `json:"excluded"`
	DownSince   *time.Time  `json:"downSince"`
	UpSince     *time.Time  `json:"upSince"`
	LastMessage string      `json:"lastMessage"`
	Hosts       []string    `json:"hosts"`
	Traffic     TrafficView `json:"traffic"`
}

type TrafficView struct {
	UsedBytes  int64 `json:"usedBytes"`
	LimitBytes int64 `json:"limitBytes"`
	ResetDay   *int  `json:"resetDay,omitempty"`
	OverLimit  bool  `json:"overLimit"`
}

const (
	HostOK         = "ok"
	HostAtRisk     = "at_risk"
	HostManaged    = "managed"
	HostDryRun     = "dry_run"
	HostOverridden = "overridden"
	HostDisabled   = "disabled"
	HostExcluded   = "excluded"
	HostUnbound    = "unbound"
)

type HostView struct {
	UUID       string     `json:"uuid"`
	Remark     string     `json:"remark"`
	Address    string     `json:"address"`
	Port       int        `json:"port"`
	IsDisabled bool       `json:"isDisabled"`
	IsHidden   bool       `json:"isHidden"`
	Nodes      []string   `json:"nodes"`
	Tags       []string   `json:"tags"`
	Excluded   bool       `json:"excluded"`
	State      string     `json:"state"`
	Reason     string     `json:"reason,omitempty"`
	DisableAt  *time.Time `json:"disableAt,omitempty"`
}

func (c *Checker) Snapshot() Snapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.snapshot
}

func (c *Checker) buildSnapshot(cfg settings.Settings, nodes []remnawave.Node, hosts []remnawave.Host, states map[string]*store.NodeState, managed map[string]*store.ManagedHost, ev evaluator) {
	excludedHosts := toSet(cfg.Checker.ExcludedHosts)
	snap := Snapshot{
		CheckedAt:    time.Now(),
		PanelOK:      true,
		PanelVersion: c.panelVersion,
		Nodes:        make([]NodeView, 0, len(nodes)),
		Hosts:        make([]HostView, 0, len(hosts)),
		Managed:      sortedManaged(managed),
	}

	bound := map[string][]string{}
	for _, h := range hosts {
		for _, n := range h.Nodes {
			bound[n] = append(bound[n], h.UUID)
		}
	}

	for _, n := range nodes {
		st := states[n.UUID]
		v := NodeView{
			UUID: n.UUID, Name: n.Name, Address: n.Address, CountryCode: n.CountryCode,
			Excluded: ev.excluded[n.UUID], Hosts: bound[n.UUID],
		}
		if v.Hosts == nil {
			v.Hosts = []string{}
		}
		v.Traffic = TrafficView{UsedBytes: n.TrafficUsed(), LimitBytes: n.TrafficLimit(), OverLimit: ev.overLimit(n.UUID)}
		if v.Traffic.LimitBytes > 0 {
			v.Traffic.ResetDay = n.TrafficResetDay
		}
		if v.Traffic.OverLimit && !v.Excluded {
			snap.Stats.NodesOverLimit++
		}
		if st != nil {
			v.Status, v.DownSince, v.UpSince, v.LastMessage = st.Status, st.DownSince, st.UpSince, st.LastMessage
		}
		switch v.Status {
		case StatusUp:
			snap.Stats.NodesUp++
		case StatusDown:
			snap.Stats.NodesDown++
		case StatusDisabled:
			snap.Stats.NodesDisabled++
		}
		snap.Nodes = append(snap.Nodes, v)
	}
	snap.Stats.NodesTotal = len(nodes)

	for _, h := range hosts {
		v := HostView{
			UUID: h.UUID, Remark: h.Remark, Address: h.Address, Port: h.Port,
			IsDisabled: h.IsDisabled, IsHidden: h.IsHidden, Nodes: h.Nodes, Tags: h.AllTags(),
			Excluded: excludedHosts[h.UUID],
		}
		if v.Nodes == nil {
			v.Nodes = []string{}
		}
		b := ev.bound(h)
		if len(h.Nodes) > 0 {
			snap.Stats.HostsBound++
		}
		m := managed[h.UUID]
		if m != nil {
			v.Reason = m.Reason
		}
		switch {
		case m != nil && m.State == store.HostStateDisabled:
			v.State = HostManaged
		case m != nil && m.State == store.HostStateDryRun:
			v.State = HostDryRun
		case m != nil && m.State == store.HostStateOverridden:
			v.State = HostOverridden
		case h.IsDisabled:
			v.State = HostDisabled
		case v.Excluded:
			v.State = HostExcluded
		case len(b) == 0:
			v.State = HostUnbound
		default:
			v.State = HostOK
			if at := ev.disableAt(b); at != nil {
				v.State = HostAtRisk
				v.DisableAt = at
				snap.Stats.HostsAtRisk++
			}
		}
		snap.Hosts = append(snap.Hosts, v)
	}
	snap.Stats.HostsTotal = len(hosts)
	snap.Stats.HostsManaged = len(managed)

	slices.SortStableFunc(snap.Nodes, func(a, b NodeView) int { return strings.Compare(a.Name, b.Name) })

	c.mu.Lock()
	snap.NextCheckAt = c.snapshot.NextCheckAt
	c.snapshot = snap
	c.mu.Unlock()
}
