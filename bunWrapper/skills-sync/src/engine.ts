import { existsSync, lstatSync, readdirSync, readFileSync, statSync } from 'node:fs';
import { basename, isAbsolute, join, relative, resolve as resolvePath } from 'node:path';
import type { Config } from './config.ts';
import { compileRegex } from './config.ts';
import { configInvalid, toolFailure } from './exit.ts';
import { info, reportTermLiveness } from './report.ts';
import type { DeclareSource, ResolverSpec } from './spec.ts';

export interface DeclaredPackage {
  name: string;
  version: string | null;
  declaredIn: string;
}

export interface InstalledPackage {
  name: string;
  vendorName: string;
  packageDir: string;
  skillsDir: string | null;
}

export interface Resolution {
  installed: InstalledPackage[];
  unresolved: DeclaredPackage[];
}

export interface TreeEntry {
  path: string;
  sha256: string;
  source: string;
}

export interface DepsVerdict {
  satisfied: boolean;
  reasons: string[];
}

function readText(path: string): string {
  try {
    return readFileSync(path, 'utf8');
  } catch (e) {
    throw toolFailure(`could not read '${path}': ${(e as Error).message}`);
  }
}

function parseStructured(path: string, format: 'json' | 'yaml'): unknown {
  const text = readText(path);
  if (format === 'json') {
    try {
      return JSON.parse(text);
    } catch (e) {
      throw configInvalid(`'${path}' is not valid JSON: ${(e as Error).message}`);
    }
  }
  const yaml = (Bun as unknown as { YAML?: { parse(t: string): unknown } }).YAML;
  if (!yaml || typeof yaml.parse !== 'function') {
    throw toolFailure(`this build of bun has no Bun.YAML, so '${path}' cannot be parsed`);
  }
  try {
    return yaml.parse(text);
  } catch (e) {
    throw configInvalid(`'${path}' is not valid YAML: ${(e as Error).message}`);
  }
}

const GLOB_PRUNE = ['.git/', 'node_modules/', '.dart_tool/', '.direnv/', 'build/', '.claude/skills/vendor/'];

function sourceFiles(repoRoot: string, src: DeclareSource): string[] {
  if (src.file) {
    const path = join(repoRoot, src.file);
    return existsSync(path) ? [path] : [];
  }
  const glob = new Bun.Glob(src.glob as string);
  const found: string[] = [];
  for (const rel of glob.scanSync({ cwd: repoRoot, onlyFiles: true, dot: false, followSymlinks: false })) {
    const normalised = rel.split('\\').join('/');
    if (
      GLOB_PRUNE.some(p => normalised === p.slice(0, -1) || normalised.startsWith(p) || normalised.includes(`/${p}`))
    ) {
      continue;
    }
    found.push(join(repoRoot, rel));
  }
  return found.sort();
}

export function declaredPackages(repoRoot: string, spec: ResolverSpec, quiet = false): DeclaredPackage[] {
  const packages = new Map<string, DeclaredPackage>();
  const liveness: { term: string; hits: number }[] = [];
  let witnessedWithoutNames = false;

  for (const src of spec.declare) {
    const term = src.file ?? (src.glob as string);
    const files = sourceFiles(repoRoot, src);
    let hits = 0;

    for (const file of files) {
      const shown = relative(repoRoot, file);
      if (src.format === 'text') {
        const pattern = compileRegex(shown, 'pattern', src.pattern as string);
        const re = new RegExp(pattern.source, 'gm');
        const text = readText(file);
        for (const m of text.matchAll(re)) {
          hits += 1;
          if (src.witnessOnly) {
            witnessedWithoutNames = true;
            continue;
          }
          const name = m[src.nameGroup ?? 1];
          if (!name) continue;
          const version = src.versionGroup ? (m[src.versionGroup] ?? null) : null;
          packages.set(`${name}@${version ?? ''}`, { name, version, declaredIn: shown });
        }
      } else {
        const doc = parseStructured(file, src.format);
        if (doc === null || typeof doc !== 'object') continue;
        const match = compileRegex(shown, 'match', src.match as string);
        for (const mapName of src.maps as string[]) {
          const map = (doc as Record<string, unknown>)[mapName];
          if (map === null || typeof map !== 'object' || Array.isArray(map)) continue;
          for (const key of Object.keys(map as Record<string, unknown>)) {
            if (!match.test(key)) continue;
            hits += 1;
            if (src.witnessOnly) {
              witnessedWithoutNames = true;
              continue;
            }
            const value = (map as Record<string, unknown>)[key];
            const version = typeof value === 'string' ? value : null;
            packages.set(`${key}@${version ?? ''}`, { name: key, version, declaredIn: shown });
          }
        }
      }
    }
    liveness.push({ term: `${term} (${files.length} file(s))`, hits });
  }

  if (!quiet) reportTermLiveness('declare', liveness);

  if (witnessedWithoutNames && packages.size === 0 && !quiet) {
    info('declare: a lockfile witness matched but named no package; the naming manifest is the subject');
  }

  return [...packages.values()].sort((a, b) => (a.name < b.name ? -1 : a.name > b.name ? 1 : 0));
}

