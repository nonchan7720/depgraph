// Package codexhooks copies the depgraph plugin's Codex hooks
// (plugin/codex/hooks.json) into a hooks.json that Codex reads from its config
// layers (~/.codex/hooks.json, <repo>/.codex/hooks.json, /etc/codex/hooks.json),
// for Codex builds that install the plugin but do not run the hooks it bundles.
//
// The copy is the template with ${PLUGIN_ROOT} replaced by the plugin root's
// absolute path. Other hooks and keys of the target are kept in their order;
// only the plugin's own groups (a command that IsPluginCommand) are replaced, so
// installing again leaves one set. The target is written atomically.
package codexhooks

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Template is the hooks template, relative to the plugin root.
const Template = "codex/hooks.json"

// pluginCommand matches the commands of the template, before and after
// ${PLUGIN_ROOT} is replaced. scripts/hook.sh (the plugin's, see there) looks
// for the same shape to tell that a config layer carries the copy.
var pluginCommand = regexp.MustCompile(`/scripts/hook\.sh" (session-start|prompt) codex$`)

// IsPluginCommand reports whether a hook command is one of the plugin's Codex
// hooks (from the template or a copy of it).
func IsPluginCommand(command string) bool {
	return pluginCommand.MatchString(command)
}

// InstallOptions says what Install does.
type InstallOptions struct {
	Root   string // plugin root (the directory with codex/hooks.json)
	Target string // hooks.json to write into; may be "" with Print
	Print  bool   // print the result to the writer instead of writing Target
	Remove bool   // take the plugin's hooks out of Target instead of adding them
}

// Install adds (or removes) the plugin's hooks in o.Target, or prints the
// result to out with o.Print.
func Install(o InstallOptions, out io.Writer) error {
	if o.Target == "" && (!o.Print || o.Remove) {
		return errors.New("no target hooks.json")
	}
	root, err := filepath.Abs(o.Root)
	if err != nil {
		return err
	}
	if strings.ContainsAny(root, "\"$`\\\n") {
		return fmt.Errorf("plugin root %q has a character the hook command cannot quote (\" $ ` \\ or a newline)", root)
	}
	groups, err := loadTemplate(root)
	if err != nil {
		return err
	}

	var current []byte
	mode := fs.FileMode(0o644)
	if o.Target != "" {
		b, err := os.ReadFile(o.Target)
		switch {
		case err == nil:
			current = b
			if fi, err := os.Stat(o.Target); err == nil {
				mode = fi.Mode().Perm()
			}
		case errors.Is(err, fs.ErrNotExist):
			if o.Remove && !o.Print {
				return nil // nothing to remove
			}
		default:
			return err
		}
	}
	if o.Remove {
		groups = nil
	}
	merged, err := merge(current, groups)
	if err != nil {
		return fmt.Errorf("%s: %w", o.Target, err)
	}
	if o.Print {
		_, err := out.Write(merged)
		return err
	}
	return writeAtomic(o.Target, merged, mode)
}

// group is one entry of an event's list: {"matcher": ..., "hooks": [...]}.
type group struct {
	event string
	raw   json.RawMessage
}

// loadTemplate reads <root>/codex/hooks.json and returns its groups with
// ${PLUGIN_ROOT} replaced by root, in the template's event order.
func loadTemplate(root string) ([]group, error) {
	path := filepath.Join(root, filepath.FromSlash(Template))
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%s is not the depgraph plugin root (no %s): %w", root, Template, err)
	}
	var top object
	if err := json.Unmarshal(b, &top); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	var hooks object
	if raw, ok := top.get("hooks"); !ok {
		return nil, fmt.Errorf("%s: no hooks", path)
	} else if err := json.Unmarshal(raw, &hooks); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	var groups []group
	for _, m := range hooks {
		var list []json.RawMessage
		if err := json.Unmarshal(m.val, &list); err != nil {
			return nil, fmt.Errorf("%s: %s: %w", path, m.key, err)
		}
		for _, g := range list {
			var gm object
			if err := json.Unmarshal(g, &gm); err != nil {
				return nil, fmt.Errorf("%s: %s: %w", path, m.key, err)
			}
			raw, err := resolveRoot(gm, root)
			if err != nil {
				return nil, fmt.Errorf("%s: %s: %w", path, m.key, err)
			}
			groups = append(groups, group{event: m.key, raw: raw})
		}
	}
	return groups, nil
}

