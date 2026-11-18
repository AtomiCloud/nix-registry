package sandbox

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"github.com/AtomiCloud/obs/internal/files"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AtomiCloud/obs/internal/seed"
	"github.com/google/pprof/profile"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func reply(body string) *http.Response {
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}
}

func TestTempoPayloadAndPartialRejection(t *testing.T) {
	ds := &seed.Dataset{Traces: []*seed.TTrace{{TraceID: [16]byte{1}, Spans: []*seed.TSpan{{SpanID: [8]byte{2}, ParentID: [8]byte{3}, Name: "POST /pay", Service: "checkout", StartNs: 123000000000, DurNs: 100000000, Kind: 2, Status: 2, Attrs: map[string]seed.TAttrVal{"cached": seed.TBool(true), "attempt": seed.TInt(2)}}}}}}
	var calls int
	hc := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Path != "/v1/traces" {
			t.Errorf("path %s", r.URL)
		}
		b, _ := io.ReadAll(r.Body)
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{hex.EncodeToString(ds.Traces[0].TraceID[:]), `"parentSpanId":"0300000000000000"`, `"endTimeUnixNano":"123100000000"`, `"service.name"`, `"boolValue":true`, `"intValue":"2"`} {
			if !strings.Contains(string(b), want) {
				t.Errorf("payload lacks %s: %s", want, b)
			}
		}
		if calls == 1 {
			return reply(`{}`), nil
		}
		return reply(`{"partialSuccess":{"rejectedSpans":"1","errorMessage":"too old"}}`), nil
	})}
	if err := LoadTraces(context.Background(), hc, "http://tempo", ds); err != nil {
		t.Fatal(err)
	}
	if err := LoadTraces(context.Background(), hc, "http://tempo", ds); err == nil || !strings.Contains(err.Error(), "too old") {
		t.Fatalf("silent rejected spans: %v", err)
	}
}

func TestProfilePayloadIsDeterministicAndHasSpanNames(t *testing.T) {
	sd := &seed.Seed{Resource: map[string]string{"durian.cloud.service": "checkout", "durian.cloud.module": "api"}}
	ds := &seed.Dataset{Start: time.Unix(1700000000, 0), End: time.Unix(1700000600, 0), Traces: []*seed.TTrace{{Spans: []*seed.TSpan{{Name: "POST /pay", Service: "checkout"}}}}}
	var got [][]byte
	hc := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/ingest" || r.URL.Query().Get("format") != "pprof" {
			t.Errorf("ingest %s", r.URL)
		}
		if !strings.Contains(r.URL.Query().Get("name"), "service_name=checkout") {
			t.Errorf("service selector %s", r.URL)
		}
		b, _ := io.ReadAll(r.Body)
		got = append(got, b)
		p, err := profile.ParseData(b)
		if err != nil {
			t.Fatal(err)
		}
		if err := p.CheckValid(); err != nil {
			t.Fatal(err)
		}
		if len(p.Sample) != 1 || p.Sample[0].Label["span_name"][0] != "POST /pay" {
			t.Errorf("span labels %v", p.Sample)
		}
		return reply(`{}`), nil
	})}
	for j := 0; j < 2; j++ {
		if err := LoadProfiles(context.Background(), hc, "http://pyroscope", sd, ds); err != nil {
			t.Fatal(err)
		}
	}
	if len(got) < 2 || string(got[0]) != string(got[len(got)/2]) {
		t.Fatal("profile bytes vary for same seed")
	}
}

func TestConcreteProfileDatasourceIsProvisioned(t *testing.T) {
	dir := project(t)
	if err := os.WriteFile(filepath.Join(dir, "dashboards", "profiles.json"), []byte(`{"uid":"profiles","panels":[{"id":1,"type":"flamegraph","datasource":{"type":"grafana-pyroscope-datasource","uid":"checkout-profiles"},"targets":[{"refId":"A"}]}]}`), 0644); err != nil {
		t.Fatal(err)
	}
	set, err := files.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range datasources(set, "demo", true) {
		if d.UID == "checkout-profiles" && d.Kind == "profiles" {
			return
		}
	}
	t.Fatal("concrete Pyroscope panel UID was not provisioned")
}
