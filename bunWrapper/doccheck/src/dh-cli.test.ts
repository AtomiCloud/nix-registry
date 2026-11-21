import { afterEach, expect, test } from 'bun:test';
import { mkdtempSync, mkdirSync, readFileSync, rmSync, symlinkSync, writeFileSync, existsSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { parse } from 'durian-hypermedia';
import { main, hydrate, humanText, publicJson } from './dh-cli';
import { expectedLlm, llmForFile, parseFile, sectionHashes, sectionsOf, strictYaml, walkPages } from './dh-library';

const roots: string[] = [];
afterEach(() => roots.splice(0).forEach(root => rmSync(root, { recursive: true, force: true })));
function tree() {
  const root = mkdtempSync(join(tmpdir(), 'dh-test-'));
  roots.push(root);
  mkdirSync(join(root, 'docs'));
  return root;
}
const page =
  '---\ntitle: Example\nconcept: example\ndescription: A page.\nnamespaces: []\ntype: feature\n---\n\nA summary with `code`.\n\n## How it works\n\nA person reads this.\n';

test('llm mirrors checked sources and removes an orphan view', () => {
  const root = tree();
  writeFileSync(join(root, 'docs', 'example.md'), page);
  expect(main(['llm', join(root, 'docs')])).toBe(0);
  expect(readFileSync(join(root, 'docs-llm', 'example.md'), 'utf8')).toBe(llmForFile(join(root, 'docs', 'example.md')));
  writeFileSync(
    join(root, 'docs-llm', 'orphan.md'),
    '<!-- Made by dh llm from the source; nobody edits this file. -->\n',
  );
  writeFileSync(join(root, 'docs-llm', 'unrelated.md'), 'a file the tool does not own');
  expect(main(['llm', join(root, 'docs')])).toBe(0);
  expect(existsSync(join(root, 'docs-llm', 'orphan.md'))).toBe(false);
  expect(existsSync(join(root, 'docs-llm', 'unrelated.md'))).toBe(true);
  expect(expectedLlm(join(root, 'docs')).size).toBe(2);
});

test('LLM output refuses overlapping paths, including a symlink', () => {
  const root = tree();
  const source = join(root, 'docs', 'example.md');
  writeFileSync(source, page);
  expect(main(['llm', join(root, 'docs'), '--out', root])).toBe(1);
  expect(main(['llm', join(root, 'docs'), '--out', join(root, 'docs', 'view')])).toBe(1);
  symlinkSync(join(root, 'docs'), join(root, 'view'));
  expect(main(['llm', join(root, 'docs'), '--out', join(root, 'view')])).toBe(1);
  expect(readFileSync(source, 'utf8')).toBe(page);
});

test('an existing output file link cannot overwrite a source', () => {
  const root = tree();
  const source = join(root, 'docs', 'example.md');
  writeFileSync(source, page);
  mkdirSync(join(root, 'docs-llm'));
  symlinkSync(source, join(root, 'docs-llm', 'example.md'));
  expect(main(['llm', join(root, 'docs')])).toBe(1);
  expect(readFileSync(source, 'utf8')).toBe(page);
});

test('public LLM output drops security findings and retains ordinary prose', () => {
  const root = tree();
  const path = join(root, 'docs', 'example.md');
  writeFileSync(path, page + '\n> **Security finding:** Sensitive details.\n\nVisible ending.\n');
  expect(llmForFile(path)).toContain('Sensitive details');
  expect(llmForFile(path, true)).not.toContain('Sensitive details');
  expect(llmForFile(path, true)).toContain('Visible ending');
  const doc = parse(readFileSync(path, 'utf8'), { path: 'docs/example.md', publicView: true });
  expect(JSON.stringify(publicJson(doc))).not.toContain('Sensitive details');
  expect(JSON.stringify(publicJson(doc))).toContain('Visible ending');
});

test('stamp records sections while retaining the translation body', () => {
  const root = tree();
  writeFileSync(join(root, 'docs', 'example.md'), page);
  const translation = join(root, 'docs', 'spanish.md');
  writeFileSync(
    translation,
    page.replace('type: feature', 'type: feature\ntranslation:\n  lang: es\n  source: example.md'),
  );
  expect(main(['stamp', translation])).toBe(0);
  const text = readFileSync(translation, 'utf8');
  expect(text).toContain('sourceHashes:');
  expect(text.endsWith('A person reads this.\n')).toBe(true);
  expect(main(['stamp', join(root, 'docs', 'example.md')])).toBe(1);
  writeFileSync(join(root, 'docs', 'example.md'), page + '\n```terminal\n$ unfinished\n');
  expect(main(['stamp', translation])).toBe(1);
});

test('section hashes treat fenced headings as code and change by section', () => {
  const text = 'Summary\n\n```md\n## Example\n```\n\n## Real\nText\n';
  expect(sectionsOf(text)).toHaveLength(2);
  const before = sectionHashes(text);
  const after = sectionHashes(text.replace('Text', 'Changed'));
  expect(after[0]).toBe(before[0]);
  expect(after[1]).not.toBe(before[1]);
});

test('strict YAML refuses duplicate keys and unquoted colon values', () => {
  expect(strictYaml('title: Example')).toEqual({ title: 'Example' });
  expect(() => strictYaml('title: One\ntitle: Two')).toThrow();
  expect(() => strictYaml('title: One: Two')).toThrow();
  const first = strictYaml('items: [one, two]');
  first.items.push('changed');
  expect(strictYaml('items: [one, two]')).toEqual({ items: ['one', 'two'] });
});

test('json hydrates parser supplied code ranges from a host checkout', () => {
  const root = tree();
  writeFileSync(join(root, 'sample.ts'), 'const one = 1;\nconst two = 2;\n');
  const doc = parse('```codewalk repo=demo\n@step sample.ts#L1-L1\nRead one.\n@step #L2-L2\nRead two.\n```\n', {
    path: 'docs/example.dh.md',
  });
  const result = hydrate(doc, root);
  const walk: any = result.blocks.find(block => block.type === 'codewalk');
  // An explicit repo can be the code root itself.
  mkdirSync(join(root, 'demo'));
  writeFileSync(join(root, 'demo', 'sample.ts'), 'const one = 1;\nconst two = 2;\n');
  hydrate(result, root);
  expect(walk.steps[0].code).toBe('const one = 1;');
  expect(walk.steps[1].code).toBe('const two = 2;');
});

test('json refuses a code link that leaves the checkout and reads nothing of its target', () => {
  const root = tree();
  writeFileSync(join(root, 'secret.txt'), 'top secret\n');
  mkdirSync(join(root, 'demo'));
  symlinkSync(join(root, 'secret.txt'), join(root, 'demo', 'link.ts'));
  writeFileSync(join(root, 'demo', 'real.ts'), 'const one = 1;\n');
  const doc = parse('```codewalk repo=demo\n@step link.ts#L1-L1\nRead.\n@step real.ts#L1-L1\nRead.\n```\n', {
    path: 'docs/example.dh.md',
  });
  const walk: any = hydrate(doc, root).blocks.find(block => block.type === 'codewalk');
  expect(walk.steps[0].error).toContain('leaves');
  expect(JSON.stringify(walk)).not.toContain('top secret');
  expect(walk.steps[1].code).toBe('const one = 1;');
});

test('json export refuses an output link before writing anything', () => {
  const root = tree();
  const source = join(root, 'docs', 'example.md');
  writeFileSync(source, page);
  mkdirSync(join(root, 'out'));
  symlinkSync(source, join(root, 'out', 'example.json'));
  expect(main(['json', join(root, 'docs'), '--out', join(root, 'out')])).toBe(1);
  expect(readFileSync(source, 'utf8')).toBe(page);
  // a link out of the folder, and one that does not exist yet, are refused too
  rmSync(join(root, 'out', 'example.json'));
  writeFileSync(join(root, 'elsewhere.json'), 'untouched');
  symlinkSync(join(root, 'elsewhere.json'), join(root, 'out', 'example.json'));
  expect(main(['json', join(root, 'docs'), '--out', join(root, 'out')])).toBe(1);
  expect(readFileSync(join(root, 'elsewhere.json'), 'utf8')).toBe('untouched');
  rmSync(join(root, 'out', 'example.json'));
  expect(main(['json', join(root, 'docs'), '--out', join(root, 'out')])).toBe(0);
  expect(JSON.parse(readFileSync(join(root, 'out', 'example.json'), 'utf8')).ast).toBe(1);
});

test('tokens include ordinary human prose and usage rejects invalid commands', () => {
  const doc = parse(page, { path: 'example.md' });
  expect(humanText(doc)).toContain('A person reads this.');
  expect(main(['unknown', 'docs'])).toBe(2);
  expect(main(['llm', '--out'])).toBe(2);
});

test('page discovery skips generated maps, word lists, translations and symlinks', () => {
  const root = tree();
  writeFileSync(join(root, 'docs', 'example.md'), page);
  writeFileSync(join(root, 'docs', 'README.md'), '# Map');
  writeFileSync(join(root, 'docs', 'UL.md'), '# Words');
  symlinkSync(root, join(root, 'docs', 'cycle'));
  expect(walkPages(join(root, 'docs'))).toHaveLength(1);
});

test('reusing a parsed page still detects a source change', () => {
  const root = tree();
  const path = join(root, 'docs', 'example.md');
  writeFileSync(path, page);
  expect(parseFile(path)).toBe(parseFile(path));
  writeFileSync(path, page + '\nNew source text.\n');
  expect(llmForFile(path)).toContain('New source text.');
});

test('LLM generation refuses normalized collisions and its reserved index', () => {
  const root = tree();
  writeFileSync(join(root, 'docs', 'example.md'), page);
  writeFileSync(join(root, 'docs', 'example.dh.md'), page);
  expect(() => expectedLlm(join(root, 'docs'))).toThrow('collides');
  rmSync(join(root, 'docs', 'example.dh.md'));
  writeFileSync(join(root, 'docs', 'index.md'), page);
  expect(() => expectedLlm(join(root, 'docs'))).toThrow('collides');
});

test('public export drops lazy and indented security quote continuation and its heading', () => {
  const root = tree();
  const path = join(root, 'docs', 'example.md');
  writeFileSync(
    path,
    page +
      '\n  > **Security finding:** Secret title.\n  >\n  > ### Hidden heading\nSecret continuation.\n\nVisible ending.\n',
  );
  const doc = parseFile(path, true);
  expect(JSON.stringify(publicJson(doc))).not.toContain('Secret');
  expect(JSON.stringify(publicJson(doc))).not.toContain('Hidden heading');
  expect(llmForFile(path, true)).not.toContain('Secret continuation');
  expect(llmForFile(path, true)).toContain('Visible ending');
});

test('directory stamping finds translated pages and skips English sources', () => {
  const root = tree();
  writeFileSync(join(root, 'docs', 'example.md'), page);
  mkdirSync(join(root, 'docs', 'i18n', 'es'), { recursive: true });
  const translation = join(root, 'docs', 'i18n', 'es', 'example.md');
  writeFileSync(
    translation,
    page.replace('type: feature', 'type: feature\ntranslation:\n  lang: es\n  source: ../../example.md'),
  );
  expect(main(['stamp', join(root, 'docs')])).toBe(0);
  expect(readFileSync(translation, 'utf8')).toContain('sourceHashes:');
});

test('context overview is excluded and its intro is present in the LLM index', () => {
  const root = tree();
  const area = join(root, 'docs', 'domain', 'example');
  mkdirSync(area, { recursive: true });
  writeFileSync(join(area, 'index.md'), '# Example\nGenerated.\n');
  writeFileSync(join(area, 'overview.yaml'), 'intro: This context owns the example.\n');
  writeFileSync(join(area, 'example.md'), page);
  const expected = expectedLlm(join(root, 'docs'));
  expect(expected.has(join(root, 'docs-llm', 'domain', 'example', 'index.md'))).toBe(false);
  expect(expected.get(join(root, 'docs-llm', 'index.md'))).toContain('## Example\n\nThis context owns the example.');
});

test('an interleaved reading path links each page once outside context introductions', () => {
  const root = tree();
  for (const context of ['a', 'b']) {
    const dir = join(root, 'docs', 'domain', context);
    mkdirSync(dir, { recursive: true });
    writeFileSync(join(dir, 'overview.yaml'), `intro: Context ${context} owns these examples.\n`);
    for (const name of ['one', 'two']) writeFileSync(join(dir, `${name}.md`), page);
  }
  writeFileSync(
    join(root, 'docs', 'outline.yaml'),
    'readingPath:\n  - domain/a/one.md\n  - domain/b/one.md\n  - domain/a/two.md\n',
  );
  const index = expectedLlm(join(root, 'docs')).get(join(root, 'docs-llm', 'index.md'))!;
  const boundary = index.indexOf('## Pages, in reading order');
  expect(index.slice(0, boundary)).not.toContain('](domain/');
  expect(index.indexOf('](domain/a/one.md)')).toBeLessThan(index.indexOf('](domain/b/one.md)'));
  expect(index.indexOf('](domain/b/one.md)')).toBeLessThan(index.indexOf('](domain/a/two.md)'));
  expect(index.match(/\]\(domain\/a\/one\.md\)/g)).toHaveLength(1);
});
