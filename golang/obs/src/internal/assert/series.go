// Package assert is `obs assert` (concept section 11.4): it counts the real series per metric
// in a series export taken after an e2e run and fails when a count is above the contract's
// worst case, which means a hole (a cast, reflection, a raw OTel call).
package assert

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

// Series is one stored series: its name and its labels (without __name__).
type Series struct {
	Name   string
	Labels map[string]string
}

// key is the identity of a series: name plus sorted labels, so an export that repeats a series
// (one line per chunk) counts it once.
func (s Series) key() string {
	ks := make([]string, 0, len(s.Labels))
	for k := range s.Labels {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	var sb strings.Builder
	sb.WriteString(s.Name)
	for _, k := range ks {
		sb.WriteString("\x00" + k + "\x01" + s.Labels[k])
	}
	return sb.String()
}

// ReadSeries reads a VictoriaMetrics series export file in either form:
//
//   - /api/v1/series: one JSON document {"status":"success","data":[{"__name__":"m","l":"v"},...]}
//   - /api/v1/export: one JSON object per line {"metric":{"__name__":"m","l":"v"},"values":[..],"timestamps":[..]}
//
// Series are returned once each, in input order.
func ReadSeries(path string) ([]Series, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return parseSeries(path, f)
}

// ParseSeries reads a /api/v1/series answer (or export lines) from r; name labels errors.
// obs live usage reads the same document from a running store, so it shares this reader.
func ParseSeries(name string, r io.Reader) ([]Series, error) { return parseSeries(name, r) }

func parseSeries(name string, r io.Reader) ([]Series, error) {
	dec := json.NewDecoder(bufio.NewReaderSize(r, 1<<20))
	seen := map[string]bool{}
	var out []Series
	add := func(m map[string]string) error {
		n, ok := m["__name__"]
		if !ok || n == "" {
			return fmt.Errorf("a series has no __name__")
		}
		labels := make(map[string]string, len(m))
		for k, v := range m {
			if k != "__name__" {
				labels[k] = v
			}
		}
		s := Series{Name: n, Labels: labels}
		if k := s.key(); !seen[k] {
			seen[k] = true
			out = append(out, s)
		}
		return nil
	}
	docs := 0
	for {
		var doc struct {
			Status string              `json:"status"`
			Error  string              `json:"error"`
			Data   []map[string]string `json:"data"`
			Metric map[string]string   `json:"metric"`
		}
		err := dec.Decode(&doc)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%s: not a series export (/api/v1/series JSON or /api/v1/export lines): %v", name, err)
		}
		docs++
		switch {
		case doc.Status != "" && doc.Status != "success":
			return nil, fmt.Errorf("%s: the export is a failed response: status %q %s", name, doc.Status, doc.Error)
		case doc.Data != nil:
			for _, m := range doc.Data {
				if err := add(m); err != nil {
					return nil, fmt.Errorf("%s: %v", name, err)
				}
			}
		case doc.Metric != nil:
			if err := add(doc.Metric); err != nil {
				return nil, fmt.Errorf("%s: %v", name, err)
			}
		case doc.Status == "success":
			// an empty /api/v1/series answer: data is [] or null
		default:
			return nil, fmt.Errorf("%s: JSON document %d has neither \"data\" (/api/v1/series) nor \"metric\" (/api/v1/export)", name, docs)
		}
	}
	if docs == 0 {
		return nil, fmt.Errorf("%s: the export is empty", name)
	}
	return out, nil
}
