// The page's pure logic: folding tool calls, reading sources and file references, the sidebar's
// tree, the composer's key, the upgrade rules, the i18n dictionaries.
import { describe, expect, test } from "vitest";
import type { Block, Session } from "../src/api/types.gen";
import { argOf, folded, foldLine, inputOf } from "../src/chat/fold";
import { commandsOf, keyMode, matching } from "../src/chat/composer";
import { ctxParts } from "../src/chat/meters";
import { filesIn, resolve, sourceOf } from "../src/chat/refs";
import { en } from "../src/i18n/en";
import { zh } from "../src/i18n/zh";
import { jumpList } from "../src/jump";
import { buildTree, commonDir, flat } from "../src/tree";
import { entryOf, reloadNow } from "../src/upgrade";
import { tilde, titleOf } from "../src/ui";

const b = (at: string, kind: Block["kind"], more: Partial<Block> = {}): Block => ({ at, kind, ...more });

describe("folded", () => {
  test("steps between two things said fold; a result from another page joins its call", () => {
    const shown = folded([
      b("0.0", "user", { text: "go" }),
      b("10.0", "thinking", { text: "hmm" }),
      b("10.1", "tool", { id: "a", name: "Read", input: '{"file_path":"/w/x.go"}' }),
      b("20.0", "result", { id: "a", text: "ok" }),
      b("30.0", "tool", { id: "b", name: "Bash", input: '{"command":"ls"}', result: { at: "31.0", text: "x", error: true } }),
      b("40.0", "assistant", { text: "done" }),
    ]);
    expect(shown.map((s) => s.kind)).toEqual(["user", "fold", "assistant"]);
    const fold = shown[1] as Extract<(typeof shown)[number], { kind: "fold" }>;
    expect(fold.steps.map((s) => s.at)).toEqual(["10.0", "10.1", "30.0"]);
    expect(fold.steps[1]!.result?.text).toBe("ok");
    expect(foldLine(fold.steps)).toEqual([
      { name: "", n: 1, failed: 0 },
      { name: "Read", n: 1, failed: 0 },
      { name: "Bash", n: 1, failed: 1 },
    ]);
  });
  test("calls with no result at the end run on their own; empty thoughts go", () => {
    const shown = folded([b("0.0", "user", { text: "go" }), b("5.0", "thinking", { text: "  " }), b("10.0", "tool", { id: "a", name: "Bash", input: "{}" })]);
    expect(shown.map((s) => s.kind)).toEqual(["user", "running"]);
  });
});

describe("argOf", () => {
  test("a file relative to the folder, an edit's lines, a command on one line", () => {
    expect(argOf({ name: "Edit", input: JSON.stringify({ file_path: "/w/a/b.ts", old_string: "x", new_string: "x\ny" }) }, "/w")).toEqual({ arg: "a/b.ts", code: true, stat: "+2 −1" });
    expect(argOf({ name: "Bash", input: JSON.stringify({ command: "go test\n  ./..." }) })).toEqual({ arg: "go test ./...", code: true });
    expect(argOf({ name: "WebSearch", input: JSON.stringify({ query: "iridium" }) })).toEqual({ arg: "iridium", code: false });
  });
  test("an input cut short is read for its first fields", () => {
    expect(inputOf('{"command":"ls -la","description":"lis')).toEqual({ command: "ls -la" });
  });
});

describe("filesIn", () => {
  test("pictures out of the text, file links as their names by their absolute paths; fenced code as it is", () => {
    const r = filesIn("See ![](a.png) and [the log](./logs/run.log#L3), [home](~/x.txt), `/w/b.mp4`.\n\n```\n![](/w/c.png)\n```", "/w", "/home/k");
    expect(r.text).toContain("See  and the log, home, `/w/b.mp4`.");
    expect(r.text).toContain("![](/w/c.png)");
    expect([...r.labels]).toEqual([
      ["/w/logs/run.log", "the log"],
      ["/home/k/x.txt", "home"],
    ]);
  });
  test("web links and pictures stay", () => {
    const r = filesIn("[x](https://example.com) ![y](https://example.com/a.png)");
    expect(r.labels.size).toBe(0);
    expect(r.text).toBe("[x](https://example.com) ![y](https://example.com/a.png)");
  });
  test("resolve: file://, a line, ~/ and the session's folder", () => {
    expect(resolve("file:///w/a.go:10:2", "/s")).toBe("/w/a.go");
    expect(resolve("b/c.md", "/s/")).toBe("/s/b/c.md");
    expect(resolve("~/d", "/s", "/home/k")).toBe("/home/k/d");
  });
});

