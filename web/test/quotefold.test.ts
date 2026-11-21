// Quotes as QQ has them, merged forwards and folded messages (kumo, 2026-11-21): the pure parts
// (chat/channel/lib.ts insertWords, quoteAt; chat/boxed.ts members named; chat/forward.ts;
// chat/channel/folds.ts; chat/channel/jump.ts around).
import { describe, expect, test } from "vitest";
import type { ChannelMessage, ChannelPage } from "../src/api/client";
import { boxed, edited, outOf, wordsAt } from "../src/chat/boxed";
import { insertNick, insertWords, msgOf, quoteAt, startsAt, type Msg } from "../src/chat/channel/lib";
import { oldRead, recentFrom } from "../src/chat/channel/folds";
import { around } from "../src/chat/channel/jump";
import { ChannelLog } from "../src/chat/channel/log";
import { forwardPlain, msgWords, splitForwards, withForwards } from "../src/chat/forward";

describe("a mention put in", () => {
  test("one space before it unless at a line's start or after one, one after it", () => {
    expect(insertWords("", 0, 0, "@ann")).toEqual({ text: "@ann ", caret: 5 });
    expect(insertWords("hi", 2, 2, "@ann")).toEqual({ text: "hi @ann ", caret: 8 });
    expect(insertWords("hi there", 2, 2, "@ann")).toEqual({ text: "hi @ann there", caret: 8 });
    expect(insertWords("hi\nthere", 3, 3, "@ann")).toEqual({ text: "hi\n@ann there", caret: 8 });
    expect(insertWords("你好@a", 2, 4, "@ann")).toEqual({ text: "你好 @ann ", caret: 8 });
    expect(insertNick("say ", 4, 4, "pi")).toEqual({ text: "say @pi ", caret: 8 });
  });
  test("a quote's @ at the start, once", () => {
    expect(insertNick("look", 0, 0, "pi").text).toBe("@pi look");
    expect(insertNick(" look", 0, 0, "pi").text).toBe("@pi look");
    expect(startsAt("@pi look", "pi")).toBe(true);
    expect(startsAt("  @PI look", "pi")).toBe(true);
    expect(startsAt("@pi2 look", "pi")).toBe(false);
    expect(startsAt("look @pi", "pi")).toBe(false);
    expect(startsAt("@Ann Lee hi", "Ann Lee")).toBe(true);
  });
  test("whom a quote names: an agent by its nick, another's message by its name, not one's own", () => {
    const agent: Msg = { n: 1, text: "", files: [], author: { kind: "agent", session: "s", nick: "pi", name: "Pi" } };
    const person: Msg = { n: 2, text: "", files: [], author: { kind: "person", id: "u", name: "Ann" } };
    expect(quoteAt(agent, false)).toBe("pi");
    expect(quoteAt(person, false)).toBe("Ann");
    expect(quoteAt(person, true)).toBeUndefined();
    expect(quoteAt({ author: { kind: "script", name: "x" } }, false)).toBeUndefined();
    expect(quoteAt({ author: { kind: "iridium" } }, false)).toBeUndefined();
    expect(quoteAt({ author: { kind: "agent", session: "s", nick: "", name: "Pi" } }, false)).toBeUndefined();
  });
});

describe("members named in the composer's box", () => {
  const names = ["pi", "Ann Lee"];
  test("one piece each, its letters the words' own", () => {
    const b = boxed("hi @pi and @Ann Lee!", names);
    expect(b.value).toBe("hi @pi and @Ann Lee!");
    expect(b.refs).toMatchObject([
      { from: 3, to: 6, at: 3, end: 6, m: { kind: "nick", nick: "pi" } },
      { from: 11, to: 19, m: { kind: "nick", nick: "Ann Lee" } },
    ]);
    expect(wordsAt(b, 6)).toBe(6);
    // Not named without the list of names, nor as part of a longer word.
    expect(boxed("hi @pi", []).refs).toEqual([]);
    expect(boxed("hi @pix", names).refs).toEqual([]);
  });
  test("an edit touching one takes it whole; a letter typed inside goes after it", () => {
    expect(edited("hi @pi x", "hi @p x", 5, names)).toEqual({ words: "hi  x", caret: 3 });
    expect(edited("hi @pi x", "hi @pzi x", 5, names)).toEqual({ words: "hi @piz x", caret: 7 });
    expect(edited("hi @pi x", "hi @pi! x", 7, names)).toEqual({ words: "hi @pi! x", caret: 7 });
    expect(edited("hi @pi x", "hi @pi x", 6, names)).toEqual({ words: "hi @pi x", caret: 6 });
  });
  test("an arrow stepping in from one edge goes on past it (it could not get past before)", () => {
    const b = boxed("a @pi b", names); // the name is 2..5
    expect(outOf(b, 3, 2)).toBe(5); // → from its start
    expect(outOf(b, 4, 5)).toBe(2); // ← from its end
    expect(outOf(b, 4)).toBe(5); // a press inside: the nearer edge
    expect(outOf(b, 3)).toBe(2);
    expect(outOf(b, 1, 0)).toBe(1);
  });
});

