package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// DataSchema returns the JSON Schema of a problem's public `data` as canonical JSON text.
// No public data => {"not":{}} (A127: data is then omitted on the wire).
func DataSchema(m *Module, p *Problem) string {
	pub := p.PublicData()
	if len(pub) == 0 {
		return `{"not":{}}`
	}
	var b strings.Builder
	b.WriteByte('{')
	if p.Description != "" {
		b.WriteString(`"description":`)
		writeJSONString(&b, p.Description)
		b.WriteByte(',')
	}
	if p.Deprecated != "" {
		b.WriteString(`"deprecated":true,`)
	}
	writeObjectSchemaBody(&b, m, pub)
	b.WriteByte('}')
	return b.String()
}

func writeObjectSchemaBody(b *strings.Builder, m *Module, fields []*Field) {
	b.WriteString(`"type":"object","properties":{`)
	var req []string
	for i, f := range fields {
		if i > 0 {
			b.WriteByte(',')
		}
		writeJSONString(b, f.Name)
		b.WriteByte(':')
		writeFieldSchema(b, m, f)
		if !f.Optional {
			req = append(req, f.Name)
		}
	}
	b.WriteByte('}')
	if len(req) > 0 {
		b.WriteString(`,"required":[`)
		for i, r := range req {
			if i > 0 {
				b.WriteByte(',')
			}
			writeJSONString(b, r)
		}
		b.WriteByte(']')
	}
}

func writeFieldSchema(b *strings.Builder, m *Module, f *Field) {
	b.WriteByte('{')
	if f.Description != "" {
		b.WriteString(`"description":`)
		writeJSONString(b, f.Description)
		b.WriteByte(',')
	}
	if f.Deprecated != "" {
		b.WriteString(`"deprecated":true,`)
	}
	writeTypeSchemaBody(b, m, f.Type)
	if f.Example != nil {
		b.WriteString(`,"examples":[`)
		switch f.Example.Tag {
		case "!!str":
			writeJSONString(b, f.Example.Value)
		case "!!int":
			i, _ := strconv.ParseInt(f.Example.Value, 0, 64)
			b.WriteString(strconv.FormatInt(i, 10))
		case "!!float":
			fl, _ := strconv.ParseFloat(f.Example.Value, 64)
			s, _ := formatNumber(fl)
			b.WriteString(s)
		case "!!bool":
			b.WriteString(f.Example.Value)
		}
		b.WriteByte(']')
	}
	b.WriteByte('}')
}

func writeTypeSchemaBody(b *strings.Builder, m *Module, t FieldType) {
	switch t.Kind {
	case KString:
		b.WriteString(`"type":"string"`)
		if t.Format != "" {
			b.WriteString(`,"format":`)
			writeJSONString(b, t.Format)
		}
	case KInteger:
		fmt.Fprintf(b, `"type":"integer","minimum":-%d,"maximum":%d`, int64(MaxSafeInteger), int64(MaxSafeInteger))
	case KInt64:
		b.WriteString(`"type":"string","format":"int64","pattern":"^-?(0|[1-9][0-9]*)$"`)
	case KNumber:
		b.WriteString(`"type":"number"`)
	case KBoolean:
		b.WriteString(`"type":"boolean"`)
	case KDateTime:
		b.WriteString(`"type":"string","format":"date-time"`)
	case KArray:
		b.WriteString(`"type":"array","items":{`)
		writeTypeSchemaBody(b, m, *t.Items)
		b.WriteByte('}')
	case KRef:
		nt := m.TypeByName(t.Ref)
		b.WriteString(`"title":`)
		writeJSONString(b, nt.Name)
		b.WriteByte(',')
		if nt.Description != "" {
			b.WriteString(`"description":`)
			writeJSONString(b, nt.Description)
			b.WriteByte(',')
		}
		if nt.IsEnum {
			b.WriteString(`"type":"string","enum":[`)
			for i, v := range nt.Values {
				if i > 0 {
					b.WriteByte(',')
				}
				writeJSONString(b, v)
			}
			b.WriteByte(']')
		} else {
			writeObjectSchemaBody(b, m, nt.Fields)
		}
	}
}

