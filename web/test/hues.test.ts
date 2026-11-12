import { describe, expect, it } from "vitest";
import { apart, mainHue, spread } from "../src/hues";

/** n pixels of one colour, then m of another. */
const px = (...parts: [r: number, g: number, b: number, n: number][]) => {
  const out: number[] = [];
  for (const [r, g, b, n] of parts) for (let i = 0; i < n; i++) out.push(r, g, b, 255);
  return new Uint8ClampedArray(out);
};

describe("mainHue", () => {
  it("is the hue most of the coloured pixels have", () => {
    // Mostly grey, a good part orange, a little blue: orange (OKLCH hue about 45–60°).
    const h = mainHue(px([128, 128, 128, 600], [235, 104, 52, 300], [42, 120, 214, 100]))!;
    expect(h).toBeGreaterThan(30);
    expect(h).toBeLessThan(70);
  });
  it("is none for a grey, black and white image", () => {
    expect(mainHue(px([20, 20, 20, 300], [250, 250, 250, 300], [128, 128, 128, 400]))).toBeNull();
  });
});

describe("spread", () => {
  it("keeps hues far apart as they are", () => {
    const out = spread([
      { id: "a", hue: 30 },
      { id: "b", hue: 200 },
    ]);
    expect(out.get("a")).toBe(30);
    expect(out.get("b")).toBe(200);
  });
  it("moves one too near another away, near its own", () => {
    const out = spread([
      { id: "a", hue: 30 },
      { id: "b", hue: 40 },
    ]);
    expect(out.get("a")).toBe(30);
    expect(apart(out.get("b")!, 30)).toBeGreaterThanOrEqual(40);
    expect(apart(out.get("b")!, 40)).toBeLessThanOrEqual(35);
  });
  it("keeps many apart and gives the same on every call", () => {
    const want = Array.from({ length: 9 }, (_, i) => ({ id: `id${i}`, hue: i % 2 ? 100 : null }));
    const out = spread(want);
    const hs = [...out.values()];
    for (let i = 0; i < hs.length; i++) for (let j = i + 1; j < hs.length; j++) expect(apart(hs[i]!, hs[j]!)).toBeGreaterThanOrEqual(20);
    expect(spread([...want].reverse())).toEqual(out);
  });
});
