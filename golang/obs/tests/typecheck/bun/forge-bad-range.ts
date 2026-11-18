// planted forging (review round 5): a widened number outside the bounded range (shard 999999) via a cast.
import { CheckoutCharges, metricTable, type CheckoutChargesLabels, type MetricLabels } from './signals';
import { dropped, metrics } from '@atomicloud/diene.interfaces';
const { counter } = metrics<MetricLabels>(metricTable);
const n: number = 999999;
const raw = { provider: 'stripe', outcome: 'ok', shard: n, hour: 5 } as CheckoutChargesLabels;
const result = counter(CheckoutCharges, raw);
console.log('result:', JSON.stringify(result));
if (result.ok || dropped('unknown_value') !== 1)
  throw new Error('the port did not refuse and count dropped.unknown_value');
