// planted forging: an app class EXTENDS a library base (Dart has no friend libraries). It compiles; the port
// must refuse it at run time, count it, and never throw.
// ignore_for_file: avoid_print
import 'package:checkout_signals/src/signals/signals.g.dart';

final class ForgedEvent extends Event<ErrorLevel> {
  const ForgedEvent() : super('checkout.chrage.faild');
}

void main() {
  final port = DurianSignalPort.install(metrics: metricTable, events: eventTable);
  final result = error(const ForgedEvent());
  print('result: ${result is DurianSignalErr<bool> ? result.error : 'ok'}');
  if (result is! DurianSignalErr<bool> || port.dropped['unknown_event'] != 1) {
    throw StateError('the port did not refuse and count dropped.unknown_event');
  }
}
