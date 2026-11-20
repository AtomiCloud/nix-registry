import { existsSync, readdirSync, readFileSync } from 'node:fs';
import { basename, dirname, join, relative, resolve, sep } from 'node:path';
import { spawnSync } from 'node:child_process';
import { parseDocument, stringify } from 'yaml';
import { parse, type Document, type Block, type MdBlock, type MdInline } from 'durian-hypermedia';
import { parseFile } from './dh-library';

export type Issue = { file: string; line: number; check: string; message: string; level: 'error' | 'warn' };
export type Config = { pageOrder: 'A' | 'B'; innerFolder: string; llmView: 'committed' | 'build' };
export type Page = {
  file: string;
  rel: string;
  doc: Document;
  fm: Record<string, any>;
  body: string;
  generated: boolean;
  adr: boolean;
  translated: boolean;
};
export type Entry = { full: string; type?: string; rank: string; learns: string; sources?: string[]; order?: number };
export type Context = { context: string; learns?: string; pages: Entry[] };
export type Outline = { repoName?: string; contexts: Context[]; guides: Entry[]; repo: Entry[]; readingPath: string[] };
export const DEFAULTS: Config = { pageOrder: 'A', innerFolder: 'model', llmView: 'committed' };
export const ranks = ['core', 'supporting', 'edge'];
export const slug = (s: string) =>
  s
    .toLowerCase()
    .replace(/\./g, '-')
    .replace(/[^\p{L}\p{N} -]/gu, '')
    .trim()
    .replace(/ +/g, '-');
