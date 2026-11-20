import { afterEach, describe, expect, test } from 'bun:test';
import { mkdtempSync, mkdirSync, readFileSync, writeFileSync, rmSync, unlinkSync } from 'node:fs';
import { join, dirname, resolve } from 'node:path';
import { tmpdir } from 'node:os';
import { parse } from 'durian-hypermedia';
import {
  DEFAULTS,
  type Page,
  type Config,
  type Issue,
  type Outline,
  loadPage,
  loadPages,
  yamlText,
  configFor,
  yaml,
} from './model';
import {
  checkFrontMatter,
  checkShape,
  checkRich,
  checkTree,
  checkHistory,
  checkTranslations,
  checkLlm,
  checkChanges,
  historyHits,
  proseLineCount,
  loadUl,
} from './checks';
import { handwritten, fromPages, checkOutline, splitOutline, makeStubs, loadOutline } from './outline';
import { expectedGenerated, generate } from './generate';
import { checkCode, defined, commentsOf } from './code';
import { expectedLlm, sectionHashes } from './dh-library';
import { adrNew, main } from './main';

const roots: string[] = [];
afterEach(() => {
  for (const d of roots.splice(0)) rmSync(d, { recursive: true, force: true });
});
const cfg: Config = { ...DEFAULTS, llmView: 'build' };
function fixture() {
  const root = mkdtempSync(join(tmpdir(), 'doccheck-test-'));
  roots.push(root);
  const docs = join(root, 'docs');
  mkdirSync(docs);
  return { root, docs };
}
function put(file: string, text: string) {
  mkdirSync(dirname(file), { recursive: true });
  writeFileSync(file, text);
}
const fm = {
  title: 'Order',
  concept: 'order',
  description: "An order holds a buyer's items.",
  namespaces: [],
  type: 'feature',
  rank: 'core',
  order: 1,
};
const body =
  "An order holds a buyer's items.\n\n## What problem it solves\n\nA buyer groups the items.\n\n## How it works\n\nThe buyer places an order.\n";
function page(docs: string, extra: Record<string, any> = {}, text = body, rel = 'domain/shop/model/order.md'): Page {
  const file = join(docs, rel);
  put(file, `---\n${yamlText({ ...fm, ...extra })}---\n\n${text}`);
  return loadPage(file, docs);
}
function run(fn: (issues: Issue[]) => void) {
  const issues: Issue[] = [];
  fn(issues);
  return issues;
}
const has = (issues: Issue[], name: string) => issues.some(i => i.check === name && i.level === 'error');
const outline = (pages: Page[]): Outline => ({
  contexts: [
    {
      context: 'shop',
      learns: 'Orders and the items they hold',
      pages: pages.map(p => ({ full: p.rel, type: p.fm.type, rank: p.fm.rank, learns: p.fm.description })),
    },
  ],
  guides: [],
  repo: [],
  readingPath: [],
});
function oldOutline(
  docs: string,
  es: any[] = [{ page: 'model/order.md', type: 'feature', rank: 'core', learns: fm.description }],
) {
  put(
    join(docs, 'outline.yaml'),
    yamlText({ contexts: [{ context: 'shop', learns: 'Orders', pages: es }], guides: [], repo: [], readingPath: [] }),
  );
}
function context(docs: string) {
  put(join(docs, 'domain/shop/overview.yaml'), 'intro: Orders hold the items a buyer wants.\n');
  put(
    join(docs, 'domain/shop/UL.yaml'),
    "namespace: example.org/shop/orders\npremise: Orders\nlanguage:\n  order:\n    desc: A buyer's items.\n",
  );
  put(join(docs, 'domain/shop/index.md'), '# Shop\n');
}

