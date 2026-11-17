// Package obs is a minimal assumed API from concept §3.4 and goals/lib/go-interfaces.md.
// This is a compile-and-run fixture, NOT the real interfaces library. Every name below that the goal does
// not spell (the /obs package path, IntSpec methods, level markers, New*, Register, Dropped, the tables)
// is assumed, not in the goal.
package obs

import (
	"context"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"sync"
	"time"
)

type IntSpec interface {
	Name() string
	Min() int64
	Max() int64
	Unit() string
}

type Int[S IntSpec] struct {
	n  int64
	ok bool
}

func NewInt[S IntSpec](n int64) (Int[S], error) {
	var spec S
	if n < spec.Min() || n > spec.Max() {
		return Int[S]{}, fmt.Errorf("%s: %d is outside %d..%d", spec.Name(), n, spec.Min(), spec.Max())
	}
	return Int[S]{n, true}, nil
}

func AtTime[S IntSpec](t time.Time) (Int[S], error) {
	var spec S
	switch spec.Unit() {
	case "hour_of_day":
		return NewInt[S](int64(t.Hour()))
	case "weekday":
		return NewInt[S](int64(t.Weekday()))
	default:
		return Int[S]{}, fmt.Errorf("%s: not a time bucket", spec.Name())
	}
}

func (v Int[S]) Int() int64  { return v.n }
func (v Int[S]) Valid() bool { return v.ok }

type Level interface{ Level() string }
type TraceLevel struct{}
type DebugLevel struct{}
type InfoLevel struct{}
type WarnLevel struct{}
type ErrorLevel struct{}
type FatalLevel struct{}

func (TraceLevel) Level() string { return "trace" }
func (DebugLevel) Level() string { return "debug" }
func (InfoLevel) Level() string  { return "info" }
func (WarnLevel) Level() string  { return "warn" }
func (ErrorLevel) Level() string { return "error" }
func (FatalLevel) Level() string { return "fatal" }

type Event[L Level] struct{ name string }

// NewEvent is exported so the generated package can make carriers; an app that calls it is caught
// by the port (name and level checked against the registered table) and by the forbidigo lint rule.
func NewEvent[L Level](name string) Event[L] { return Event[L]{name} }
func (e Event[L]) Name() string              { return e.name }
func (Event[L]) Level() string               { var l L; return l.Level() }

// Error logs an error event. Every log call goes through logEvent: a refusal is returned and counted,
// never thrown, and never changes the app's result.
func Error(_ context.Context, ev Event[ErrorLevel], fields ...Field) error { return logEvent(ev, fields) }
func Warn(_ context.Context, ev Event[WarnLevel], fields ...Field) error   { return logEvent(ev, fields) }
func Info(_ context.Context, ev Event[InfoLevel], fields ...Field) error   { return logEvent(ev, fields) }

func logEvent[L Level](ev Event[L], _ []Field) error {
	mu.Lock()
	defer mu.Unlock()
	if ev.name == "" {
		return dropLocked("unknown_event", "unset event (zero Event{})")
	}
	level, ok := tables.Events[ev.name]
	if !ok {
		return dropLocked("unknown_event", "event %q is not in the generated event table", ev.name)
	}
	if level != ev.Level() {
		return dropLocked("unknown_event", "event %q is %s in the generated event table, not %s", ev.name, level, ev.Level())
	}
	return nil
}

type Field struct {
	Name  string
	Value any
}

type DurianSignalCounter[L any] struct{ name string }
type DurianSignalHistogram[L any] struct{ name string }
type DurianSignalGauge[L any] struct{ name string }
type DurianSignalSpan[A any] struct{ name, kind string }

