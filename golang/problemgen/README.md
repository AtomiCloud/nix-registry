<!-- Copied from the problemgen spike SPEC.md; the format reference for problemgen 0.1.0. -->
# Problem definitions — format v1 (`problemgen`)

Status: spike spec, 2026-11-13. Tool: `problemgen` 0.1.0 (Go, one static binary; `diff --git` runs git).
Machine-readable structure: `tool/schema/problems.schema.json` (`problemgen schema`).
Every rule below names its enforcer: `lint`, `diff`, `verify` (shipped in the tool), or
the compiler / a per-language lint / an architecture rule (configured in each service; the
tool does not ship those). A file holds exactly one YAML document (P001); `status` is a
plain three-digit decimal integer (P007: `4.22e2`, `0x1a6` are refused). Free text (titles,
descriptions, deprecation notes, templates, string examples) may hold any Unicode except
characters some target reads as a line break or that are invisible controls: C0/C1 controls,
U+2028/2029, U+FEFF, bidi embeddings/overrides/isolates (U+202A–202E, U+2066–2069),
noncharacters (L242). The plain marks LRM, RLM and ALM (U+200E, U+200F, U+061C) are allowed.
Tabs and newlines are refused by the schema's text pattern (P006), so every text is one line:
no YAML folded (`>`) or literal (`|`) blocks. A private field's example, description and
deprecation note (in problem data, and in a named type's fields, where `private` is refused
later by L204) are kept out of diagnostics at every stage: a diagnostic located at such a value
gets a generic message; elsewhere in the same file, whole tokens equal to a value of at least
3 characters are replaced. Values of 1–2 characters are not redacted.

## 1. The rule this format serves

A **Problem is an API-layer value**: an RFC 9457 response body (`type`, `title`, `status`,
`detail`, `instance`, `data`). Status codes and type addresses are HTTP concerns.

- The **domain** returns its own hand-written errors (Result-style; no status codes, no
  addresses, no generated imports).
- The **API layer** maps each domain error to a generated Problem value, at one seam.
- No generated type is an exception/`Error`/`error`, and none is thrown.

The generator writes, for the API layer only: typed Problem values (data + constructors),
the serialiser, the parser, and the closed set the seam maps into.

## 2. Files

| File | One per | Holds |
|---|---|---|
| `problems/<module>.<version>.problems.yaml` (`kind: ProblemSet`) | (module, version) of a service | the problems of that module version |
| `<name>.problems.yaml` (`kind: ProblemLibrary`) | shared set | definitions imported by sets (`builtin:generic/v1` ships inside the tool) |
| `problemgen.yaml` (`kind: GeneratorConfig`) | repository | platform, service, output paths per module, optional `portal` (record default only) |

The definition is language-neutral and landscape-neutral. Only `problemgen.yaml` knows
platform/service; only the running service knows the error portal origin and the landscape (§7).

## 3. Definition format

```yaml
apiVersion: problems.atomi.cloud/v1
kind: ProblemSet
module: notes            # DNS label; the {module} segment of the type address
version: v1              # ^v[0-9]+$; a new version is a NEW FILE (old one stays while served)
imports:
  - builtin:generic/v1                       # all twelve generic problems (§10)
  # - { from: ./shared.problems.yaml, only: [rate_limited] }
types:                   # named enums and objects (nested objects/enums must be named)
  NoteState:
    enum: [draft, published, archived]       # snake_case values, frozen within a version
  Ownership:
    fields:
      ownerId: { type: string, format: uuid }
      since:   { type: date-time }
problems:
  note_locked:                               # id: snake_case, <= 63 chars
    title: Note Locked                       # fixed text, never templated, no braces
    status: 423                              # 400..599
    recoverable: true                        # required, no default
    description: The note is being edited by someone else.   # docs only (portal + schema)
    detail: "Note '{noteId}' is locked by another editor until {until}."   # -> RFC 9457 detail
    log: "lock held on {noteId} by {holder} (session {sessionId}) until {until}"  # -> log only
    retryAfter: retryInSeconds               # optional: Retry-After header from this field
    deprecated: "use note_conflict"          # optional: presence = deprecated (a reason is required)
    data:
      noteId:         { type: string, format: uuid, example: "0190c2a8-7d8e-7b6a-9c1d-2f3e4a5b6c7d" }
      until:          { type: date-time }
      state:          { type: NoteState }
      owner:          { type: Ownership, optional: true }
      retryInSeconds: { type: integer }
      holder:         { type: string, private: true }     # log only: never on the wire, never in the record
      sessionId:      { type: string, private: true }
```