describe('front matter and page rules: clean and refused', () => {
  test('valid front matter passes', () => {
    const { docs } = fixture();
    expect(run(i => checkFrontMatter(page(docs), cfg, i))).toEqual([]);
  });
  for (const [name, extra] of Object.entries({
    unknown: { mispelt: true },
    missing: { title: null },
    multiline: { description: 'two\nlines' },
    long: { description: 'a'.repeat(161) },
    namespaces: { namespaces: 'one' },
    type: { type: 'algorithm' },
    code: { code: ['x.ts:2'] },
    issues: { issues: ['https://example.org'] },
    pin: { pins: { repo: 'abc' } },
    translation: { translation: { lang: 'es', source: 'order.md' } },
    stub: { stub: 'yes' },
    implementation: { type: 'algorithm', code: [] },
  }))
    test(`front matter valid refuses ${name}`, () => {
      const { docs } = fixture();
      const rel = name === 'implementation' ? 'domain/shop/implementation/order.md' : undefined;
      expect(
        has(
          run(i => checkFrontMatter(page(docs, extra, body, rel), cfg, i)),
          'front matter valid',
        ),
      ).toBe(true);
    });
  test('duplicate YAML keys refuse', () => expect(() => yaml('title: one\ntitle: two\n')).toThrow());
  test('paths resolve and refuse missing references', () => {
    const { docs } = fixture();
    page(docs);
    let p = page(docs, { related: ['order.md'] });
    expect(
      has(
        run(i => checkFrontMatter(p, cfg, i)),
        'front matter paths resolve',
      ),
    ).toBe(false);
    p = page(docs, { related: ['missing.md'] });
    expect(
      has(
        run(i => checkFrontMatter(p, cfg, i)),
        'front matter paths resolve',
      ),
    ).toBe(true);
  });
  test('concept name: pass matching; refuse filename and title mismatch', () => {
    const { docs } = fixture();
    expect(
      has(
        run(i => checkShape(page(docs), cfg, i)),
        'page named after its concept',
      ),
    ).toBe(false);
    expect(
      has(
        run(i => checkShape(page(docs, { concept: 'basket' }), cfg, i)),
        'page named after its concept',
      ),
    ).toBe(true);
  });
  test('concept on one page: distinct passes, duplicate refuses', () => {
    const { docs } = fixture();
    const a = page(docs);
    const b = page(docs, { concept: 'basket', title: 'Basket', order: 2 }, body, 'domain/shop/model/basket.md');
    context(docs);
    expect(
      has(
        run(i => checkTree(docs, [a, b], outline([a, b]), cfg, i)),
        'concept on one page',
      ),
    ).toBe(false);
    b.fm.concept = 'order';
    expect(
      has(
        run(i => checkTree(docs, [a, b], outline([a, b]), cfg, i)),
        'concept on one page',
      ),
    ).toBe(true);
  });
  test('doc shape clean passes', () => {
    const { docs } = fixture();
    expect(
      has(
        run(i => checkShape(page(docs), cfg, i)),
        'doc shape',
      ),
    ).toBe(false);
  });
  for (const [name, text, extra] of [
    ['summary', body.replace("An order holds a buyer's items.", 'a\nb\nc\nd'), {}],
    ['missing walk', body.replace('## How it works', '## Other'), {}],
    ['section order', body + '\n## Case: delivery\n\nOne case.\n\n## How it works\n\nWalk.\n', {}],
    ['stub sections', body, { stub: true }],
    ['known bugs', body + '\n## Known bug: loss\n\nThe item is lost.\n', {}],
    ['why source', body + '\n## Why this way\n\nBecause it is nicer.\n', {}],
    ['length', body + '\n' + Array(301).fill('A line of prose.').join('\n'), {}],
  ] as const)
    test(`doc shape refuses ${name}`, () => {
      const { docs } = fixture();
      expect(
        has(
          run(i => checkShape(page(docs, extra, text), cfg, i)),
          'doc shape',
        ),
      ).toBe(true);
    });
  test('fenced code excluded from 300 prose lines', () => {
    const { docs } = fixture();
    const p = page(docs, {}, body + '\n```ts\n' + Array(301).fill('const x = 1;').join('\n') + '\n```\n');
    expect(
      has(
        run(i => checkShape(p, cfg, i)),
        'doc shape',
      ),
    ).toBe(false);
  });
  test('sourced why accepts design note links, Inferred refuses', () => {
    const { docs } = fixture();
    const a = page(
      docs,
      {},
      body + '\n## Why this way\n\nThe [design note](https://example.org/design) explains the reason.\n',
    );
    expect(
      has(
        run(i => checkShape(a, cfg, i)),
        'doc shape',
      ),
    ).toBe(false);
    const b = page(docs, {}, body + '\n## Why this way\n\nInferred: this seems simpler.\n');
    expect(
      has(
        run(i => checkShape(b, cfg, i)),
        'doc shape',
      ),
    ).toBe(true);
  });
  test('inner folder switch accepts core and refuses model when core chosen', () => {
    const { docs } = fixture();
    const a = page(docs, {}, body, 'domain/shop/core/order.md');
    context(docs);
    expect(
      has(
        run(i => checkTree(docs, [a], outline([a]), { ...cfg, innerFolder: 'core' }, i)),
        'doc shape',
      ),
    ).toBe(false);
    expect(
      has(
        run(i => checkTree(docs, [a], outline([a]), cfg, i)),
        'doc shape',
      ),
    ).toBe(true);
  });
  test('page list clean passes; handwritten Pages refuses', () => {
    const { docs } = fixture();
    expect(
      has(
        run(i => checkShape(page(docs), cfg, i)),
        'page list in a page',
      ),
    ).toBe(false);
    expect(
      has(
        run(i => checkShape(page(docs, {}, body + '\n## Pages\n\nA list.\n'), cfg, i)),
        'page list in a page',
      ),
    ).toBe(true);
  });
  test('context files required; guides cannot define words', () => {
    const { docs } = fixture();
    const p = page(docs);
    context(docs);
    expect(
      has(
        run(i => checkTree(docs, [p], outline([p]), cfg, i)),
        'doc shape',
      ),
    ).toBe(false);
    unlinkSync(join(docs, 'domain/shop/overview.yaml'));
    put(join(docs, 'guides/UL.yaml'), '{}');
    expect(
      has(
        run(i => checkTree(docs, [p], outline([p]), cfg, i)),
        'doc shape',
      ),
    ).toBe(true);
  });
});

