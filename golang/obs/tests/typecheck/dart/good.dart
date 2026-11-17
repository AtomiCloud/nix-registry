// Good consumer: the way the call surface (lib/dart-interfaces) uses the generated types. It RUNS: the port
// is built from the generated tables, every valid call is Ok and nothing is dropped (the control for the
// forge-*.dart refusals).
// ignore_for_file: avoid_print
import 'package:checkout_signals/src/signals/signals.g.dart';

void main() {
  final port = DurianSignalPort.install(metrics: metricTable, events: eventTable);
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
  final results = <Result<bool>>[
    checkoutCharges.add(labels),
    checkoutChargeDuration.record(
      const CheckoutChargeDurationLabels(provider: Provider.adyen, outcome: Outcome.declined),
    ),
    checkoutQueueDepth.set(const CheckoutQueueDepthLabels(lane: Lane(2))),
    error(chargeFailedEvent),
    warn(chargeDeclinedEvent, {'order_id': 'o-1'}),
    info(chargeSucceededEvent),
  ];
  for (final r in results) {
    if (r is DurianSignalErr<bool>) throw StateError('a valid call was refused: ${r.error}');
  }
  if (port.dropped.values.any((n) => n != 0)) {
    throw StateError('a valid generated call was dropped: ${port.dropped}');
  }
  print(labels.entries);
  final CheckoutChargesMetric counter = checkoutCharges;
  const Event<ErrorLevel> failed = chargeFailedEvent;
  print('${counter.name} ${failed.name} ${failed.level}');
  print(BillingDay.of(DateTime.utc(2026, 11, 17)).wire);
  print(metricTable['checkout.charges']!.keys);
  print(eventNames);
  print(checkoutProviderCallSpan.kind);
}
