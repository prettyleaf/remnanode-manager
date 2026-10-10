package mihomo

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const template = `mixed-port: 7890
mode: rule
proxies: # LEAVE THIS LINE!

proxy-groups:
  - name: '→ Remnawave'
    type: 'select'
    proxies: # LEAVE THIS LINE!

  - name: Germany
    type: url-test
    remnawave:
      include-proxies: false
    proxies:
      - 🇩🇪 DE-1
      - 🇩🇪 DE-2

  - name: Only-NL
    type: select
    remnawave:
      include-proxies: false
    proxies: ['🇳🇱 NL']

  - name: Streaming
    type: select
    proxies:
      - Germany
      - 🇳🇱 NL
      - DIRECT

rules:
  # streaming goes through NL
  - DOMAIN-SUFFIX,netflix.com,🇳🇱 NL
  - AND,((DOMAIN,example.com),(NETWORK,UDP)),🇳🇱 NL
  - GEOIP,RU,DIRECT,no-resolve
  - MATCH,→ Remnawave
`

func decode(t *testing.T, doc string) map[string]any {
	t.Helper()
	var out map[string]any
	if err := yaml.Unmarshal([]byte(doc), &out); err != nil {
		t.Fatalf("result is not valid YAML: %v\n%s", err, doc)
	}
	return out
}

func proxiesOf(t *testing.T, doc, name string) []any {
	t.Helper()
	for _, g := range decode(t, doc)["proxy-groups"].([]any) {
		gm := g.(map[string]any)
		if gm["name"] == name {
			p, _ := gm["proxies"].([]any)
			return p
		}
	}
	t.Fatalf("group %q not found", name)
	return nil
}

func TestRemoveAndRestoreRoundTrip(t *testing.T) {
	opts := Options{CleanRules: true, EmptyGroupFallback: "DIRECT"}

	out, patch, err := Remove(template, "🇳🇱 NL", opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(patch.Groups) != 2 || len(patch.Rules) != 2 {
		t.Fatalf("unexpected patch: %+v", patch)
	}
	if got := proxiesOf(t, out, "Only-NL"); len(got) != 1 || got[0] != "DIRECT" {
		t.Fatalf("Only-NL should fall back to DIRECT, got %v", got)
	}
	if got := proxiesOf(t, out, "Streaming"); len(got) != 2 || got[0] != "Germany" || got[1] != "DIRECT" {
		t.Fatalf("Streaming not cleaned: %v", got)
	}
	rules := decode(t, out)["rules"].([]any)
	if len(rules) != 2 {
		t.Fatalf("rules not cleaned: %v", rules)
	}
	if !strings.Contains(out, "# streaming goes through NL") && !strings.Contains(out, "streaming goes through NL") {
		t.Logf("comment dropped (acceptable but unexpected):\n%s", out)
	}

	restored, changed, err := Restore(out, "🇳🇱 NL", patch)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("restore reported no change")
	}
	if got := proxiesOf(t, restored, "Only-NL"); len(got) != 1 || got[0] != "🇳🇱 NL" {
		t.Fatalf("Only-NL not restored: %v", got)
	}
	if got := proxiesOf(t, restored, "Streaming"); len(got) != 3 || got[1] != "🇳🇱 NL" {
		t.Fatalf("Streaming not restored in place: %v", got)
	}
	rules = decode(t, restored)["rules"].([]any)
	if len(rules) != 4 || rules[0] != "DOMAIN-SUFFIX,netflix.com,🇳🇱 NL" || rules[1] != "AND,((DOMAIN,example.com),(NETWORK,UDP)),🇳🇱 NL" {
		t.Fatalf("rules not restored in order: %v", rules)
	}

	// A second restore must be a no-op.
	if _, changed, _ := Restore(restored, "🇳🇱 NL", patch); changed {
		t.Fatal("second restore changed the document")
	}
}

func TestRemoveKeepsGroupsThatRemnawaveFills(t *testing.T) {
	doc := `proxy-groups:
  - name: Auto
    type: url-test
    proxies:
      - A
`
	out, patch, err := Remove(doc, "A", Options{EmptyGroupFallback: "DIRECT"})
	if err != nil {
		t.Fatal(err)
	}
	if len(patch.Groups) != 1 || patch.Groups[0].Fallback != "" {
		t.Fatalf("fallback must not be used when remnawave appends hosts: %+v", patch)
	}
	if got := proxiesOf(t, out, "Auto"); len(got) != 0 {
		t.Fatalf("expected empty proxies, got %v", got)
	}
}

func TestRemoveNoMatchReturnsOriginal(t *testing.T) {
	out, patch, err := Remove(template, "missing", Options{CleanRules: true})
	if err != nil {
		t.Fatal(err)
	}
	if !patch.Empty() || out != template {
		t.Fatal("document must stay untouched when nothing matches")
	}
}

