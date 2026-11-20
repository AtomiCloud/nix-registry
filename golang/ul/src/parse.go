package main

import (
	"bytes"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

type parser struct {
	file  string
	probs []Problem
	opts  options
}

type pair struct {
	key   string
	knode *yaml.Node
	vnode *yaml.Node
}

var yamlLineRe = regexp.MustCompile(`line (\d+)`)

func (p *parser) add(code string, line int, format string, a ...any) {
	if line < 1 {
		line = 1
	}
	p.probs = append(p.probs, Problem{Code: code, File: p.file, Line: line, Message: fmt.Sprintf(format, a...)})
}

// parseDocs parses the rules that look at one file alone: UL101, UL102, UL104, UL105, UL106
// and UL108. A document whose YAML cannot be read, or whose namespace is unusable, is dropped
// (it has no id to hang words on); every other problem keeps the document.
func parseDocs(file string, data []byte, opts options) ([]*Doc, []Problem) {
	p := &parser{file: file, opts: opts}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var docs []*Doc
	count := 0
	for {
		var n yaml.Node
		err := dec.Decode(&n)
		if err == io.EOF {
			break
		}
		if err != nil {
			line := 1
			if m := yamlLineRe.FindStringSubmatch(err.Error()); m != nil {
				line, _ = strconv.Atoi(m[1])
			}
			p.add("UL101", line, "not valid YAML: %s", strings.TrimPrefix(err.Error(), "yaml: "))
			return nil, p.probs
		}
		count++
		if count > 1 {
			p.add("UL101", n.Line, "more than one YAML document in the file")
			break
		}
		if d := p.doc(&n); d != nil {
			docs = append(docs, d)
		}
	}
	if count == 0 {
		p.add("UL101", 1, "the file is empty")
	}
	return docs, p.probs
}

func resolveNode(n *yaml.Node) *yaml.Node {
	for n != nil && n.Kind == yaml.AliasNode {
		n = n.Alias
	}
	return n
}

func isNullNode(n *yaml.Node) bool { return n.Kind == yaml.ScalarNode && n.Tag == "!!null" }

// pairs lists the key/value pairs of a mapping; a duplicate key is UL101 and its later
// occurrence is ignored.
func (p *parser) pairs(n *yaml.Node, what string) []pair {
	seen := map[string]int{}
	var out []pair
	for i := 0; i+1 < len(n.Content); i += 2 {
		k := resolveNode(n.Content[i])
		v := n.Content[i+1]
		if k.Kind != yaml.ScalarNode {
			p.add("UL101", k.Line, "%s: a key must be plain text", what)
			continue
		}
		if k.Tag == "!!merge" {
			p.add("UL101", k.Line, "%s: merge keys (<<) are not supported", what)
			continue
		}
		if first, ok := seen[k.Value]; ok {
			p.add("UL101", k.Line, "%s: duplicate key %q (first at line %d)", what, k.Value, first)
			continue
		}
		seen[k.Value] = k.Line
		out = append(out, pair{k.Value, k, v})
	}
	return out
}

// text returns the string of a scalar; ok is false for null or a non-scalar.
func text(n *yaml.Node) (string, bool) {
	n = resolveNode(n)
	if n == nil || n.Kind != yaml.ScalarNode || isNullNode(n) {
		return "", false
	}
	return n.Value, true
}

// oneLine trims the trailing newline of a block scalar and reports whether one line remains.
func oneLine(s string) (string, bool) {
	s = strings.TrimRight(s, "\r\n")
	return s, !strings.ContainsAny(s, "\r\n")
}

func (p *parser) doc(root *yaml.Node) *Doc {
	n := root
	if n.Kind == yaml.DocumentNode {
		if len(n.Content) == 0 {
			n = &yaml.Node{Kind: yaml.MappingNode, Line: root.Line}
		} else {
			n = n.Content[0]
		}
	}
	n = resolveNode(n)
	if isNullNode(n) {
		n = &yaml.Node{Kind: yaml.MappingNode, Line: n.Line}
	}
	if n.Kind != yaml.MappingNode {
		p.add("UL101", n.Line, "a UL.yaml must be a mapping with namespace, premise and language")
		return nil
	}
	d := &Doc{File: p.file, NSLine: n.Line}
	var haveNS, havePremise, haveLang bool
	var langNode *yaml.Node
	var langLine int
	for _, kv := range p.pairs(n, "UL.yaml") {
		switch kv.key {
		case "namespace":
			haveNS = true
			d.NSLine = kv.knode.Line
			s, ok := text(kv.vnode)
			if !ok {
				haveNS = false
				break
			}
			d.Namespace = s
		case "premise":
			havePremise = true
			s, ok := text(kv.vnode)
			if !ok {
				p.add("UL104", kv.knode.Line, "premise must be one line of text")
				break
			}
			line, single := oneLine(s)
			switch {
			case !single:
				p.add("UL104", kv.knode.Line, "premise must be one line, not several")
			case strings.TrimSpace(line) == "":
				p.add("UL104", kv.knode.Line, "premise is empty")
			}
			d.Premise = line
		case "language":
			haveLang = true
			langNode = resolveNode(kv.vnode)
			langLine = kv.knode.Line
		default:
			p.add("UL101", kv.knode.Line, "unknown field %q (allowed: namespace, premise, language)", kv.key)
		}
	}
	if !haveNS {
		p.add("UL102", n.Line, "namespace is missing")
		return nil
	}
	if why := validNamespace(d.Namespace); why != "" {
		p.add("UL102", d.NSLine, "namespace %q: %s", d.Namespace, why)
		return nil
	}
	if !havePremise {
		p.add("UL104", n.Line, "premise is missing")
	}
	switch {
	case !haveLang:
		p.add("UL105", n.Line, "language is missing: a namespace needs at least one word")
	case langNode == nil || isNullNode(langNode):
		p.add("UL105", langLine, "language is empty: a namespace needs at least one word")
	case langNode.Kind != yaml.MappingNode:
		p.add("UL105", langLine, "language must be a mapping of word to entry")
	default:
		ps := p.pairs(langNode, "language")
		if len(ps) == 0 {
			p.add("UL105", langLine, "language is empty: a namespace needs at least one word")
		}
		for _, kv := range ps {
			if why := validName(kv.key); why != "" {
				p.add("UL105", kv.knode.Line, "word %q is malformed: %s", kv.key, why)
				continue
			}
			if e := p.entry(kv.key, kv.knode.Line, kv.vnode); e != nil {
				d.Entries = append(d.Entries, e)
			}
		}
	}
	return d
}

func (p *parser) list(word, field string, n *yaml.Node) []Named {
	n = resolveNode(n)
	if isNullNode(n) {
		return nil
	}
	if n.Kind != yaml.SequenceNode {
		p.add("UL101", n.Line, "%s: %s must be a list", word, field)
		return nil
	}
	var out []Named
	for _, c := range n.Content {
		c = resolveNode(c)
		if c.Kind != yaml.ScalarNode || isNullNode(c) || strings.TrimSpace(c.Value) == "" {
			p.add("UL101", c.Line, "%s: every item of %s must be non-empty text", word, field)
			continue
		}
		out = append(out, Named{c.Value, c.Line})
	}
	return out
}

func (p *parser) entry(word string, line int, v *yaml.Node) *Entry {
	e := &Entry{Word: word, WordLine: line, Status: statusActive}
	v = resolveNode(v)
	if v.Kind != yaml.MappingNode && !isNullNode(v) {
		p.add("UL105", line, "word %q: the entry must be a mapping (desc and more)", word)
		return nil
	}
	descSeen := false
	if v.Kind == yaml.MappingNode {
		for _, kv := range p.pairs(v, word) {
			switch kv.key {
			case "desc":
				descSeen = true
				s, ok := text(kv.vnode)
				one, single := oneLine(s)
				switch {
				case !ok || (single && strings.TrimSpace(one) == ""):
					p.add("UL106", kv.knode.Line, "%s: desc is empty", word)
				case !single:
					p.add("UL106", kv.knode.Line, "%s: desc must be one line, not several", word)
				case utf8.RuneCountInString(one) > 160:
					p.add("UL106", kv.knode.Line, "%s: desc is %d characters; the limit is 160", word, utf8.RuneCountInString(one))
				}
				e.Desc = one
			case "details":
				s, ok := text(kv.vnode)
				if !ok && !isNullNode(resolveNode(kv.vnode)) {
					p.add("UL101", kv.knode.Line, "%s: details must be text", word)
				}
				e.Details = s
			case "aliases":
				for _, a := range p.list(word, "aliases", kv.vnode) {
					if why := validName(a.Name); why != "" {
						p.add("UL105", a.Line, "%s: alias %q is malformed: %s", word, a.Name, why)
						continue
					}
					e.Aliases = append(e.Aliases, a)
				}
			case "avoid":
				e.Avoid = p.list(word, "avoid", kv.vnode)
			case "see":
				e.See = p.list(word, "see", kv.vnode)
			case "status":
				s, _ := text(kv.vnode)
				if s != statusActive && s != statusRetired {
					p.add("UL108", kv.knode.Line, "%s: status %q is not active or retired", word, s)
					break
				}
				e.Status = s
			case "translations":
				if p.opts.translations != modeFixed {
					p.add("UL101", kv.knode.Line, "%s: unknown field \"translations\" (this repo runs ul with --translations %s, which has no such field)", word, p.opts.translations)
					break
				}
				e.Translations = p.translations(word, kv.vnode)
			case "replacedBy":
				s, ok := text(kv.vnode)
				if !ok || strings.TrimSpace(s) == "" {
					p.add("UL101", kv.knode.Line, "%s: replacedBy must be a word or an id", word)
					break
				}
				e.ReplacedBy = Named{s, kv.knode.Line}
			default:
				p.add("UL101", kv.knode.Line, "%s: unknown field %q (allowed: desc, details, aliases, avoid, see, status, replacedBy%s)", word, kv.key, p.allowedExtra())
			}
		}
	}
	if !descSeen {
		p.add("UL106", line, "%s: desc is missing", word)
	}
	return e
}

// allowedExtra names the fields a mode adds to the allowed list in the unknown-field message.
func (p *parser) allowedExtra() string {
	if p.opts.translations == modeFixed {
		return ", translations"
	}
	return ""
}

// langRe is the shape of a language code: a BCP 47 tag such as es, pt or zh-Hans.
var langRe = regexp.MustCompile(`(?i)^[a-z]{2,3}(-[a-z]{4})?(-([a-z]{2}|[0-9]{3}))?$`)

// translations reads the translations field: a mapping of language code to one fixed term
// (UL110 refuses anything else).
func (p *parser) translations(word string, n *yaml.Node) []Translation {
	n = resolveNode(n)
	if isNullNode(n) {
		return nil
	}
	if n.Kind != yaml.MappingNode {
		p.add("UL110", n.Line, "%s: translations must be a mapping of language code to term", word)
		return nil
	}
	var out []Translation
	seenLang := map[string]bool{}
	for _, kv := range p.pairs(n, word+": translations") {
		if seenLang[strings.ToLower(kv.key)] {
			p.add("UL110", kv.knode.Line, "%s: translations: %q repeats a language that is already there (tags match ignoring case)", word, kv.key)
			continue
		}
		seenLang[strings.ToLower(kv.key)] = true
		if !langRe.MatchString(kv.key) {
			p.add("UL110", kv.knode.Line, "%s: translations: %q is not a language code such as es, pt or zh-Hans", word, kv.key)
			continue
		}
		s, ok := text(kv.vnode)
		term, single := oneLine(s)
		switch {
		case !ok || strings.TrimSpace(term) == "":
			p.add("UL110", kv.knode.Line, "%s: translations: the %s term is empty", word, kv.key)
		case !single:
			p.add("UL110", kv.knode.Line, "%s: translations: the %s term must be one line", word, kv.key)
		case term != strings.TrimSpace(term):
			p.add("UL110", kv.knode.Line, "%s: translations: the %s term has spaces at its start or end", word, kv.key)
		case utf8.RuneCountInString(term) > 100:
			p.add("UL110", kv.knode.Line, "%s: translations: the %s term is longer than 100 characters", word, kv.key)
		default:
			out = append(out, Translation{Lang: kv.key, Term: term, Line: kv.knode.Line})
		}
	}
	return out
}
