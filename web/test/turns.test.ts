import { expect, test } from "vitest";
import { earlierOf, liveAt, rowPlace, slotAfter, tailOf, turnKey, turnsOf, type Row } from "../src/chat/turns";

const row = (key: string, agent: boolean, works?: string): Row<string, string> => ({ key, agent, node: key, ...(works ? { works } : {}) });

test("the agent's rows between two messages are one turn, a call's work one that folds", () => {
  const turns = turnsOf([row("a0", true), row("u1", false), row("a1", true), row("a2", true), row("c1", false, "ch"), row("a3", true), row("a4", true), row("u2", false), row("c2", false, "ch")]);
  expect(turns.map((x) => [x.kind, x.key, x.kind === "one" ? 0 : x.rows.length])).toEqual([
    ["agent", "start:a", 1],
    ["one", "u1", 0],
    ["agent", "u1:a", 2],
    ["works", "c1", 2],
    ["one", "u2", 0],
    ["works", "c2", 0],
  ]);
});

test("what a turn did before its last words folds once it is more than a line", () => {
  const words = (x: string) => x.startsWith("w");
  expect(earlierOf(["s1", "w1"], words)).toBeNull();
  expect(earlierOf(["w1", "s1"], words)).toBeNull();
  expect(earlierOf(["s1", "s2"], words)).toBeNull();
  expect(earlierOf(["s1", "s2", "w1", "s3"], words)).toEqual({ before: ["s1", "s2"], after: ["w1", "s3"], words: 0 });
  expect(earlierOf(["w1", "w2"], words)).toEqual({ before: ["w1"], after: ["w2"], words: 1 });
});

test("the turn at work is the last that is not others' words: what they say meanwhile is under it", () => {
  const other = (k: string) => k.startsWith("ch:");
  const turns = turnsOf([row("u1", false), row("a1", true), row("ch:m5", false), row("ch:m6", false)]);
  expect(tailOf(turns, other)).toBe(1);
  expect(tailOf(turnsOf([row("u1", false), row("ch:m5", false)]), other)).toBe(0);
  expect(tailOf(turnsOf([row("ch:m5", false)]), other)).toBe(-1);
  expect(tailOf(turnsOf([row("u1", false), row("a1", true)]), other)).toBe(1);
});

test("a turn seen at work stays the one at work as words come under it; one begun with words already there is a new one", () => {
  // At work, nothing under it; then words come under it: still that turn.
  const a = liveAt(null, true, "u1:a", false, true);
  expect(a).toEqual({ key: "u1:a", under: false });
  expect(liveAt(a, true, "u1:a", true, true)).toBe(a);
  // At rest with words under the last turn, then at work: a turn to come, under them, till what
  // began it is in the record (another key).
  const b = liveAt(null, true, "u1:a", true, true);
  expect(b).toEqual({ key: "u1:a", under: true });
  expect(liveAt(b, true, "u9:a", false, true)).toEqual({ key: "u9:a", under: false });
  // Opened while at work (never seen at rest): the turn there is the one at work.
  expect(liveAt(null, true, "u1:a", true, false)).toEqual({ key: "u1:a", under: false });
  expect(liveAt(a, false, "u1:a", true, true)).toBeNull();
});

test("a subagent stays after the turn it began at, whatever comes under it", () => {
  const before = turnsOf([row("u1", false), row("a1", true)]);
  const key = turnKey(before.at(-1)!);
  expect(slotAfter(before, key)).toBe(2);
  // You say more, the agent goes on: it is still after that turn, over the rest.
  const after = turnsOf([row("u1", false), row("a1", true), row("u2", false), row("a2", true)]);
  expect(slotAfter(after, key)).toBe(2);
  // Begun as the turn had no row yet (its key is the turn's to come).
  const none = turnsOf([row("u1", false)]);
  expect(slotAfter(after, turnKey(none[0]!))).toBe(2);
  expect(slotAfter(after, "gone")).toBe(4);
});

test("right after the turn at work: under its row at work, over what you sent into it; over what waits to begin a turn", () => {
  // The turn at work is 3: itself at 3, what waits to begin one at 3.1, its row at work 3.2, what you sent into it 3.3.
  expect(rowPlace(4, 3, false)).toBeGreaterThan(3.2);
  expect(rowPlace(4, 3, false)).toBeLessThan(3.3);
  expect(rowPlace(4, 3, true)).toBeLessThan(3.1);
  expect(rowPlace(4, 3, true)).toBeGreaterThan(3);
  expect(rowPlace(2, 3, false)).toBe(1.5);
  expect(rowPlace(6, 3, false)).toBe(5.5);
});
