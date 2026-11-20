// Planted late registration: a real signal closes registration before the forged table arrives.
// The late install is refused and every later signal is poisoned until restart.
// ignore_for_file: avoid_print
import 'package:checkout_signals/src/signals/signals.g.dart';

final class ForgedEvent extends Event<ErrorLevel> {
  const ForgedEvent() : super('app.made.up');
}

void main() {
  final port = (installSignals() as DurianSignalOk<DurianSignalPort>).value;
  error(chargeFailedEvent);
  final second = DurianSignalPort.install(
    contract: 'forged:observability/contract.yaml',
    hash: 'sha256:forged',
    metrics: const {},
    events: const {'app.made.up': 'error'},
  );
  print(
    'result: ${second is DurianSignalErr<DurianSignalPort> ? second.error : 'ok'}',
  );
  final ev = error(const ForgedEvent());
  final valid = error(chargeFailedEvent);
  if (second is! DurianSignalErr<DurianSignalPort> ||
      port.dropped['register'] != 1 ||
      ev is! DurianSignalErr<bool> ||
      valid is! DurianSignalErr<bool>) {
    throw StateError('the second install was taken');
  }
}