// yamlQuote renders a string as a YAML double-quoted scalar (JSON escapes are valid YAML).
func yamlQuote(s string) string {
	var b strings.Builder
	writeJSONString(&b, s)
	return b.String()
}

// landscapeRe is the DNS-label rule for a landscape, enforced at render time (Helm) and for a
// literal landscape. Kept in step with dnsLabel in config.go.
const landscapeRe = `^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`

// portalOriginRe is the shape of an error portal origin: https://host[:port] with nothing after.
// host: lower-case DNS labels of 1-63 characters ([a-z0-9], inner '-'), dot-separated, the last
// label starting with a letter (so an IPv4 literal is refused; IPv6 literals cannot match). port:
// 1-5 digits, no leading zero. validPortal adds the two checks a regex cannot make cheaply: host
// at most 253 characters and port at most 65535. Every runtime's site check, the Helm record and
// this tool apply the same rule; conformance/vectors.json "origins" lists accepted and refused
// values every language is driven with. [.] avoids a backslash, so the same text works as a
// Helm template string.
const portalOriginRe = `^https://(([a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?)[.])*[a-z]([-a-z0-9]{0,61}[a-z0-9])?(:[1-9][0-9]{0,4})?$`

// portalMsg is the refusal text (tool, record, runtimes).
const portalMsg = "must be an https origin: https://host[:port], host = lower-case DNS labels (1-63 characters, at most 253 in all, the last starting with a letter; no IP literal), port 1-65535, nothing after"

// validPortal reports whether s is an error portal origin (portalOriginRe, host <= 253, port <= 65535).
func validPortal(s string) bool {
	if !portalRe.MatchString(s) {
		return false
	}
	hostPort := strings.TrimPrefix(s, "https://")
	host, port, hasPort := strings.Cut(hostPort, ":")
	if len(host) > 253 {
		return false
	}
	if hasPort {
		n, err := strconv.Atoi(port)
		return err == nil && n >= 1 && n <= 65535
	}
	return true
}

// RenderRecordYAML renders the Problem CR for one module. landscape == "" renders the Helm
// form: the inputs are .Values.landscape (required, a DNS label) and .Values.errorPortal.url
// (the same error portal origin the service reads at start-up; when unset, the config's
// optional portal is the default, else Helm fails). Otherwise the literal landscape and
// portal are used and must already be valid (ValidLandscape, an https origin).
// Every string-valued field is a YAML double-quoted scalar, so values such as 123, yes or on
// stay strings under YAML 1.1 and 1.2 readers alike.
func RenderRecordYAML(m *Module, cfg *Config, landscape, portal, source string) string {
	l, o := landscape, portal
	var b strings.Builder
	b.WriteString("# Code generated by problemgen " + toolVersion + ". DO NOT EDIT.\n")
	if source != "" {
		b.WriteString("# source: " + source + " sha256:" + m.Def.SHA256 + "\n")
	}
	if landscape == "" {
		b.WriteString("# Helm inputs: the landscape (required, a DNS label) and errorPortal.url (an https origin,\n")
		b.WriteString("# the value the service reads at start-up); everything else is literal.\n")
		b.WriteString(`{{- $landscape := toString (required "problemgen: .Values.landscape is required" .Values.landscape) }}` + "\n")
		b.WriteString(`{{- if not (regexMatch "` + landscapeRe + `" $landscape) }}{{ fail "problemgen: .Values.landscape must be a DNS label (a-z, 0-9, -; at most 63 characters)" }}{{ end }}` + "\n")
		l = "{{ $landscape }}"
		b.WriteString("{{- $errorPortal := .Values.errorPortal | default dict }}\n")
		if cfg.Portal != "" {
			b.WriteString(`{{- $portal := toString (default "` + cfg.Portal + `" $errorPortal.url) }}` + "\n")
		} else {
			b.WriteString(`{{- $portal := toString (required "problemgen: .Values.errorPortal.url is required" $errorPortal.url) }}` + "\n")
		}
		b.WriteString(`{{- $portalHost := regexReplaceAll "^https://([^:]*).*$" $portal "${1}" }}{{- $portalPort := regexReplaceAll "^https://[^:]*:?" $portal "" }}` + "\n")
		b.WriteString(`{{- if or (not (regexMatch "` + portalOriginRe + `" $portal)) (gt (len $portalHost) 253) (gt (atoi $portalPort) 65535) }}{{ fail "problemgen: .Values.errorPortal.url ` + portalMsg + `" }}{{ end }}` + "\n")
		o = "{{ $portal }}"
	}
	q := yamlQuote
	mod, ver := m.Def.Module, m.Def.Version
	b.WriteString("apiVersion: atomi.cloud/v1alpha1\nkind: Problem\nmetadata:\n")
	fmt.Fprintf(&b, "  name: %s\n", q(cfg.Service+"."+mod+"."+l+"."+ver))
	fmt.Fprintf(&b, "  namespace: %s\n", q(cfg.Platform))
	b.WriteString("spec:\n")
	fmt.Fprintf(&b, "  platform: %s\n  service: %s\n  module: %s\n  landscape: %s\n  version: %s\n", q(cfg.Platform), q(cfg.Service), q(mod), q(l), q(ver))
	if len(m.Problems) == 0 {
		b.WriteString("  problems: []\n")
		return yamlSafe(b.String())
	}
	b.WriteString("  problems:\n")
	for _, p := range m.Problems {
		fmt.Fprintf(&b, "    - id: %s\n", q(p.ID))
		fmt.Fprintf(&b, "      type: %s\n", q(TypeURI(o, l, cfg.Platform, cfg.Service, mod, ver, p.ID)))
		fmt.Fprintf(&b, "      title: %s\n", q(p.Title))
		fmt.Fprintf(&b, "      status: %d\n", p.Status)
		fmt.Fprintf(&b, "      recoverable: %v\n", p.Recoverable)
		fmt.Fprintf(&b, "      schema: %s\n", DataSchema(m, p))
	}
	return yamlSafe(b.String())
}

