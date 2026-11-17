// planted forging (review round 5): an app labels class with the exact keys of checkout.charges and free
// values, on an app counter that names the declared metric. It compiles; the port refuses its runtime type.
// ignore_for_file: avoid_print
import 'package:checkout_signals/src/signals/signals.g.dart';

final class Raw extends DurianSignalLabels {
  const Raw();

  @override
  Map<String, Object> get entries => {'provider': 'visa', 'outcome': 'ok', 'shard': 999999, 'hour': 4};
}

final class ForgedMetric extends DurianSignalCounter<Raw> {
  const ForgedMetric() : super('checkout.charges');
}

void main() {
  final port = (installSignals() as DurianSignalOk<DurianSignalPort>).value;
  final result = DurianSignalPort.add(const ForgedMetric(), const Raw());
  print('result: ${result is DurianSignalErr<bool> ? result.error : 'ok'}');
  if (result is! DurianSignalErr<bool> || port.dropped['unknown_key'] != 1) {
    throw StateError('the port did not refuse and count dropped.unknown_key');
  }
}
