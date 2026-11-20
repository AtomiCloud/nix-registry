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
    required this.labels,
    this.values = const {},
    this.ranges = const {},
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

  /// The generated labels class of this metric: the port refuses any other runtime type.
  final Type labels;

  /// The allowed wire values per enum label.
  final Map<String, List<String>> values;

  /// The [min, max] per bounded-number label.
  final Map<String, List<int>> ranges;
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

  /// Merges a deep copy of each contract at boot, before signals flow.
  static Result<DurianSignalPort> install({
    required String contract,
    required String hash,
    required Map<String, DurianSignalMetricInfo> metrics,
    required Map<String, String> events,
    Map<String, DurianSignalSpanInfo> spans = const {},
  }) {
    final p = _current ??= DurianSignalPort._({}, {});
    Result<DurianSignalPort> refuse(String why) {
      p._poisoned = true;
      p.dropped['register'] = p.dropped['register']! + 1;
      return DurianSignalErr('dropped.register: $why');
    }

    if (p._poisoned)
      return refuse('contract "$contract": the port is already poisoned');
    if (p._closed)
      return refuse('contract "$contract" registered after the first signal');
    final copiedMetrics = metrics.map(
      (name, row) => MapEntry(
        name,
        DurianSignalMetricInfo(
          kind: row.kind,
          unit: row.unit,
          budget: row.budget,
          acrossPods: row.acrossPods,
          perPod: row.perPod,
          labels: row.labels,
          keys: List.unmodifiable(row.keys),
          values: Map.unmodifiable(
            row.values.map((k, v) => MapEntry(k, List<String>.unmodifiable(v))),
          ),
          ranges: Map.unmodifiable(
            row.ranges.map((k, v) => MapEntry(k, List<int>.unmodifiable(v))),
          ),
        ),
      ),
    );
    final copiedEvents = Map<String, String>.unmodifiable(events);
    final copiedSpans = spans.map(
      (k, v) => MapEntry(
        k,
        DurianSignalSpanInfo(kind: v.kind, keys: List.unmodifiable(v.keys)),
      ),
    );
    final fingerprint = <String, Object>{
      'hash': hash,
      'metrics': copiedMetrics.map(
        (k, v) => MapEntry(k, <Object>[
          v.kind,
          v.unit,
          v.budget,
          v.acrossPods,
          v.perPod,
          v.labels,
          v.keys,
          v.values,
          v.ranges,
        ]),
      ),
      'events': copiedEvents,
      'spans': copiedSpans.map((k, v) => MapEntry(k, <Object>[v.kind, v.keys])),
    };
    final previous = p._contracts[contract];
    if (previous != null) {
      if (_equal(previous, fingerprint)) return DurianSignalOk(p);
      return refuse(
        'contract "$contract" reused with a different hash or table',
      );
    }
    final names = {...metrics.keys, ...events.keys, ...spans.keys};
    for (final name in names) {
      final owner = p._owners[name];
      if (owner != null)
        return refuse(
          'signal "$name" is owned by both contract "$owner" and contract "$contract"',
        );
    }
    p._contracts[contract] = fingerprint;
    p._metrics.addAll(copiedMetrics);
    p._events.addAll(copiedEvents);
    p._spans.addAll(copiedSpans);
    for (final name in names) {
      p._owners[name] = contract;
    }
    return DurianSignalOk(p);
  }

  static bool _equal(Object? a, Object? b) {
    if (a is Map<Object?, Object?> && b is Map<Object?, Object?>)
      return a.length == b.length &&
          a.keys.every((Object? k) => b.containsKey(k) && _equal(a[k], b[k]));
    if (a is List<Object?> && b is List<Object?>)
      return a.length == b.length &&
          List.generate(a.length, (i) => i).every((i) => _equal(a[i], b[i]));
    return a == b;
  }

  final Map<String, Object> _contracts = {};
  final Map<String, String> _owners = {};
  final Map<String, DurianSignalSpanInfo> _spans = {};
  bool _closed = false;

  final Map<String, DurianSignalMetricInfo> _metrics;
  final Map<String, String> _events;
  bool _poisoned = false;

  /// dropped.<reason>: unknown_event, unknown_metric, unknown_key, unknown_value, register, poisoned, guard_error.
  final Map<String, int> dropped = {
    'unknown_event': 0,
    'unknown_metric': 0,
    'unknown_key': 0,
    'unknown_value': 0,
    'register': 0,
    'poisoned': 0,
    'guard_error': 0,
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
    try {
      return p._check(kind, name, labels);
    } catch (e) {
      // Last resort: a hand-made labels class can throw from its entries getter. Never rethrow.
      return p._drop('guard_error', 'metric "$name": $e');
    }
  }

  Result<bool> _check(String kind, String name, DurianSignalLabels labels) {
    final p = this;
    if (_poisoned) {
      return _drop(
        'poisoned',
        'metric "$name": the port is poisoned (an invalid install was refused); every signal is refused until restart',
      );
    }
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
    if (labels.runtimeType != info.labels) {
      return p._drop(
        'unknown_key',
        'metric "$name" labels type ${labels.runtimeType} is not its generated type ${info.labels}',
      );
    }
    final entries = labels.entries;
    final keys = entries.keys.toList();
    if (keys.join(' ') != info.keys.join(' ')) {
      return p._drop(
        'unknown_key',
        'metric "$name" labels [${keys.join(' ')}] are not its generated keys [${info.keys.join(' ')}]',
      );
    }
    for (final MapEntry(:key, :value) in entries.entries) {
      final allowed = info.values[key];
      final wire = value is DurianSignalWire ? value.wire : '$value';
      if (allowed != null && !allowed.contains(wire)) {
        return p._drop(
          'unknown_value',
          'metric "$name" label $key value "$wire" is not one of $allowed',
        );
      }
      final range = info.ranges[key];
      if (range != null &&
          (value is! int || value < range[0] || value > range[1])) {
        return p._drop(
          'unknown_value',
          'metric "$name" label $key value $value is outside ${range[0]}..${range[1]}',
        );
      }
    }
    _closed = true;
    return const DurianSignalOk(true);
  }

  static Result<bool> _log(String level, Event<DurianSignalLevel> event) {
    final p = _current;
    if (p == null) return const DurianSignalErr('no port installed');
    if (p._poisoned) {
      return p._drop(
        'poisoned',
        'event "${event.name}": the port is poisoned (an invalid install was refused); every signal is refused until restart',
      );
    }
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
    p._closed = true;
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
