const NIX_KEYWORDS = new Set([
  'assert',
  'else',
  'false',
  'if',
  'in',
  'inherit',
  'let',
  'null',
  'or',
  'rec',
  'then',
  'true',
  'with',
]);

function spacesLike(value: string): string {
  return value.replace(/[^\n]/g, ' ');
}

/**
 * Replace comments and string literals with spaces while preserving newlines and
 * character offsets. The oracle must not mistake `# fake = binding` or a string
 * containing Nix-looking text for material the resolver was asked to merge.
 */
export function maskNixTrivia(source: string): string {
  let output = '';
  let index = 0;

  while (index < source.length) {
    if (source[index] === '#') {
      const end = source.indexOf('\n', index);
      const stop = end === -1 ? source.length : end;
      output += spacesLike(source.slice(index, stop));
      index = stop;
      continue;
    }
    if (source.startsWith('/*', index)) {
      const close = source.indexOf('*/', index + 2);
      const stop = close === -1 ? source.length : close + 2;
      output += spacesLike(source.slice(index, stop));
      index = stop;
      continue;
    }
    if (source[index] === '"') {
      let stop = index + 1;
      while (stop < source.length) {
        if (source[stop] === '\\') {
          stop += 2;
          continue;
        }
        stop++;
        if (source[stop - 1] === '"') break;
      }
      output += spacesLike(source.slice(index, stop));
      index = stop;
      continue;
    }
    if (source.startsWith("''", index)) {
      const close = source.indexOf("''", index + 2);
      const stop = close === -1 ? source.length : close + 2;
      output += spacesLike(source.slice(index, stop));
      index = stop;
      continue;
    }

    output += source[index];
    index++;
  }
  return output;
}

function splitTopLevel(value: string): string[] {
  const parts: string[] = [];
  let start = 0;
  let braces = 0;
  let brackets = 0;
  let parentheses = 0;

  for (let index = 0; index < value.length; index++) {
    switch (value[index]) {
      case '{':
        braces++;
        break;
      case '}':
        braces--;
        break;
      case '[':
        brackets++;
        break;
      case ']':
        brackets--;
        break;
      case '(':
        parentheses++;
        break;
      case ')':
        parentheses--;
        break;
      case ',':
        if (braces === 0 && brackets === 0 && parentheses === 0) {
          parts.push(value.slice(start, index));
          start = index + 1;
        }
        break;
    }
  }
  parts.push(value.slice(start));
  return parts;
}

export interface FunctionHeader {
  args: string[];
  colonIndex: number;
}

