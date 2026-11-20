import { createHash } from 'node:crypto';
import { existsSync, readFileSync, readdirSync, statSync } from 'node:fs';
import { basename, dirname, join, relative, resolve, sep } from 'node:path';
import { parse, llmView, setYamlParser, type Document } from 'durian-hypermedia';
import { parseDocument } from 'yaml';
import { Lexer } from 'marked';

/** Strict YAML is shared with the pinned format parser. Duplicate keys refuse. */
export function strictYaml(text: string): any {
  const doc = parseDocument(text, { uniqueKeys: true });
  if (doc.errors.length) throw new Error(doc.errors.map(e => e.message).join('; '));
  return doc.toJS({ maxAliasCount: 16 });
}
setYamlParser(strictYaml);

export function rootOf(path: string): string {
  const abs = resolve(path);
  const at = abs.lastIndexOf(`${sep}docs${sep}`);
  return at < 0 ? dirname(abs) : abs.slice(0, at);
}

const pageCache = new Map<string, { source: string; document: Document }>();
/** Remove an entire CommonMark quote, including lazy continuation lines. */
export function publicSource(source: string): string {
  const fm = source.match(/^---\r?\n[\s\S]*?\r?\n---(?:\r?\n|$)/)?.[0] ?? '';
  const body = source.slice(fm.length);
  const sensitive = (token: any): boolean => {
    if (token.type === 'blockquote') {
      const first = token.tokens?.find((item: any) => item.type !== 'space');
      const inline = first?.tokens?.find((item: any) => item.type !== 'space');
      if (inline?.type === 'strong' && inline.text === 'Security finding:') return true;
    }
    return (token.tokens ?? []).some(sensitive) || (token.items ?? []).some(sensitive);
  };
  return (
    fm +
    Lexer.lex(body)
      .map(token => (sensitive(token) ? '\n' : token.raw))
      .join('')
  );
}
export function parseFile(path: string, publicView = false): Document {
  const source = readFileSync(path, 'utf8');
  const key = `${resolve(path)}:${publicView}`;
  const cached = pageCache.get(key);
  if (cached?.source === source) return cached.document;
  const document = parse(publicView ? publicSource(source) : source, {
    path: relative(rootOf(path), resolve(path)).split(sep).join('/'),
    publicView,
  });
  if (pageCache.size >= 512) pageCache.delete(pageCache.keys().next().value!);
  pageCache.set(key, { source, document });
  return document;
}

export function llmForFile(path: string, publicView = false): string {
  const doc = parseFile(path, publicView);
  const error = doc.problems.find(p => p.level === 'error');
  if (error) throw new Error(`${path}:${error.line}: ${error.message}`);
  return llmView(doc);
}

export const contextOverview = (path: string): boolean => /\/docs\/domain\/[^/]+\/index\.md$/.test(resolve(path));
/** Maps, context overviews and word lists are generated. Translations stay outside the English view. */
export function walkPages(target: string, includeTranslations = false): string[] {
  const abs = resolve(target);
  if (!statSync(abs).isDirectory()) return abs.endsWith('.md') ? [abs] : [];
  return readdirSync(abs, { withFileTypes: true })
    .flatMap(entry => {
      const path = join(abs, entry.name);
      if (entry.isSymbolicLink() || (!includeTranslations && entry.name === 'i18n') || entry.name.startsWith('.'))
        return [];
      if (entry.isDirectory()) return walkPages(path, includeTranslations);
      return entry.name.endsWith('.md') && !['README.md', 'UL.md'].includes(entry.name) && !contextOverview(path)
        ? [path]
        : [];
    })
    .sort();
}

