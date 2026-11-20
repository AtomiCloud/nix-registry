package live

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// Recorded responses. testdata/recorded/<scenario>.json holds the answers a REAL local sandbox
// (VictoriaMetrics v1.156.0, VictoriaLogs v1.54.0, Tempo 3.1.1, Grafana 13.3.0; seeded by
// `obs look --keep` on tests/fixtures/live-recorded) gave to every request one obs live command
// sent, together with the instant it was run (Now). The tests replay them: the command is run with
// that Now, so it sends the same requests, and the answer for each comes from the file. A request
// that is not in the file fails the test: change a query and the fixture must be recorded again.
//
// To record (needs docker and the sandbox running; see the README, "Recording the fixtures"):
//
//	OBS_LIVE_RECORD='metrics=http://127.0.0.1:24428,logs=http://127.0.0.1:24429,traces=http://127.0.0.1:24200,grafana=http://127.0.0.1:24000' \
//	  go test ./internal/live -run TestRecorded
//
// Without OBS_LIVE_RECORD the files are replayed and nothing touches the network.

type exchange struct {
	Method string          `json:"method"`
	Path   string          `json:"path"`
	Query  string          `json:"query"` // the query string, parameters sorted
	Status int             `json:"status"`
	JSON   json.RawMessage `json:"json,omitempty"`
	Text   string          `json:"text,omitempty"`
}

func TestRecordingPublishesOnlyAfterPassingTest(t *testing.T) {
	if mode := os.Getenv("OBS_RECORDING_GUARD_CHILD"); mode != "" {
		recordedStores(t, "guard")
		if mode == "fail" {
			t.Error("planted recording failure")
		}
		return
	}
	// os.Args[0] may be relative (./live.test from go test -c); the child runs in another directory.
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("cannot resolve this test binary: %v", err)
	}
	for _, mode := range []string{"fail", "pass"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			file := filepath.Join(dir, "testdata", "recorded", "guard.json")
			if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
				t.Fatal(err)
			}
			const original = "existing fixture must survive a failed recording\n"
			if err := os.WriteFile(file, []byte(original), 0o644); err != nil {
				t.Fatal(err)
			}
			cmd := exec.CommandContext(t.Context(), exe, "-test.run=^TestRecordingPublishesOnlyAfterPassingTest$")
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "OBS_RECORDING_GUARD_CHILD="+mode, "OBS_LIVE_RECORD=metrics=http://127.0.0.1:1")
			out, err := cmd.CombinedOutput()
			if (err != nil) != (mode == "fail") {
				t.Fatalf("child result: %v: %s", err, out)
			}
			got, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "fail" && string(got) != original {
				t.Fatal("failed recording overwrote the existing fixture")
			}
			if mode == "pass" && (!json.Valid(got) || !bytes.Contains(got, []byte(`"scenario": "guard"`))) {
				t.Fatalf("passing recording must publish: %s", got)
			}
		})
	}
}

type recording struct {
	Scenario  string     `json:"scenario"`
	Now       int64      `json:"now_unix"`
	Sandbox   string     `json:"sandbox,omitempty"`
	Exchanges []exchange `json:"exchanges"`
}

func canonicalQuery(q url.Values) string {
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		vs := append([]string(nil), q[k]...)
		sort.Strings(vs)
		for _, v := range vs {
			parts = append(parts, url.QueryEscape(k)+"="+url.QueryEscape(v))
		}
	}
	return strings.Join(parts, "&")
}

type harness struct {
	t      *testing.T
	Now    time.Time
	Target *Target
	rec    *recorder // every request the stores received
	mu     sync.Mutex
	got    []exchange
}

