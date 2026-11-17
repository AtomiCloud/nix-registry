// Package obs is a minimal assumed API from concept §3.4 and goals/lib/go-interfaces.md.
// This is a compile fixture, NOT the real interfaces library.
package obs

import (
	"context"
	"fmt"
	"reflect"
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

func NewEvent[L Level](name string) Event[L] { return Event[L]{name} }
func (e Event[L]) Name() string              { return e.name }
func (Event[L]) Level() string               { var l L; return l.Level() }
func Error(_ context.Context, ev Event[ErrorLevel], fields ...Field) error {
	if ev.name == "" {
		return fmt.Errorf("unset event")
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
	if n.name == "" {
		return fmt.Errorf("unset name")
	}
	v := reflect.ValueOf(labels)
	for i := 0; i < v.NumField(); i++ {
		if b, ok := v.Field(i).Interface().(interface{ Valid() bool }); ok && !b.Valid() {
			return fmt.Errorf("unset label %s", v.Type().Field(i).Name)
		}
	}
	return nil
}
func Record[L any](_ context.Context, n DurianSignalHistogram[L], labels L) error { return nil }
func Set[L any](_ context.Context, n DurianSignalGauge[L], labels L) error        { return nil }

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
