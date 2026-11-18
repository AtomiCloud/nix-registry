package sizing

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestGolden(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   Input
	}{{"idle", Input{}}, {"typical", Input{1000, 30, 90, 30}}, {"busy", Input{10000, 300, 900, 60}}} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Calculate(tc.in)
			if err != nil {
				t.Fatal(err)
			}
			b, err := json.MarshalIndent(got, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			want, err := os.ReadFile(filepath.Join("testdata", tc.name+".json"))
			if err != nil {
				t.Fatal(err)
			}
			if string(want) != string(append(b, 10)) {
				t.Fatalf("sizing golden mismatch for %s\ngot %s", tc.name, b)
			}
		})
	}
}
func TestInvalid(t *testing.T) {
	for _, in := range []Input{{-1, 1, 1, 1}, {math.NaN(), 1, 1, 1}, {math.Inf(1), 1, 1, 1}, {1, -1, 1, 1}, {1, 1, -1, 1}, {1, 1, 1, 2}, {1, 1, 1, -1}} {
		if _, err := Calculate(in); err == nil {
			t.Fatalf("invalid input accepted: %+v", in)
		}
	}
}
