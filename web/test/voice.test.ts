// A voice frame's head and the numbers measured from it (src/voice/wire.ts).
import { expect, test } from "vitest";
import { Clock, HEADER, low32, pack, pct, playout, since, unpack } from "../src/voice/wire";

test("a frame comes back as it was packed, the sender's number before it", () => {
  const sent = pack(65537 + 7, 2 ** 32 + 1234.9, new Uint8Array([9, 8, 7]));
  expect(sent.length).toBe(HEADER + 3);
  const passed = new Uint8Array(2 + sent.length);
  new DataView(passed.buffer).setUint16(0, 3);
  passed.set(sent, 2);
  const f = unpack(passed.buffer)!;
  expect([f.from, f.seq, f.clock, [...f.opus]]).toEqual([3, 8, 1234, [9, 8, 7]]);
  expect(unpack(new Uint8Array(2 + HEADER).buffer)).toBeNull(); // no audio in it
  passed[2] = 9;
  expect(unpack(passed.buffer)).toBeNull(); // another kind
});

test("how long a frame took is right across the clock's wrap", () => {
  expect(since(low32(2 ** 32 + 50), low32(2 ** 32 - 100))).toBe(150);
  expect(since(low32(1000), low32(1040))).toBe(-40);
});

test("the room's clock is taken from the quickest round trip", () => {
  const c = new Clock();
  expect(c.ready).toBe(false);
  c.pong(0, 400, 5300); // slow, lopsided: would say 5100
  c.pong(1000, 1040, 6020); // quick: the room is 5000 ahead
  c.pong(2000, 2200, 7150);
  expect(c.offset).toBe(5000);
  expect(c.rtt).toBe(200);
});

test("a speaker's words wait as long as its frames' delays spread, a frame and a little over it", () => {
  // Not measured yet: a guess.
  expect(playout([], 40)).toEqual({ target: 120, max: 360 });
  // A steady link: a frame and a little.
  expect(playout(Array(50).fill(300), 40).target).toBe(60);
  // Jitter of 190 ms, one frame in a hundred far out: that one is not waited for, the rest are.
  const jitter = [...Array.from({ length: 99 }, (_, i) => 300 + (i % 20) * 10), 5000];
  const p = playout(jitter, 40);
  expect(p.target).toBeGreaterThanOrEqual(240);
  expect(p.target).toBeLessThanOrEqual(260);
  expect(p.max).toBe(p.target + p.target);
  // Never past 800 ms.
  expect(playout([0, 0, 0, 0, 3000, 3000, 3000, 3000], 40).target).toBe(800);
  expect(pct([], 0.5)).toBeNull();
});
