// Package mihomo removes host remarks from Mihomo/Clash/Stash subscription
// templates and puts them back later.
//
// Remnawave drops disabled hosts from generated subscriptions, but templates
// often reference host remarks by hand (proxy-groups with
// `remnawave: {include-proxies: false}` or rules pointing at a specific proxy).
// Mihomo refuses to load a config that references a missing proxy, so those
// references are removed while the host is disabled and restored afterwards.
//
// Edits are applied to the template text line by line so that the admin's
// formatting, comments and emoji survive. Every edit is cross-checked against
// the same change applied to a parsed YAML tree; on mismatch nothing is
// returned, so a broken template is never pushed to the panel.
package mihomo

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

type GroupChange struct {
	Group string `json:"group"`
	Index int    `json:"index"`
	Prev  string `json:"prev,omitempty"`
	// Raw is the original line of a block-style item, re-inserted verbatim.
	Raw   string `json:"raw,omitempty"`
	Style string `json:"style,omitempty"`
	// Fallback is set when the group became empty and Fallback was inserted
	// to keep the config valid.
	Fallback string `json:"fallback,omitempty"`
}

type RuleChange struct {
	Index int    `json:"index"`
	Rule  string `json:"rule"`
	Prev  string `json:"prev,omitempty"`
	Raw   string `json:"raw,omitempty"`
	Style string `json:"style,omitempty"`
}

type Patch struct {
	Groups []GroupChange `json:"groups,omitempty"`
	Rules  []RuleChange  `json:"rules,omitempty"`
}

func (p Patch) Empty() bool { return len(p.Groups) == 0 && len(p.Rules) == 0 }

type Options struct {
	CleanRules bool
	// EmptyGroupFallback is inserted into a group that would otherwise be left
	// without proxies. Empty string disables the fallback.
	EmptyGroupFallback string
}

var ErrVerify = errors.New("template edit verification failed")

// Remove strips every reference to remark from proxy-groups (and rules when
// enabled). It returns the original document untouched when nothing matched.
func Remove(doc, remark string, opts Options) (string, Patch, error) {
	root, top, err := parse(doc)
	if err != nil || top == nil {
		return doc, Patch{}, err
	}
	patch := removeTree(top, remark, opts)
	if patch.Empty() {
		return doc, patch, nil
	}
	expected, err := decodeTree(root)
	if err != nil {
		return doc, Patch{}, err
	}

	out := doc
	for i := range patch.Groups {
		ch := &patch.Groups[i]
		ref, err := locate(out, func(top *yaml.Node) *seqRef { return groupProxies(top, ch.Group) })
		if err != nil {
			return doc, Patch{}, err
		}
		idx := indexOf(ref.seq, remark)
		if idx < 0 {
			return doc, Patch{}, fmt.Errorf("%w: %q not found in group %q", ErrVerify, remark, ch.Group)
		}
		ch.Style = styleName(ref.seq.Content[idx])
		if out, ch.Raw, err = deleteItem(out, ref, idx); err != nil {
			return doc, Patch{}, err
		}
		if ch.Fallback != "" {
			ref, err := locate(out, func(top *yaml.Node) *seqRef { return groupProxies(top, ch.Group) })
			if err != nil {
				return doc, Patch{}, err
			}
			raw := ""
			if ch.Raw != "" {
				raw = indentOf(ch.Raw) + "- " + ch.Fallback
			}
			if out, err = insertItem(out, ref, 0, ch.Fallback, "plain", raw); err != nil {
				return doc, Patch{}, err
			}
		}
	}
	for i := range patch.Rules {
		ch := &patch.Rules[i]
		ref, err := locate(out, rulesSeq)
		if err != nil {
			return doc, Patch{}, err
		}
		idx := indexOf(ref.seq, ch.Rule)
		if idx < 0 {
			return doc, Patch{}, fmt.Errorf("%w: rule %q not found", ErrVerify, ch.Rule)
		}
		ch.Style = styleName(ref.seq.Content[idx])
		if out, ch.Raw, err = deleteItem(out, ref, idx); err != nil {
			return doc, Patch{}, err
		}
	}

	if err := verify(out, expected); err != nil {
		return doc, Patch{}, err
	}
	return out, patch, nil
}

