// The effort slider's knob pulls the liquid (the user, 2026-11-06): the knob leads, the fill's front
// follows a beat later, and between them a few blobs on springs of their own, each a little slower
// and looser than the last. The fill and the blobs are drawn through a goo filter (effort.tsx
// GooFilter: blur, then the alpha pulled hard), so where they come close they run together: a neck
// of liquid drawn out as the knob jumps and taken back in as the front catches up, shaking once.
// Only the springs live here; the page reads them as CSS variables (--knob, --front, --b0..).

/** A damped spring toward a target: where it is, how fast, how stiff, how damped. */
export type Spring = { x: number; v: number; k: number; c: number };

/** The springs, in order: the knob (stiff, a slight overshoot), the front (slow, lagging), and the
 *  blobs between, looser and looser (they wobble). */
export const SPRINGS: Pick<Spring, "k" | "c">[] = [
  { k: 260, c: 24 },
  { k: 48, c: 12.5 }, // the front: a little slower still (the user, 2026-11-06)
  { k: 190, c: 14 },
  { k: 130, c: 13 },
  { k: 95, c: 11 },
  // the knob's two trailing balls: elastic, lagging, as in the code the user brought (2026-11-06)
  { k: 150, c: 9 },
  { k: 100, c: 8 },
];
export const VARS = ["--knob", "--front", "--b0", "--b1", "--b2", "--k1", "--k2"];

/** Moves s toward to by dt seconds, in steps short enough to stay stable for the stiffest. */
export function step(s: Spring, to: number, dt: number) {
  const n = Math.max(1, Math.ceil(dt / (1 / 120)));
  const h = dt / n;
  for (let i = 0; i < n; i++) {
    s.v += (s.k * (to - s.x) - s.c * s.v) * h;
    s.x += s.v * h;
  }
}

/** Whether s has come to rest at to. */
export const still = (s: Spring, to: number) => Math.abs(s.x - to) < 1e-4 && Math.abs(s.v) < 1e-3;

/** Runs the springs on root's style till the returned stop is called. frac: where the knob is to
 *  be (0..1). calm (reduced motion, a screenshot): all go there at once. */
export function pull(root: HTMLElement, frac: () => number, calm: boolean) {
  const f0 = frac();
  const ss: Spring[] = SPRINGS.map((p) => ({ ...p, x: f0, v: 0 }));
  const put = () => ss.forEach((s, i) => root.style.setProperty(VARS[i]!, s.x.toFixed(4)));
  put();
  let then = 0;
  let id = 0;
  let resting = true;
  const tick = (now: number) => {
    const dt = then ? Math.min(1 / 30, (now - then) / 1000) : 0;
    then = now;
    const to = frac();
    if (calm) {
      if (ss[0]!.x !== to) (ss.forEach((s) => ((s.x = to), (s.v = 0))), put());
    } else if (!resting || !ss.every((s) => still(s, to))) {
      for (const s of ss) step(s, to, dt);
      resting = ss.every((s) => still(s, to));
      if (resting) ss.forEach((s) => ((s.x = to), (s.v = 0)));
      put();
    }
    id = requestAnimationFrame(tick);
  };
  id = requestAnimationFrame(tick);
  return () => cancelAnimationFrame(id);
}