describe('outline and generators', () => {
  test('mode A uses folder order and rejects missing rank/order', () => {
    const { docs } = fixture();
    const a = page(docs);
    expect(run(i => fromPages(docs, [a], i))).toEqual([]);
    a.fm.rank = 'wrong';
    delete a.fm.order;
    expect(
      has(
        run(i => fromPages(docs, [a], i)),
        'outline in step',
      ),
    ).toBe(true);
  });
  test('mode B matches pages; missing, extra and wrong types refuse', () => {
    const { docs } = fixture();
    const a = page(docs);
    oldOutline(docs);
    const o = handwritten(docs);
    expect(run(i => checkOutline(docs, [a], o, i))).toEqual([]);
    expect(
      has(
        run(i => checkOutline(docs, [], o, i)),
        'outline in step',
      ),
    ).toBe(true);
    a.fm.type = 'format';
    expect(
      has(
        run(i => checkOutline(docs, [a], o, i)),
        'outline in step',
      ),
    ).toBe(true);
    expect(
      has(
        run(i => checkOutline(docs, [a], { contexts: [], guides: [], repo: [], readingPath: [] }, i)),
        'outline in step',
      ),
    ).toBe(true);
  });
  for (const bad of [
    'extra: true\n',
    'contexts: wrong\n',
    'contexts: []\nreadingPath: [missing.md]\n',
    'contexts:\n- context: shop\n  pages:\n  - page: ../escape.md\n    type: feature\n    rank: core\n    learns: a\n',
  ])
    test('malformed outline refuses ' + bad.slice(0, 24), () => {
      const { docs } = fixture();
      put(join(docs, 'outline.yaml'), bad);
      expect(() => handwritten(docs)).toThrow();
    });
  test('split preserves selected frontmatter bytes and both order modes', () => {
    const { docs } = fixture();
    const p = page(docs);
    delete p.fm.rank;
    delete p.fm.order;
    put(
      p.file,
      `---\ntitle: Order\nconcept: order\ndescription: ${fm.description}\nnamespaces: []\ntype: feature\n---\n\n${body}`,
    );
    oldOutline(docs);
    const before = readFileSync(p.file, 'utf8').split('\n---')[0];
    expect(splitOutline(docs, loadPages(docs))).toBe(1);
    expect(readFileSync(p.file, 'utf8').startsWith(before)).toBe(true);
    const a = loadPages(docs);
    expect(a[0].fm.order).toBe(1);
    expect(configFor(docs).pageOrder).toBe('A');
    expect(run(i => checkOutline(docs, a, fromPages(docs, a, i), i))).toEqual([]);
  });
  test('stubs never overwrite a page', () => {
    const { docs } = fixture();
    oldOutline(docs);
    const o = handwritten(docs);
    expect(makeStubs(docs, o, { ...cfg, pageOrder: 'B' })).toBe(1);
    const file = join(docs, 'domain/shop/model/order.md');
    expect(loadPage(file, docs).fm.stub).toBe(true);
    const before = readFileSync(file, 'utf8');
    expect(makeStubs(docs, o, cfg)).toBe(0);
    expect(readFileSync(file, 'utf8')).toBe(before);
  });
  test('generated files pass after generation; edits, missing and orphan maps refuse', () => {
    const { docs } = fixture();
    const a = page(docs);
    context(docs);
    const o = outline([a]);
    const issues: Issue[] = [];
    generate(docs, [a], o, cfg, false, issues);
    expect(issues).toEqual([]);
    expect(run(i => generate(docs, loadPages(docs), o, cfg, true, i))).toEqual([]);
    const index = readFileSync(join(docs, 'domain/shop/index.md'), 'utf8');
    expect(index.startsWith('# Shop')).toBe(true);
    expect(index).not.toContain('concept:');
    unlinkSync(join(docs, 'domain/README.md'));
    expect(
      has(
        run(i => generate(docs, loadPages(docs), o, cfg, true, i)),
        'generated files',
      ),
    ).toBe(true);
    put(join(docs, 'domain/shop/README.md'), 'wrong');
    expect(
      has(
        run(i => generate(docs, loadPages(docs), o, cfg, true, i)),
        'generated files',
      ),
    ).toBe(true);
  });
  test('overview intro prose passes; link refuses', () => {
    const { docs } = fixture();
    const p = page(docs);
    context(docs);
    expect(run(i => expectedGenerated(docs, [p], outline([p]), cfg, i))).toEqual([]);
    put(join(docs, 'domain/shop/overview.yaml'), 'intro: "Read [order](model/order.md)."\n');
    expect(
      has(
        run(i => expectedGenerated(docs, [p], outline([p]), cfg, i)),
        'page list in a page',
      ),
    ).toBe(true);
  });
  test('overview schema and links checked without running gen', () => {
    const { docs } = fixture();
    const p = page(docs);
    context(docs);
    expect(
      has(
        run(i => checkTree(docs, [p], outline([p]), cfg, i)),
        'doc shape',
      ),
    ).toBe(false);
    put(join(docs, 'domain/shop/overview.yaml'), 'intro: Orders.\nextra: wrong\n');
    expect(
      has(
        run(i => checkTree(docs, [p], outline([p]), cfg, i)),
        'doc shape',
      ),
    ).toBe(true);
    put(join(docs, 'domain/shop/overview.yaml'), 'intro: "[Orders](model/order.md)"\n');
    expect(
      has(
        run(i => checkTree(docs, [p], outline([p]), cfg, i)),
        'page list in a page',
      ),
    ).toBe(true);
  });
  test('config defaults and each switch', () => {
    const { docs } = fixture();
    expect(configFor(docs)).toEqual(DEFAULTS);
    put(join(docs, 'doccheck.yaml'), 'pageOrder: B\ninnerFolder: core\nllmView: build\n');
    expect(configFor(docs)).toEqual({ pageOrder: 'B', innerFolder: 'core', llmView: 'build' });
    put(join(docs, 'doccheck.yaml'), 'pageOder: A\n');
    expect(() => configFor(docs)).toThrow();
  });
});