export const canonical = (s: string) => s.replace(/\.dh\.md$/, '.md');
export const inside = (root: string, path: string) => {
  const r = relative(resolve(root), resolve(path));
  return r === '' || (!r.startsWith(`..${sep}`) && r !== '..' && !r.startsWith(sep));
};
export const issue = (
  issues: Issue[],
  file: string,
  check: string,
  message: string,
  level: Issue['level'] = 'error',
  line = 1,
) => issues.push({ file, line, check, message, level });
export function yaml(text: string): any {
  const d = parseDocument(text, { uniqueKeys: true, strict: true });
  if (d.errors.length) throw new Error(d.errors.map(e => e.message).join('; '));
  return d.toJS({ maxAliasCount: 16 });
}
export const yamlText = (v: unknown) => stringify(v, { lineWidth: 0 });
export function readYaml(file: string) {
  return yaml(readFileSync(file, 'utf8'));
}
export function configFor(docs: string, override: Partial<Config> = {}): Config {
  const f = join(docs, 'doccheck.yaml');
  const v = existsSync(f) ? readYaml(f) : {};
  if (!v || typeof v !== 'object' || Array.isArray(v)) throw new Error('doccheck.yaml must be a mapping');
  for (const k of Object.keys(v)) if (!(k in DEFAULTS)) throw new Error(`unknown doccheck.yaml key ${k}`);
  const c = { ...DEFAULTS, ...v, ...override };
  if (!['A', 'B'].includes(c.pageOrder)) throw new Error('pageOrder must be A or B');
  if (!['committed', 'build'].includes(c.llmView)) throw new Error('llmView must be committed or build');
  if (!/^[a-z][a-z0-9-]*$/.test(c.innerFolder) || ['architecture', 'implementation', 'adr'].includes(c.innerFolder))
    throw new Error('innerFolder must be a distinct folder name');
  return c;
}
export function walk(dir: string): string[] {
  if (!existsSync(dir)) return [];
  return readdirSync(dir, { withFileTypes: true })
    .sort((a, b) => a.name.localeCompare(b.name))
    .flatMap(e =>
      e.isSymbolicLink() || ['.git', 'node_modules', '.direnv', 'vendor'].includes(e.name)
        ? []
        : e.isDirectory()
          ? walk(join(dir, e.name))
          : [join(dir, e.name)],
    );
}
export function bodyOf(text: string) {
  return text.replace(/^---\r?\n[\s\S]*?\r?\n---(?:\r?\n|$)/, '');
}
export function loadPage(file: string, docs: string): Page {
  const source = readFileSync(file, 'utf8');
  const doc = parseFile(file);
  const rel = relative(docs, file).split(sep).join('/');
  const generated =
    basename(file) === 'README.md' || basename(file) === 'UL.md' || /^domain\/[^/]+\/index\.md$/.test(rel);
  return {
    file,
    rel,
    doc,
    fm: doc.frontMatter,
    body: bodyOf(source),
    generated,
    adr: rel.split('/').includes('adr'),
    translated: rel.startsWith('i18n/'),
  };
}
export function loadPages(docs: string) {
  return walk(docs)
    .filter(f => f.endsWith('.md') && !['README.md', 'UL.md'].includes(basename(f)))
    .map(f => loadPage(f, docs));
}
export const entriesOf = (o: Outline) => [...o.contexts.flatMap(c => c.pages), ...o.guides, ...o.repo];
export const planned = (p: Page) => !p.generated && !p.adr && !p.translated && !p.rel.startsWith('standards/');
export function git(root: string, args: string[]): string | null {
  const r = spawnSync('git', ['-C', root, ...args], { encoding: 'utf8', maxBuffer: 32 << 20 });
  return r.status === 0 ? r.stdout : null;
}
export function repoName(docs: string, o?: Outline) {
  return (
    git(dirname(docs), ['remote', 'get-url', 'origin'])
      ?.trim()
      .split(/[/:]/)
      .pop()
      ?.replace(/\.git$/, '') ||
    o?.repoName ||
    basename(dirname(docs))
  );
}
export function inlineText(xs: MdInline[], codes = false): string {
  return xs
    .map(x =>
      'c' in x
        ? inlineText(x.c, codes)
        : x.t === 'code'
          ? codes
            ? x.v
            : ''
          : 'v' in x
            ? x.v
            : x.t === 'kbd'
              ? x.label
              : x.t === 'mouse'
                ? x.target
                : x.t === 'img'
                  ? x.alt
                  : x.t === 'ref'
                    ? x.text
                    : '\n',
    )
    .join('');
}
export function mdText(bs: MdBlock[], codes = false): string {
  return bs
    .map(b =>
      b.t === 'code'
        ? codes
          ? b.text
          : ''
        : b.t === 'raw'
          ? b.text
          : b.t === 'table'
            ? [b.head, ...b.rows].map(row => row.map(c => inlineText(c, codes)).join(' ')).join('\n')
            : b.t === 'list'
              ? b.items.map(i => mdText(i.c, codes)).join('\n')
              : 'c' in b
                ? Array.isArray(b.c) && b.c[0] && 't' in b.c[0] && ['quote', 'callout'].includes(b.t)
                  ? mdText(b.c as MdBlock[], codes)
                  : inlineText(b.c as MdInline[], codes)
                : '',
    )
    .join('\n');
}
export function blocksOf(bs: Block[]): Block[] {
  return bs.flatMap(b => [
    b,
    ...(b.type === 'human'
      ? blocksOf(b.body)
      : b.type === 'tabs' || b.type === 'accordion'
        ? b.items.flatMap(i => blocksOf(i.body))
        : []),
  ]);
}
export function markdownBlocks(doc: Document): MdBlock[] {
  return blocksOf(doc.blocks).flatMap(b => (b.type === 'markdown' ? b.md : []));
}
export function allInline(bs: MdBlock[]): MdInline[] {
  const inline = (xs: MdInline[]): MdInline[] => xs.flatMap(x => [x, ...('c' in x ? inline(x.c) : [])]);
  return bs.flatMap(b =>
    b.t === 'list'
      ? b.items.flatMap(i => allInline(i.c))
      : b.t === 'quote' || b.t === 'callout'
        ? allInline(b.c)
        : b.t === 'table'
          ? [b.head, ...b.rows].flatMap(r => r.flatMap(inline))
          : 'c' in b
            ? inline(b.c as MdInline[])
            : [],
  );
}
export function writeFrontMatter(p: Page, extra: Record<string, unknown>) {
  // Existing selected fields are emitted verbatim by dh, so preserve their bytes during migration.
  const match = p.doc.source.match(/^(---\r?\n)([\s\S]*?)(\r?\n---(?:\r?\n|$))/);
  if (!match) throw new Error(`${p.rel}: cannot migrate missing front matter`);
  const raw = match[2];
  const collisions = Object.keys(extra).filter(k => k in p.fm);
  if (collisions.some(k => JSON.stringify(extra[k]) !== JSON.stringify(p.fm[k])))
    throw new Error(`${p.rel}: ordering fields already exist with different values: ${collisions.join(', ')}`);
  const fresh = Object.fromEntries(Object.entries(extra).filter(([k]) => !(k in p.fm)));
  return `${match[1]}${raw}${Object.keys(fresh).length ? '\n' + yamlText(fresh).trimEnd() : ''}${match[3]}${p.body}`;
}