// yamlSafe escapes, as \uXXXX, every character a YAML reader would not carry literally
// inside a double-quoted scalar (C1 controls, NEL and U+2028/2029, which YAML 1.1 readers
// treat as line breaks, U+FEFF, noncharacters). Every such character in the record sits in a
// double-quoted YAML or JSON string, where \uXXXX means the same character. Lint (L242)
// already refuses them in definitions; this is the backstop.
func yamlSafe(s string) string {
	var b strings.Builder
	for _, c := range s {
		if c != '\n' && unsafeTextRune(c) {
			if c > 0xffff {
				fmt.Fprintf(&b, "\\U%08X", c)
			} else {
				fmt.Fprintf(&b, "\\u%04X", c)
			}
			continue
		}
		b.WriteRune(c)
	}
	return b.String()
}

// RenderCatalogJSON renders the landscape-free frontend catalog entries for one module:
// what a client needs to classify a received problem without calling any server.
func RenderCatalogJSON(m *Module, cfg *Config) string {
	var b strings.Builder
	b.WriteString("[\n")
	for i, p := range m.Problems {
		b.WriteString(`  {"platform":`)
		writeJSONString(&b, cfg.Platform)
		b.WriteString(`,"service":`)
		writeJSONString(&b, cfg.Service)
		b.WriteString(`,"module":`)
		writeJSONString(&b, m.Def.Module)
		b.WriteString(`,"version":`)
		writeJSONString(&b, m.Def.Version)
		b.WriteString(`,"id":`)
		writeJSONString(&b, p.ID)
		fmt.Fprintf(&b, `,"status":%d,"recoverable":%v,"title":`, p.Status, p.Recoverable)
		writeJSONString(&b, p.Title)
		b.WriteByte('}')
		if i < len(m.Problems)-1 {
			b.WriteByte(',')
		}
		b.WriteByte('\n')
	}
	b.WriteString("]\n")
	return b.String()
}

var landscapeLabel = regexp.MustCompile(landscapeRe)

// ValidLandscape reports whether s can be a landscape (a DNS label of at most 63 characters).
func ValidLandscape(s string) bool { return landscapeLabel.MatchString(s) }
