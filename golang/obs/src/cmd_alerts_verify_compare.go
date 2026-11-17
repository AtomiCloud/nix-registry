package main

// Canonical comparison for OBS_VERIFY_PRUNE=1 (obs alerts self-check, see
// cmd_alerts_verify.go). The two child runs write JSON; this file turns each into
// a keyed tree and reports every difference with the alert, run, minute, instance
// or query-input it sits under. Numbers compare exactly (bit-for-bit as float64,
// -0 differs from 0). NaN and +/-Inf are strings in obs JSON (the jf rule) and
// compare as strings, so NaN = NaN, +Inf = +Inf, and NaN against a number is red.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

// pruneDiff is one difference. Path names where it is, outermost first.
type pruneDiff struct {
	Path   []string
	Pruned string // "(missing)" when the pruned run has no such entry
	Full   string // "(missing)" when the unpruned run has no such entry
}

func (d pruneDiff) String() string {
	return fmt.Sprintf("%s: pruned=%s full=%s", strings.Join(d.Path, " > "), d.Pruned, d.Full)
}

const missing = "(missing)"

// cmpStats counts how many leaf values were compared.
type cmpStats struct{ Compared int }

func decodeJSON(raw []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return v, nil
}

// sameNumber compares exactly: equal float64 bits after parsing, so 1 and 1.0 match.
func sameNumber(a, b json.Number) bool {
	x, errX := strconv.ParseFloat(string(a), 64)
	y, errY := strconv.ParseFloat(string(b), 64)
	if errX != nil || errY != nil {
		return a == b
	}
	return math.Float64bits(x) == math.Float64bits(y)
}

func render(v any) string {
	switch t := v.(type) {
	case nil:
		return "null"
	case string:
		return strconv.Quote(t)
	case json.Number:
		return string(t)
	case bool:
		return strconv.FormatBool(t)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	s := string(b)
	if len(s) > 160 {
		s = s[:157] + "..."
	}
	return s
}

// diffTree walks both trees. Maps are keyed (union of keys, sorted), arrays are
// compared by index, leaves exactly. Anything present on one side only, or of a
// different kind, is a difference.
func diffTree(path []string, pruned, full any, st *cmpStats, out *[]pruneDiff) {
	here := func() []string { return append([]string(nil), path...) }
	switch p := pruned.(type) {
	case map[string]any:
		f, ok := full.(map[string]any)
		if !ok {
			*out = append(*out, pruneDiff{here(), render(pruned), render(full)})
			return
		}
		keys := map[string]bool{}
		for k := range p {
			keys[k] = true
		}
		for k := range f {
			keys[k] = true
		}
		sorted := make([]string, 0, len(keys))
		for k := range keys {
			sorted = append(sorted, k)
		}
		sort.Strings(sorted)
		for _, k := range sorted {
			pv, pok := p[k]
			fv, fok := f[k]
			sub := append(here(), k)
			switch {
			case pok && !fok:
				*out = append(*out, pruneDiff{sub, render(pv), missing})
			case !pok && fok:
				*out = append(*out, pruneDiff{sub, missing, render(fv)})
			default:
				diffTree(sub, pv, fv, st, out)
			}
		}
	case []any:
		f, ok := full.([]any)
		if !ok {
			*out = append(*out, pruneDiff{here(), render(pruned), render(full)})
			return
		}
		n := max(len(p), len(f))
		for i := 0; i < n; i++ {
			sub := append(here(), fmt.Sprintf("[%d]", i))
			switch {
			case i >= len(f):
				*out = append(*out, pruneDiff{sub, render(p[i]), missing})
			case i >= len(p):
				*out = append(*out, pruneDiff{sub, missing, render(f[i])})
			default:
				diffTree(sub, p[i], f[i], st, out)
			}
		}
	case json.Number:
		st.Compared++
		if f, ok := full.(json.Number); !ok || !sameNumber(p, f) {
			*out = append(*out, pruneDiff{here(), render(pruned), render(full)})
		}
	case string:
		st.Compared++
		if f, ok := full.(string); !ok || f != p {
			*out = append(*out, pruneDiff{here(), render(pruned), render(full)})
		}
	case bool:
		st.Compared++
		if f, ok := full.(bool); !ok || f != p {
			*out = append(*out, pruneDiff{here(), render(pruned), render(full)})
		}
	case nil:
		st.Compared++
		if full != nil {
			*out = append(*out, pruneDiff{here(), render(pruned), render(full)})
		}
	default:
		*out = append(*out, pruneDiff{here(), render(pruned), render(full)})
	}
}

// minuteKey names a step by its UTC time and, when the base is known, its offset.
func minuteKey(ms int64, base time.Time) string {
	t := time.UnixMilli(ms).UTC()
	k := "minute " + t.Format(time.RFC3339)
	if !base.IsZero() {
		k += " (+" + t.Sub(base).String() + " from base)"
	}
	return k
}

// normalizeSteps turns the --steps JSON ([]AlertRun) into a tree keyed by
// "alert <id>" > "run <name>" > "minute <time>" so a diff names all three.
func normalizeSteps(raw []byte, base time.Time) (map[string]any, error) {
	v, err := decodeJSON(raw)
	if err != nil {
		return nil, fmt.Errorf("steps JSON: %w", err)
	}
	runs, ok := v.([]any)
	if !ok {
		if v == nil {
			return map[string]any{}, nil
		}
		return nil, fmt.Errorf("steps JSON: want an array of alert runs")
	}
	out := map[string]any{}
	for i, r := range runs {
		m, ok := r.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("steps JSON: run %d is not an object", i)
		}
		alert, _ := m["alert"].(string)
		run, _ := m["run"].(string)
		byAlert, _ := out["alert "+alert].(map[string]any)
		if byAlert == nil {
			byAlert = map[string]any{}
			out["alert "+alert] = byAlert
		}
		rec := map[string]any{}
		for k, val := range m {
			if k == "alert" || k == "run" || k == "steps" {
				continue
			}
			if k == "transitions" {
				val = sortedPruneItems(val)
			}
			rec[k] = val
		}
		steps := map[string]any{}
		if list, ok := m["steps"].([]any); ok {
			for _, s := range list {
				sm, ok := s.(map[string]any)
				if !ok {
					continue
				}
				ms := int64(0)
				if n, ok := sm["t"].(json.Number); ok {
					ms, _ = n.Int64()
				}
				entry := map[string]any{}
				for k, val := range sm {
					if k != "t" {
						if k == "err" {
							val = sortedPruneItems(val)
						}
						entry[k] = val
					}
				}
				steps[minuteKey(ms, base)] = entry
			}
		}
		rec["steps"] = steps
		byAlert["run "+run] = rec
	}
	return out, nil
}

