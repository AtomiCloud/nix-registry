package main

import (
	"fmt"
	"regexp"
	"strconv"
	"unicode/utf16"
	"unicode/utf8"
)

// JV is a JSON value that keeps what encoding/json loses: object key order, exact number
// text, and strings as UTF-16 code units (so lone surrogates survive into test vectors).
type JV struct {
	Kind byte // 's' string, 'n' number, 'b' bool, 'z' null, 'a' array, 'o' object
	Str  []uint16
	Num  string
	Bool bool
	Arr  []*JV
	Keys []string
	Vals []*JV
}

// Get returns an object member. A duplicated name yields its last member, as RenderVector
// and encoding/json do.
func (v *JV) Get(k string) (*JV, bool) {
	if v == nil || v.Kind != 'o' {
		return nil, false
	}
	for i := len(v.Keys) - 1; i >= 0; i-- {
		if v.Keys[i] == k {
			return v.Vals[i], true
		}
	}
	return nil, false
}

// GoString converts code units to a Go string; lone surrogates become U+FFFD.
func (v *JV) GoString() string { return unitsToString(v.Str) }

func unitsToString(u []uint16) string {
	return string(utf16.Decode(u))
}

// stringToUnits converts a Go string to UTF-16 code units; invalid UTF-8 bytes become U+FFFD each.
func stringToUnits(s string) []uint16 {
	var rs []rune
	for len(s) > 0 {
		r, n := utf8.DecodeRuneInString(s)
		rs = append(rs, r)
		s = s[n:]
	}
	return utf16.Encode(rs)
}

// jsonNumber is the RFC 8259 number grammar; ParseFloat alone also accepts "1.", "-.5" and
// "0x1p-2", which are not JSON.
var jsonNumber = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?$`)

// ParseJV parses JSON text.
func ParseJV(src []byte) (*JV, error) {
	p := &jp{s: src}
	p.ws()
	v, err := p.value()
	if err != nil {
		return nil, err
	}
	p.ws()
	if p.i != len(p.s) {
		return nil, fmt.Errorf("offset %d: trailing data", p.i)
	}
	return v, nil
}

type jp struct {
	s []byte
	i int
}

func (p *jp) ws() {
	for p.i < len(p.s) && (p.s[p.i] == ' ' || p.s[p.i] == '\n' || p.s[p.i] == '\r' || p.s[p.i] == '\t') {
		p.i++
	}
}

func (p *jp) errf(f string, a ...any) error {
	return fmt.Errorf("offset %d: %s", p.i, fmt.Sprintf(f, a...))
}

func (p *jp) value() (*JV, error) {
	if p.i >= len(p.s) {
		return nil, p.errf("unexpected end")
	}
	switch c := p.s[p.i]; {
	case c == '{':
		p.i++
		v := &JV{Kind: 'o'}
		p.ws()
		if p.i < len(p.s) && p.s[p.i] == '}' {
			p.i++
			return v, nil
		}
		for {
			p.ws()
			if p.i >= len(p.s) || p.s[p.i] != '"' {
				return nil, p.errf("expected key")
			}
			k, err := p.str()
			if err != nil {
				return nil, err
			}
			p.ws()
			if p.i >= len(p.s) || p.s[p.i] != ':' {
				return nil, p.errf("expected ':'")
			}
			p.i++
			p.ws()
			val, err := p.value()
			if err != nil {
				return nil, err
			}
			v.Keys = append(v.Keys, unitsToString(k))
			v.Vals = append(v.Vals, val)
			p.ws()
			if p.i < len(p.s) && p.s[p.i] == ',' {
				p.i++
				continue
			}
			if p.i < len(p.s) && p.s[p.i] == '}' {
				p.i++
				return v, nil
			}
			return nil, p.errf("expected ',' or '}'")
		}
	case c == '[':
		p.i++
		v := &JV{Kind: 'a'}
		p.ws()
		if p.i < len(p.s) && p.s[p.i] == ']' {
			p.i++
			return v, nil
		}
		for {
			p.ws()
			val, err := p.value()
			if err != nil {
				return nil, err
			}
			v.Arr = append(v.Arr, val)
			p.ws()
			if p.i < len(p.s) && p.s[p.i] == ',' {
				p.i++
				continue
			}
			if p.i < len(p.s) && p.s[p.i] == ']' {
				p.i++
				return v, nil
			}
			return nil, p.errf("expected ',' or ']'")
		}
	case c == '"':
		u, err := p.str()
		if err != nil {
			return nil, err
		}
		return &JV{Kind: 's', Str: u}, nil
	case c == 't' && p.lit("true"):
		return &JV{Kind: 'b', Bool: true}, nil
	case c == 'f' && p.lit("false"):
		return &JV{Kind: 'b'}, nil
	case c == 'n' && p.lit("null"):
		return &JV{Kind: 'z'}, nil
	case c == '-' || (c >= '0' && c <= '9'):
		st := p.i
		if p.s[p.i] == '-' {
			p.i++
		}
		for p.i < len(p.s) && (p.s[p.i] >= '0' && p.s[p.i] <= '9' || p.s[p.i] == '.' || p.s[p.i] == 'e' || p.s[p.i] == 'E' || p.s[p.i] == '+' || p.s[p.i] == '-') {
			p.i++
		}
		txt := string(p.s[st:p.i])
		if !jsonNumber.MatchString(txt) {
			return nil, p.errf("bad number %q", txt)
		}
		if _, err := strconv.ParseFloat(txt, 64); err != nil {
			if ne, ok := err.(*strconv.NumError); !ok || ne.Err != strconv.ErrRange {
				return nil, p.errf("bad number %q", txt)
			}
		}
		return &JV{Kind: 'n', Num: txt}, nil
	}
	return nil, p.errf("unexpected %q", p.s[p.i])
}

func (p *jp) lit(w string) bool {
	if p.i+len(w) <= len(p.s) && string(p.s[p.i:p.i+len(w)]) == w {
		p.i += len(w)
		return true
	}
	return false
}

func (p *jp) str() ([]uint16, error) {
	p.i++ // opening quote
	var out []uint16
	for p.i < len(p.s) {
		c := p.s[p.i]
		switch {
		case c == '"':
			p.i++
			return out, nil
		case c == '\\':
			if p.i+1 >= len(p.s) {
				return nil, p.errf("bad escape")
			}
			e := p.s[p.i+1]
			p.i += 2
			switch e {
			case '"', '\\', '/':
				out = append(out, uint16(e))
			case 'b':
				out = append(out, '\b')
			case 'f':
				out = append(out, '\f')
			case 'n':
				out = append(out, '\n')
			case 'r':
				out = append(out, '\r')
			case 't':
				out = append(out, '\t')
			case 'u':
				if p.i+4 > len(p.s) {
					return nil, p.errf("bad \\u escape")
				}
				n, err := strconv.ParseUint(string(p.s[p.i:p.i+4]), 16, 16)
				if err != nil {
					return nil, p.errf("bad \\u escape")
				}
				out = append(out, uint16(n))
				p.i += 4
			default:
				return nil, p.errf("bad escape \\%c", e)
			}
		case c < 0x20:
			return nil, p.errf("control character in string")
		default:
			r, n := utf8.DecodeRune(p.s[p.i:])
			p.i += n
			if r >= 0x10000 {
				a, b := utf16.EncodeRune(r)
				out = append(out, uint16(a), uint16(b))
			} else {
				out = append(out, uint16(r))
			}
		}
	}
	return nil, p.errf("unterminated string")
}
