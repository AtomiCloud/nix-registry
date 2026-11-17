package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestPruneRecordedIdentityUsesAllLabels(t *testing.T) {
	prefix := strings.Repeat("x", 200)
	raw, err := json.Marshal([]any{
		map[string]any{"labels": map[string]string{"a": prefix, "z": "one"}, "timestamps": []int64{1}, "values": []float64{1}},
		map[string]any{"labels": map[string]string{"a": prefix, "z": "two"}, "timestamps": []int64{1}, "values": []float64{1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := normalizeRecorded(raw)
	if err != nil || len(got) != 2 {
		t.Fatalf("distinct long label sets collided: %v %+v", err, got)
	}
}

func TestPruneTransitionOrderIsNotAnEvaluationDifference(t *testing.T) {
	a := `{"t":1,"instance":"a","from":"Normal","to":"Alerting"}`
	b := `{"t":1,"instance":"b","from":"Normal","to":"Alerting"}`
	makeRun := func(rows string) []byte {
		return []byte(`[{"alert":"fixture","run":"baseline","transitions":[` + rows + `]}]`)
	}
	left, err := normalizeSteps(makeRun(a+","+b), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	right, err := normalizeSteps(makeRun(b+","+a), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	var st cmpStats
	var differences []pruneDiff
	diffTree(nil, left, right, &st, &differences)
	if len(differences) != 0 {
		t.Fatalf("transition iteration order is unstable: %+v", differences)
	}
	changed, err := normalizeSteps(makeRun(strings.Replace(a, "Alerting", "Error", 1)+","+b), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	differences = nil
	diffTree(nil, left, changed, &st, &differences)
	if len(differences) == 0 {
		t.Fatal("real state change was ignored")
	}
}
