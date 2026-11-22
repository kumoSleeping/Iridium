// The picture viewer's sums (src/chat/zoom-lib.ts).
import { expect, test } from "vitest";
import { bound, maxScale, zoomAt } from "../src/chat/zoom-lib";

test("zoomAt keeps the point under the pointer", () => {
  const at = { x: 120, y: -40 };
  const o = zoomAt({ x: 10, y: 5 }, 1, 3, at);
  // The picture's point under `at` before: (at - offset) / 1; after: offset' + that * 3 = at.
  expect(o.x + (at.x - 10) * 3).toBeCloseTo(at.x);
  expect(o.y + (at.y - 5) * 3).toBeCloseTo(at.y);
  expect(zoomAt({ x: 0, y: 0 }, 2, 2, at)).toEqual({ x: 0, y: 0 });
});

test("bound: at most 24px past an edge; a picture narrower than the window stays in the middle", () => {
  expect(bound({ x: 9999, y: -9999 }, 2, 1000, 500, 1200, 800)).toEqual({ x: (2000 - 1200) / 2 + 24, y: -((1000 - 800) / 2 + 24) });
  expect(bound({ x: 50, y: 50 }, 1.5, 400, 300, 1200, 800)).toEqual({ x: 0, y: 0 });
});

test("maxScale: five times its fit, or twice its own pixels if that is more", () => {
  expect(maxScale(1)).toBe(5);
  expect(maxScale(0.25)).toBe(8);
  expect(maxScale(0)).toBe(5);
});
