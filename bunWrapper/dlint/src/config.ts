import { existsSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { DlintError, usage } from './exit.ts';

export interface DlintConfig {
  checks: Record<string, unknown>;
}

function parseYaml(source: string, text: string): unknown {
  const yaml = (Bun as unknown as { YAML?: { parse(input: string): unknown } }).YAML;
  if (!yaml || typeof yaml.parse !== 'function') usage(`'${source}' needs Bun.YAML`);
  try {
    return yaml.parse(text);
  } catch (error) {
    usage(`'${source}' is not valid YAML: ${(error as Error).message}`);
  }
}

export function loadConfig(root: string): DlintConfig {
  const source = process.env.DLINT_CONFIG ?? join(root, 'dlint.yaml');
  if (!existsSync(source)) usage(`configuration '${source}' is absent`);
  let document: unknown;
  try {
    document = parseYaml(source, readFileSync(source, 'utf8'));
  } catch (error) {
    if (error instanceof DlintError) throw error;
    usage(`could not read '${source}': ${(error as Error).message}`);
  }
  if (document === null || typeof document !== 'object' || Array.isArray(document)) {
    usage(`'${source}' must be a mapping`);
  }
  const config = document as Record<string, unknown>;
  if (config.schemaVersion !== 1) usage(`'${source}' must set schemaVersion: 1`);
  if (config.checks === null || typeof config.checks !== 'object' || Array.isArray(config.checks)) {
    usage(`'${source}' must set checks to a mapping`);
  }
  return { checks: config.checks as Record<string, unknown> };
}
