// A conversation come back to (chat/channel/lazyrows.tsx): which rows stand as boxes in its first
// frame, which are drawn next; and the list's rows kept the same objects (store.ts sharedRows).
import { describe, expect, it } from "vitest";
import { boxes, nextRows } from "../src/chat/channel/lazyrows";
import { sharedRows } from "../src/store";

type G = { key: string; v: number };
const rows = (n: number): G[] => Array.from({ length: n }, (_, i) => ({ key: `r${i}`, v: 0 }));
const seenOf = (gs: G[], h = 100, w = 800) => new Map(gs.map((g) => [g.key, { g, h, w }]));
const same = (a: G, b: G) => a.key === b.key && a.v === b.v;

describe("boxes", () => {
  it("at its end: the rows within what is seen and a pane and a half over it are drawn, the rest boxes", () => {
    const gs = rows(100);
    const b = boxes(gs, seenOf(gs), same, null, 1000, 800)!;
    // 2500px from the end at 100px a row: the last 26 rows drawn (the one that crosses the edge too).
    expect(b.size).toBe(74);
    expect(b.has("r73")).toBe(true);
    expect(b.has("r74")).toBe(false);
    expect(b.get("r0")).toBe(100);
  });
  it("none when it was never drawn here, at another width, or too few are left", () => {
    const gs = rows(100);
    expect(boxes(gs, new Map(), same, null, 1000, 800)).toBeNull();
    expect(boxes(gs, seenOf(gs, 100, 700), same, null, 1000, 800)).toBeNull();
    expect(boxes(gs, seenOf(gs), same, null, 1000, 0)).toBeNull();
    expect(boxes(rows(27), seenOf(rows(27)), same, null, 1000, 800)).toBeNull();
  });
  it("a row that is another now is drawn, and counts for nothing in what is under the rest", () => {
    const gs = rows(100);
    const seen = seenOf(gs);
    gs[99] = { key: "r99", v: 1 };
    gs[10] = { key: "r10", v: 1 };
    const b = boxes(gs, seen, same, null, 1000, 800)!;
    expect(b.has("r10")).toBe(false);
    expect(b.has("r9")).toBe(true);
    // The last row not known: one more row drawn over the rest.
    expect(b.has("r72")).toBe(true);
    expect(b.has("r73")).toBe(false);
  });
  it("left up from its end: the rows round where it was left are drawn", () => {
    const gs = rows(100);
    const b = boxes(gs, seenOf(gs), same, 5000, 1000, 800)!;
    // Rows at 100px: what is seen is rows 50..59; a pane and a half over, a pane under.
    expect(b.has("r33")).toBe(true);
    expect(b.has("r34")).toBe(false);
    expect(b.has("r70")).toBe(false);
    expect(b.has("r71")).toBe(true);
  });
  it("left up from its end, a row not known: under what is drawn, the rest still boxes; over it, every row drawn", () => {
    const gs = rows(100);
    const seen = seenOf(gs);
    gs[99] = { key: "r99", v: 1 };
    const b = boxes(gs, seen, same, 5000, 1000, 800)!;
    expect(b.has("r99")).toBe(false);
    expect(b.has("r98")).toBe(true);
    expect(b.has("r10")).toBe(true);
    gs[20] = { key: "r20", v: 1 };
    expect(boxes(gs, seen, same, 5000, 1000, 800)).toBeNull();
  });
});

describe("nextRows", () => {
  it("the boxes nearest what is drawn, both ways, about a chunk of them", () => {
    const gs = rows(100);
    const left = new Map(gs.flatMap((g, i) => (i < 40 || i > 60 ? [[g.key, 100] as const] : [])));
    const next = nextRows(gs, left);
    expect(next.slice(0, 4)).toEqual(["r39", "r61", "r38", "r62"]);
    expect(next.length).toBe(8);
  });
  it("a box between rows drawn goes first; the last ones are all taken", () => {
    const gs = rows(10);
    expect(nextRows(gs, new Map([["r0", 100], ["r5", 100]]))).toEqual(["r5", "r0"]);
    const all = new Map(gs.map((g) => [g.key, 5000]));
    expect(nextRows(gs, all)).toEqual(["r9"]);
  });
});

describe("sharedRows", () => {
  it("a row as it was is the object it was; one changed, or new, is the new one", () => {
    const was = new Map<string, { raw: string; row: { id: string; n: number } }>();
    const a = sharedRows([{ id: "a", n: 1 }, { id: "b", n: 1 }], was);
    const b = sharedRows([{ id: "a", n: 1 }, { id: "b", n: 2 }, { id: "c", n: 1 }], was);
    expect(b[0]).toBe(a[0]);
    expect(b[1]).not.toBe(a[1]);
    expect(b[1]).toEqual({ id: "b", n: 2 });
    expect(was.size).toBe(3);
    const c = sharedRows([{ id: "c", n: 1 }], was);
    expect(c[0]).toBe(b[2]);
    expect(was.size).toBe(1);
  });
});
