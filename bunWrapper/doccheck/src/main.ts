#!/usr/bin/env bun
import { existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { dirname, join, relative, resolve, basename } from 'node:path';
import {
  type Config,
  type Issue,
  type Page,
  type Outline,
  configFor,
  loadPages,
  issue,
  yamlText,
  readYaml,
  slug,
  inside,
  entriesOf,
  git,
  repoName,
} from './model';
import { loadOutline, checkOutline, splitOutline, handwritten, makeStubs, outlineText } from './outline';
import { generate } from './generate';
import { checkTree, checkHistory, checkTranslations, checkLlm, checkChanges, loadUl, type UlData } from './checks';
import { checkCode } from './code';
import './dh-library';

export const VERSION = '0.1.0';
export function checkAll(
  docs: string,
  c: Config,
  pages = loadPages(docs),
  ul: UlData = { schema: 'ul/v1', words: [] },
  src?: string,
): Issue[] {
  const issues: Issue[] = [];
  const o = loadOutline(docs, c, pages, issues);
  checkOutline(docs, pages, o, issues);
  checkTree(docs, pages, o, c, issues);
  checkCode(docs, pages, o, issues, src);
  checkHistory(docs, pages, ul, issues);
  checkTranslations(docs, pages, issues);
  checkChanges(docs, pages, issues);
  checkLlm(docs, c, issues);
  return issues;
}
export function adrNew(docs: string, area: string, title: string): string {
  const adrSlug = title
    .normalize('NFKD')
    .replace(/\p{M}/gu, '')
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-|-$/g, '');
  if (!title.trim() || !adrSlug) throw new Error('ADR title must contain a name with Latin letters or digits');
  let dir =
    area === '.' ? docs : existsSync(join(docs, 'domain', area)) ? join(docs, 'domain', area) : resolve(docs, area);
  if (
    !inside(docs, dir) ||
    relative(docs, dir)
      .split(/[\\/]/)
      .some(p => ['standards', 'i18n'].includes(p))
  )
    throw new Error('ADR area must be a repo-owned docs folder');
  if (basename(dir) !== 'adr') dir = join(dir, 'adr');
  const pages = loadPages(docs).filter(p => p.adr && dirname(p.file) === dir);
  const ns = pages.map(p => Number(basename(p.file).slice(0, 4))).sort((a, b) => a - b);
  if (ns.some((n, i) => n !== i + 1)) throw new Error('fix ADR numbering before creating another');
  const next = pages.length + 1;
  if (next > 9999) throw new Error('ADR folder is full');
  const file = join(dir, `${String(next).padStart(4, '0')}-${adrSlug}.md`);
  let namespaces: string[] = [];
  const ul = join(dirname(dir), 'UL.yaml');
  if (existsSync(ul)) {
    const ns = readYaml(ul)?.namespace;
    if (typeof ns === 'string') namespaces = [ns];
  }
  const date = new Intl.DateTimeFormat('en-CA', {
    timeZone: 'America/Los_Angeles',
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
  }).format(new Date());
  const fm = { title, description: title, date, namespaces };
  mkdirSync(dir, { recursive: true });
  writeFileSync(file, `---\n${yamlText(fm)}---\n\n## Context\n\n## Options\n\n## Decision\n\n## Consequences\n`, {
    flag: 'wx',
  });
  return file;
}
const HELP = `doccheck ${VERSION}
Usage:
  doccheck check [docs-dir] [--src code-root]
  doccheck gen [docs-dir] [--check]
  doccheck outline [docs-dir]
  doccheck outline stubs [docs-dir]
  doccheck outline split [docs-dir]
  doccheck adr new <area> <title> [--docs docs-dir]
Switches: --page-order A|B  --inner-folder model  --llm-view committed|build
Config: docs/doccheck.yaml (pageOrder: A, innerFolder: model, llmView: committed)
Output: --format text|json. Exit: 0 clean, 1 refused, 2 usage or IO error.
`;
export function main(argv = process.argv.slice(2)): number {
  if (argv.includes('--version') || argv[0] === 'version') {
    console.log(VERSION);
    return 0;
  }
  if (!argv.length || argv.includes('--help') || argv[0] === 'help') {
    console.log(HELP);
    return 0;
  }
  const args = [...argv];
  let format = 'text',
    docsArg: string | undefined,
    src: string | undefined,
    check = false;
  const overrides: Partial<Config> = {};
  const positional: string[] = [];
  try {
    for (let i = 0; i < args.length; i++) {
      const a = args[i];
      if (a === '--check') {
        check = true;
        continue;
      }
      if (['--format', '--docs', '--root', '--src', '--page-order', '--inner-folder', '--llm-view'].includes(a)) {
        const v = args[++i];
        if (!v || v.startsWith('--')) throw new Error(`${a} needs a value`);
        if (a === '--format') format = v;
        else if (a === '--docs') docsArg = v;
        else if (a === '--root') docsArg = join(v, 'docs');
        else if (a === '--src') src = resolve(v);
        else if (a === '--page-order') overrides.pageOrder = v as Config['pageOrder'];
        else if (a === '--inner-folder') overrides.innerFolder = v;
        else overrides.llmView = v as Config['llmView'];
      } else if (a.startsWith('-')) throw new Error(`unknown option ${a}`);
      else positional.push(a);
    }
    if (!['text', 'json'].includes(format)) throw new Error('format must be text or json');
    const cmd = positional.shift()!;
    let sub = '';
    if (cmd === 'outline' && ['stubs', 'split'].includes(positional[0])) sub = positional.shift()!;
    if (cmd === 'stubs') sub = 'stubs';
    const adr = cmd === 'adr' && positional[0] === 'new';
    if (!['check', 'gen', 'outline', 'stubs', 'adr'].includes(cmd)) throw new Error(`unknown command ${cmd}`);
    if (cmd === 'adr' && !adr) throw new Error('use adr new <area> <title>');
    const docs = resolve(docsArg ?? (!adr ? positional.shift() : undefined) ?? 'docs');
    if (!existsSync(docs)) throw new Error(`docs folder does not exist: ${docs}`);
    const c = configFor(docs, overrides);
    const pages = loadPages(docs);
    const issues: Issue[] = [];
    let message = '';
    if (adr) {
      positional.shift();
      if (positional.length !== 2) throw new Error('adr new needs area and quoted title');
      message = `wrote ${adrNew(docs, positional[0], positional[1])}`;
    } else {
      if (positional.length) throw new Error('unexpected positional arguments');
      if (check && cmd !== 'gen') throw new Error('--check applies to gen');
      if (sub === 'split') message = `moved ${splitOutline(docs, pages)} pages to front matter; run doccheck gen`;
      else if (sub === 'stubs') {
        const o = handwritten(docs);
        message = `wrote ${makeStubs(docs, o, c)} stubs`;
      } else if (cmd === 'check') {
        let ul: UlData;
        try {
          ul = loadUl(dirname(docs));
        } catch (e) {
          issue(issues, docs, 'history phrases', (e as Error).message);
          ul = { schema: 'ul/v1', words: [] };
        }
        issues.push(...checkAll(docs, c, pages, ul, src));
        message = `checked ${pages.filter(p => !p.generated).length} pages`;
      } else {
        const o = loadOutline(docs, c, pages, issues);
        if (cmd === 'outline') {
          if (issues.length) {
          } else if (format === 'text') {
            console.log(outlineText(o));
            return 0;
          } else {
            console.log(JSON.stringify({ outline: o, issues }));
            return 0;
          }
        } else
          message = check
            ? 'checked generated files'
            : `wrote ${generate(docs, pages, o, c, false, issues)} generated files`;
        if (cmd === 'gen' && check) generate(docs, pages, o, c, true, issues);
      }
    }
    const errors = issues.filter(i => i.level === 'error').length;
    if (format === 'json') console.log(JSON.stringify({ schema: 'doccheck/v1', message, errors, issues }));
    else {
      for (const i of issues)
        console.log(
          `${i.level === 'error' ? 'ERROR' : 'warn'} ${relative(process.cwd(), i.file)}:${i.line}: [${i.check}] ${i.message}`,
        );
      console.log(
        `${errors ? `${errors} error(s)` : `ok: ${message}`}${issues.some(i => i.level === 'warn') ? `; ${issues.filter(i => i.level === 'warn').length} warning(s)` : ''}`,
      );
    }
    return errors ? 1 : 0;
  } catch (e) {
    if (format === 'json') console.log(JSON.stringify({ schema: 'doccheck/v1', error: (e as Error).message }));
    else console.error(`doccheck: ${(e as Error).message}`);
    return 2;
  }
}
if (import.meta.main) process.exitCode = main();
