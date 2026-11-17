// Consumer sketch of the Bun call surface: everything here must type-check against signals.ts.
import {
  CheckoutCharges,
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

declare function counter<N extends keyof MetricLabels>(name: N, labels: MetricLabels[N]): void;
declare function logError(ev: { readonly name: string; readonly level: 'error' }): void;
declare function run<N extends keyof SpanAttrs & string>(name: N, attributes: SpanAttrs[N]): void;

counter(CheckoutCharges, { provider: 'stripe', outcome: 'ok', shard: 3, hour: 5 });
counter(CheckoutChargeDuration, { provider: 'adyen', outcome: 'declined' });
logError(ChargeFailed);
run('checkout.charge', { user_id: 'u-1', provider: 'paypal', shard: 15 });
run('checkout.queue.consume', {});

export const tables = [metricTable['checkout.charges'].keys, spanTable['checkout.charge'].kind, eventNames.length];
