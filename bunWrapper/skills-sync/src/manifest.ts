import type { DeclaredPackage, TreeEntry } from './engine.ts';

export const MANIFEST_SCHEMA_VERSION = 1;

export interface Manifest {
  schemaVersion: number;
  generator: string;
  runtime: string;
  packages: string[];
  entries: { path: string; sha256: string }[];
}

export function buildManifest(runtime: string, declared: DeclaredPackage[], entries: TreeEntry[]): Manifest {
  return {
    schemaVersion: MANIFEST_SCHEMA_VERSION,
    generator: 'skills-sync',
    runtime,
    packages: [...new Set(declared.map(d => d.name))].sort(),
    entries: entries.map(e => ({ path: e.path, sha256: e.sha256 })),
  };
}

export function renderManifest(manifest: Manifest): string {
  return `${JSON.stringify(manifest, null, 2)}\n`;
}
