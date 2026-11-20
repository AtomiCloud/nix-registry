import { existsSync, readFileSync, mkdtempSync, writeFileSync, rmSync } from 'node:fs';
import { dirname, join, relative, basename } from 'node:path';
import { tmpdir } from 'node:os';
import { spawnSync } from 'node:child_process';
import {
  type Page,
  type Issue,
  type Outline,
  git,
  repoName,
  inside,
  walk,
  issue,
  blocksOf,
  markdownBlocks,
  allInline,
  inlineText,
} from './model';
import { parse, type CodeWalkBlock } from 'durian-hypermedia';
import { hydrateEditors } from './dh-code';

export const CODE_FORM = /^[A-Za-z0-9_.-]+\/(?!\/)(?!.*(?:^|\/)\.\.(?:\/|$))[^\s#]+(?:#[\p{L}\p{N}_.$:-]+)?$/u;
const esc = (s: string) => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
export function referenceParts(ref: string) {
  const [path, name] = ref.replace(/^code:/, '').split('#');
  const i = path.indexOf('/');
  return { repo: path.slice(0, i), file: path.slice(i + 1), name };
}
export function defined(text: string, name: string, file = 'source.ts') {
  const anchors = [...commentsOf(text, file).matchAll(new RegExp(`doc-anchor:\\s*${esc(name)}(?=\\s|$|[*/])`, 'g'))];
  if (anchors.length > 1) throw new Error(`doc-anchor ${name} is used twice in ${file}`);
  if (anchors.length === 1) return true;
  const tmp = mkdtempSync(join(tmpdir(), 'doccheck-symbols-'));
  try {
    const path = join(tmp, basename(file));
    writeFileSync(path, text);
    const result = spawnSync('ctags', ['--options=NONE', '--output-format=json', '--fields=+s', '-f', '-', path], {
      encoding: 'utf8',
    });
    if (result.status !== 0)
      throw new Error(`universal-ctags failed: ${result.error?.message ?? result.stderr.trim()}`);
    const tags = result.stdout
      .split('\n')
      .filter(Boolean)
      .map(s => JSON.parse(s));
    const requested = name.replace(/::/g, '.');
    return tags.some(t => {
      const qualified = `${t.scope ?? ''}.${t.name}`.replace(/::/g, '.');
      return t.name === name || qualified === requested || qualified.endsWith(`.${requested}`);
    });
  } finally {
    rmSync(tmp, { recursive: true, force: true });
  }
}
export function checkCode(
  docs: string,
  pages: Page[],
  o: Outline,
  issues: Issue[],
  sourceRoot?: string,
  readGit = git,
) {
  const root = dirname(docs),
    self = repoName(docs, o);
  const stagedPaths = new Set((readGit(root, ['diff', '--cached', '--name-only', '--', 'docs']) ?? '').split('\n'));
  const cache = new Map<string, string | null>();
  const read = (file: string, pin?: string): string | null => {
    const k = `${pin ?? 'work'}:${file}`;
    if (cache.has(k)) return cache.get(k)!;
    const path = join(root, file);
    let text: string | null = null;
    if (inside(root, path))
      text = pin
        ? readGit(root, ['show', `${pin}:${file}`])
        : (readGit(root, ['show', `:${file}`]) ?? (existsSync(path) ? readFileSync(path, 'utf8') : null));
    cache.set(k, text);
    return text;
  };
  let sourceFiles: string[] | undefined;
  const movedHint = (file: string, name?: string): string => {
    if (!name) return '';
    if (!sourceFiles) {
      const tracked = readGit(root, ['ls-files', '-co', '--exclude-standard', '-z']);
      sourceFiles = (
        tracked
          ? tracked
              .split('\0')
              .filter(Boolean)
              .map(path => join(root, path))
          : walk(root)
      )
        .filter(path => codeFile(path) && !inside(docs, path) && !inside(join(root, 'docs-llm'), path))
        .map(path => relative(root, path));
    }
    const found: string[] = [];
    for (const candidate of sourceFiles) {
      if (candidate === file) continue;
      const text = read(candidate);
      if (text == null) continue;
      try {
        if (defined(text, name, candidate)) found.push(candidate);
      } catch {
        /* A separate malformed definition is not a valid destination. */
      }
    }
    return found.length ? `; ${name} is defined in ${found.join(', ')}` : '';
  };
  const reference = (p: Page, ref: string) => {
    ref = ref.replace(/^code:/, '');
    if (!CODE_FORM.test(ref)) {
      issue(issues, p.file, 'code references', `malformed code reference ${ref}`);
      return;
    }
    const r = referenceParts(ref);
    const pin = p.fm.pins?.[r.repo];
    if (r.repo !== self && !pin) {
      issue(issues, p.file, 'code references', `${ref}: another repo requires pins.${r.repo}`);
      return;
    }
    if (r.repo !== self && !sourceRoot) return;
    const text = r.repo === self ? read(r.file) : readGit(join(sourceRoot!, r.repo), ['show', `${pin}:${r.file}`]);
    if (text == null)
      issue(
        issues,
        p.file,
        'code references',
        `${ref}: file does not exist in this repo${r.repo === self ? movedHint(r.file, r.name) : ''}`,
      );
    else if (r.name) {
      try {
        if (!defined(text, r.name, r.file))
          issue(
            issues,
            p.file,
            'code references',
            `${ref}: symbol or doc-anchor is not defined${r.repo === self ? movedHint(r.file, r.name) : ''}; use a code anchor for a language universal-ctags does not parse`,
          );
      } catch (e) {
        issue(issues, p.file, 'code references', (e as Error).message);
      }
    }
  };
  for (const p of pages.filter(p => !p.generated)) {
    for (const ref of Array.isArray(p.fm.code) ? p.fm.code : []) if (typeof ref === 'string') reference(p, ref);
    const xs = allInline(markdownBlocks(p.doc));
    for (const x of xs) if (x.t === 'coderef') reference(p, x.ref);
    const prose = xs.map(x => (x.t === 'code' || x.t === 'text' ? x.v : '')).join('\n');
    for (const m of prose.matchAll(/(?:\b[\w./-]+\.[a-zA-Z0-9]+:\d+(?:-\d+)?|#L\d+(?:-L?\d+)?)/g))
      issue(issues, p.file, 'line numbers in prose', `${m[0]}: use a code reference by symbol or anchor`);
    for (const x of xs)
      if (x.t === 'link' && x.href && /#L\d+/.test(x.href))
        issue(issues, p.file, 'line numbers in prose', `${x.href}: line-number links belong only in pinned code walks`);
    for (const ref of Array.isArray(p.fm.code) ? p.fm.code : []) {
      if (typeof ref !== 'string' || !CODE_FORM.test(ref)) continue;
      const r = referenceParts(ref);
      if (r.repo !== self) continue;
      const text = read(r.file);
      if (text != null && codeFile(r.file) && !commentsOf(text, r.file).includes(`${self}/docs/${p.rel}`))
        issue(issues, p.file, 'code links back', `${r.file} needs a comment pointing back to ${self}/docs/${p.rel}`);
    }
    checkWalks(p);
    for (const q of hydrateEditors(p.doc, { repoRoot: root, repoName: self, codeRoot: sourceRoot }))
      issue(issues, p.file, 'part data', q.message, q.level, q.line);
  }
  function checkWalks(p: Page) {
    const walks = blocksOf(p.doc.blocks).filter((b): b is CodeWalkBlock => b.type === 'codewalk');
    const staged = stagedPaths.has(`docs/${p.rel}`);
    if (!walks.length && !staged) return;
    const old = staged ? readGit(root, ['show', `HEAD:docs/${p.rel}`]) : null;
    const oldDoc = old ? parse(old, { path: p.file }) : null;
    const oldWalks = oldDoc ? blocksOf(oldDoc.blocks).filter((b): b is CodeWalkBlock => b.type === 'codewalk') : [];
    const stagedText = staged ? readGit(root, ['show', `:docs/${p.rel}`]) : null;
    const stagedDoc = stagedText ? parse(stagedText, { path: p.file }) : null;
    const stagedWalks = stagedDoc
      ? blocksOf(stagedDoc.blocks).filter((b): b is CodeWalkBlock => b.type === 'codewalk')
      : [];
    // Immutable pin comparisons use the two committed inputs, independent of unstaged edits.
    stagedWalks.forEach((b, wi) =>
      b.steps.forEach((s, si) => {
        if (s.repo !== self || !s.lines) return;
        const pin = (stagedDoc!.frontMatter.pins as any)?.[s.repo];
        const was = (oldDoc?.frontMatter.pins as any)?.[s.repo];
        const prev = oldWalks[wi]?.steps[si];
        if (
          !pin ||
          !was ||
          pin === was ||
          !prev ||
          JSON.stringify(prev.lines) !== JSON.stringify(s.lines) ||
          inlineText(prev.note) !== inlineText(s.note)
        )
          return;
        const range = (text: string | null) =>
          text
            ?.replace(/\n$/, '')
            .split('\n')
            .slice(s.lines![0] - 1, s.lines![1])
            .join('\n');
        if (range(read(prev.file, was)) !== range(read(s.file, pin)))
          issue(
            issues,
            p.file,
            'code walk ranges',
            `${s.file}: staged pin moved and cited lines changed without updating range or note`,
            'error',
            b.line,
          );
      }),
    );
    walks.forEach((b, wi) =>
      b.steps.forEach((s, si) => {
        const pin = p.fm.pins?.[s.repo];
        const foreign = s.repo && s.repo !== self;
        if (foreign) {
          if (!pin) issue(issues, p.file, 'code walk ranges', `repo ${s.repo} requires a pins entry`, 'error', b.line);
          if (!pin || !sourceRoot) return;
        }
        // A made-up @file already has code and range validation from the pinned parser.
        if (s.code != null && !s.repo) return;
        if (!s.lines) return;
        let text = read(s.file, pin);
        if (foreign && sourceRoot) text = readGit(join(sourceRoot, s.repo), ['show', `${pin}:${s.file}`]);
        if (text == null) {
          issue(
            issues,
            p.file,
            'code walk ranges',
            `${s.file} or pin ${pin ?? 'working tree'} does not exist`,
            'error',
            b.line,
          );
          return;
        }
        const lines = text.replace(/\n$/, '').split('\n');
        if (s.lines[0] < 1 || s.lines[1] < s.lines[0] || s.lines[1] > lines.length)
          issue(
            issues,
            p.file,
            'code walk ranges',
            `${s.file}: range ${s.lines.join('-')} exceeds ${lines.length} lines`,
            'error',
            b.line,
          );
        const cited = lines.slice(s.lines[0] - 1, s.lines[1]).join('\n');
        if (pin && !foreign) {
          const current = read(s.file)
            ?.replace(/\n$/, '')
            .split('\n')
            .slice(s.lines[0] - 1, s.lines[1])
            .join('\n');
          if (current !== cited)
            issue(
              issues,
              p.file,
              'code walk pin behind',
              `${s.file} cited lines differ from pin ${pin}`,
              'warn',
              b.line,
            );
        }
      }),
    );
  }
  const tracked = readGit(root, ['ls-files', '-co', '--exclude-standard', '-z']);
  const files =
    tracked
      ?.split('\0')
      .filter(Boolean)
      .map(f => join(root, f)) ?? walk(root);
  const docPattern = new RegExp(`${esc(self)}/(docs/[\\w./-]+(?:\\.dh)?\\.md)(?=$|[\\s)\\]}>:;,'\"])`, 'g');
  for (const file of files) {
    if (
      !inside(root, file) ||
      inside(docs, file) ||
      inside(join(root, 'docs-llm'), file) ||
      !existsSync(file) ||
      !codeFile(file)
    )
      continue;
    const text = readFileSync(file, 'utf8');
    for (const m of commentsOf(text, file).matchAll(docPattern))
      if (!inside(docs, join(root, m[1])) || !existsSync(join(root, m[1])))
        issue(issues, file, 'code links resolve', `${self}/${m[1]} points to a missing doc`);
  }
}
export const codeFile = (file: string) =>
  /\.(?:[cm]?[jt]sx?|rs|go|py|rb|cs|java|kt|swift|c|h|cpp|hpp|sh|bash|zsh|nix|ya?ml|toml|tf|ex|exs|php|scala|sql|lua)$/.test(
    file,
  );
// A small lexical scan keeps string literals out of code comments. It does not interpret code.
export function commentsOf(text: string, file: string): string {
  const hash = /\.(py|rb|sh|bash|zsh|nix|ya?ml|toml|tf|ex|exs)$/.test(file);
  const sql = file.endsWith('.sql') || file.endsWith('.lua');
  let out = '',
    i = 0;
  while (i < text.length) {
    // Rust lifetimes ('a, 'static) are identifiers. Only a closed character literal starts a string.
    const rustLifetime = file.endsWith('.rs') && text[i] === "'" && !/^'(?:\\.|[^'\\\n])'/.test(text.slice(i));
    if (!rustLifetime && (text[i] === '"' || text[i] === "'" || text[i] === '`')) {
      const q = text[i++];
      while (i < text.length) {
        if (text[i] === '\\') {
          i += 2;
          continue;
        }
        if (text[i++] === q) break;
      }
      continue;
    }
    if (text.startsWith('/*', i)) {
      const end = text.indexOf('*/', i + 2);
      out += text.slice(i, end < 0 ? text.length : end + 2) + '\n';
      i = end < 0 ? text.length : end + 2;
      continue;
    }
    if (text.startsWith('//', i) || (hash && text[i] === '#') || (sql && text.startsWith('--', i))) {
      const end = text.indexOf('\n', i);
      out += text.slice(i, end < 0 ? text.length : end) + '\n';
      i = end < 0 ? text.length : end + 1;
      continue;
    }
    i++;
  }
  return out;
}
