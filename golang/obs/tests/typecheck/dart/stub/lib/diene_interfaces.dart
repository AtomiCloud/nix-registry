// Minimal assumed API from the goal's typed signal call shapes. NOT the real library.
/// A label value that has a wire form.
abstract interface class DurianSignalWire {
  String get wire;
}

/// The labels or attributes of a signal. Only the generated classes below extend it.
abstract class DurianSignalLabels {
  const DurianSignalLabels();

  /// The wire name and value of each label, in contract order.
  Map<String, Object> get entries;
}

/// The result of making a bounded number: a value, or the reason it was refused.
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

/// The range of a bounded number or time bucket. Only this library can make one.
final class DurianSignalBoundSpec<T> {
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

abstract interface class DurianSignalLevel {
  String get level;
}

abstract interface class Event<L extends DurianSignalLevel> {
  String get name;
  String get level;
}

/// A counter name carrier; [L] is its labels class.
abstract class DurianSignalCounter<L extends DurianSignalLabels> {
  const DurianSignalCounter(this.name);

  Result<bool> add(L labels) => const DurianSignalOk(true);

  final String name;
}

/// A histogram name carrier.
abstract class DurianSignalHistogram<L extends DurianSignalLabels> {
  const DurianSignalHistogram(this.name);

  Result<bool> record(L labels) => const DurianSignalOk(true);

  final String name;
}

/// A gauge name carrier.
abstract class DurianSignalGauge<L extends DurianSignalLabels> {
  const DurianSignalGauge(this.name);

  Result<bool> set(L labels) => const DurianSignalOk(true);

  final String name;
}

/// A span name carrier; [A] is its attributes class.
abstract class DurianSignalSpan<A extends DurianSignalLabels> {
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

final class TraceLevel implements DurianSignalLevel {
  const TraceLevel();
  @override
  String get level => 'trace';
}

final class DebugLevel implements DurianSignalLevel {
  const DebugLevel();
  @override
  String get level => 'debug';
}

final class InfoLevel implements DurianSignalLevel {
  const InfoLevel();
  @override
  String get level => 'info';
}

final class WarnLevel implements DurianSignalLevel {
  const WarnLevel();
  @override
  String get level => 'warn';
}

final class ErrorLevel implements DurianSignalLevel {
  const ErrorLevel();
  @override
  String get level => 'error';
}

final class FatalLevel implements DurianSignalLevel {
  const FatalLevel();
  @override
  String get level => 'fatal';
}

Result<bool> error(
  Event<ErrorLevel> event, [
  Map<String, Object?>? fields,
  Object? cause,
  StackTrace? stack,
]) => const DurianSignalOk(true);
