// planted forging (review round 6): app code installs the port with a forged table BEFORE installSignals()
// runs. The generated install is then the second one: it poisons the port, which refuses every signal from
// then on, the forged one and the real one alike (a loud telemetry failure, never an app failure).
// ignore_for_file: avoid_print
import 'package:checkout_signals/src/signals/signals.g.dart';

final class ForgedEvent extends Event<ErrorLevel> {
  const ForgedEvent() : super('checkout.chrage.faild');
}

void main() {
  final first = DurianSignalPort.install(
    metrics: const {},
    events: const {'checkout.chrage.faild': 'error'},
  );
  final port = (first as DurianSignalOk<DurianSignalPort>).value;
  final generated = installSignals();
  final forged = error(const ForgedEvent());
  final real = error(chargeFailedEvent);
  print('result: ${forged is DurianSignalErr<bool> ? forged.error : 'ok'}');
  print('real: ${real is DurianSignalErr<bool> ? real.error : 'ok'}');
  if (generated is! DurianSignalErr<DurianSignalPort> ||
      forged is! DurianSignalErr<bool> ||
      real is! DurianSignalErr<bool> ||
      port.dropped['register'] != 1 ||
      port.dropped['poisoned'] != 2) {
    throw StateError('an install that ran first was used silently');
  }
}
