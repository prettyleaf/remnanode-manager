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
