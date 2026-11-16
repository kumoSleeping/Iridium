// One page for a session, one agent or several: what of the channel's log goes where among the
// own agent's record (src/chat/channel/merge.ts).
import { describe, expect, test } from "vitest";
import type { ChannelMessage } from "../src/api/client";
import { merge, msgOf, type Msg } from "../src/chat/channel/lib";
import { betweenAgents, callerOf, FOLD_AT, FOLD_SHOW, foreignOf, givenHere, held, HOLD, linesOf, marksAt, named, ownAnchors, slotsOf } from "../src/chat/channel/merge";

const OWN = "own";
const said = (n: number, session: string, text = "hi", time = n * 10, data: ChannelMessage["_data"] = {}): Msg => msgOf({ id: String(n), content: text, user: { id: `agent:${session}`, name: "Claude", nick: session }, created_at: time, _data: { session, ...data } })!;
const person = (n: number, text = "hi", input?: string, time = n * 10): Msg => msgOf({ id: String(n), content: text, user: { id: "ir_web/k", name: "kumo" }, created_at: time, _data: input ? { input } : {} })!;
const note = (n: number, time = n * 10): Msg => msgOf({ id: String(n), content: "x joined", user: { id: "iridium", name: "Iridium" }, created_at: time })!;

describe("what of the log the record does not hold", () => {
  test("not the own agent's words, nor a person's it was given; others', a person's to another, Iridium's lines", () => {
    const ms = [person(1, "to all", "in1"), said(2, OWN), said(3, "codex"), person(4, "@codex only", "in2"), note(5)];
    expect(foreignOf(ms, OWN, new Set(["in1"])).map((m) => m.n)).toEqual([3, 4, 5]);
  });
  test("a person's read from a record (no input) is the record's too", () => {
    const typed = msgOf({ id: "1", content: "typed", user: { id: "terminal", name: "t" }, created_at: 1, _data: {} })!;
    expect(typed.input).toBeUndefined();
    expect(foreignOf([typed], OWN, new Set())).toEqual([]);
  });
  test("yours to another agent only, heard live then read: kept, its input known (#8 @pi12 vanished for its sender)", () => {
    // As it was heard: an event's message once had no _data (no input), so it was taken for words
    // typed at the agent's terminal and left out; reading it again did not put its input in.
    const heard = person(8, "@pi12 look", undefined);
    const read = person(8, "@pi12 look", "in8");
    const held = merge(merge([], [heard]), [read]);
    expect(held[0]!.input).toBe("in8");
    expect(foreignOf(held, OWN, new Set()).map((m) => m.n)).toEqual([8]);
  });
  test("the own agent's words sent with ir on its way: kept, its record has them as a step alone", () => {
    const sent = msgOf({ id: "6", content: "on my way", user: { id: `agent:${OWN}`, name: "Claude", nick: OWN }, created_at: 60, _data: { kind: "progress", input: "in6" } })!;
    expect(foreignOf([said(2, OWN), sent], OWN, new Set()).map((m) => m.n)).toEqual([6]);
    expect(foreignOf([sent], OWN, new Set(["in6"]))).toEqual([]);
  });
  test("since: none older than the record's first block loaded", () => {
    expect(foreignOf([said(1, "codex"), said(2, "codex")], OWN, new Set(), 15).map((m) => m.n)).toEqual([2]);
  });
});

