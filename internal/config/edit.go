package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"
)

// doc is a settings file being edited. The comments at the top of the file are kept as they are;
// the settings below them are a YAML tree, so their own comments and unknown keys survive too.
type doc struct {
	header  string
	hadBody bool       // the file had settings, so the header already sits right above them
	node    *yaml.Node // the document
	root    *yaml.Node // its top-level mapping
}

// edit reads the file (initial stands in for a missing one), lets fn change it and writes it
// back. With removeEmpty a file left without settings is deleted instead of written.
func edit(path, initial string, removeEmpty bool, fn func(d *doc) error) error {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		raw, err = []byte(initial), nil
	}
	if err != nil {
		return err
	}

	d, err := parseDoc(string(raw))
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if err := fn(d); err != nil {
		return err
	}

	out, err := d.encode()
	if err != nil {
		return err
	}
	if len(out) == 0 && removeEmpty {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}

		return nil
	}
	if unchanged, err := os.ReadFile(path); err == nil && bytes.Equal(unchanged, out) {
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	return os.WriteFile(path, out, 0o644)
}

func parseDoc(raw string) (*doc, error) {
	header, body := splitHeader(raw)
	d := &doc{header: header, hadBody: strings.TrimSpace(body) != ""}

	if !d.hadBody {
		d.root = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		d.node = &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{d.root}}

		return d, nil
	}

	d.node = &yaml.Node{}
	if err := yaml.Unmarshal([]byte(body), d.node); err != nil {
		return nil, err
	}
	if d.node.Kind != yaml.DocumentNode || len(d.node.Content) != 1 || d.node.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("must be a YAML mapping of settings")
	}
	d.root = d.node.Content[0]

	return d, nil
}

// splitHeader cuts the leading comment and blank lines off the file.
func splitHeader(raw string) (header, body string) {
	rest := raw
	for rest != "" {
		line, after, _ := strings.Cut(rest, "\n")
		if t := strings.TrimSpace(line); t != "" && !strings.HasPrefix(t, "#") {
			break
		}
		rest = after
	}

	return raw[:len(raw)-len(rest)], rest
}

func (d *doc) encode() ([]byte, error) {
	header := d.header
	if strings.TrimSpace(header) == "" {
		header = ""
	}
	if len(d.root.Content) == 0 {
		if header != "" {
			header = strings.TrimRight(header, "\n") + "\n"
		}

		return []byte(header), nil
	}
	if header != "" && !d.hadBody {
		header = strings.TrimRight(header, "\n") + "\n\n"
	}

	var b bytes.Buffer
	e := yaml.NewEncoder(&b)
	e.SetIndent(2)
	if err := e.Encode(d.node); err != nil {
		return nil, err
	}
	if err := e.Close(); err != nil {
		return nil, err
	}

	return append([]byte(header), b.Bytes()...), nil
}

// index returns the position of the key's value in root.Content, or -1.
func (d *doc) index(key string) int {
	for i := 0; i+1 < len(d.root.Content); i += 2 {
		if d.root.Content[i].Value == key {
			return i + 1
		}
	}

	return -1
}

func (d *doc) remove(key string) {
	if i := d.index(key); i >= 0 {
		d.root.Content = append(d.root.Content[:i-1], d.root.Content[i+1:]...)
	}
}

// setString sets a key to a string. An empty value removes the key.
func (d *doc) setString(key, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		d.remove(key)
		return
	}

	scalar := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
	if i := d.index(key); i >= 0 {
		old := d.root.Content[i]
		scalar.HeadComment, scalar.LineComment, scalar.FootComment = old.HeadComment, old.LineComment, old.FootComment
		d.root.Content[i] = scalar

		return
	}

	d.root.Content = append(d.root.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, scalar)
}

// list returns the strings under key; a missing key is an empty list.
func (d *doc) list(key string) ([]string, error) {
	i := d.index(key)
	if i < 0 {
		return nil, nil
	}

	seq := d.root.Content[i]
	if seq.Kind == yaml.ScalarNode && seq.Tag == "!!null" {
		return nil, nil
	}
	if seq.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("%s must be a list", key)
	}

	items := make([]string, 0, len(seq.Content))
	for _, n := range seq.Content {
		if n.Kind != yaml.ScalarNode {
			return nil, fmt.Errorf("%s must be a list of paths", key)
		}
		items = append(items, n.Value)
	}

	return items, nil
}

// setList replaces the list under key. Items that stay keep their comments. An empty list
// removes the key.
func (d *doc) setList(key string, items []string) {
	if len(items) == 0 {
		d.remove(key)
		return
	}

	seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	existing := map[string]*yaml.Node{}
	if i := d.index(key); i >= 0 && d.root.Content[i].Kind == yaml.SequenceNode {
		seq = d.root.Content[i]
		for _, n := range seq.Content {
			existing[n.Value] = n
		}
	}

	seq.Content = seq.Content[:0:0]
	for _, item := range items {
		n, ok := existing[item]
		if !ok {
			n = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: item}
		}
		seq.Content = append(seq.Content, n)
	}

	if i := d.index(key); i >= 0 {
		d.root.Content[i] = seq
		return
	}

	d.root.Content = append(d.root.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, seq)
}
