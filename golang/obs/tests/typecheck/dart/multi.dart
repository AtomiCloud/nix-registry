// ignore_for_file: avoid_print
import 'dart:collection';

import 'package:checkout_signals/src/signals/signals.g.dart' as checkout;
import 'package:checkout_signals/src/billing/signals.g.dart' as billing;
import 'package:diene_interfaces/diene_interfaces.dart';

void check(bool ok, Object why) {
  if (!ok) throw StateError('$why');
}

final class ForgedEvent extends Event<ErrorLevel> {
  const ForgedEvent() : super('forged.fresh');
}

final class BadMetrics extends MapBase<String, DurianSignalMetricInfo> {
  @override
  Iterable<String> get keys => throw StateError('invalid table');
  @override
  DurianSignalMetricInfo? operator [](Object? key) => null;
  @override
  void operator []=(String key, DurianSignalMetricInfo value) {}
  @override
  void clear() {}
  @override
  DurianSignalMetricInfo? remove(Object? key) => null;
}

void main(List<String> args) {
  final scenario = args[0];
  final port =
      (checkout.installSignals() as DurianSignalOk<DurianSignalPort>).value;
  check(
    billing.installSignals() is DurianSignalOk<DurianSignalPort>,
    'billing install',
  );
  var id = 'checkout_signals:observability/contract.yaml', hash = 'sha256:HASH';
  Map<String, DurianSignalMetricInfo> metrics = checkout.metricTable;
  Map<String, String> events = checkout.eventTable;
  Map<String, DurianSignalSpanInfo> spans = checkout.spanTable;
  Result<DurianSignalPort> install() => DurianSignalPort.install(
    contract: id,
    hash: hash,
    metrics: metrics,
    events: events,
    spans: spans,
  );
  switch (scenario) {
    case 'merged':
      check(
        error(checkout.chargeFailedEvent) is DurianSignalOk<bool>,
        'checkout event',
      );
      check(
        error(billing.chargeFailedEvent) is DurianSignalOk<bool>,
        'billing event',
      );
      check(
        checkout.checkoutInvoices.add(
          checkout.CheckoutInvoicesLabels(
            billingDay: checkout.BillingDay.of(DateTime.utc(2026, 11, 17)),
            provider: checkout.Provider.stripe,
          ),
        ) is DurianSignalOk<bool>,
        'checkout metric',
      );
      check(
        billing.billingCharges.add(
          const billing.BillingChargesLabels(provider: billing.Provider.stripe),
        ) is DurianSignalOk<bool>,
        'billing metric',
      );
      break;
    case 'repeat':
      metrics = Map.of(metrics);
      events = Map.of(events);
      spans = Map.of(spans);
      check(
        install() is DurianSignalOk<DurianSignalPort>,
        'same id/hash/content no-op',
      );
      break;
    case 'invalid-registration':
      id = 'fresh:contract.yaml';
      events = {};
      spans = {};
      metrics = BadMetrics();
      break;
    case 'changed-hash':
      hash = 'changed';
      break;
    case 'changed-table':
      events = {'other.event': 'error'};
      break;
    case 'duplicate-metric':
      id = 'other:contract.yaml';
      metrics = {'checkout.charges': checkout.metricTable['checkout.charges']!};
      events = {};
      spans = {};
      break;
    case 'duplicate-event':
      id = 'other:contract.yaml';
      metrics = {};
      events = {'checkout.charge.failed': 'error'};
      spans = {};
      break;
    case 'duplicate-span':
      id = 'other:contract.yaml';
      metrics = {};
      events = {};
      spans = {'checkout.charge': checkout.spanTable['checkout.charge']!};
      break;
    case 'cross-kind':
      id = 'other:contract.yaml';
      metrics = {};
      events = {'checkout.charge': 'error'};
      spans = {};
      break;
    case 'late-span':
      check(
        DurianSignalPort.startSpan(
          checkout.checkoutQueueConsumeSpan,
          const checkout.CheckoutQueueConsumeAttributes(orderId: 'o'),
        ) is DurianSignalOk<bool>,
        'first span',
      );
      id = 'late:contract.yaml';
      metrics = {};
      events = {};
      spans = {};
      break;
    case 'late':
      check(
        error(billing.chargeFailedEvent) is DurianSignalOk<bool>,
        'first signal',
      );
      id = 'late:contract.yaml';
      metrics = {};
      events = {};
      spans = {};
      break;
    case 'fresh-forgery':
      id = 'forged:contract.yaml';
      hash = 'forged';
      metrics = {};
      events = {'forged.fresh': 'error'};
      spans = {};
      check(
        install() is DurianSignalOk<DurianSignalPort>,
        'fresh names admitted',
      );
      check(
        error(const ForgedEvent()) is DurianSignalOk<bool>,
        'fresh forgery admitted: lint only',
      );
      break;
  }
  if (!['merged', 'repeat', 'fresh-forgery'].contains(scenario)) {
    final r = install();
    check(r is DurianSignalErr<DurianSignalPort>, 'registration accepted');
    if (r is DurianSignalErr<DurianSignalPort> &&
        (scenario.startsWith('duplicate') || scenario == 'cross-kind'))
      check(
        r.error.contains('checkout_signals:observability/contract.yaml') &&
            r.error.contains('other:contract.yaml'),
        r.error,
      );
    check(
      error(checkout.chargeFailedEvent) is DurianSignalErr<bool>,
      'must poison checkout',
    );
    check(
      error(billing.chargeFailedEvent) is DurianSignalErr<bool>,
      'must poison billing',
    );
    check(
      port.dropped['register'] == 1 && port.dropped['poisoned'] == 2,
      'counts',
    );
  }
  print('PASS multi $scenario');
}