describe('pinned format checks: passing and refused', () => {
  function rich(text: string, ext = 'dh.md') {
    const { docs } = fixture();
    return page(docs, {}, 'An order.\n\n## How it works\n\n' + text, `order.${ext}`);
  }
  const terminal = '```terminal\n% echo hi\nhi\n```\n';
  const pairs: [string, string, string, string?][] = [
    ['docs parse', terminal, '```terminal\n% echo hi\n'],
    ['parts known', terminal, ':::unknown\nhello\n:::\n'],
    ['LLM text present', terminal, '```terminal\n' + 'one '.repeat(1700) + '\n```\n'],
    ['fence partner', ':::human llm=none\nA human walk.\n:::\n', ':::human\nA human walk.\n:::\n'],
    ['LLM text placed', terminal + ':::llm\nA short command.\n:::\n', ':::llm\nOrphan text.\n:::\n'],
    ['part data', terminal, '```terminal\n@step One\n% echo hi\n```\n'],
    ['part data', '```filetree\nsrc/\n  main.ts (new)\n```\n', '```filetree\nsrc/\n  main.ts (mystery)\n```\n'],
    [
      'part data',
      '```animation\nbox a "A"\nbox b "B"\n@step One\n@step Two\n```\n',
      '```animation\nunknown a "A"\n```\n',
    ],
    [
      'part data',
      '```chart\nkind: bar\nx: [A, B]\nseries:\n- label: Items\n  data: [1, 2]\n```\n',
      '```chart\nkind: wrong\nseries: []\n```\n',
    ],
    [
      'security blocks',
      terminal + '> **Security finding:** A finding.\n',
      terminal + '**Security finding:** outside a quote.\n',
    ],
    ['watch-out blocks', terminal + '> **Watch out:** One point.\n', terminal + '> **Watch out:**\n'],
    [
      'code walk ranges',
      '```codewalk\n@file a.ts\nconst a = 1;\nconst b = 2;\n@step a.ts#L1\nOne.\n@step a.ts#L2\nTwo.\n```\n',
      '```codewalk\n@file a.ts\nconst a = 1;\n@step a.ts#L1-L99\nOne.\n@step a.ts#L1\nTwo.\n```\n',
    ],
  ];
  pairs.forEach(([name, good, bad], i) =>
    test(`${name} pair ${i + 1}`, () => {
      const a = run(x => checkRich(rich(good), x));
      expect(a.filter(x => x.check === name && x.level === 'error')).toEqual([]);
      const b = run(x => checkRich(rich(bad), x));
      expect(has(b, name)).toBe(true);
    }),
  );
  test('ordinary Markdown refuses rich parts', () =>
    expect(
      has(
        run(i => checkRich(rich(terminal, 'md'), i)),
        'parts known',
      ),
    ).toBe(true));
  test('empty dh refuses', () =>
    expect(
      has(
        run(i => checkRich(rich('ordinary prose'), i)),
        'parts known',
      ),
    ).toBe(true));
});

