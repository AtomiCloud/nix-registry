import { existsSync, readFileSync } from 'node:fs';
import { join, relative, resolve } from 'node:path';
import { spawnSync } from 'node:child_process';
import type { Block, Document, EditorBlock, Problem } from 'durian-hypermedia';
// These are the pinned package's own parser and edit evaluator. They are not
// reimplemented here. The public Document does not retain an editor's raw diff.
import {
  parseDoc,
  editorSpec,
  applyDiff,
  llmFormOf,
  type Node,
  type PartNode,
} from '../node_modules/durian-hypermedia/src/core/dh-lib';

export interface CodeHost {
  repoRoot: string;
  repoName: string;
  codeRoot?: string;
}

export function llmReplacementWarnings(document: Document): Problem[] {
  if (!document.source.includes(':::llm')) return [];
  const output: Problem[] = [];
  const count = (text: string) => text.trim().split(/\s+/).filter(Boolean).length;
  let parsed: ReturnType<typeof parseDoc>;
  try {
    parsed = parseDoc(document.path, document.source);
  } catch {
    return output;
  }
  function visit(nodes: Node[]) {
    for (const node of nodes)
      if (node.kind === 'part') {
        if (node.llm) {
          const replacement = count(node.llm.body);
          const original = count(node.name === 'human' ? node.body : llmFormOf(node, false));
          if (original >= 40 && replacement > original && replacement <= 160)
            output.push({
              line: node.llm.line,
              level: 'warn',
              message: `LLM replacement is ${replacement} words, longer than the ${original}-word part; shorten it or give the reviewer a reason`,
            });
        }
        visit(node.nodes);
        node.items.forEach(item => visit(item.nodes));
      }
  }
  visit(parsed.body);
  return output;
}

function git(root: string, args: string[]): string | null {
  const result = spawnSync('git', ['-C', root, ...args], { encoding: 'utf8', maxBuffer: 16 * 1024 * 1024 });
  return result.status === 0 ? result.stdout : null;
}
function contained(root: string, file: string): string {
  const path = resolve(root, file);
  const rel = relative(resolve(root), path);
  if (rel === '..' || rel.startsWith('../') || file.startsWith('/'))
    throw new Error(`editor path leaves its repository: ${file}`);
  return path;
}

/** Supply filesystem contents to the pinned editor parser, then replay its own edits. */
export function hydrateEditors(document: Document, host: CodeHost): Problem[] {
  const output: Problem[] = [];
  const editors: EditorBlock[] = [];
  function blocks(items: Block[]) {
    for (const block of items) {
      if (block.type === 'editor') editors.push(block);
      else if (block.type === 'human') blocks(block.body);
      else if (block.type === 'tabs' || block.type === 'accordion') block.items.forEach(item => blocks(item.body));
    }
  }
  blocks(document.blocks);
  if (!editors.length) return output;
  const parsed = parseDoc(document.path, document.source);
  const parts: PartNode[] = [];
  function nodes(items: Node[]) {
    for (const node of items)
      if (node.kind === 'part') {
        if (node.name === 'editor') parts.push(node);
        nodes(node.nodes);
        node.items.forEach(item => nodes(item.nodes));
      }
  }
  nodes(parsed.body);
  for (const part of parts) {
    const block = editors.find(editor => editor.line === part.line);
    if (!block) continue;
    const spec = editorSpec(parsed, part);
    const current = new Map<string, string[]>();
    const deferred = new Set<string>();
    const report = (line: number, message: string) =>
      output.push({ line, level: 'error', message: `editor: ${message}` });
    for (const [i, file] of spec.files.entries()) {
      let text: string | null = file.made ? (file.text ?? '') : null;
      const own = !file.repo || file.repo === host.repoName;
      const pins = document.frontMatter.pins as Record<string, string> | undefined;
      const pin = pins?.[file.repo];
      if (!file.made && !own && !pin) {
        report(file.line, `repo ${file.repo} requires a pins entry`);
        deferred.add(file.path);
        continue;
      }
      if (!file.made && !own && !host.codeRoot) {
        deferred.add(file.path);
        continue;
      }
      try {
        if (!file.made) {
          const repoRoot = own ? host.repoRoot : contained(host.codeRoot!, file.repo);
          const path = contained(repoRoot, file.file);
          if (pin) {
            if (!/^[a-f0-9]{40}$/i.test(pin)) throw new Error(`invalid full commit id for ${file.repo}`);
            text = git(repoRoot, ['show', `${pin}:${file.file}`]);
          } else
            text = git(repoRoot, ['show', `:${file.file}`]) ?? (existsSync(path) ? readFileSync(path, 'utf8') : null);
          if (text == null) throw new Error(`${file.repo}/${file.file} does not exist${pin ? ` at ${pin}` : ''}`);
        }
        const clean = text!.replace(/\n$/, '');
        current.set(file.path, clean.split('\n'));
        if (block.files[i]) Object.assign(block.files[i], { code: clean, commit: pin ?? null, error: null });
      } catch (error) {
        const message = error instanceof Error ? error.message : String(error);
        report(file.line, message);
        if (block.files[i]) block.files[i].error = message;
        deferred.add(file.path);
      }
    }
    for (const [i, step] of spec.steps.entries()) {
      if (deferred.has(step.file)) continue;
      const before = current.get(step.file);
      if (!before) {
        report(step.line, `edit names unlisted file ${step.file}`);
        continue;
      }
      const result = applyDiff(before, step.diff);
      if (result.err) report(step.line, `${step.file}: ${result.err}`);
      current.set(step.file, result.after);
      if (block.steps[i])
        Object.assign(block.steps[i], {
          before: before.join('\n'),
          after: result.after.join('\n'),
          added: result.added,
          removed: result.removed,
          error: result.err ?? null,
        });
    }
  }
  return output;
}
