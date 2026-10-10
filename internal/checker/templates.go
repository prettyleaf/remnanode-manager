package checker

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/prettyleaf/remnanode-manager/internal/mihomo"
	"github.com/prettyleaf/remnanode-manager/internal/notify"
	"github.com/prettyleaf/remnanode-manager/internal/remnawave"
	"github.com/prettyleaf/remnanode-manager/internal/settings"
	"github.com/prettyleaf/remnanode-manager/internal/store"
)

func (c *Checker) selectedTemplates(ctx context.Context, client *remnawave.Client, cfg settings.Mihomo) ([]remnawave.SubscriptionTemplate, error) {
	all, err := client.Templates(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]remnawave.SubscriptionTemplate, 0, len(all))
	for _, t := range all {
		if !slices.Contains(cfg.TemplateTypes, t.TemplateType) {
			continue
		}
		if len(cfg.TemplateUUIDs) > 0 && !slices.Contains(cfg.TemplateUUIDs, t.UUID) {
			continue
		}
		out = append(out, t)
	}
	return out, nil
}

// removeFromTemplates strips references to the host from the selected
// templates and returns what was changed so it can be undone later.
func (c *Checker) removeFromTemplates(ctx context.Context, client *remnawave.Client, cfg settings.Settings, host remnawave.Host, all []remnawave.Host, disabling map[string]bool) []store.TemplatePatch {
	patches := []store.TemplatePatch{}

	// Another enabled host with the same remark keeps the reference valid.
	for _, other := range all {
		if other.UUID != host.UUID && other.Remark == host.Remark && !other.IsDisabled && !disabling[other.UUID] {
			c.event(ctx, store.Event{Level: "info", Type: "mihomo_skipped", HostUUID: host.UUID,
				Message: fmt.Sprintf("Templates left untouched for %q: another enabled host has the same remark", host.Remark),
				Meta:    map[string]any{"host": host.Remark, "reason": "duplicate_remark"}})
			return patches
		}
	}

	templates, err := c.selectedTemplates(ctx, client, cfg.Mihomo)
	if err != nil {
		c.failure(ctx, "Failed to load subscription templates", err)
		return patches
	}
	opts := mihomo.Options{CleanRules: cfg.Mihomo.CleanRules, EmptyGroupFallback: cfg.Mihomo.EmptyGroupFallback}
	for _, t := range templates {
		_, doc, err := client.TemplateYAML(ctx, t.UUID)
		if err != nil {
			c.failure(ctx, fmt.Sprintf("Failed to read template %q", t.Name), err)
			continue
		}
		out, patch, err := mihomo.Remove(doc, host.Remark, opts)
		if err != nil {
			c.failure(ctx, fmt.Sprintf("Failed to patch template %q for host %q", t.Name, host.Remark), err)
			continue
		}
		if patch.Empty() {
			continue
		}
		if err := client.UpdateTemplateYAML(ctx, t.UUID, out); err != nil {
			c.failure(ctx, fmt.Sprintf("Failed to save template %q", t.Name), err)
			continue
		}
		patches = append(patches, store.TemplatePatch{TemplateUUID: t.UUID, TemplateName: t.Name, Patch: patch})
		c.templateEvent(ctx, "mihomo_removed", host.Remark, host.UUID, t.Name, patch)
	}
	return patches
}

