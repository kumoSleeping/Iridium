// Order that changes of itself, paced (motion.ts usePaced).
import { describe, expect, test } from "vitest";
import { BEAT, draws, drawn, paceWait, type Moves } from "../src/moves";

describe("a change of order waits for the beat", () => {
  test("the first after a quiet beat is drawn at once", () => {
    expect(paceWait(1000, 5000, false)).toBe(0);
    expect(paceWait(1000, 1000 + BEAT, false)).toBe(0);
  });
  test("one that follows another within the beat waits for the beat's end", () => {
    expect(paceWait(1000, 1200, false)).toBe(BEAT - 200);
  });
  test("the user's own never waits", () => {
    expect(paceWait(1000, 1200, true)).toBe(0);
  });
});

describe("rows are read before a change of order, not at every draw", () => {
  test("the same order: nothing read; another list in the same place: nothing moves from the last one's", () => {
    const s: Moves = {};
    draws(s, null, "a b", "chat1");
    drawn(s, null, false);
    expect(s.drawn).toBe("a b");
    draws(s, null, "a b", "chat1");
    expect(s.next).toBe("a b");
    draws(s, null, "b a", "chat2");
    expect(s.drawn).toBeUndefined();
    drawn(s, null, false);
    expect(s.drawn).toBe("b a");
  });
});