Field keys: `type` (required), `items` (arrays), `format` (strings: `uuid`, `date`, `uri`),
`optional`, `private`, `description`, `example` (scalars; type-checked by lint), `deprecated`.

## 4. Templates (`detail`, `log`)

```
template    = *( literal / "{{" / "}}" / placeholder )
placeholder = "{" name [ "|count" ] [ "|default:" text ] "}"
```

- `name` must be a declared data field of the same problem (L211). Literal braces: `{{` `}}`;
  a lone `}` is an error (L210).
- `detail` may use only **public** fields (L212). `log` may use any field; every private
  field must appear in `log` (L217: no dead private data). `log` defaults to `detail`.
- Placeholders render scalars only. An array needs `|count` (renders its length) (L213/L214).
  Objects cannot be rendered (L213).
- An **optional** field needs `|default:text`, rendered when absent (L215); a required field
  must not have one (L216). There is no `null` (absent = absent).
- Value formatting is fixed, never locale-dependent: strings as-is; integers/int64 in decimal;
  numbers in ECMAScript form (§6); booleans `true`/`false`; date-time canonical (§6); enums
  their wire value.
- **detail**: values are inserted verbatim (invalid UTF-8 / lone surrogates → U+FFFD). JSON
  escaping protects the wire.
- **log**: inserted values additionally escape `\` and U+0000–U+001F, U+007F, U+2028, U+2029
  (`\n`, `\t`, `\u001b` …) so a value can never forge a log line. Literal template text is
  plain text without control characters (schema).
- No other formatting (no number formats, date formats, plurals, conditionals). Deliberate:
  every extra feature must be implemented byte-identically four times. Localised, formatted
  messages are a client concern (§12).

## 5. Field types

| Type | Wire | Go | TypeScript | C# | Dart |
|---|---|---|---|---|---|
| `string` (+`format`) | string | `string` | `string` | `string` | `String` |
| `integer` | number, within ±(2^53−1) | `int64` | `number` | `long` | `int` |
| `int64` | **decimal string** | `int64` | `bigint` | `long` | `int` |
| `number` | finite number | `float64` | `number` | `double` | `double` |
| `boolean` | `true`/`false` | `bool` | `boolean` | `bool` | `bool` |
| `date-time` | string, UTC, 3 fraction digits, `Z` | `time.Time` | `Date` | `DateTimeOffset` | `DateTime` |
| enum (named) | snake_case string | named string + consts | string-literal union | enum + wire map | enum + wire value |
| object (named) | object | struct + constructor | type + factory | sealed record | final class |
| `array` (`items`: any non-array) | array | `[]T` | `readonly T[]` | `IReadOnlyList<T>` | `List<T>` |
| `optional: true` | member omitted | `*T` | `T \| undefined` | `T?` | `T?` |

Deliberately **excluded** (they do not map cleanly to all four, or cannot be byte-identical):
`oneOf/anyOf/allOf`, maps/dictionaries, `null` distinct from absent, arrays of arrays,
recursive types (L202), decimals (use `string` + document, per A114 money-as-string),
binary, validation constraints (`minimum`, `pattern`, `maxLength`), inline (unnamed) nested
objects/enums. Formats (`uuid`, `date`, `uri`) are documentation + schema; they are not
validated at run time (lint validates examples).

## 6. Wire format (canonical JSON — byte-identical in every language)

- Member order: `type`, `title`, `status`, `detail`, `instance` (only when set), `data`.
- `data` is present **iff the problem has at least one public field**; it is then always an
  object (`{}` when every field is absent). A problem with no public data omits `data`
  (lib-contracts §2.2 / A127; never `null`).
- Inside `data` and named objects: declaration order; absent optionals omitted; never `null`.
- Strings: escape `"` `\` and U+0000–U+001F (`\b \t \n \f \r`, else `\u00xx` lowercase);
  everything else literal UTF-8 (including `<>&`, U+2028/9, non-ASCII). Invalid UTF-8
  (Go: per invalid byte) and lone UTF-16 surrogates become U+FFFD.
