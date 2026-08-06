export const info = (m: string) => console.log(`ℹ️ ${m}`);
export const ok = (m: string) => console.log(`✅ ${m}`);
export const skipped = (m: string) => console.log(`⏭️ ${m}`);
export const warn = (m: string) => console.log(`⚠️ ${m}`);
export const refusal = (m: string) => console.error(`❌ ${m}`);

export function reportTermLiveness(label: string, terms: { term: string; hits: number }[]): void {
  if (terms.length === 0) {
    info(`${label}: no terms in the vocabulary`);
    return;
  }
  for (const t of terms) {
    info(`${label}: '${t.term}' → ${t.hits} hit(s)${t.hits === 0 ? ' — DEAD' : ''}`);
  }
  const dead = terms.filter(t => t.hits === 0).length;
  info(`${label}: ${terms.length} term(s), ${dead} dead`);
}
