// planted forging (review round 5): app code installs the port a second time with its own tables. install
// works once per process: the second call is refused and counted, and poisons the port (every signal,
// even a valid one, is refused until restart).
// ignore_for_file: avoid_print
import 'package:checkout_signals/src/signals/signals.g.dart';

final class ForgedEvent extends Event<ErrorLevel> {
  const ForgedEvent() : super('app.made.up');
}

void main() {
  final port = (installSignals() as DurianSignalOk<DurianSignalPort>).value;
  final second = DurianSignalPort.install(metrics: const {}, events: const {'app.made.up': 'error'});
  print('result: ${second is DurianSignalErr<DurianSignalPort> ? second.error : 'ok'}');
  final ev = error(const ForgedEvent());
  final valid = error(chargeFailedEvent);
  if (second is! DurianSignalErr<DurianSignalPort> ||
      port.dropped['register'] != 1 ||
      ev is! DurianSignalErr<bool> ||
      valid is! DurianSignalErr<bool>) {
    throw StateError('the second install was taken');
  }
}