// Restore re-inserts references removed by Remove. Entries that are already
// present (for example re-added by an admin) are skipped.
func Restore(doc, remark string, patch Patch) (string, bool, error) {
	root, top, err := parse(doc)
	if err != nil || top == nil {
		return doc, false, err
	}
	if !restoreTree(top, remark, patch) {
		return doc, false, nil
	}
	expected, err := decodeTree(root)
	if err != nil {
		return doc, false, err
	}

	out := doc
	// Changes were recorded while removing items one after another, so they
	// are undone in reverse order to land at the original positions.
	for _, ch := range slices.Backward(patch.Groups) {
		ref, err := locate(out, func(top *yaml.Node) *seqRef { return groupProxies(top, ch.Group) })
		if errors.Is(err, errNotFound) {
			continue
		}
		if err != nil {
			return doc, false, err
		}
		if ch.Fallback != "" && len(ref.seq.Content) == 1 && scalar(ref.seq.Content[0]) == ch.Fallback {
			if out, _, err = deleteItem(out, ref, 0); err != nil {
				return doc, false, err
			}
			if ref, err = locate(out, func(top *yaml.Node) *seqRef { return groupProxies(top, ch.Group) }); err != nil {
				return doc, false, err
			}
		}
		if indexOf(ref.seq, remark) >= 0 {
			continue
		}
		pos := restorePosition(ref.seq, ch.Prev, ch.Index)
		if out, err = insertItem(out, ref, pos, remark, ch.Style, ch.Raw); err != nil {
			return doc, false, err
		}
	}
	for _, ch := range slices.Backward(patch.Rules) {
		ref, err := locate(out, rulesSeq)
		if errors.Is(err, errNotFound) {
			break
		}
		if err != nil {
			return doc, false, err
		}
		if indexOf(ref.seq, ch.Rule) >= 0 {
			continue
		}
		pos := restorePosition(ref.seq, ch.Prev, ch.Index)
		if out, err = insertItem(out, ref, pos, ch.Rule, ch.Style, ch.Raw); err != nil {
			return doc, false, err
		}
	}

	if err := verify(out, expected); err != nil {
		return doc, false, err
	}
	return out, true, nil
}

// References lists proxy-group names and rules that point at remark.
func References(doc, remark string) (groups []string, rules []string, err error) {
	_, top, err := parse(doc)
	if err != nil || top == nil {
		return nil, nil, err
	}
	for _, group := range seqItems(mapGet(top, "proxy-groups")) {
		if group.Kind == yaml.MappingNode && indexOf(mapGet(group, "proxies"), remark) >= 0 {
			groups = append(groups, scalar(mapGet(group, "name")))
		}
	}
	for _, item := range seqItems(mapGet(top, "rules")) {
		if item.Kind == yaml.ScalarNode && RuleTarget(item.Value) == remark {
			rules = append(rules, item.Value)
		}
	}
	return groups, rules, nil
}

// RuleTarget extracts the policy (proxy or group name) a rule routes to.
// Commas inside parentheses (logical rules) are not treated as separators.
func RuleTarget(rule string) string {
	fields := splitTopLevel(rule)
	if len(fields) == 0 {
		return ""
	}
	switch strings.ToUpper(fields[0]) {
	case "MATCH":
		if len(fields) > 1 {
			return fields[1]
		}
		return ""
	case "SUB-RULE":
		return ""
	}
	if len(fields) > 2 {
		return fields[2]
	}
	return ""
}

func splitTopLevel(s string) []string {
	var out []string
	depth, start := 0, 0
	for i, r := range s {
		switch r {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 {
				out = append(out, strings.TrimSpace(s[start:i]))
				start = i + 1
			}
		}
	}
	return append(out, strings.TrimSpace(s[start:]))
}

// ---- tree layer (source of truth for verification) ----

