// planted forging: compiles (TypeScript is structural), the port must refuse it at run time and count it.
import { CheckoutCharges, eventTable, metricTable, type MetricLabels } from './signals';
import { dropped, logger, metrics } from '@atomicloud/diene.interfaces';
const log = logger(eventTable);
const { counter } = metrics<MetricLabels>(metricTable);
void log;
void counter;
void CheckoutCharges;
const result = log.error({ name: 'checkout.chrage.faild', level: 'error' });
console.log('result:', JSON.stringify(result));
if (result.ok || dropped('unknown_event') !== 1)
  throw new Error('the port did not refuse and count dropped.unknown_event');