describe("in its place by time", () => {
  test("before the first row later than it; after them all when none is", () => {
    const slots = slotsOf([10, 20, 30], [{ time: 15 }, { time: 16 }, { time: 40 }]);
    expect([...slots.entries()].map(([i, ms]) => [i, ms.map((m) => m.time)])).toEqual([
      [1, [15, 16]],
      [3, [40]],
    ]);
  });
  test("a row with no time goes with the next that has one (a turn's thinking with its words); none after, as the one before; what is being written stays last", () => {
    // Messages said between two turns go above the later turn's thinking, not under it.
    expect([...slotsOf([10, undefined, 20], [{ time: 15 }, { time: 16 }]).keys()]).toEqual([1]);
    expect([...slotsOf([undefined, 5], [{ time: 1 }]).keys()]).toEqual([0]);
    expect([...slotsOf([10, undefined], [{ time: 12 }]).keys()]).toEqual([2]);
    expect([...slotsOf([10, undefined, Infinity], [{ time: 12 }]).keys()]).toEqual([1]);
  });
  test("a turn still at its steps: what called it stays above them (seen: the steps so far under the turn before, the call under what you said)", () => {
    // The turn before's words at 10, what you said to the channel at 15, the steps since 16:
    // nothing with a time after them till the turn says something.
    expect([...slotsOf([10, { since: 16 }], [{ time: 15 }]).keys()]).toEqual([1]);
    // What another says meanwhile goes under them.
    expect([...slotsOf([10, { since: 16 }], [{ time: 15 }, { time: 30 }]).keys()]).toEqual([1, 2]);
    // A subagent at work under them is where they are.
    expect([...slotsOf([10, { since: 16 }, undefined], [{ time: 15 }, { time: 30 }]).keys()]).toEqual([1, 3]);
    // Once the turn's words came, the steps go with them, as ever.
    expect([...slotsOf([10, { since: 16 }, 40], [{ time: 15 }, { time: 30 }]).keys()]).toEqual([1]);
  });
  test("one at the same time as a row goes after it", () => {
    expect([...slotsOf([10, 20], [{ time: 10 }]).keys()]).toEqual([1]);
  });
});

describe("read marks among both", () => {
  test("own words by their block, a person's by its input, others' by their own rows", () => {
    const ms = [person(1, "go", "in1"), said(2, OWN, "ok", 20, { block: "7.0" }), said(3, "codex")];
    const anchors = [...ownAnchors(ms, OWN, [{ key: "5.0", line: 5, id: "in1" }, { key: "7.0", line: 7 }]), { key: "ch:3", seq: 3 }];
    expect(anchors).toEqual([
      { key: "5.0", seq: 1 },
      { key: "7.0", seq: 2 },
      { key: "ch:3", seq: 3 },
    ]);
    const marks = marksAt(anchors, [
      { member: "a", seq: 2 },
      { member: "b", seq: 9 },
      { member: "me", seq: 3 },
      { member: "c", seq: 0 },
    ], "me");
    expect(Object.fromEntries([...marks].map(([k, rs]) => [k, rs.map((r) => r.member)]))).toEqual({ "7.0": ["a"], "ch:3": ["b"] });
  });
  test("a stretch's words by the last row before where it ends", () => {
    expect(ownAnchors([said(4, OWN, "x", 40, { from: 3, to: 9 })], OWN, [{ key: "3.0", line: 3 }, { key: "8.1", line: 8 }, { key: "9.0", line: 9 }])).toEqual([{ key: "8.1", seq: 4 }]);
  });
});

describe("the hand-over", () => {
  test("held while the words calling it are not shown, or the own agent still writes them", () => {
    const h = { since: 1000, called: 7 };
    expect(held(h, () => false, false, 1200)).toBe(true);
    expect(held(h, (n) => n === 7, false, 1200)).toBe(false);
    expect(held(h, (n) => n === 7, true, 1200)).toBe(true);
    expect(held({ since: 1000 }, () => false, false, 1200)).toBe(false);
  });
  test("never longer than HOLD", () => {
    expect(held({ since: 1000, called: 7 }, () => false, true, 1000 + HOLD)).toBe(false);
  });
  test("the caller: the latest naming @nick by another, standing by itself", () => {
    const ms = [person(1, "@codex test it"), said(2, "codex", "@codex is me"), said(3, OWN, "done, @codex run the tests"), said(4, OWN, "email a@codexs.com")];
    expect(callerOf(ms, "codex", "codex")).toBe(3);
    expect(callerOf(ms, "pi", "pi")).toBeUndefined();
    expect(callerOf([person(1, "@codex2 go")], "codex", "codex")).toBeUndefined();
    // All the agents called at once: each held on that message as on its own @.
    expect(callerOf([person(1, "@全体Agent roll call")], "codex", "codex")).toBe(1);
    expect(callerOf([person(3, "@全体Agents")], "codex", "codex")).toBeUndefined();
    // A nick with a "-" (a default one, Pi-9579) or a pattern's characters: matched as it is.
    expect(callerOf([person(1, "@Pi-9579 hi")], "Pi-9579", "pi")).toBe(1);
    expect(callerOf([person(1, "@Pi-95790 hi")], "Pi-9579", "pi")).toBeUndefined();
    expect(callerOf([person(1, "@a.b+(c) go")], "a.b+(c)", "x")).toBe(1);
    expect(callerOf([person(1, "@axb go")], "a.b", "x")).toBeUndefined();
  });
});