describe('history and UL integration', () => {
  for (const [good, bad] of [
    ['The previous row.', 'Previously the row was empty.'],
    ['The value is true now.', 'It now uses a flag.'],
    ['After 30 seconds it ends.', 'After v2 it ends.'],
    ['A migration runs.', '## Migration'],
    ['A permanent hold.', 'A temporary hold.'],
  ])
    test(`history pass/refuse ${bad}`, () => {
      expect(historyHits(good, [])).toEqual([]);
      expect(historyHits(bad, []).length).toBeGreaterThan(0);
    });
  test('UL exemption uses word and aliases', () => {
    expect(historyHits('A temporary hold.', [{ word: 'temporary hold' }])).toEqual([]);
    expect(historyHits('A temporary hold.', [{ word: 'hold', aliases: ['temporary hold'] }])).toEqual([]);
    expect(historyHits('A temporary hold.', [{ word: 'permanent hold' }]).length).toBe(1);
  });
  test('history skips code, stubs and ADR; headings and strike refuse', () => {
    const { docs } = fixture();
    const ul = { schema: 'ul/v1', words: [] };
    const p = page(docs, {}, body + '\n`previously`\n\n```ts\n// formerly\n```\n\n### `history`\n');
    expect(run(i => checkHistory(docs, [p], ul, i))).toEqual([]);
    const bad = page(docs, {}, body + '\n## History\n\n~~Old text~~\n');
    expect(
      has(
        run(i => checkHistory(docs, [bad], ul, i)),
        'history phrases',
      ),
    ).toBe(true);
    bad.fm.stub = true;
    expect(run(i => checkHistory(docs, [bad], ul, i))).toEqual([]);
  });
  test('UL active descriptions scanned, retired skipped', () => {
    const { docs } = fixture();
    expect(
      run(i =>
        checkHistory(docs, [], { schema: 'ul/v1', words: [{ word: 'order', desc: 'An order', status: 'active' }] }, i),
      ),
    ).toEqual([]);
    expect(
      has(
        run(i =>
          checkHistory(
            docs,
            [],
            { schema: 'ul/v1', words: [{ word: 'order', desc: 'Previously a row', status: 'active' }] },
            i,
          ),
        ),
        'history phrases',
      ),
    ).toBe(true);
    expect(
      run(i =>
        checkHistory(
          docs,
          [],
          { schema: 'ul/v1', words: [{ word: 'order', desc: 'Previously a row', status: 'retired' }] },
          i,
        ),
      ),
    ).toEqual([]);
  });
  test('overview intro scanned', () => {
    const { docs } = fixture();
    context(docs);
    expect(run(i => checkHistory(docs, [], { schema: 'ul/v1', words: [] }, i))).toEqual([]);
    put(join(docs, 'domain/shop/overview.yaml'), 'intro: Previously orders were rows.\n');
    expect(
      has(
        run(i => checkHistory(docs, [], { schema: 'ul/v1', words: [] }, i)),
        'history phrases',
      ),
    ).toBe(true);
  });
  test('UL all machine contract', () => {
    const { root, docs } = fixture();
    context(docs);
    const ul = loadUl(root);
    expect(ul.schema).toBe('ul/v1');
    expect(ul.words.some(w => w.word === 'order')).toBe(true);
  });
});

