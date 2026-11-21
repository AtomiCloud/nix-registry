'use strict';
// Runnable half of the stub (see index.d.ts). A refusal returns an Err and counts dropped.<reason>; it never throws.
const counts = { unknown_event: 0, unknown_metric: 0, unknown_key: 0, unknown_value: 0, register: 0, poisoned: 0 };
const ok = value => ({ ok: true, value });
const drop = (reason, error) => {
  counts[reason] += 1;
  return { ok: false, error: `dropped.${reason}: ${error}` };
};
const own = (o, k) => Object.prototype.hasOwnProperty.call(o, k);

// Merged boot registry. Only registered table objects bind; guards read frozen copies.
const { isDeepStrictEqual } = require('node:util');
const contracts = new Map();
const owners = new Map();
const registered = { metrics: new WeakSet(), events: new WeakSet(), spans: new WeakSet() };
const snapshot = { metrics: {}, events: {}, spans: {} };
let closed = false;
let poisoned = false;
const deepFreeze = o => {
  if (o && typeof o === 'object') {
    for (const v of Object.values(o)) deepFreeze(v);
    Object.freeze(o);
  }
  return o;
};
const poison = () => {
  if (!poisoned) counts.register += 1;
  poisoned = true;
};
const bind = (which, table) => {
  if (!table || !registered[which].has(table)) poison();
};
const poisonedErr = what =>
  drop(
    'poisoned',
    `${what}: the port is poisoned (a competing table was refused); every signal is refused until restart`,
  );

exports.dropped = reason => counts[reason];
exports.register = tables => {
  const refuse = why => {
    poisoned = true;
    return drop('register', why);
  };
  try {
    if (poisoned) return refuse('the port is already poisoned');
    if (closed) return refuse('registered after the first signal');
    // Read caller properties once, then clone before comparing or checking owners.
    const input = {
      contract: tables.contract,
      hash: tables.hash,
      metrics: tables.metrics,
      events: tables.events,
      spans: tables.spans,
    };
    const copy = deepFreeze(structuredClone(input));
    if (typeof copy.contract !== 'string' || !copy.contract || typeof copy.hash !== 'string')
      return refuse('invalid contract identity');
    for (const which of ['metrics', 'events', 'spans']) {
      if (input[which] != null && (typeof input[which] !== 'object' || Array.isArray(input[which])))
        return refuse('invalid registration table contents');
    }
    const previous = contracts.get(copy.contract);
    if (previous) {
      if (!isDeepStrictEqual(previous, copy))
        return refuse('contract "' + copy.contract + '" reused with a different hash or table');
    } else {
      for (const which of ['metrics', 'events', 'spans']) {
        for (const name of Object.keys(copy[which] || {})) {
          if (owners.has(name))
            return refuse(
              'signal "' +
                name +
                '" is owned by both contract "' +
                owners.get(name) +
                '" and contract "' +
                copy.contract +
                '"',
            );
        }
      }
      contracts.set(copy.contract, copy);
      for (const which of ['metrics', 'events', 'spans']) {
        for (const name of Object.keys(copy[which] || {})) owners.set(name, copy.contract);
        Object.assign(snapshot[which], copy[which]);
      }
    }
    for (const which of ['metrics', 'events', 'spans']) if (input[which]) registered[which].add(input[which]);
    return ok(undefined);
  } catch {
    return refuse('invalid registration table contents');
  }
};
exports.bounded = (spec, n) =>
  Number.isInteger(n) && n >= spec.min && n <= spec.max
    ? ok(n)
    : { ok: false, error: `${spec.name}: ${n} is outside ${spec.min}..${spec.max}` };
exports.metrics = given => {
  bind('metrics', given);
  const table = poisoned ? {} : snapshot.metrics;
  const emit = kind => (name, labels) => {
    if (poisoned) return poisonedErr(`metric "${String(name)}"`);
    if (!own(table, name))
      return drop('unknown_metric', `metric "${String(name)}" is not in the generated metric table`);
    const row = table[name];
    if (row.kind !== kind)
      return drop('unknown_metric', `metric "${name}" is a ${row.kind} in the generated metric table, not a ${kind}`);
    const extra = Object.keys(labels).filter(k => !row.keys.includes(k));
    for (const [k, v] of Object.entries(labels)) {
      const allowed = row.values?.[k];
      if (allowed && !allowed.includes(v))
        return drop('unknown_value', `metric "${name}" label ${k} value "${v}" is not one of [${allowed.join(' ')}]`);
      const range = row.ranges?.[k];
      if (range && !(Number.isInteger(v) && v >= range[0] && v <= range[1]))
        return drop('unknown_value', `metric "${name}" label ${k} value ${v} is outside ${range[0]}..${range[1]}`);
    }
    if (extra.length > 0)
      return drop(
        'unknown_key',
        `metric "${name}" keys [${extra.join(' ')}] are not in its generated keys [${row.keys.join(' ')}]`,
      );
    closed = true;
    return ok(undefined);
  };
  return { counter: emit('counter'), histogram: emit('histogram'), gauge: emit('gauge') };
};
exports.logger = given => {
  bind('events', given);
  const events = poisoned ? {} : snapshot.events;
  const log = level => event => {
    if (poisoned) return poisonedErr(`event "${event.name}"`);
    if (!own(events, event.name))
      return drop('unknown_event', `event "${event.name}" is not in the generated event table`);
    if (events[event.name] !== level || event.level !== level)
      return drop(
        'unknown_event',
        `event "${event.name}" is ${events[event.name]} in the generated event table, not ${level}`,
      );
    closed = true;
    return ok(undefined);
  };
  return { error: log('error'), warn: log('warn'), info: log('info'), debug: log('debug') };
};
exports.tracer = given => {
  bind('spans', given);
  const table = poisoned ? {} : snapshot.spans;
  return {
    run: (name, attributes, fn) => {
      if (poisoned) counts.poisoned += 1;
      const keys = !poisoned && own(table, name) ? table[name].keys : [];
      let valid = !poisoned && own(table, name);
      for (const k of Object.keys(attributes))
        if (!keys.includes(k)) {
          counts.unknown_key += 1;
          valid = false;
        }
      if (valid) closed = true;
      return fn({ set: () => ok(undefined), end: () => ok(undefined) });
    },
  };
};
