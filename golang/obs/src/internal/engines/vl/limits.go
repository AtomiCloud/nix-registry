package vl

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/VictoriaMetrics/VictoriaMetrics/lib/cgroup"
	"github.com/VictoriaMetrics/VictoriaMetrics/lib/flagutil"
	"gopkg.in/yaml.v3"
)

// Limits mirrors the query frontend flags of VictoriaLogs v1.54.0.
// Check satisfies alerts.QueryGuard; NewWithLimits also enforces runtime limits.
// A zero Limits disables limits for programmatic callers. Use DefaultLimits or
// LoadLimits for the pinned server defaults, including its 16KiB query limit.
type Limits struct {
	MaxQueryLen           int
	MaxQueryTimeRange     time.Duration
	MaxQueryDuration      time.Duration
	MaxQueueDuration      time.Duration
	MaxConcurrentRequests int
	SelectDisabled        bool
	queryLenSet           bool // an explicit maxQueryLen=0 rejects every nonempty query
	runtimeSet            bool // server zero durations/concurrency are not unlimited
}

// DefaultLimits follows app/vlselect/{main.go,logsql/logsql.go} at v1.54.0.
func DefaultLimits() Limits {
	n := cgroup.AvailableCPUs()
	if n <= 4 {
		n *= 2
	}
	return Limits{MaxQueryLen: 16 * 1024, MaxQueryDuration: 30 * time.Second,
		MaxQueueDuration: 10 * time.Second, MaxConcurrentRequests: min(n, 16),
		queryLenSet: true, runtimeSet: true}
}

// LoadLimits reads the consumer's extracted VictoriaLogs chart args. A missing
// implicit file returns one note; an explicitly requested missing file is an error.
func LoadLimits(dir, path string) (Limits, string, error) {
	explicit := path != ""
	if !explicit {
		path = filepath.Join(dir, "victorialogs-settings.yaml")
	}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) && !explicit {
		return DefaultLimits(), "no " + path + "; using VictoriaLogs v1.54.0 query defaults (extract deployed chart args to this file or use --vl-settings)", nil
	}
	if err != nil {
		return Limits{}, "", err
	}
	l, err := ParseLimits(b)
	if err != nil {
		return Limits{}, "", fmt.Errorf("%s: %w", path, err)
	}
	return l, "", nil
}

// ParseLimits accepts a YAML flag map, a list of one-flag maps, or a list of
// -flag=value strings. Non-query flags are irrelevant to this embedded store.
// Unknown search flags fail closed so a new server refusal is never ignored.
func ParseLimits(b []byte) (Limits, error) {
	l := DefaultLimits()
	var doc yaml.Node
	d := yaml.NewDecoder(strings.NewReader(string(b)))
	if err := d.Decode(&doc); err != nil && err != io.EOF {
		return l, err
	}
	var extra yaml.Node
	if err := d.Decode(&extra); err != io.EOF {
		return l, fmt.Errorf("want exactly one settings YAML document")
	}
	if len(doc.Content) == 0 {
		return l, nil
	}
	seen := map[string]bool{}
	apply := func(key, value string) error {
		key = strings.TrimLeft(key, "-")
		if key == "" {
			return fmt.Errorf("empty VictoriaLogs flag name")
		}
		if seen[key] {
			return fmt.Errorf("duplicate -%s", key)
		}
		seen[key] = true
		var err error
		switch key {
		case "search.maxQueryLen":
			var size int64
			size, err = flagutil.ParseBytes(value)
			if err == nil && (size < 0 || int64(int(size)) != size) {
				err = fmt.Errorf("must fit a nonnegative int")
			}
			l.MaxQueryLen = int(size)
		case "search.maxQueryTimeRange":
			var duration flagutil.ExtendedDuration
			err = duration.Set(value)
			l.MaxQueryTimeRange = duration.Duration()
		case "search.maxQueryDuration":
			l.MaxQueryDuration, err = time.ParseDuration(value)
		case "search.maxQueueDuration":
			l.MaxQueueDuration, err = time.ParseDuration(value)
		case "search.maxConcurrentRequests":
			l.MaxConcurrentRequests, err = strconv.Atoi(value)
			if err == nil && l.MaxConcurrentRequests < 0 {
				err = fmt.Errorf("must be nonnegative")
			}
		case "select.disable":
			l.SelectDisabled, err = strconv.ParseBool(value)
		case "search.allowPartialResponse":
			_, err = strconv.ParseBool(value) // cluster-only
		case "search.logSlowQueryDuration":
			_, err = time.ParseDuration(value) // logging only
		default:
			if strings.HasPrefix(key, "search.") {
				return fmt.Errorf("unsupported query setting -%s", key)
			}
		}
		if err != nil {
			return fmt.Errorf("invalid -%s: %w", key, err)
		}
		return nil
	}
	var walk func(*yaml.Node) error
	walk = func(n *yaml.Node) error {
		switch n.Kind {
		case yaml.MappingNode:
			for i := 0; i < len(n.Content); i += 2 {
				k, v := n.Content[i], n.Content[i+1]
				if k.Kind != yaml.ScalarNode || v.Kind != yaml.ScalarNode {
					return fmt.Errorf("settings must contain scalar flag/value pairs")
				}
				if err := apply(k.Value, v.Value); err != nil {
					return err
				}
			}
		case yaml.SequenceNode:
			for _, c := range n.Content {
				if c.Kind == yaml.ScalarNode {
					k, v, ok := strings.Cut(c.Value, "=")
					if !ok {
						return fmt.Errorf("list flags must use -flag=value or a flag/value map")
					}
					if err := apply(k, v); err != nil {
						return err
					}
				} else if c.Kind == yaml.MappingNode && len(c.Content) == 2 {
					if err := walk(c); err != nil {
						return err
					}
				} else {
					return fmt.Errorf("each settings list item must be one flag")
				}
			}
		default:
			return fmt.Errorf("settings must be a YAML flag map or list")
		}
		return nil
	}
	err := walk(doc.Content[0])
	return l, err
}

// Check applies length to the original query, before threshold/filter rewriting,
// then range to the actual intersection with any authored _time filter.
func (l Limits) Check(query string, start, end time.Time) error {
	if l.SelectDisabled {
		return fmt.Errorf("query refused by -select.disable=true")
	}
	if (l.queryLenSet || l.MaxQueryLen > 0) && len(query) > l.MaxQueryLen {
		return fmt.Errorf("query %q: -search.maxQueryLen=%d exceeded (%d bytes)", query, l.MaxQueryLen, len(query))
	}
	if l.MaxQueryTimeRange > 0 {
		q, err := Parse(query, end)
		if err != nil {
			return err
		}
		q.q.AddTimeFilter(start.UnixNano(), end.UnixNano()-1)
		a, b := q.q.GetFilterTimeRange()
		if b > a && (b-a < 0 || b-a > l.MaxQueryTimeRange.Nanoseconds()) {
			return fmt.Errorf("query %q: -search.maxQueryTimeRange=%s exceeded", query, l.MaxQueryTimeRange)
		}
	}
	return nil
}
