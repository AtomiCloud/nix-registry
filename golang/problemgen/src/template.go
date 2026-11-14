package main

import (
	"fmt"
	"strings"
)

// Template grammar (SPEC §4):
//
//	template    = *( literal / "{{" / "}}" / placeholder )
//	placeholder = "{" name [ "|count" ] [ "|default:" deftext ] "}"
//	name        = a declared data field of the same problem (lowerCamel)
//	deftext     = 1*( any char except "{" "}" "|" and control characters )
//
// "{{" renders "{" and "}}" renders "}". A lone "}" is an error.

// Part is one piece of a parsed template: literal text or a placeholder.
type Part struct {
	Lit        string
	Field      string
	Count      bool
	HasDefault bool
	Default    string
	Offset     int // byte offset of the placeholder in the source, for diagnostics
}

// IsPlaceholder reports whether the part is a placeholder.
func (p Part) IsPlaceholder() bool { return p.Field != "" }

// Template is a parsed template.
type Template struct {
	Src   string
	Parts []Part
}

// ParseTemplate parses a template. It checks only the grammar; field checks are lint's.
func ParseTemplate(src string) (*Template, error) {
	t := &Template{Src: src}
	var lit strings.Builder
	flush := func() {
		if lit.Len() > 0 {
			t.Parts = append(t.Parts, Part{Lit: lit.String()})
			lit.Reset()
		}
	}
	for i := 0; i < len(src); {
		c := src[i]
		switch {
		case c == '{' && i+1 < len(src) && src[i+1] == '{':
			lit.WriteByte('{')
			i += 2
		case c == '}' && i+1 < len(src) && src[i+1] == '}':
			lit.WriteByte('}')
			i += 2
		case c == '}':
			return nil, fmt.Errorf("offset %d: lone '}' (write '}}' for a literal brace)", i)
		case c == '{':
			end := strings.IndexByte(src[i+1:], '}')
			if end < 0 {
				return nil, fmt.Errorf("offset %d: unclosed placeholder", i)
			}
			body := src[i+1 : i+1+end]
			if strings.ContainsRune(body, '{') {
				return nil, fmt.Errorf("offset %d: '{' inside a placeholder", i)
			}
			p, err := parsePlaceholder(body)
			if err != nil {
				return nil, fmt.Errorf("offset %d: %v", i, err)
			}
			p.Offset = i
			flush()
			t.Parts = append(t.Parts, p)
			i += end + 2
		default:
			lit.WriteByte(c)
			i++
		}
	}
	flush()
	return t, nil
}

func parsePlaceholder(body string) (Part, error) {
	segs := strings.Split(body, "|")
	name := segs[0]
	if !isLowerCamel(name) {
		return Part{}, fmt.Errorf("placeholder name %q must be a field name (lowerCamel)", name)
	}
	p := Part{Field: name}
	rest := segs[1:]
	if len(rest) > 0 && rest[0] == "count" {
		p.Count = true
		rest = rest[1:]
	}
	if len(rest) > 0 && strings.HasPrefix(rest[0], "default:") {
		p.HasDefault = true
		p.Default = strings.TrimPrefix(rest[0], "default:")
		if p.Default == "" {
			return Part{}, fmt.Errorf("empty default in {%s}", body)
		}
		rest = rest[1:]
	}
	if len(rest) > 0 {
		return Part{}, fmt.Errorf("unknown modifier %q in {%s} (allowed: |count, |default:text, in that order)", rest[0], body)
	}
	return p, nil
}

func isLowerCamel(s string) bool {
	if s == "" || s[0] < 'a' || s[0] > 'z' {
		return false
	}
	for i := 1; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}
