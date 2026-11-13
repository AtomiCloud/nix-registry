package main

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// ---- Canonical primitives (SPEC §6). Every runtime implements exactly these. ----

// MaxSafeInteger bounds `integer` fields (JSON numbers exact in every language, A114).
const MaxSafeInteger = 9007199254740991

// writeJSONString writes s as a canonical JSON string: escape '"', '\\' and U+0000..U+001F
// (short forms \b \t \n \f \r, otherwise \u00xx lowercase); everything else literal UTF-8;
// invalid UTF-8 becomes U+FFFD per invalid byte (UTF-16 runtimes: per lone surrogate).
func writeJSONString(b *strings.Builder, s string) {
	b.WriteByte('"')
	for len(s) > 0 {
		r, n := utf8.DecodeRuneInString(s)
		s = s[n:]
		switch {
		case r == '"':
			b.WriteString(`\"`)
		case r == '\\':
			b.WriteString(`\\`)
		case r < 0x20:
			b.WriteString(controlEscape(r))
		default:
			b.WriteRune(r) // RuneError writes U+FFFD
		}
	}
	b.WriteByte('"')
}

func controlEscape(r rune) string {
	switch r {
	case '\b':
		return `\b`
	case '\t':
		return `\t`
	case '\n':
		return `\n`
	case '\f':
		return `\f`
	case '\r':
		return `\r`
	}
	return fmt.Sprintf(`\u%04x`, r)
}