describe("merged forwards", () => {
  const fwd = `<message forward><message id="1"><author id="u1" name="Ann"/>hello<br/>a &lt; b</message><message><author id="u2" name="Bob"/><message forward><message><author name="Cy"/>inner</message></message></message></message>`;
  test("taken out of the words, a card each, one inside a message its own", () => {
    const { words, forwards } = splitForwards(`look at this\n${fwd}\nok`);
    expect(words).toBe("look at this\n\nok");
    expect(forwards).toHaveLength(1);
    expect(forwards[0]!.msgs).toMatchObject([
      { id: "1", who: "Ann", user: "u1", text: "hello\na < b", forwards: [] },
      { who: "Bob", text: "", forwards: [{ msgs: [{ who: "Cy", text: "inner" }] }] },
    ]);
    expect(forwardPlain(forwards[0]!)).toBe("Ann: hello\n  a < b\nBob:\n  Cy: inner");
    expect(withForwards("see", forwards)).toBe("see\n\nAnn: hello\n  a < b\nBob:\n  Cy: inner");
  });
  test("in code, or never closed, it is words", () => {
    for (const s of ["`" + fwd + "`", "```\n" + fwd + "\n```", "<message forward><message>open", "a forward of mine"]) expect(splitForwards(s)).toEqual({ words: s, forwards: [] });
  });
  test("a channel's message read: its words apart from its forward, kept per message", () => {
    const content = (fwd + " thanks").replaceAll("&", "&amp;").replaceAll("<", "&lt;").replaceAll(">", "&gt;").replaceAll('"', "&quot;");
    const m = msgOf({ id: "7", content, user: { id: "u", name: "Ann" } } as ChannelMessage)!;
    const w = msgWords(m);
    expect(w.words).toBe("thanks");
    expect(w.forwards[0]!.msgs.map((x) => x.who)).toEqual(["Ann", "Bob"]);
    expect(msgWords(m)).toBe(w);
  });
});

describe("old words folded of themselves", () => {
  const long = "1\n2\n3\n4\n5\n6\n7";
  const msg = (n: number, kind: "person" | "agent", text = "short"): Msg => ({ n, text, files: [], author: kind === "person" ? { kind, id: "u", name: "Ann" } : { kind, session: "s", nick: "pi", name: "Pi" } });
  test("the latest rounds begin at the rounds-th last person's message", () => {
    expect(recentFrom([true, false, true, false, true], 2)).toBe(2);
    expect(recentFrom([true, false, true], 5)).toBe(0);
    expect(recentFrom([], 5)).toBe(0);
  });
  test("long, read, and before the latest five rounds", () => {
    const ms: Msg[] = [msg(1, "person", long), msg(2, "agent", long), msg(3, "agent")];
    for (let r = 0; r < 5; r++) ms.push(msg(4 + 2 * r, "person"), msg(5 + 2 * r, "agent", long));
    expect([...oldRead(ms, 100)]).toEqual([1, 2]);
    // Not read yet: not folded.
    expect([...oldRead(ms, 1)]).toEqual([1]);
    // Fewer rounds than five: none old.
    expect([...oldRead(ms.slice(0, 9), 100)]).toEqual([]);
  });
});

describe("a quote gone to", () => {
  const person = (n: number): ChannelMessage => ({ id: String(n), content: `m${n}`, user: { id: "local", name: "Iridium" }, created_at: n });
  test("the log around the message, and all between it and what is held", async () => {
    const all = Array.from({ length: 30 }, (_, i) => person(i + 1));
    const n = (m: ChannelMessage) => Number(m.id);
    const page = (data: ChannelMessage[], more: boolean): ChannelPage => ({ data, start: n(data[0] ?? { id: "0", content: "" }), end: n(data.at(-1) ?? { id: "0", content: "" }), more });
    const log = new ChannelLog("c", async (_, q) => page(all.filter((m) => (q.before === undefined || n(m) < q.before)).slice(-5), true));
    await log.newer();
    expect(log.msgs.map((m) => m.n)).toEqual([26, 27, 28, 29, 30]);
    const asked: object[] = [];
    const got = await around(log, 5, async (_, q) => {
      asked.push(q);
      if (q.around !== undefined) return page(all.filter((m) => n(m) >= q.around! - 3 && n(m) <= q.around! + 3), true);
      return page(all.filter((m) => n(m) > q.after!).slice(0, 10), true);
    });
    expect(got).toBe(true);
    expect(log.msgs.map((m) => m.n)).toEqual(Array.from({ length: 29 }, (_, i) => i + 2));
    expect(log.more).toBe(true);
    expect(asked).toEqual([{ around: 5 }, { after: 8, limit: 500 }, { after: 18, limit: 500 }]);
    // Held already: nothing asked.
    expect(await around(log, 10, async () => page([], false))).toBe(false);
  });
});
