import { existsSync, readFileSync, writeFileSync, mkdirSync } from 'node:fs';
import { dirname, join, basename } from 'node:path';
import {
  type Config,
  type Outline,
  type Entry,
  type Page,
  type Issue,
  readYaml,
  entriesOf,
  planned,
  canonical,
  ranks,
  inside,
  issue,
  yamlText,
  slug,
  writeFrontMatter,
  loadPage,
} from './model';

const obj = (v: any) => v && typeof v === 'object' && !Array.isArray(v);
function keys(v: any, allowed: string[], where: string) {
  if (!obj(v)) throw new Error(`${where} must be a mapping`);
  for (const k of Object.keys(v)) if (!allowed.includes(k)) throw new Error(`${where}: unknown field ${k}`);
}
export function handwritten(docs: string): Outline {
  const raw = readYaml(join(docs, 'outline.yaml'));
  keys(raw, ['repoName', 'contexts', 'guides', 'repo', 'readingPath'], 'outline');
  const rows = (v: any, prefix: string): Entry[] => {
    if (v == null) return [];
    if (!Array.isArray(v)) throw new Error(`${prefix} pages must be a list`);
    return v.map((e: any) => {
      keys(e, ['page', 'type', 'rank', 'learns', 'sources'], 'outline entry');
      if (
        typeof e.page !== 'string' ||
        !e.page.endsWith('.md') ||
        !inside(docs, join(docs, prefix, e.page)) ||
        e.page.startsWith('/') ||
        e.page.split('/').includes('..')
      )
        throw new Error('outline entry needs a relative Markdown page path');
      if (!ranks.includes(e.rank)) throw new Error(`${e.page}: rank must be core, supporting or edge`);
      if (typeof e.learns !== 'string' || !e.learns.trim() || e.learns.length > 160 || /\n/.test(e.learns))
        throw new Error(`${e.page}: learns must be one line, 1..160 characters`);
      // Old outlines explicitly list generated context overviews. They remain readable on migration.
      if (e.page !== 'index.md' && (typeof e.type !== 'string' || !e.type))
        throw new Error(`${e.page}: type is required`);
      if (
        e.sources != null &&
        (!Array.isArray(e.sources) ||
          e.sources.some((s: any) => typeof s !== 'string' || !/^code:[^/\s]+\/[^\s]+$/.test(s)))
      )
        throw new Error(`${e.page}: sources must be code references`);
      return {
        full: prefix + e.page,
        type: e.type,
        rank: e.rank,
        learns: e.learns,
        ...(e.sources ? { sources: e.sources } : {}),
      };
    });
  };
  if (raw.contexts != null && !Array.isArray(raw.contexts)) throw new Error('contexts must be a list');
  const names = new Set<string>();
  const contexts = (raw.contexts ?? []).map((c: any) => {
    keys(c, ['context', 'learns', 'pages'], 'context');
    if (typeof c.context !== 'string' || !/^[a-z][a-z0-9-]*$/.test(c.context) || names.has(c.context))
      throw new Error('context needs a unique folder name');
    names.add(c.context);
    if (c.learns != null && (typeof c.learns !== 'string' || c.learns.length > 160 || /\n/.test(c.learns)))
      throw new Error('context learns must be one line of at most 160 characters');
    return { context: c.context, learns: c.learns, pages: rows(c.pages, `domain/${c.context}/`) };
  });
  if (
    raw.readingPath != null &&
    (!Array.isArray(raw.readingPath) || raw.readingPath.some((p: any) => typeof p !== 'string'))
  )
    throw new Error('readingPath must be a list of paths');
  const out = {
    repoName: raw.repoName,
    contexts,
    guides: rows(raw.guides, 'guides/'),
    repo: rows(raw.repo, ''),
    readingPath: raw.readingPath ?? [],
  };
  const seen = new Set<string>();
  for (const e of entriesOf(out)) {
    const k = canonical(e.full);
    if (seen.has(k)) throw new Error(`duplicate outline entry ${e.full}`);
    seen.add(k);
  }
  for (const p of out.readingPath)
    if (!seen.has(canonical(p))) throw new Error(`readingPath ${p} is not an outline entry`);
  return out;
}
export function fromPages(docs: string, pages: Page[], issues: Issue[]): Outline {
  const out: Outline = { contexts: [], guides: [], repo: [], readingPath: [] };
  const orders = new Set<string>();
  for (const p of pages.filter(planned)) {
    const f = p.fm;
    if (!ranks.includes(f.rank))
      issue(issues, p.file, 'outline in step', 'mode A requires rank: core, supporting or edge');
    if (!Number.isSafeInteger(f.order) || f.order < 1)
      issue(issues, p.file, 'outline in step', 'mode A requires a positive integer order within its folder');
    const k = `${dirname(p.rel)}:${f.order}`;
    if (orders.has(k)) issue(issues, p.file, 'outline in step', `duplicate order ${f.order} in ${dirname(p.rel)}`);
    orders.add(k);
    const learns = f.learns ?? f.description;
    if (typeof learns !== 'string' || !learns.trim() || learns.length > 160 || /\n/.test(learns))
      issue(issues, p.file, 'outline in step', 'learns must be one line, 1..160 characters');
    if (f.readingPath != null && (!Number.isSafeInteger(f.readingPath) || f.readingPath < 1))
      issue(issues, p.file, 'outline in step', 'readingPath must be a positive integer');
    const e: Entry = {
      full: p.rel,
      type: f.type,
      rank: f.rank,
      learns,
      order: f.order,
      ...(f.sources ? { sources: f.sources } : {}),
    };
    const m = p.rel.match(/^domain\/([^/]+)\//);
    if (m) {
      let c = out.contexts.find(c => c.context === m[1]);
      if (!c) {
        const ov = join(docs, 'domain', m[1], 'overview.yaml');
        let learns = m[1];
        try {
          const intro = readYaml(ov)?.intro;
          if (typeof intro === 'string') learns = intro.trim().split(/\n/).join(' ').slice(0, 160);
        } catch {}
        c = { context: m[1], learns, pages: [] };
        out.contexts.push(c);
      }
      c.pages.push(e);
    } else if (p.rel.startsWith('guides/')) out.guides.push(e);
    else out.repo.push(e);
  }
  const sort = (es: Entry[]) =>
    es.sort(
      (a, b) =>
        dirname(a.full).localeCompare(dirname(b.full)) ||
        (a.order ?? 0) - (b.order ?? 0) ||
        a.full.localeCompare(b.full),
    );
  out.contexts.sort((a, b) => a.context.localeCompare(b.context)).forEach(c => sort(c.pages));
  sort(out.guides);
  sort(out.repo);
  const reading = pages
    .filter(p => planned(p) && Number.isSafeInteger(p.fm.readingPath))
    .sort((a, b) => a.fm.readingPath - b.fm.readingPath);
  if (new Set(reading.map(p => p.fm.readingPath)).size !== reading.length)
    issue(issues, join(docs, 'outline.yaml'), 'outline in step', 'readingPath positions must be unique');
  out.readingPath = reading.map(p => p.rel);
  return out;
}
export function loadOutline(docs: string, c: Config, pages: Page[], issues: Issue[]): Outline {
  if (c.pageOrder === 'A') return fromPages(docs, pages, issues);
  try {
    return handwritten(docs);
  } catch (e) {
    issue(issues, join(docs, 'outline.yaml'), 'outline in step', (e as Error).message);
    return { contexts: [], guides: [], repo: [], readingPath: [] };
  }
}
export function outlineText(o: Outline): string {
  const row = (e: Entry, prefix = '') => ({
    page: e.full.slice(prefix.length),
    ...(e.type ? { type: e.type } : {}),
    rank: e.rank,
    learns: e.learns,
    ...(e.sources ? { sources: e.sources } : {}),
  });
  return (
    '# Generated by doccheck gen from page front matter.\n' +
    yamlText({
      ...(o.repoName ? { repoName: o.repoName } : {}),
      contexts: o.contexts.map(c => ({
        context: c.context,
        ...(c.learns ? { learns: c.learns } : {}),
        pages: c.pages.map(e => row(e, `domain/${c.context}/`)),
      })),
      guides: o.guides.map(e => row(e, 'guides/')),
      repo: o.repo.map(e => row(e)),
      readingPath: o.readingPath,
    })
  );
}
export function checkOutline(docs: string, pages: Page[], o: Outline, issues: Issue[]) {
  const by = new Map(pages.map(p => [canonical(p.rel), p]));
  const entries = entriesOf(o);
  const keys = new Set(entries.map(e => canonical(e.full)));
  for (const e of entries) {
    const p = by.get(canonical(e.full));
    if (!p)
      issue(issues, join(docs, 'outline.yaml'), 'outline in step', `${e.full} has no page; write it or make a stub`);
    else if (e.type && p.fm.type !== e.type)
      issue(issues, p.file, 'outline in step', `type ${p.fm.type} differs from outline type ${e.type}`);
  }
  for (const p of pages.filter(planned))
    if (!keys.has(canonical(p.rel))) issue(issues, p.file, 'outline in step', 'page is missing from the outline');
}
export function splitOutline(docs: string, pages: Page[]): number {
  const o = handwritten(docs);
  const by = new Map(pages.map(p => [canonical(p.rel), p]));
  const writes: [string, string][] = [];
  const counts = new Map<string, number>();
  for (const e of entriesOf(o)) {
    const p = by.get(canonical(e.full));
    if (!p) throw new Error(`outline split: ${e.full} has no page; run outline stubs first`);
    if (p.generated) continue;
    const dir = dirname(p.rel);
    const n = (counts.get(dir) ?? 0) + 1;
    counts.set(dir, n);
    const read = o.readingPath.findIndex(x => canonical(x) === canonical(e.full));
    const fm = { ...p.fm };
    delete fm.readingPath;
    const text = writeFrontMatter(
      { ...p, fm },
      {
        rank: e.rank,
        order: n,
        learns: e.learns,
        ...(e.sources ? { sources: e.sources } : {}),
        ...(read >= 0 ? { readingPath: read + 1 } : {}),
      },
    );
    writes.push([p.file, text]);
  }
  // Validate every input before changing any page.
  for (const [file, text] of writes) writeFileSync(file, text);
  const f = join(docs, 'doccheck.yaml');
  const now = existsSync(f) ? readYaml(f) : {};
  writeFileSync(f, yamlText({ ...now, pageOrder: 'A' }));
  return writes.length;
}
export function makeStubs(docs: string, o: Outline, c: Config): number {
  let n = 0;
  const counts = new Map<string, number>();
  for (const e of entriesOf(o)) {
    if (e.full.match(/^domain\/[^/]+\/index\.md$/)) continue;
    const f = join(docs, e.full);
    const dir = dirname(e.full);
    const order = (counts.get(dir) ?? 0) + 1;
    counts.set(dir, order);
    if ([f, f.replace(/\.md$/, '.dh.md'), f.replace(/\.dh\.md$/, '.md')].some(existsSync)) continue;
    // A stub contains no rich parts, so it starts as ordinary Markdown.
    const target = f.replace(/\.dh\.md$/, '.md');
    const concept = basename(target, '.md').replace(/-/g, ' ');
    const area = e.full.match(/^domain\/([^/]+)\//)?.[1];
    let namespaces: string[] = [];
    if (area && existsSync(join(docs, 'domain', area, 'UL.yaml'))) {
      const ns = readYaml(join(docs, 'domain', area, 'UL.yaml'))?.namespace;
      if (typeof ns === 'string') namespaces = [ns];
    }
    const fm = {
      title: concept,
      concept,
      description: e.learns,
      namespaces,
      type: e.type,
      stub: true,
      ...(c.pageOrder === 'A' ? { rank: e.rank, order, learns: e.learns } : {}),
    };
    mkdirSync(dirname(target), { recursive: true });
    writeFileSync(target, `---\n${yamlText(fm)}---\n\n${e.learns}\n`);
    n++;
  }
  return n;
}
