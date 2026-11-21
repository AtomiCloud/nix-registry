package obs

import "testing"

func TestRepeatNilAndEmptyMaps(t *testing.T) {
	// Each case owns disjoint names and registers before any signal closes boot.
	for _, nested := range []bool{false, true} {
		id := "empty:contract.yaml"
		if nested {
			id = "nested:contract.yaml"
		}
		first := DurianSignalTables{Contract: id, Hash: "x"}
		repeat := DurianSignalTables{Contract: id, Hash: "x", Metrics: map[string]DurianSignalMetricInfo{}, Events: map[string]string{}, Spans: map[string]DurianSignalSpanInfo{}}
		if nested {
			first.Metrics = map[string]DurianSignalMetricInfo{"repeat.metric": {Kind: "counter"}}
			repeat.Metrics = map[string]DurianSignalMetricInfo{"repeat.metric": {Kind: "counter", Values: map[string][]string{}, Ranges: map[string][2]int64{}}}
		}
		if err := Register(first); err != nil {
			t.Fatal(err)
		}
		if err := Register(repeat); err != nil {
			t.Errorf("equal nil/empty maps refused: %v", err)
		}
	}
}