function expandTemplate(template: string, vars: Record<string, string>): string {
  return template.replace(
    /\{([a-zA-Z]+)(\|(lower|basename))?\}/g,
    (whole, key: string, _p, filter: string | undefined) => {
      const value = vars[key];
      if (value === undefined) return whole;
      if (filter === 'lower') return value.toLowerCase();
      if (filter === 'basename') return basename(value);
      return value;
    },
  );
}

function templateVars(repoRoot: string, pkg: DeclaredPackage | null): Record<string, string> {
  const vars: Record<string, string> = { home: process.env.HOME ?? '', root: repoRoot };
  if (pkg) {
    vars.name = pkg.name;
    vars.version = pkg.version ?? '';
  }
  return vars;
}

function absolutise(repoRoot: string, path: string): string {
  return isAbsolute(path) ? path : join(repoRoot, path);
}

export function dependencyVerdict(repoRoot: string, spec: ResolverSpec, declared: DeclaredPackage[]): DepsVerdict {
  const reasons: string[] = [];
  const deps = spec.deps ?? {};

  for (const p of deps.requirePath ?? []) {
    const path = absolutise(repoRoot, p);
    if (!existsSync(path)) reasons.push(`'${path}' does not exist`);
  }
  for (const cmd of deps.requireCommand ?? []) {
    if (Bun.which(cmd) === null) reasons.push(`'${cmd}' is not on PATH`);
  }
  return { satisfied: reasons.length === 0, reasons };
}

export function unresolvedReasons(unresolved: DeclaredPackage[]): string[] {
  return unresolved.map(
    p => `declared package '${p.name}${p.version ? ` ${p.version}` : ''}' (from ${p.declaredIn}) is not installed`,
  );
}

function splitConcatenatedJson(text: string): unknown[] {
  const out: unknown[] = [];
  let depth = 0;
  let start = -1;
  let inString = false;
  let escaped = false;
  for (let i = 0; i < text.length; i += 1) {
    const c = text[i];
    if (inString) {
      if (escaped) escaped = false;
      else if (c === '\\') escaped = true;
      else if (c === '"') inString = false;
      continue;
    }
    if (c === '"') {
      inString = true;
      continue;
    }
    if (c === '{') {
      if (depth === 0) start = i;
      depth += 1;
      continue;
    }
    if (c === '}') {
      depth -= 1;
      if (depth === 0 && start >= 0) {
        out.push(JSON.parse(text.slice(start, i + 1)));
        start = -1;
      }
    }
  }
  return out;
}

function vendorNameFor(spec: ResolverSpec, name: string): string {
  return (spec.resolve.vendorName ?? 'full') === 'basename' ? basename(name) : name;
}

function isDirectory(path: string): boolean {
  if (!existsSync(path)) return false;
  try {
    return statSync(path).isDirectory();
  } catch {
    return false;
  }
}

function skillsDirIfPopulated(packageDir: string, subdir: string): string | null {
  const dir = join(packageDir, subdir);
  if (!isDirectory(dir)) return null;
  return walkFiles(dir).length > 0 ? dir : null;
}

