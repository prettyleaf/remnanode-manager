package remnawave

import "time"

// The structs below intentionally contain only the fields this service needs.
// encoding/json ignores unknown fields and leaves missing ones zero-valued,
// which keeps the client compatible with panel versions 2.7.x through 3.x.

type Node struct {
	UUID              string     `json:"uuid"`
	Name              string     `json:"name"`
	Address           string     `json:"address"`
	Port              *int       `json:"port"`
	CountryCode       string     `json:"countryCode"`
	IsConnected       bool       `json:"isConnected"`
	IsConnecting      bool       `json:"isConnecting"`
	IsDisabled        bool       `json:"isDisabled"`
	LastStatusChange  *time.Time `json:"lastStatusChange"`
	LastStatusMessage *string    `json:"lastStatusMessage"`
	Tags              []string   `json:"tags"`
	// Traffic accounting configured on the node in the panel. The limit only
	// applies while tracking is active; the panel resets usage on the reset day.
	IsTrafficTrackingActive bool     `json:"isTrafficTrackingActive"`
	TrafficLimitBytes       *float64 `json:"trafficLimitBytes"`
	TrafficUsedBytes        *float64 `json:"trafficUsedBytes"`
	TrafficResetDay         *int     `json:"trafficResetDay"`
	// Users connected right now and Xray uptime in seconds, as last
	// collected by the panel.
	UsersOnline *float64 `json:"usersOnline"`
	XrayUptime  *float64 `json:"xrayUptime"`
}

// Online returns the number of users on the node, if the panel reports it.
func (n Node) Online() (int, bool) {
	if n.UsersOnline == nil || *n.UsersOnline < 0 {
		return 0, false
	}
	return int(*n.UsersOnline), true
}

// XrayUptimeDuration returns 0 when the panel does not know the uptime.
func (n Node) XrayUptimeDuration() time.Duration {
	if n.XrayUptime == nil || *n.XrayUptime <= 0 {
		return 0
	}
	return time.Duration(*n.XrayUptime * float64(time.Second))
}

// TrafficLimit returns the node's traffic limit, or 0 when it has none.
func (n Node) TrafficLimit() int64 {
	if !n.IsTrafficTrackingActive || n.TrafficLimitBytes == nil || *n.TrafficLimitBytes <= 0 {
		return 0
	}
	return int64(*n.TrafficLimitBytes)
}

func (n Node) TrafficUsed() int64 {
	if n.TrafficUsedBytes == nil || *n.TrafficUsedBytes < 0 {
		return 0
	}
	return int64(*n.TrafficUsedBytes)
}

type HostInbound struct {
	ConfigProfileUUID        *string `json:"configProfileUuid"`
	ConfigProfileInboundUUID *string `json:"configProfileInboundUuid"`
}

type Host struct {
	UUID                         string      `json:"uuid"`
	ViewPosition                 int         `json:"viewPosition"`
	Remark                       string      `json:"remark"`
	Address                      string      `json:"address"`
	Port                         int         `json:"port"`
	IsDisabled                   bool        `json:"isDisabled"`
	IsHidden                     bool        `json:"isHidden"`
	Nodes                        []string    `json:"nodes"`
	Inbound                      HostInbound `json:"inbound"`
	ExcludeFromSubscriptionTypes []string    `json:"excludeFromSubscriptionTypes"`
	// 2.7.x exposes a single "tag", 3.x replaced it with "tags".
	Tag  *string  `json:"tag"`
	Tags []string `json:"tags"`
}

// AllTags merges the 2.7.x "tag" and 3.x "tags" fields.
func (h Host) AllTags() []string {
	if len(h.Tags) > 0 {
		return h.Tags
	}
	if h.Tag != nil && *h.Tag != "" {
		return []string{*h.Tag}
	}
	return []string{}
}

type SubscriptionTemplate struct {
	UUID                string  `json:"uuid"`
	Name                string  `json:"name"`
	TemplateType        string  `json:"templateType"`
	EncodedTemplateYAML *string `json:"encodedTemplateYaml"`
}

type Metadata struct {
	Version string `json:"version"`
	Build   struct {
		Time   string `json:"time"`
		Number string `json:"number"`
	} `json:"build"`
}
