package settings

import (
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
)

const (
	PolicyAll = "all"
	PolicyAny = "any"
)

// Event types used for the log and Telegram notifications.
const (
	EventNodeDown         = "node_down"
	EventNodeUp           = "node_up"
	EventHostsDisabled    = "hosts_disabled"
	EventHostsEnabled     = "hosts_enabled"
	EventMihomoUpdated    = "mihomo_updated"
	EventPanelUnreachable = "panel_unreachable"
	EventPanelRestored    = "panel_restored"
	EventGuardTriggered   = "guard_triggered"
	EventTrafficLimit     = "traffic_limit"
	EventNodeBlocked      = "node_blocked"
	EventNodeUnblocked    = "node_unblocked"
	EventHostsBlocked     = "hosts_blocked"
	EventBlockGuard       = "block_guard"
	EventError            = "error"
)

var EventTypes = []string{
	EventNodeDown,
	EventNodeUp,
	EventHostsDisabled,
	EventTrafficLimit,
	EventNodeBlocked,
	EventNodeUnblocked,
	EventHostsBlocked,
	EventHostsEnabled,
	EventMihomoUpdated,
	EventPanelUnreachable,
	EventPanelRestored,
	EventGuardTriggered,
	EventBlockGuard,
	EventError,
}

var MihomoTemplateTypes = []string{"MIHOMO", "STASH", "CLASH"}

type Panel struct {
	URL         string            `json:"url"`
	Token       string            `json:"token,omitempty"`
	Headers     map[string]string `json:"headers"`
	InsecureTLS bool              `json:"insecureTls"`
}

type Checker struct {
	Enabled                 bool     `json:"enabled"`
	IntervalSeconds         int      `json:"intervalSeconds"`
	DownMinutes             int      `json:"downMinutes"`
	RecoveryMinutes         int      `json:"recoveryMinutes"`
	Policy                  string   `json:"policy"`
	AutoEnable              bool     `json:"autoEnable"`
	TreatDisabledNodeAsDown bool     `json:"treatDisabledNodeAsDown"`
	DryRun                  bool     `json:"dryRun"`
	MassDownGuardPercent    int      `json:"massDownGuardPercent"`
	ExcludedNodes           []string `json:"excludedNodes"`
	ExcludedHosts           []string `json:"excludedHosts"`
}

// Traffic disables hosts of nodes that used up DisablePercent of the traffic
// limit set on the node in the panel.
type Traffic struct {
	Enabled        bool `json:"enabled"`
	DisablePercent int  `json:"disablePercent"`
}

// Block watches users online on nodes that stay connected to the panel. When
// they suddenly vanish, the node's address was most likely blocked for
// clients while the panel can still reach it.
type Block struct {
	Enabled bool `json:"enabled"`
	// A drop: online at or below this share of the usual level.
	DropPercent int `json:"dropPercent"`
	// Nodes with fewer users than this usually are not judged.
	MinUsers int `json:"minUsers"`
	// How long a drop must last before it is reported, and how long the
	// online must stay back before the block is considered gone.
	ConfirmMinutes int `json:"confirmMinutes"`
	// When at least this share of the watched nodes drops at once, it is a
	// common problem rather than blocks. 0 turns the guard off.
	GuardPercent int `json:"guardPercent"`
	// Hosts are only disabled when this is on, DisableMinutes after the report.
	DisableHosts   bool `json:"disableHosts"`
	DisableMinutes int  `json:"disableMinutes"`
}

type Mihomo struct {
	Enabled            bool     `json:"enabled"`
	TemplateTypes      []string `json:"templateTypes"`
	TemplateUUIDs      []string `json:"templateUuids"`
	CleanRules         bool     `json:"cleanRules"`
	EmptyGroupFallback string   `json:"emptyGroupFallback"`
}

type TelegramEvent struct {
	Enabled  bool   `json:"enabled"`
	Template string `json:"template"`
}

