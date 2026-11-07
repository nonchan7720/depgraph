package config

import (
	"bytes"
	"errors"
	"fmt"

	"gopkg.in/yaml.v3"
)

// LanguageEntry is a languages: setting `depgraph init` adds.
type LanguageEntry struct {
	Dir, Lang, VersionFile string
}

// AddLanguageSettings adds entries to the content of a .depgraph.yml (nil
// for a new one), keeping everything else, comments included: the root's
// under languages:, the others under components:. A setting that is
// already there is not replaced.
func AddLanguageSettings(b []byte, entries []LanguageEntry) ([]byte, error) {
	var doc yaml.Node
	if len(bytes.TrimSpace(b)) > 0 {
		if err := yaml.Unmarshal(b, &doc); err != nil {
			return nil, fmt.Errorf("yaml: %w", err)
		}
	}
	if doc.Kind == 0 {
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode}}}
	}
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("want a mapping at the top of the file")
	}
	top := doc.Content[0]
	for _, e := range entries {
		m := top
		if e.Dir != "." && e.Dir != "" {
			m = mappingAt(mappingAt(top, "components"), e.Dir)
		}
		langs := mappingAt(m, "languages")
		if get(langs, e.Lang) != nil {
			return nil, fmt.Errorf("languages.%s of %s is already set", e.Lang, e.Dir)
		}
		setting := &yaml.Node{Kind: yaml.MappingNode, Style: yaml.FlowStyle}
		setting.Content = append(setting.Content, scalar("versionFile"), scalar(e.VersionFile))
		langs.Content = append(langs.Content, scalar(e.Lang), setting)
	}
	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	if _, err := Parse(out.Bytes()); err != nil {
		return nil, fmt.Errorf("the result would not be valid: %w", err)
	}
	return out.Bytes(), nil
}

func scalar(s string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Value: s} }

func get(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// mappingAt returns the mapping under key of m, adding it when missing.
func mappingAt(m *yaml.Node, key string) *yaml.Node {
	if v := get(m, key); v != nil {
		if v.Kind == yaml.MappingNode {
			return v
		}
		v.Kind, v.Tag, v.Value, v.Content = yaml.MappingNode, "", "", nil // null: an empty mapping
		return v
	}
	v := &yaml.Node{Kind: yaml.MappingNode}
	m.Content = append(m.Content, scalar(key), v)
	return v
}