// restoreTemplates undoes the template changes made for a managed host.
func (c *Checker) restoreTemplates(ctx context.Context, client *remnawave.Client, m *store.ManagedHost) {
	for _, tp := range m.Mihomo {
		_, doc, err := client.TemplateYAML(ctx, tp.TemplateUUID)
		if remnawave.IsNotFound(err) {
			continue
		}
		if err != nil {
			c.failure(ctx, fmt.Sprintf("Failed to read template %q", tp.TemplateName), err)
			continue
		}
		out, changed, err := mihomo.Restore(doc, m.Remark, tp.Patch)
		if err != nil {
			c.failure(ctx, fmt.Sprintf("Failed to restore template %q for host %q", tp.TemplateName, m.Remark), err)
			continue
		}
		if !changed {
			continue
		}
		if err := client.UpdateTemplateYAML(ctx, tp.TemplateUUID, out); err != nil {
			c.failure(ctx, fmt.Sprintf("Failed to save template %q", tp.TemplateName), err)
			continue
		}
		c.templateEvent(ctx, "mihomo_restored", m.Remark, m.UUID, tp.TemplateName, tp.Patch)
	}
}

func (c *Checker) templateEvent(ctx context.Context, typ, remark, hostUUID, template string, patch mihomo.Patch) {
	groups := make([]string, 0, len(patch.Groups))
	for _, g := range patch.Groups {
		if !slices.Contains(groups, g.Group) {
			groups = append(groups, g.Group)
		}
	}
	rules := make([]string, 0, len(patch.Rules))
	for _, r := range patch.Rules {
		rules = append(rules, r.Rule)
	}
	action, verb := "removed from", "Удалён"
	if typ == "mihomo_restored" {
		action, verb = "restored in", "Возвращён"
	}
	details := []string{}
	if len(groups) > 0 {
		details = append(details, "groups: "+strings.Join(groups, ", "))
	}
	if len(rules) > 0 {
		details = append(details, fmt.Sprintf("rules: %d", len(rules)))
	}
	c.event(ctx, store.Event{Level: "info", Type: typ, HostUUID: hostUUID,
		Message: fmt.Sprintf("Host %q %s template %q (%s)", remark, action, template, strings.Join(details, "; ")),
		Meta:    map[string]any{"host": remark, "template": template, "groups": groups, "rules": rules}})
	c.tg.Notify(notify.Message{Type: settings.EventMihomoUpdated, Text: map[string]string{
		"template": template, "action": verb, "host": remark,
	}})
}

// TemplateReferences reports which selected templates reference the remark.
func (c *Checker) TemplateReferences(ctx context.Context, remark string) ([]TemplateRef, error) {
	cfg := c.store.Settings()
	client, err := c.client(cfg)
	if err != nil {
		return nil, err
	}
	all, err := client.Templates(ctx)
	if err != nil {
		return nil, err
	}
	out := []TemplateRef{}
	for _, t := range all {
		if !slices.Contains(settings.MihomoTemplateTypes, t.TemplateType) {
			continue
		}
		_, doc, err := client.TemplateYAML(ctx, t.UUID)
		if err != nil {
			return nil, err
		}
		groups, rules, err := mihomo.References(doc, remark)
		if err != nil {
			out = append(out, TemplateRef{UUID: t.UUID, Name: t.Name, Type: t.TemplateType, Error: err.Error()})
			continue
		}
		if len(groups) > 0 || len(rules) > 0 {
			out = append(out, TemplateRef{UUID: t.UUID, Name: t.Name, Type: t.TemplateType, Groups: groups, Rules: rules})
		}
	}
	return out, nil
}

type TemplateRef struct {
	UUID   string   `json:"uuid"`
	Name   string   `json:"name"`
	Type   string   `json:"type"`
	Groups []string `json:"groups"`
	Rules  []string `json:"rules"`
	Error  string   `json:"error,omitempty"`
}

// Templates lists Clash-family templates for the settings page.
func (c *Checker) Templates(ctx context.Context) ([]remnawave.SubscriptionTemplate, error) {
	client, err := c.client(c.store.Settings())
	if err != nil {
		return nil, err
	}
	all, err := client.Templates(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]remnawave.SubscriptionTemplate, 0, len(all))
	for _, t := range all {
		if slices.Contains(settings.MihomoTemplateTypes, t.TemplateType) {
			t.EncodedTemplateYAML = nil
			out = append(out, t)
		}
	}
	return out, nil
}
