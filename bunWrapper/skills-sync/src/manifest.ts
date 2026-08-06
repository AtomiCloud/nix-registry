import type { DeclaredPackage, TreeEntry } from './engine.ts';

export const MANIFEST_SCHEMA_VERSION = 1;

export interface Manifest {
  schemaVersion: number;
  generator: string;
  runtime: string;
  packages: string[];
  entries: { path: string; sha256: string }[];
}

// The manifest records only what is REPRODUCIBLE on another machine. A resolved
// source path is not: the nuget cache lives under $HOME and the go module cache
// under $GOPATH, so writing one would make every CI run disagree with every
// developer's tree and the freshness gate would be red for a reason that has
// nothing to do with the skills.
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

export function describeManifestDrift(expected: Manifest, actualText: string | null): string | null {
  if (actualText === null) return 'the vendor manifest is missing';
  let actual: unknown;
  try {
    actual = JSON.parse(actualText);
  } catch (e) {
    return `the vendor manifest is not valid JSON (${(e as Error).message})`;
  }
  if (renderManifest(expected).trim() !== JSON.stringify(actual, null, 2).trim()) {
    return 'the vendor manifest does not describe the vendored tree';
  }
  return null;
}
