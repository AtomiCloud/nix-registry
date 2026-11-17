// Consumer sketch of the Bun call surface: everything here must type-check against signals.ts, and it RUNS:
// every valid call is Ok and nothing is dropped (the control for the forge-*.ts refusals).
import {
  CheckoutCharges,
  SequenceSpec,
  type Sequence,
  CheckoutChargeDuration,
  ChargeFailed,
  ChargeDeclined,
  eventNames,
  eventTable,
  metricTable,
  type MetricLabels,
  type SpanAttrs,
  spanTable,
} from './signals';
import { bounded, dropped, logger, metrics, tracer, type Result } from '@atomicloud/diene.interfaces';

// The bun-interfaces trace-port constraint: a generated `type` map must fit it (an interface would not).
type SpanAttributeValue = string | number | boolean | readonly string[] | readonly number[] | readonly boolean[];
type SpanAttributeMap = { readonly [name: string]: { readonly [key: string]: SpanAttributeValue | undefined } };
type Fits<M extends SpanAttributeMap> = M;
export type SpansFit = Fits<SpanAttrs>;

const { counter, histogram } = metrics<MetricLabels>(metricTable);
const log = logger(eventTable);
const { run } = tracer<SpanAttrs>(spanTable);

const results: Result<void>[] = [
  counter(CheckoutCharges, { provider: 'stripe', outcome: 'ok', shard: 3, hour: 5 }),
  histogram(CheckoutChargeDuration, { provider: 'adyen', outcome: 'declined' }),
  log.error(ChargeFailed),
  log.warn(ChargeDeclined, { order_id: 'o-1' }),
  run('checkout.charge', { user_id: 'u-1', provider: 'paypal', shard: 15 }, () => ({ ok: true, value: undefined })),
  run('checkout.queue.consume', {}, () => ({ ok: true, value: undefined })),
];
for (const r of results) if (!r.ok) throw new Error(`a valid generated call was refused: ${r.error}`);
for (const r of ['unknown_event', 'unknown_metric', 'unknown_key'] as const)
  if (dropped(r) !== 0) throw new Error(`a valid generated call was dropped as ${r}`);

export const tables = [metricTable['checkout.charges'].keys, spanTable['checkout.charge'].kind, eventNames.length];

const result = bounded(SequenceSpec, 3);
if (result.ok) {
  const sequence: Sequence = result.value;
  void sequence;
}
if (bounded(SequenceSpec, 10000).ok) throw new Error('10000 accepted');
console.log('good: every valid call Ok, dropped 0');
