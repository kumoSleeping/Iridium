// Each login's colour on the usage page (the user, 2026-11-12): never grey, black or white, its hue
// taken from its avatar's main colour, and kept apart from the others' so no two read alike.
// A hue is OKLCH's (degrees); every colour is drawn at one lightness and chroma, so the chart's
// parts weigh the same whatever the avatar.
import { useEffect, useMemo, useState } from "react";

/** A hue as the chart's colour: as light and as strong as the series' (styles.css). */
export const hueColor = (h: number) => `oklch(0.66 0.16 ${Math.round(h)})`;

/** Hues apart by at least this much read as two colours; fewer when there are many. */
const GAP = 40;

const KEPT = "usage.hues";
/** An avatar's hue (null: it has no colour of its own), by its address; kept on this device, so the
 *  chart opens in its colours rather than recolouring as the avatars load. */
let found: Map<string, number | null> | null = null;
const known = () => {
  if (found) return found;
  found = new Map();
  try {
    for (const [k, v] of Object.entries(JSON.parse(localStorage.getItem(KEPT) ?? "{}") as Record<string, number | null>)) found.set(k, v);
  } catch {
    // none kept, or not readable: read again
  }
  return found;
};
const save = () => {
  try {
    // The last 64: the avatars change, the old ones are not asked again.
    localStorage.setItem(KEPT, JSON.stringify(Object.fromEntries([...known()].slice(-64))));
  } catch {
    // full or off: read again next time
  }
};

const asking = new Map<string, Promise<number | null>>();

/** sRGB (0–255) to OKLab's lightness and its a, b. */
function oklab(r8: number, g8: number, b8: number): [number, number, number] {
  const lin = (c: number) => ((c /= 255) <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4);
  const r = lin(r8), g = lin(g8), b = lin(b8);
  const l = Math.cbrt(0.4122214708 * r + 0.5363325363 * g + 0.0514459929 * b);
  const m = Math.cbrt(0.2119034982 * r + 0.6806995451 * g + 0.1073969566 * b);
  const s = Math.cbrt(0.0883024619 * r + 0.2817188376 * g + 0.6299787005 * b);
  return [0.2104542553 * l + 0.793617785 * m - 0.0040720468 * s, 1.9779984951 * l - 2.428592205 * m + 0.4505937099 * s, 0.0259040371 * l + 0.7827717662 * m - 0.808675766 * s];
}

/** The hue most of an image's coloured pixels have: each pixel with colour enough (not grey, not
 *  near black or white) votes for its hue in 10° bins, weighed by how strong its colour is; the
 *  strongest bin with its neighbours wins, and its pixels' mean hue is the answer. null: under 4%
 *  of the image has colour (a grey or black-and-white avatar). Exported for its test. */
export function mainHue(px: Uint8ClampedArray): number | null {
  const BINS = 36;
  const bins = new Float64Array(BINS);
  const hues: [number, number][] = [];
  let n = 0;
  for (let i = 0; i < px.length; i += 4) {
    if (px[i + 3]! < 128) continue;
    n++;
    const [L, a, b] = oklab(px[i]!, px[i + 1]!, px[i + 2]!);
    const c = Math.hypot(a, b);
    if (c < 0.05 || L < 0.25 || L > 0.96) continue;
    const h = ((Math.atan2(b, a) * 180) / Math.PI + 360) % 360;
    // Strong colours count most, and a mid light one more than a dim one.
    const w = c * (1 - Math.abs(L - 0.65));
    bins[Math.floor(h / (360 / BINS)) % BINS]! += w;
    hues.push([h, w]);
  }
  if (!n || hues.length < n * 0.04) return null;
  let best = 0;
  let top = -1;
  for (let i = 0; i < BINS; i++) {
    const v = bins[(i + BINS - 1) % BINS]! + 2 * bins[i]! + bins[(i + 1) % BINS]!;
    if (v > top) (top = v), (best = i);
  }
  const at = (best + 0.5) * (360 / BINS);
  // The mean of the hues near the peak, round the circle.
  let x = 0, y = 0;
  for (const [h, w] of hues) {
    if (apart(h, at) > 20) continue;
    x += w * Math.cos((h * Math.PI) / 180);
    y += w * Math.sin((h * Math.PI) / 180);
  }
  return ((Math.atan2(y, x) * 180) / Math.PI + 360) % 360;
}

