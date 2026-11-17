// Minimal assumed API from concept §3.4 and goals/lib/bun-interfaces.md.
// Compile-only stub; NOT the real library. Result is supplied by diene.result in production.
export type Result<T, E = string> =
  | { readonly ok: true; readonly value: T }
  | { readonly ok: false; readonly error: E };
export type Event<L extends string> = { readonly name: string; readonly level: L };
declare const boundedBrand: unique symbol;
export type Bounded<N extends string> = number & { readonly [boundedBrand]: N };
export declare function bounded<const N extends string>(
  spec: { readonly name: N; readonly min: number; readonly max: number },
  n: number,
): Result<Bounded<N>>;
export type MetricTable<M> = {
  readonly [N in keyof M]: { readonly kind: string; readonly keys: readonly (keyof M[N])[] };
};
export interface Metrics<M> {
  counter<N extends keyof M>(name: N, labels: M[N]): Result<void>;
}
export declare function metrics<M>(table: MetricTable<M>): Metrics<M>;
export declare function logError(event: Event<'error'>, fields?: Record<string, unknown>, error?: Error): Result<void>;
export type SpanAttributeValue = string | number | boolean | readonly string[] | readonly number[] | readonly boolean[];
export type SpanAttributeMap = { readonly [name: string]: { readonly [key: string]: SpanAttributeValue | undefined } };
export type DurianSignalSpanTable<M> = {
  readonly [N in keyof M]: {
    readonly kind: 'internal' | 'client' | 'server' | 'producer' | 'consumer';
    readonly keys: readonly (keyof M[N])[];
  };
};
export interface Span<A> {
  set<K extends keyof A & string>(key: K, value: NonNullable<A[K]>): Result<void>;
  end(): Result<void>;
}
export interface Tracer<M extends SpanAttributeMap> {
  run<N extends keyof M & string, T, E>(
    name: N,
    attributes: M[N],
    fn: (span: Span<M[N]>) => Result<T, E> | Promise<Result<T, E>>,
  ): Result<T, E>;
}
export declare function tracer<M extends SpanAttributeMap>(table: DurianSignalSpanTable<M>): Tracer<M>;

export declare function logWarn(event: Event<'warn'>, fields?: Record<string, unknown>): Result<void>;
