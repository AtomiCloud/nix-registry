// The resolver SPEC is data, never code.
//
// This is what makes the lifecycle test answer 1 (D10). A repository that
// introduces a language names the runtime in its own `skills-sync.yaml`; if the
// language is one skills-sync already ships a preset for, that is the only edit.
// If it is a language nobody has met before, the same file may carry the whole
// resolver INLINE — still one place, and still no change to this package, to
// workspace, or to shared.
//
// Everything a runtime needs is expressed as three data steps:
//
//   declare  which manifests in the repository NAME diene skill-shipping
//            packages, and how a package name (and optionally a version) is
//            read out of them
//   deps     how to tell a restored dependency tree from an unrestored one
//   resolve  where an INSTALLED package's shipped skills live on disk
//
// Nothing here reads the file system; resolve.ts does that.

export type DeclareFormat = 'json' | 'yaml' | 'text';

export interface DeclareSource {
  // Exactly one of `file` or `glob`. `glob` exists because a pub workspace keeps
  // its real dependencies in packages/<name>/pubspec.yaml while the root carries
  // only a member list, so a root-only parse sees no dependencies on exactly the
  // nodes that have them.
  file?: string;
  glob?: string;
  format: DeclareFormat;
  // json/yaml: merge these maps and take their keys.
  maps?: string[];
  // json/yaml: regex the key must match to count as a diene package.
  match?: string;
  // text: regex over the file, with the capture groups below.
  pattern?: string;
  nameGroup?: number;
  versionGroup?: number;
  // Optional: a second, weaker witness that the repository declares packages
  // (a lockfile line). It can prove DECLARED but never yields a package name.
  witnessOnly?: boolean;
}

export interface DepsSpec {
  // Paths that must exist for the dependency tree to count as restored.
  requirePath?: string[];
  // Binaries that must be on PATH.
  requireCommand?: string[];
}
//
// There is deliberately no per-package existence test here. EVERY declared
// package must resolve to an installed directory or the dependency tree is
// unrestored — that is checked by the resolution itself, uniformly, for all
// strategies. A cache holding only SOME of the declared packages must never
// publish a partial vendor tree: the freshness gate would then fail on a diff
// that names the missing skills but not the reason, which is how a partially
// warm runner cache reads as a content defect.

export type ResolveStrategy = 'path-template' | 'json-file' | 'json-command';

export interface ResolveSpec {
  strategy: ResolveStrategy;

  // path-template: the INSTALLED PACKAGE directory (not the skills directory —
  // `subdir` appends that). `{name}`, `{name|lower}`, `{name|basename}`,
  // `{version}` and `{home}` are substituted.
  template?: string;

  // json-file: read this file. json-command: run this argv.
  file?: string;
  command?: string[];
  // Where the list of installed packages lives inside that JSON. Empty means the
  // document IS the list, or — for json-command — a stream of concatenated
  // top-level objects, which is what `go list -m -json all` emits.
  listPath?: string;
  nameKey?: string;
  dirKey?: string;
  // A `file://` or relative dirKey is resolved against this directory.
  dirRelativeTo?: string;

  // Where inside the resolved package directory the shipped skills live.
  subdir?: string;
  // How the package is named inside the vendor tree.
  vendorName?: 'full' | 'basename';
}

export interface ResolverSpec {
  name: string;
  declare: DeclareSource[];
  deps?: DepsSpec;
  resolve: ResolveSpec;
}

// --------------------------------------------------------------------------- //
// built-in presets
// --------------------------------------------------------------------------- //
//
// Each preset is the DATA form of one arm of the mechanism this tool replaces
// (the shell resolver deleted from the workspace and shared templates). They are
// presets, not privileges: a repository can override any of them with an inline
// `resolver:` and nothing about the engine changes.

const nodePreset: ResolverSpec = {
  name: 'node',
  declare: [
    {
      file: 'package.json',
      format: 'json',
      maps: ['dependencies', 'devDependencies', 'optionalDependencies', 'peerDependencies'],
      match: '^@atomicloud/diene\\.',
    },
    {
      file: 'bun.lock',
      format: 'text',
      pattern: '^\\s*"(@atomicloud/diene\\.[^"]+)"',
      nameGroup: 1,
      witnessOnly: true,
    },
  ],
  deps: { requirePath: ['node_modules'] },
  resolve: {
    strategy: 'path-template',
    template: 'node_modules/{name}',
    subdir: 'skills',
    vendorName: 'basename',
  },
};

const nugetPreset: ResolverSpec = {
  name: 'nuget',
  declare: [
    {
      file: 'Directory.Packages.props',
      format: 'text',
      pattern: 'PackageVersion\\s+Include="(AtomiCloud\\.Diene\\.[^"]+)"\\s+Version="([^"]+)"',
      nameGroup: 1,
      versionGroup: 2,
    },
  ],
  resolve: {
    strategy: 'path-template',
    template: '{home}/.nuget/packages/{name|lower}/{version}',
    subdir: 'skills',
    vendorName: 'full',
  },
};

const goPreset: ResolverSpec = {
  name: 'go',
  declare: [
    {
      file: 'go.mod',
      format: 'text',
      // Only `require` entries are dependency obligations; a main module whose
      // own name matches is not one.
      pattern: '^\\s*(?:require\\s+)?((?:[^\\s()]+/)?diene[._-][^\\s]*)\\s+v[^\\s]+',
      nameGroup: 1,
    },
  ],
  deps: { requireCommand: ['go'] },
  resolve: {
    strategy: 'json-command',
    command: ['go', 'list', '-m', '-json', 'all'],
    nameKey: 'Path',
    dirKey: 'Dir',
    subdir: 'skills',
    vendorName: 'basename',
  },
};

const pubPreset: ResolverSpec = {
  name: 'pub',
  declare: [
    {
      glob: '**/pubspec.yaml',
      format: 'yaml',
      maps: ['dependencies', 'dev_dependencies', 'dependency_overrides'],
      match: '^diene_',
    },
  ],
  deps: { requirePath: ['.dart_tool/package_config.json'] },
  resolve: {
    strategy: 'json-file',
    file: '.dart_tool/package_config.json',
    listPath: 'packages',
    nameKey: 'name',
    dirKey: 'rootUri',
    dirRelativeTo: '.dart_tool',
    subdir: 'skills',
    vendorName: 'full',
  },
};

// The alias table is the whole of "which languages does skills-sync know". A
// template names one of these; flutter and dart are the same pub mechanism, and
// bun and node are the same node_modules mechanism.
export const PRESETS: Record<string, ResolverSpec> = {
  node: nodePreset,
  bun: nodePreset,
  nuget: nugetPreset,
  dotnet: nugetPreset,
  go: goPreset,
  pub: pubPreset,
  dart: pubPreset,
  flutter: pubPreset,
};

export const PRESET_NAMES = Object.keys(PRESETS).sort();
