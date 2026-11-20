import * as checkout from './signals';
import * as billing from './billing';
import { register, logger, metrics, dropped, tracer, type EventTable } from '@atomicloud/diene.interfaces';
declare const process: { argv: string[] };
const check = (ok: boolean, why: unknown) => {
  if (!ok) throw new Error(String(why));
};
const scenario = process.argv[2]!;
let table: { contract: string; hash: string; metrics: unknown; events: EventTable; spans: unknown } = {
  contract: '@example/checkout:observability/contract.yaml',
  hash: 'sha256:HASH',
  metrics: checkout.metricTable as unknown,
  events: checkout.eventTable,
  spans: checkout.spanTable as unknown,
};
const real = logger(checkout.eventTable),
  other = logger(billing.eventTable);
switch (scenario) {
  case 'refused-metric':
  case 'refused-event':
  case 'refused-span': {
    if (scenario === 'refused-metric')
      check(
        !metrics<checkout.MetricLabels>(checkout.metricTable).counter(
          'not.in.contract' as typeof checkout.CheckoutInvoices,
          { billing_day: '2026-11-17' as checkout.BillingDay, provider: 'stripe' },
        ).ok,
        'first metric refused',
      );
    if (scenario === 'refused-event')
      check(!real.error({ name: 'not.in.contract', level: 'error' }).ok, 'first event refused');
    if (scenario === 'refused-span') {
      const attrs = { order_id: 'o', unexpected: 'x' };
      tracer<checkout.SpanAttrs>(checkout.spanTable).run(checkout.CheckoutQueueConsume, attrs, () => ({
        ok: true,
        value: undefined,
      }));
      check(dropped('unknown_key') === 1, 'first span refused');
    }
    check(
      register({ contract: 'boot-extra:contract.yaml', hash: 'x', events: { 'boot.extra': 'error' } }).ok,
      'refused signal must leave registration open',
    );
    check(real.error(checkout.ChargeFailed).ok, 'real signal after boot registration');
    check(dropped('register') === 0 && dropped('poisoned') === 0, 'no poison after refused signal');
    break;
  }
  case 'merged':
    check(real.error(checkout.ChargeFailed).ok, 'checkout event');
    check(other.error(billing.ChargeFailed).ok, 'billing event');
    check(
      metrics<checkout.MetricLabels>(checkout.metricTable).counter(checkout.CheckoutInvoices, {
        billing_day: '2026-11-17' as checkout.BillingDay,
        provider: 'stripe',
      }).ok,
      'checkout metric',
    );
    check(
      metrics<billing.MetricLabels>(billing.metricTable).counter(billing.BillingCharges, { provider: 'stripe' }).ok,
      'billing metric',
    );
    break;
  case 'repeat':
    table = {
      ...table,
      metrics: { ...checkout.metricTable },
      events: { ...checkout.eventTable },
      spans: { ...checkout.spanTable },
    };
    check(register(table).ok, 'same id/hash/content must be no-op');
    check(logger(table.events).error(checkout.ChargeFailed).ok, 'equal table object binds');
    break;
  case 'invalid-registration':
    table = { contract: 'fresh:contract.yaml', hash: 'x', metrics: () => undefined, events: {}, spans: {} };
    break;
  case 'changed-hash':
    table.hash = 'changed';
    break;
  case 'changed-table':
    table = { ...table, events: { 'other.event': 'error' } };
    break;
  case 'duplicate-metric':
    table = {
      contract: 'other:contract.yaml',
      hash: 'x',
      metrics: { 'checkout.charges': { kind: 'counter', keys: [] } },
      events: {},
      spans: {},
    };
    break;
  case 'duplicate-event':
    table = {
      contract: 'other:contract.yaml',
      hash: 'x',
      metrics: {},
      events: { 'checkout.charge.failed': 'error' },
      spans: {},
    };
    break;
  case 'duplicate-span':
    table = {
      contract: 'other:contract.yaml',
      hash: 'x',
      metrics: {},
      events: {},
      spans: { 'checkout.charge': { kind: 'internal', keys: [] } },
    };
    break;
  case 'cross-kind':
    table = {
      contract: 'other:contract.yaml',
      hash: 'x',
      metrics: {},
      events: { 'checkout.charge': 'error' },
      spans: {},
    };
    break;
  case 'late-span':
    tracer<checkout.SpanAttrs>(checkout.spanTable).run(checkout.CheckoutQueueConsume, { order_id: 'o' }, () => ({
      ok: true,
      value: undefined,
    }));
    check(dropped('poisoned') === 0, 'first span');
    table = { contract: 'late:contract.yaml', hash: 'x', metrics: {}, events: {}, spans: {} };
    break;
  case 'late':
    check(other.error(billing.ChargeFailed).ok, 'first signal');
    table = { contract: 'late:contract.yaml', hash: 'x', metrics: {}, events: {}, spans: {} };
    break;
  case 'fresh-forgery': {
    const events = { 'forged.fresh': 'error' } as const;
    check(
      register({ contract: 'forged:contract.yaml', hash: 'x', events }).ok,
      'fresh names admitted before first signal',
    );
    check(logger(events).error({ name: 'forged.fresh', level: 'error' }).ok, 'fresh forgery admitted: lint only');
    break;
  }
}
if (!['merged', 'repeat', 'fresh-forgery'].includes(scenario) && !scenario.startsWith('refused-')) {
  const r = register(table);
  check(!r.ok, 'registration accepted');
  if (!r.ok && (scenario.startsWith('duplicate') || scenario === 'cross-kind'))
    check(
      r.error.includes('@example/checkout:observability/contract.yaml') && r.error.includes('other:contract.yaml'),
      r.error,
    );
  check(!real.error(checkout.ChargeFailed).ok, 'must poison checkout');
  check(!other.error(billing.ChargeFailed).ok, 'must poison billing');
  check(dropped('register') === 1 && dropped('poisoned') === 2, 'counts');
}
console.log('PASS multi', scenario);
