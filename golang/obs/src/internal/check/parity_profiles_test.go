//go:build parity

package check

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

type profileSummary struct {
	Samples   int64 `json:"samples"`
	Functions []struct {
		Name            string `json:"name"`
		Self, Inclusive float64
	} `json:"functions"`
	Spans []struct {
		Name  string  `json:"name"`
		Share float64 `json:"share"`
	} `json:"spans"`
}

func summarizeProfile(t *testing.T, binary, file string) profileSummary {
	t.Helper()
	output, err := exec.Command(binary, "profile", "summarize", "--format", "pprof", "--json", file).CombinedOutput()
	if err != nil {
		t.Fatalf("profile %s: %v: %s", file, err, output)
	}
	var result profileSummary
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatalf("profile summary %v: %s", err, output)
	}
	return result
}

// Connect unary protobuf uses the ordinary protobuf message as its HTTP body.
// These four scalar request fields avoid adding Pyroscope's server dependency to obs.
func profileRequest(profileType, selector string, start, end int64) []byte {
	var out []byte
	for i, value := range []string{profileType, selector} {
		out = append(out, byte((i+1)*8+2))
		out = binary.AppendUvarint(out, uint64(len(value)))
		out = append(out, value...)
	}
	for i, value := range []int64{start, end} {
		out = append(out, byte((i+3)*8))
		out = binary.AppendUvarint(out, uint64(value))
	}
	return out
}

func TestParityProfiles(t *testing.T) {
	// Same order as the alerts adapters: no stack skips, a stack without the
	// freshly built binary for an available command fails.
	base := os.Getenv("OBS_PARITY_PYROSCOPE")
	if base == "" {
		t.Skip("OBS_PARITY_PYROSCOPE not set")
	}
	binaryPath := requireCommand(t, "profile")
	fixture := os.Getenv("OBS_PARITY_PROFILE_FIXTURE")
	if fixture == "" {
		fixture = "../../../tests/fixtures/profile/base-1.pb.gz"
	}
	original := summarizeProfile(t, binaryPath, fixture)
	if len(original.Spans) == 0 {
		t.Fatal("profile fixture has no span_name labels")
	}
	end := time.Now().UTC().Add(-time.Minute)
	start := end.Add(-time.Minute)
	service := "obs-parity-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	profile, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	parityEvidence(t, "original-summary", original)
	if state := os.Getenv("OBS_PARITY_STATE"); state != "" {
		if err := os.WriteFile(filepath.Join(state, "profile-original.pb.gz"), profile, 0644); err != nil {
			t.Fatal(err)
		}
	}
	// The legacy ingest label-set syntax takes bare values; JSON/PromQL quotes
	// here become part of the stored label value instead of being unquoted.
	params := url.Values{"name": {"obs-parity{service_name=" + service + "}"}, "from": {strconv.FormatInt(start.Unix(), 10)}, "until": {strconv.FormatInt(end.Unix(), 10)}, "format": {"pprof"}, "spyName": {"gospy"}, "sampleRate": {"100"}}
	postReal(t, base+"/ingest?"+params.Encode(), "application/octet-stream", bytes.NewReader(profile))
	// Discover what was really stored, rather than guessing Pyroscope's type ID.
	var profileType string
	for attempt := 0; attempt < 30; attempt++ {
		body := postReal(t, base+"/querier.v1.QuerierService/ProfileTypes", "application/json", bytes.NewBufferString(fmt.Sprintf(`{"start":"%d","end":"%d"}`, start.UnixMilli()-1000, end.UnixMilli()+1000)))
		var types struct {
			ProfileTypes []struct{ ID, SampleType, SampleUnit string } `json:"profileTypes"`
		}
		if err := json.Unmarshal(body, &types); err != nil {
			t.Fatal(err)
		}
		for _, kind := range types.ProfileTypes {
			if kind.SampleType == "cpu" && kind.SampleUnit == "nanoseconds" {
				profileType = kind.ID
			}
		}
		if profileType != "" {
			break
		}
		time.Sleep(time.Second)
	}
	if profileType == "" {
		t.Fatal("real Pyroscope did not store the pprof CPU profile")
	}
	mergedCount := 0
	merge := func(selector string) profileSummary {
		var body []byte
		for attempt := 0; attempt < 30; attempt++ {
			body = postReal(t, base+"/querier.v1.QuerierService/SelectMergeProfile", "application/proto", bytes.NewReader(profileRequest(profileType, selector, start.UnixMilli()-1000, end.UnixMilli()+1000)))
			if len(body) > 0 {
				break
			}
			time.Sleep(250 * time.Millisecond)
		}
		if len(body) == 0 {
			t.Fatalf("Pyroscope SelectMergeProfile %s: seeded profile has not loaded", selector)
		}
		mergedCount++
		if state := os.Getenv("OBS_PARITY_STATE"); state != "" {
			if err := os.WriteFile(filepath.Join(state, fmt.Sprintf("profile-pyroscope-%d.pprof", mergedCount)), body, 0644); err != nil {
				t.Fatal(err)
			}
		}
		file := filepath.Join(t.TempDir(), "merged.pprof")
		if err := os.WriteFile(file, body, 0644); err != nil {
			t.Fatal(err)
		}
		return summarizeProfile(t, binaryPath, file)
	}
	all := merge(`{service_name=` + strconv.Quote(service) + `}`)
	if all.Samples <= 0 {
		t.Fatal("Pyroscope merged CPU profile has no samples")
	}
	got, want := map[string][]float64{}, map[string][]float64{}
	for _, f := range original.Functions {
		want[f.Name] = []float64{f.Self, f.Inclusive}
	}
	for _, f := range all.Functions {
		got[f.Name] = []float64{f.Self, f.Inclusive}
	}
	if err := compareNumbers(got, want); err != nil {
		t.Fatalf("profile fixture %s functions vs Pyroscope: %v", fixture, err)
	}
	for _, span := range original.Spans {
		selected := merge(`{service_name=` + strconv.Quote(service) + `,span_name=` + strconv.Quote(span.Name) + `}`)
		share := float64(selected.Samples) / float64(all.Samples)
		if !near(share, span.Share) {
			t.Fatalf("profile fixture %s span %q: obs share %g, real Pyroscope %g (%d/%d samples)", fixture, span.Name, span.Share, share, selected.Samples, all.Samples)
		}
	}
	t.Logf("MEASURED %d function self/inclusive shares and %d span CPU shares match real Pyroscope", len(original.Functions), len(original.Spans))
}
