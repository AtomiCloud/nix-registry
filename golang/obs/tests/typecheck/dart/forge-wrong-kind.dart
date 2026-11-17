// planted forging: an app class EXTENDS a library base (Dart has no friend libraries). It compiles; the port
// must refuse it at run time, count it, and never throw.
// ignore_for_file: avoid_print
import 'package:checkout_signals/src/signals/signals.g.dart';

// A histogram's name on a hand-made counter.
final class ForgedCounter extends DurianSignalCounter<CheckoutChargeDurationLabels> {
  const ForgedCounter() : super('checkout.charge.duration');
}

void main() {
  final port = DurianSignalPort.install(metrics: metricTable, events: eventTable);
  final result = DurianSignalPort.add(const ForgedCounter(), const CheckoutChargeDurationLabels(provider: Provider.stripe, outcome: Outcome.ok));
  print('result: ${result is DurianSignalErr<bool> ? result.error : 'ok'}');
  if (result is! DurianSignalErr<bool> || port.dropped['unknown_metric'] != 1) {
    throw StateError('the port did not refuse and count dropped.unknown_metric');
  }
}