func removeTree(top *yaml.Node, remark string, opts Options) Patch {
	var patch Patch
	for _, group := range seqItems(mapGet(top, "proxy-groups")) {
		if group.Kind != yaml.MappingNode {
			continue
		}
		name := scalar(mapGet(group, "name"))
		proxies := mapGet(group, "proxies")
		if name == "" || proxies == nil || proxies.Kind != yaml.SequenceNode {
			continue
		}
		var changes []GroupChange
		for i := 0; i < len(proxies.Content); {
			item := proxies.Content[i]
			if item.Kind == yaml.ScalarNode && item.Value == remark {
				changes = append(changes, GroupChange{Group: name, Index: i, Prev: prevValue(proxies, i)})
				proxies.Content = slices.Delete(proxies.Content, i, i+1)
				continue
			}
			i++
		}
		if len(changes) == 0 {
			continue
		}
		if len(proxies.Content) == 0 && opts.EmptyGroupFallback != "" && needsFallback(group) {
			proxies.Content = append(proxies.Content, strNode(opts.EmptyGroupFallback))
			changes[len(changes)-1].Fallback = opts.EmptyGroupFallback
		}
		patch.Groups = append(patch.Groups, changes...)
	}

	if opts.CleanRules {
		if rules := mapGet(top, "rules"); rules != nil && rules.Kind == yaml.SequenceNode {
			for i := 0; i < len(rules.Content); {
				item := rules.Content[i]
				if item.Kind == yaml.ScalarNode && RuleTarget(item.Value) == remark {
					patch.Rules = append(patch.Rules, RuleChange{Index: i, Rule: item.Value, Prev: prevValue(rules, i)})
					rules.Content = slices.Delete(rules.Content, i, i+1)
					continue
				}
				i++
			}
		}
	}
	return patch
}

func restoreTree(top *yaml.Node, remark string, patch Patch) bool {
	changed := false
	groups := seqItems(mapGet(top, "proxy-groups"))
	for _, ch := range slices.Backward(patch.Groups) {
		group := findGroup(groups, ch.Group)
		if group == nil {
			continue
		}
		proxies := mapGet(group, "proxies")
		if proxies == nil || proxies.Kind != yaml.SequenceNode {
			if !isNull(proxies) {
				continue
			}
			// "proxies:" left without items decodes as null.
			proxies = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
			mapSet(group, "proxies", proxies)
		}
		if ch.Fallback != "" && len(proxies.Content) == 1 && scalar(proxies.Content[0]) == ch.Fallback {
			proxies.Content = proxies.Content[:0]
			changed = true
		}
		if indexOf(proxies, remark) >= 0 {
			continue
		}
		insertNode(proxies, restorePosition(proxies, ch.Prev, ch.Index), strNode(remark))
		changed = true
	}

	if len(patch.Rules) > 0 {
		rules := mapGet(top, "rules")
		if isNull(rules) {
			rules = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
			mapSet(top, "rules", rules)
		}
		if rules != nil && rules.Kind == yaml.SequenceNode {
			for _, ch := range slices.Backward(patch.Rules) {
				if indexOf(rules, ch.Rule) >= 0 {
					continue
				}
				insertNode(rules, restorePosition(rules, ch.Prev, ch.Index), strNode(ch.Rule))
				changed = true
			}
		}
	}
	return changed
}

// ---- text layer ----

var errNotFound = errors.New("sequence not found")

type seqRef struct {
	key *yaml.Node
	seq *yaml.Node // SequenceNode or a null scalar for "key:" without items
}

func groupProxies(top *yaml.Node, name string) *seqRef {
	group := findGroup(seqItems(mapGet(top, "proxy-groups")), name)
	if group == nil {
		return nil
	}
	return refFor(group, "proxies")
}

func rulesSeq(top *yaml.Node) *seqRef { return refFor(top, "rules") }

func refFor(m *yaml.Node, key string) *seqRef {
	k, v := mapEntry(m, key)
	if k == nil || (v.Kind != yaml.SequenceNode && !isNull(v)) {
		return nil
	}
	return &seqRef{key: k, seq: v}
}

func locate(doc string, find func(top *yaml.Node) *seqRef) (*seqRef, error) {
	_, top, err := parse(doc)
	if err != nil {
		return nil, err
	}
	if top == nil {
		return nil, errNotFound
	}
	ref := find(top)
	if ref == nil {
		return nil, errNotFound
	}
	return ref, nil
}

