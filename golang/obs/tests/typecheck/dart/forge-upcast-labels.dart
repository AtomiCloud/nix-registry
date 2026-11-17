// planted forging: review item 3 at run time: the port call with a real metric and another metric's labels. It compiles; the port
// must refuse it at run time, count it, and never throw.
// ignore_for_file: avoid_print
import 'package:checkout_signals/src/signals/signals.g.dart';

void main() {
  final port = DurianSignalPort.install(metrics: metricTable, events: eventTable);
  final result = DurianSignalPort.add(checkoutCharges, const CheckoutChargeDurationLabels(provider: Provider.stripe, outcome: Outcome.ok));
  print('result: ${result is DurianSignalErr<bool> ? result.error : 'ok'}');
  if (result is! DurianSignalErr<bool> || port.dropped['unknown_key'] != 1) {
    throw StateError('the port did not refuse and count dropped.unknown_key');
  }
}