/** An avatar's hue: its pixels read small off a canvas. null: no colour, or not readable (another
 *  site's image without CORS, a broken one). */
function avatarHue(src: string): Promise<number | null> {
  const k = known();
  if (k.has(src)) return Promise.resolve(k.get(src)!);
  let p = asking.get(src);
  if (p) return p;
  p = (async () => {
    let h: number | null = null;
    try {
      const img = new Image();
      if (new URL(src, location.href).origin !== location.origin) img.crossOrigin = "anonymous";
      img.src = src;
      await img.decode();
      const c = document.createElement("canvas");
      c.width = c.height = 32;
      const g = c.getContext("2d", { willReadFrequently: true });
      if (g) {
        g.drawImage(img, 0, 0, 32, 32);
        h = mainHue(g.getImageData(0, 0, 32, 32).data);
      }
    } catch {
      h = null;
    }
    k.set(src, h);
    save();
    asking.delete(src);
    return h;
  })();
  asking.set(src, p);
  return p;
}

/** Two hues' distance round the circle, 0 to 180. */
export const apart = (a: number, b: number) => {
  const d = Math.abs(a - b) % 360;
  return d > 180 ? 360 - d : d;
};

const fnv = (s: string) => {
  let h = 0x811c9dc5;
  for (let i = 0; i < s.length; i++) h = Math.imul(h ^ s.charCodeAt(i), 0x01000193);
  return h >>> 0;
};

/** Each one's hue, as near its own (its avatar's, else one its ID hashes to) as the others let it:
 *  those with an avatar's colour take theirs first, by ID, so the same logins come out the same on
 *  every device; one too near a hue taken moves to the nearest one far enough from all, or, with
 *  too many for that, the one furthest from all. Exported for its test. */
export function spread(want: { id: string; hue: number | null }[]): Map<string, number> {
  const gap = Math.min(GAP, (360 / Math.max(1, want.length)) * 0.8);
  const order = [...want].sort((a, b) => Number(a.hue === null) - Number(b.hue === null) || (a.id < b.id ? -1 : a.id > b.id ? 1 : 0));
  const taken: number[] = [];
  const out = new Map<string, number>();
  for (const w of order) {
    const own = w.hue ?? fnv(w.id) % 360;
    const near = (h: number) => (taken.length ? Math.min(...taken.map((t) => apart(h, t))) : 360);
    let h = own;
    if (near(own) < gap) {
      // Out from its own hue both ways, 5° at a time.
      let far = own;
      h = NaN;
      for (let d = 5; d <= 180 && Number.isNaN(h); d += 5) {
        for (const c of [own + d, own - d]) {
          const x = (c + 360) % 360;
          if (near(x) >= gap) {
            h = x;
            break;
          }
          if (near(x) > near(far)) far = x;
        }
      }
      if (Number.isNaN(h)) h = far;
    }
    taken.push(h);
    out.set(w.id, h);
  }
  return out;
}

/** The logins' colours (id → its avatar's address, if it has one): at first from what is kept, and
 *  again as each avatar's colour is read. */
export function useHues(who: [id: string, avatar: string | undefined][]): Map<string, string> {
  const key = who.map(([id, a]) => `${id}\u0001${a ?? ""}`).join("\u0000");
  const [, setRead] = useState(0);
  useEffect(() => {
    let live = true;
    for (const [, a] of who) if (a && !known().has(a)) void avatarHue(a).then(() => live && setRead((n) => n + 1));
    return () => {
      live = false;
    };
  }, [key]);
  const k = known();
  const sig = who.map(([, a]) => (a && k.has(a) ? String(k.get(a)) : "?")).join(",");
  return useMemo(() => {
    const hues = spread(who.map(([id, a]) => ({ id, hue: a ? (k.get(a) ?? null) : null })));
    return new Map([...hues].map(([id, h]) => [id, hueColor(h)]));
  }, [key, sig]);
}