describe("words between agents fold, words to a person do not", () => {
  const nicks = new Set(["claude", "codex"]);
  test("naming only agents: folds; naming a person or all: never", () => {
    expect(betweenAgents(said(1, "codex", "@claude done, report:\n…"), [], nicks)).toBe(true);
    expect(named('<at id="agent:own"/> done', nicks)).toEqual({ agents: 1, people: 0 });
    expect(betweenAgents(said(1, "codex", "@claude @kumo done"), [], nicks)).toBe(false);
    expect(betweenAgents(said(1, "codex", "@全体成员 done"), [], nicks)).toBe(false);
    expect(named('<at type="all"/> done', nicks)).toEqual({ agents: 0, people: 1 });
  });
  test("naming none: by who began the stretch", () => {
    const ms = [person(1, "@claude go"), said(2, OWN, "@codex run the tests"), said(3, "codex", "all green\n…")];
    expect(betweenAgents(ms[2]!, ms, nicks)).toBe(true);
    const ms2 = [said(1, OWN, "@codex look"), person(2, "@codex and tell me"), said(3, "codex", "here it is")];
    expect(betweenAgents(ms2[2]!, ms2, nicks)).toBe(false);
  });
  test("never a person's, an ask, an error, Iridium's", () => {
    expect(betweenAgents(person(1, "@codex x"), [], nicks)).toBe(false);
    expect(betweenAgents(said(1, "codex", "@claude?", 10, { kind: "ask" }), [], nicks)).toBe(false);
    expect(betweenAgents(said(1, "codex", "@claude!", 10, { kind: "error" }), [], nicks)).toBe(false);
    expect(betweenAgents(note(1), [], nicks)).toBe(false);
  });
  test("an address or code is not a name", () => {
    expect(named("mail a@codex.com, `@claude`", nicks)).toEqual({ agents: 0, people: 0 });
  });
  test("lines", () => {
    expect(linesOf("a\nb\n\n")).toBe(2);
    expect(FOLD_AT).toBeGreaterThan(FOLD_SHOW);
  });
});

describe("its own channel's message given to the agent is the log's, drawn once", () => {
  const ctx = "<channel-context>\nYou are claude8 in a channel shared with people and other agents.\n</channel-context>";
  test("known by its relay, or, a block the state brought, by its words", () => {
    expect(givenHere("s1", { channel: "s1" }, "123")).toBe(true);
    expect(givenHere("s1", { channel: "other" }, "123")).toBe(false);
    // As the record has it (the session's tail says no relay): "#n: words" and the channel's block.
    expect(givenHere("s1", undefined, `#14: 123\n\n${ctx}`)).toBe(true);
    expect(givenHere("s1", undefined, "#16: 1233\n\n<channel-context>\n</channel-context>")).toBe(true);
  });
  test("not what was typed to it, nor a call from another channel", () => {
    expect(givenHere("s1", undefined, "123")).toBe(false);
    expect(givenHere("s1", undefined, "#14 is wrong")).toBe(false);
    expect(givenHere("s1", undefined, undefined)).toBe(false);
    expect(givenHere("s1", undefined, '#3: hi\n\n<channel-context>\nYou are claude8, called in the channel "x" (abc), which people and agents share.\n</channel-context>')).toBe(false);
  });
});
