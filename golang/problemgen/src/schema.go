package main

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
	"gopkg.in/yaml.v3"
)

//go:embed schema/problems.schema.json
var definitionSchemaJSON []byte

var (
	compiledOnce sync.Once
	compiled     *jsonschema.Schema
	compileErr   error
)

func definitionSchema() (*jsonschema.Schema, error) {
	compiledOnce.Do(func() {
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(definitionSchemaJSON))
		if err != nil {
			compileErr = err
			return
		}
		c := jsonschema.NewCompiler()
		if err := c.AddResource("problems.schema.json", doc); err != nil {
			compileErr = err
			return
		}
		compiled, compileErr = c.Compile("problems.schema.json")
	})
	return compiled, compileErr
}

// yamlToJSON converts a YAML node to JSON text, keeping integers exact and refusing
// non-JSON scalars (YAML timestamps, binary, NaN...).
func yamlToJSON(n *yaml.Node, buf *bytes.Buffer) error {
	switch n.Kind {
	case yaml.MappingNode:
		buf.WriteByte('{')
		for i := 0; i+1 < len(n.Content); i += 2 {
			if i > 0 {
				buf.WriteByte(',')
			}
			k, _ := json.Marshal(n.Content[i].Value)
			buf.Write(k)
			buf.WriteByte(':')
			if err := yamlToJSON(n.Content[i+1], buf); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
	case yaml.SequenceNode:
		buf.WriteByte('[')
		for i, c := range n.Content {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := yamlToJSON(c, buf); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
	case yaml.ScalarNode:
		switch n.Tag {
		case "!!str":
			s, _ := json.Marshal(n.Value)
			buf.Write(s)
		case "!!int":
			i, err := strconv.ParseInt(n.Value, 0, 64)
			if err != nil {
				return fmt.Errorf("line %d: integer %q out of range", n.Line, n.Value)
			}
			buf.WriteString(strconv.FormatInt(i, 10))
		case "!!float":
			f, err := strconv.ParseFloat(n.Value, 64)
			if err != nil || strings.ContainsAny(strings.ToLower(n.Value), "in") {
				return fmt.Errorf("line %d: %q is not a finite JSON number", n.Line, n.Value)
			}
			buf.WriteString(strconv.FormatFloat(f, 'g', -1, 64))
		case "!!bool":
			if n.Value == "true" || n.Value == "false" {
				buf.WriteString(n.Value)
			} else {
				return fmt.Errorf("line %d: write booleans as true/false, not %q", n.Line, n.Value)
			}
		case "!!null":
			buf.WriteString("null")
		default:
			return fmt.Errorf("line %d: unsupported YAML scalar type %s", n.Line, n.Tag)
		}
	default:
		return fmt.Errorf("line %d: unsupported YAML node", n.Line)
	}
	return nil
}

// nodeAt walks a JSON-pointer-like path to the YAML node, returning the deepest found.
func nodeAt(root *yaml.Node, path []string) *yaml.Node {
	cur := root
	for _, seg := range path {
		switch cur.Kind {
		case yaml.MappingNode:
			var next *yaml.Node
			for i := 0; i+1 < len(cur.Content); i += 2 {
				if cur.Content[i].Value == seg {
					next = cur.Content[i+1]
					break
				}
			}
			if next == nil {
				return cur
			}
			cur = next
		case yaml.SequenceNode:
			i, err := strconv.Atoi(seg)
			if err != nil || i >= len(cur.Content) {
				return cur
			}
			cur = cur.Content[i]
		default:
			return cur
		}
	}
	return cur
}

func validateSchema(file string, root *yaml.Node) []Diag {
	var buf bytes.Buffer
	if err := yamlToJSON(root, &buf); err != nil {
		return []Diag{{"P005", Pos{File: file}, err.Error()}}
	}
	sch, err := definitionSchema()
	if err != nil {
		return []Diag{{"P000", Pos{File: file}, "internal: schema does not compile: " + err.Error()}}
	}
	inst, err := jsonschema.UnmarshalJSON(&buf)
	if err != nil {
		return []Diag{{"P005", Pos{File: file}, err.Error()}}
	}
	err = sch.Validate(inst)
	if err == nil {
		return nil
	}
	var ve *jsonschema.ValidationError
	if !errors.As(err, &ve) {
		return []Diag{{"P006", Pos{File: file}, err.Error()}}
	}
	// Report the leaf causes (the most specific messages), one per instance location.
	type leaf struct {
		loc []string
		msg string
	}
	var leaves []leaf
	var walk func(e *jsonschema.ValidationError)
	walk = func(e *jsonschema.ValidationError) {
		if len(e.Causes) == 0 {
			leaves = append(leaves, leaf{e.InstanceLocation, e.ErrorKind.LocalizedString(enPrinter)})
			return
		}
		for _, c := range e.Causes {
			walk(c)
		}
	}
	walk(ve)
	seen := map[string]bool{}
	var out []Diag
	for _, l := range leaves {
		key := strings.Join(l.loc, "/") + "|" + l.msg
		if seen[key] {
			continue
		}
		seen[key] = true
		n := nodeAt(root, l.loc)
		out = append(out, Diag{"P006", pos(file, n), fmt.Sprintf("at /%s: %s", strings.Join(l.loc, "/"), l.msg)})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Pos.Line < out[j].Pos.Line })
	return out
}

var enPrinter = message.NewPrinter(language.English)