func deleteItem(doc string, ref *seqRef, idx int) (string, string, error) {
	seq := ref.seq
	if seq.Kind != yaml.SequenceNode || idx >= len(seq.Content) {
		return doc, "", fmt.Errorf("%w: item %d is out of range", ErrVerify, idx)
	}
	if seq.Style&yaml.FlowStyle != 0 {
		items := slices.Delete(slices.Clone(seq.Content), idx, idx+1)
		out, err := rewriteFlow(doc, seq, items)
		return out, "", err
	}

	item := seq.Content[idx]
	lines := strings.SplitAfter(doc, "\n")
	ln := item.Line - 1
	if ln < 0 || ln >= len(lines) {
		return doc, "", fmt.Errorf("%w: bad line for item %q", ErrVerify, item.Value)
	}
	if (idx > 0 && seq.Content[idx-1].Line == item.Line) ||
		(idx+1 < len(seq.Content) && seq.Content[idx+1].Line == item.Line) {
		return doc, "", fmt.Errorf("unsupported template formatting near line %d", item.Line)
	}
	dash := strings.TrimLeft(lines[ln], " \t")
	if !strings.HasPrefix(dash, "-") {
		return doc, "", fmt.Errorf("unsupported template formatting near line %d", item.Line)
	}
	// Reject multi-line scalars: the next line must not continue this item.
	if ln+1 < len(lines) {
		next := lines[ln+1]
		trimmed := strings.TrimSpace(next)
		itemIndent := len(lines[ln]) - len(dash)
		nextIndent := len(next) - len(strings.TrimLeft(next, " \t"))
		if trimmed != "" && !strings.HasPrefix(trimmed, "#") && nextIndent > itemIndent && !strings.HasPrefix(trimmed, "-") {
			return doc, "", fmt.Errorf("unsupported multi-line item near line %d", item.Line)
		}
	}
	raw := lines[ln]
	lines = slices.Delete(lines, ln, ln+1)
	return strings.Join(lines, ""), raw, nil
}

func insertItem(doc string, ref *seqRef, idx int, value, style, raw string) (string, error) {
	seq := ref.seq
	if seq.Kind == yaml.SequenceNode && seq.Style&yaml.FlowStyle != 0 {
		items := slices.Clone(seq.Content)
		node := strNode(value)
		node.Style = styleFromName(style)
		items = slices.Insert(items, max(0, min(idx, len(items))), node)
		return rewriteFlow(doc, seq, items)
	}

	lines := strings.SplitAfter(doc, "\n")
	nl := lineEnding(lines)
	var at int
	var indent string

	switch {
	case seq.Kind == yaml.SequenceNode && len(seq.Content) > 0:
		if idx < len(seq.Content) {
			at = seq.Content[idx].Line - 1
		} else {
			at = seq.Content[len(seq.Content)-1].Line
		}
		sibling := seq.Content[min(idx, len(seq.Content)-1)]
		indent = indentOf(lines[sibling.Line-1])
	case isNull(seq):
		if seq.Value != "" && seq.Line == ref.key.Line {
			return doc, fmt.Errorf("unsupported template formatting near line %d", seq.Line)
		}
		at = ref.key.Line
		indent = strings.Repeat(" ", ref.key.Column-1+2)
		if r := indentOf(raw); len(r) > ref.key.Column-1 {
			indent = r
		}
	default:
		return doc, fmt.Errorf("unsupported template formatting near line %d", seq.Line)
	}

	line := raw
	if line == "" || indentOf(line) != indent {
		line = indent + "- " + renderScalar(value, style, false)
	}
	line = strings.TrimRight(line, "\r\n") + nl

	if at > len(lines) {
		at = len(lines)
	}
	// Appending after a final line without a newline needs one first.
	if at > 0 && at <= len(lines) && !strings.HasSuffix(lines[at-1], "\n") {
		lines[at-1] += nl
	}
	lines = slices.Insert(lines, at, line)
	return strings.Join(lines, ""), nil
}