describe('code references and comments', () => {
  test('a refused moved symbol names its destination file', () => {
    const { root, docs } = fixture();
    const p = page(docs, { code: ['shop/src/old.ts#total'] });
    put(join(root, 'src/new.ts'), 'export function total() {}\n');
    const issues = run(i => checkCode(docs, [p], { ...outline([p]), repoName: 'shop' }, i));
    expect(issues.some(issue => issue.check === 'code references' && issue.message.includes('src/new.ts'))).toBe(true);
  });
  test('symbol declarations and anchors pass; mentions refuse', () => {
    expect(defined('export function total() { return 1; }', 'total')).toBe(true);
    expect(defined('// doc-anchor: durable\nconst total = 1;', 'durable')).toBe(true);
    expect(defined('const message = "total";', 'total')).toBe(false);
  });
  test('anchor must be unique', () => {
    expect(defined('// doc-anchor: stable\nconst x=1;', 'stable')).toBe(true);
    expect(() => defined('// doc-anchor: stable\nconst x=1;\n// doc-anchor: stable\n', 'stable')).toThrow('used twice');
  });
  test('ctags receiver suffix accepts right receiver only', () => {
    const code = 'package demo\ntype Executor struct {}\nfunc (e *Executor) Run() {}\n';
    expect(defined(code, 'Executor.Run', 'executor.go')).toBe(true);
    expect(defined(code, 'Other.Run', 'executor.go')).toBe(false);
  });
  test('comments ignore strings', () => {
    expect(commentsOf('const x = "repo/docs/missing.md"; // repo/docs/order.md', 'main.ts')).toContain(
      'repo/docs/order.md',
    );
    expect(commentsOf('const x = "repo/docs/missing.md"; // repo/docs/order.md', 'main.ts')).not.toContain('missing');
  });
  test('Rust lifetime does not hide following backlinks', () => {
    expect(commentsOf("fn value<'a>(x: &'a str) {}\n// shop/docs/order.md\n", 'a.rs')).toContain('shop/docs/order.md');
    expect(commentsOf("let c = 'x';\n// shop/docs/order.md\n", 'a.rs')).toContain('shop/docs/order.md');
  });
  test('code reference file and definition pass; missing refuses', () => {
    const { root, docs } = fixture();
    const p = page(docs, { code: ['shop/src/main.ts#total'] });
    const o = { ...outline([p]), repoName: 'shop' };
    put(join(root, 'src/main.ts'), '// shop/docs/domain/shop/model/order.md\nexport function total() {}\n');
    expect(
      has(
        run(i => checkCode(docs, [p], o, i)),
        'code references',
      ),
    ).toBe(false);
    p.fm.code = ['shop/src/main.ts#missing'];
    expect(
      has(
        run(i => checkCode(docs, [p], o, i)),
        'code references',
      ),
    ).toBe(true);
  });
  test('external refs require a full pin', () => {
    const { docs } = fixture();
    const p = page(docs, { code: ['outside/src/a.ts#A'], pins: { outside: 'a'.repeat(40) } });
    expect(
      has(
        run(i => checkCode(docs, [p], outline([p]), i)),
        'code references',
      ),
    ).toBe(false);
    delete p.fm.pins;
    expect(
      has(
        run(i => checkCode(docs, [p], outline([p]), i)),
        'code references',
      ),
    ).toBe(true);
  });
  test('line numbers outside code fences refused', () => {
    const { docs } = fixture();
    const a = page(docs, {}, body + '\n```ts\n// a.ts:2\n```\n');
    expect(
      has(
        run(i => checkCode(docs, [a], outline([a]), i)),
        'line numbers in prose',
      ),
    ).toBe(false);
    const b = page(docs, {}, body + '\nRead `a.ts:2`.\n');
    expect(
      has(
        run(i => checkCode(docs, [b], outline([b]), i)),
        'line numbers in prose',
      ),
    ).toBe(true);
  });
  test('code links back required for commentable files', () => {
    const { root, docs } = fixture();
    const p = page(docs, { code: ['shop/src/a.ts'] });
    const o = { ...outline([p]), repoName: 'shop' };
    put(join(root, 'src/a.ts'), '// shop/docs/domain/shop/model/order.md\nconst a = 1;\n');
    expect(
      has(
        run(i => checkCode(docs, [p], o, i)),
        'code links back',
      ),
    ).toBe(false);
    put(join(root, 'src/a.ts'), 'const a = 1;\n');
    expect(
      has(
        run(i => checkCode(docs, [p], o, i)),
        'code links back',
      ),
    ).toBe(true);
  });
  test('code comment doc links resolve', () => {
    const { root, docs } = fixture();
    const p = page(docs);
    const o = { ...outline([p]), repoName: 'shop' };
    put(join(root, 'src/a.ts'), '// shop/docs/domain/shop/model/order.md\n');
    expect(
      has(
        run(i => checkCode(docs, [p], o, i)),
        'code links resolve',
      ),
    ).toBe(false);
    put(join(root, 'src/a.ts'), '// shop/docs/missing.md\n');
    expect(
      has(
        run(i => checkCode(docs, [p], o, i)),
        'code links resolve',
      ),
    ).toBe(true);
  });
  test('codewalk pin behind warns only when cited lines drift', () => {
    const { docs } = fixture();
    const pin = 'a'.repeat(40);
    const p = page(
      docs,
      { pins: { shop: pin } },
      body + '\n```codewalk repo=shop\n@step shop/src/a.ts#L1\nOne.\n@step shop/src/a.ts#L1\nTwo.\n```\n',
      'domain/shop/model/order.dh.md',
    );
    const o = { ...outline([p]), repoName: 'shop' };
    const read = (current: string) => (_root: string, args: string[]) =>
      args[0] === 'show' ? (args[1] === `${pin}:src/a.ts` ? 'old\n' : args[1] === ':src/a.ts' ? current : null) : null;
    expect(
      run(i => checkCode(docs, [p], o, i, undefined, read('old\n'))).some(i => i.check === 'code walk pin behind'),
    ).toBe(false);
    expect(
      run(i => checkCode(docs, [p], o, i, undefined, read('new\n'))).some(
        i => i.check === 'code walk pin behind' && i.level === 'warn',
      ),
    ).toBe(true);
  });
  test('staged pin check cannot be bypassed by unstaged walk removal or new note', () => {
    const { docs } = fixture();
    const oldPin = 'a'.repeat(40),
      newPin = 'b'.repeat(40);
    const base = page(docs);
    const make = (pin: string, note = 'One.') =>
      `---\n${yamlText({ ...fm, pins: { shop: pin } })}---\n\n${body}\n\`\`\`codewalk repo=shop\n@step shop/src/a.ts#L1\n${note}\n@step shop/src/a.ts#L1\nTwo.\n\`\`\`\n`;
    const old = make(oldPin),
      staged = make(newPin);
    const read = (newText: string) => (_root: string, args: string[]) =>
      args[0] === 'diff'
        ? `docs/${base.rel}\n`
        : args[0] === 'show'
          ? args[1] === `HEAD:docs/${base.rel}`
            ? old
            : args[1] === `:docs/${base.rel}`
              ? newText
              : args[1] === `${oldPin}:src/a.ts`
                ? 'before\n'
                : args[1] === `${newPin}:src/a.ts`
                  ? 'after\n'
                  : args[1] === ':src/a.ts'
                    ? 'after\n'
                    : null
          : null;
    const o = { ...outline([base]), repoName: 'shop' };
    expect(
      has(
        run(i => checkCode(docs, [base], o, i, undefined, read(staged))),
        'code walk ranges',
      ),
    ).toBe(true);
    expect(
      has(
        run(i =>
          checkCode(
            docs,
            [base],
            o,
            i,
            undefined,
            read(make(newPin, 'Updated note.').replace('Two.', 'Updated second note.')),
          ),
        ),
        'code walk ranges',
      ),
    ).toBe(false);
  });
});

