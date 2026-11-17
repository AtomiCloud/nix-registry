// Minimal assumed API from the goal's typed signal call shapes, with enough body to RUN the planted
// forging consumers. NOT the real library. The goal spells checkoutCharges.add(...), error(Event<L>, ...),
// enums, extension types, shardSpec.of(n) and the entries getter; every other name here (DurianSignalWire,
// DurianSignalLabels, the carrier bases, DurianSignalBoundSpec and atTime, the level markers,
// DurianSignalPort, the table rows, Result/DurianSignalOk/DurianSignalErr in place of diene_result) is
// assumed, not in the goal.
//
// Shape of the type boundary (concept 3.4: one hole per language, a run-time guard and a lint rule):
// every base is `abstract base class` or sealed, so no app class can `implements` one; the bases have no
// generic add/record/set (each generated class has its own typed method, so an upcast cannot reach a
// covariant check that throws). `extends` from an app stays possible (Dart has no friend libraries): the
// port refuses such a carrier at run time (name, kind, level and label keys checked against the generated
// tables it was installed with) and the lint rule forbids it outside generated files.

/// A label value that has a wire form.
abstract interface class DurianSignalWire {
  String get wire;
}

/// The labels or attributes of a signal. Only generated classes extend it.
abstract base class DurianSignalLabels {
  const DurianSignalLabels();

  /// The wire name and value of each label, in contract order.
  Map<String, Object> get entries;
}

/// The result of making a bounded number or of a port call: a value, or the reason it was refused.
sealed class Result<T> {
  const Result._();
}

/// A value.
final class DurianSignalOk<T> extends Result<T> {
  const DurianSignalOk(this.value) : super._();

  final T value;
}

/// The reason a value was refused.
final class DurianSignalErr<T> extends Result<T> {
  const DurianSignalErr(this.error) : super._();

  final String error;
}

/// The range of a bounded number or time bucket. Abstract: only a generated subclass is made.
abstract base class DurianSignalBoundSpec<T> {
  const DurianSignalBoundSpec(
    this.name,
    this.min,
    this.max,
    this.unit,
    this._make,
  );

  final String name;
  final int min;
  final int max;

  /// hour_of_day or weekday for a time bucket, empty for a bounded number.
  final String unit;

  final T Function(int) _make;

  /// Makes a value, or says why [n] is refused (works in release builds).
  Result<T> of(int n) => n < min || n > max
      ? DurianSignalErr<T>('$name: $n is outside $min..$max')
      : DurianSignalOk<T>(_make(n));

  /// The bucket of [t] (always in range for a time bucket).
  Result<T> atTime(DateTime t) => switch (unit) {
    'hour_of_day' => of(t.hour),
    'weekday' => of(t.weekday % 7),
    _ => DurianSignalErr<T>("$name: unit '$unit' is not a time bucket"),
  };
}

/// The level markers: a sealed set, so the level of an Event<L> is fixed by L.
sealed class DurianSignalLevel {
  const DurianSignalLevel();
}

final class TraceLevel extends DurianSignalLevel {
  const TraceLevel();
}

final class DebugLevel extends DurianSignalLevel {
  const DebugLevel();
}

final class InfoLevel extends DurianSignalLevel {
  const InfoLevel();
}

final class WarnLevel extends DurianSignalLevel {
  const WarnLevel();
}

final class ErrorLevel extends DurianSignalLevel {
  const ErrorLevel();
}

final class FatalLevel extends DurianSignalLevel {
  const FatalLevel();
}

String _levelOf(Type l) => l == TraceLevel
    ? 'trace'
    : l == DebugLevel
    ? 'debug'
    : l == InfoLevel
    ? 'info'
    : l == WarnLevel
    ? 'warn'
    : l == ErrorLevel
    ? 'error'
    : l == FatalLevel
    ? 'fatal'
    : '';

/// A log event: a name carrier whose type fixes its level.
abstract base class Event<L extends DurianSignalLevel> {
  const Event(this.name);

  /// The event name (the value of the durian.cloud/event field).
  final String name;

  /// The level, derived from [L] (it cannot disagree with the type).
  String get level => _levelOf(L);
}

/// The common part of the metric name carriers. No add/record/set here: each generated class has
/// its own method typed to its own labels class.
abstract base class DurianSignalMetric<L extends DurianSignalLabels> {
  const DurianSignalMetric(this.name);

  final String name;
}

/// A counter name carrier; [L] is its labels class.
abstract base class DurianSignalCounter<L extends DurianSignalLabels>
    extends DurianSignalMetric<L> {
  const DurianSignalCounter(super.name);
}

/// A histogram name carrier.
abstract base class DurianSignalHistogram<L extends DurianSignalLabels>
    extends DurianSignalMetric<L> {
  const DurianSignalHistogram(super.name);
}