describe("sourceOf", () => {
  const sessions = [{ id: "s1", agent: "codex", cwd: "/w", name: "holder", status: "idle", hosted: true, live: false, updatedAt: 0 } as Session];
  const endpoints = [
    { id: "p", name: "kumo-phone", kind: "panel" as const, avatar: "/a.png" },
    { id: "q", name: "QQ", kind: "channel" as const },
  ];
  test("your words: typed here, in a terminal, or on another panel with its avatar", () => {
    expect(sourceOf("网页", ["网页"], endpoints, sessions)).toEqual({ kind: "you" });
    expect(sourceOf(undefined, ["网页"], endpoints, sessions)).toEqual({ kind: "you" });
    expect(sourceOf("kumo-phone", ["网页"], endpoints, sessions)).toEqual({ kind: "you", panel: { name: "kumo-phone", avatar: "/a.png" } });
  });
  test("letters: a plugin, another session's ir; Iridium's own line", () => {
    expect(sourceOf("QQ", ["网页"], endpoints, sessions)).toMatchObject({ kind: "letter", name: "QQ", channel: true });
    expect(sourceOf("ir:s1", ["网页"], endpoints, sessions)).toMatchObject({ kind: "letter", name: "holder", agent: "codex", session: "s1" });
    expect(sourceOf("本机", ["网页"], endpoints, sessions)).toMatchObject({ kind: "letter", name: "本机" });
    expect(sourceOf("Iridium", ["网页"], endpoints, sessions)).toEqual({ kind: "iridium" });
  });
});

const S = (id: string, cwd: string, more: Partial<Session> = {}): Session => ({ id, agent: "claude", cwd, status: "idle", hosted: true, live: true, updatedAt: 0, title: id, ...more });

describe("buildTree", () => {
  const ws = [
    { id: 1, name: "Iridium", path: "/w/ir" },
    { id: 2, name: "Empty", path: "/w/empty" },
  ];
  test("workspace → folder → session; a folder of one is not shown; 未归类 named from its common folder", () => {
    const list = [S("a", "/w/ir", { workspace: 1 }), S("b", "/w/ir/web", { workspace: 1, updatedAt: 5 }), S("c", "/w/ir/web", { workspace: 1 }), S("d", "/w/ir/docs", { workspace: 1 }), S("e", "/w/x/one"), S("f", "/w/x/two"), S("g", "/w/x/two", { archived: true }), S("h", "/w/ir", { workspace: 1, status: "waiting" })];
    const t = buildTree(list, ws, "未归类");
    expect(t.workspaces.map((w) => w.name)).toEqual(["Iridium", "Empty", "未归类"]);
    const ir = t.workspaces[0]!;
    expect(ir.rows.map((r) => (r.kind === "session" ? r.s.id : `[${r.f.name}:${r.f.sessions.map((s) => s.id)}]`))).toEqual(["a", "d", "h", "[web:b,c]"]);
    expect(t.workspaces[1]!.rows).toEqual([]);
    expect(t.workspaces[2]!.root).toBe("/w/x");
    expect(t.archived.map((s) => s.id)).toEqual(["g"]);
    expect(t.needs.map((s) => s.id)).toEqual(["h"]);
    expect(flat(t).map((s) => s.id)).toEqual(["a", "d", "h", "b", "c", "e", "f"]);
  });
  test("commonDir", () => {
    expect(commonDir(["/a/b/c", "/a/b/d"])).toBe("/a/b");
    expect(commonDir(["/a/b", "/a/b"])).toBe("/a");
    expect(commonDir(["/a", "/b"])).toBe("");
  });
});

describe("jumpList", () => {
  const all = [S("a", "/w/ir", { name: "Holder" }), S("b", "/w/kdj", { title: "mini player", last: { role: "assistant", text: "the holder is done" } }), S("c", "/w/x", { name: "old", archived: true })];
  test("nothing typed: lately opened first, the open one not at all, no archived", () => {
    expect(jumpList(all, all.slice(0, 2), "", "a", ["b"]).map((s) => s.id)).toEqual(["b"]);
  });
  test("typed: name start, then name or folder, then the last words; archived last", () => {
    expect(jumpList(all, all.slice(0, 2), "hold", null, []).map((s) => s.id)).toEqual(["a", "b"]);
    expect(jumpList(all, all.slice(0, 2), "old", null, []).map((s) => s.id)).toEqual(["a", "b", "c"]);
  });
});