- `integer`: decimal. Outside ±(2^53−1): the serialiser **refuses** (programming error).
- `int64`: decimal in a JSON string (A114: integers beyond 2^53 as strings).
- `number`: ECMAScript `Number.prototype.toString` of the float64 (shortest round-trip;
  plain for 1e−7 < |x| < 1e21, else `d.ddde±x`); `-0` → `0`. NaN/±Infinity: **refused**.
- `date-time`: `YYYY-MM-DDTHH:MM:SS.mmmZ`, UTC, truncated to milliseconds; years 0001–9999.
  Parsing accepts any RFC 3339 offset; the lexical year is four digits `0000`–`9999`, and the 0001–9999 rule applies
  only to the **UTC instant after applying the offset** (`0000-12-31T23:00:00-01:00` is valid; `0000-01-01T00:00:00Z` and
  `9999-12-31T23:30:00-01:00` are not).
- No whitespace. Content-Type `application/problem+json`.
- "Refused" = the serialiser returns an error (Go) or throws a non-Problem programming error
  (TS `RangeError`, C# `ArgumentOutOfRangeException`/`InvalidOperationException`, Dart
  `ArgumentError`); the HTTP mapper then writes a plain 500 and logs the cause. Proven by the
  `refuse-*` vectors.

## 7. Type address: generation time vs run time

`{portal}/docs/{landscape}/{platform}/{service}/{module}/{version}/{id}` (C0 §2).

| Segment | Known at | Source |
|---|---|---|
| platform, service | generation | `problemgen.yaml` (baked into generated code and record) |
| module, version, id | generation | the definition |
| portal, landscape | **run time** | service config: `errorPortal.url` (an https origin, no path) and the landscape |

