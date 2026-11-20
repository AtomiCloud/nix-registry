# Documentation tools

`doccheck` (proposal) checks documentation and generates its maps. `dh`
(proposal) writes the Durian Hypermedia LLM view and renderer input. Both
executables use the `durian-hypermedia` parser pinned to tag `v0.4.0`.
`bun.lock` records its tag object, and the Nix dependency hash verifies the
complete dependency tree. The source is fetched from that tag; no working
copy is included here.

Build with `nix build .#doccheck .#dh`. The `doccheck` build runs the unit
tests. Its runtime includes the registry's `ul`, Git and universal-ctags;
`dh` includes Git for loading code at its pins. Word validation belongs to
`ul check`; link validation belongs to lychee.

Run from the repository root:

```sh
doccheck check docs
doccheck gen docs
doccheck gen docs --check
doccheck outline stubs docs
doccheck outline split docs
doccheck adr new . "Describe the decision" --docs docs
dh llm docs
dh json docs/domain/example/model/example.dh.md
dh tokens docs
dh stamp docs/i18n/es/domain/example/model/example.dh.md
```

The one configuration file is `docs/doccheck.yaml` (proposal):

```yaml
pageOrder: A
innerFolder: model
llmView: committed
```

- `pageOrder: A` keeps `rank`, `order`, `learns` and the optional `readingPath`
  position in page front matter. `order` is a positive integer within the
  page's folder. Split and stub creation use gaps of ten, leaving room to
  insert a page. `doccheck gen` writes `outline.yaml` from these fields.
- `pageOrder: B` reads a hand-written `docs/outline.yaml`. `doccheck outline
split` copies its entries into page front matter and selects mode A.
- `innerFolder` names the folder for a context's model pages. Set it to
  `core` or another folder name and move the pages and their references
  together.
- `llmView: committed` checks every LLM view against its source, including
  missing and orphan files. `llmView: build` leaves generation to the site
  build. `dh llm docs` works in either mode.

`doccheck gen --check` writes nothing. It checks the top, domain, guide and
standard maps, each context overview, decision maps and, in mode A, the
outline. Context introductions live in `overview.yaml`, with the single
key `intro`.

`dh llm` mirrors source pages to `docs-llm/` and removes orphan views. Its
`--out` directory must be separate from the source tree. `dh json --src`
reads code from the host's code folder; another repository sits at
`<code-folder>/<repo>`. `--public` removes security findings from exported
LLM views and JSON. `dh tokens` reports estimates using characters divided
by four, rounded up. `dh stamp` records source section hashes in a
translation's front matter.

For local development, run `bun install --frozen-lockfile` and `bun test src`
in this folder. The command entry points are `src/main.ts` and
`src/dh-cli.ts`.
