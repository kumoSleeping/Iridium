// The effort slider's fill as a liquid (the user, 2026-11-06): warm liquid in a glass tube, and in
// it a few big translucent drops of a pinker one, drifting with the current. Drawn per pixel on a
// small canvas the page stretches over the track (its smoothing is the last of the blur).
//
// Few, large, soft boundaries: the medium is one smooth current (oil orange at the top settling to
// fire orange at the foot, a single slow swell along the track, pinker along it and the higher the
// level); the only real edges are the drops' own, round. Each drop is translucent: its body is pink
// over the medium seen through it (magnified and flipped, as a lens does), clearest at the centre,
// densest toward the rim. Light from above is caught at its rim and focused along its lower edge,
// with a faint glow spilling past it; no sweeping shine. Gravity: the drops ride a little low, the
// medium is deepest at the foot. Flow time t drifts the current and carries the drops (they breathe
// and wobble as they go), faster the higher the level (rate); moving the knob sloshes it all along
// the track (push), the drops, heavier, further than the current.

export const W = 160;
export const H = 15;

const TW = 260; // the track, css px
const TH = 24;

const lin = (h: number) => [Math.pow(((h >> 16) & 255) / 255, 2.2), Math.pow(((h >> 8) & 255) / 255, 2.2), Math.pow((h & 255) / 255, 2.2)];
const OIL = lin(0xe9822f);
const FIRE = lin(0xff4a17);
const PINK = lin(0xff2f82);
const CREAM = lin(0xfff0dc);
const GLOW = lin(0xffd9c2);

// linear 0..1 -> sRGB byte
const LUT = new Uint8ClampedArray(4097);
for (let i = 0; i <= 4096; i++) LUT[i] = Math.round(Math.pow(i / 4096, 1 / 2.2) * 255);
const enc = (x: number) => LUT[x <= 0 ? 0 : x >= 1 ? 4096 : (x * 4096) | 0]!;

// one ramp, oil (0) -> fire (0.5) -> pink (1), in linear light, tabled
const RN = 256;
const RAMP = new Float32Array((RN + 1) * 3);
for (let i = 0; i <= RN; i++) {
  const h = i / RN;
  const [a, b, k] = h < 0.5 ? [OIL, FIRE, h / 0.5] : [FIRE, PINK, (h - 0.5) / 0.5];
  const s = k * k * (3 - 2 * k);
  for (let c = 0; c < 3; c++) RAMP[i * 3 + c] = a[c]! + (b[c]! - a[c]!) * s;
}

/** The medium's place on the ramp at css point (x, y). */
function medium(x: number, y: number, t: number, base: number, shift: number) {
  const u = (x - shift) / TW;
  const v = y / TH;
  const h = base + 0.26 * (x / TW) + 0.12 * v + 0.3 * Math.sin(u * 5 - t * 0.55 + 1.2 * Math.sin(v * 2.2 + u * 2.7 - t * 0.37));
  return h < 0 ? 0 : h > 1 ? 1 : h;
}

// the drops: place on the loop, size, height, phase
const DROPS = [
  { p: 0, r: 8.6, y: 12.8, k: 0.0 },
  { p: 0.35, r: 5.8, y: 14.2, k: 2.1 },
  { p: 0.66, r: 7.4, y: 13.2, k: 4.4 },
];
const N = DROPS.length;
const EDGE = 40;
const LOOP = TW + 2 * EDGE;

const dx = new Float64Array(N);
const dy = new Float64Array(N);
const rx = new Float64Array(N);
const ry = new Float64Array(N);

