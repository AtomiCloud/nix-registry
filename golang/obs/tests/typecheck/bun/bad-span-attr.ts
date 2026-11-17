// planted: must NOT type-check
import { CheckoutCharges, ChargeFailed, type MetricLabels } from './signals';
declare function counter<N extends keyof MetricLabels>(name: N, labels: MetricLabels[N]): void;
declare function logWarn(ev: { readonly name: string; readonly level: 'warn' }): void;
declare function logSpan<N extends keyof import('./signals').SpanAttrs>(
  n: N,
  a: import('./signals').SpanAttrs[N],
): void;
logSpan('checkout.charge', { provider: 'visa' });
void CheckoutCharges;
void ChargeFailed;
void counter;
void logWarn;
void logSpan;
