// Reactions and messages taken back (docs/channels.md "表情" "撤回"): src/chat/reactions.ts,
// emoji.ts, and what the log and the record do with them (channel/log.ts, lib.ts).
import { describe, expect, test } from "vitest";
import type { ChannelMessage, ChannelPage } from "../src/api/client";
import { givenTurn, msgOf } from "../src/chat/channel/lib";
import { ChannelLog } from "../src/chat/channel/log";
import { bumped, FIRST, recentEmoji, ROW, ROWS } from "../src/chat/emoji";
import { blockKey, blockSeq, hear, isRecalled, keysOf, reacted, reactionLine, reactionsOf, refOf, seqOf } from "../src/chat/reactions";

describe("reactions", () => {
  test("put on and taken off: each emoji once, each member once in it, none left goes", () => {
    let l = reacted([], "👍", { id: "kumo" }, true);
    l = reacted(l, "👍", { id: "bob", name: "Bob" }, true);
    l = reacted(l, "🎉", { id: "kumo" }, true);
    expect(l).toEqual([
      { emoji: "👍", count: 2, users: [{ id: "kumo" }, { id: "bob", name: "Bob" }] },
      { emoji: "🎉", count: 1, users: [{ id: "kumo" }] },
    ]);
    expect(reacted(l, "👍", { id: "kumo" }, true)).toBe(l);
    expect(reacted(l, "👍", { id: "nobody" }, false)).toBe(l);
    l = reacted(l, "🎉", { id: "kumo" }, false);
    expect(l.map((r) => r.emoji)).toEqual(["👍"]);
    expect(reacted(l, "👍", { id: "bob" }, false)[0]).toEqual({ emoji: "👍", count: 1, users: [{ id: "kumo" }] });
  });
  test("a message's blocks in its session's record: its agent's words by place, a person's by input", () => {
    expect(keysOf("s1", { session: "s1", block: "40.0", input: "in-1" })).toEqual(["b:40.0", "i:in-1"]);
    expect(keysOf("s1", { session: "s2", block: "40.0" })).toEqual([]);
    expect(blockKey({ at: "40.0" })).toBe("b:40.0");
    expect(refOf("s1", { at: "40.0" })).toBe("b:s1:40.0");
    expect(refOf("s1", { at: "10.0", input: "in-1" })).toBe("in-1");
  });
  test("the events: what is put on and taken off, which block is which, what is taken back", () => {
    hear({ type: "reaction-added", channel: { id: "c9" }, message: { id: "4", content: "", _data: { session: "c9", block: "70.0" } }, user: { id: "kumo", name: "kumo" }, emoji: { id: "👍" } });
    hear({ type: "reaction-added", channel: { id: "c9" }, message: { id: "4", content: "" }, user: { id: "agent:s2", name: "cc" }, emoji: { id: "👍" } });
    expect(reactionsOf("c9", 4)).toEqual([{ emoji: "👍", count: 2, users: [{ id: "kumo", name: "kumo" }, { id: "agent:s2", name: "cc" }] }]);
    expect(seqOf("c9", "b:70.0")).toBe(4);
    expect(blockSeq("c9", { at: "70.0" })).toBe(4);
    hear({ type: "message-created", channel: { id: "c9" }, message: { id: "5", content: "hi", _data: { kind: "user", input: "in-5" } } });
    expect(blockSeq("c9", { at: "80.0", input: "in-5" })).toBe(5);
    hear({ type: "reaction-removed", channel: { id: "c9" }, message: { id: "4", content: "" }, user: { id: "kumo" }, emoji: { id: "👍" } });
    expect(reactionsOf("c9", 4)[0]?.count).toBe(1);
    hear({ type: "message-deleted", channel: { id: "c9" }, message: { id: "4", content: "", _data: { session: "c9", block: "70.0" } } });
    expect(isRecalled("c9", 4)).toBe(true);
    expect(reactionsOf("c9", 4)).toEqual([]);
    // A line of Iridium's own that says so, though the event was missed.
    hear({ type: "message-created", channel: { id: "c9" }, message: { id: "9", content: "kumo 撤回了一条消息", _data: { kind: "note", event: { type: "recall", seq: 6, by: "kumo" } } } });
    expect(isRecalled("c9", 6)).toBe(true);
  });
});

describe("the emoji", () => {
  test("pressed lately first, each once; filled up to two rows", () => {
    expect(bumped(["👍", "❤️", "😂"], "😂")).toEqual(["😂", "👍", "❤️"]);
    expect(bumped(FIRST, "🦄")).toHaveLength(ROW * ROWS);
    expect(recentEmoji(null)).toEqual(FIRST);
    expect(recentEmoji(JSON.stringify(["🦄", "👍"])).slice(0, 3)).toEqual(["🦄", "👍", "❤️"]);
    expect(recentEmoji("not json")).toEqual(FIRST);
  });
});

describe("a message taken back", () => {
  const person = (n: number, text = "hi"): ChannelMessage => ({ id: String(n), content: text, user: { id: "kumo", name: "kumo" }, created_at: n });
  const recallNote = (n: number, seq: number): ChannelMessage => ({ id: String(n), content: "kumo 撤回了一条消息", user: { id: "iridium", name: "Iridium" }, _data: { kind: "note", event: { type: "recall", seq, by: "kumo" } } });
  test("out of the log by its event, and by the line that says so; not put back by a page read after", async () => {
    const all = [person(1), person(2), person(3)];
    const get = async (_: string, q: { before?: number; after?: number }): Promise<ChannelPage> => {
      const data = all.filter((m) => q.after === undefined || Number(m.id) > q.after);
      return { data, start: 1, end: Number(data.at(-1)?.id ?? 0), more: false };
    };
    const log = new ChannelLog("c", get);
    await log.newer();
    log.heard(person(2), true);
    expect(log.msgs.map((m) => m.n)).toEqual([1, 3]);
    log.take({ data: [person(1), person(2), person(3)], start: 1, end: 3, more: false });
    expect(log.msgs.map((m) => m.n)).toEqual([1, 3]);
    all.push(recallNote(4, 1));
    await log.newer();
    expect(log.msgs.map((m) => m.n)).toEqual([3, 4]);
  });
  test("its line: an event of its own", () => {
    expect(msgOf(recallNote(4, 1))).toMatchObject({ n: 4, author: { kind: "iridium" }, event: { type: "recall", seq: 1, by: "kumo" } });
  });
});

describe("a reaction an agent was given", () => {
  test("in a channel of several, as a message it was given; alone, its line", () => {
    const g = givenTurn("#7 [kumo reacted 👍 to: the tests pass]\n\n<channel-context>\nYou are a in a channel shared with people\n</channel-context>");
    expect(g).toMatchObject({ seq: 7, words: "[kumo reacted 👍 to: the tests pass]" });
    expect(givenTurn("#7: words\n\n<channel-context>\nx\n</channel-context>")).toMatchObject({ seq: 7, words: "words" });
    expect(reactionLine("#2 [Alice B reacted 🎉 to: done, and more]")).toEqual({ seq: 2, who: "Alice B", emoji: "🎉", words: "done, and more" });
    expect(reactionLine("#2: [x reacted 🎉 to: y]")).toBeNull();
  });
});
