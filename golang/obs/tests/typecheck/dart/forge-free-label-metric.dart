// planted forging: an app class EXTENDS a library base (Dart has no friend libraries). It compiles; the port
// must refuse it at run time, count it, and never throw.
// ignore_for_file: avoid_print
import 'package:checkout_signals/src/signals/signals.g.dart';

// Review repro: a free user id label and a name not in the contract.
final class FreeLabels extends DurianSignalLabels {
  const FreeLabels();

  @override
  Map<String, Object> get entries => {'user_id': 'free-customer-123'};
}

final class ForgedMetric extends DurianSignalCounter<FreeLabels> {
  const ForgedMetric() : super('not.in.contract');
}

void main() {
  final port = DurianSignalPort.install(metrics: metricTable, events: eventTable);
  final result = DurianSignalPort.add(const ForgedMetric(), const FreeLabels());
  print('result: ${result is DurianSignalErr<bool> ? result.error : 'ok'}');
  if (result is! DurianSignalErr<bool> || port.dropped['unknown_metric'] != 1) {
    throw StateError('the port did not refuse and count dropped.unknown_metric');
  }
}
