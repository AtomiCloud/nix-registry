// planted: must NOT type-check
import { CheckoutCharges, ChargeFailed, type MetricLabels } from './signals';
import { metrics, logger, tracer } from '@atomicloud/diene.interfaces';
import { metricTable, spanTable } from './signals';
const { counter } = metrics<MetricLabels>(metricTable);
const { run: logSpan } = tracer<import('./signals').SpanAttrs>(spanTable);
counter(CheckoutCharges, { provider: 'stripe', outcome: 'ok', shard: 3, hour: 5, user_id: 'u' });
void CheckoutCharges;
void ChargeFailed;
void counter;
void logger;
void logSpan;
