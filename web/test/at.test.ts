import { describe, expect, test } from "vitest";
import type { DshWorkspace, Session, Workspace } from "../src/api/types.gen";
import type { Machine } from "../src/machines";
import { arrowInto, atPath, atSections, baseName, pathSection, typedPath } from "../src/chat/at";
import { placeMention, placeOf } from "../src/chat/mention";
import { mentions, placesIn } from "../src/chat/refs";

const win = { id: "m1", name: "otae-2", os: "windows", home: "C:\\Users\\o", sep: "\\", agents: [{ agent: "dsh" }], online: true } as Machine;
const mac = { id: "m2", name: "Book", os: "darwin", home: "/Users/k", sep: "/", agents: [{ agent: "claude" }], online: false } as Machine;

describe("place mentions", () => {
  test("a folder here and a workspace at another machine, as links", () => {
    expect(mentions("看 [ir](ir:folder//home/otae/ir) 吧")).toEqual(["看 ", { kind: "place", name: "ir", path: "/home/otae/ir", text: "[ir](ir:folder//home/otae/ir)" }, " 吧"]);
    expect(mentions("[Ds_Benchmark](ir:folder/otae-2:C:\\Code\\Ds_Benchmark)")).toEqual([{ kind: "place", name: "Ds_Benchmark", machine: "otae-2", path: "C:\\Code\\Ds_Benchmark", text: "[Ds_Benchmark](ir:folder/otae-2:C:\\Code\\Ds_Benchmark)" }]);
  });
  test("a path with spaces or brackets in <>, links side by side, words after", () => {
    expect(mentions("[a (copy)](<ir:folder//x/a (copy)>)")[0]).toMatchObject({ path: "/x/a (copy)" });
    const two = mentions("[a](ir:folder//x)[b](ir:folder//y)，然后（看看）");
    expect(two.slice(0, 2)).toMatchObject([{ path: "/x" }, { path: "/y" }]);
    expect(two[2]).toBe("，然后（看看）");
  });
  test("not a place", () => {
    expect(mentions("[x](ir:folder/relative)")).toEqual(["[x](ir:folder/relative)"]);
    expect(mentions("[x](ir:folder/a/b:c)")).toEqual(["[x](ir:folder/a/b:c)"]);
    expect(mentions("文件夹「ir」（/home/otae/ir）")).toEqual(["文件夹「ir」（/home/otae/ir）"]);
  });
  test("placesIn: each once", () => {
    expect(placesIn("[ir](ir:folder//home/otae/ir) [Ds](ir:folder/OTAE-2:C:\\Ds) [Ds](ir:folder/otae-2:C:\\Ds)")).toEqual([
      { kind: "local", path: "/home/otae/ir", name: "ir" },
      { kind: "remote", machine: "OTAE-2", path: "C:\\Ds", name: "Ds" },
    ]);
  });
  test("sent as a link, read back as the page's place", () => {
    expect(placeMention({ path: "/home/otae/ir", name: "ir" }, [win])).toBe("[ir](ir:folder//home/otae/ir)");
    expect(placeMention({ machine: "m1", path: "C:/Code/Ds_Benchmark", name: "Ds_Benchmark" }, [win])).toBe("[Ds_Benchmark](ir:folder/otae-2:C:\\Code\\Ds_Benchmark)");
    expect(placeOf({ machine: "OTAE-2", path: "C:\\Code\\Ds" }, [win])).toEqual({ machine: "m1", path: "C:/Code/Ds" });
    expect(placeOf({ machine: "gone", path: "/x" }, [win])).toBeUndefined();
    expect(placeOf({ path: "/x" }, [win])).toEqual({ path: "/x" });
  });
  test("a machine whose name has a space or a colon: by its ID", () => {
    const pc = { ...win, id: "m3", name: "My PC" };
    const said = placeMention({ machine: "m3", path: "C:/Code/Ds", name: "Ds" }, [pc]);
    expect(said).toBe("[Ds](ir:folder/m3:C:\\Code\\Ds)");
    expect(placeOf(mentions(`${said} hi`)[0] as { machine: string; path: string }, [pc])).toEqual({ machine: "m3", path: "C:/Code/Ds" });
    expect(placeMention({ machine: "m4", path: "/x", name: "x" }, [{ ...mac, id: "m4", name: "a:b" }])).toBe("[x](ir:folder/m4:/x)");
  });
  test("a name with brackets, or too long: said so it reads back", () => {
    for (const name of ["My [big] app", "a」b", "x".repeat(300)]) {
      const said = placeMention({ machine: "m1", path: "C:/Code/big dir", name }, [win]);
      expect(mentions(`${said} hi`)[0]).toMatchObject({ kind: "place", machine: "otae-2", path: "C:\\Code\\big dir" });
      const here = placeMention({ path: "/w/big", name }, [win]);
      expect(mentions(`${here} hi`)[0]).toMatchObject({ kind: "place", path: "/w/big" });
    }
  });
});