describe("composer", () => {
  test("the key: send idle; in a turn, stop with nothing typed, queue words, steer with ! or no queue", () => {
    expect(keyMode(false, "hi", 0, true)).toBe("send");
    expect(keyMode(true, " ", 0, true)).toBe("stop");
    expect(keyMode(true, "", 1, true)).toBe("queue");
    expect(keyMode(true, "hi", 0, true)).toBe("queue");
    expect(keyMode(true, "!hi", 0, true)).toBe("steer");
    expect(keyMode(true, "hi", 0, false)).toBe("steer");
  });
  test("commands: name start, name, description", () => {
    const cs = [
      { name: "context", description: "上下文占用" },
      { name: "compact", description: "压缩上下文" },
      { name: "cost", description: "spend" },
    ];
    expect(matching(cs, "co").map((c) => c.name)).toEqual(["context", "compact", "cost"]);
    expect(matching(cs, "压缩").map((c) => c.name)).toEqual(["compact"]);
    expect(matching(cs, "ntex").map((c) => c.name)).toEqual(["context"]);
    expect(commandsOf({ ...S("a", "/w"), agent: "pi", asks: [], tasks: [], queue: [], end: 0, proc: 0 })).toEqual([]);
  });
});

describe("context parts", () => {
  test("used largest first and darkest, then the reserve, then free", () => {
    const p = ctxParts({
      used: 300,
      max: 1000,
      at: 0,
      categories: [
        { id: "system", name: "System prompt", tokens: 100, kind: "used" },
        { id: "messages", name: "Messages", tokens: 200, kind: "used" },
        { id: "buffer", name: "Autocompact buffer", tokens: 150, kind: "buffer" },
        { id: "free", name: "Free space", tokens: 550, kind: "free" },
      ],
    });
    expect(p.map((x) => [x.key, x.tokens])).toEqual([
      ["messages", 200],
      ["system", 100],
      ["buffer", 150],
      ["free", 550],
    ]);
  });
  test("only a total: free is the rest", () => {
    expect(ctxParts({ used: 300, max: 1000, at: 0 }).map((x) => [x.key, x.tokens])).toEqual([["free", 700]]);
  });
});

describe("upgrade", () => {
  const last = { at: 0, build: null };
  const you = { back: true, busy: false };
  test("loads again only for another build, back in front, not busy, once per build, not within 10 s", () => {
    expect(reloadNow("/a.js", "/b.js", you, last, 20_000)).toBe(true);
    expect(reloadNow("/a.js", "/a.js", you, last, 20_000)).toBe(false);
    expect(reloadNow("/a.js", null, you, last, 20_000)).toBe(false);
    expect(reloadNow("/a.js", "/b.js", { back: false, busy: false }, last, 20_000)).toBe(false);
    expect(reloadNow("/a.js", "/b.js", { back: true, busy: true }, last, 20_000)).toBe(false);
    expect(reloadNow("/a.js", "/b.js", you, { at: 15_000, build: null }, 20_000)).toBe(false);
    expect(reloadNow("/a.js", "/b.js", you, { at: 0, build: "/b.js" }, 20_000)).toBe(false);
  });
  test("the build is the HTML's first module script", () => {
    expect(entryOf('<script src="/theme.js"></script><script type="module" crossorigin src="/assets/index-x.js"></script>')).toBe("/assets/index-x.js");
  });
});

describe("i18n", () => {
  test("en has exactly zh's keys, none empty", () => {
    expect(Object.keys(en).sort()).toEqual(Object.keys(zh).sort());
    for (const [k, v] of Object.entries(en)) expect([k, v.length > 0]).toEqual([k, true]);
    // Each {var} of a string is in its translation.
    for (const k of Object.keys(zh) as (keyof typeof zh)[]) expect([k, en[k].match(/\{\w+\}/g)?.sort()]).toEqual([k, zh[k].match(/\{\w+\}/g)?.sort()]);
  });
});

describe("names", () => {
  test("a session's name; ~ for home", () => {
    expect(titleOf({ cwd: "/w/x", title: "t" })).toBe("t");
    expect(titleOf({ cwd: "/w/x", name: "n", title: "t" })).toBe("n");
    expect(titleOf({ cwd: "/w/x/" })).toBe("x");
    expect(tilde("/home/kumo/a")).toBe("~/a");
    expect(tilde("/tmp/a")).toBe("/tmp/a");
  });
});
