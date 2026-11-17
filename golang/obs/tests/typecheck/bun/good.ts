// Consumer sketch of the Bun call surface: everything here must type-check against signals.ts.
import {
  CheckoutCharges,
  SequenceSpec,
  type Sequence,
  CheckoutChargeDuration,
  ChargeFailed,
  eventNames,
  metricTable,
  type MetricLabels,
  type SpanAttrs,
  spanTable,
} from './signals';

// The bun-interfaces trace-port constraint: a generated `type` map must fit it (an interface would not).
type SpanAttributeValue = string | number | boolean | readonly string[] | readonly number[] | readonly boolean[];
type SpanAttributeMap = { readonly [name: string]: { readonly [key: string]: SpanAttributeValue | undefined } };
type Fits<M extends SpanAttributeMap> = M;
export type SpansFit = Fits<SpanAttrs>;

import { metrics, logWarn, tracer } from '@atomicloud/diene.interfaces';
const { counter } = metrics<MetricLabels>(metricTable);
const { run: logSpan } = tracer<import('./signals').SpanAttrs>(spanTable);
import { logError } from '@atomicloud/diene.interfaces';
const { run } = tracer<SpanAttrs>(spanTable);

counter(CheckoutCharges, { provider: 'stripe', outcome: 'ok', shard: 3, hour: 5 });
counter(CheckoutChargeDuration, { provider: 'adyen', outcome: 'declined' });
logError(ChargeFailed);
run('checkout.charge', { user_id: 'u-1', provider: 'paypal', shard: 15 }, () => ({ ok: true, value: undefined }));
run('checkout.queue.consume', {}, () => ({ ok: true, value: undefined }));

export const tables = [metricTable['checkout.charges'].keys, spanTable['checkout.charge'].kind, eventNames.length];

import { bounded } from '@atomicloud/diene.interfaces';
const result = bounded(SequenceSpec, 3);
if (result.ok) {
  const sequence: Sequence = result.value;
  void sequence;
}
