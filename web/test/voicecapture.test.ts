// The capturing side's gains (src/voice/capture.worklet.js), run outside a browser.
import { beforeAll, expect, test } from "vitest";

const RATE = 48000;
type Port = { onmessage: ((e: { data: unknown }) => void) | null; postMessage: (m: { pcm: Float32Array }) => void };
let Capture: new () => { port: Port; process: (i: Float32Array[][]) => boolean };

beforeAll(async () => {
  const g = globalThis as Record<string, unknown>;
  g.sampleRate = RATE;
  g.currentFrame = 0;
  g.AudioWorkletProcessor = class {
    port: Port = { onmessage: null, postMessage: () => {} };
  };
  g.registerProcessor = (_: string, c: typeof Capture) => (Capture = c);
  // @ts-expect-error plain JS, no types
  await import("../src/voice/capture.worklet.js");
});

/** What comes out for seconds of a steady level each, in order. */
function through(c: InstanceType<typeof Capture>, parts: [number, number][], between?: (i: number) => void) {
  const out: number[] = [];
  c.port.postMessage = (m) => out.push(...m.pcm);
  parts.forEach(([level, seconds], i) => {
    between?.(i);
    for (let q = 0; q < (seconds * RATE) / 128; q++) c.process([[new Float32Array(128).fill(level)]]);
  });
  return out;
}
const steepest = (xs: number[]) => xs.reduce((m, x, i) => Math.max(m, Math.abs(x - (xs[i - 1] ?? x))), 0);

test("hushed, the sound goes down to nothing and comes back without a step", () => {
  const c = new Capture();
  const out = through(c, [[0.5, 0.2], [0.5, 0.2], [0.5, 0.2]], (i) => i && c.port.onmessage!({ data: { hush: i === 1 } }));
  expect(steepest(out)).toBeLessThan(0.01);
  expect(out[Math.round(0.35 * RATE)]).toBe(0);
  expect(out.at(-1)).toBe(0.5);
});

test("the gate lets a voice at the microphone through whole, and shuts on what is far quieter", () => {
  const c = new Capture();
  c.port.onmessage!({ data: { gate: true } });
  // A voice, then a voice across the room (far quieter) for a second, then the voice again.
  const out = through(c, [[0.3, 1], [0.02, 1], [0.3, 0.5]]);
  expect(steepest(out.slice(1.5 * RATE))).toBeLessThan(0.01); // shut and open again, never at a step
  expect(out[RATE / 2]).toBeCloseTo(0.3);
  expect(out[Math.round(1.9 * RATE)]).toBe(0); // shut, once it has held
  expect(out[Math.round(1.1 * RATE)]).toBeGreaterThan(0); // not at once: the ends of words are kept
  expect(out.at(-1)).toBeCloseTo(0.3);
  // Open again within the block the voice came back in.
  expect(out[Math.round(2 * RATE) + 960]).toBeGreaterThan(0.25);
  // Without the gate, all of it.
  const d = new Capture();
  expect(through(d, [[0.03, 0.5]]).at(-1)).toBeCloseTo(0.03);
});

test("a mark set is the one used; by itself, the mark stays well over the room's noise", () => {
  // Set high: a voice under it does not go out; over it, it does.
  const c = new Capture();
  c.port.onmessage!({ data: { gate: true, mark: 0.2 } });
  const out = through(c, [[0.1, 0.5], [0.3, 0.5]]);
  expect(out[Math.round(0.4 * RATE)]).toBe(0);
  expect(out.at(-1)).toBeCloseTo(0.3);
  // By itself, in a loud room (a fan at 0.01, nobody speaking): shut, though over the lowest mark.
  const d = new Capture();
  d.port.onmessage!({ data: { gate: true } });
  const room = through(d, [[0.01, 5], [0.2, 0.5]]);
  expect(room[Math.round(4.9 * RATE)]).toBe(0);
  expect(room.at(-1)).toBeCloseTo(0.2); // and a voice over the fan goes out
});
