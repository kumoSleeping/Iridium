// Chats warmed ahead (src/warm.ts, src/chats.ts warmTo): which are picked, and how they are fetched.
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import type { Session } from "../src/api/types.gen";
import { chatOf, peekChat, stateAhead, warmed, warmTo } from "../src/chats";
import { getCarried, setCarried } from "../src/store";
import { buildTree } from "../src/tree";
import { MADE_MAX, PER_WORKSPACE, pick, workspaceOf } from "../src/warm";

const NOW = 10_000_000;
const ses = (id: string, over: Partial<Session> = {}): Session => ({ id, agent: "claude", cwd: "/a", status: "idle", hosted: true, live: false, workspace: 1, updatedAt: NOW, ...over });
const wss = [
  { id: 1, name: "A", path: "/a" },
  { id: 2, name: "B", path: "/b" },
] as never;
const tree = (all: Session[]) => buildTree(all, wss, "none", NOW);

describe("pick", () => {
  test("five a workspace, the latest first; the last workspace first, then the others by their latest", () => {
    const a = Array.from({ length: 8 }, (_, i) => ses(`a${i}`, { updatedAt: NOW - i }));
    const b = Array.from({ length: 3 }, (_, i) => ses(`b${i}`, { cwd: "/b", workspace: 2, updatedAt: NOW + 100 - i }));
    const all = [...a, ...b];
    expect([...pick(all, tree(all), null, 1, NOW).keys()]).toEqual(["a0", "a1", "a2", "a3", "a4", "b0", "b1", "b2"]);
    expect([...pick(all, tree(all), null, null, NOW).keys()]).toEqual(["b0", "b1", "b2", "a0", "a1", "a2", "a3", "a4"]);
    expect(PER_WORKSPACE).toBe(5);
  });
  test("the open one takes no place; those next to it in the sidebar come first", () => {
    const all = Array.from({ length: 8 }, (_, i) => ses(`a${i}`, { updatedAt: NOW - i }));
    expect([...pick(all, tree(all), "a6", 1, NOW).keys()]).toEqual(["a5", "a7", "a0", "a1", "a2"]);
  });
  test("those that wait on you count in their workspace; archived ones never", () => {
    const all = [ses("a0", { updatedAt: NOW - 5 }), ses("w", { status: "waiting", updatedAt: NOW - 1 }), ses("x", { archived: true, updatedAt: NOW })];
    expect([...pick(all, tree(all), null, 1, NOW).keys()]).toEqual(["w", "a0"]);
    expect(workspaceOf(tree(all), "w")).toBe(1);
    expect(workspaceOf(tree(all), "x")).toBeUndefined();
  });
  test("those agents made, at work, apart from the five; done, they go", () => {
    const main = Array.from({ length: 6 }, (_, i) => ses(`a${i}`, { live: true, updatedAt: NOW - i }));
    const sub = ses("sub", { subagent: true, parent: "a0", updatedAt: NOW - 10 });
    const run = ses("ran", { launcher: "a0", status: "running", updatedAt: NOW - 20 });
    const all = [...main, sub, run];
    const got = [...pick(all, tree(all), null, 1, NOW).keys()];
    expect(got).toEqual(["a0", "a1", "a2", "a3", "a4", "sub", "ran"]);
    const done = [...main, { ...sub, done: true }, { ...run, status: "idle" as const }];
    expect([...pick(done, tree(done), null, 1, NOW).keys()]).toEqual(["a0", "a1", "a2", "a3", "a4"]);
    const many = [...main, ...Array.from({ length: 20 }, (_, i) => ses(`s${i}`, { subagent: true, parent: "a0", updatedAt: NOW - 100 - i }))];
    expect(pick(many, tree(many), null, 1, NOW).size).toBe(PER_WORKSPACE + MADE_MAX);
  });
});

describe("warmTo", () => {
  let asked: string[] = [];
  beforeEach(() => {
    asked = [];
    vi.stubGlobal("fetch", async (url: string) => {
      asked.push(url);
      return new Response(JSON.stringify({ blocks: [], start: 10, end: 20, more: true }), { headers: { "content-type": "application/json" } });
    });
  });
  afterEach(() => (warmTo(new Map()), vi.unstubAllGlobals()));
  const settle = () => new Promise((r) => setTimeout(r, 20));

  test("the newest page of each and its state, one at a time, not the page before; again only as it changes", async () => {
    warmTo(new Map([["w1", 1], ["w2", 1]]));
    await settle();
    expect(asked).toEqual(["/v1/internal/sessions/w1/messages", "/v1/internal/sessions/w1", "/v1/internal/sessions/w2/messages", "/v1/internal/sessions/w2"]);
    expect(peekChat("w1")?.log.loaded).toBe(true);
    warmTo(new Map([["w1", 1], ["w2", 2]]));
    await settle();
    expect(asked.slice(4)).toEqual(["/v1/internal/sessions/w2/messages?after=20"]);
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
    expect(asked.at(-1)).toBe("/v1/internal/sessions/v1/messages?before=10");
  });
});
