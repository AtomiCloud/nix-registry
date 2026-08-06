// Output shapes, kept in one place so that a reader can tell a REFUSAL from a
// SKIP from a note without reading the surrounding logic. The glyphs match
// dlint's, which is what the same reviewers already read.

export const info = (m: string) => console.log(`ℹ️ ${m}`);
export const ok = (m: string) => console.log(`✅ ${m}`);
export const skipped = (m: string) => console.log(`⏭️ ${m}`);
export const warn = (m: string) => console.log(`⚠️ ${m}`);
export const refusal = (m: string) => console.error(`❌ ${m}`);

// A sweep that names N terms must account for all N. A term with zero hits is
// named DEAD rather than folded into the total, because an unreported dead term
// overstates how wide the sweep actually was.
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