describe("the @ list", () => {
  const s = (id: string, updatedAt: number, more: Partial<Session> = {}) => ({ id, agent: "claude", cwd: "/home/k/" + id, title: id, status: "idle", hosted: true, live: false, updatedAt, ...more }) as Session;
  const ws: Workspace[] = [
    { id: 1, name: "ir", path: "/home/k/ir" },
    { id: 2, name: "notes", path: "/home/k/notes" },
    { id: 3, name: "Bench", path: "m1:C:/Code/Bench" },
    { id: 4, name: "Quiet", path: "m1:C:/Code/Quiet" },
  ] as Workspace[];
  const list = [
    s("a", 1, { workspace: 1 }),
    s("b", 2, { workspace: 2 }),
    s("c", 3, { workspace: 1 }),
    s("d", 4, { workspace: 1 }),
    s("e", 5, { workspace: 1 }),
    s("here", 9, { workspace: 1 }),
    s("r1", 6, { workspace: 3, machine: "m1", cwd: "C:\\Code\\Bench", own: true }),
    s("r2", 7, { workspace: 4, machine: "m1", cwd: "C:\\Code\\Quiet" }),
  ];
  const dsh: Record<string, DshWorkspace[]> = { m1: [{ title: "Bench", path: "C:/Code/Bench", sessions: 1 }, { title: "Other", path: "C:/Other", sessions: 0 }] };
  test("sections: the newest four sessions listed, this machine's groups, each machine's places", () => {
    const out = atSections({ list, here: "here", workspaces: ws, machines: [win, mac], dsh }, "");
    expect(out.map((x) => x.key)).toEqual(["sessions", "local", "m:m1"]);
    expect(out[0]!.rows.map((r) => (r.kind === "session" ? r.s.id : ""))).toEqual(["r1", "e", "d", "c"]);
    expect(out[1]!.rows.map((r) => (r.kind === "place" ? r.p.name : ""))).toEqual(["ir", "notes"]);
    // Quiet: a group at m1 no one opened, not listed; Bench once, by its group's name.
    expect(out[2]!.rows.map((r) => (r.kind === "place" ? [r.p.name, r.p.path, r.hint] : []))).toEqual([
      ["Bench", "C:/Code/Bench", "C:\\Code\\Bench"],
      ["Other", "C:/Other", "C:\\Other"],
    ]);
  });
  test("typed: every section filtered, empty ones gone; a machine away faint", () => {
    const out = atSections({ list, here: "here", workspaces: ws, machines: [win, mac], dsh }, "no");
    expect(out.map((x) => x.key)).toEqual(["local"]);
    const off = atSections({ list, here: "here", workspaces: ws, machines: [{ ...win, online: false }], dsh: {} }, "bench");
    expect(off.map((x) => [x.key, x.off])).toEqual([["sessions", undefined], ["m:m1", true]]);
    expect(off[1]!.rows.every((r) => r.off)).toBe(true);
  });
  test("a typed path", () => {
    expect(atPath("/home/ot", [win])).toEqual({ dir: "/home", part: "ot" });
    expect(atPath("/", [win])).toEqual({ dir: "/", part: "" });
    expect(atPath("~/co", [win])).toEqual({ dir: "~", part: "co" });
    expect(atPath("~/co", [win], "/home/otae")).toEqual({ dir: "/home/otae", part: "co" });
    expect(atPath("~", [win], "/home/otae")).toEqual({ dir: "/home/otae", part: "" });
    expect(atPath("otae-2:C:\\Code\\Ds", [win])).toEqual({ m: win, dir: "C:/Code", part: "Ds" });
    expect(atPath("OTAE-2:", [win])).toEqual({ m: win, dir: "C:/Users/o", part: "" });
    expect(atPath("m1:C:", [win])).toEqual({ m: win, dir: "C:/", part: "" });
    expect(atPath("otae-2:~/Desk", [win])).toEqual({ m: win, dir: "C:/Users/o", part: "Desk" });
    expect(atPath("C:\\x", [win])).toBeNull();
    expect(atPath("side", [win])).toBeNull();
  });
  test("a typed path's section: its folder, then those the name begun finds", () => {
    const p = atPath("otae-2:C:/Code/d", [win])!;
    const sec = pathSection(p, { path: "C:/Code", folders: ["Alpha", "Ds_Benchmark", "dsh", "odd"] });
    expect(sec.head).toEqual({ kind: "path", m: win, path: "C:\\Code" });
    expect(sec.rows.map((r) => (r.kind === "place" ? r.p.path : ""))).toEqual(["C:/Code", "C:/Code/Ds_Benchmark", "C:/Code/dsh", "C:/Code/odd"]);
    expect(sec.rows[0]).toMatchObject({ self: true, p: { machine: "m1", name: "Code" } });
    const none = pathSection(atPath("/nope/x", [])!, { missing: true });
    expect(none.rows).toHaveLength(1);
    expect(none.rows[0]!.off).toBe(true);
    // ~ not known yet: its folder not to be named (it would not read back), till the listing says.
    expect(pathSection(atPath("~", [])!, {}).rows[0]!.off).toBe(true);
    expect(pathSection(atPath("~", [])!, { path: "/home/k", folders: [] }).rows[0]).toMatchObject({ p: { path: "/home/k" } });
    expect(pathSection(atPath("~", [])!, { path: "/home/k", folders: [] }).rows[0]!.off).toBeUndefined();
  });
  test("→ goes in only with the caret alone at the @word's end, no key held, to change it", () => {
    const text = "@/home/otae x";
    expect(arrowInto(text, 0, 11, 11, "@/home/otae/", false)).toBe(true);
    expect(arrowInto(text, 0, 3, 3, "@/home/", false)).toBe(false); // inside the word
    expect(arrowInto(text, 0, 11, 11, "@/home/otae/", true)).toBe(false); // Ctrl, Alt, ⌘ or Shift
    expect(arrowInto(text, 0, 6, 11, "@/home/otae/", false)).toBe(false); // a selection
    expect(arrowInto("@/", 0, 2, 2, "@/", false)).toBe(false); // nothing to change: the caret moves
  });
  test("→ into a folder; names", () => {
    expect(typedPath({ path: "/home/k" }, [])).toBe("/home/k/");
    expect(typedPath({ machine: "m1", path: "C:/" }, [win])).toBe("otae-2:C:/");
    expect(typedPath({ machine: "m9", path: "/x" }, [win])).toBe("m9:/x/");
    expect(baseName("C:/")).toBe("C:");
    expect(baseName("/")).toBe("/");
    expect(baseName("/a/b/")).toBe("b");
  });
});
