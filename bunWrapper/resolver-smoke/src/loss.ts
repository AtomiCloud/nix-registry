import {
  compareGuardedUnits,
  guardedUnitKey,
  guardedUnitsOf,
  type GuardedKind,
  type GuardedUnit,
  type MaterialUnit,
} from './probes/material.ts';

/**
 * The two truncation caps, written once each.
 *
 * `PUBLISHED_DISCLOSURE_CAP` is NOT resolver-smoke's. It belongs to `assertNoLoss`
 * in vendor/nix.mjs, which slices the sorted lost list to 24 names and then
 * THROWS the message instead of returning the merged output. The merged output is
 * the only thing that could tell resolver-smoke which names sit past the cap, and
 * it is discarded by the throw, so the withheld names cannot be recovered from
 * outside the bundle by any honest means. Widening the disclosure has exactly one
 * possible home: `assertNoLoss` in the published bundle.
 *
 * `OWN_DISCLOSURE_CAP` is resolver-smoke's own, applies to its own
 * material-survival list, and is escapable with `--full` / `--json` because
 * resolver-smoke holds the whole list.
 *
 * Both numbers are quoted in `usage()` and in the README; they are imported from
 * here rather than retyped so the documentation cannot drift from the behaviour.
 */
export const PUBLISHED_DISCLOSURE_CAP = 24;
export const OWN_DISCLOSURE_CAP = 16;

/** The `origin.template` resolver-smoke stamps on the synthetic child it builds. */
export const CHILD_TEMPLATE = 'resolver-smoke-child';

/**
 * The invariant tail of the published loss-guard message, used to bound the unit
 * list rather than to identify the message: a unit name may contain any of `.`,
 * `'` or `-`, so the list has to be delimited by text the bundle always emits.
 */
const LOSS_TAIL =
  ". Every function argument, 'with' prelude, inherited identifier and binding present in an " +
  'input must survive into the merged output; refusing rather than emitting a file that is ' +
  'missing them.';

const KIND_BY_LABEL: Record<string, GuardedKind> = {
  'function argument': 'arg',
  binding: 'binding',
  'inherited identifier': 'inherit',
};

/**
 * Inverts the bundle's `describe()` — and resolver-smoke's own `describeUnit`,
 * which produces the same four forms.
 *
 * The lookahead is the load-bearing part. Nix identifiers may contain `'`
 * (`foo'` is a legal name), so `binding 'foo''` is a real possibility and a
 * greedy or naive split on `', '` would mis-read it. Each unit therefore only
 * matches when the very next thing is the end of the list or `, ` followed by
 * another form's opening words.
 */
