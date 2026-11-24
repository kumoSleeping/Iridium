// Chats warmed ahead (src/warm.ts, src/chats.ts warmTo): which are picked, and how they are fetched.
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import type { Session } from "../src/api/types.gen";
import { speakAs } from "../src/api/client";
import { chatOf, peekChat, stateAhead, warmed, warmTo } from "../src/chats";
import { getCarried, setCarried } from "../src/store";
import { buildTree } from "../src/tree";
import { MADE_MAX, PER_WORKSPACE, pick, WARM_MAX, workspaceOf } from "../src/warm";

const NOW = 10_000_000;
const ses = (id: string, over: Partial<Session> = {}): Session => ({ id, agent: "claude", cwd: "/a", status: "idle", hosted: true, live: false, workspace: 1, updatedAt: NOW, ...over });
const wss = [
  { id: 1, name: "A", path: "/a" },
  { id: 2, name: "B", path: "/b" },
] as never;
const tree = (all: Session[]) => buildTree(all, wss, "none", NOW);

const A10 = Array.from({ length: 10 }, (_, i) => `a${i}`);

describe("pick", () => {
  test("ten a workspace, the latest first; the last workspace first, then the others by their latest", () => {
    const a = Array.from({ length: 13 }, (_, i) => ses(`a${i}`, { updatedAt: NOW - i }));
    const b = Array.from({ length: 3 }, (_, i) => ses(`b${i}`, { cwd: "/b", workspace: 2, updatedAt: NOW + 100 - i }));
    const all = [...a, ...b];
    expect([...pick(all, tree(all), null, 1, NOW).keys()]).toEqual([...A10, "b0", "b1", "b2"]);
    expect([...pick(all, tree(all), null, null, NOW).keys()]).toEqual(["b0", "b1", "b2", ...A10]);
    expect(PER_WORKSPACE).toBe(10);
  });
  test("the open one takes no place; those next to it in the sidebar come first", () => {
    const all = Array.from({ length: 8 }, (_, i) => ses(`a${i}`, { updatedAt: NOW - i }));
    expect([...pick(all, tree(all), "a6", 1, NOW).keys()]).toEqual(["a5", "a7", "a0", "a1", "a2", "a3", "a4"]);
  });
  test("those that wait on you count in their workspace; archived ones never", () => {
    const all = [ses("a0", { updatedAt: NOW - 5 }), ses("w", { status: "waiting", updatedAt: NOW - 1 }), ses("x", { archived: true, updatedAt: NOW })];
    expect([...pick(all, tree(all), null, 1, NOW).keys()]).toEqual(["w", "a0"]);
    expect(workspaceOf(tree(all), "w")).toBe(1);
    expect(workspaceOf(tree(all), "x")).toBeUndefined();
  });
  test("those agents made, at work, apart from the ten; done, they go", () => {
    const main = Array.from({ length: 11 }, (_, i) => ses(`a${i}`, { live: true, updatedAt: NOW - i }));
    const sub = ses("sub", { subagent: true, parent: "a0", updatedAt: NOW - 10 });
    const run = ses("ran", { launcher: "a0", status: "running", updatedAt: NOW - 20 });
    const all = [...main, sub, run];
    const got = [...pick(all, tree(all), null, 1, NOW).keys()];
    expect(got).toEqual([...A10, "sub", "ran"]);
    const done = [...main, { ...sub, done: true }, { ...run, status: "idle" as const }];
    expect([...pick(done, tree(done), null, 1, NOW).keys()]).toEqual(A10);
    const many = [...main, ...Array.from({ length: 20 }, (_, i) => ses(`s${i}`, { subagent: true, parent: "a0", updatedAt: NOW - 100 - i }))];
    expect(pick(many, tree(many), null, 1, NOW).size).toBe(PER_WORKSPACE + MADE_MAX);
  });
  test("capped across many workspaces at WARM_MAX, keeping the latest workspaces first", () => {
    const manyWss = Array.from({ length: 6 }, (_, i) => ({ id: i + 1, name: `W${i + 1}`, path: `/w${i + 1}` })) as never;
    const all = Array.from({ length: 6 }, (_, w) =>
      Array.from({ length: 10 }, (_, i) => ses(`w${w + 1}_${i}`, { cwd: `/w${w + 1}`, workspace: w + 1, updatedAt: NOW - w * 100 - i })),
    ).flat();
    const picked = [...pick(all, buildTree(all, manyWss, "none", NOW), null, 6, NOW).keys()];
    expect(picked.length).toBe(WARM_MAX);
    // Last workspace (6) first, then the most recent others (1, 2).
    expect(picked.slice(0, 10)).toEqual(Array.from({ length: 10 }, (_, i) => `w6_${i}`));
    expect(picked.slice(10, 20)).toEqual(Array.from({ length: 10 }, (_, i) => `w1_${i}`));
    expect(picked.slice(20, 30)).toEqual(Array.from({ length: 10 }, (_, i) => `w2_${i}`));
  });
});

