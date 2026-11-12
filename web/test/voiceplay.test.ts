// The playing side's queue (src/voice/play.worklet.js), run outside a browser: the audio thread's
// names stood in for.
import { beforeAll, expect, test } from "vitest";

const RATE = 48000;
type Port = { onmessage: ((e: { data: unknown }) => void) | null; postMessage: (m: unknown) => void };
let Play: new () => { port: Port; process: (i: unknown, o: Float32Array[][]) => boolean };

beforeAll(async () => {
  const g = globalThis as Record<string, unknown>;
  g.sampleRate = RATE;
  g.AudioWorkletProcessor = class {
    port: Port = { onmessage: null, postMessage: () => {} };
  };
  g.registerProcessor = (_: string, c: typeof Play) => (Play = c);
  // @ts-expect-error plain JS, no types
  await import("../src/voice/play.worklet.js");
});

/** n samples at full level: any step in what is played is the queue's doing. */
const ones = (n: number) => new Float32Array(n).fill(1);
function run(p: InstanceType<typeof Play>, quanta: number) {
  const all: number[] = [];
  for (let i = 0; i < quanta; i++) {
    const out = new Float32Array(128);
    p.process([], [[out]]);
    all.push(...out);
  }
  return all;
}
const push = (p: InstanceType<typeof Play>, pcm: Float32Array, target = 1920, max = 9600) => p.port.onmessage!({ data: { id: 1, pcm, target, max } });
const steepest = (xs: number[]) => xs.reduce((m, x, i) => Math.max(m, Math.abs(x - (xs[i - 1] ?? 0))), 0);

test("words start once enough of them wait, and begin and end without a step", () => {
  const p = new Play();
  push(p, ones(1920), 3840);
  expect(Math.max(...run(p, 10))).toBeLessThan(0.001); // one frame of two: waiting
  push(p, ones(1920), 3840);
  const heard = run(p, 40);
  expect(Math.max(...heard)).toBeGreaterThan(0.95);
  expect(heard.filter((x) => x > 0.001).length).toBe(3840); // all of it
  expect(steepest(heard)).toBeLessThan(0.01);
});

test("a frame that comes alone is not played; short words are", () => {
  const p = new Play();
  push(p, ones(1920), 3840);
  expect(Math.max(...run(p, 60))).toBeLessThan(0.001);
  // The next words are not behind it.
  push(p, ones(1920), 3840);
  push(p, ones(1920), 3840);
  expect(run(p, 40).filter((x) => x > 0.001).length).toBe(3840);
  // Three frames, the target five: played once no more come.
  const q = new Play();
  for (let i = 0; i < 3; i++) push(q, ones(1920), 9600, 19200);
  expect(run(q, 200).filter((x) => x > 0.001).length).toBe(5760);
});

test("a queue too far behind drops its oldest, and comes in again without a step", () => {
  const p = new Play();
  const said: { late: number }[] = [];
  p.port.postMessage = (m) => said.push(m as { late: number });
  for (let i = 0; i < 2; i++) push(p, ones(1920));
  const heard = run(p, 5);
  for (let i = 0; i < 6; i++) push(p, ones(1920)); // a second's worth at once, past max
  heard.push(...run(p, 400));
  expect(steepest(heard)).toBeLessThan(0.01);
  expect(said.reduce((n, m) => n + m.late, 0)).toBeGreaterThan(0);
  expect(heard.filter((x) => x > 0.001).length).toBeLessThan(8 * 1920);
});

test("a long queue lets go of the pauses at its head, and only those", () => {
  const p = new Play();
  const said: { skipped: number }[] = [];
  p.port.postMessage = (m) => said.push(m as { skipped: number });
  // Words, a pause of five chunks, words: more than target and its slack waits.
  const words = () => push(p, ones(960).fill(0.5), 1920, 96000);
  const pause = () => push(p, new Float32Array(960), 1920, 96000);
  words();
  words();
  for (let i = 0; i < 5; i++) pause();
  for (let i = 0; i < 4; i++) words();
  const heard = run(p, 400);
  expect(heard.filter((x) => x > 0.4).length).toBeGreaterThan(6 * 960 - 1000); // every word, but the fades
  expect(said.reduce((n, m) => n + m.skipped, 0)).toBeGreaterThan(0);
  expect(heard.findLastIndex((x) => x > 0.001)).toBeLessThan(11 * 960); // sooner than it all would take
});

test("what is mixed is bent under full level, never over it", () => {
  const p = new Play();
  for (const id of [1, 2, 3]) for (let i = 0; i < 2; i++) p.port.onmessage!({ data: { id, pcm: ones(1920).fill(0.6), target: 1920, max: 9600 } });
  const heard = run(p, 40);
  expect(Math.max(...heard)).toBeLessThanOrEqual(1);
  expect(Math.max(...heard)).toBeGreaterThan(0.9);
});
