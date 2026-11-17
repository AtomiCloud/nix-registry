// planted forging: a widened (non-literal) object with an extra free key compiles; the port must refuse the key.
import { CheckoutCharges, metricTable, type MetricLabels } from './signals';
import { dropped, metrics } from '@atomicloud/diene.interfaces';
const { counter } = metrics<MetricLabels>(metricTable);
const wide = { provider: 'stripe', outcome: 'ok', shard: 3, hour: 5, user_id: 'free-customer-123' } as const;
const result = counter(CheckoutCharges, wide);
console.log('result:', JSON.stringify(result));
if (result.ok || dropped('unknown_key') !== 1) throw new Error('the port did not refuse and count dropped.unknown_key');
