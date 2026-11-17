'use strict';
// Runnable half of the stub (see index.d.ts). A refusal returns an Err and counts dropped.<reason>; it never throws.
const counts = { unknown_event: 0, unknown_metric: 0, unknown_key: 0, unknown_value: 0 };
const ok = value => ({ ok: true, value });
const drop = (reason, error) => {
  counts[reason] += 1;
  return { ok: false, error: `dropped.${reason}: ${error}` };
};
const own = (o, k) => Object.prototype.hasOwnProperty.call(o, k);
exports.dropped = reason => counts[reason];
exports.bounded = (spec, n) =>
  Number.isInteger(n) && n >= spec.min && n <= spec.max
    ? ok(n)
    : { ok: false, error: `${spec.name}: ${n} is outside ${spec.min}..${spec.max}` };
exports.metrics = table => {
  const emit = kind => (name, labels) => {
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
    return ok(undefined);
  };
  return { counter: emit('counter'), histogram: emit('histogram'), gauge: emit('gauge') };
};
exports.logger = events => {
  const log = level => event => {
    if (!own(events, event.name))
      return drop('unknown_event', `event "${event.name}" is not in the generated event table`);
    if (events[event.name] !== level || event.level !== level)
      return drop(
        'unknown_event',
        `event "${event.name}" is ${events[event.name]} in the generated event table, not ${level}`,
      );
    return ok(undefined);
  };
  return { error: log('error'), warn: log('warn'), info: log('info'), debug: log('debug') };
};
exports.tracer = table => ({
  run: (name, attributes, fn) => {
    const keys = own(table, name) ? table[name].keys : [];
    for (const k of Object.keys(attributes)) if (!keys.includes(k)) counts.unknown_key += 1;
    return fn({ set: () => ok(undefined), end: () => ok(undefined) });
  },
});
