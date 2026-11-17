package main

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

var sessionIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// sessionID applies spec §4.2: --session, else $UL_SESSION, else $FY_SESSION_ID. --no-session
// turns the layer off.
func sessionID(g *globals, getenv func(string) string) (string, error) {
	if g.noSession {
		return "", nil
	}
	id := g.session
	if id == "" {
		id = getenv("UL_SESSION")
	}
	if id == "" {
		id = getenv("FY_SESSION_ID")
	}
	if id != "" && !sessionIDRe.MatchString(id) {
		return "", fmt.Errorf("session id %q: use letters, digits, . _ - (at most 128, starting with a letter or digit)", id)
	}
	return id, nil
}

func stateDir(getenv func(string) string) string {
	if d := getenv("UL_STATE_DIR"); d != "" {
		return d
	}
	if x := getenv("XDG_STATE_HOME"); x != "" {
		return filepath.Join(x, "ul")
	}
	return filepath.Join(getenv("HOME"), ".local", "state", "ul")
}

func sessionPath(getenv func(string) string, id string) string {
	return filepath.Join(stateDir(getenv), "sessions", id+".yaml")
}

func str(s string) *yaml.Node {
	n := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s}
	if strings.Contains(s, "\n") {
		n.Style = yaml.LiteralStyle
	}
	return n
}

func mapping(kv ...*yaml.Node) *yaml.Node {
	return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: kv}
}

func seq(items []Named) *yaml.Node {
	n := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for _, it := range items {
		n.Content = append(n.Content, str(it.Name))
	}
	return n
}

// marshalDocs writes the session layer as UL.yaml text: one document per namespace,
// namespaces and words sorted, so the same layer always gives the same bytes.
func marshalDocs(docs []*Doc) ([]byte, error) {
	docs = slices.Clone(docs)
	slices.SortFunc(docs, func(a, b *Doc) int { return cmpFold(a.Namespace, b.Namespace) })
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	for _, d := range docs {
		es := slices.Clone(d.Entries)
		slices.SortFunc(es, func(a, b *Entry) int { return cmpFold(a.Word, b.Word) })
		lang := mapping()
		for _, e := range es {
			em := mapping(str("desc"), str(e.Desc))
			add := func(k string, v *yaml.Node) { em.Content = append(em.Content, str(k), v) }
			if e.Details != "" {
				add("details", str(e.Details))
			}
			if len(e.Aliases) > 0 {
				add("aliases", seq(e.Aliases))
			}
			if len(e.Avoid) > 0 {
				add("avoid", seq(e.Avoid))
			}
			if len(e.See) > 0 {
				add("see", seq(e.See))
			}
			if e.Status == statusRetired {
				add("status", str(e.Status))
			}
			if e.ReplacedBy.Name != "" {
				add("replacedBy", str(e.ReplacedBy.Name))
			}
			lang.Content = append(lang.Content, str(e.Word), em)
		}
		root := mapping(str("namespace"), str(d.Namespace), str("premise"), str(d.Premise), str("language"), lang)
		if err := enc.Encode(root); err != nil {
			return nil, err
		}
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// loadSession reads the session file. A missing file is an empty layer.
func loadSession(path string) (docs []*Doc, probs []Problem, raw []byte, err error) {
	raw, err = os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil, nil
	}
	if err != nil {
		return nil, nil, nil, err
	}
	docs, probs = parseDocs(path, raw, parseOpts{session: true})
	return docs, probs, raw, nil
}

// writeAtomic replaces a file in one rename, so a reader never sees half of it.
func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".ul-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