/// A gauge name carrier.
abstract base class DurianSignalGauge<L extends DurianSignalLabels>
    extends DurianSignalMetric<L> {
  const DurianSignalGauge(super.name);
}

/// A span name carrier; [A] is its attributes class.
abstract base class DurianSignalSpan<A extends DurianSignalLabels> {
  const DurianSignalSpan(this.name, this.kind);

  final String name;

  /// internal, client, server, producer or consumer.
  final String kind;
}

/// One row of metricTable.
final class DurianSignalMetricInfo {
  const DurianSignalMetricInfo({
    required this.kind,
    required this.unit,
    required this.budget,
    required this.acrossPods,
    required this.perPod,
    required this.keys,
  });

  /// counter, histogram or gauge.
  final String kind;

  /// The stored name suffix (seconds, bytes), or empty.
  final String unit;

  /// The most series the metric may store per landscape (0: none stated).
  final int budget;

  /// sum, avg, max or last; empty for counters, histograms and per-pod gauges.
  final String acrossPods;

  /// True for a gauge that keeps its pod keys.
  final bool perPod;

  /// The allowed label keys, in contract order.
  final List<String> keys;
}

/// One row of spanTable.
final class DurianSignalSpanInfo {
  const DurianSignalSpanInfo({required this.kind, required this.keys});

  final String kind;
  final List<String> keys;
}

/// The telemetry port, built from the generated tables. A refusal is an Err and a count in
/// [dropped]; it never throws and never changes the app's result.
final class DurianSignalPort {
  DurianSignalPort._(this._metrics, this._events);

  static DurianSignalPort? _current;

  /// Builds the port from the generated metricTable and eventTable and makes it the one in use.
  static DurianSignalPort install({
    required Map<String, DurianSignalMetricInfo> metrics,
    required Map<String, String> events,
  }) => _current = DurianSignalPort._(metrics, events);

  final Map<String, DurianSignalMetricInfo> _metrics;
  final Map<String, String> _events;

  /// dropped.<reason>: unknown_event, unknown_metric, unknown_key.
  final Map<String, int> dropped = {
    'unknown_event': 0,
    'unknown_metric': 0,
    'unknown_key': 0,
  };

  Result<bool> _drop(String reason, String why) {
    dropped[reason] = dropped[reason]! + 1;
    return DurianSignalErr<bool>('dropped.$reason: $why');
  }

  /// Called by a generated counter's add.
  static Result<bool> add(
    DurianSignalCounter<DurianSignalLabels> metric,
    DurianSignalLabels labels,
  ) => _emit('counter', metric.name, labels);

  /// Called by a generated histogram's record.
  static Result<bool> record(
    DurianSignalHistogram<DurianSignalLabels> metric,
    DurianSignalLabels labels,
  ) => _emit('histogram', metric.name, labels);

  /// Called by a generated gauge's set.
  static Result<bool> set(
    DurianSignalGauge<DurianSignalLabels> metric,
    DurianSignalLabels labels,
  ) => _emit('gauge', metric.name, labels);

  static Result<bool> _emit(
    String kind,
    String name,
    DurianSignalLabels labels,
  ) {
    final p = _current;
    if (p == null) return const DurianSignalErr('no port installed');
    final info = p._metrics[name];
    if (info == null) {
      return p._drop(
        'unknown_metric',
        'metric "$name" is not in the generated metric table',
      );
    }
    if (info.kind != kind) {
      return p._drop(
        'unknown_metric',
        'metric "$name" is a ${info.kind} in the generated metric table, not a $kind',
      );
    }
    final keys = labels.entries.keys.toList();
    if (keys.join(' ') != info.keys.join(' ')) {
      return p._drop(
        'unknown_key',
        'metric "$name" labels [${keys.join(' ')}] are not its generated keys [${info.keys.join(' ')}]',
      );
    }
    return const DurianSignalOk(true);
  }

  static Result<bool> _log(String level, Event<DurianSignalLevel> event) {
    final p = _current;
    if (p == null) return const DurianSignalErr('no port installed');
    final want = p._events[event.name];
    if (want == null) {
      return p._drop(
        'unknown_event',
        'event "${event.name}" is not in the generated event table',
      );
    }
    if (want != level) {
      return p._drop(
        'unknown_event',
        'event "${event.name}" is $want in the generated event table, not $level',
      );
    }
    return const DurianSignalOk(true);
  }
}

Result<bool> error(
  Event<ErrorLevel> event, [
  Map<String, Object?>? fields,
  Object? cause,
  StackTrace? stack,
]) => DurianSignalPort._log('error', event);

Result<bool> warn(Event<WarnLevel> event, [Map<String, Object?>? fields]) =>
    DurianSignalPort._log('warn', event);

Result<bool> info(Event<InfoLevel> event, [Map<String, Object?>? fields]) =>
    DurianSignalPort._log('info', event);
