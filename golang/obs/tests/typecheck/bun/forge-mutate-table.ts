// planted forging (review round 6, GPT repro): app code edits the generated tables after they were registered
// (they are plain exported objects). The port validates against a frozen deep copy taken at registration, so
// the edits change nothing: the misspelled event and the out-of-range shard are still refused.
import { ChargeFailed, CheckoutCharges, eventTable, metricTable, type MetricLabels } from './signals';
import { dropped, logger, metrics } from '@atomicloud/diene.interfaces';
const log = logger(eventTable);
const m = metrics<MetricLabels>(metricTable);
Object.assign(eventTable, { 'checkout.chrage.faild': 'error' });
Object.assign(metricTable['checkout.charges'].ranges.shard, { 1: 999999 });
const labels: MetricLabels['checkout.charges'] = { provider: 'stripe', outcome: 'ok', shard: 3, hour: 5 };
Object.assign(labels, { shard: 999999 });
const badRange = m.counter(CheckoutCharges, labels);
const forged = log.error({ name: 'checkout.chrage.faild', level: 'error' });
const real = log.error(ChargeFailed);
console.log('result:', JSON.stringify(forged), JSON.stringify(badRange));
if (forged.ok || badRange.ok || !real.ok || dropped('unknown_event') !== 1 || dropped('unknown_value') !== 1)
  throw new Error('an edit of the exported tables changed the guard');
