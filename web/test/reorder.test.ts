// Rows dragged to a new place (reorder.ts): the key a session is dropped at, and the groups' order.
import { describe, expect, test } from "vitest";
import type { Session } from "../src/api/types.gen";
import { pinFolder, placeFolder, placeGroup, placeSession, sortList, withPlace } from "../src/reorder";
import { buildTree, foldRest } from "../src/tree";

const s = (id: string, updatedAt: number, more: Partial<Session> = {}) => ({ id, updatedAt, agent: "claude", cwd: "/w", status: "idle", hosted: false, live: false, ...more }) as Session;

describe("reorder", () => {
  const p1 = s("p1", 50, { pinned: true });
  const p2 = s("p2", 40, { pinned: true });
  const a = s("a", 300);
  const b = s("b", 200);
  const c = s("c", 100);
  const sibs = sortList([c, a, p2, b, p1]);

  test("the list as the server sorts it", () => {
    expect(sibs.map((x) => x.id)).toEqual(["p1", "p2", "a", "b", "c"]);
    expect(sortList([a, s("d", 10, { order: 250 })]).map((x) => x.id)).toEqual(["a", "d"]);
  });

  test("a session between two, at the top, at the foot", () => {
    expect(placeSession(sibs, c, a, "bottom")).toEqual({ order: 250, pinned: false });
    expect(placeSession(sibs, c, a, "top", 1000)).toEqual({ order: 1000, pinned: false });
    expect(placeSession(sibs, a, c, "bottom")).toEqual({ order: -900, pinned: false });
    // B above A: the key between A and the one above it would be, A's own at the top.
    const placed = placeSession(sibs, b, a, "top", 0);
    expect(sortList([...sibs.filter((x) => x.id !== "b"), { ...b, ...placed }]).map((x) => x.id)).toEqual(["p1", "p2", "b", "a", "c"]);
  });

  test("dropped among the pinned, it is pinned; among the rest, not", () => {
    const at = placeSession(sibs, c, p1, "bottom");
    expect(at).toEqual({ order: 45, pinned: true });
    expect(placeSession(sibs, p1, b, "top").pinned).toBe(false);
  });

  test("a group's row moved before or after another; one stored, not listed now, keeps its place", () => {
    expect(placeGroup([], ["w1", "w2", "w3"], "w3", "w1", "top")).toEqual(["w3", "w1", "w2"]);
    expect(placeGroup(["w2", "w1"], ["w2", "w1", "w3"], "w2", "w3", "bottom")).toEqual(["w1", "w3", "w2"]);
    expect(placeGroup(["w9", "w1"], ["w1", "w1@m"], "w1@m", "w1", "top")).toEqual(["w9", "w1@m", "w1"]);
  });
});

describe("folders", () => {
  const f = (path: string, at: number) => ({ key: path, name: path, path, sessions: [{ id: path, updatedAt: at } as Session] });
  const x = f("/w/x", 300);
  const y = f("/w/y", 200);
  const z = f("/w/z", 100);
  test("a folder between two, at the top; one dragged keeps its key", () => {
    const none = { groups: [], order: {}, pinned: [] };
    expect(placeFolder([x, y, z], z, x, "bottom", none)).toEqual({ order: 250, pinned: false });
    expect(placeFolder([x, y, z], z, x, "top", none, 0)).toEqual({ order: 301, pinned: false });
    expect(placeFolder([x, y, z], x, z, "top", { groups: [], order: { "/w/y": 150 }, pinned: [] })).toEqual({ order: 125, pinned: false });
  });

  test("pinned: among the pinned, or last of them dropped on a session at the root", () => {
    const p = { ...x, pinned: true };
    const none = { groups: [], order: {}, pinned: ["/w/x"] };
    expect(placeFolder([p, y, z], z, p, "bottom", none, 0)).toEqual({ order: 300 - 1000, pinned: true });
    expect(pinFolder([p, y, z], z, none)).toEqual({ order: -700, pinned: true });
    expect(pinFolder([y, z], z, { groups: [], order: {}, pinned: [] })).toEqual({ pinned: true });
    expect(withPlace(none, "/w/z", { order: 5, pinned: true })).toEqual({ groups: [], order: { "/w/z": 5 }, pinned: ["/w/x", "/w/z"] });
    expect(withPlace({ groups: [], order: { "/w/x": 9 }, pinned: ["/w/x"] }, "/w/x", { order: 0, pinned: false })).toEqual({ groups: [], order: {}, pinned: [] });
  });

  test("groups in their own order, whichever machine; 未归类 last", () => {
    const ses = (id: string, cwd: string, more: Partial<Session>) => ({ id, cwd, updatedAt: 1, agent: "claude", status: "idle", hosted: false, live: false, ...more }) as Session;
    const wss = [{ id: 2, name: "dsh", path: "m:C:/w" }, { id: 1, name: "here", path: "/w" }];
    const tree = buildTree([ses("a", "/w", { workspace: 1 }), ses("b", "C:\\w", { workspace: 2, machine: "m" }), ses("c", "/x", {}), ses("d", "C:\\x", { machine: "m" })], wss, "-", 0, undefined, ["m"]);
    expect(tree.workspaces.map((w) => w.key)).toEqual(["w2", "w1", "w0", "w0:m"]);
    // Dragged: the remote group above this machine's; a group at two machines, its rows apart.
    const moved = buildTree([ses("a", "/w", { workspace: 1 }), ses("b", "C:\\w", { workspace: 2, machine: "m" }), ses("e", "C:\\w\\e", { workspace: 1, machine: "m" }), ses("c", "/x", {})], wss, "-", 0, undefined, ["m"], { groups: ["w2", "w1@m"], order: {}, pinned: [] });
    expect(moved.workspaces.map((w) => w.key)).toEqual(["w2", "w1@m", "w1", "w0"]);
  });

  test("pinned folders above the sessions at the root", () => {
    const ses = (id: string, cwd: string) => ({ id, cwd, updatedAt: 1, agent: "claude", status: "idle", hosted: false, live: false, workspace: 1 }) as Session;
    const tree = buildTree([ses("r", "/w"), ses("a", "/w/a"), ses("b", "/w/b")], [{ id: 1, name: "w", path: "/w" }], "-", 0, undefined, [], { groups: [], order: {}, pinned: ["/w/b"] });
    expect(tree.workspaces[0]!.rows.map((r) => (r.kind === "session" ? r.s.id : r.f.name))).toEqual(["b", "r", "a"]);
  });
});

describe("a folder's rest folded", () => {
  const ss = Array.from({ length: 9 }, (_, i) => ({ id: `s${i}`, updatedAt: 100 - i }) as Session);
  test("the first five and a row for the rest; a kept one wherever it is", () => {
    expect(foldRest(ss.slice(0, 6), () => false)).toEqual({ shown: ss.slice(0, 6), rest: 0 });
    const f = foldRest(ss, (s) => s.id === "s7");
    expect(f.shown.map((s) => s.id)).toEqual(["s0", "s1", "s2", "s3", "s4", "s7"]);
    expect(f.rest).toBe(3);
    // Kept all but one: nothing folded.
    expect(foldRest(ss, (s) => s.id !== "s8").rest).toBe(0);
  });
});
