// 在哪干活 under a chat's title: the places a session sent sessions to, and those it named.
import { describe, expect, test } from "vitest";
import type { Session } from "../src/api/types.gen";
import { dispatchedFrom, spotsOf } from "../src/chat/where";

const S = (id: string, cwd: string, more: Partial<Session> = {}): Session => ({ id, agent: "claude", cwd, status: "idle", hosted: true, live: true, updatedAt: 0, title: id, ...more });
const ws = [
  { id: 1, name: "Iridium", path: "/w/ir" },
  { id: 7, name: "Ds_Benchmark", path: "m:C:/Code/Ds_Benchmark" },
];

describe("dispatchedFrom", () => {
  test("those it started and theirs, not subagents, not one taken out by hand; a cycle stops", () => {
    const list = [
      S("p", "/w/ir", { workspace: 1, own: true }),
      S("a", "C:\\Code\\Ds_Benchmark", { workspace: 7, machine: "m", launcher: "p" }),
      S("b", "/w/ir/web", { workspace: 1, launcher: "a" }),
      S("out", "/w/ir", { workspace: 1, launcher: "p", own: true }),
      S("sa", "/w/ir", { subagent: true, parent: "p", launcher: "p" }),
      S("x", "/w/ir", { workspace: 1, launcher: "y" }),
      S("y", "/w/ir", { workspace: 1, launcher: "x" }),
    ];
    expect(dispatchedFrom("p", list, ws).map((s) => s.id)).toEqual(["a", "b"]);
    // One in the middle of a chain: those under it.
    expect(dispatchedFrom("a", list, ws).map((s) => s.id)).toEqual(["b"]);
    expect(dispatchedFrom("out", list, ws)).toEqual([]);
    expect(dispatchedFrom("x", list, ws).map((s) => s.id)).toEqual(["y"]);
  });
  test("one taken out by hand in the middle of a chain ends it: what it started is its, not its launcher's", () => {
    const list = [S("p", "/w/ir", { workspace: 1, own: true }), S("out", "/w/ir", { workspace: 1, launcher: "p", own: true }), S("c", "C:\\Code\\Ds_Benchmark", { workspace: 7, machine: "m", launcher: "out" })];
    expect(dispatchedFrom("p", list, ws)).toEqual([]);
    expect(dispatchedFrom("out", list, ws).map((s) => s.id)).toEqual(["c"]);
    expect(spotsOf(list[0]!, list, [], ws)).toEqual([]);
  });
});

describe("spotsOf", () => {
  test("its own folder, each place sent to with those there, named ones not sent to; none with neither", () => {
    const list = [
      S("p", "/w/ir", { workspace: 1, own: true }),
      S("a", "C:\\Code\\Ds_Benchmark", { workspace: 7, machine: "m", launcher: "p", status: "running" }),
      S("a2", "C:\\Code\\Ds_Benchmark", { workspace: 7, machine: "m", launcher: "p" }),
      S("b", "/w/ir", { workspace: 1, launcher: "a2" }),
      S("q", "/w/other", { own: true }),
    ];
    const spots = spotsOf(list[0]!, list, [{ machine: "m", path: "C:/Code", name: "Code" }, { machine: "m", path: "C:/Code/Ds_Benchmark/x", name: "x" }, { path: "/srv", name: "srv" }, { path: "/w/ir", name: "ir" }], ws);
    expect(spots.map((x) => [x.kind, x.key, x.name, x.sessions.map((s) => s.id)])).toEqual([
      ["own", "/w/ir", "Iridium", ["b"]],
      ["sent", "m:C:/Code/Ds_Benchmark", "Ds_Benchmark", ["a", "a2"]],
      ["named", "m:C:/Code/Ds_Benchmark/x", "x", []],
      ["named", "/srv", "srv", []],
    ]);
    expect(spots[1]!.place).toEqual({ machine: "m", path: "C:/Code/Ds_Benchmark" });
    expect(spotsOf(list[4]!, list, [], ws)).toEqual([]);
    // Only its own folder named: nothing.
    expect(spotsOf(list[4]!, list, [{ path: "/w/other", name: "other" }], ws)).toEqual([]);
    expect(spotsOf(S("s", "/w/ir", { subagent: true }), list, [{ path: "/srv", name: "srv" }], ws)).toEqual([]);
  });
  test("one at another machine always says where it runs, even with nothing sent or named", () => {
    const list = [S("p", "/w/ir", { workspace: 1, own: true }), S("a", "C:\\Code\\Ds_Benchmark", { workspace: 7, machine: "m", launcher: "p" })];
    expect(spotsOf(list[1]!, list, [], ws).map((x) => [x.kind, x.key, x.name])).toEqual([["own", "m:C:/Code/Ds_Benchmark", "Ds_Benchmark"]]);
    expect(spotsOf(list[1]!, list, [], ws)[0]!.place).toEqual({ machine: "m", path: "C:/Code/Ds_Benchmark" });
  });
});
