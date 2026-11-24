// Folding rules for channel messages (src/chat/channel/folds.ts).
import { describe, expect, test } from "vitest";
import type { ChannelMessage } from "../src/api/client";
import { msgOf, type Msg } from "../src/chat/channel/lib";
import { betweenAgents, FOLD_AT, FOLD_SHOW, linesOf, named } from "../src/chat/channel/folds";

const OWN = "own";
const said = (n: number, session: string, text = "hi", time = n * 10, data: ChannelMessage["_data"] = {}): Msg => msgOf({ id: String(n), content: text, user: { id: `agent:${session}`, name: "Claude", nick: session }, created_at: time, _data: { session, ...data } })!;
const person = (n: number, text = "hi", input?: string, time = n * 10): Msg => msgOf({ id: String(n), content: text, user: { id: "ir_web/k", name: "kumo" }, created_at: time, _data: input ? { input } : {} })!;
const note = (n: number, time = n * 10): Msg => msgOf({ id: String(n), content: "x joined", user: { id: "iridium", name: "Iridium" }, created_at: time })!;

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
