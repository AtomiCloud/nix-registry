package storemove

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func httpFixture(body string, check func(*http.Request)) HTTP {
	return HTTP{Client: &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		if check != nil {
			check(r)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}}
}
func TestCounters(t *testing.T) {
	h := httpFixture("# TYPE vl_rows_ingested_total counter\nvl_rows_ingested_total{type=\"json\"} 99\nvl_rows_ingested_total{type=\"opentelemetry_protobuf\",tenant=\"a\"} 4\nvl_rows_ingested_total{type=\"opentelemetry_protobuf\",tenant=\"b\"} 5\n", nil)
	v, err := h.Counter(context.Background(), "http://fixture/metrics", received("victorialogs"))
	if err != nil || v != 9 {
		t.Fatalf("%g %v", v, err)
	}
	for _, bad := range []string{"", "vm_rows_inserted_total NaN", "vm_rows_inserted_total -1", "vm_rows_inserted_total +Inf"} {
		if _, err := httpFixture(bad, nil).Counter(context.Background(), "http://fixture/metrics", "vm_rows_inserted_total"); err == nil {
			t.Fatalf("bad counter accepted: %q", bad)
		}
	}
}
func matrix(start time.Time, mutate int) string {
	var points []string
	for i := 0; i < 60; i++ {
		ts := start.Add(time.Duration(i) * time.Minute).Unix()
		if mutate == 1 && i == 17 {
			ts++
		}
		if mutate == 2 && i == 18 {
			continue
		}
		points = append(points, fmt.Sprintf(`[%d,"%d"]`, ts, i+1))
	}
	return `{"status":"success","data":{"resultType":"matrix","result":[{"values":[` + strings.Join(points, ",") + `]}]}}`
}
func TestStoreQueryAPIs(t *testing.T) {
	start := time.Unix(1700000040, 0)
	end := start.Add(59 * time.Minute)
	for _, store := range []string{"victorialogs", "victoriametrics"} {
		t.Run(store, func(t *testing.T) {
			h := httpFixture(matrix(start, 0), func(r *http.Request) {
				q := r.URL.Query()
				if q.Get("step") != "60" || q.Get("query") != "known fixture" || q.Get("start") != "1700000040" || r.Header.Get("X-Scope-OrgID") != "fixture" {
					t.Fatal(r.URL, r.Header)
				}
			})
			cfg := config()
			cfg.Query = "known fixture"
			cfg.Tenant = "fixture"
			v, err := h.Counts(context.Background(), store, cfg, end)
			if err != nil || len(v) != 60 || v[17] != 18 {
				t.Fatalf("%v %v", v, err)
			}
		})
	}
	for _, mutate := range []int{1, 2} {
		if _, err := matrixCounts([]byte(matrix(start, mutate)), start); err == nil {
			t.Fatalf("corrupt time series %d accepted", mutate)
		}
	}
	for _, bad := range []string{`{"status":"error"}`, `{"status":"success","data":{"resultType":"matrix","result":[]}}`, `{`} {
		if _, err := matrixCounts([]byte(bad), start); err == nil {
			t.Fatal("malformed query accepted")
		}
	}
}
func TestPyroscopeQueryAPI(t *testing.T) {
	start := time.Unix(1700000040, 0)
	var ps []string
	for i := 0; i < 60; i++ {
		ps = append(ps, fmt.Sprintf(`{"timestamp":"%d","value":%d}`, start.Add(time.Duration(i)*time.Minute).UnixMilli(), i+1))
	}
	body := `{"series":[{"points":[` + strings.Join(ps, ",") + `]}]}`
	h := httpFixture(body, func(r *http.Request) {
		if r.Method != "POST" {
			t.Fatal(r.Method)
		}
		b, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(b), `"profileTypeID"`) {
			t.Fatal(string(b))
		}
	})
	v, err := h.Counts(context.Background(), "pyroscope", config(), start.Add(59*time.Minute))
	if err != nil || len(v) != 60 {
		t.Fatalf("%v %v", v, err)
	}
}
