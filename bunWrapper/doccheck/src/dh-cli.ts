#!/usr/bin/env bun
import {
  existsSync,
  mkdirSync,
  readFileSync,
  readdirSync,
  realpathSync,
  rmSync,
  statSync,
  writeFileSync,
} from 'node:fs';
import { basename, dirname, join, relative, resolve } from 'node:path';
import { spawnSync } from 'node:child_process';
import { VERSION, type Block, type Document } from 'durian-hypermedia';
import { stringify } from 'yaml';
import { hydrateEditors } from './dh-code';
import {
  bodyOf,
  expectedLlm,
  inside,
  llmForFile,
  parseFile,
  rootOf,
  sectionHashes,
  strictYaml,
  tokens,
  walkPages,
} from './dh-library';

const usage = 'usage: dh <llm|json|tokens|stamp> <page|dir> [--src dir] [--out dir] [--public]';

function canonical(path: string): string {
  const abs = resolve(path);
  return existsSync(abs) ? realpathSync(abs) : join(canonical(dirname(abs)), basename(abs));
}
function below(parent: string, child: string): boolean {
  const path = relative(parent, child);
  return path === '' || (path !== '..' && !path.startsWith('../'));
}

function readCode(repo: string, path: string, commit: string | null, codeRoot: string): string {
  const repoRoot = basename(codeRoot) === repo || !repo ? codeRoot : inside(codeRoot, repo);
  const file = inside(repoRoot, path);
  if (!commit) return readFileSync(file, 'utf8');
  if (!/^[a-f0-9]{40}$/i.test(commit)) throw new Error(`invalid full commit id: ${commit}`);
  const result = spawnSync('git', ['-C', repoRoot, 'show', `${commit}:${relative(repoRoot, file)}`], {
    encoding: 'utf8',
    maxBuffer: 16 * 1024 * 1024,
  });
  if (result.status !== 0) throw new Error(`cannot read ${repo}/${path} at ${commit}`);
  return result.stdout;
}

/** Fill code from the host's checkout. The format parser supplies every range. */
export function hydrate(doc: Document, codeRoot: string): Document {
  function visit(blocks: Block[]) {
    for (const block of blocks) {
      if (block.type === 'codewalk') {
        for (const step of block.steps) {
          if (step.code != null && !step.error) continue;
          try {
            const text = readCode(step.repo, step.file, step.commit, codeRoot);
            const lines = text.split('\n');
            const [from, to] = step.lines ?? [1, lines.length];
            if (from < 1 || to > lines.length || to < from)
              throw new Error(`range ${from}-${to} is outside ${step.file}`);
            step.code = lines.slice(from - 1, to).join('\n');
            step.error = null;
            block.walkFiles[step.key] = text;
          } catch (error) {
            step.error = String(error instanceof Error ? error.message : error);
          }
        }
      } else if (block.type === 'human') visit(block.body);
      else if (block.type === 'tabs' || block.type === 'accordion') block.items.forEach(item => visit(item.body));
    }
  }
  visit(doc.blocks);
  const origin = spawnSync('git', ['-C', codeRoot, 'remote', 'get-url', 'origin'], { encoding: 'utf8' });
  const outline = join(codeRoot, 'docs', 'outline.yaml');
  const repoName =
    origin.status === 0
      ? origin.stdout
          .trim()
          .split(/[/:]/)
          .pop()!
          .replace(/\.git$/, '')
      : ((existsSync(outline) ? strictYaml(readFileSync(outline, 'utf8'))?.repoName : undefined) ?? basename(codeRoot));
  hydrateEditors(doc, { repoRoot: codeRoot, repoName, codeRoot });
  return doc;
}

/** The renderer receives no source text or security callout in a public export. */
export function publicJson(doc: Document): Document {
  const copy = structuredClone(doc);
  copy.source = '';
  function clean(value: any): any {
    if (Array.isArray(value))
      return value.filter(item => !(item?.t === 'callout' && item.kind === 'security')).map(clean);
    if (value && typeof value === 'object')
      return Object.fromEntries(Object.entries(value).map(([key, item]) => [key, clean(item)]));
    return value;
  }
  copy.blocks = clean(copy.blocks);
  return copy;
}

function write(path: string, text: string) {
  mkdirSync(dirname(path), { recursive: true });
  writeFileSync(path, text);
  console.log(`wrote ${path}`);
}

function allMd(dir: string): string[] {
  return readdirSync(dir, { withFileTypes: true }).flatMap(entry => {
    const path = join(dir, entry.name);
    if (entry.isSymbolicLink()) return [];
    return entry.isDirectory() ? allMd(path) : entry.name.endsWith('.md') ? [resolve(path)] : [];
  });
}

/** Read the typed tree to count human text; :::llm replacements are not displayed. */
export function humanText(doc: Document): string {
  const texts: string[] = [];
  function visit(value: any, key = '') {
    if (value == null || ['llm', 'source', 'attrs', 'llmKind', 'error', 'walkFiles'].includes(key)) return;
    if (typeof value === 'string') {
      if (
        ['v', 'alt', 'target', 'text', 'title', 'note', 'code', 'before', 'after', 'name', 'label', 'caption'].includes(
          key,
        )
      )
        texts.push(value);
    } else if (Array.isArray(value)) value.forEach(item => visit(item, key));
    else if (typeof value === 'object') Object.entries(value).forEach(([field, item]) => visit(item, field));
  }
  visit(doc.blocks);
  return texts.join('\n');
}

