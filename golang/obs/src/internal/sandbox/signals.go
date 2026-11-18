package sandbox

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/AtomiCloud/obs/internal/seed"
	"github.com/google/pprof/profile"
)

func otlpAttrs(attrs map[string]seed.TAttrVal) []any {
	keys := make([]string, 0, len(attrs))
	for k := range attrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]any, 0, len(keys))
	for _, k := range keys {
		v := attrs[k]
		var val any
		switch v.Kind {
		case 0:
			val = map[string]any{"stringValue": v.S}
		case 1:
			val = map[string]any{"intValue": strconv.FormatInt(v.I, 10)}
		case 2:
			val = map[string]any{"doubleValue": v.F}
		case 3:
			val = map[string]any{"boolValue": v.B}
		}
		out = append(out, map[string]any{"key": k, "value": val})
	}
	return out
}

// LoadTraces sends seed traces to Tempo's OTLP/HTTP receiver (not its query port).
// Trace and span IDs are hex strings per OTLP JSON, and nanoseconds/int64s are
// decimal strings. Batches stay bounded and partial-success rejections fail setup.
func LoadTraces(ctx context.Context, hc *http.Client, endpoint string, ds *seed.Dataset) error {
	var resources []any
	flush := func() error {
		if len(resources) == 0 {
			return nil
		}
		b, err := json.Marshal(map[string]any{"resourceSpans": resources})
		if err != nil {
			return err
		}
		result, err := postResult(ctx, hc, endpoint+"/v1/traces", "application/json", b)
		if err != nil {
			return err
		}
		var response struct {
			PartialSuccess struct {
				RejectedSpans json.RawMessage `json:"rejectedSpans"`
				ErrorMessage  string          `json:"errorMessage"`
			} `json:"partialSuccess"`
		}
		if err := json.Unmarshal(result, &response); err != nil {
			return fmt.Errorf("Tempo OTLP response: %w", err)
		}
		rejected := strings.Trim(string(response.PartialSuccess.RejectedSpans), "\"")
		if response.PartialSuccess.ErrorMessage != "" || (rejected != "" && rejected != "0") {
			return fmt.Errorf("Tempo rejected seed spans: %s", result)
		}
		resources = nil
		return nil
	}
	for _, trace := range ds.Traces {
		for _, span := range trace.Spans {
			attrs := map[string]seed.TAttrVal{"service.name": seed.TStr(span.Service)}
			for k, v := range span.Resource {
				attrs[k] = v
			}
			model := map[string]any{"traceId": hex.EncodeToString(trace.TraceID[:]), "spanId": hex.EncodeToString(span.SpanID[:]), "name": span.Name, "kind": span.Kind,
				"startTimeUnixNano": strconv.FormatUint(span.StartNs, 10), "endTimeUnixNano": strconv.FormatUint(span.StartNs+span.DurNs, 10),
				"attributes": otlpAttrs(span.Attrs), "status": map[string]int{"code": span.Status}}
			if span.ParentID != ([8]byte{}) {
				model["parentSpanId"] = hex.EncodeToString(span.ParentID[:])
			}
			resources = append(resources, map[string]any{"resource": map[string]any{"attributes": otlpAttrs(attrs)},
				"scopeSpans": []any{map[string]any{"scope": map[string]string{"name": "obs-seed"}, "spans": []any{model}}}})
			if len(resources) == 256 {
				if err := flush(); err != nil {
					return err
				}
			}
		}
	}
	return flush()
}

// LoadProfiles writes three synthetic CPU pprofs per (service, span name), across
// the seed window. Each is main -> obs.seed.work -> <span>, with deterministic
// 10/20/30 ms CPU samples and span_name labels. It is illustrative, not a measured
// application profile. Without traces one obs.seed.work span is used.
func LoadProfiles(ctx context.Context, hc *http.Client, endpoint string, sd *seed.Seed, ds *seed.Dataset) error {
	type target struct{ service, span string }
	seen := map[target]bool{}
	for _, tr := range ds.Traces {
		for _, sp := range tr.Spans {
			seen[target{sp.Service, sp.Name}] = true
		}
	}
	if len(seen) == 0 {
		service := sd.Resource["service.name"]
		if service == "" {
			service = sd.Resource["durian.cloud.service"]
		}
		if service == "" {
			service = "obs-seed"
		}
		seen[target{service, "obs.seed.work"}] = true
	}
	var targets []target
	for k := range seen {
		targets = append(targets, k)
	}
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].service != targets[j].service {
			return targets[i].service < targets[j].service
		}
		return targets[i].span < targets[j].span
	})
	for _, target := range targets {
		labels := map[string]string{"service_name": target.service}
		for k, v := range sd.Resource {
			if strings.HasPrefix(k, "durian.cloud.") {
				labels[strings.ReplaceAll(k, ".", "_")] = v
			}
		}
		var keys, parts []string
		for k := range labels {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			// The legacy ingest endpoint does not unquote label values.
			if strings.ContainsAny(labels[k], "{},\n\r") {
				return fmt.Errorf("profile label %s value %q cannot be represented by Pyroscope ingest", k, labels[k])
			}
			parts = append(parts, k+"="+labels[k])
		}
		for j := 0; j < 3; j++ {
			at := ds.Start.Add(ds.End.Sub(ds.Start) * time.Duration(2*j+1) / 6)
			duration := min(10*time.Second, ds.End.Sub(ds.Start)/6)
			if duration <= 0 {
				continue
			}
			p := &profile.Profile{SampleType: []*profile.ValueType{{Type: "cpu", Unit: "nanoseconds"}}, PeriodType: &profile.ValueType{Type: "cpu", Unit: "nanoseconds"}, Period: 10_000_000, TimeNanos: at.UnixNano(), DurationNanos: int64(duration)}
			for i, name := range []string{"main", "obs.seed.work", target.span} {
				f := &profile.Function{ID: uint64(i + 1), Name: name, SystemName: name, Filename: "obs-seed.go"}
				p.Function = append(p.Function, f)
				p.Location = append(p.Location, &profile.Location{ID: uint64(i + 1), Line: []profile.Line{{Function: f, Line: int64(i + 1)}}})
			}
			p.Sample = []*profile.Sample{{Location: []*profile.Location{p.Location[2], p.Location[1], p.Location[0]}, Value: []int64{int64(j+1) * 10_000_000}, Label: map[string][]string{"span_name": {target.span}}}}
			var buf bytes.Buffer
			if err := p.Write(&buf); err != nil {
				return err
			}
			q := url.Values{"name": {"obs-seed{" + strings.Join(parts, ",") + "}"}, "format": {"pprof"}, "spyName": {"gospy"}, "sampleRate": {"100"},
				"from": {strconv.FormatInt(at.Unix(), 10)}, "until": {strconv.FormatInt(at.Add(duration).Unix(), 10)}}
			if err := post(ctx, hc, endpoint+"/ingest?"+q.Encode(), "application/octet-stream", buf.Bytes()); err != nil {
				return err
			}
		}
	}
	return nil
}
