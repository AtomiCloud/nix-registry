package storemove

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// HTTP uses store-native /metrics and range-query APIs, never Grafana or a real
// cluster in tests. Client.Transport is replaceable with a fixture transport.
type HTTP struct{ Client *http.Client }

func (h HTTP) request(ctx context.Context, method, address string, body []byte, tenant string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, address, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	if tenant != "" {
		req.Header.Set("X-Scope-OrgID", tenant)
	}
	client := h.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("%s: HTTP %d", address, res.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, 16<<20))
	if len(b) >= 16<<20 {
		return nil, fmt.Errorf("HTTP response exceeds 16 MiB")
	}
	return b, err
}
func (h HTTP) Counter(ctx context.Context, endpoint, name string) (float64, error) {
	b, err := h.request(ctx, http.MethodGet, endpoint, nil, "")
	if err != nil {
		return 0, err
	}
	// The only label predicate required by section 10.6 is the VL OTLP type.
	metric, predicate := name, ""
	if n := strings.IndexByte(name, 123); n >= 0 {
		metric, predicate = name[:n], strings.TrimSuffix(name[n+1:], "}")
	}
	scanner := bufio.NewScanner(bytes.NewReader(b))
	scanner.Buffer(make([]byte, 4096), 1<<20)
	var sum float64
	found := false
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		labelled := fields[0]
		if labelled != metric && !strings.HasPrefix(labelled, metric+"{") {
			continue
		}
		if predicate != "" && !strings.Contains(labelled, predicate) {
			continue
		}
		v, e := strconv.ParseFloat(fields[1], 64)
		if e != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
			return 0, fmt.Errorf("%s: invalid counter value %q", name, fields[1])
		}
		sum += v
		found = true
	}
	if err = scanner.Err(); err != nil {
		return 0, err
	}
	if !found {
		return 0, fmt.Errorf("%s: required counter absent", name)
	}
	return sum, nil
}
func (h HTTP) Counts(ctx context.Context, store string, c Config, end time.Time) ([]float64, error) {
	start := end.Add(-59 * time.Minute)
	endpoint, err := url.Parse(c.ReadURL)
	if err != nil {
		return nil, err
	}
	var body []byte
	method := http.MethodGet
	if store == "pyroscope" {
		method = http.MethodPost
		body, err = json.Marshal(map[string]any{"labelSelector": c.Query, "profileTypeID": c.ProfileType, "start": strconv.FormatInt(start.UnixMilli(), 10), "end": strconv.FormatInt(end.UnixMilli(), 10), "step": 60})
		if err != nil {
			return nil, err
		}
	} else {
		q := endpoint.Query()
		q.Set("query", c.Query)
		q.Set("start", strconv.FormatInt(start.Unix(), 10))
		q.Set("end", strconv.FormatInt(end.Unix(), 10))
		q.Set("step", "60")
		endpoint.RawQuery = q.Encode()
	}
	b, err := h.request(ctx, method, endpoint.String(), body, c.Tenant)
	if err != nil {
		return nil, err
	}
	if store == "pyroscope" {
		return profileCounts(b, start)
	}
	if store == "tempo" {
		return tempoCounts(b, start)
	}
	return matrixCounts(b, start)
}

func tempoCounts(b []byte, start time.Time) ([]float64, error) {
	var reply struct {
		Series []struct {
			Samples []struct {
				Timestamp json.Number `json:"timestampMs"`
				Value     float64     `json:"value"`
			} `json:"samples"`
		} `json:"series"`
	}
	if err := json.Unmarshal(b, &reply); err != nil {
		return nil, err
	}
	if len(reply.Series) != 1 {
		return nil, fmt.Errorf("known Tempo dataset: want one aggregate series, got %d", len(reply.Series))
	}
	values, seen := make([]float64, 60), make([]bool, 60)
	for _, sample := range reply.Series[0].Samples {
		ts, err := strconv.ParseFloat(string(sample.Timestamp), 64)
		if err != nil {
			return nil, err
		}
		if err = insertMinute(values, seen, start, ts/1000, sample.Value); err != nil {
			return nil, err
		}
	}
	for _, present := range seen {
		if !present {
			return nil, fmt.Errorf("known Tempo dataset: missing minute")
		}
	}
	return values, nil
}
func matrixCounts(b []byte, start time.Time) ([]float64, error) {
	var reply struct {
		Status string
		Error  string
		Data   struct {
			ResultType string
			Result     []struct{ Values [][]json.RawMessage }
		}
	}
	if err := json.Unmarshal(b, &reply); err != nil {
		return nil, err
	}
	if reply.Status != "success" || reply.Data.ResultType != "matrix" || len(reply.Data.Result) != 1 {
		return nil, fmt.Errorf("known dataset query: want success matrix with one aggregate series (got status=%q type=%q series=%d error=%q)", reply.Status, reply.Data.ResultType, len(reply.Data.Result), reply.Error)
	}
	values := make([]float64, 60)
	seen := make([]bool, 60)
	for _, pair := range reply.Data.Result[0].Values {
		if len(pair) != 2 {
			return nil, fmt.Errorf("malformed range sample")
		}
		var ts float64
		var str string
		if err := json.Unmarshal(pair[0], &ts); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(pair[1], &str); err != nil {
			return nil, err
		}
		v, err := strconv.ParseFloat(str, 64)
		if err != nil {
			return nil, err
		}
		if err = insertMinute(values, seen, start, ts, v); err != nil {
			return nil, err
		}
	}
	for _, present := range seen {
		if !present {
			return nil, fmt.Errorf("known dataset query: missing minute")
		}
	}
	return values, nil
}
func insertMinute(values []float64, seen []bool, start time.Time, ts, value float64) error {
	delta := ts - float64(start.Unix())
	idx := int(delta / 60)
	if math.IsNaN(ts) || math.IsInf(ts, 0) || delta < 0 || delta != float64(idx*60) || idx >= 60 || seen[idx] || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return fmt.Errorf("invalid/duplicate minute sample timestamp=%v value=%v", ts, value)
	}
	values[idx] = value
	seen[idx] = true
	return nil
}
func profileCounts(b []byte, start time.Time) ([]float64, error) {
	var reply struct {
		Series []struct {
			Points []struct {
				Timestamp json.Number `json:"timestamp"`
				Value     float64     `json:"value"`
			} `json:"points"`
		} `json:"series"`
	}
	if err := json.Unmarshal(b, &reply); err != nil {
		return nil, err
	}
	if len(reply.Series) != 1 {
		return nil, fmt.Errorf("known profile dataset: want one aggregate series")
	}
	values := make([]float64, 60)
	seen := make([]bool, 60)
	for _, p := range reply.Series[0].Points {
		ts, err := strconv.ParseFloat(string(p.Timestamp), 64)
		if err != nil {
			return nil, err
		}
		if err = insertMinute(values, seen, start, ts/1000, p.Value); err != nil {
			return nil, err
		}
	}
	for _, present := range seen {
		if !present {
			return nil, fmt.Errorf("known profile dataset: missing minute")
		}
	}
	return values, nil
}
