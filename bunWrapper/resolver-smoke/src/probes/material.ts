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
  for (const match of code.matchAll(/\bwith\s+([a-zA-Z_][a-zA-Z0-9_'-]*)\s*;/g)) {
    withPreludes.add(match[1]);
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