describe('translation and LLM freshness', () => {
  test('translation keeps the full made-up codewalk file, including lines outside a step', () => {
    const { docs } = fixture();
    const text =
      body +
      '\n```codewalk\n@file demo.ts\nconst one = 1;\nconst two = 2;\n@step demo.ts#L1\nOne.\n@step demo.ts#L1\nAgain.\n```\n';
    const source = page(docs, {}, text, 'domain/shop/model/order.dh.md');
    const translated = page(
      docs,
      {
        translation: {
          lang: 'es',
          source: '../../../../domain/shop/model/order.dh.md',
          sourceHashes: sectionHashes(source.body),
        },
      },
      text,
      'i18n/es/domain/shop/order.dh.md',
    );
    expect(
      has(
        run(i => checkTranslations(docs, [source, translated], i)),
        'translation shape',
      ),
    ).toBe(false);
    const changed = page(
      docs,
      { translation: translated.fm.translation },
      text.replace('const two = 2;', 'const two = 99;'),
      translated.rel,
    );
    expect(
      has(
        run(i => checkTranslations(docs, [source, changed], i)),
        'translation shape',
      ),
    ).toBe(true);
  });
  test('translation hashes pass; changed section refuses with name', () => {
    const { docs } = fixture();
    const a = page(docs);
    const b = page(
      docs,
      {
        translation: { lang: 'es', source: '../../../domain/shop/model/order.md', sourceHashes: sectionHashes(a.body) },
      },
      a.body,
      'i18n/es/domain/order.md',
    ); // source is relative to translation
    b.fm.translation.source = '../../../domain/shop/model/order.md';
    const good = run(i => checkTranslations(docs, [a, b], i));
    expect(has(good, 'translation behind')).toBe(false);
    b.fm.translation.sourceHashes[1] = '0000000000';
    expect(
      has(
        run(i => checkTranslations(docs, [a, b], i)),
        'translation behind',
      ),
    ).toBe(true);
  });
  test('translation shape pass; inline code and codeblocks refuse', () => {
    const { docs } = fixture();
    const a = page(docs, {}, body + '\n`Order`\n\n```ts\nconst a = 1;\n```\n');
    let b = page(
      docs,
      {
        translation: { lang: 'es', source: '../../../domain/shop/model/order.md', sourceHashes: sectionHashes(a.body) },
      },
      a.body,
      'i18n/es/domain/order.md',
    );
    expect(
      has(
        run(i => checkTranslations(docs, [a, b], i)),
        'translation shape',
      ),
    ).toBe(false);
    b = page(docs, { translation: b.fm.translation }, a.body.replace('const a = 1;', 'const a = 2;'), b.rel);
    expect(
      has(
        run(i => checkTranslations(docs, [a, b], i)),
        'translation shape',
      ),
    ).toBe(true);
  });
  test('translated headings pass; part ids and terminal commands remain fixed', () => {
    const { docs } = fixture();
    const source = body + '\n```terminal #command\n% echo order\norder\n```\n';
    const a = page(docs, {}, source, 'domain/shop/model/order.dh.md');
    const tr = { lang: 'es', source: '../../../domain/shop/model/order.dh.md', sourceHashes: sectionHashes(a.body) };
    let b = page(
      docs,
      { translation: tr },
      source.replace('What problem it solves', 'El problema').replace('How it works', 'Cómo funciona'),
      'i18n/es/domain/order.dh.md',
    );
    expect(
      has(
        run(i => checkShape(b, cfg, i)),
        'doc shape',
      ),
    ).toBe(false);
    expect(
      has(
        run(i => checkTranslations(docs, [a, b], i)),
        'translation shape',
      ),
    ).toBe(false);
    b = page(docs, { translation: tr }, source.replace('#command', '#other'), b.rel);
    expect(
      has(
        run(i => checkTranslations(docs, [a, b], i)),
        'translation shape',
      ),
    ).toBe(true);
    b = page(docs, { translation: tr }, source.replace('echo order', 'echo changed'), b.rel);
    expect(
      has(
        run(i => checkTranslations(docs, [a, b], i)),
        'translation shape',
      ),
    ).toBe(true);
  });
  test('LLM committed pass and refuse drift/missing/orphan; build mode skips', () => {
    const { docs } = fixture();
    page(docs);
    context(docs);
    for (const [f, t] of expectedLlm(docs)) put(f, t);
    expect(run(i => checkLlm(docs, { ...cfg, llmView: 'committed' }, i))).toEqual([]);
    put(join(dirname(docs), 'docs-llm/orphan.md'), 'orphan');
    expect(
      has(
        run(i => checkLlm(docs, { ...cfg, llmView: 'committed' }, i)),
        'LLM view in step',
      ),
    ).toBe(true);
    expect(run(i => checkLlm(docs, cfg, i))).toEqual([]);
  });
});

