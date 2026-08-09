export interface NixProbe {
  path: string;
  emptySkeleton: string | null;
  inject: (real: string, sentinel: string) => string | null;
}