describe("warmTo", () => {
  let asked: string[] = [];
  beforeEach(() => {
    asked = [];
    speakAs("ir_web", "u1");
    // message.list as "<url> <channel> [<direction> <next>]".
    vi.stubGlobal("fetch", async (url: string, init: RequestInit = {}) => {
      const b = typeof init.body === "string" ? JSON.parse(init.body) : undefined;
      asked.push(b ? [url, b.channel_id, b.direction, b.next].filter(Boolean).join(" ") : url);
      const page = { data: [], _data: { start: 10, end: 20, more: true } };
      // A first screen: its state and its record's newest page.
      const first = /\/sessions\/([^/]+)\/first/.exec(url);
      return new Response(JSON.stringify(first ? { session: { id: first[1], status: "idle" }, page } : page), { headers: { "content-type": "application/json" } });
    });
  });
  afterEach(() => {
    warmTo(new Map());
    vi.unstubAllGlobals();
    for (const id of ["w1", "w2", "v1", "v2"]) setCarried(`session:${id}`, null);
  });
  // A first screen looks for this device's copy first (its code loaded the first time).
  const settle = () => new Promise((r) => setTimeout(r, 150));

  test("the first screen of each (its state with it), one at a time, not the page before; again only as it changes", async () => {
    warmTo(new Map([["w1", 1], ["w2", 1]]));
    await settle();
    expect(asked).toEqual(["/v1/internal/sessions/w1/first", "/v1/internal/sessions/w2/first"]);
    expect(peekChat("w1")?.log.loaded).toBe(true);
    warmTo(new Map([["w1", 1], ["w2", 2]]));
    await settle();
    expect(asked.slice(2)).toEqual(["/v1/internal/block.list w2 after 20"]);
  });
  test("a state the stream brought meanwhile is not put over; one asked for again only as it is not there", async () => {
    let answer: () => void = () => {};
    vi.stubGlobal("fetch", (url: string) => {
      asked.push(url);
      return new Promise<Response>((r) => (answer = () => r(new Response(JSON.stringify({ id: "z", status: "idle" }), { headers: { "content-type": "application/json" } }))));
    });
    const asking = stateAhead("z");
    expect(stateAhead("z")).toBe(asking);
    setCarried("session:z", { id: "z", status: "running" });
    answer();
    expect(await asking).toBe(true);
    expect(getCarried<{ status: string }>("session:z")?.status).toBe("running");
    setCarried("session:z", null);
    const again = stateAhead("z");
    answer();
    expect(await again).toBe(true);
    expect(getCarried<{ status: string }>("session:z")?.status).toBe("idle");
    expect(asked).toEqual(["/v1/internal/sessions/z", "/v1/internal/sessions/z"]);
    setCarried("session:z", null);
  });
  test("a session not there: stateAhead says so", async () => {
    vi.stubGlobal("fetch", async () => new Response(JSON.stringify({ error: "no such session" }), { status: 404, headers: { "content-type": "application/json" } }));
    expect(await stateAhead("gone")).toBe(false);
    expect(getCarried("session:gone")).toBeUndefined();
  });
  test("one not wanted goes; one visited is kept, and asks for the page before as you scroll", async () => {
    warmTo(new Map([["v1", 1], ["v2", 1]]));
    await settle();
    const it = peekChat("v1");
    expect(chatOf("v1")).toBe(it);
    warmTo(new Map([["v1", 1]]));
    expect(warmed()).toEqual([]);
    expect(peekChat("v2")).toBeUndefined();
    expect(peekChat("v1")).toBe(it);
    await it!.log.older();
    expect(asked.at(-1)).toBe("/v1/internal/block.list v1 before 10");
  });
});
