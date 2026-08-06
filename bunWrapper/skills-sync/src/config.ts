import { existsSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { configInvalid, toolFailure } from './exit.ts';
import { PRESETS, PRESET_NAMES, type ResolverSpec } from './spec.ts';

export const CONFIG_DEFAULT = 'skills-sync.yaml';
export const VENDOR_DEFAULT = '.claude/skills/vendor';
export const MANIFEST_NAME = 'manifest.json';
export const KEEP_NAME = '.gitkeep';

export const VENDOR_OWN_FILES = [KEEP_NAME, MANIFEST_NAME];

export interface Config {
  source: string | null;
  enabled: boolean;
  explicitOptOut: boolean;
  runtimes: string[];
  resolvers: ResolverSpec[];
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

export function locateConfig(repoRoot: string): string | null {
  const override = process.env.SKILLS_SYNC_CONFIG;
  if (override && override.length > 0) {
    const path = override.startsWith('/') ? override : join(repoRoot, override);
    if (!existsSync(path)) {
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
      explicitOptOut: false,
      runtimes: [],
      resolvers: [],
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

  if (raw.runtime !== undefined) {
    fail(source, `'runtime' is retired; declare 'runtimes: [name, ...]' (or 'runtimes: []' to opt out)`);
  }
  const runtimesRaw = raw.runtimes;
  if (runtimesRaw !== undefined && !Array.isArray(runtimesRaw)) {
    fail(source, `'runtimes' must be a list of runtime names, found ${typeof runtimesRaw}`);
  }
  const runtimes: string[] = [];
  if (Array.isArray(runtimesRaw)) {
    runtimesRaw.forEach((entry, i) => {
      if (typeof entry !== 'string' || entry.trim().length === 0) {
        fail(source, `'runtimes[${i}]' must be a non-empty runtime name`);
      }
      const name = entry.trim();
      if (runtimes.includes(name)) fail(source, `'runtimes' lists '${name}' twice`);
      runtimes.push(name);
    });
  }

  const inline = raw.resolver;
  if (inline !== undefined && (inline === null || typeof inline !== 'object' || Array.isArray(inline))) {
    fail(source, `'resolver' must be a mapping, found ${Array.isArray(inline) ? 'array' : typeof inline}`);
  }

  if (runtimes.length === 0 && inline === undefined) {
    return {
      source,
      enabled: false,
      explicitOptOut: runtimesRaw !== undefined,
      runtimes: [],
      resolvers: [],
      vendorDir,
      requireSubjects,
    };
  }

  const resolvers: ResolverSpec[] = [];
  for (const name of runtimes) {
    const preset = PRESETS[name];
    if (!preset) {
      fail(
        source,
        `'runtimes' names '${name}', which is not a built-in preset. Built-in presets: ${PRESET_NAMES.join(', ')}. A runtime skills-sync does not know is added with an inline 'resolver:' in this same file — never by editing skills-sync.`,
      );
    }
    resolvers.push(preset);
  }
  if (inline !== undefined) {
    const resolver = validateResolver(source, inline as Record<string, unknown>);
    if (resolvers.some(r => r.name === resolver.name)) {
      fail(source, `inline resolver '${resolver.name}' duplicates a name already in 'runtimes'`);
    }
    resolvers.push(resolver);
  }

  return {
    source,
    enabled: true,
    explicitOptOut: false,
    runtimes: resolvers.map(r => r.name),
    resolvers,
    vendorDir,
    requireSubjects,
  };
}

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
