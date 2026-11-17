// planted: must NOT type-check
import { CheckoutCharges, ChargeFailed, type MetricLabels } from './signals';
import { metrics, logger, tracer } from '@atomicloud/diene.interfaces';
import { metricTable, spanTable } from './signals';
const { counter } = metrics<MetricLabels>(metricTable);
const { run: logSpan } = tracer<import('./signals').SpanAttrs>(spanTable);
import { eventTable } from './signals';
logger(eventTable).warn(ChargeFailed);
void CheckoutCharges;
void ChargeFailed;
void counter;
void logger;
void logSpan;
