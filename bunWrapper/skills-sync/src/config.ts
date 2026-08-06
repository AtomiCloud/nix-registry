import { existsSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { configInvalid, toolFailure } from './exit.ts';
import { PRESETS, PRESET_NAMES, type ResolverSpec } from './spec.ts';

export const CONFIG_DEFAULT = 'skills-sync.yaml';
export const VENDOR_DEFAULT = '.claude/skills/vendor';
export const MANIFEST_NAME = 'manifest.json';
export const KEEP_NAME = '.gitkeep';

// Files the tool itself owns inside the vendor tree. They are never skill
// content, so they are never subjects of the freshness comparison.
export const VENDOR_OWN_FILES = [KEEP_NAME, MANIFEST_NAME];

export interface Config {
  // The absolute path of the file this was read from, or null when no file
  // exists. Reported in the same sentence as every result: a verdict about a
  // configuration is worthless without naming which configuration.
  source: string | null;
  // `off` is a legitimate, owner-ruled state: the central wiring is generic and
  // INERT wherever no runtime is named.
  enabled: boolean;
  runtimeName: string | null;
  resolver: ResolverSpec | null;
  vendorDir: string;
  requireSubjects: boolean;
}

function fail(source: string, message: string): never {
  throw configInvalid(`skills-sync configuration '${source}': ${message}`);
}

function parseDocument(source: string, text: string): unknown {
  if (source.endsWith('.json')) {
    try {
      return JSON.parse(text);
    } catch (e) {
      fail(source, `is not valid JSON (${(e as Error).message})`);
    }
  }
  // Bun parses YAML natively. Guard rather than assume: an older runtime would
  // otherwise throw a TypeError that reads like a bug in this tool.
  const yaml = (Bun as unknown as { YAML?: { parse(t: string): unknown } }).YAML;
  if (!yaml || typeof yaml.parse !== 'function') {
    throw toolFailure(
      `this build of bun has no Bun.YAML, so '${source}' cannot be parsed; skills-sync needs bun >= 1.2.21`,
    );
  }
  try {
    return yaml.parse(text);
  } catch (e) {
    fail(source, `is not valid YAML (${(e as Error).message})`);
  }
}

// Locates the configuration. $SKILLS_SYNC_CONFIG wins; otherwise
// ./skills-sync.yaml, then ./skills-sync.yml, then ./skills-sync.json.
export function locateConfig(repoRoot: string): string | null {
  const override = process.env.SKILLS_SYNC_CONFIG;
  if (override && override.length > 0) {
    const path = override.startsWith('/') ? override : join(repoRoot, override);
    if (!existsSync(path)) {
      // An override that points at nothing is a mistake, never an "off". Only
      // the ABSENCE of any declaration is off; a broken pointer is loud.
      fail(path, 'is named by $SKILLS_SYNC_CONFIG but does not exist');
    }
    return path;
  }
  for (const name of [CONFIG_DEFAULT, 'skills-sync.yml', 'skills-sync.json']) {
    const path = join(repoRoot, name);
    if (existsSync(path)) return path;
  }
  return null;
}

export function loadConfig(repoRoot: string): Config {
  const source = locateConfig(repoRoot);
  if (source === null) {
    return {
      source: null,
      enabled: false,
      runtimeName: null,
      resolver: null,
      vendorDir: VENDOR_DEFAULT,
      requireSubjects: true,
    };
  }

  let text = '';
  try {
    text = readFileSync(source, 'utf8');
  } catch (e) {
    throw toolFailure(`could not read '${source}': ${(e as Error).message}`);
  }

  const doc = parseDocument(source, text);
  if (doc === null || typeof doc !== 'object' || Array.isArray(doc)) {
    fail(source, 'must be a mapping at the top level');
  }
  const raw = doc as Record<string, unknown>;

  if (raw.schemaVersion !== 1) {
    fail(source, `'schemaVersion' must be 1, found ${JSON.stringify(raw.schemaVersion ?? null)}`);
  }

  const vendorDir = typeof raw.vendorDir === 'string' && raw.vendorDir.length > 0 ? raw.vendorDir : VENDOR_DEFAULT;
  if (raw.vendorDir !== undefined && typeof raw.vendorDir !== 'string') {
    fail(source, `'vendorDir' must be a string, found ${typeof raw.vendorDir}`);
  }
  if (vendorDir.startsWith('/') || vendorDir.split('/').includes('..')) {
    fail(source, `'vendorDir' must be a path inside the repository, found '${vendorDir}'`);
  }

  if (raw.requireSubjects !== undefined && typeof raw.requireSubjects !== 'boolean') {
    fail(source, `'requireSubjects' must be true or false, found ${typeof raw.requireSubjects}`);
  }
  const requireSubjects = raw.requireSubjects === undefined ? true : (raw.requireSubjects as boolean);

  const runtimeRaw = raw.runtime;
  if (runtimeRaw !== undefined && typeof runtimeRaw !== 'string') {
    fail(source, `'runtime' must be a string, found ${typeof runtimeRaw}`);
  }
  const runtimeName = typeof runtimeRaw === 'string' ? runtimeRaw.trim() : null;

  const inline = raw.resolver;
  if (inline !== undefined && (inline === null || typeof inline !== 'object' || Array.isArray(inline))) {
    fail(source, `'resolver' must be a mapping, found ${Array.isArray(inline) ? 'array' : typeof inline}`);
  }

  // OFF is declared by naming nothing, or by naming 'none'. It is not a
  // fallback for a value the tool failed to understand: an unknown runtime name
  // is invalid configuration, because silently treating it as off is exactly how
  // a guarantee disappears from a repository that believes it has one.
  const namesNothing =
    (runtimeName === null || runtimeName.length === 0 || runtimeName === 'none') && inline === undefined;
  if (namesNothing) {
    return { source, enabled: false, runtimeName: null, resolver: null, vendorDir, requireSubjects };
  }

  if (inline !== undefined) {
    const resolver = validateResolver(source, inline as Record<string, unknown>);
    if (runtimeName && runtimeName !== 'none' && runtimeName !== resolver.name) {
      fail(
        source,
        `names runtime '${runtimeName}' and also carries an inline resolver called '${resolver.name}'; declare one or give them the same name`,
      );
    }
    return { source, enabled: true, runtimeName: resolver.name, resolver, vendorDir, requireSubjects };
  }

  const preset = PRESETS[runtimeName as string];
  if (!preset) {
    fail(
      source,
      `'runtime' is '${runtimeName}', which is not a built-in preset. Built-in presets: ${PRESET_NAMES.join(', ')}. A runtime skills-sync does not know is added with an inline 'resolver:' in this same file — never by editing skills-sync.`,
    );
  }
  return { source, enabled: true, runtimeName: preset.name, resolver: preset, vendorDir, requireSubjects };
}

// The inline resolver is validated as strictly as the presets are written: an
// unusable spec must refuse here, not resolve to zero packages later and read as
// "this repository vendors nothing".
function validateResolver(source: string, raw: Record<string, unknown>): ResolverSpec {
  const name = raw.name;
  if (typeof name !== 'string' || name.length === 0) {
    fail(source, `'resolver.name' must be a non-empty string`);
  }
  const declareSources = raw.declare;
  if (!Array.isArray(declareSources) || declareSources.length === 0) {
    fail(source, `'resolver.declare' must be a non-empty array`);
  }
  declareSources.forEach((entry, i) => {
    if (entry === null || typeof entry !== 'object' || Array.isArray(entry)) {
      fail(source, `'resolver.declare[${i}]' must be a mapping`);
    }
    const e = entry as Record<string, unknown>;
    const hasFile = typeof e.file === 'string' && e.file.length > 0;
    const hasGlob = typeof e.glob === 'string' && e.glob.length > 0;
    if (hasFile === hasGlob) {
      fail(source, `'resolver.declare[${i}]' needs exactly one of 'file' or 'glob'`);
    }
    if (e.format !== 'json' && e.format !== 'yaml' && e.format !== 'text') {
      fail(source, `'resolver.declare[${i}].format' must be one of json, yaml, text`);
    }
    if (e.format === 'text') {
      if (typeof e.pattern !== 'string' || e.pattern.length === 0) {
        fail(source, `'resolver.declare[${i}].pattern' is required for format 'text'`);
      }
      compileRegex(source, `resolver.declare[${i}].pattern`, e.pattern as string);
      if (e.nameGroup !== undefined && typeof e.nameGroup !== 'number') {
        fail(source, `'resolver.declare[${i}].nameGroup' must be a number`);
      }
    } else {
      if (!Array.isArray(e.maps) || e.maps.length === 0) {
        fail(source, `'resolver.declare[${i}].maps' must be a non-empty array for format '${String(e.format)}'`);
      }
      if (typeof e.match !== 'string' || e.match.length === 0) {
        fail(source, `'resolver.declare[${i}].match' is required for format '${String(e.format)}'`);
      }
      compileRegex(source, `resolver.declare[${i}].match`, e.match as string);
    }
  });

  const resolveSpec = raw.resolve;
  if (resolveSpec === null || typeof resolveSpec !== 'object' || Array.isArray(resolveSpec)) {
    fail(source, `'resolver.resolve' must be a mapping`);
  }
  const r = resolveSpec as Record<string, unknown>;
  const strategy = r.strategy;
  if (strategy !== 'path-template' && strategy !== 'json-file' && strategy !== 'json-command') {
    fail(source, `'resolver.resolve.strategy' must be one of path-template, json-file, json-command`);
  }
  if (strategy === 'path-template' && (typeof r.template !== 'string' || r.template.length === 0)) {
    fail(source, `'resolver.resolve.template' is required for strategy 'path-template'`);
  }
  if (strategy === 'json-file' && (typeof r.file !== 'string' || r.file.length === 0)) {
    fail(source, `'resolver.resolve.file' is required for strategy 'json-file'`);
  }
  if (strategy === 'json-command') {
    if (!Array.isArray(r.command) || r.command.length === 0 || r.command.some(c => typeof c !== 'string')) {
      fail(source, `'resolver.resolve.command' must be a non-empty array of strings for strategy 'json-command'`);
    }
  }
  if (strategy !== 'path-template') {
    if (typeof r.nameKey !== 'string' || r.nameKey.length === 0) {
      fail(source, `'resolver.resolve.nameKey' is required for strategy '${String(strategy)}'`);
    }
    if (typeof r.dirKey !== 'string' || r.dirKey.length === 0) {
      fail(source, `'resolver.resolve.dirKey' is required for strategy '${String(strategy)}'`);
    }
  }
  if (r.vendorName !== undefined && r.vendorName !== 'full' && r.vendorName !== 'basename') {
    fail(source, `'resolver.resolve.vendorName' must be 'full' or 'basename'`);
  }

  const deps = raw.deps;
  if (deps !== undefined && (deps === null || typeof deps !== 'object' || Array.isArray(deps))) {
    fail(source, `'resolver.deps' must be a mapping`);
  }

  return raw as unknown as ResolverSpec;
}

export function compileRegex(source: string, key: string, pattern: string): RegExp {
  try {
    return new RegExp(pattern);
  } catch (e) {
    fail(source, `'${key}' is not a valid regular expression: ${(e as Error).message}`);
  }
}
