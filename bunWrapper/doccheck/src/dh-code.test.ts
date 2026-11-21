import { afterEach, expect, test } from 'bun:test';
import { mkdtempSync, mkdirSync, writeFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { parse } from 'durian-hypermedia';
import { hydrateEditors, llmReplacementWarnings } from './dh-code';
import './dh-library';

const roots: string[] = [];
test('an LLM replacement longer than a substantial part warns below 160 words', () => {
  const source = '```terminal\n' + 'word '.repeat(42) + '\n```\n\n:::llm\n';
  expect(
    llmReplacementWarnings(parse(source + 'word '.repeat(50) + '\n:::\n', { path: 'example.dh.md' })),
  ).toHaveLength(1);
  expect(
    llmReplacementWarnings(parse(source + 'word '.repeat(30) + '\n:::\n', { path: 'example.dh.md' })),
  ).toHaveLength(0);
});
afterEach(() => roots.splice(0).forEach(root => rmSync(root, { recursive: true, force: true })));
function fixture(text = 'const one = 1;\nconst two = 2;\n') {
  const root = mkdtempSync(join(tmpdir(), 'doccheck-editor-'));
  roots.push(root);
  mkdirSync(join(root, 'src'));
  writeFileSync(join(root, 'src', 'sample.ts'), text);
  return root;
}
function doc(old = 'const one = 1;') {
  return parse(
    `---\ntitle: Example\npins: {}\n---\n\nAn example.\n\n## Editor\n\n\`\`\`editor repo=shop\nsrc/sample.ts\n@step Change one\n-${old}\n+const one = 3;\n@step Change two\n-const two = 2;\n+const two = 4;\n\`\`\`\n`,
    { path: 'docs/example.dh.md' },
  );
}

test('editor loads host files and replays the pinned parser edits', () => {
  const root = fixture();
  const document = doc();
  expect(hydrateEditors(document, { repoRoot: root, repoName: 'shop' })).toEqual([]);
  const block: any = document.blocks.find(block => block.type === 'editor');
  expect(block.files[0].code).toBe('const one = 1;\nconst two = 2;');
  expect(block.steps[0].before).toContain('const one = 1;');
  expect(block.steps[0].after).toContain('const one = 3;');
  expect(block.steps[1].before).toContain('const one = 3;');
  expect(block.steps[1].after).toContain('const two = 4;');
});

test('editor refuses an edit to a line absent from the actual file', () => {
  const root = fixture();
  const errors = hydrateEditors(doc('a made-up old line'), { repoRoot: root, repoName: 'shop' });
  expect(errors.some(error => error.message.includes('line not in the file'))).toBe(true);
  expect(errors.every(error => error.line > 0)).toBe(true);
});

test('external editor files require a pin and defer only when code root is unavailable', () => {
  const root = fixture();
  const document = doc();
  expect(
    hydrateEditors(document, { repoRoot: root, repoName: 'another' }).some(error =>
      error.message.includes('pins entry'),
    ),
  ).toBe(true);
  document.frontMatter.pins = { shop: 'a'.repeat(40) };
  expect(hydrateEditors(document, { repoRoot: root, repoName: 'another' })).toEqual([]);
  expect(
    hydrateEditors(document, { repoRoot: root, repoName: 'another', codeRoot: root }).some(error =>
      error.message.includes('does not exist'),
    ),
  ).toBe(true);
});
