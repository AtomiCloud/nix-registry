package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/AtomiCloud/obs/internal/seed"
)

func post(ctx context.Context, hc *http.Client, u, ctype string, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", ctype)
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("POST %s: HTTP %d: %s", u, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return nil
}

// LoadSeed pushes the seed's samples to VictoriaMetrics (/api/v1/import) and its log
// rows to VictoriaLogs (/insert/jsonline), then waits until both show the data.
func LoadSeed(ctx context.Context, hc *http.Client, vm, vl string, ds *seed.Dataset) error {
	if err := loadMetrics(ctx, hc, vm, ds); err != nil {
		return fmt.Errorf("loading metrics: %w", err)
	}
	if err := loadLogs(ctx, hc, vl, ds); err != nil {
		return fmt.Errorf("loading logs: %w", err)
	}
	return nil
}

type vmLine struct {
	Metric     map[string]string `json:"metric"`
	Values     []float64         `json:"values"`
	Timestamps []int64           `json:"timestamps"`
}

func loadMetrics(ctx context.Context, hc *http.Client, vm string, ds *seed.Dataset) error {
	keys := make([]string, 0, len(ds.Series))
	for k := range ds.Series {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var buf bytes.Buffer
	flush := func() error {
		if buf.Len() == 0 {
			return nil
		}
		err := post(ctx, hc, vm+"/api/v1/import", "application/x-ndjson", buf.Bytes())
		buf.Reset()
		return err
	}
	for _, k := range keys {
		s := ds.Series[k]
		l := vmLine{Metric: map[string]string{}}
		for _, lb := range s.Labels {
			l.Metric[lb.Name] = lb.Value
		}
		for i, v := range s.Vals {
			if math.IsNaN(v) || math.IsInf(v, 0) {
				continue // JSON cannot carry them; VictoriaMetrics treats a gap the same
			}
			l.Values = append(l.Values, v)
			l.Timestamps = append(l.Timestamps, s.Ts[i])
		}
		b, _ := json.Marshal(l)
		buf.Write(b)
		buf.WriteByte('\n')
		if buf.Len() > 4<<20 {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	if err := flush(); err != nil {
		return err
	}
	// Make the samples searchable now, not at the next flush interval.
	_, _, _ = get(ctx, hc, vm+"/internal/force_flush", false)
	return nil
}

func loadLogs(ctx context.Context, hc *http.Client, vl string, ds *seed.Dataset) error {
	keys := make([]string, 0, len(ds.Streams))
	for k := range ds.Streams {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	total := 0
	for _, k := range keys {
		st := ds.Streams[k]
		names := make([]string, 0, len(st.Fields))
		for _, f := range st.Fields {
			names = append(names, f.Name)
		}
		q := url.Values{}
		q.Set("_stream_fields", strings.Join(names, ","))
		q.Set("_msg_field", "_msg")
		q.Set("_time_field", "_time")
		endpoint := vl + "/insert/jsonline?" + q.Encode()
		var buf bytes.Buffer
		flush := func() error {
			if buf.Len() == 0 {
				return nil
			}
			err := post(ctx, hc, endpoint, "application/stream+json", buf.Bytes())
			buf.Reset()
			return err
		}
		for _, row := range st.Rows {
			m := make(map[string]string, len(st.Fields)+len(row.Fields)+2)
			for _, f := range st.Fields {
				m[f.Name] = f.Value
			}
			for _, f := range row.Fields {
				m[f.Name] = f.Value
			}
			m["_msg"] = row.Msg
			m["_time"] = time.Unix(0, row.TsNs).UTC().Format(time.RFC3339Nano)
			b, _ := json.Marshal(m)
			buf.Write(b)
			buf.WriteByte('\n')
			total++
			if buf.Len() > 4<<20 {
				if err := flush(); err != nil {
					return err
				}
			}
		}
		if err := flush(); err != nil {
			return err
		}
	}
	if total == 0 {
		return nil
	}
	// VictoriaLogs makes rows searchable after a short flush interval: wait for the first.
	deadline := time.Now().Add(30 * time.Second)
	for {
		b, code, err := get(ctx, hc, vl+"/select/logsql/query?limit=1&query="+url.QueryEscape("*"), false)
		if err == nil && code == http.StatusOK && len(bytes.TrimSpace(b)) > 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("VictoriaLogs shows no rows 30 s after the load")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}