export function main(args = process.argv.slice(2)): number {
  if (args[0] === '--version') {
    console.log(VERSION);
    return 0;
  }
  if (['--help', '-h'].includes(args[0])) {
    console.log(usage);
    return 0;
  }
  const [command, ...rest] = args;
  let codeRoot: string | undefined;
  let outDir: string | undefined;
  let publicView = false;
  const targets: string[] = [];
  for (let i = 0; i < rest.length; i++) {
    if (rest[i] === '--src' || rest[i] === '--out') {
      const option = rest[i];
      if (!rest[i + 1] || rest[i + 1].startsWith('--')) {
        console.error(`${option} needs a directory`);
        return 2;
      }
      if (option === '--src') codeRoot = resolve(rest[++i]);
      else outDir = resolve(rest[++i]);
    } else if (rest[i] === '--public') publicView = true;
    else if (rest[i].startsWith('-')) {
      console.error(`unknown option ${rest[i]}`);
      return 2;
    } else targets.push(resolve(rest[i]));
  }
  if (!['llm', 'json', 'tokens', 'stamp'].includes(command) || !targets.length) {
    console.error(usage);
    return 2;
  }
  if (outDir && targets.length > 1) {
    console.error('--out accepts one source target at a time');
    return 2;
  }
  try {
    if (command === 'tokens') console.log('file\tsource\thuman text\tLLM view (tokens estimated as characters / 4)');
    for (const target of targets) {
      if (command === 'llm') {
        if (statSync(target).isDirectory()) {
          const output = outDir ?? join(dirname(target), 'docs-llm');
          if (below(canonical(target), canonical(output)) || below(canonical(output), canonical(target)))
            throw new Error('--out must neither contain nor sit inside the source tree');
          const expected = expectedLlm(target, output, publicView);
          // Validate the whole output set before changing a view.
          for (const path of expected.keys())
            if (!below(canonical(output), canonical(path))) throw new Error(`output link leaves ${output}: ${path}`);
          for (const [path, text] of expected) write(path, text);
          if (existsSync(output))
            for (const path of allMd(output)) {
              if (!expected.has(path)) {
                const text = readFileSync(path, 'utf8');
                if (
                  text.includes('<!-- Made by dh llm from the source; nobody edits this file. -->') ||
                  text.includes('<!-- Made by dh llm from docs/, in reading order; nobody edits this folder. -->')
                ) {
                  rmSync(path);
                  console.log(`removed ${path} (no source page)`);
                }
              }
            }
        } else {
          const sourcePath = relative(join(rootOf(target), 'docs'), target);
          const output = outDir
            ? join(outDir, basename(target).replace(/\.dh\.md$/, '.md'))
            : join(
                rootOf(target),
                'docs-llm',
                (sourcePath.startsWith('../') ? basename(target) : sourcePath).replace(/\.dh\.md$/, '.md'),
              );
          if (canonical(output) === canonical(target)) throw new Error('the output must differ from the source');
          if (below(canonical(join(rootOf(target), 'docs')), canonical(output)))
            throw new Error('the LLM output must be outside docs/');
          write(output, llmForFile(target, publicView));
        }
        continue;
      }
      for (const page of walkPages(target, command === 'stamp')) {
        const doc = parseFile(page, publicView);
        if (command === 'stamp' && statSync(target).isDirectory() && !doc.frontMatter.translation) continue;
        const error = doc.problems.find(p => p.level === 'error');
        if (error) throw new Error(`${page}:${error.line}: ${error.message}`);
        if (command === 'stamp') {
          const translation: any = doc.frontMatter.translation;
          if (!translation?.source || !translation?.lang) throw new Error(`${page}: translation needs lang and source`);
          const source = resolve(dirname(page), translation.source);
          const sourceDoc = parseFile(source);
          const sourceError = sourceDoc.problems.find(problem => problem.level === 'error');
          if (sourceError) throw new Error(`${source}:${sourceError.line}: ${sourceError.message}`);
          const raw = readFileSync(page, 'utf8');
          const match = raw.match(/^---\r?\n([\s\S]*?)\r?\n---(?:\r?\n|$)/);
          if (!match) throw new Error(`${page}: missing front matter`);
          const fm = strictYaml(match[1]);
          fm.translation.sourceHashes = sectionHashes(bodyOf(sourceDoc.source));
          write(page, `---\n${stringify(fm).trimEnd()}\n---\n${raw.slice(match[0].length)}`);
        } else if (command === 'json') {
          const hydrated = hydrate(doc, codeRoot ?? rootOf(page));
          const json = JSON.stringify(publicView ? publicJson(hydrated) : hydrated, null, 2) + '\n';
          if (outDir)
            write(
              join(
                outDir,
                relative(statSync(target).isDirectory() ? target : dirname(target), page).replace(
                  /(?:\.dh)?\.md$/,
                  '.json',
                ),
              ),
              json,
            );
          else process.stdout.write(json);
        } else {
          hydrate(doc, codeRoot ?? rootOf(page));
          console.log(
            [
              relative(process.cwd(), page),
              tokens(doc.source),
              tokens(humanText(doc)),
              tokens(llmForFile(page, publicView)),
            ].join('\t'),
          );
        }
      }
    }
    return 0;
  } catch (error) {
    console.error(error instanceof Error ? error.message : String(error));
    return 1;
  }
}

if (import.meta.main) process.exitCode = main();