type Telegram struct {
	Enabled  bool                     `json:"enabled"`
	BotToken string                   `json:"botToken,omitempty"`
	ChatIDs  []string                 `json:"chatIds"`
	ThreadID string                   `json:"threadId"`
	APIURL   string                   `json:"apiUrl"`
	Proxy    string                   `json:"proxy"`
	Silent   bool                     `json:"silent"`
	Events   map[string]TelegramEvent `json:"events"`
}

type General struct {
	EventRetentionDays int `json:"eventRetentionDays"`
}

type Settings struct {
	Panel    Panel    `json:"panel"`
	Checker  Checker  `json:"checker"`
	Traffic  Traffic  `json:"traffic"`
	Block    Block    `json:"block"`
	Mihomo   Mihomo   `json:"mihomo"`
	Telegram Telegram `json:"telegram"`
	General  General  `json:"general"`
}

var DefaultTemplates = map[string]string{
	EventNodeDown:         "⚠️ <b>Нода недоступна</b>\n\n🖥 {node} <code>{node_address}</code>\n💬 {message}\n\nПривязанные хосты ({hosts_count}) будут отключены через {down_minutes} мин., если нода не поднимется.",
	EventNodeUp:           "✅ <b>Нода снова в сети</b>\n\n🖥 {node} <code>{node_address}</code>\n⏱ Простой: {downtime}",
	EventHostsDisabled:    "⛔ <b>Хосты отключены</b>{dry_run}\n\n{hosts}\n\nПричина: ноды недоступны дольше {down_minutes} мин.",
	EventHostsEnabled:     "♻️ <b>Хосты включены обратно</b>{dry_run}\n\n{hosts}",
	EventMihomoUpdated:    "🧩 <b>Шаблон Mihomo обновлён</b>\n\n📄 {template}\n🔀 {action}: {host}",
	EventPanelUnreachable: "🚨 <b>Панель Remnawave недоступна</b>\n\n{error}",
	EventPanelRestored:    "🟢 <b>Связь с панелью восстановлена</b>\n\nВерсия: {version}",
	EventGuardTriggered:   "🛡 <b>Сработала защита от массового отключения</b>\n\nНедоступно нод: {down_nodes} из {total_nodes} ({percent}%). Хосты не отключаются.",
	EventTrafficLimit:     "📊 <b>Хосты отключены по трафику</b>{dry_run}\n\n{hosts}\n\nПричина: ноды израсходовали {percent}% лимита трафика.",
	EventNodeBlocked:      "🚫 <b>Похоже, ноду заблокировали</b>\n\n🖥 {node} <code>{node_address}</code>\n👥 Онлайн: {online}, обычно {usual}\n\nНода на связи с панелью, но пользователи пропали.\n{action}",
	EventNodeUnblocked:    "✅ <b>Онлайн на ноде восстановился</b>\n\n🖥 {node} <code>{node_address}</code>\n👥 Онлайн: {online}, до падения {usual}\n⏱ Без пользователей: {duration}",
	EventHostsBlocked:     "🚫 <b>Хосты отключены: подозрение на блокировку</b>{dry_run}\n\n{hosts}\n\nПричина: онлайн на нодах упал и не вернулся за {disable_minutes} мин.",
	EventBlockGuard:       "🛡 <b>Онлайн упал сразу на {drop_nodes} из {total_nodes} нод</b>\n\nЭто похоже на общую проблему (отключение мобильного интернета, сбой статистики панели), а не на блокировку отдельных нод. Пока это продолжается, блокировки не фиксируются.",
	EventError:            "❗ <b>Ошибка</b>\n\n{error}",
}

