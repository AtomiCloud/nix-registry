package live

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/AtomiCloud/obs/internal/contract"
	"github.com/AtomiCloud/obs/internal/seed"
)

const fixtureDir = "../../../tests/fixtures/good/observability"

func fixtureContract(t *testing.T) *contract.Contract {
	t.Helper()
	c, err := contract.Load(fixtureDir + "/contract.yaml")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// matrixServer answers query_range with one series made of samples, and records requests.
func matrixServer(t *testing.T, rec *recorder, samples func(start, end, step int64) []Sample, queries *[]string) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		if r.URL.Path != "/api/v1/query_range" {
			http.Error(w, "unexpected", 500)
			return
		}
		q := r.URL.Query()
		*queries = append(*queries, q.Get("query"))
		var start, end, step int64
		fmt.Sscan(q.Get("start"), &start)
		fmt.Sscan(q.Get("end"), &end)
		fmt.Sscan(strings.TrimSuffix(q.Get("step"), "s"), &step)
		var vs []string
		for _, s := range samples(start, end, step) {
			vs = append(vs, fmt.Sprintf(`[%d,"%v"]`, s.T, s.V))
		}
		fmt.Fprintf(w, `{"status":"success","data":{"resultType":"matrix","result":[{"metric":{},"values":[%s]}]}}`, strings.Join(vs, ","))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestShapeReportSetsRealNextToSeedWithOneQuery(t *testing.T) {
	rec := &recorder{}
	var queries []string
	srv := matrixServer(t, rec, func(start, end, step int64) []Sample {
		var out []Sample
		for ts := start; ts <= end; ts += step {
			out = append(out, Sample{T: ts, V: 5 + 4*float64((ts/step)%12)/11})
		}
		return out
	}, &queries)
	sd, err := seed.Load(fixtureDir + "/seed.yaml")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	res, err := ShapeReport(context.Background(), &Client{}, &Target{Name: "fake", Metrics: srv.URL}, fixtureContract(t), ShapeOptions{
		Window: 6 * time.Hour, Step: 5 * time.Minute, Seed: sd, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Metrics) != 1 || res.Metrics[0].Metric != "checkout.charges" {
		t.Fatalf("the fixture contract has one metric: %+v", res.Metrics)
	}
	m := res.Metrics[0]
	if m.Real == nil || m.Real.Points < 70 || m.Real.Level.Max > 9.01 || m.Real.Level.Min < 4.99 {
		t.Fatalf("real side: %+v (%s)", m.Real, m.RealNote)
	}
	if m.Seed == nil || m.Seed.Points < 30 || m.Seed.Level.Typical <= 0 {
		t.Fatalf("seed side comes from the seed generator and the embedded VictoriaMetrics: %+v (%s)", m.Seed, m.SeedNote)
	}
	if !strings.Contains(m.Query, "sum(rate(checkout_charges_total{") || !strings.Contains(m.Query, `"durian.cloud.service"="checkout"`) {
		t.Fatalf("a counter is its summed rate, for this service: %s", m.Query)
	}
	assertReadOnly(t, rec)
	var sb strings.Builder
	res.Print(&sb)
	for _, want := range []string{"checkout.charges", "level", "trend", "cycle", "spikes", "gaps", "real", "seed", "n/a"} {
		if !strings.Contains(sb.String(), want) {
			t.Errorf("text lacks %q:\n%s", want, sb.String())
		}
	}
}

func TestShapeReportNeverFailsOnWhatItFindsOnlyOnAnUnreachableTarget(t *testing.T) {
	_, err := ShapeReport(context.Background(), &Client{}, &Target{Name: "x", Metrics: "http://127.0.0.1:1"}, fixtureContract(t), ShapeOptions{Window: time.Hour, Step: 5 * time.Minute})
	if err == nil || !strings.Contains(err.Error(), "127.0.0.1:1") {
		t.Fatalf("%v", err)
	}
}

func TestShapeQueryPerKind(t *testing.T) {
	c := fixtureContract(t)
	q := shapeQuery(c, "checkout.charges", c.Metrics["checkout.charges"], `,"durian.cloud.service"="checkout"`, 5*time.Minute)
	if q != `sum(rate(checkout_charges_total{"durian.cloud.service"="checkout"}[300s]))` {
		t.Fatal(q)
	}
	h := contract.Metric{Kind: contract.MetricHistogram, Unit: "s"}
	if q := shapeQuery(c, "http.server.request.duration", h, "", 5*time.Minute); q != `sum(rate(http_server_request_duration_seconds_count[300s]))` {
		t.Fatal(q)
	}
	g := contract.Metric{Kind: contract.MetricGauge}
	if q := shapeQuery(c, "queue.depth", g, "", 5*time.Minute); q != `sum(queue_depth)` {
		t.Fatal(q)
	}
}

func TestShapeMetricFilter(t *testing.T) {
	rec := &recorder{}
	var queries []string
	srv := matrixServer(t, rec, func(a, b, s int64) []Sample { return []Sample{{T: a, V: 1}} }, &queries)
	res, err := ShapeReport(context.Background(), &Client{}, &Target{Metrics: srv.URL}, fixtureContract(t), ShapeOptions{Window: time.Hour, Step: 5 * time.Minute, Only: []string{"nothing.here"}})
	if err == nil || !strings.Contains(err.Error(), "nothing.here") {
		t.Fatalf("an unknown --metric must be named: %v %+v", err, res)
	}
}