func rewriteFlow(doc string, seq *yaml.Node, items []*yaml.Node) (string, error) {
	start, err := offsetOf(doc, seq.Line, seq.Column)
	if err != nil {
		return doc, err
	}
	if start >= len(doc) || doc[start] != '[' {
		return doc, fmt.Errorf("unsupported template formatting near line %d", seq.Line)
	}
	end, err := flowEnd(doc, start)
	if err != nil {
		return doc, err
	}
	parts := make([]string, 0, len(items))
	for _, it := range items {
		if it.Kind != yaml.ScalarNode {
			return doc, fmt.Errorf("unsupported template formatting near line %d", seq.Line)
		}
		parts = append(parts, renderScalar(it.Value, styleName(it), true))
	}
	return doc[:start] + "[" + strings.Join(parts, ", ") + "]" + doc[end+1:], nil
}

// offsetOf converts a 1-based line and rune column to a byte offset.
func offsetOf(doc string, line, column int) (int, error) {
	off := 0
	for l := 1; l < line; l++ {
		i := strings.IndexByte(doc[off:], '\n')
		if i < 0 {
			return 0, fmt.Errorf("%w: line %d out of range", ErrVerify, line)
		}
		off += i + 1
	}
	for c := 1; c < column && off < len(doc); c++ {
		_, size := utf8.DecodeRuneInString(doc[off:])
		off += size
	}
	return off, nil
}

func flowEnd(doc string, start int) (int, error) {
	depth := 0
	for i := start; i < len(doc); i++ {
		switch c := doc[i]; c {
		case '\'':
			for i++; i < len(doc); i++ {
				if doc[i] == '\'' {
					if i+1 < len(doc) && doc[i+1] == '\'' {
						i++
						continue
					}
					break
				}
			}
		case '"':
			for i++; i < len(doc); i++ {
				if doc[i] == '\\' {
					i++
					continue
				}
				if doc[i] == '"' {
					break
				}
			}
		case '#':
			if i > start && (doc[i-1] == ' ' || doc[i-1] == '\t') {
				for i < len(doc) && doc[i] != '\n' {
					i++
				}
			}
		case '[', '{':
			depth++
		case ']', '}':
			depth--
			if depth == 0 {
				return i, nil
			}
		}
	}
	return 0, fmt.Errorf("%w: unterminated flow sequence", ErrVerify)
}

func renderScalar(v, style string, flow bool) string {
	switch style {
	case "double":
		return quoteDouble(v)
	case "single":
		return quoteSingle(v)
	}
	if safePlain(v, flow) {
		return v
	}
	return quoteSingle(v)
}

func safePlain(v string, flow bool) bool {
	if v == "" || strings.TrimSpace(v) != v || strings.ContainsAny(v, "\n\r\t") {
		return false
	}
	if strings.ContainsRune("-?:,[]{}#&*!|>'\"%@`", rune(v[0])) {
		return false
	}
	if strings.Contains(v, ": ") || strings.Contains(v, " #") || strings.HasSuffix(v, ":") {
		return false
	}
	if flow && strings.ContainsAny(v, ",[]{}") {
		return false
	}
	var decoded any
	if err := yaml.Unmarshal([]byte(v), &decoded); err != nil {
		return false
	}
	s, ok := decoded.(string)
	return ok && s == v
}

func quoteSingle(v string) string { return "'" + strings.ReplaceAll(v, "'", "''") + "'" }

func quoteDouble(v string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range v {
		switch {
		case r == '\\' || r == '"':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r < 0x20:
			fmt.Fprintf(&b, "\\x%02x", r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func styleName(n *yaml.Node) string {
	switch {
	case n.Style&yaml.DoubleQuotedStyle != 0:
		return "double"
	case n.Style&yaml.SingleQuotedStyle != 0:
		return "single"
	}
	return "plain"
}

func styleFromName(s string) yaml.Style {
	switch s {
	case "double":
		return yaml.DoubleQuotedStyle
	case "single":
		return yaml.SingleQuotedStyle
	}
	return 0
}

func indentOf(line string) string {
	return line[:len(line)-len(strings.TrimLeft(line, " \t"))]
}

func lineEnding(lines []string) string {
	if len(lines) > 0 && strings.HasSuffix(lines[0], "\r\n") {
		return "\r\n"
	}
	return "\n"
}

func verify(out string, expected any) error {
	root, _, err := parse(out)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrVerify, err)
	}
	got, err := decodeTree(root)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrVerify, err)
	}
	if !reflect.DeepEqual(normalize(got), normalize(expected)) {
		return ErrVerify
	}
	return nil
}

