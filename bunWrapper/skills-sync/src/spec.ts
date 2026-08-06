export type DeclareFormat = 'json' | 'yaml' | 'text';

export interface DeclareSource {
  file?: string;
  glob?: string;
  format: DeclareFormat;
  maps?: string[];
  match?: string;
  pattern?: string;
  nameGroup?: number;
  versionGroup?: number;
  witnessOnly?: boolean;
}

export interface DepsSpec {
  requirePath?: string[];
  requireCommand?: string[];
}

export type ResolveStrategy = 'path-template' | 'json-file' | 'json-command';

export interface ResolveSpec {
  strategy: ResolveStrategy;

  template?: string;

  file?: string;
  command?: string[];
  listPath?: string;
  nameKey?: string;
  dirKey?: string;
  dirRelativeTo?: string;

  subdir?: string;
  vendorName?: 'full' | 'basename';
}

export interface ResolverSpec {
  name: string;
  declare: DeclareSource[];
  deps?: DepsSpec;
  resolve: ResolveSpec;
}

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