export function frame(data: Uint8ClampedArray, t: number, energy: number, push: number) {
  const shift = push * 150;
  const dropShift = push * 230;
  const base = 0.24 + 0.08 * energy;
  const glowK = 0.55 + 0.35 * energy;
  for (let n = 0; n < N; n++) {
    const d = DROPS[n]!;
    let x = (d.p * LOOP + t * 26 + 8 * Math.sin(t * 0.37 + d.k) + dropShift) % LOOP;
    if (x < 0) x += LOOP;
    dx[n] = x - EDGE;
    dy[n] = d.y + 0.9 * Math.sin(t * 0.8 + d.k * 1.7);
    // breathing, and a wobble between longer and rounder (area kept)
    const b = 1 + 0.04 * Math.sin(t * 2.1 + d.k);
    const s = 1.12 + 0.1 * Math.sin(t * 1.5 + d.k * 0.7);
    rx[n] = d.r * b * s;
    ry[n] = (d.r * b) / Math.sqrt(s);
  }
  for (let j = 0; j < H; j++) {
    const y = ((j + 0.5) / H) * TH;
    const v = y / TH;
    const lift = (1 - v) * (1 - v) * 0.05; // thinner and lighter at the top
    const deep = 1 + 0.32 * v * v; // deeper at the foot: more saturated, hardly darker
    const alpha0 = 0.86 + 0.13 * v;
    for (let i = 0; i < W; i++) {
      const x = ((i + 0.5) / W) * TW;
      let h = medium(x, y, t, base, shift);
      let a = alpha0;
      let light = lift;
      let glow = 0;
      for (let n = 0; n < N; n++) {
        const ex = (x - dx[n]!) / rx[n]!;
        if (ex > 1.45 || ex < -1.45) continue;
        const ey = (y - dy[n]!) / ry[n]!;
        const dd = ex * ex + ey * ey;
        if (dd > 2.1) continue;
        const d = Math.sqrt(dd);
        // the outline, soft over ~2.2 css px of the drop's short side
        const e = 2.2 / ry[n]!;
        let c = (1 + e * 0.5 - d) / e;
        c = c < 0 ? 0 : c > 1 ? 1 : c;
        if (c > 0) {
          // the body: pink over the medium behind, seen through the lens
          const behind = medium(dx[n]! + (x - dx[n]!) * 0.5, dy[n]! - (y - dy[n]!) * 0.6, t, base, shift);
          h += (Math.min(1, behind + 0.1) - h) * c; // a little pinker: a drop of a richer liquid, not air
          light += 0.14 * c * (1 - dd * 0.7);
          // a faint, small light high in the drop: weak, no shine
          const sx = ex + 0.32, sy = ey + 0.5;
          const sp = 1 - (sx * sx + sy * sy) * 9;
          if (sp > 0) light += sp * sp * 0.22;
          a += (1 - a) * c * 0.5;
        }
        // light at the rim: a band inside the edge, focused along the bottom
        const r = (d - 0.84) / 0.2;
        const q = 1 - r * r;
        if (q > 0) {
          const low = ey > 0.9 ? 1 : ey < -0.6 ? 0 : (ey + 0.6) / 1.5;
          glow += q * q * (0.25 + 0.75 * low * low);
        }
        // and a little spilling past it
        if (d > 0.96) {
          const o = 1 - (d - 0.96) / 0.45;
          if (o > 0) glow += o * o * 0.1;
        }
      }
      const hi = (h * RN) | 0;
      const g = glow * glowK * 0.5;
      const o = (j * W + i) * 4;
      for (let c = 0; c < 3; c++) {
        let p = RAMP[hi * 3 + c]!;
        p = Math.pow(p, deep);
        p = p + (CREAM[c]! - p) * light;
        p = p + GLOW[c]! * g;
        data[o + c] = enc(p);
      }
      a += g * 0.4;
      data[o + 3] = (a > 1 ? 1 : a) * 255;
    }
  }
}

/** Flow-time seconds per real second at the level frac (0..1): faster the harder it thinks. */
export function rate(frac: number) {
  return 0.35 + 1.45 * frac * (0.6 + 0.4 * frac);
}

/** The slosh: a damped spring, its push (along the track, about -0.3..0.3) and how fast it moves. */
export type Slosh = { push: number; vel: number };

/** One step of the slosh by dt seconds: underdamped, so it swings past once or twice and settles. */
export function slosh(s: Slosh, dt: number) {
  const k = 38;
  const c = 6.5;
  s.vel += (-k * s.push - c * s.vel) * dt;
  s.push += s.vel * dt;
}

/** Runs the liquid on canvas till the returned stop is called. frac: the knob's place now (0..1).
 *  Still (reduced motion, or a screenshot being taken): one frame, nothing runs on. */
export function flow(canvas: HTMLCanvasElement, frac: () => number, still: boolean) {
  canvas.width = W;
  canvas.height = H;
  const g = canvas.getContext("2d");
  if (!g) return () => {};
  const img = g.createImageData(W, H);
  const s: Slosh = { push: 0, vel: 0 };
  let t = Math.random() * 100;
  let last = frac();
  let then = 0;
  let id = 0;
  const draw = () => {
    frame(img.data, t, Math.min(1, frac() * 0.8 + Math.abs(s.vel) * 0.6), s.push);
    g.putImageData(img, 0, 0);
  };
  const tick = (now: number) => {
    const dt = then ? Math.min(0.05, (now - then) / 1000) : 0;
    then = now;
    const f = frac();
    // The knob moved: the liquid is thrown the way it went, and runs faster a while.
    if (f !== last) {
      s.vel += (f - last) * 1.5;
      last = f;
    }
    slosh(s, dt);
    t += dt * (rate(f) + Math.min(1.5, Math.abs(s.vel) * 4));
    draw();
    id = requestAnimationFrame(tick);
  };
  if (still) draw();
  else id = requestAnimationFrame(tick);
  return () => cancelAnimationFrame(id);
}
