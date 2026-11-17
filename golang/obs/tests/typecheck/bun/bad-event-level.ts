// planted: must NOT type-check
import { CheckoutCharges, ChargeFailed, type MetricLabels } from './signals';
import { metrics, logWarn, tracer } from '@atomicloud/diene.interfaces';
import { metricTable, spanTable } from './signals';
const { counter } = metrics<MetricLabels>(metricTable);
const { run: logSpan } = tracer<import('./signals').SpanAttrs>(spanTable);
logWarn(ChargeFailed);
void CheckoutCharges;
void ChargeFailed;
void counter;
void logWarn;
void logSpan;