/** Parse a Nix argument-set function header, including multiline/defaulted forms. */
export function findFunctionHeader(source: string): FunctionHeader | null {
  const code = maskNixTrivia(source);
  const open = code.search(/\S/);
  if (open === -1 || code[open] !== '{') return null;

  let depth = 0;
  let close = -1;
  for (let index = open; index < code.length; index++) {
    if (code[index] === '{') depth++;
    if (code[index] === '}') {
      depth--;
      if (depth === 0) {
        close = index;
        break;
      }
    }
  }
  if (close === -1) return null;

  let colon = close + 1;
  while (colon < code.length && /\s/.test(code[colon])) colon++;
  if (code[colon] !== ':') return null;

  const args = splitTopLevel(code.slice(open + 1, close))
    .map(part => part.trim())
    .filter(part => part.length > 0 && part !== '...')
    .map(part => part.match(/^([a-zA-Z_][a-zA-Z0-9_'-]*)\b/)?.[1])
    .filter((part): part is string => part !== undefined);
  return { args: [...new Set(args)], colonIndex: colon };
}

function withoutLeadingInheritSource(value: string): string {
  const trimmed = value.trimStart();
  if (!trimmed.startsWith('(')) return trimmed;
  let depth = 0;
  for (let index = 0; index < trimmed.length; index++) {
    if (trimmed[index] === '(') depth++;
    if (trimmed[index] === ')') {
      depth--;
      if (depth === 0) return trimmed.slice(index + 1);
    }
  }
  return trimmed;
}

export interface MaterialInventory {
  units: Set<string>;
  args: Set<string>;
  bindings: Set<string>;
  inherited: Set<string>;
  identifiers: Set<string>;
  withPreludes: Set<string>;
}

/**
 * The four kinds the published bundle's loss guard inventories, in the bundle's
 * own spelling. `identifier` is resolver-smoke's own FIFTH kind and is
 * deliberately not here: the bundle has no equivalent, so any claim made about
 * what the bundle withheld has to be made over these four and no others.
 */
export const GUARDED_KINDS = ['arg', 'binding', 'inherit', 'with'] as const;
export type GuardedKind = (typeof GUARDED_KINDS)[number];

export interface MaterialUnit {
  kind: string;
  name: string;
}

export interface GuardedUnit extends MaterialUnit {
  kind: GuardedKind;
}

/**
 * `args`, `bindings`, `inherited` and `withPreludes` below are kept
 * BYTE-EQUIVALENT to `inventoryMaterial` in vendor/nix.mjs on purpose, down to
 * the regexes. resolver-smoke claims that every name the published loss guard
 * withheld is inside the candidate remainder it prints, and that claim is only
 * true if resolver-smoke inventories the same units the guard does: a kind
 * resolver-smoke cannot see is a name the bound cannot cover. The `with` regex
 * in particular accepts a DOTTED path and strips interior whitespace, so
 * `with pkgs.lib;` is one unit `with:pkgs.lib` rather than nothing at all.
 *
 * `identifiers` is the one deliberate addition. The bundle has no equivalent and
 * does not guard it; resolver-smoke's own material-survival probe uses it to
 * catch losses the guard permits, and it is excluded from every guarded-kind
 * computation for exactly that reason.
 *
 * A vendor refresh MUST re-check this equivalence — see the vendor-refresh
 * section of README.md. It is load-bearing now, not cosmetic.
 */
export function inventoryMaterial(source: string): MaterialInventory {
  const code = maskNixTrivia(source);
  const header = findFunctionHeader(source);
  const args = new Set(header?.args ?? []);
  const bindings = new Set<string>();
  const inherited = new Set<string>();
  const identifiers = new Set<string>();
  const withPreludes = new Set<string>();

  for (const match of code.matchAll(/(?:^|[\n;{])\s*([a-zA-Z_][a-zA-Z0-9_'-]*(?:\.[a-zA-Z_][a-zA-Z0-9_'-]*)*)\s*=/gm)) {
    // Nix treats `hook = { enable = true; };` and
    // `hook.enable = true;` as the same attribute path, and the published
    // merger legitimately re-renders between those forms. Compare the
    // semantic path segments so pretty-printing cannot create a false loss;
    // the identifier inventory below still proves that every segment itself
    // survives.
    for (const segment of match[1].split('.')) bindings.add(segment);
  }
  for (const match of code.matchAll(/\binherit\b([^;]*);/g)) {
    const body = withoutLeadingInheritSource(match[1]);
    for (const identifier of body.match(/[a-zA-Z_][a-zA-Z0-9_'-]*/g) ?? []) {
      inherited.add(identifier);
    }
  }
  for (const match of code.matchAll(/\bwith\s+([a-zA-Z_][a-zA-Z0-9_'-]*(?:\s*\.\s*[a-zA-Z_][a-zA-Z0-9_'-]*)*)\s*;/g)) {
    withPreludes.add(match[1].replace(/\s+/g, ''));
  }
  for (const identifier of code.match(/[a-zA-Z_][a-zA-Z0-9_'-]*/g) ?? []) {
    if (!NIX_KEYWORDS.has(identifier)) identifiers.add(identifier);
  }

  const units = new Set<string>();
  for (const value of args) units.add(`arg:${value}`);
  for (const value of bindings) units.add(`binding:${value}`);
  for (const value of inherited) units.add(`inherit:${value}`);
  for (const value of identifiers) units.add(`identifier:${value}`);
  for (const value of withPreludes) units.add(`with:${value}`);

  return { units, args, bindings, inherited, identifiers, withPreludes };
}

/**
 * The bundle's own ordering, reproduced exactly:
 *
 *   lost.sort((a, b) => a.kind === b.kind
 *     ? a.value.localeCompare(b.value)
 *     : a.kind.localeCompare(b.kind));
 *
 * The published loss guard sorts the whole lost list with this comparator and
 * only THEN slices the first 24, so every name it withheld sorts strictly after
 * the last name it disclosed. That single fact is what turns the truncated
 * message into a provable bound, and it holds only while this comparator matches
 * the bundle's — `localeCompare` included, which orders `dn-inspect` before
 * `doInstallCheck` where a code-unit comparison would not.
 */
export function compareGuardedUnits(a: MaterialUnit, b: MaterialUnit): number {
  return a.kind === b.kind ? a.name.localeCompare(b.name) : a.kind.localeCompare(b.kind);
}

export function guardedUnitKey(unit: MaterialUnit): string {
  return `${unit.kind}:${unit.name}`;
}

/** Every guarded unit of one source, in the bundle's order. */
export function guardedUnitsOf(source: string): GuardedUnit[] {
  const inventory = inventoryMaterial(source);
  const units: GuardedUnit[] = [];
  for (const name of inventory.args) units.push({ kind: 'arg', name });
  for (const name of inventory.bindings) units.push({ kind: 'binding', name });
  for (const name of inventory.inherited) units.push({ kind: 'inherit', name });
  for (const name of inventory.withPreludes) units.push({ kind: 'with', name });
  return units.sort(compareGuardedUnits);
}

/** Split a `<kind>:<name>` inventory key back into its parts. */
export function splitUnitKey(unit: string): MaterialUnit {
  const separator = unit.indexOf(':');
  return separator === -1
    ? { kind: 'identifier', name: unit }
    : { kind: unit.slice(0, separator), name: unit.slice(separator + 1) };
}

export function describeMaterialUnit(unit: MaterialUnit): string {
  return describeUnit(guardedUnitKey(unit));
}

export function describeUnit(unit: string): string {
  const [kind, value] = unit.split(':', 2);
  switch (kind) {
    case 'arg':
      return `function argument '${value}'`;
    case 'binding':
      return `binding '${value}'`;
    case 'inherit':
      return `inherited identifier '${value}'`;
    case 'with':
      return `prelude 'with ${value};'`;
    default:
      return `identifier '${value}'`;
  }
}