// The New* constructors are exported for the generated package only (Go has no friend packages);
// the port refuses a name that is not in the registered table, and forbidigo flags calls outside
// generated files.
func NewCounter[L any](n string) DurianSignalCounter[L]     { return DurianSignalCounter[L]{n} }
func NewHistogram[L any](n string) DurianSignalHistogram[L] { return DurianSignalHistogram[L]{n} }
func NewGauge[L any](n string) DurianSignalGauge[L]         { return DurianSignalGauge[L]{n} }
func NewSpan[A any](n, k string) DurianSignalSpan[A]        { return DurianSignalSpan[A]{n, k} }
func (v DurianSignalCounter[L]) Name() string               { return v.name }
func (v DurianSignalHistogram[L]) Name() string             { return v.name }
func (v DurianSignalGauge[L]) Name() string                 { return v.name }
func (v DurianSignalSpan[A]) Name() string                  { return v.name }
func (v DurianSignalSpan[A]) Kind() string                  { return v.kind }

func Add[L any](_ context.Context, n DurianSignalCounter[L], labels L) error {
	return emit("counter", n.name, labels)
}
func Record[L any](_ context.Context, n DurianSignalHistogram[L], labels L) error {
	return emit("histogram", n.name, labels)
}
func Set[L any](_ context.Context, n DurianSignalGauge[L], labels L) error {
	return emit("gauge", n.name, labels)
}

// emit is the run-time guard of every metric call: the name must be in the registered table with
// this kind, the labels struct must carry exactly the table's keys, and no label may be unset.
func emit(kind, name string, labels any) error {
	mu.Lock()
	defer mu.Unlock()
	if name == "" {
		return dropLocked("unknown_metric", "unset metric name")
	}
	info, ok := tables.Metrics[name]
	if !ok {
		return dropLocked("unknown_metric", "metric %q is not in the generated metric table", name)
	}
	if info.Kind != kind {
		return dropLocked("unknown_metric", "metric %q is a %s in the generated metric table, not a %s", name, info.Kind, kind)
	}
	v := reflect.ValueOf(labels)
	if v.Kind() != reflect.Struct {
		return dropLocked("unknown_key", "metric %q labels are not a generated labels struct", name)
	}
	keys := make([]string, 0, v.NumField())
	for i := 0; i < v.NumField(); i++ {
		keys = append(keys, v.Type().Field(i).Tag.Get("obs"))
	}
	if !slices.Equal(keys, info.Keys) {
		return dropLocked("unknown_key", "metric %q labels %v are not its generated keys %v", name, keys, info.Keys)
	}
	for i := 0; i < v.NumField(); i++ {
		if b, ok := v.Field(i).Interface().(interface{ Valid() bool }); ok && !b.Valid() {
			return dropLocked("unset_label", "metric %q label %s is unset", name, keys[i])
		}
	}
	return nil
}

type DurianSignalMetricInfo struct {
	Kind, Unit string
	Budget     int64
	AcrossPods string
	PerPod     bool
	Keys       []string
}
type DurianSignalSpanInfo struct {
	Kind string
	Keys []string
}

// DurianSignalTables is what a generated package registers from its init(): the metric table,
// the event table (name -> level) and the span table.
type DurianSignalTables struct {
	Metrics map[string]DurianSignalMetricInfo
	Events  map[string]string
	Spans   map[string]DurianSignalSpanInfo
}

var (
	mu      sync.Mutex
	tables  = DurianSignalTables{Metrics: map[string]DurianSignalMetricInfo{}, Events: map[string]string{}, Spans: map[string]DurianSignalSpanInfo{}}
	dropped = map[string]int64{}
)

// Register adds a generated package's tables (copied, so a later write to the exported maps
// changes nothing). Called only from generated init() functions.
func Register(t DurianSignalTables) {
	mu.Lock()
	defer mu.Unlock()
	maps.Copy(tables.Metrics, t.Metrics)
	maps.Copy(tables.Events, t.Events)
	maps.Copy(tables.Spans, t.Spans)
}

// Dropped is the dropped.<reason> counter (unknown_event, unknown_metric, unknown_key, unset_label).
func Dropped(reason string) int64 {
	mu.Lock()
	defer mu.Unlock()
	return dropped[reason]
}

func dropLocked(reason, format string, a ...any) error {
	dropped[reason]++
	return fmt.Errorf("dropped.%s: "+format, append([]any{reason}, a...)...)
}