// normalizeVerdicts keys the --out verdict list by alert, run and expectation.
func normalizeVerdicts(raw []byte) (map[string]any, error) {
	v, err := decodeJSON(raw)
	if err != nil {
		return nil, fmt.Errorf("out JSON: %w", err)
	}
	top, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("out JSON: want an object")
	}
	out := map[string]any{"failed": top["failed"]}
	list, _ := top["verdicts"].([]any)
	for i, e := range list {
		m, ok := e.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("out JSON: verdict %d is not an object", i)
		}
		out[fmt.Sprintf("alert %v > run %v > expect %v", m["alert"], m["run"], m["expect"])] = m
	}
	return out, nil
}

// normalizeRecorded keys the --recorded rows by label set and first timestamp: one
// series per run shares its labels, and the row order differs between processes.
func normalizeRecorded(raw []byte) (map[string]any, error) {
	v, err := decodeJSON(raw)
	if err != nil {
		return nil, fmt.Errorf("recorded JSON: %w", err)
	}
	out := map[string]any{}
	rows, _ := v.([]any)
	for i, r := range rows {
		m, ok := r.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("recorded JSON: row %d is not an object", i)
		}
		labels, err := json.Marshal(m["labels"])
		if err != nil {
			return nil, err
		}
		key := "series " + string(labels)
		if ts, ok := m["timestamps"].([]any); ok && len(ts) > 0 {
			key += " from t=" + render(ts[0])
		}
		if _, dup := out[key]; dup {
			return nil, fmt.Errorf("recorded JSON: two rows share %s", key)
		}
		out[key] = m
	}
	return out, nil
}

// baseFromOut reads base_ms from the Check JSON; ok is false when the field is absent.
func baseFromOut(raw []byte) (ms int64, ok bool) {
	var o struct {
		BaseMs *int64 `json:"base_ms"`
	}
	if json.Unmarshal(raw, &o) != nil || o.BaseMs == nil {
		return 0, false
	}
	return *o.BaseMs, true
}

// Cross-instance transitions and simultaneous errors have no iteration order.
func sortedPruneItems(v any) any {
	items, ok := v.([]any)
	if !ok {
		return v
	}
	out := append([]any(nil), items...)
	sort.Slice(out, func(i, j int) bool {
		a, _ := json.Marshal(out[i])
		b, _ := json.Marshal(out[j])
		return string(a) < string(b)
	})
	return out
}
