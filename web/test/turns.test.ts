import { expect, test } from "vitest";
import { earlierOf, turnsOf, type Row } from "../src/chat/turns";

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
