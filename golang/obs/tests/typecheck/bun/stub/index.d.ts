// Minimal assumed API from concept §3.4 and goals/lib/bun-interfaces.md, with a small runnable index.js
// so planted forging consumers can be RUN. NOT the real library. Result is supplied by diene.result in
// production. Assumed, not in the goal: register(tables), metrics(table), logger(events), EventTable,
// dropped(), Level, MetricTable; the goal spells counter(name, labels), log.error(...), bounded(spec, n), Tracer/Span.
export type Result<T, E = string> =
  | { readonly ok: true; readonly value: T }
  | { readonly ok: false; readonly error: E };
export type Level = 'trace' | 'debug' | 'info' | 'warn' | 'error' | 'fatal';
export type Event<L extends Level> = { readonly name: string; readonly level: L };
declare const boundedBrand: unique symbol;
export type Bounded<N extends string> = number & { readonly [boundedBrand]: N };
export declare function bounded<const N extends string>(
  spec: { readonly name: N; readonly min: number; readonly max: number },
  n: number,
): Result<Bounded<N>>;
export type MetricTable<M> = {
  readonly [N in keyof M]: {
    readonly kind: string;
    readonly keys: readonly (keyof M[N])[];
    /** Allowed values per enum label. */
    readonly values?: { readonly [K in keyof M[N]]?: readonly string[] };
    /** [min, max] per bounded-number label. */
    readonly ranges?: { readonly [K in keyof M[N]]?: readonly number[] };
  };
};
export interface Metrics<M> {
  counter<N extends keyof M>(name: N, labels: M[N]): Result<void>;
  histogram<N extends keyof M>(name: N, labels: M[N]): Result<void>;
  gauge<N extends keyof M>(name: N, labels: M[N]): Result<void>;
}
/** The port refuses a name not in the table, a kind that differs, keys not in the name's key list and values
 * outside the table's allowed values or ranges. */
export declare function metrics<M>(table: MetricTable<M>): Metrics<M>;
/** The generated event table: event name -> level. */
export type EventTable = { readonly [name: string]: Level };
export interface Log {
  error(event: Event<'error'>, fields?: Record<string, unknown>, error?: Error): Result<void>;
  warn(event: Event<'warn'>, fields?: Record<string, unknown>): Result<void>;
  info(event: Event<'info'>, fields?: Record<string, unknown>): Result<void>;
  debug(event: Event<'debug'>, fields?: Record<string, unknown>): Result<void>;
}
/** The port refuses an event whose name is not in the table or whose level differs from it. */
export declare function logger(events: EventTable): Log;
/** The dropped.<reason> counter. */
export declare function dropped(
  reason: 'unknown_event' | 'unknown_metric' | 'unknown_key' | 'unknown_value' | 'register' | 'poisoned',
): number;
/**
 * Registers the generated tables, once per process: the generated signals.ts calls it when it is loaded.
 * metrics(), logger() and tracer() accept only the registered table objects. A second registration, or any
 * other table, POISONS the port: it is refused (dropped.register) and every signal is refused until
 * restart (dropped.poisoned). The guard reads a frozen deep copy taken at registration. The lint rule
 * forbids calling register outside generated files.
 */
export declare function register(tables: {
  readonly metrics?: unknown;
  readonly events?: EventTable;
  readonly spans?: unknown;
}): Result<void>;
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
