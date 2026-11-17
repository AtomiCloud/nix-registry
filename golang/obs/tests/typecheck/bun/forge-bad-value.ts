// planted forging (review round 5): an `as` cast carries a value the contract does not allow ("visa").
// It compiles; the port checks each value against the table's allowed values and refuses it.
import { CheckoutCharges, metricTable, type CheckoutChargesLabels, type MetricLabels } from './signals';
import { dropped, metrics } from '@atomicloud/diene.interfaces';
const { counter } = metrics<MetricLabels>(metricTable);
const raw = { provider: 'visa', outcome: 'ok', shard: 3, hour: 5 } as unknown as CheckoutChargesLabels;
const result = counter(CheckoutCharges, raw);
console.log('result:', JSON.stringify(result));
if (result.ok || dropped('unknown_value') !== 1)
  throw new Error('the port did not refuse and count dropped.unknown_value');