// normalize treats an empty sequence and null as equal: "proxies:" with all
// items removed decodes as null while the tree layer keeps an empty list.
func normalize(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = normalize(val)
		}
		return out
	case []any:
		if len(t) == 0 {
			return nil
		}
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = normalize(val)
		}
		return out
	}
	return v
}

// ---- yaml helpers ----

func parse(doc string) (*yaml.Node, *yaml.Node, error) {
	if strings.TrimSpace(doc) == "" {
		return nil, nil, nil
	}
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(doc), &root); err != nil {
		return nil, nil, err
	}
	if root.Kind != yaml.DocumentNode || len(root.Content) == 0 {
		return nil, nil, errors.New("template is not a YAML document")
	}
	top := root.Content[0]
	if top.Kind != yaml.MappingNode {
		return nil, nil, errors.New("template root is not a mapping")
	}
	return &root, top, nil
}

func decodeTree(root *yaml.Node) (any, error) {
	var v any
	err := root.Decode(&v)
	return v, err
}

func mapEntry(m *yaml.Node, key string) (*yaml.Node, *yaml.Node) {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil, nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			v := m.Content[i+1]
			if v.Kind == yaml.AliasNode && v.Alias != nil {
				v = v.Alias
			}
			return m.Content[i], v
		}
	}
	return nil, nil
}

func mapGet(m *yaml.Node, key string) *yaml.Node {
	_, v := mapEntry(m, key)
	return v
}

func mapSet(m *yaml.Node, key string, v *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1] = v
			return
		}
	}
}

func seqItems(n *yaml.Node) []*yaml.Node {
	if n == nil || n.Kind != yaml.SequenceNode {
		return nil
	}
	return n.Content
}

func scalar(n *yaml.Node) string {
	if n == nil || n.Kind != yaml.ScalarNode {
		return ""
	}
	return n.Value
}

func isNull(n *yaml.Node) bool {
	return n != nil && n.Kind == yaml.ScalarNode && n.Tag == "!!null"
}

func truthy(n *yaml.Node) bool {
	return strings.EqualFold(scalar(n), "true")
}

// needsFallback reports whether an emptied group stays empty after Remnawave
// renders the template: groups without `include-proxies: false` receive every
// host automatically, and use/include-all pull proxies from elsewhere.
func needsFallback(group *yaml.Node) bool {
	if mapGet(group, "use") != nil ||
		truthy(mapGet(group, "include-all")) ||
		truthy(mapGet(group, "include-all-proxies")) ||
		truthy(mapGet(group, "include-all-providers")) {
		return false
	}
	rw := mapGet(group, "remnawave")
	return rw != nil && strings.EqualFold(scalar(mapGet(rw, "include-proxies")), "false")
}

func findGroup(groups []*yaml.Node, name string) *yaml.Node {
	for _, g := range groups {
		if g.Kind == yaml.MappingNode && scalar(mapGet(g, "name")) == name {
			return g
		}
	}
	return nil
}

func indexOf(seq *yaml.Node, value string) int {
	for i, item := range seqItems(seq) {
		if item.Kind == yaml.ScalarNode && item.Value == value {
			return i
		}
	}
	return -1
}

func prevValue(seq *yaml.Node, i int) string {
	if i == 0 {
		return ""
	}
	return scalar(seq.Content[i-1])
}

func restorePosition(seq *yaml.Node, prev string, index int) int {
	n := len(seqItems(seq))
	if prev == "" {
		return 0
	}
	if j := indexOf(seq, prev); j >= 0 {
		return j + 1
	}
	return min(index, n)
}

func insertNode(seq *yaml.Node, i int, n *yaml.Node) {
	seq.Content = slices.Insert(seq.Content, max(0, min(i, len(seq.Content))), n)
}

func strNode(v string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v}
}
