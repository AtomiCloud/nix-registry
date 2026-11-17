// planted forging: an app class EXTENDS a library base (Dart has no friend libraries). It compiles; the port
// must refuse it at run time, count it, and never throw.
// ignore_for_file: avoid_print
import 'package:checkout_signals/src/signals/signals.g.dart';

// A real event name, but it is an info event in the contract.
final class ForgedEvent extends Event<ErrorLevel> {
  const ForgedEvent() : super('checkout.charge.succeeded');

  @override
  String get level => 'error';
}

void main() {
  final port = DurianSignalPort.install(metrics: metricTable, events: eventTable);
  final result = error(const ForgedEvent());
  print('result: ${result is DurianSignalErr<bool> ? result.error : 'ok'}');
  if (result is! DurianSignalErr<bool> || port.dropped['unknown_event'] != 1) {
    throw StateError('the port did not refuse and count dropped.unknown_event');
  }
}
