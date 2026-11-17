// planted forging (review round 5): an app subclass of the bound spec with a wider range makes Shard(999999)
// in release builds; the generated labels accept it. The port checks the table's range for each label.
// ignore_for_file: avoid_print
import 'package:checkout_signals/src/signals/signals.g.dart';

final class ForgedSpec extends DurianSignalBoundSpec<Shard> {
  const ForgedSpec() : super('shard', 0, 1000000, '', Shard.new);
}

void main() {
  final port = (installSignals() as DurianSignalOk<DurianSignalPort>).value;
  final shard = (const ForgedSpec().of(999999) as DurianSignalOk<Shard>).value;
  final result = checkoutCharges.add(
    CheckoutChargesLabels(provider: Provider.stripe, outcome: Outcome.ok, shard: shard, hour: const Hour(4)),
  );
  print('result: ${result is DurianSignalErr<bool> ? result.error : 'ok'}');
  if (result is! DurianSignalErr<bool> || port.dropped['unknown_value'] != 1) {
    throw StateError('the port did not refuse and count dropped.unknown_value');
  }
}