func Default() Settings {
	events := make(map[string]TelegramEvent, len(EventTypes))
	for _, t := range EventTypes {
		events[t] = TelegramEvent{Enabled: t != EventMihomoUpdated, Template: DefaultTemplates[t]}
	}
	return Settings{
		Panel: Panel{Headers: map[string]string{}},
		Checker: Checker{
			Enabled:         true,
			IntervalSeconds: 30,
			DownMinutes:     5,
			RecoveryMinutes: 2,
			Policy:          PolicyAll,
			AutoEnable:      true,
			ExcludedNodes:   []string{},
			ExcludedHosts:   []string{},
		},
		Traffic: Traffic{DisablePercent: 95},
		Block: Block{
			DropPercent:    15,
			MinUsers:       20,
			ConfirmMinutes: 5,
			GuardPercent:   60,
			DisableMinutes: 30,
		},
		Mihomo: Mihomo{
			Enabled:            false,
			TemplateTypes:      []string{"MIHOMO"},
			TemplateUUIDs:      []string{},
			CleanRules:         true,
			EmptyGroupFallback: "DIRECT",
		},
		Telegram: Telegram{
			ChatIDs: []string{},
			APIURL:  "https://api.telegram.org",
			Events:  events,
		},
		General: General{EventRetentionDays: 30},
	}
}

// FillDefaults fills zero values that would otherwise make the service misbehave,
// e.g. after upgrading from a version that did not know a field yet.
func (s *Settings) FillDefaults() {
	d := Default()
	if s.Panel.Headers == nil {
		s.Panel.Headers = map[string]string{}
	}
	if s.Checker.IntervalSeconds == 0 {
		s.Checker.IntervalSeconds = d.Checker.IntervalSeconds
	}
	if s.Checker.DownMinutes == 0 {
		s.Checker.DownMinutes = d.Checker.DownMinutes
	}
	if s.Checker.Policy == "" {
		s.Checker.Policy = d.Checker.Policy
	}
	if s.Checker.ExcludedNodes == nil {
		s.Checker.ExcludedNodes = []string{}
	}
	if s.Checker.ExcludedHosts == nil {
		s.Checker.ExcludedHosts = []string{}
	}
	if s.Traffic.DisablePercent == 0 {
		s.Traffic.DisablePercent = d.Traffic.DisablePercent
	}
	if s.Block.DropPercent == 0 {
		s.Block.DropPercent = d.Block.DropPercent
	}
	if s.Block.MinUsers == 0 {
		s.Block.MinUsers = d.Block.MinUsers
	}
	if s.Block.ConfirmMinutes == 0 {
		s.Block.ConfirmMinutes = d.Block.ConfirmMinutes
	}
	if s.Block.DisableMinutes == 0 {
		s.Block.DisableMinutes = d.Block.DisableMinutes
	}
	if s.Mihomo.TemplateTypes == nil {
		s.Mihomo.TemplateTypes = d.Mihomo.TemplateTypes
	}
	if s.Mihomo.TemplateUUIDs == nil {
		s.Mihomo.TemplateUUIDs = []string{}
	}
	if s.Telegram.ChatIDs == nil {
		s.Telegram.ChatIDs = []string{}
	}
	if s.Telegram.APIURL == "" {
		s.Telegram.APIURL = d.Telegram.APIURL
	}
	if s.Telegram.Events == nil {
		s.Telegram.Events = map[string]TelegramEvent{}
	}
	for _, t := range EventTypes {
		ev, ok := s.Telegram.Events[t]
		if !ok {
			s.Telegram.Events[t] = d.Telegram.Events[t]
			continue
		}
		if strings.TrimSpace(ev.Template) == "" {
			ev.Template = DefaultTemplates[t]
			s.Telegram.Events[t] = ev
		}
	}
	if s.General.EventRetentionDays == 0 {
		s.General.EventRetentionDays = d.General.EventRetentionDays
	}
}