export function readOrder(docsDir: string, pages: string[]): string[] {
  const outlinePath = join(docsDir, 'outline.yaml');
  const outline = existsSync(outlinePath) ? (strictYaml(readFileSync(outlinePath, 'utf8')) ?? {}) : {};
  const order: string[] = [...(outline.readingPath ?? [])];
  for (const context of outline.contexts ?? []) {
    for (const page of context.pages ?? []) order.push(`domain/${context.context}/${page.page}`);
  }
  for (const page of outline.guides ?? []) order.push(`guides/${page.page}`);
  for (const page of outline.repo ?? []) order.push(page.page);
  const key = (s: string) => s.replace(/\.dh\.md$/, '.md');
  const ranks = new Map<string, number>();
  order.forEach((path, i) => {
    if (!ranks.has(key(path))) ranks.set(key(path), i);
  });
  return [...pages].sort(
    (a, b) =>
      (ranks.get(key(relative(docsDir, a))) ?? order.length) - (ranks.get(key(relative(docsDir, b))) ?? order.length) ||
      a.localeCompare(b),
  );
}

export function expectedLlm(
  docsDir: string,
  outDir = join(dirname(resolve(docsDir)), 'docs-llm'),
  publicView = false,
): Map<string, string> {
  const out = new Map<string, string>();
  const index: string[] = [];
  const intros: string[] = [];
  const seenContexts = new Set<string>();
  const reservedIndex = join(resolve(outDir), 'index.md');
  for (const page of readOrder(docsDir, walkPages(docsDir))) {
    const target = join(outDir, relative(docsDir, page).replace(/\.dh\.md$/, '.md'));
    const doc = parseFile(page, publicView);
    const error = doc.problems.find(p => p.level === 'error');
    if (error) throw new Error(`${page}:${error.line}: ${error.message}`);
    if (out.has(resolve(target)) || resolve(target) === reservedIndex)
      throw new Error(`${page}: LLM output path collides at ${target}`);
    out.set(resolve(target), llmView(doc));
    const context = relative(docsDir, page).match(/^domain\/([^/]+)\//)?.[1];
    if (context && !seenContexts.has(context)) {
      seenContexts.add(context);
      const overview = join(docsDir, 'domain', context, 'overview.yaml');
      const metadata = strictYaml(readFileSync(overview, 'utf8'));
      if (!metadata || typeof metadata.intro !== 'string') throw new Error(`${overview}: intro is required`);
      intros.push(
        `## ${context.charAt(0).toUpperCase() + context.slice(1).replace(/-/g, ' ')}`,
        '',
        metadata.intro.trim(),
        '',
      );
    }
    index.push(
      `- [${doc.frontMatter.title ?? relative(docsDir, page)}](${relative(outDir, target)}): ${doc.frontMatter.description ?? ''}`,
    );
  }
  if (out.size)
    out.set(
      join(resolve(outDir), 'index.md'),
      `# Docs for LLMs\n<!-- Made by dh llm from docs/, in reading order; nobody edits this folder. -->\n\n${intros.join('\n')}${intros.length ? '\n' : ''}## Pages, in reading order\n\n${index.join('\n')}\n`,
    );
  return out;
}

/** Section boundaries come from the CommonMark lexer, including fenced examples. */
export function sectionsOf(body: string): string[] {
  const sections: string[] = [];
  let current = '';
  for (const token of Lexer.lex(body)) {
    if (token.type === 'heading' && token.depth === 2) {
      sections.push(current.replace(/\n$/, ''));
      current = '';
    }
    current += token.raw;
  }
  sections.push(current.replace(/\n$/, ''));
  return sections;
}
export const sectionHashes = (body: string): string[] =>
  sectionsOf(body).map(s => createHash('sha256').update(s.trim()).digest('hex').slice(0, 10));
export const bodyOf = (text: string): string => text.replace(/^---\r?\n[\s\S]*?\r?\n---(?:\r?\n|$)/, '');
export const tokens = (text: string): number => Math.ceil(text.length / 4);

export function sourceRoot(path: string, codeRoot?: string): string {
  return resolve(codeRoot ?? rootOf(path));
}

export function inside(root: string, path: string): string {
  const resolved = resolve(root, path);
  const rel = relative(resolve(root), resolved);
  if (rel === '..' || rel.startsWith(`..${sep}`) || rel.startsWith(sep))
    throw new Error(`path leaves ${root}: ${path}`);
  return resolved;
}