// recordedStores returns four fake stores that replay (or, with OBS_LIVE_RECORD, record) the
// scenario. The Target points at them.
func recordedStores(t *testing.T, scenario string) *harness {
	t.Helper()
	h := &harness{t: t, rec: &recorder{}}
	file := filepath.Join("testdata", "recorded", scenario+".json")
	real := map[string]string{}
	if spec := os.Getenv("OBS_LIVE_RECORD"); spec != "" {
		for _, kv := range strings.Split(spec, ",") {
			k, v, _ := strings.Cut(kv, "=")
			real[k] = strings.TrimRight(v, "/")
		}
		h.Now = time.Now().UTC().Truncate(time.Second)
	}
	var replay map[string]exchange
	if len(real) == 0 {
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("recorded responses missing: %v (record them: see the README, Recording the fixtures)", err)
		}
		var rc recording
		if err := json.Unmarshal(b, &rc); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		h.Now = time.Unix(rc.Now, 0).UTC()
		replay = map[string]exchange{}
		for _, e := range rc.Exchanges {
			replay[e.Method+" "+e.Path+"?"+e.Query] = e
		}
	}
	mk := func(store string) string {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h.rec.record(r)
			// bodies of POSTs are form encoded queries: fold them into the key like a query string
			q := r.URL.Query()
			if r.Method == http.MethodPost {
				if err := r.ParseForm(); err == nil {
					for k, v := range r.PostForm {
						q[k] = append(q[k], v...)
					}
				}
			}
			key := r.Method + " " + r.URL.Path + "?" + canonicalQuery(q)
			if len(real) == 0 {
				e, ok := replay[key]
				if !ok {
					h.t.Errorf("request not in %s: %s (the command sends a request that was not recorded; record again)", file, key)
					http.Error(w, "not recorded", 599)
					return
				}
				if len(e.JSON) > 0 {
					w.Header().Set("Content-Type", "application/json")
				}
				w.WriteHeader(e.Status)
				if len(e.JSON) > 0 {
					w.Write(e.JSON)
				} else {
					io.WriteString(w, e.Text)
				}
				return
			}
			up, err := http.NewRequest(r.Method, real[store]+r.URL.Path+"?"+r.URL.RawQuery, r.Body)
			if err != nil {
				http.Error(w, err.Error(), 502)
				return
			}
			up.Header = r.Header.Clone()
			up.Header.Del("Accept-Encoding") // keep the recorded bodies plain text
			resp, err := http.DefaultClient.Do(up)
			if err != nil {
				http.Error(w, err.Error(), 502)
				return
			}
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)
			e := exchange{Method: r.Method, Path: r.URL.Path, Query: canonicalQuery(q), Status: resp.StatusCode}
			if json.Valid(body) {
				var buf bytes.Buffer
				json.Indent(&buf, body, "  ", "  ")
				e.JSON = json.RawMessage(bytes.TrimSpace(buf.Bytes()))
			} else {
				e.Text = string(body)
			}
			h.mu.Lock()
			h.got = append(h.got, e)
			h.mu.Unlock()
			if ct := resp.Header.Get("Content-Type"); ct != "" {
				w.Header().Set("Content-Type", ct)
			}
			w.WriteHeader(resp.StatusCode)
			w.Write(body)
		}))
		t.Cleanup(srv.Close)
		return srv.URL
	}
	h.Target = &Target{Name: "recorded", Metrics: mk("metrics"), Logs: mk("logs"), Traces: mk("traces"), Grafana: mk("grafana"), Headers: map[string]string{}}
	if len(real) > 0 {
		t.Cleanup(func() {
			if t.Failed() {
				return
			}
			h.mu.Lock()
			defer h.mu.Unlock()
			seen := map[string]bool{}
			var ex []exchange
			for _, e := range h.got {
				k := e.Method + " " + e.Path + "?" + e.Query
				if !seen[k] {
					seen[k] = true
					ex = append(ex, e)
				}
			}
			sort.SliceStable(ex, func(i, j int) bool {
				return ex[i].Method+ex[i].Path+ex[i].Query < ex[j].Method+ex[j].Path+ex[j].Query
			})
			rc := recording{Scenario: scenario, Now: h.Now.Unix(), Sandbox: os.Getenv("OBS_LIVE_RECORD_NOTE"), Exchanges: ex}
			b, err := json.MarshalIndent(rc, "", "  ")
			if err != nil {
				t.Errorf("%v", err)
				return
			}
			if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
				t.Errorf("%v", err)
				return
			}
			if err := os.WriteFile(file, append(b, '\n'), 0o644); err != nil {
				t.Errorf("%v", err)
				return
			}
			fmt.Fprintf(os.Stderr, "recorded %d exchanges into %s (now %s)\n", len(ex), file, h.Now.Format(time.RFC3339))
		})
	}
	return h
}
