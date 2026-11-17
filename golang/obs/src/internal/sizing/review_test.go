package sizing

import (
	"encoding/json"
	"testing"
)

func TestReview7BucketExcludesLogs(t *testing.T) {
	e, err := Calculate(Input{1000, 30, 90, 30})
	if err != nil {
		t.Fatal(err)
	}
	if e.BucketGBPerDay != 8.7 {
		t.Fatalf("bucket includes Loki logs: got %g want 8.7", e.BucketGBPerDay)
	}
	b, _ := json.Marshal(e)
	var fields map[string]any
	json.Unmarshal(b, &fields)
	if fields["victorialogs_disk_gb_per_day"] != 8.3 {
		t.Fatalf("VictoriaLogs disk missing: %s", b)
	}
}