const DESCRIBED_UNIT =
  /(?:(function argument|binding|inherited identifier) '(.+?)'|prelude 'with (.+?);')(?=$|, (?:function argument|binding|inherited identifier|prelude) ')/y;

function parseDescribedUnits(body: string): GuardedUnit[] | null {
  const units: GuardedUnit[] = [];
  let index = 0;
  while (index < body.length) {
    DESCRIBED_UNIT.lastIndex = index;
    const match = DESCRIBED_UNIT.exec(body);
    if (match === null) return null;
    units.push(
      match[1] === undefined ? { kind: 'with', name: match[3] } : { kind: KIND_BY_LABEL[match[1]], name: match[2] },
    );
    index = DESCRIBED_UNIT.lastIndex;
    if (index === body.length) break;
    if (!body.startsWith(', ', index)) return null;
    index += 2;
  }
  return units;
}

export interface PublishedDisclosure {
  disclosed: GuardedUnit[];
  disclosedCount: number;
  withheldCount: number;
  totalLostCount: number;
}

/**
 * Parse the published loss guard's own refusal, or return null.
 *
 * Null is the important half. The published mergers refuse for several other
 * reasons — the `all = rec { ... }` shape check, an unknown top-level key — and
 * those messages carry no lost-unit list at all. Reporting a loss detail for one
 * of them would mean inventing an inventory the resolver never published, so a
 * message that is not exactly this shape gets no loss detail and resolver-smoke
 * says nothing about it.
 */
export function parsePublishedLoss(path: string, message: string): PublishedDisclosure | null {
  const opening = `Cannot merge ${path}: the merge lost `;
  if (!message.startsWith(opening)) return null;
  const tail = message.indexOf(LOSS_TAIL, opening.length);
  if (tail === -1) return null;

  let body = message.slice(opening.length, tail);
  let withheldCount = 0;
  const more = /, plus (\d+) more$/.exec(body);
  if (more !== null) {
    withheldCount = Number(more[1]);
    body = body.slice(0, more.index);
  }

  const disclosed = parseDescribedUnits(body);
  if (disclosed === null || disclosed.length === 0) return null;
  return {
    disclosed,
    disclosedCount: disclosed.length,
    withheldCount,
    totalLostCount: disclosed.length + withheldCount,
  };
}

function dedupe(units: GuardedUnit[]): GuardedUnit[] {
  const seen = new Set<string>();
  const unique: GuardedUnit[] = [];
  for (const unit of units) {
    const key = guardedUnitKey(unit);
    if (seen.has(key)) continue;
    seen.add(key);
    unique.push(unit);
  }
  return unique.sort(compareGuardedUnits);
}

/**
 * The guarded units the synthetic child brings that no other input of this arm
 * has. This is the one part of the arithmetic resolver-smoke can state without
 * qualification, because it holds the child bytes it generated.
 *
 * It is what makes the per-arm residual delta interpretable: the bundle's
 * `lostUnits` iterates `for (const input of inputs)`, so a two-input arm
 * inventories the child's material too, and a self-probe arm cannot. On the diene
 * subject that is exactly the 8 → 10 difference between the arms.
 */
export function childContribution(childSources: string[], otherSources: string[]): GuardedUnit[] {
  const elsewhere = new Set(otherSources.flatMap(guardedUnitsOf).map(guardedUnitKey));
  return dedupe(childSources.flatMap(guardedUnitsOf).filter(unit => !elsewhere.has(guardedUnitKey(unit))));
}

/**
 * Every guarded unit of this arm's inputs that sorts strictly after the last
 * DISCLOSED unit under the bundle's own comparator.
 *
 * This is a BOUND, never a result. The published guard sorts the complete lost
 * list and only then slices off the first 24, so every withheld name is provably
 * inside this set — but so are plenty of names that survived the merge, because
 * resolver-smoke cannot see the merged output the guard threw away. It is
 * labelled `candidate (upper bound)` wherever it is printed for that reason.
 *
 * When nothing was withheld the bound is empty rather than "everything after the
 * last disclosed unit": the disclosed list is already the complete lost set, and
 * a non-empty bound on an empty remainder would read as though something were
 * still hidden.
 */
export function candidateRemainder(
  inputSources: string[],
  disclosed: GuardedUnit[],
  withheldCount: number,
): GuardedUnit[] {
  if (withheldCount === 0 || disclosed.length === 0) return [];
  const last = disclosed[disclosed.length - 1];
  return dedupe(inputSources.flatMap(guardedUnitsOf).filter(unit => compareGuardedUnits(unit, last) > 0));
}

export type LossSource = 'published-loss-guard' | 'resolver-smoke-material-survival';

export interface LossDetail {
  source: LossSource;
  disclosed: MaterialUnit[];
  disclosedCount: number;
  withheldCount: number;
  totalLostCount: number;
  childContributed: GuardedUnit[];
  candidateRemainder: GuardedUnit[];
  remainderDetermined: boolean;
}

/**
 * Build the loss detail for a published-guard refusal, or null when the message
 * is one of the other refusal shapes.
 */
export function publishedLossDetail(
  path: string,
  message: string,
  inputSources: string[],
  childSources: string[],
): LossDetail | null {
  const parsed = parsePublishedLoss(path, message);
  if (parsed === null) return null;
  const others = inputSources.filter(source => !childSources.includes(source));
  const remainder = candidateRemainder(inputSources, parsed.disclosed, parsed.withheldCount);
  return {
    source: 'published-loss-guard',
    ...parsed,
    childContributed: childContribution(childSources, others),
    candidateRemainder: remainder,
    remainderDetermined: remainder.length === parsed.withheldCount,
  };
}

/**
 * Build the loss detail for resolver-smoke's OWN material-survival list. Nothing
 * is withheld here — resolver-smoke holds the whole list — so the remainder is
 * empty and trivially determined, and the default-mode 16-name truncation is
 * presentation only.
 */
export function ownLossDetail(lost: MaterialUnit[], inputSources: string[], childSources: string[]): LossDetail {
  const others = inputSources.filter(source => !childSources.includes(source));
  return {
    source: 'resolver-smoke-material-survival',
    disclosed: lost,
    disclosedCount: lost.length,
    withheldCount: 0,
    totalLostCount: lost.length,
    childContributed: childContribution(childSources, others),
    candidateRemainder: [],
    remainderDetermined: true,
  };
}
