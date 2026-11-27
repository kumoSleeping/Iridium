// A face made from a seed (kumo, 2026-11-27): one with none of its own drawn as two colours in a
// grid mirrored left to right, not as a letter. The same seed (a login's id) gives the same face
// everywhere; nothing is stored.

/** Two colours that sit well together, one pair a face. */
const PAIRS: [string, string][] = [
  ["#f47b20", "#2f6fe0"],
  ["#e5484d", "#f5d90a"],
  ["#30a46c", "#0b7285"],
  ["#8e4ec6", "#f76b15"],
  ["#0091ff", "#ffc53d"],
  ["#d6409f", "#3e63dd"],
  ["#12a594", "#e93d82"],
  ["#5b5bd6", "#46a758"],
];

/** FNV-1a: a seed's number, the same on every page. */
function hash(s: string): number {
  let h = 0x811c9dc5;
  for (let i = 0; i < s.length; i++) h = Math.imul(h ^ s.charCodeAt(i), 0x01000193);
  return h >>> 0;
}

const N = 5;

/** The cells of a seed's face: which of the N×N are the first colour, mirrored left to right. */
export function patternOf(seed: string): { cells: boolean[]; pair: [string, string] } {
  let h = hash(seed);
  const pair = PAIRS[h % PAIRS.length]!;
  const half = Math.ceil(N / 2);
  const cells: boolean[] = Array.from({ length: N * N }, () => false);
  for (let y = 0; y < N; y++)
    for (let x = 0; x < half; x++) {
      h = Math.imul(h ^ (h >>> 15), 0x2c1b3c6d) >>> 0;
      const on = (h & 1) === 1;
      cells[y * N + x] = on;
      cells[y * N + (N - 1 - x)] = on;
    }
  return { cells, pair };
}

export function PatternFace({ seed, size }: { seed: string; size: number }) {
  const { cells, pair } = patternOf(seed);
  return (
    <svg width={size} height={size} viewBox={`0 0 ${N} ${N}`} shapeRendering="crispEdges" className="block rounded-full" aria-hidden>
      <rect width={N} height={N} fill={pair[1]} />
      {cells.map((on, i) => (on ? <rect key={i} x={i % N} y={Math.floor(i / N)} width={1} height={1} fill={pair[0]} /> : null))}
    </svg>
  );
}
