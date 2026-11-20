package live

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var daysRe = regexp.MustCompile(`^(-?[0-9]+(?:\.[0-9]+)?)([dw])$`)

// ParseSpan reads a Go duration with the extra units d (24 h) and w (7 d): 90m, 7d, 2w.
func ParseSpan(s string) (time.Duration, error) {
	if m := daysRe.FindStringSubmatch(s); m != nil {
		f, _ := strconv.ParseFloat(m[1], 64)
		unit := 24 * time.Hour
		if m[2] == "w" {
			unit = 7 * 24 * time.Hour
		}
		return time.Duration(f * float64(unit)), nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("%q is not a duration such as 90m, 24h, 7d or 2w", s)
	}
	return d, nil
}

// Window is a time range the user names with --window from..to.
type Window struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}

func parseInstant(s string, now time.Time) (time.Time, error) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "-") {
		d, err := ParseSpan(s[1:])
		if err != nil {
			return time.Time{}, err
		}
		return now.Add(-d), nil
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, s, time.UTC); err == nil {
			return t, nil
		}
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil && n > 100000000 {
		return time.Unix(n, 0).UTC(), nil
	}
	return time.Time{}, fmt.Errorf("%q is not a time (RFC 3339, 2026-11-10, 2026-11-10T14:30, unix seconds, or -6h for six hours ago)", s)
}

// ParseWindow reads `from..to`; each end is a time or a negative span ago. Times are UTC.
func ParseWindow(s string, now time.Time) (Window, error) {
	a, b, ok := strings.Cut(s, "..")
	if !ok {
		return Window{}, fmt.Errorf("--window %q: want from..to, for example 2026-11-10T14:00Z..2026-11-10T15:30Z or -6h..-2h", s)
	}
	from, err := parseInstant(a, now)
	if err != nil {
		return Window{}, fmt.Errorf("--window %q: %v", s, err)
	}
	to, err := parseInstant(b, now)
	if err != nil {
		return Window{}, fmt.Errorf("--window %q: %v", s, err)
	}
	if !to.After(from) {
		return Window{}, fmt.Errorf("--window %q: the end is not after the start", s)
	}
	return Window{From: from, To: to}, nil
}