export function resolveInstalled(repoRoot: string, spec: ResolverSpec, declared: DeclaredPackage[]): Resolution {
  const wanted = new Map(declared.map(d => [d.name, d]));
  const found: InstalledPackage[] = [];
  const subdir = spec.resolve.subdir ?? 'skills';

  if (spec.resolve.strategy === 'path-template') {
    const unresolved: DeclaredPackage[] = [];
    for (const pkg of declared) {
      const packageDir = absolutise(
        repoRoot,
        expandTemplate(spec.resolve.template as string, templateVars(repoRoot, pkg)),
      );
      if (!isDirectory(packageDir)) {
        unresolved.push(pkg);
        continue;
      }
      found.push({
        name: pkg.name,
        vendorName: vendorNameFor(spec, pkg.name),
        packageDir,
        skillsDir: skillsDirIfPopulated(packageDir, subdir),
      });
    }
    return { installed: found.sort(byVendorName), unresolved };
  }

  let records: unknown[] = [];
  if (spec.resolve.strategy === 'json-file') {
    const path = absolutise(repoRoot, spec.resolve.file as string);
    if (!existsSync(path)) return { installed: [], unresolved: declared };
    const doc = parseStructured(path, 'json');
    const list = spec.resolve.listPath ? (doc as Record<string, unknown>)[spec.resolve.listPath] : doc;
    if (!Array.isArray(list)) {
      throw configInvalid(`'${path}' has no array at '${spec.resolve.listPath ?? '<root>'}'`);
    }
    records = list;
  } else {
    const argv = spec.resolve.command as string[];
    const run = Bun.spawnSync(argv, { cwd: repoRoot, stdout: 'pipe', stderr: 'pipe' });
    if (run.exitCode !== 0) {
      throw toolFailure(
        `'${argv.join(' ')}' failed with exit ${run.exitCode} in '${repoRoot}': ${new TextDecoder().decode(run.stderr).trim()}`,
      );
    }
    records = splitConcatenatedJson(new TextDecoder().decode(run.stdout));
  }

  const seen = new Set<string>();
  for (const record of records) {
    if (record === null || typeof record !== 'object') continue;
    const r = record as Record<string, unknown>;
    const name = r[spec.resolve.nameKey as string];
    const dirRaw = r[spec.resolve.dirKey as string];
    if (typeof name !== 'string' || typeof dirRaw !== 'string' || dirRaw.length === 0) continue;
    if (!wanted.has(name) || seen.has(name)) continue;

    let dir = dirRaw;
    if (dir.startsWith('file://')) dir = dir.slice('file://'.length);
    if (!isAbsolute(dir)) {
      const base = spec.resolve.dirRelativeTo ? join(repoRoot, spec.resolve.dirRelativeTo) : repoRoot;
      dir = resolvePath(base, dir);
    }
    if (!isDirectory(dir)) continue;
    seen.add(name);
    found.push({
      name,
      vendorName: vendorNameFor(spec, name),
      packageDir: dir,
      skillsDir: skillsDirIfPopulated(dir, subdir),
    });
  }

  return { installed: found.sort(byVendorName), unresolved: declared.filter(d => !seen.has(d.name)) };
}

const byVendorName = (a: InstalledPackage, b: InstalledPackage): number =>
  a.vendorName < b.vendorName ? -1 : a.vendorName > b.vendorName ? 1 : 0;

export function walkFiles(root: string): string[] {
  const out: string[] = [];
  const visit = (dir: string, prefix: string, seen: Set<string>) => {
    let entries;
    try {
      entries = readdirSync(dir, { withFileTypes: true });
    } catch (e) {
      throw toolFailure(`could not read directory '${dir}': ${(e as Error).message}`);
    }
    for (const entry of entries) {
      const abs = join(dir, entry.name);
      const rel = prefix ? `${prefix}/${entry.name}` : entry.name;
      let stat;
      try {
        stat = statSync(abs);
      } catch {
        if (lstatSync(abs).isSymbolicLink()) {
          throw toolFailure(`'${abs}' is a symlink with no target; the shipped skills tree is broken`);
        }
        continue;
      }
      if (stat.isDirectory()) {
        const real = resolvePath(abs);
        if (seen.has(real)) continue;
        seen.add(real);
        visit(abs, rel, seen);
      } else if (stat.isFile()) {
        out.push(rel);
      }
    }
  };
  visit(root, '', new Set([resolvePath(root)]));
  return out.sort();
}

export function sha256(path: string): string {
  const hasher = new Bun.CryptoHasher('sha256');
  try {
    hasher.update(readFileSync(path));
  } catch (e) {
    throw toolFailure(`could not hash '${path}': ${(e as Error).message}`);
  }
  return hasher.digest('hex');
}

export function expectedTree(installed: InstalledPackage[]): TreeEntry[] {
  const entries: TreeEntry[] = [];
  const claimed = new Map<string, string>();
  for (const pkg of installed) {
    if (pkg.skillsDir === null) continue;
    for (const rel of walkFiles(pkg.skillsDir)) {
      const path = `${pkg.vendorName}/${rel}`;
      const previous = claimed.get(path);
      if (previous !== undefined && previous !== pkg.skillsDir) {
        throw configInvalid(
          `two resolved packages both claim '${path}' ('${previous}' and '${pkg.skillsDir}'); the vendor tree cannot represent both`,
        );
      }
      claimed.set(path, pkg.skillsDir);
      entries.push({ path, sha256: sha256(join(pkg.skillsDir, rel)), source: join(pkg.skillsDir, rel) });
    }
  }
  return entries.sort((a, b) => (a.path < b.path ? -1 : a.path > b.path ? 1 : 0));
}