func TestRestoreAfterTemplateEdit(t *testing.T) {
	opts := Options{CleanRules: true}
	out, patch, err := Remove(template, "🇩🇪 DE-1", opts)
	if err != nil {
		t.Fatal(err)
	}
	// Admin added a new proxy to the group in the meantime.
	edited := strings.Replace(out, "      - 🇩🇪 DE-2", "      - 🇩🇪 DE-3\n      - 🇩🇪 DE-2", 1)
	restored, changed, err := Restore(edited, "🇩🇪 DE-1", patch)
	if err != nil || !changed {
		t.Fatalf("restore failed: %v %v", err, changed)
	}
	got := proxiesOf(t, restored, "Germany")
	if len(got) != 3 || got[0] != "🇩🇪 DE-1" {
		t.Fatalf("unexpected order: %v", got)
	}
}

func TestRuleTarget(t *testing.T) {
	cases := map[string]string{
		"DOMAIN-SUFFIX,google.com,Proxy":           "Proxy",
		"GEOIP,RU,DIRECT,no-resolve":               "DIRECT",
		"MATCH,Final":                              "Final",
		" MATCH , Final ":                          "Final",
		"AND,((DOMAIN,a.com),(NETWORK,UDP)),Group": "Group",
		"SUB-RULE,(NETWORK,tcp),sub":               "",
		"RULE-SET,ads,REJECT":                      "REJECT",
		"IP-CIDR,10.0.0.0/8,🇳🇱 NL,no-resolve":      "🇳🇱 NL",
	}
	for rule, want := range cases {
		if got := RuleTarget(rule); got != want {
			t.Errorf("RuleTarget(%q) = %q, want %q", rule, got, want)
		}
	}
}

func TestReferences(t *testing.T) {
	groups, rules, err := References(template, "🇳🇱 NL")
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 2 || len(rules) != 2 {
		t.Fatalf("groups=%v rules=%v", groups, rules)
	}
}

func TestFormattingIsPreserved(t *testing.T) {
	out, patch, err := Remove(template, "🇩🇪 DE-1", Options{CleanRules: true})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(template, "      - 🇩🇪 DE-1\n", "", 1)
	if out != want {
		t.Fatalf("unexpected diff:\n%s", out)
	}
	restored, _, err := Restore(out, "🇩🇪 DE-1", patch)
	if err != nil {
		t.Fatal(err)
	}
	if restored != template {
		t.Fatalf("restore is not byte-exact:\n%s", restored)
	}
}

func TestFlowAndFallbackRoundTripIsExact(t *testing.T) {
	opts := Options{CleanRules: true, EmptyGroupFallback: "DIRECT"}
	out, patch, err := Remove(template, "🇳🇱 NL", opts)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "proxies: [DIRECT]") {
		t.Fatalf("flow fallback missing:\n%s", out)
	}
	if !strings.Contains(out, "  # streaming goes through NL\n") || !strings.Contains(out, "\n\n  - name: Germany") {
		t.Fatalf("comments or blank lines lost:\n%s", out)
	}
	restored, _, err := Restore(out, "🇳🇱 NL", patch)
	if err != nil {
		t.Fatal(err)
	}
	if restored != template {
		t.Fatalf("restore is not byte-exact:\n%s", restored)
	}
}

func TestBlockGroupEmptiedWithFallback(t *testing.T) {
	doc := "proxy-groups:\n  - name: G\n    remnawave:\n      include-proxies: false\n    proxies:\n      - \"A\" # main\n\nrules:\n  - MATCH,G\n"
	out, patch, err := Remove(doc, "A", Options{EmptyGroupFallback: "DIRECT"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "    proxies:\n      - DIRECT\n") {
		t.Fatalf("fallback not inserted as block item:\n%s", out)
	}
	restored, _, err := Restore(out, "A", patch)
	if err != nil {
		t.Fatal(err)
	}
	if restored != doc {
		t.Fatalf("restore is not byte-exact:\n%q", restored)
	}
}

func TestBlockGroupEmptiedWithoutFallback(t *testing.T) {
	doc := "proxy-groups:\n  - name: G\n    proxies:\n      - A\nrules:\n  - MATCH,G"
	out, patch, err := Remove(doc, "A", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if out != "proxy-groups:\n  - name: G\n    proxies:\nrules:\n  - MATCH,G" {
		t.Fatalf("unexpected output:\n%q", out)
	}
	restored, _, err := Restore(out, "A", patch)
	if err != nil {
		t.Fatal(err)
	}
	if restored != doc {
		t.Fatalf("restore is not byte-exact:\n%q", restored)
	}
}

func TestCRLF(t *testing.T) {
	doc := strings.ReplaceAll(template, "\n", "\r\n")
	out, patch, err := Remove(doc, "🇩🇪 DE-2", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ReplaceAll(out, "\r\n", ""), "\n") {
		t.Fatal("mixed line endings")
	}
	restored, _, err := Restore(out, "🇩🇪 DE-2", patch)
	if err != nil {
		t.Fatal(err)
	}
	if restored != doc {
		t.Fatalf("restore is not byte-exact")
	}
}

func TestRestoreIntoTemplateWithoutGroup(t *testing.T) {
	_, patch, err := Remove(template, "🇩🇪 DE-1", Options{})
	if err != nil {
		t.Fatal(err)
	}
	doc := "proxy-groups:\n  - name: Other\n    proxies:\n      - X\n"
	out, changed, err := Restore(doc, "🇩🇪 DE-1", patch)
	if err != nil || changed || out != doc {
		t.Fatalf("restore must be a no-op when the group is gone: %v %v", err, changed)
	}
}
