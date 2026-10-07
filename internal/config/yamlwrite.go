package config

import (
	"bytes"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// SetRepoOverride edits the YAML config file at path so that repoSlug within
// projectKey maps to the given Cloud project and/or target slug. Comments and
// unrelated structure are preserved. An empty destination inherits the project
// destination and an empty targetSlug keeps the source slug; when both are
// empty, any existing override for the repo is removed.
func SetRepoOverride(path, projectKey, repoSlug, destination, targetSlug string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read config %q: %w", path, err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("parse config %q: %w", path, err)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return fmt.Errorf("config %q is empty", path)
	}

	_, projects := mapPair(doc.Content[0], "projects")
	if projects == nil || projects.Kind != yaml.SequenceNode {
		return fmt.Errorf("config %q has no projects list", path)
	}

	var project *yaml.Node
	for _, p := range projects.Content {
		if _, key := mapPair(p, "key"); key != nil && key.Value == projectKey {
			project = p
			break
		}
	}
	if project == nil {
		return fmt.Errorf("project %q not found in %q", projectKey, path)
	}

	_, overrides := mapPair(project, "overrides")
	var entry *yaml.Node
	if overrides != nil {
		for _, e := range overrides.Content {
			if _, repo := mapPair(e, "repo"); repo != nil && repo.Value == repoSlug {
				entry = e
				break
			}
		}
	}

	if destination == "" && targetSlug == "" {
		if overrides == nil || entry == nil {
			return nil
		}
		removeSeqItem(overrides, entry)
		if len(overrides.Content) == 0 {
			removeMapKey(project, "overrides")
		}
		return writeYAML(path, &doc)
	}

	if entry == nil {
		entry = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		setMapValue(entry, "repo", scalarNode(repoSlug))
		if overrides == nil {
			overrides = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
			setMapValue(project, "overrides", overrides)
		}
		overrides.Content = append(overrides.Content, entry)
	}
	if destination != "" {
		setMapValue(entry, "destination", scalarNode(destination))
	} else {
		removeMapKey(entry, "destination")
	}
	if targetSlug != "" {
		setMapValue(entry, "target_slug", scalarNode(targetSlug))
	} else {
		removeMapKey(entry, "target_slug")
	}
	return writeYAML(path, &doc)
}

func writeYAML(path string, doc *yaml.Node) error {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	if err := enc.Close(); err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), mode); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return os.Rename(tmp, path)
}

func scalarNode(v string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v}
}

// mapPair returns the key and value nodes for key in a mapping node.
func mapPair(m *yaml.Node, key string) (keyNode, valNode *yaml.Node) {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil, nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i], m.Content[i+1]
		}
	}
	return nil, nil
}

// setMapValue sets key to val in a mapping, replacing an existing value in place.
func setMapValue(m *yaml.Node, key string, val *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1] = val
			return
		}
	}
	m.Content = append(m.Content, scalarNode(key), val)
}

func removeMapKey(m *yaml.Node, key string) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content = append(m.Content[:i], m.Content[i+2:]...)
			return
		}
	}
}

func removeSeqItem(seq, item *yaml.Node) {
	for i, c := range seq.Content {
		if c == item {
			seq.Content = append(seq.Content[:i], seq.Content[i+1:]...)
			return
		}
	}
}