func (s *Settings) Validate() error {
	var errs []error
	c := s.Checker
	if c.IntervalSeconds < 10 || c.IntervalSeconds > 3600 {
		errs = append(errs, errors.New("checker.intervalSeconds must be between 10 and 3600"))
	}
	if c.DownMinutes < 1 || c.DownMinutes > 1440 {
		errs = append(errs, errors.New("checker.downMinutes must be between 1 and 1440"))
	}
	if c.RecoveryMinutes < 0 || c.RecoveryMinutes > 1440 {
		errs = append(errs, errors.New("checker.recoveryMinutes must be between 0 and 1440"))
	}
	if c.Policy != PolicyAll && c.Policy != PolicyAny {
		errs = append(errs, errors.New("checker.policy must be \"all\" or \"any\""))
	}
	if c.MassDownGuardPercent < 0 || c.MassDownGuardPercent > 100 {
		errs = append(errs, errors.New("checker.massDownGuardPercent must be between 0 and 100"))
	}
	if s.Traffic.DisablePercent < 1 || s.Traffic.DisablePercent > 100 {
		errs = append(errs, errors.New("traffic.disablePercent must be between 1 and 100"))
	}
	b := s.Block
	if b.DropPercent < 1 || b.DropPercent > 90 {
		errs = append(errs, errors.New("block.dropPercent must be between 1 and 90"))
	}
	if b.MinUsers < 1 || b.MinUsers > 100000 {
		errs = append(errs, errors.New("block.minUsers must be between 1 and 100000"))
	}
	if b.ConfirmMinutes < 1 || b.ConfirmMinutes > 120 {
		errs = append(errs, errors.New("block.confirmMinutes must be between 1 and 120"))
	}
	if b.GuardPercent < 0 || b.GuardPercent > 100 {
		errs = append(errs, errors.New("block.guardPercent must be between 0 and 100"))
	}
	if b.DisableMinutes < 1 || b.DisableMinutes > 1440 {
		errs = append(errs, errors.New("block.disableMinutes must be between 1 and 1440"))
	}
	for _, t := range s.Mihomo.TemplateTypes {
		if !slices.Contains(MihomoTemplateTypes, t) {
			errs = append(errs, fmt.Errorf("mihomo.templateTypes: unknown type %q", t))
		}
	}
	if s.Mihomo.Enabled && len(s.Mihomo.TemplateTypes) == 0 {
		errs = append(errs, errors.New("mihomo.templateTypes must not be empty"))
	}
	if s.Telegram.APIURL != "" {
		if u, err := url.Parse(s.Telegram.APIURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			errs = append(errs, errors.New("telegram.apiUrl must be an http(s) URL"))
		}
	}
	if s.Telegram.Proxy != "" {
		if u, err := url.Parse(s.Telegram.Proxy); err != nil || u.Host == "" {
			errs = append(errs, errors.New("telegram.proxy must be a URL like socks5://host:port"))
		}
	}
	if s.General.EventRetentionDays < 1 || s.General.EventRetentionDays > 3650 {
		errs = append(errs, errors.New("general.eventRetentionDays must be between 1 and 3650"))
	}
	return errors.Join(errs...)
}

// Normalize trims user input.
func (s *Settings) Normalize() {
	s.Panel.URL = strings.TrimSpace(s.Panel.URL)
	s.Panel.Token = strings.TrimSpace(s.Panel.Token)
	headers := make(map[string]string, len(s.Panel.Headers))
	for k, v := range s.Panel.Headers {
		if k = strings.TrimSpace(k); k != "" {
			headers[k] = strings.TrimSpace(v)
		}
	}
	s.Panel.Headers = headers
	s.Telegram.BotToken = strings.TrimSpace(s.Telegram.BotToken)
	s.Telegram.ThreadID = strings.TrimSpace(s.Telegram.ThreadID)
	s.Telegram.APIURL = strings.TrimRight(strings.TrimSpace(s.Telegram.APIURL), "/")
	s.Telegram.Proxy = strings.TrimSpace(s.Telegram.Proxy)
	s.Telegram.ChatIDs = cleanList(s.Telegram.ChatIDs)
	s.Checker.ExcludedNodes = cleanList(s.Checker.ExcludedNodes)
	s.Checker.ExcludedHosts = cleanList(s.Checker.ExcludedHosts)
	s.Mihomo.TemplateUUIDs = cleanList(s.Mihomo.TemplateUUIDs)
	s.Mihomo.EmptyGroupFallback = strings.TrimSpace(s.Mihomo.EmptyGroupFallback)
}

func cleanList(in []string) []string {
	out := make([]string, 0, len(in))
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v != "" && !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}

func (s Settings) PanelConfigured() bool {
	return s.Panel.URL != "" && s.Panel.Token != ""
}