At start-up each service builds a **site** from `errorPortal.url` and its landscape — Go
`rt.NewSite`, TS `rt.Site.of`, C# `Site.Create`, Dart `rt.Site(...)` — which refuses a
missing or malformed value (portal `^https://[a-z0-9.-]+(:[0-9]+)?$`, landscape a DNS label),
so the service stops before it serves anything. The portal is an **origin only**: with a path
(`https://host/errors`) every address gains a segment, and every parser (all four check
`/docs/` as the first path segment and exactly 7 segments) reads every received problem as
`Unknown`. The serialiser and HTTP mapper take the site;
a value not built that way is refused. Generated code carries no portal. The address is built
in exactly one place per language (the runtime's `TypeURI(meta, site)`). The record takes the
portal from the same Helm value the pod reads (`.Values.errorPortal.url`, §13), so the
service and the record agree by construction. A service whose runtime config names a
different platform/service than the generated constants should fail at start-up (a one-line
assertion the template wires; see REPORT, open items).

## 8. Privacy and causes

- `private: true` fields: constructor inputs used only by `log`. Never serialised, never in
  the schema/record, never in the catalog (L212, tests).
- A **cause** (inner error, exception, stack) may be attached to a Problem value
  (`WithCause` / `cause:`) for the log hook. It is never serialised (conformance + tests).
- The **log line** is rendered in the API layer when the domain error is mapped (the mapper
  has the domain error, so private context is at hand). The runtime's HTTP mapper calls the
  log hook with `(problem, logLine, cause)`.

## 9. Retry hints

`retryAfter: <field>` names a required public `integer` field (seconds) (L220) on a
recoverable problem (L221). The HTTP mapper sets `Retry-After` from it; the field also stays
in `data`. The generic `dependency_failed` (503) carries `retryInSeconds`, so every 503 from
an unavailable or timed-out dependency has a `Retry-After` header.

## 10. Imports and clashes

- `imports` lists `builtin:<name>` (shipped in the tool; `builtin:generic/v1` = the §2.2
  baseline — `validation_error`, `entity_not_found`, `conflict`, `unauthenticated`,
  `unauthorized`, `invalid_json` — plus the six of errors-and-problems §4:
  `dependency_failed` (503, recoverable; public `retryInSeconds` → `Retry-After`; private
  `dependency`, `failure`), `internal_error` (500; public `reference` = the trace id;
  private `reason`), `route_not_found` (404; `method`, `path`), `method_not_allowed` (405;
  `method`, `path`, `allowed[]`; the API layer also sets `Allow`), `payload_too_large` (413;
  `limitBytes`), `unsupported_media_type` (415; `contentType`, `supported[]`)) or relative
  paths to `ProblemLibrary` files. Libraries are flat (no imports).
  `only:` picks problems (L103 if missing) and imports exactly the named types those
  problems reach, transitively; without `only:` every problem and type is imported.
- Imported problems become problems **of the importing module and version** (their address
  uses the importing module), exactly as lib-contracts §2.2 expects them in each row.
- A local id equal to an imported id is an error (L102); a local definition never overrides
  an import. The same id/type from two imports is accepted only if byte-identical (diamond).
- Every identifier the generators declare is modelled per language and scope (package /
  module / namespace / library; each struct, class, record, enum; Dart constructor
  parameters) in `tool/symbols.go`. Two equal identifiers in one scope are an error (L230),
  e.g. Go `NewFoo` from problem `foo` and type `NewFoo` from `new_foo`, or an optional
  `a` (setter `WithA`) next to a field `withA`. Keywords, generated members and
  standard-library type names the generated code relies on (dart:core, TS globals, C#
  System; e.g. `format_exception` → `FormatException`) are refused (L231).

## 11. Versioning, breaking changes, deprecation

`problemgen diff --old <file> new.yaml` or `--git <ref> new.yaml`. Within one (module,
version). With `--git`, the old definition **and every file it imports** are read from
the one commit the ref resolves to (`builtin:` libraries come from the running binary). A ref
that is not a commit, or any git failure, exits 2; only "the path does not exist in that
commit" counts as a new file.

| Change | Verdict |
|---|---|
| add a problem | allowed |
| remove a problem | **D101** (deprecate instead; remove in a new version) |
| change status / recoverable / title / retryAfter | **D102 / D103 / D104 / D111** |
| change detail/log template, description, examples | allowed (not contract) |
| add an optional public field | allowed |
| add a required public field | **D108** (an old server does not send it; a new client would refuse) |
| remove a public field / make it private | **D105** |
| change a field's type, format, item type, named type | **D106** |
| optional ↔ required | **D107** (both directions break a reader on the other side of a rollout) |
| change an enum's value set (add or remove) | **D109** (frozen; clients switch exhaustively) |
| private fields | free (not contract) |
| deprecate / un-deprecate | allowed |
| change `version` | new contract: no constraints; keep the old file while it is served |
| change `module` | **D112** |

Diff runs on the resolved set, so a change inside an imported library is caught in every
module that imports it. `deprecated` emits `// Deprecated:` (Go), `@deprecated` (TS),
`[Obsolete]` (C#), `@Deprecated` (Dart) and `"deprecated": true` in the schema.

## 12. Localisation

Out of scope for the definition. `detail` is English for humans reading raw responses and
logs. Clients that localise do it from the typed value: the generated client parse gives the
problem id + typed data, which is the message key + arguments. No message keys are added to
the format (they would be a second, unenforced naming scheme).

## 13. Outputs

- **Code** (Go/TS/C#/Dart): one file per module per language, committed. Header carries the
  tool version, the definition's path **relative to the generated file** (so it names exactly
  one definition) and its sha256. `generate` also writes the config's manifest
  `<config>.outputs`: one line per file it generated, `<sha256 of the content>  <path relative
  to the config>`. `problemgen verify` fails CI on any difference in a configured output or the
  manifest, and on a file the manifest lists that is no longer an output (stale). `generate`
  deletes a stale file only if all hold: the entry is a relative path inside the config
  directory or a directory the config writes to now; no component is a symlink; the content
  still has the recorded sha256; its header's source is one of this config's definitions.
  Catalogs carry no header, so they are never deleted. A stale file that is not deleted stays
  in the manifest, and verify keeps failing until a person removes it. `generate` refuses to
  overwrite a file whose header names another existing definition. An empty `modules` list
  and a module with no outputs are refused. (Two configs generating the same file from the
  same definition cannot be told apart; that is a configuration error.)
- **Problem record** (`atomi.cloud/v1alpha1 Problem`, one per module × version, error-portal
  row shape): `metadata.name = {service}.{module}.{landscape}.{version}`, `spec.problems[]`
  = `{id, type, title, status, recoverable, schema}`. The Helm inputs are the landscape
  (`required`, then `fail` unless it is a DNS label, `^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)
  and `.Values.errorPortal.url` — the error portal origin the pod reads at start-up; `fail`
  unless it is an https origin. When `problemgen.yaml` sets the optional `portal`, that is the
  default for an unset value; otherwise the value is `required` (the CRD requires every
  problem's `type` URI). The catalog never carried a portal (it is landscape-free). Everything
  else is literal, and every string field is a YAML double-quoted scalar (so
  `123`, `yes`, `on` stay strings). Lint refuses `{{`/`}}` in any record-bound text —
  titles, descriptions, public fields and every named type they reach (descriptions, nested
  fields, examples, enum values) — and checks the rendered record for `{{` (L240); records
  over 900 KiB are refused (L241; etcd limit 1.5 MiB). Platform/service are DNS labels of at
  most 63 characters.
- **schema** = JSON Schema of the public `data` only: `type: object`, `properties` in
  declaration order, `required`, field `description`/`examples`/`deprecated`; `integer` with
  ±(2^53−1) bounds; `int64` as string + pattern; `date-time`/`uuid`/`date`/`uri` as
  `format`; named types inlined with `title`. No public data → `{"not":{}}`.
  `additionalProperties` is left open (optional fields may be added within a version).
- **endpoints: dropped.** The CRD field is optional. A hand-written list would be an
  unenforced convention that rots; deriving it needs each language's API description to
  reference problem ids (open item).
- **Catalog entries** (frontend): landscape-free JSON list `{platform, service, module,
  version, id, status, recoverable, title}` — what a client needs to classify a problem
  without calling a server.

## 14. Parsing (client side)

Generated `Parse(body)` per module:
- not an RFC 9457 object → "not a problem" (api-engine classifier case 3);
- address not this platform/service/module/version, unknown id, status ≠ the definition, or
  data not matching (wrong type, missing required, bad enum) → **`Unknown`** (keeps the raw
  envelope + a reason) — never an exception;
- otherwise the typed value; unknown extra members are ignored (forward compatible); `null`
  members read as absent. The portal host and landscape are not checked.
  An absent `status` is "not a problem"; a present `status` that is `null` reads as 0, so the
  status check fails and the body is **`Unknown`** (all four runtimes agree).
- A parsed value keeps the received `type` and `detail`, so re-serialising it is lossless.

## 15. Commands

`problemgen lint | diff | generate | verify | schema | rules | conformance`. Exit 0 = clean,
1 = findings/drift, 2 = usage.

## 16. Generated code contract (every language)

Per module: a **closed set** (`Problem` sealed interface / union / abstract record / sealed
class) containing one type per problem **plus `Unknown`**; per problem a constructor whose
required fields are required parameters (wrong type or missing field = compile error, except
Go's zero-value struct literal, see REPORT); optional fields settable; `instance` and `cause`
settable; `meta` (id, title, status, recoverable, platform, service, module, version); `detail()` and `logLine()`; data writer; `Parse`. Nothing extends an
exception/error type; nothing in the generated file throws a Problem.

## 17. Runtime contract (what each problems library must provide)

1. `Meta` type and the problem interface/base the generated code implements.
2. The `Site` value (errorPortal.url + landscape, checked once at start-up) and the single
   `TypeURI(meta, site)` builder.
3. The canonical JSON writer (§6): strings, integer range check, int64 strings, ES number
   formatting, date-time formatting, U+FFFD replacement.
4. Template helpers: log-escape, detail sanitise, number/date-time formatting.
5. Envelope parser + address parser + typed decode helpers; `Unknown` support.
6. HTTP mapper: `(problem, site) → status, headers (Content-Type, Retry-After), body`
   + a log hook `(problem, logLine, cause)`; plain-500 fallback when serialisation refuses.

## 18. Consumers without HTTP (queue workers)

Same definitions, same generated values. A worker maps its domain error to a Problem the same
way and (a) logs `logLine()` through the same hook, (b) when the failure leaves the process
(dead-letter message, failure event, job record) it carries the same canonical RFC 9457
JSON; `status` then classifies retryable vs terminal together with `recoverable`. A worker
never needs the HTTP mapper.
