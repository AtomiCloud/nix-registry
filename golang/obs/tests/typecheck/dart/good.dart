// Good consumer: the way the call surface (lib/dart-interfaces) uses the generated types.
// ignore_for_file: avoid_print
import 'package:checkout_signals/src/signals/signals.g.dart';

void main() {
  const shard = Shard(3);
  final hour = hourSpec.atTime(DateTime.utc(2026, 11, 17, 4));
  if (shardSpec.of(16) is! DurianSignalErr<Shard>) {
    throw StateError('16 accepted');
  }
  if (shardSpec.of(3) is! DurianSignalOk<Shard>) {
    throw StateError('3 refused');
  }
  final labels = CheckoutChargesLabels(
    provider: Provider.stripe,
    outcome: Outcome.ok,
    shard: shard,
    hour: (hour as DurianSignalOk<Hour>).value,
  );
  print(labels.entries);
  final CheckoutChargesMetric counter = checkoutCharges;
  const DurianSignalEvent<DurianSignalLevelError> failed = chargeFailedEvent;
  print('${counter.name} ${failed.name} ${failed.level}');
  print(BillingDay.of(DateTime.utc(2026, 11, 17)).wire);
  print(metricTable['checkout.charges']!.keys);
  print(eventNames);
  print(checkoutProviderCallSpan.kind);
}