// resolveRoot returns the group with ${PLUGIN_ROOT} replaced in the commands of
// its hooks, keys in their order.
func resolveRoot(g object, root string) (json.RawMessage, error) {
	for i, m := range g {
		if m.key != "hooks" {
			continue
		}
		var hooks []object
		if err := json.Unmarshal(m.val, &hooks); err != nil {
			return nil, err
		}
		list := make([]json.RawMessage, 0, len(hooks))
		for _, h := range hooks {
			for j, f := range h {
				var cmd string
				if f.key == "command" && json.Unmarshal(f.val, &cmd) == nil {
					h[j].val = encodeString(strings.ReplaceAll(cmd, "${PLUGIN_ROOT}", root))
				}
			}
			list = append(list, h.encode())
		}
		g[i].val = encodeArray(list)
	}
	return g.encode(), nil
}

// isPluginGroup reports whether a group runs one of the plugin's hooks.
func isPluginGroup(raw json.RawMessage) bool {
	var g struct {
		Hooks []struct {
			Command string `json:"command"`
		} `json:"hooks"`
	}
	if json.Unmarshal(raw, &g) != nil {
		return false
	}
	for _, h := range g.Hooks {
		if IsPluginCommand(h.Command) {
			return true
		}
	}
	return false
}

// merge returns current (a hooks.json, or empty) without the plugin's groups,
// plus groups, indented.
func merge(current []byte, groups []group) ([]byte, error) {
	var top object
	if len(bytes.TrimSpace(current)) > 0 {
		if err := json.Unmarshal(current, &top); err != nil {
			return nil, err
		}
	}
	var hooks object
	if raw, ok := top.get("hooks"); ok {
		if err := json.Unmarshal(raw, &hooks); err != nil {
			return nil, fmt.Errorf(`"hooks" must be an object: %w`, err)
		}
	}
	// drop the plugin's groups (an event left empty goes too)
	kept := hooks[:0]
	for _, m := range hooks {
		var list []json.RawMessage
		if err := json.Unmarshal(m.val, &list); err != nil {
			return nil, fmt.Errorf("hooks.%s must be a list: %w", m.key, err)
		}
		var others []json.RawMessage
		for _, g := range list {
			if !isPluginGroup(g) {
				others = append(others, g)
			}
		}
		if len(others) == len(list) {
			kept = append(kept, m)
		} else if len(others) > 0 {
			kept = append(kept, member{m.key, encodeArray(others)})
		}
	}
	hooks = kept
	for _, g := range groups {
		i := hooks.index(g.event)
		if i < 0 {
			hooks = append(hooks, member{g.event, encodeArray([]json.RawMessage{g.raw})})
			continue
		}
		var list []json.RawMessage
		_ = json.Unmarshal(hooks[i].val, &list) // checked above
		hooks[i].val = encodeArray(append(list, g.raw))
	}
	top.set("hooks", hooks.encode())

	var buf bytes.Buffer
	if err := json.Indent(&buf, compact(top.encode()), "", "  "); err != nil {
		return nil, err
	}
	buf.WriteByte('\n')
	return buf.Bytes(), nil
}

func compact(b []byte) []byte {
	var buf bytes.Buffer
	if err := json.Compact(&buf, b); err != nil {
		return b
	}
	return buf.Bytes()
}

func writeAtomic(path string, data []byte, mode fs.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".hooks.json.*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// ---- a JSON object that keeps its key order -------------------------------

type member struct {
	key string
	val json.RawMessage
}

type object []member

func (o *object) UnmarshalJSON(b []byte) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return errors.New("not a JSON object")
	}
	*o = (*o)[:0]
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		key, _ := tok.(string)
		var val json.RawMessage
		if err := dec.Decode(&val); err != nil {
			return err
		}
		if i := o.index(key); i >= 0 {
			(*o)[i].val = val // the last one wins, as in encoding/json
			continue
		}
		*o = append(*o, member{key, val})
	}
	_, err = dec.Token()
	return err
}

func (o object) index(key string) int {
	for i, m := range o {
		if m.key == key {
			return i
		}
	}
	return -1
}

func (o object) get(key string) (json.RawMessage, bool) {
	if i := o.index(key); i >= 0 {
		return o[i].val, true
	}
	return nil, false
}

func (o *object) set(key string, val json.RawMessage) {
	if i := o.index(key); i >= 0 {
		(*o)[i].val = val
		return
	}
	*o = append(*o, member{key, val})
}

// encode writes the object without HTML escaping (commands keep && and >).
func (o object) encode() json.RawMessage {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, m := range o {
		if i > 0 {
			buf.WriteByte(',')
		}
		buf.Write(encodeString(m.key))
		buf.WriteByte(':')
		buf.Write(m.val)
	}
	buf.WriteByte('}')
	return buf.Bytes()
}

func encodeArray(list []json.RawMessage) json.RawMessage {
	buf := []byte{'['}
	for i, v := range list {
		if i > 0 {
			buf = append(buf, ',')
		}
		buf = append(buf, v...)
	}
	return append(buf, ']')
}

func encodeString(s string) json.RawMessage {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return bytes.TrimRight(buf.Bytes(), "\n")
}
