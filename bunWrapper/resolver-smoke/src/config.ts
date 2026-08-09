import { existsSync, readFileSync } from 'node:fs';
import { isAbsolute, join } from 'node:path';
import { configInvalid, toolFailure } from './exit.ts';

export const CONFIG_NAMES = [
  'resolver-smoke.yaml',
  'resolver-smoke.yml',
  'resolver-smoke.json',
  '.resolver-smoke.json',
] as const;

export interface Config {
  source: string | null;
  requireSubjects: boolean;
}

function fail(source: string, message: string): never {
  throw configInvalid(`resolver-smoke configuration '${source}': ${message}`);
}

function parseDocument(source: string, text: string): unknown {
  if (source.endsWith('.json')) {
    try {
      return JSON.parse(text);
    } catch (error) {
      fail(source, `is not valid JSON (${(error as Error).message})`);
    }
  }

  const yaml = (Bun as unknown as { YAML?: { parse(value: string): unknown } }).YAML;
  if (!yaml || typeof yaml.parse !== 'function') {
    throw toolFailure(
      `this build of bun has no Bun.YAML, so '${source}' cannot be parsed; resolver-smoke needs bun >= 1.2.21`,
    );
  }
  try {
    return yaml.parse(text);
  } catch (error) {
    fail(source, `is not valid YAML (${(error as Error).message})`);
  }
}

function resolveNamedPath(root: string, path: string): string {
  return isAbsolute(path) ? path : join(root, path);
}

function locateConfig(root: string, explicitPath?: string): string | null {
  const named = explicitPath ?? process.env.RESOLVER_SMOKE_CONFIG;
  if (named && named.length > 0) {
    const path = resolveNamedPath(root, named);
    if (!existsSync(path)) {
      fail(path, `is named by ${explicitPath ? '--config' : '$RESOLVER_SMOKE_CONFIG'} but does not exist`);
    }
    return path;
  }

  const found = CONFIG_NAMES.map(name => join(root, name)).filter(path => existsSync(path));
  if (found.length > 1) {
    throw configInvalid(
      `resolver-smoke found multiple configuration files (${found.join(', ')}); keep exactly one so declarations cannot disagree`,
    );
  }
  return found[0] ?? null;
}

export function loadConfig(root: string, explicitPath?: string): Config {
  const source = locateConfig(root, explicitPath);
  if (source === null) return { source: null, requireSubjects: true };

  let text = '';
  try {
    text = readFileSync(source, 'utf8');
  } catch (error) {
    throw toolFailure(`could not read resolver-smoke configuration '${source}': ${(error as Error).message}`);
  }

  const document = parseDocument(source, text);
  if (document === null || typeof document !== 'object' || Array.isArray(document)) {
    fail(source, 'must be a mapping at the top level');
  }
  const raw = document as Record<string, unknown>;
  const unknown = Object.keys(raw).filter(key => key !== 'schemaVersion' && key !== 'requireSubjects');
  if (unknown.length > 0) {
    fail(source, `has unknown key${unknown.length === 1 ? '' : 's'}: ${unknown.join(', ')}`);
  }
  if (raw.schemaVersion !== 1) {
    fail(source, `'schemaVersion' must be 1, found ${JSON.stringify(raw.schemaVersion ?? null)}`);
  }
  if (raw.requireSubjects !== undefined && typeof raw.requireSubjects !== 'boolean') {
    fail(source, `'requireSubjects' must be true or false, found ${typeof raw.requireSubjects}`);
  }

  return {
    source,
    requireSubjects: raw.requireSubjects === undefined ? true : raw.requireSubjects,
  };
}
