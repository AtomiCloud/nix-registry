import { existsSync, readFileSync, readdirSync } from 'node:fs';
import { dirname, join, resolve, relative, basename } from 'node:path';
import { spawnSync } from 'node:child_process';
import {
  check as checkFormat,
  parse,
  animationTexts,
  type MdBlock,
  type Block,
  type MdInline,
} from 'durian-hypermedia';
import { Lexer } from 'marked';
import {
  type Page,
  type Config,
  type Issue,
  type Outline,
  issue,
  yaml,
  readYaml,
  slug,
  walk,
  git,
  markdownBlocks,
  mdText,
  inlineText,
  allInline,
  blocksOf,
  inside,
  planned,
  canonical,
} from './model';
import { CODE_FORM, checkCode } from './code';
import { expectedLlm, sectionsOf, sectionHashes } from './dh-library';

export type UlWord = {
  word: string;
  aliases?: string[];
  desc?: string;
  details?: string;
  status?: string;
  file?: string;
  line?: number;
};
export type UlData = { schema: string; root?: string; words: UlWord[]; namespaces?: any[] };
export function loadUl(root: string): UlData {
  const r = spawnSync('ul', ['all', '--root', root, '--format', 'json'], { encoding: 'utf8', maxBuffer: 32 << 20 });
  if (r.status !== 0) throw new Error(`ul all failed: ${r.error?.message ?? r.stderr.trim()}`);
  const v = JSON.parse(r.stdout);
  if (v.schema !== 'ul/v1' || !Array.isArray(v.words)) throw new Error('ul all did not return ul/v1 words');
  return v;
}
const fields = [
  'title',
  'concept',
  'description',
  'namespaces',
  'type',
  'related',
  'adrs',
  'issues',
  'stub',
  'pins',
  'translation',
  'code',
  'upstream',
  'exit',
  'date',
  'covers',
  'supersedes',
  'banner',
  'rank',
  'order',
  'learns',
  'sources',
  'readingPath',
];
const problem: Record<string, string> = {
  feature: 'What problem it solves',
  prereq: 'Why you need it',
  flow: 'What problem it solves',
  design: 'What problem it solves',
  algorithm: 'What problem it solves',
  guard: 'What must hold',
  workaround: 'What it works around',
};
const step: Record<string, string> = {
  feature: 'How it works',
  prereq: 'How it works',
  format: 'Fields',
  surface: 'How a call goes',
  flow: 'Steps',
  design: 'How it works',
  algorithm: 'How it works',
  guard: 'How it works',
  workaround: 'How it works',
  'how-to': 'Steps',
};
const mapping = (x: any) => x && typeof x === 'object' && !Array.isArray(x);
const strings = (x: any) => Array.isArray(x) && x.every((s: any) => typeof s === 'string');
const line = (x: any, max = Infinity) => typeof x === 'string' && !!x.trim() && !/[\r\n]/.test(x) && x.length <= max;
export function checkFrontMatter(p: Page, c: Config, issues: Issue[]) {
  const err = (m: string) => issue(issues, p.file, 'front matter valid', m);
  const f = p.fm;
  const raw = p.doc.source.match(/^---\r?\n([\s\S]*?)\r?\n---(?:\r?\n|$)/);
  if (!raw) {
    err('every page needs front matter');
    return;
  }
  try {
    if (!mapping(yaml(raw[1]))) err('front matter must be a mapping');
  } catch (e) {
    err((e as Error).message);
    return;
  }
  for (const k of Object.keys(f)) if (!fields.includes(k)) err(`unknown field ${k}`);
  for (const k of ['title', 'description', ...(p.adr ? [] : ['concept'])])
    if (!line(f[k], k === 'description' ? 160 : Infinity))
      err(`${k} must be a nonempty single line${k === 'description' ? ' of at most 160 characters' : ''}`);
  if (!strings(f.namespaces)) err('namespaces must be an array of strings');
  const folder = p.rel.split('/').at(-2) ?? '';
  const types: Record<string, string[]> = {
    [c.innerFolder]: ['feature', 'prereq', 'format'],
    architecture: ['surface', 'flow', 'design'],
    implementation: ['algorithm', 'guard', 'workaround'],
    guides: ['how-to', 'tutorial'],
  };
  if (!p.adr && !p.rel.startsWith('standards/')) {
    if (!line(f.type) || !Object.values(types).flat().includes(f.type)) err('type is missing or unknown');
    else if (types[folder] && !types[folder].includes(f.type)) err(`type ${f.type} does not belong in ${folder}/`);
  }
  for (const k of ['related', 'adrs', 'issues', 'code', 'supersedes', 'covers', 'sources'])
    if (f[k] != null && !strings(f[k])) err(`${k} must be an array of strings`);
  if (f.stub != null && typeof f.stub !== 'boolean') err('stub must be true or false');
  if (folder === 'implementation' && !f.stub && (!Array.isArray(f.code) || !f.code.length))
    err('an implementation page requires code entries');
  for (const v of Array.isArray(f.code) ? f.code : [])
    if (typeof v !== 'string' || !CODE_FORM.test(v) || /#L\d+|:\d+(?:-|$)/.test(v))
      err(`code entry ${v} needs <repo>/<path>#<symbol-or-anchor>, without line numbers`);
  for (const v of Array.isArray(f.issues) ? f.issues : [])
    if (
      typeof v !== 'string' ||
      !(v === 'not filed' || /^https?:\/\/[^\s]+\/(?:issues|issue|tickets|browse)\/[^/#?]+(?:[?#].*)?$/.test(v))
    )
      err(`issues entry ${v} must name one issue URL or not filed`);
  if (
    f.pins != null &&
    (!mapping(f.pins) ||
      Object.entries(f.pins).some(
        ([r, pin]) => !/^[\w.-]+$/.test(r) || typeof pin !== 'string' || !/^[a-f0-9]{40}$/i.test(pin),
      ))
  )
    err('pins must map repo names to full 40-character commit ids');
  if (f.type === 'workaround') {
    if (!line(f.exit)) err('workaround requires a one-line exit condition');
    if (f.upstream != null && !/^https?:\/\/\S+$/.test(f.upstream)) err('upstream must be an HTTP URL');
  }
  if (p.translated) {
    const t = f.translation;
    if (
      !mapping(t) ||
      !line(t.lang) ||
      !line(t.source) ||
      !strings(t.sourceHashes) ||
      t.sourceHashes.some((s: string) => !/^\w{10}$/.test(s))
    )
      err('translation requires lang, source and sourceHashes');
    else if (t.lang !== p.rel.split('/')[1]) err('translation.lang must match the i18n language folder');
    if (mapping(t) && Object.keys(t).some(k => !['lang', 'source', 'sourceHashes'].includes(k)))
      err('unknown translation field');
  } else if (f.translation != null) err('translation belongs only under docs/i18n/');
  if (p.adr) {
    if (!/^\d{4}-[a-z0-9]+(?:-[a-z0-9]+)*\.md$/.test(basename(p.file)) || p.file.endsWith('.dh.md'))
      issue(issues, p.file, 'ADR shape', 'ADR name must be NNNN-kebab-slug.md');
    if (
      typeof f.date !== 'string' ||
      !/^\d{4}-\d{2}-\d{2}$/.test(f.date) ||
      !Number.isFinite(new Date(f.date).getTime()) ||
      new Date(f.date).toISOString().slice(0, 10) !== f.date
    )
      issue(issues, p.file, 'ADR shape', 'date must be a real YYYY-MM-DD date');
    const hs = p.doc.headings.filter(h => h.depth === 2).map(h => h.text);
    const req = ['Context', 'Options', 'Decision', 'Consequences'];
    if (req.some((h, i) => hs.indexOf(h) < 0 || (i > 0 && hs.indexOf(h) < hs.indexOf(req[i - 1]))))
      issue(issues, p.file, 'ADR shape', 'required headings in order: Context, Options, Decision, Consequences');
  }
  for (const k of ['related', 'adrs', 'supersedes'])
    for (const path of Array.isArray(f[k]) ? f[k] : []) {
      if (typeof path !== 'string') continue;
      const [part, anchor] = path.split('#');
      const dest = resolve(dirname(p.file), part || '.');
      if (!existsSync(dest)) issue(issues, p.file, 'front matter paths resolve', `${k}: ${path} does not resolve`);
      else if (anchor && dest.endsWith('.md')) {
        const d = parse(readFileSync(dest, 'utf8'), { path: dest });
        if (!d.headings.some(h => h.id.replace(/^dh-/, '') === anchor))
          issue(issues, p.file, 'front matter paths resolve', `${path}: heading does not exist`);
      }
      if (['adrs', 'supersedes'].includes(k) && !dest.split(/[\\/]/).includes('adr'))
        issue(issues, p.file, 'front matter paths resolve', `${k} must point at an ADR`);
    }
}
export function checkShape(p: Page, c: Config, issues: Issue[]) {
  if (p.translated) return;
  const f = p.fm;
  const hs = p.doc.headings.filter(h => h.depth === 2).map(h => h.text);
  const err = (m: string) => issue(issues, p.file, 'doc shape', m);
  if (!p.adr && !p.translated && line(f.concept)) {
    const name =
      p.rel.startsWith('standards/') && basename(p.file) === 'index.md'
        ? basename(dirname(p.file))
        : basename(p.file).replace(/\.dh\.md$|\.md$/, '');
    if (slug(f.concept) !== name || String(f.title).toLowerCase() !== f.concept.toLowerCase())
      issue(issues, p.file, 'page named after its concept', `file and title must match concept ${f.concept}`);
  }
  if (p.adr) return;
  const summary: string[] = [];
  let ended = false;
  for (const b of p.doc.blocks)
    if (b.type === 'markdown' && !ended)
      for (const t of b.md) {
        if (t.t === 'h' && t.depth === 2) {
          ended = true;
          break;
        }
        if (t.t !== 'h') summary.push(mdText([t], true));
      }
  const n = summary
    .join('\n')
    .split('\n')
    .filter(s => s.trim()).length;
  if (n < 1 || n > 3) err(`summary must be 1..3 nonempty lines, found ${n}`);
  const proseLines = proseLineCount(p.doc.blocks);
  if (proseLines > 300) err(`page has ${proseLines} prose lines; limit 300`);
  if (f.stub) {
    if (hs.length) err('a stub has no ## section');
    return;
  }
  if (hs.some(h => /^(Pages|See also|Related pages)(?:\b|$)/i.test(h)))
    issue(issues, p.file, 'page list in a page', 'put reading links in related; page lists are generated');
  const prob = problem[f.type],
    walk = step[f.type];
  if (prob && !hs.includes(prob)) err(`missing ## ${prob}`);
  if (walk && !hs.includes(walk)) err(`missing ## ${walk}`);
  if (f.type === 'tutorial' && !hs.some(h => /^Step \d+: .+/.test(h)))
    err('tutorial requires ## Step <n>: <what happens>');
  const order = (h: string) =>
    h === prob
      ? 0
      : h === walk || /^Step \d+: /.test(h)
        ? 1
        : h.startsWith('Case:')
          ? 3
          : h.startsWith('Edge case:')
            ? 4
            : h.startsWith('Known bug:')
              ? 5
              : h === 'Why this way'
                ? 6
                : h === 'Exit'
                  ? 7
                  : h === 'Questions for the designer'
                    ? 8
                    : 2;
  let last = -1;
  for (const h of hs) {
    const k = order(h);
    if (k < last) err(`## ${h} comes out of order`);
    last = Math.max(last, k);
  }
  const bugs = hs.filter(h => h.startsWith('Known bug:')).length;
  if (bugs !== (Array.isArray(f.issues) ? f.issues.length : 0))
    err('one Known bug section is required per issues entry');
  if (f.type === 'workaround' && !hs.includes('Exit')) err('workaround requires ## Exit');
  const why = sectionsOf(p.body).find(s => /^## Why this way\s*\n/.test(s));
  if (why) {
    const d = parse(why, { path: 'why.md' });
    const xs = allInline(markdownBlocks(d));
    const source =
      xs.some(x => x.t === 'coderef' || (x.t === 'link' && x.href)) ||
      /\bSource:/.test(mdText(markdownBlocks(d), true));
    if (!source) err('Why this way must cite a source or link an ADR');
  }
  if (/(?:^|\n)\s*Inferred:/.test(mdText(markdownBlocks(p.doc))))
    err('Inferred reasons need a source or a question for the designer');
}
export function proseLineCount(bs: Block[]): number {
  const noFences = (ms: MdBlock[]): MdBlock[] =>
    ms
      .filter(m => m.t !== 'code')
      .map(m =>
        m.t === 'quote' || m.t === 'callout'
          ? { ...m, c: noFences(m.c) }
          : m.t === 'list'
            ? { ...m, items: m.items.map(i => ({ ...i, c: noFences(i.c) })) }
            : m,
      );
  let n = 0;
  for (const b of bs) {
    if (b.type === 'markdown')
      n += mdText(noFences(b.md), true)
        .split('\n')
        .filter(x => x.trim()).length;
    else if (b.type === 'human') n += proseLineCount(b.body);
    else if (b.type === 'tabs' || b.type === 'accordion')
      n += b.items.reduce((a, i) => a + 1 + proseLineCount(i.body), 0);
  }
  return n;
}

export function checkRich(p: Page, issues: Issue[]) {
  // The pinned package owns syntax, part evaluation, LLM pairing and rich-data validation.
  // Its pure host cannot read code; the filesystem checks in code.ts own those messages.
  for (const q of checkFormat(p.doc)) {
    if (/^\[code references\]/.test(q.message) || /^\[line numbers in prose\]/.test(q.message)) continue;
    if (/^\[code walk ranges\].*(?:no pins entry|file .*does not exist|no repo:)/.test(q.message)) continue;
    const m = q.message.match(/^\[([^\]]+)\] (.*)$/s);
    const category =
      m?.[1] ??
      (/:::llm/.test(q.message) && /right after|two|empty/.test(q.message) ? 'LLM text placed' : 'docs parse');
    issue(issues, p.file, category, m?.[2] ?? q.message, q.level, q.line);
  }
  // The format checker checks well-formed callouts; also reject markers outside the first quote line.
  const checkMd = (bs: MdBlock[], inQuote = false) => {
    for (const b of bs) {
      if (b.t === 'code') continue;
      if (b.t === 'quote' || b.t === 'callout') {
        const text = mdText(b.c, true);
        const sec = (text.match(/Security finding:/g) ?? []).length;
        const watch = (text.match(/Watch out:/g) ?? []).length;
        for (const [label, n] of [
          ['Security finding', sec],
          ['Watch out', watch],
        ] as const)
          if (n && !(b.t === 'callout' && (label === 'Security finding' ? b.kind === 'security' : b.kind === 'warn')))
            issue(
              issues,
              p.file,
              label === 'Security finding' ? 'security blocks' : 'watch-out blocks',
              'marker must open the first line of its blockquote',
            );
        checkMd(b.c, true);
      } else if (b.t === 'list') b.items.forEach(i => checkMd(i.c, inQuote));
      else if (!inQuote) {
        const text = mdText([b]);
        if (text.includes('Security finding:'))
          issue(issues, p.file, 'security blocks', 'marker belongs at the start of a blockquote');
        if (text.includes('Watch out:'))
          issue(issues, p.file, 'watch-out blocks', 'marker belongs at the start of a blockquote');
      }
    }
  };
  for (const b of blocksOf(p.doc.blocks)) if (b.type === 'markdown') checkMd(b.md);
}

export const HISTORY = [
  '\\b(?:previously|formerly|used to|no longer|anymore|originally|until recently)\\b',
  '\\bnow (?:uses?|is|are|has|lives|runs|points)\\b|\\b(?:is|are) now(?![.,])\\b',
  '\\b(?:was|were|has been|have been) (?:renamed|replaced|moved|removed|dropped|changed|migrated)\\b|\\b(?:renamed|switched) (?:from|to)\\b|\\bmigrated from\\b',
  '\\bwe (?:first )?tried\\b|\\b(?:first|earlier|old) (?:approach|attempt|version)\\b|\\bdid not work\\b',
  '\\b(?:since|as of|before|after|until) (?:v\\d+(?:\\.\\d+)*|\\d+\\.\\d+(?:\\.\\d+)*)\\b|\\bv\\d+ (?:replaced|replaces|supersedes)\\b',
  '\\b20\\d{2}-\\d{2}-\\d{2}\\b|\\b(?:Jan(?:uary)?|Feb(?:ruary)?|Mar(?:ch)?|Apr(?:il)?|May|Jun(?:e)?|Jul(?:y)?|Aug(?:ust)?|Sep(?:tember)?|Oct(?:ober)?|Nov(?:ember)?|Dec(?:ember)?) \\d{1,2},? 20\\d{2}\\b|\\b(?:epoch|round) \\d+\\b|\\b(?:ADR|DECISION)-\\d+\\b',
  '\\b(?:for now|temporary|temporarily|TODO|FIXME|coming soon|not (?:yet )?written)\\b|\\bwill be (?:added|removed|replaced)\\b',
  "~~[^~]+~~|^#{1,6} (?:History|Changelog|What's new|Migration)\\s*$",
];
const esc = (s: string) => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
export function historyHits(text: string, words: UlWord[], patterns = HISTORY): string[] {
  const candidates = patterns.flatMap(p => [...text.matchAll(new RegExp(p, 'gimu'))]);
  if (!candidates.length) return [];
  const ranges: [number, number][] = [];
  // Only the UL exemption is matched here; avoided, retired and translated words remain ul's job.
  for (const w of words)
    for (const word of [w.word, ...(w.aliases ?? [])]) {
      if (word.length < 2) continue;
      const re = new RegExp(`(?<![\\p{L}\\p{N}_])${esc(word)}(?![\\p{L}\\p{N}_])`, 'giu');
      for (const m of text.matchAll(re)) ranges.push([m.index!, m.index! + m[0].length]);
    }
  return candidates.filter(m => !ranges.some(([a, b]) => a <= m.index! && b >= m.index! + m[0].length)).map(m => m[0]);
}
function partProse(doc: Page['doc']): string {
  const texts = blocksOf(doc.blocks).map(b => {
    if (b.type === 'markdown') return mdText(b.md);
    if (b.type === 'codewalk') return b.steps.map(s => inlineText(s.note)).join('\n');
    if (b.type === 'terminal') return b.steps.map(s => s.title).join('\n');
    if (b.type === 'animation') return animationTexts(b).join('\n');
    if (b.type === 'mermaid')
      return b.steps
        .flatMap(s => [
          s.title,
          ...[...s.src.matchAll(/\[([^\]]*)\]|\(([^()]*)\)|\{([^{}]*)\}|"([^"\n]*)"|\|([^|\n]*)\|/g)].map(
            m => m.slice(1).find(Boolean) ?? '',
          ),
        ])
        .join('\n');
    if (b.type === 'filetree') return b.trees.flatMap(t => [t.title ?? '', ...t.rows.map(r => r.note)]).join('\n');
    if (b.type === 'tierlist' || b.type === 'alignment') return b.llm ?? '';
    if (b.type === 'chart') return textualValues(b.chart).join('\n');
    return '';
  });
  // Author replacements are the only LLM text not already visited above.
  for (const b of blocksOf(doc.blocks))
    if ('llmKind' in b && b.llmKind === 'text' && b.llm)
      texts.push(mdText(markdownBlocks(parse(b.llm, { path: 'llm.md' }))));
  return texts.join('\n');
}
function textualValues(v: any, key = ''): string[] {
  if (v == null) return [];
  if (Array.isArray(v)) return v.flatMap(x => textualValues(x, key));
  if (typeof v === 'object') return Object.entries(v).flatMap(([k, x]) => textualValues(x, k));
  return ['label', 'caption', 'text', 'title', 'note', 'name', 'button'].includes(key) && typeof v === 'string'
    ? [v]
    : [];
}
export function checkHistory(docs: string, pages: Page[], ul: UlData, issues: Issue[]) {
  let patterns = HISTORY;
  const path = join(docs, 'standards/documentation/history-phrases.yaml');
  if (existsSync(path)) {
    try {
      const d = readYaml(path);
      const ps = Array.isArray(d) ? d : d.patterns;
      if (!Array.isArray(ps)) throw new Error('patterns must be a list');
      patterns = ps.map((x: any) => (typeof x === 'string' ? x : x.pattern));
      patterns.forEach(p => new RegExp(p, 'gimu'));
    } catch (e) {
      issue(issues, path, 'history phrases', `bad patterns: ${(e as Error).message}`);
      return;
    }
  }
  for (const p of pages)
    if (!p.generated && !p.adr && !p.translated && !p.fm.stub && !/changelog\.md$/i.test(p.file)) {
      const text =
        partProse(p.doc) +
        '\n' +
        markdownBlocks(p.doc)
          .flatMap(b => (b.t === 'h' ? [`${'#'.repeat(b.depth)} ${inlineText(b.c)}`] : []))
          .join('\n');
      for (const hit of new Set(historyHits(text, ul.words, patterns)))
        issue(
          issues,
          p.file,
          'history phrases',
          `"${hit}": state the present fact; put change reasons in an ADR; preserve old names in UL`,
        );
      if (allInline(markdownBlocks(p.doc)).some(x => x.t === 'del'))
        issue(issues, p.file, 'history phrases', 'struck prose belongs in history');
    }
  for (const file of walk(join(docs, 'domain')).filter(f => basename(f) === 'overview.yaml')) {
    try {
      const intro = readYaml(file)?.intro;
      if (typeof intro === 'string')
        for (const hit of historyHits(intro, ul.words, patterns))
          issue(issues, file, 'history phrases', `intro: "${hit}"`);
    } catch {}
  }
  for (const w of ul.words)
    if (w.status !== 'retired')
      for (const hit of new Set(historyHits(`${w.desc ?? ''}\n${w.details ?? ''}`, ul.words, patterns)))
        issue(
          issues,
          resolve(ul.root ?? dirname(docs), w.file ?? 'UL.yaml'),
          'history phrases',
          `${w.word}: "${hit}"`,
          'error',
          w.line ?? 1,
        );
}

function translationStructure(p: Page) {
  const clean = (v: any, key = ''): any => {
    if (
      [
        'line',
        'title',
        'caption',
        'label',
        'text',
        'texts',
        'note',
        'llm',
        'llmKind',
        'error',
        'hl',
        'walkFiles',
        'highlighted',
      ].includes(key)
    )
      return undefined;
    if (Array.isArray(v)) return v.map(x => clean(x, key));
    if (v && typeof v === 'object')
      return Object.fromEntries(
        Object.entries(v)
          .map(([k, x]) => [k, clean(x, k)])
          .filter(([, x]) => x !== undefined),
      );
    return v;
  };
  const parts = blocksOf(p.doc.blocks)
    .filter(b => !['markdown', 'toc'].includes(b.type))
    .map(b => {
      if (b.type === 'animation')
        return {
          type: b.type,
          attrs: clean(b.attrs),
          head: b.anim.head.replace(/"(?:[^"\\]|\\.)*"/g, '"text"'),
          steps: b.anim.steps.map(s => s.raw.replace(/"(?:[^"\\]|\\.)*"/g, '"text"')),
        };
      if (b.type === 'human') return { type: b.type, attrs: clean(b.attrs) };
      if (b.type === 'tabs' || b.type === 'accordion')
        return { type: b.type, attrs: clean(b.attrs), items: b.items.length };
      if (b.type === 'codewalk')
        return {
          type: b.type,
          attrs: clean(b.attrs),
          madeFiles: Object.fromEntries(b.steps.filter(s => !s.repo).map(s => [s.key, b.walkFiles[s.key]])),
          steps: b.steps.map(s => ({
            file: s.file,
            repo: s.repo,
            commit: s.commit,
            lines: s.lines,
            focus: s.focus,
            marks: s.marks,
            animated: s.animated,
          })),
        };
      if (b.type === 'terminal')
        return {
          type: b.type,
          attrs: clean(b.attrs),
          steps: b.steps.map(s => s.lines.map(l => ({ k: l.k, text: l.text, prompt: l.prompt }))),
        };
      return clean(b);
    });
  const codes: string[] = [];
  const scan = (bs: MdBlock[]) => {
    for (const b of bs)
      if (b.t === 'code') codes.push(b.lang + ':' + b.text);
      else if (b.t === 'quote' || b.t === 'callout') scan(b.c);
      else if (b.t === 'list') b.items.forEach(i => scan(i.c));
  };
  scan(markdownBlocks(p.doc));
  const xs = allInline(markdownBlocks(p.doc));
  return {
    parts,
    codes,
    inline: xs.filter(x => x.t === 'code').map(x => (x as any).v),
    refs: xs.filter(x => x.t === 'coderef').map(x => (x as any).ref),
  };
}
export function checkTranslations(docs: string, pages: Page[], issues: Issue[]) {
  const by = new Map(pages.map(p => [p.file, p]));
  for (const p of pages.filter(p => p.translated)) {
    const t = p.fm.translation;
    if (!mapping(t) || typeof t.source !== 'string') continue;
    const file = resolve(dirname(p.file), t.source);
    const source = by.get(file);
    if (!inside(docs, file) || !source || source.translated) {
      issue(issues, p.file, 'translation behind', 'source page does not exist or is itself a translation');
      continue;
    }
    const hashes = sectionHashes(source.body),
      old = t.sourceHashes;
    if (!Array.isArray(old) || hashes.length !== old.length || hashes.some((h, i) => h !== old[i])) {
      const secs = sectionsOf(source.body);
      const changed = hashes.flatMap((h, i) =>
        h !== old?.[i] ? [i === 0 ? 'summary' : secs[i].split('\n')[0].replace(/^## /, '')] : [],
      );
      issue(
        issues,
        p.file,
        'translation behind',
        `changed source sections: ${changed.join(', ') || 'section count'}; update and run dh stamp`,
      );
    }
    const a = translationStructure(source),
      b = translationStructure(p);
    for (const k of ['parts', 'codes', 'inline', 'refs'] as const)
      if (JSON.stringify(a[k]) !== JSON.stringify(b[k]))
        issue(issues, p.file, 'translation shape', `${k} differ from source`);
  }
}
export function checkLlm(docs: string, c: Config, issues: Issue[]) {
  if (c.llmView === 'build') return;
  let expected: Map<string, string>;
  try {
    expected = expectedLlm(docs);
  } catch (e) {
    issue(issues, docs, 'LLM view in step', (e as Error).message);
    return;
  }
  for (const [f, text] of expected)
    if (!existsSync(f) || readFileSync(f, 'utf8') !== text)
      issue(issues, f, 'LLM view in step', 'missing or differs from dh llm; regenerate and commit both views');
  for (const f of walk(join(dirname(docs), 'docs-llm')).filter(f => f.endsWith('.md')))
    if (!expected.has(resolve(f))) issue(issues, f, 'LLM view in step', 'LLM view has no source');
}
export function checkTree(docs: string, pages: Page[], o: Outline, c: Config, issues: Issue[]) {
  const concepts = new Map<string, string>();
  const adrs = new Map<string, number[]>();
  for (const p of pages.filter(p => !p.generated)) {
    checkFrontMatter(p, c, issues);
    checkShape(p, c, issues);
    checkRich(p, issues);
    if (!p.adr && !p.translated && typeof p.fm.concept === 'string') {
      const key = p.fm.concept.toLowerCase();
      const first = concepts.get(key);
      if (first) issue(issues, p.file, 'concept on one page', `concept also appears in ${first}`);
      else concepts.set(key, p.rel);
    }
    if (p.adr) {
      const dir = dirname(p.rel);
      adrs.set(dir, [...(adrs.get(dir) ?? []), Number(basename(p.file).slice(0, 4))]);
    }
  }
  for (const [dir, ns] of adrs)
    if (ns.sort((a, b) => a - b).some((n, i) => n !== i + 1))
      issue(issues, join(docs, dir), 'ADR shape', 'ADR numbers must start at 0001 and have no duplicates or gaps');
  for (const dir of existsSync(join(docs, 'domain'))
    ? readdirSync(join(docs, 'domain'), { withFileTypes: true })
        .filter(d => d.isDirectory() && !d.isSymbolicLink())
        .map(d => d.name)
    : []) {
    for (const f of ['overview.yaml', 'UL.yaml', 'index.md'])
      if (!existsSync(join(docs, 'domain', dir, f)))
        issue(issues, join(docs, 'domain', dir, f), 'doc shape', `each context requires ${f}`);
    const overview = join(docs, 'domain', dir, 'overview.yaml');
    if (existsSync(overview))
      try {
        const ov = readYaml(overview);
        if (
          !mapping(ov) ||
          Object.keys(ov).some(k => k !== 'intro') ||
          typeof ov.intro !== 'string' ||
          !ov.intro.trim()
        )
          issue(issues, overview, 'doc shape', 'overview.yaml needs only a nonempty intro');
        else {
          const d = parse(ov.intro, { path: 'intro.md' });
          if (allInline(markdownBlocks(d)).some(x => x.t === 'link' || x.t === 'coderef' || x.t === 'img'))
            issue(issues, overview, 'page list in a page', 'overview intro may not contain links');
        }
      } catch (e) {
        issue(issues, overview, 'doc shape', `overview.yaml: ${(e as Error).message}`);
      }
    const extra = walk(join(docs, 'domain', dir))
      .filter(f => f.endsWith('.md') && !/[\\/](?:index|README|UL)\.md$/.test(f))
      .map(f => relative(join(docs, 'domain', dir), f).split('/')[0]);
    for (const folder of new Set(extra))
      if (![c.innerFolder, 'architecture', 'implementation', 'adr'].includes(folder))
        issue(
          issues,
          join(docs, 'domain', dir, folder),
          'doc shape',
          `context pages belong in ${c.innerFolder}, architecture or implementation`,
        );
  }
  for (const f of walk(join(docs, 'guides')))
    if (basename(f) === 'UL.yaml') issue(issues, f, 'doc shape', 'guides may not define UL.yaml');
}

export function checkChanges(docs: string, pages: Page[], issues: Issue[], readGit = git) {
  const root = dirname(docs);
  const changed = readGit(root, [
    'diff',
    '--cached',
    '--name-status',
    '--no-renames',
    '-z',
    'HEAD',
    '--',
    'docs',
    '.cyan_state.yaml',
  ]);
  if (changed == null) return;
  const xs = changed.split('\0').filter(Boolean);
  const changes: { status: string; file: string }[] = [];
  for (let i = 0; i < xs.length; i += 2) changes.push({ status: xs[i], file: xs[i + 1] });
  const statePath = join(root, '.cyan_state.yaml');
  const beforeState = readGit(root, ['show', 'HEAD:.cyan_state.yaml']);
  const afterState = readGit(root, ['show', ':.cyan_state.yaml']);
  const protectedRepo = beforeState != null || afterState != null || existsSync(statePath);
  let stateChanged = false;
  const managed = new Set<string>();
  if (protectedRepo)
    try {
      const before = beforeState ? yaml(beforeState) : null,
        after = afterState ? yaml(afterState) : null;
      const stable = (v: any): string =>
        JSON.stringify(
          v && typeof v === 'object'
            ? Array.isArray(v)
              ? v.map(x => JSON.parse(stable(x)))
              : Object.fromEntries(
                  Object.keys(v)
                    .sort()
                    .map(k => [k, JSON.parse(stable(v[k]))]),
                )
            : v,
        );
      stateChanged =
        changes.some(x => x.file === '.cyan_state.yaml') && after != null && stable(before) !== stable(after);
      const collect = (v: any) => {
        if (!v || typeof v !== 'object') return;
        for (const [k, x] of Object.entries(v)) {
          if (['files', 'managed_files'].includes(k) && Array.isArray(x))
            for (const f of x) {
              if (typeof f === 'string') managed.add(f.replace(/^\.\//, ''));
              else if (mapping(f) && typeof f.path === 'string') managed.add(f.path);
            }
          else collect(x);
        }
      };
      collect(after);
    } catch (e) {
      issue(
        issues,
        statePath,
        'engineering standards untouched',
        `cannot verify staged CyanPrint state: ${(e as Error).message}`,
      );
    }
  for (const x of changes) {
    if (!x.file) continue;
    if (
      x.file.startsWith('docs/standards/') &&
      protectedRepo &&
      (!stateChanged || (x.status !== 'D' && !managed.has(x.file)))
    )
      issue(
        issues,
        join(root, x.file),
        'engineering standards untouched',
        'standards need a semantic staged CyanPrint state update and an entry in its managed files',
      );
    if (/\/adr\/[^/]+\.md$/.test(x.file) && ['M', 'D', 'T'].includes(x.status)) {
      const before = readGit(root, ['show', `HEAD:${x.file}`]),
        after = readGit(root, ['show', `:${x.file}`]);
      if (before?.replace(/\s/g, '') !== after?.replace(/\s/g, ''))
        issue(
          issues,
          join(root, x.file),
          'ADR unchanged',
          'accepted ADRs may only change whitespace; add a superseding ADR',
        );
    }
    if (x.file === 'docs/outline.yaml') {
      const entries = (text: string | null) => {
        try {
          const d = text ? yaml(text) : {};
          return [
            ...(d.contexts ?? []).flatMap((c: any) =>
              (c.pages ?? []).map((e: any) => ({ page: `domain/${c.context}/${e.page}`, rank: e.rank })),
            ),
            ...(d.guides ?? []).map((e: any) => ({ page: `guides/${e.page}`, rank: e.rank })),
            ...(d.repo ?? []),
          ];
        } catch {
          return [];
        }
      };
      const before = entries(readGit(root, ['show', 'HEAD:docs/outline.yaml'])),
        after = entries(readGit(root, ['show', ':docs/outline.yaml']));
      const old = new Map(before.map(e => [e.page, e.rank])),
        now = new Map(after.map(e => [e.page, e.rank]));
      const added = after.filter(e => !old.has(e.page)).map(e => `added ${e.page} (${e.rank})`);
      const removed = before.filter(e => !now.has(e.page)).map(e => `removed ${e.page} (${e.rank})`);
      issue(
        issues,
        join(docs, 'outline.yaml'),
        'outline changed',
        [...added, ...removed].join('; ') || 'outline metadata or reading order changed',
        'warn',
      );
    }
    if (x.file === 'docs/guides/running-example.md') {
      const users = pages
        .filter(p =>
          allInline(markdownBlocks(p.doc)).some(
            i =>
              i.t === 'link' &&
              i.href &&
              resolve(dirname(p.file), i.href.split('#')[0]) === join(docs, 'guides/running-example.md'),
          ),
        )
        .map(p => p.rel);
      issue(
        issues,
        join(root, x.file),
        'running example users',
        `review linked pages: ${users.join(', ') || 'none'}`,
        'warn',
      );
    }
  }
}