describe('ADR and staged change checks', () => {
  test('ADR new creates required shape; malformed and skipped number refuse', () => {
    const { docs } = fixture();
    const f = adrNew(docs, '.', 'Keep orders together');
    const p = loadPage(f, docs);
    expect(run(i => checkFrontMatter(p, cfg, i))).toEqual([]);
    expect(adrNew(docs, '.', 'Keep items')).toContain('0002-');
    p.file = p.file.replace('0001-', '0003-');
    expect(
      has(
        run(i => checkTree(docs, [p], outline([]), cfg, i)),
        'ADR shape',
      ),
    ).toBe(true);
    p.doc.headings = [];
    expect(
      has(
        run(i => checkFrontMatter(p, cfg, i)),
        'ADR shape',
      ),
    ).toBe(true);
  });
  const mock = (changes: string, before: string, after: string) => (_root: string, args: string[]) =>
    args[0] === 'diff' ? changes : args[1]?.startsWith('HEAD:') ? before : after;
  test('ADR unchanged allows whitespace; edited/deleted refuses', () => {
    const { docs } = fixture();
    const changes = 'M\0docs/adr/0001-order.md\0';
    expect(run(i => checkChanges(docs, [], i, mock(changes, 'old text', 'old\n text')))).toEqual([]);
    expect(
      has(
        run(i => checkChanges(docs, [], i, mock(changes, 'old text', 'new text'))),
        'ADR unchanged',
      ),
    ).toBe(true);
  });
  test('standards unchanged passes; direct staged edit refuses', () => {
    const { docs } = fixture();
    expect(run(i => checkChanges(docs, [], i, mock('', '', '')))).toEqual([]);
    expect(
      has(
        run(i => checkChanges(docs, [], i, mock('M\0docs/standards/orders/index.md\0', '', ''))),
        'engineering standards untouched',
      ),
    ).toBe(true);
  });
  test('standards state needs semantic change and managed entry; source repo exempt', () => {
    const { docs } = fixture();
    const path = 'docs/standards/orders/index.md';
    const before = 'layer:\n  files: [docs/standards/orders/index.md]\n  revision: 1\n';
    const reader =
      (state: string | null, include = true, original: string | null = before) =>
      (_root: string, args: string[]) =>
        args[0] === 'diff'
          ? `M\0${path}\0${include ? 'M\0.cyan_state.yaml\0' : ''}`
          : args[1] === 'HEAD:.cyan_state.yaml'
            ? original
            : args[1] === ':.cyan_state.yaml'
              ? state
              : null;
    expect(
      has(
        run(i => checkChanges(docs, [], i, reader(before + '\n'))),
        'engineering standards untouched',
      ),
    ).toBe(true);
    expect(
      has(
        run(i => checkChanges(docs, [], i, reader('layer:\n  files: []\n  revision: 2\n'))),
        'engineering standards untouched',
      ),
    ).toBe(true);
    expect(
      has(
        run(i => checkChanges(docs, [], i, reader(before.replace('revision: 1', 'revision: 2')))),
        'engineering standards untouched',
      ),
    ).toBe(false);
    expect(
      has(
        run(i => checkChanges(docs, [], i, reader(null, false, null))),
        'engineering standards untouched',
      ),
    ).toBe(false);
  });
  test('outline changed warns and names ranks; unchanged passes', () => {
    const { docs } = fixture();
    expect(run(i => checkChanges(docs, [], i, mock('', '', '')))).toEqual([]);
    const issues = run(i =>
      checkChanges(
        docs,
        [],
        i,
        mock('M\0docs/outline.yaml\0', 'repo: []\n', 'repo:\n- page: architecture/order.md\n  rank: core\n'),
      ),
    );
    expect(issues[0].check).toBe('outline changed');
    expect(issues[0].level).toBe('warn');
    expect(issues[0].message).toContain('(core)');
  });
  test('running example users warns on change only', () => {
    const { docs } = fixture();
    const p = page(docs, {}, body + '\n[Example](../../../guides/running-example.md)\n');
    expect(run(i => checkChanges(docs, [p], i, mock('', '', '')))).toEqual([]);
    const issues = run(i => checkChanges(docs, [p], i, mock('M\0docs/guides/running-example.md\0', '', '')));
    expect(issues[0].check).toBe('running example users');
    expect(issues[0].message).toContain(p.rel);
  });
});

describe('CLI', () => {
  test('version and help succeed; unknown option and command refuse', () => {
    expect(main(['--version'])).toBe(0);
    expect(main(['--help'])).toBe(0);
    expect(main(['bad'])).toBe(2);
    expect(main(['check', '--bad'])).toBe(2);
  });
  test('gen --check refuses before generation and passes after', () => {
    const { docs } = fixture();
    page(docs);
    context(docs);
    expect(main(['gen', docs, '--check'])).toBe(1);
    expect(main(['gen', docs])).toBe(0);
    expect(main(['gen', docs, '--check'])).toBe(0);
  });
});