// logEscape is applied to every value inserted into a `log` template: control characters
// (U+0000..U+001F, U+007F, U+2028, U+2029) and '\\' are escaped so a value can never forge
// a new log line. Values inserted into `detail` are inserted verbatim (JSON escaping
// protects the wire).
func logEscape(s string) string {
	var b strings.Builder
	for len(s) > 0 {
		r, n := utf8.DecodeRuneInString(s)
		s = s[n:]
		switch {
		case r == '\\':
			b.WriteString(`\\`)
		case r < 0x20:
			b.WriteString(controlEscape(r))
		case r == 0x7f || r == 0x2028 || r == 0x2029:
			fmt.Fprintf(&b, `\u%04x`, r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// sanitize maps invalid UTF-8 to U+FFFD (per invalid byte), as the JSON writer does.
func sanitize(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	var b strings.Builder
	for len(s) > 0 {
		r, n := utf8.DecodeRuneInString(s)
		s = s[n:]
		b.WriteRune(r)
	}
	return b.String()
}

// formatNumber renders a finite float64 exactly as ECMAScript Number.prototype.toString
// (shortest round-trip digits; plain notation for 1e-7 < |x| < 1e21; -0 renders "0").
func formatNumber(f float64) (string, error) {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return "", fmt.Errorf("non-finite number %v cannot be serialised", f)
	}
	if f == 0 {
		return "0", nil
	}
	neg := f < 0
	if neg {
		f = -f
	}
	e := strconv.FormatFloat(f, 'e', -1, 64) // d.ddde±XX
	mant, expS, _ := strings.Cut(e, "e")
	digits := strings.Replace(mant, ".", "", 1)
	x, _ := strconv.Atoi(expS)
	k := len(digits)
	n := x + 1 // value = 0.digits × 10^n
	var s string
	switch {
	case k <= n && n <= 21:
		s = digits + strings.Repeat("0", n-k)
	case 0 < n && n <= 21:
		s = digits[:n] + "." + digits[n:]
	case -6 < n && n <= 0:
		s = "0." + strings.Repeat("0", -n) + digits
	default:
		sign := "+"
		if n-1 < 0 {
			sign = "-"
		}
		ex := n - 1
		if ex < 0 {
			ex = -ex
		}
		if k == 1 {
			s = digits + "e" + sign + strconv.Itoa(ex)
		} else {
			s = digits[:1] + "." + digits[1:] + "e" + sign + strconv.Itoa(ex)
		}
	}
	if neg {
		s = "-" + s
	}
	return s, nil
}

// formatDateTime renders an instant as UTC with exactly 3 fractional digits (A114).
func formatDateTime(t time.Time) string {
	return t.UTC().Truncate(time.Millisecond).Format("2006-01-02T15:04:05.000Z")
}

var dateTimeRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d{1,9})?(Z|[+-](0\d|1\d|2[0-3]):[0-5]\d)$`)

func parseCanonicalDateTime(s string) (time.Time, error) {
	if !dateTimeRe.MatchString(s) {
		return time.Time{}, fmt.Errorf("not an RFC 3339 date-time: %q", s)
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return t, err
	}
	if y := t.UTC().Year(); y < 1 || y > 9999 {
		return t, fmt.Errorf("year %d outside 0001..9999", y)
	}
	return t, nil
}

var int64Re = regexp.MustCompile(`^-?(0|[1-9][0-9]*)$`)

func parseInt64String(s string) (int64, error) {
	if !int64Re.MatchString(s) {
		return 0, fmt.Errorf("not a canonical integer string: %q", s)
	}
	return strconv.ParseInt(s, 10, 64)
}

var (
	uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	dateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
)

func checkFormat(format, s string) error {
	switch format {
	case "uuid":
		if !uuidRe.MatchString(s) {
			return fmt.Errorf("not a lowercase uuid: %q", s)
		}
	case "date":
		if !dateRe.MatchString(s) {
			return fmt.Errorf("not YYYY-MM-DD: %q", s)
		}
		if _, err := time.Parse("2006-01-02", s); err != nil {
			return err
		}
	case "uri":
		if !strings.Contains(s, ":") {
			return fmt.Errorf("not an absolute URI: %q", s)
		}
	}
	return nil
}

// TypeURI is the single-source address builder (C0 §2): the reference every runtime mirrors.
func TypeURI(portal, landscape, platform, service, module, version, id string) string {
	return strings.TrimRight(portal, "/") + "/docs/" + landscape + "/" + platform + "/" + service + "/" + module + "/" + version + "/" + id
}

// ---- Oracle: renders a problem from a JSON input vector, independent of any generated code. ----

// Rendered is the oracle output for one vector.
type Rendered struct {
	Wire   string
	Log    string
	Detail string
	Status int
}

// RenderVector renders wire JSON and log line for a problem with the given input data
// (a JV object). Errors mean "every language must refuse this input".
func RenderVector(m *Module, cfg *Config, landscape string, p *Problem, data *JV, instance string) (Rendered, error) {
	vals := map[string]*JV{}
	if data != nil {
		if data.Kind != 'o' {
			return Rendered{}, fmt.Errorf("data must be an object")
		}
		for i, k := range data.Keys {
			if p.Field(k) == nil {
				return Rendered{}, fmt.Errorf("unknown field %q", k)
			}
			vals[k] = data.Vals[i]
		}
	}
	for _, f := range p.Data {
		v, ok := vals[f.Name]
		if (!ok || v.Kind == 'z') && !f.Optional {
			return Rendered{}, fmt.Errorf("required field %q missing", f.Name)
		}
	}
	var b strings.Builder
	b.WriteString(`{"type":`)
	writeJSONString(&b, TypeURI(cfg.Portal, landscape, cfg.Platform, cfg.Service, m.Def.Module, m.Def.Version, p.ID))
	b.WriteString(`,"title":`)
	writeJSONString(&b, p.Title)
	b.WriteString(`,"status":`)
	b.WriteString(strconv.Itoa(p.Status))
	detail, err := renderTemplate(m, p, p.Detail, vals, false)
	if err != nil {
		return Rendered{}, err
	}
	b.WriteString(`,"detail":`)
	writeJSONString(&b, detail)
	if instance != "" {
		b.WriteString(`,"instance":`)
		writeJSONString(&b, instance)
	}
	if len(p.Data) > 0 && len(p.PublicData()) > 0 {
		b.WriteString(`,"data":`)
		if err := writeObject(&b, m, p.PublicData(), vals); err != nil {
			return Rendered{}, err
		}
	}
	b.WriteByte('}')
	logLine, err := renderTemplate(m, p, p.Log, vals, true)
	if err != nil {
		return Rendered{}, err
	}
	return Rendered{Wire: b.String(), Log: logLine, Detail: detail, Status: p.Status}, nil
}

func writeObject(b *strings.Builder, m *Module, fields []*Field, vals map[string]*JV) error {
	b.WriteByte('{')
	first := true
	for _, f := range fields {
		v, ok := vals[f.Name]
		if !ok || v.Kind == 'z' {
			continue
		}
		if !first {
			b.WriteByte(',')
		}
		first = false
		writeJSONString(b, f.Name)
		b.WriteByte(':')
		if err := writeValue(b, m, f.Type, v); err != nil {
			return fmt.Errorf("field %q: %w", f.Name, err)
		}
	}
	b.WriteByte('}')
	return nil
}

func writeValue(b *strings.Builder, m *Module, t FieldType, v *JV) error {
	s, err := scalarText(m, t, v)
	if err == errNotScalar {
		switch t.Kind {
		case KArray:
			if v.Kind != 'a' {
				return fmt.Errorf("expected array")
			}
			b.WriteByte('[')
			for i, it := range v.Arr {
				if i > 0 {
					b.WriteByte(',')
				}
				if err := writeValue(b, m, *t.Items, it); err != nil {
					return err
				}
			}
			b.WriteByte(']')
			return nil
		case KRef:
			nt := m.TypeByName(t.Ref)
			if v.Kind != 'o' {
				return fmt.Errorf("expected object %s", t.Ref)
			}
			vals := map[string]*JV{}
			for i, k := range v.Keys {
				vals[k] = v.Vals[i]
			}
			for _, f := range nt.Fields {
				if fv, ok := vals[f.Name]; (!ok || fv.Kind == 'z') && !f.Optional {
					return fmt.Errorf("%s: required field %q missing", nt.Name, f.Name)
				}
			}
			return writeObject(b, m, nt.Fields, vals)
		}
	}
	if err != nil {
		return err
	}
	switch {
	case t.Kind == KString || t.Kind == KDateTime || t.Kind == KInt64 || t.Kind == KRef:
		writeJSONString(b, s)
	default:
		b.WriteString(s)
	}
	return nil
}

var errNotScalar = fmt.Errorf("not scalar")

// scalarText returns the canonical text of a scalar value: for strings the (sanitized) string
// itself, for numbers the canonical number text, for int64 the decimal digits.
func scalarText(m *Module, t FieldType, v *JV) (string, error) {
	switch t.Kind {
	case KString:
		if v.Kind != 's' {
			return "", fmt.Errorf("expected string")
		}
		return v.GoString(), nil
	case KInteger:
		if v.Kind != 'n' {
			return "", fmt.Errorf("expected integer")
		}
		i, err := strconv.ParseInt(v.Num, 10, 64)
		if err != nil || i > MaxSafeInteger || i < -MaxSafeInteger {
			return "", fmt.Errorf("integer %s outside ±(2^53-1)", v.Num)
		}
		return strconv.FormatInt(i, 10), nil
	case KInt64:
		if v.Kind != 's' {
			return "", fmt.Errorf("expected int64 as a string")
		}
		i, err := parseInt64String(v.GoString())
		if err != nil {
			return "", err
		}
		return strconv.FormatInt(i, 10), nil
	case KNumber:
		if v.Kind == 'o' {
			if f, ok := v.Get("$float"); ok {
				if f.GoString() == "-0" {
					return formatNumber(math.Copysign(0, -1))
				}
				return "", fmt.Errorf("non-finite number %s", f.GoString())
			}
		}
		if v.Kind != 'n' {
			return "", fmt.Errorf("expected number")
		}
		f, err := strconv.ParseFloat(v.Num, 64)
		if err != nil {
			return "", err
		}
		return formatNumber(f)
	case KBoolean:
		if v.Kind != 'b' {
			return "", fmt.Errorf("expected boolean")
		}
		return strconv.FormatBool(v.Bool), nil
	case KDateTime:
		if v.Kind != 's' {
			return "", fmt.Errorf("expected date-time string")
		}
		tm, err := parseCanonicalDateTime(v.GoString())
		if err != nil {
			return "", err
		}
		return formatDateTime(tm), nil
	case KRef:
		nt := m.TypeByName(t.Ref)
		if nt != nil && nt.IsEnum {
			if v.Kind != 's' {
				return "", fmt.Errorf("expected enum string")
			}
			s := v.GoString()
			for _, ev := range nt.Values {
				if ev == s {
					return s, nil
				}
			}
			return "", fmt.Errorf("%q is not a value of %s", s, nt.Name)
		}
	}
	return "", errNotScalar
}

func renderTemplate(m *Module, p *Problem, t *Template, vals map[string]*JV, forLog bool) (string, error) {
	var b strings.Builder
	for _, part := range t.Parts {
		if !part.IsPlaceholder() {
			b.WriteString(part.Lit)
			continue
		}
		f := p.Field(part.Field)
		v, ok := vals[part.Field]
		if !ok || v.Kind == 'z' {
			b.WriteString(part.Default)
			continue
		}
		var s string
		if part.Count {
			if v.Kind != 'a' {
				return "", fmt.Errorf("field %q: expected array", f.Name)
			}
			s = strconv.Itoa(len(v.Arr))
		} else {
			var err error
			s, err = scalarText(m, f.Type, v)
			if err != nil {
				return "", fmt.Errorf("field %q: %w", f.Name, err)
			}
		}
		s = sanitize(s)
		if forLog {
			s = logEscape(s)
		}
		b.WriteString(s)
	}
	return b.String(), nil
}
