// The effort slider's fill as a liquid (the user, 2026-11-06): oil-paint orange, vivid pink and
// fire orange flowing into one another, a faint light where the currents meet. Drawn per pixel on a
// canvas a sample per CSS pixel of the track (some 300 by 24): a coarser one, stretched, came out in
// blocks (the user, 2026-11-08: "像素化"); this one the screen's own scaling keeps smooth.
//
// The flow: the track's point (u, v) is moved by two crossed sine currents that drift with time
// (domain warping, cheaply); two fields read at the moved point decide the mix: a, orange against
// fire; b, the pink veins. Where a and b meet, a little light. Moving the knob sloshes the liquid:
// a damped spring pushes the pattern along the track and back, and the flow quickens a while.
// Higher levels flow faster and run pinker.

/** Linear 0..1 to sRGB 0..255, by a table: a pow per channel per pixel is the frame's dearest part. */
const GAMMA = Float32Array.from({ length: 1025 }, (_, i) => Math.pow(i / 1024, 1 / 2.2) * 255);
const srgb = (p: number) => GAMMA[Math.round(Math.min(1, Math.max(0, p)) * 1024)]!;

/** sRGB 0..255 to linear 0..1. */
const lin = (c: number) => Math.pow(c / 255, 2.2);
const rgb = (h: number) => [lin((h >> 16) & 255), lin((h >> 8) & 255), lin(h & 255)] as const;

/** The paints, in linear light: mixed there, the colours stay bright where they meet. */
export const PAINT = {
  oil: rgb(0xe9822f), // oil-paint orange, warm and a little ochre
  fire: rgb(0xff4a17), // fire orange
  pink: rgb(0xff2f82), // vivid pink
  ember: rgb(0xb8321c), // the folds, darker
  light: rgb(0xffe2b8), // the light, cream
};

const smooth = (a: number, b: number, x: number) => {
  const k = Math.min(1, Math.max(0, (x - a) / (b - a)));
  return k * k * (3 - 2 * k);
};
const mix = (a: number, b: number, k: number) => a + (b - a) * k;

/** The colour at (u, v) of the track (0..1 each) at flow time t: RGBA 0..255 into out at o.
 *  energy: 0..1, the level and the slosh; push: how far the slosh has moved the liquid along. */
export function shade(u: number, v: number, t: number, energy: number, push: number, out: Uint8ClampedArray, o: number) {
  // The currents, crossed, each bent by the other.
  // The track is some ten times as long as high: u runs faster, so the eddies come out round.
  const X = u * 7 - push * 9;
  const w1 = Math.sin(X * 0.9 + t * 0.83 + 1.3 * Math.sin(v * 2.9 - t * 0.61));
  const w2 = Math.sin(v * 3.1 - t * 0.57 + 1.2 * Math.sin(X * 1.3 + t * 0.73));
  const x = X + 0.9 * w2;
  const y = v * 2.2 + 0.7 * w1;
  const a = 0.5 + 0.5 * Math.sin(x * 0.85 + t * 0.45 + 1.7 * Math.sin(y * 1.9 - x * 0.3 - t * 0.38));
  const b = 0.5 + 0.5 * Math.sin(y * 1.7 - x * 0.7 + t * 0.52 + 1.4 * w1);
  // Orange into fire, in strokes; pink veins, more along the track and the higher the level.
  const kFire = smooth(0.3, 0.7, a);
  const kPink = smooth(0.5, 0.85, b) * (0.45 + 0.3 * u + 0.25 * energy);
  const kEmber = smooth(0.25, 0, a) * 0.35;
  const near = Math.max(0, 1 - Math.abs(a - b) * 2.6) ** 2;
  const kLight = near * near * (0.07 + 0.06 * energy);
  for (let c = 0; c < 3; c++) {
    let p = mix(PAINT.oil[c]!, PAINT.fire[c]!, kFire);
    p = mix(p, PAINT.pink[c]!, kPink);
    p = mix(p, PAINT.ember[c]!, kEmber);
    p = mix(p, PAINT.light[c]!, kLight);
    out[o + c] = srgb(p);
  }
  // A little clear where it runs thin, so the track shows through: liquid, not a slab.
  out[o + 3] = (0.8 + 0.16 * a + 0.04 * energy) * 255;
}

/** The slosh: a damped spring, its push and how fast it moves. */
export type Slosh = { push: number; vel: number };

/** One step of the slosh by dt seconds: underdamped, so it swings past once or twice and settles. */
export function slosh(s: Slosh, dt: number) {
  const k = 38;
  const c = 6.5;
  s.vel += (-k * s.push - c * s.vel) * dt;
  s.push += s.vel * dt;
}

/** Samples per CSS pixel, and a bound on them (a wide panel, a hi-dpi screen). */
const PER_PX = 1;
const MAX_W = 480;

/** Runs the liquid on canvas till the returned stop is called. frac: the knob's place now (0..1).
 *  Still (reduced motion, or a screenshot being taken): one frame, nothing runs on. The canvas is
 *  sized to the track as laid out, and again when that changes. */
export function flow(canvas: HTMLCanvasElement, frac: () => number, still: boolean) {
  const g = canvas.getContext("2d");
  if (!g) return () => {};
  let W = 0;
  let H = 0;
  let img: ImageData | null = null;
  const fit = () => {
    const w = Math.min(MAX_W, Math.max(8, Math.round(canvas.clientWidth * PER_PX)));
    const h = Math.max(4, Math.round(canvas.clientHeight * PER_PX));
    if (w === W && h === H) return false;
    W = canvas.width = w;
    H = canvas.height = h;
    img = g.createImageData(W, H);
    return true;
  };
  fit();
  const s: Slosh = { push: 0, vel: 0 };
  let t = Math.random() * 100;
  let last = frac();
  let then = 0;
  let id = 0;
  const draw = () => {
    if (!img) return;
    const energy = Math.min(1, frac() * 0.8 + Math.abs(s.vel) * 0.25);
    const d = img.data;
    for (let j = 0; j < H; j++) for (let i = 0; i < W; i++) shade(i / (W - 1), j / (H - 1), t, energy, s.push, d, (j * W + i) * 4);
    g.putImageData(img, 0, 0);
  };
  const seen = new ResizeObserver(() => fit() && still && draw());
  const tick = (now: number) => {
    const dt = then ? Math.min(0.05, (now - then) / 1000) : 0;
    then = now;
    const f = frac();
    // The knob moved: the liquid is thrown the way it went.
    if (f !== last) {
      s.vel += (f - last) * 7;
      last = f;
    }
    slosh(s, dt);
    t += dt * (0.55 + 0.8 * f + Math.min(2, Math.abs(s.vel) * 1.5));
    draw();
    id = requestAnimationFrame(tick);
  };
  seen.observe(canvas);
  if (still) draw();
  else id = requestAnimationFrame(tick);
  return () => (cancelAnimationFrame(id), seen.disconnect());
}
