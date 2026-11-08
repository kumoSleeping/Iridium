// The page's pure logic: folding tool calls, reading sources and file references, the sidebar's
// tree, the composer's key, the upgrade rules, the i18n dictionaries.
import { describe, expect, test } from "vitest";
import { windowFor } from "../src/quota";
import type { Block, Model, Session, Terminal } from "../src/api/types.gen";
import { argOf, folded, foldLine, inputOf } from "../src/chat/fold";
import { commandsOf, keyMode, matching } from "../src/chat/composer";
import { levelName, restAt } from "../src/chat/effort";
import { ctxParts, modelView } from "../src/chat/meters";
import { atSessions, atWord, mentions, shortId, sourceOf, withoutFiles, withoutNameLines, draftWords } from "../src/chat/refs";
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
    expect(argOf({ name: "Edit", input: JSON.stringify({ file_path: "/w/a/b.ts", old_string: "x", new_string: "x\ny" }) }, "/w")).toEqual({ arg: "a/b.ts", code: true, diff: { added: 1, taken: 0 } });
    expect(argOf({ name: "Bash", input: JSON.stringify({ command: "go test\n  ./..." }) })).toEqual({ arg: "go test ./...", code: true });
    expect(argOf({ name: "WebSearch", input: JSON.stringify({ query: "iridium" }) })).toEqual({ arg: "iridium", code: false });
  });
  test("an input cut short is read for its first fields", () => {
    expect(inputOf('{"command":"ls -la","description":"lis')).toEqual({ command: "ls -la" });
  });
});

describe("withoutFiles", () => {
  test("the file lines that gave a file out; others, prose paths and fenced code as they are", () => {
    const text = "Done.\n\n[文件] `/w/a.pdf`\n[文件] `~/b.png`\n[文件] `/w/gone.txt`\nsee `/w/c.go:3` and [log](./run.log)\n\n```\n[文件] `/w/a.pdf`\n```";
    const r = withoutFiles(text, new Set(["/w/a.pdf", "/home/k/b.png"]), "/home/k");
    expect(r).toBe("Done.\n\n[文件] `/w/gone.txt`\nsee `/w/c.go:3` and [log](./run.log)\n\n```\n[文件] `/w/a.pdf`\n```");
  });
  test("a local picture as its path; web pictures stay", () => {
    expect(withoutFiles("![a](/w/a.png) ![y](https://example.com/a.png)", new Set())).toBe("`/w/a.png` ![y](https://example.com/a.png)");
  });
  test("attachment lines in what you sent", () => {
    expect(withoutFiles("look\n\n[附件] `/w/up/a.png`", new Set(["/w/up/a.png"]))).toBe("look");
  });
});

describe("withoutNameLines", () => {
  test("a reply's title lines out; fenced code and inline mentions as they are", () => {
    expect(withoutNameLines("Done.\n\n[标题] 侧边栏单行标题\n")).toBe("Done.");
    expect(withoutNameLines("see [标题] here\n```\n[标题] kept\n```\n[标题] x")).toBe("see [标题] here\n```\n[标题] kept\n```");
  });
});

describe("draftWords", () => {
  test("a draft drops its title line, and while live a last line that may become one", () => {
    expect(draftWords("Done.\n\n[标题] 侧边", true)).toBe("Done.");
    expect(draftWords("Done.\n\n[标", true)).toBe("Done.");
    expect(draftWords("Done.\n\n[", true)).toBe("Done.");
    expect(draftWords("Done.\n\n[link", true)).toBe("Done.\n\n[link");
    expect(draftWords("Done.\n\n[", false)).toBe("Done.\n\n[");
    expect(draftWords("```\n[", true)).toBe("```\n[");
  });
});

describe("sourceOf", () => {
  const sessions = [
    { id: "s1", agent: "codex", cwd: "/w", name: "holder", status: "idle", hosted: true, live: false, updatedAt: 0 } as Session,
    { id: "s2", agent: "claude", cwd: "/home/k/Iridium", status: "idle", hosted: true, live: false, workspace: 3, updatedAt: 0 } as Session,
    { id: "s3", agent: "claude", cwd: "/home/k/notes/", title: "hi there", status: "idle", hosted: false, live: false, updatedAt: 0 } as Session,
  ];
  const ws = [{ id: 3, name: "Iri" }];
  const rules = {} as Terminal["rules"];
  const terminals: Terminal[] = [
    { id: "local", name: "本机", kind: "channel", local: true, rules, usedToday: 0, device: "computer" },
    { id: "me", name: "kumo-mac", kind: "panel", rules, usedToday: 0, device: "computer" },
    { id: "p", name: "kumo-phone", kind: "panel", avatar: "/a.png", rules, usedToday: 0, device: "phone" },
    { id: "q", name: "QQ", kind: "channel", avatar: "/q.png", rules, usedToday: 0 },
  ];
  const me = terminals[1];
  const local = terminals[0];
  test("your words: typed here, in a terminal, or on another panel, with its avatar and device", () => {
    expect(sourceOf("kumo-mac", "me", me, terminals, sessions)).toEqual({ kind: "you", name: "kumo-mac", device: "computer" });
    expect(sourceOf(undefined, undefined, me, terminals, sessions)).toEqual({ kind: "you", name: "本机", device: "computer", local: true }); // typed in the agent's own terminal
    expect(sourceOf("本机", "local", local, terminals, sessions)).toEqual({ kind: "you", name: "本机", device: "computer", local: true }); // the page with no gate
    expect(sourceOf("kumo-phone", "p", me, terminals, sessions)).toEqual({ kind: "you", name: "kumo-phone", avatar: "/a.png", device: "phone" });
    expect(sourceOf("本机", "local", me, terminals, sessions)).toEqual({ kind: "you", name: "本机", device: "computer", local: true });
    // No terminals list (yet): 本机 is still yours; no device is a computer.
    expect(sourceOf("本机", "local", { id: "x", name: "x" }, [], sessions)).toEqual({ kind: "you", name: "本机", device: "computer", local: true });
    expect(sourceOf("x", "x", { id: "x", name: "x" }, [], sessions)).toMatchObject({ kind: "you", device: "computer" });
  });
  test("letters: a plugin with its sender, another session's ir by its title as the sidebar has it, its workspace only when not this one's; Iridium's own line", () => {
    expect(sourceOf("QQ·张三", "q", me, terminals, sessions)).toMatchObject({ kind: "letter", name: "QQ·张三", avatar: "/q.png", channel: true });
    expect(sourceOf("ir:s1", "local", local, terminals, sessions)).toMatchObject({ kind: "letter", name: "holder", agent: "codex", session: "s1", channel: false, where: "/w" });
    expect(sourceOf("ir:s2", "p", me, terminals, sessions, ws)).toMatchObject({ kind: "letter", name: "Iridium", agent: "claude", session: "s2" }); // no title: its folder
    expect(sourceOf("ir:s3", "local", me, terminals, sessions, ws)).toMatchObject({ name: "hi there" }); // its first message's opening
    // Here: the chat shown is in workspace 3. One in it is its title alone; one elsewhere adds where it is.
    expect(sourceOf("ir:s2", "p", me, terminals, sessions, ws, { workspace: 3 })).toMatchObject({ name: "Iridium" });
    expect(sourceOf("ir:s3", "p", me, terminals, sessions, ws, { workspace: 3 })).toMatchObject({ name: "hi there · notes" });
    expect(sourceOf("ir:s3", "p", me, terminals, sessions, ws, {})).toMatchObject({ name: "hi there" }); // both unsorted
    expect(sourceOf("ir:s2", "p", me, terminals, sessions, ws, { workspace: 0 })).toMatchObject({ name: "Iridium · Iri" });
    expect(sourceOf("ir:s2", "p", me, terminals, sessions, ws, { parent: undefined })).toMatchObject({ name: "Iridium · Iri" }); // here unsorted, its field left out
    expect(sourceOf("ir:gone1234567", "local", me, terminals, sessions)).toEqual({ kind: "letter", name: "gone1234", channel: false });
    expect(sourceOf("Iridium", "Iridium", me, terminals, sessions)).toEqual({ kind: "iridium" });
  });
  test("a subagent's prompt is its parent's: by the parent's name, a workflow's with its name", () => {
    expect(sourceOf(undefined, undefined, me, terminals, sessions, ws, { subagent: true, parent: "s2", workspace: 3 })).toEqual({ kind: "letter", name: "Iridium", channel: false, agent: "claude", session: "s2", where: "/home/k/Iridium" });
    expect(sourceOf(undefined, undefined, me, terminals, sessions, ws, { subagent: true, parent: "s1", workflow: { run: "r", name: "review" } })).toEqual({ kind: "letter", name: "holder · review", channel: false, agent: "codex", session: "s1", workflow: true, where: "/w" });
    expect(sourceOf(undefined, undefined, me, terminals, sessions, ws, { subagent: true, parent: "gone1234567" })).toEqual({ kind: "letter", name: "gone1234", channel: false, session: "gone1234567" });
    // Not a subagent, or tagged: as before.
    expect(sourceOf(undefined, undefined, me, terminals, sessions, ws, { parent: "s2" })).toMatchObject({ kind: "you", local: true });
    expect(sourceOf("Iridium", undefined, me, terminals, sessions, ws, { subagent: true, parent: "s2" })).toEqual({ kind: "iridium" });
  });
});

const S = (id: string, cwd: string, more: Partial<Session> = {}): Session => ({ id, agent: "claude", cwd, status: "idle", hosted: true, live: true, updatedAt: 0, title: id, ...more });

describe("buildTree", () => {
  const ws = [
    { id: 1, name: "Iridium", path: "/w/ir" },
    { id: 2, name: "Empty", path: "/w/empty" },
  ];
  test("workspace → folder → session; a folder of one shown too; 未归类 named from its common folder; one that needs you stays in its place", () => {
    const list = [S("a", "/w/ir", { workspace: 1 }), S("b", "/w/ir/web", { workspace: 1, updatedAt: 5 }), S("c", "/w/ir/web", { workspace: 1 }), S("d", "/w/ir/docs", { workspace: 1 }), S("e", "/w/x/one"), S("f", "/w/x/two"), S("g", "/w/x/two", { archived: true }), S("h", "/w/ir", { workspace: 1, status: "waiting" })];
    const t = buildTree(list, ws, "未归类");
    // A group with no session shown is not listed (the user, 2026-11-08).
    expect(t.workspaces.map((w) => w.name)).toEqual(["Iridium", "未归类"]);
    const ir = t.workspaces[0]!;
    expect(ir.rows.map((r) => (r.kind === "session" ? r.s.id : `[${r.f.name}:${r.f.sessions.map((s) => s.id)}]`))).toEqual(["a", "h", "[web:b,c]", "[docs:d]"]);
    expect(t.workspaces[1]!.root).toBe("/w/x");
    expect(t.archived.map((s) => s.id)).toEqual(["g"]);
    expect(t.workspaces[1]!.rows.map((r) => (r.kind === "session" ? r.s.id : `[${r.f.name}:${r.f.sessions.map((s) => s.id)}]`))).toEqual(["[one:e]", "[two:f]"]);
    expect(flat(t).map((s) => s.id)).toEqual(["a", "h", "b", "c", "d", "e", "f"]);
  });
  test("where: the folder a row says, where the tree does not", () => {
    const list = [S("a", "/w/ir", { workspace: 1 }), S("b", "/w/ir/web", { workspace: 1 }), S("c", "/w/ir/web", { workspace: 1 }), S("d", "/w/ir/docs", { workspace: 1 }), S("e", "/w/x/one"), S("f", "/w/x/two"), S("g", "/w/x/two", { archived: true }), S("h", "/w/ir/web", { workspace: 1, status: "waiting" }), S("i", "/w/ir", { workspace: 1, archived: true })];
    const t = buildTree(list, ws, "未归类");
    expect(Object.fromEntries(t.where)).toEqual({ g: "two", i: "Iridium" });
  });
  test("a subagent: no row of its own, under the session that started it, and only at work", () => {
    const now = 10 * 60_000;
    const list = [
      S("p", "/w/ir", { workspace: 1, status: "running" }),
      S("q", "/w/ir", { workspace: 1, live: false }),
      S("k1", "/w/ir/web", { subagent: true, parent: "p", updatedAt: now - 1000 }),
      S("k2", "/w/ir/web", { subagent: true, parent: "p", updatedAt: now - 1000, done: true }),
      S("k3", "/w/ir/web", { subagent: true, parent: "p", updatedAt: now - 6 * 60_000 }),
      S("k4", "/w/ir/web", { subagent: true, parent: "q", updatedAt: now - 1000 }),
      S("k5", "/w/ir/web", { subagent: true, parent: "gone", updatedAt: now - 1000 }),
    ];
    const t = buildTree(list, ws, "未归类", now);
    expect(t.workspaces[0]!.rows.map((r) => (r.kind === "session" ? r.s.id : r.f.name))).toEqual(["p", "q"]);
    expect(Object.fromEntries([...t.kids].map(([k, v]) => [k, v.map((s) => s.id)]))).toEqual({ p: ["k1"] });
    expect(flat(t).map((s) => s.id)).toEqual(["p", "k1", "q"]);
    // Its parent's turn over, it works on (a background agent, a workflow); its process gone, so is it.
    expect(buildTree(list.map((s) => (s.id === "p" ? { ...s, status: "idle" as const } : s)), ws, "未归类", now).kids.get("p")?.map((s) => s.id)).toEqual(["k1"]);
    expect(buildTree(list.map((s) => (s.id === "p" ? { ...s, live: false } : s)), ws, "未归类", now).kids.size).toBe(0);
  });
  test("a workflow's agents: one row a run under its session, its phase the latest one's at work, done of those seen", () => {
    const now = 10 * 60_000;
    const run = (id: string, phase: string, more: Partial<Session> = {}) => S(id, "/w/ir", { subagent: true, parent: "p", updatedAt: now - 1000, workflow: { run: "wf_1", name: "fix-caret", phase }, ...more });
    const list = [S("p", "/w/ir", { workspace: 1 }), run("a1", "Investigate", { done: true }), run("a2", "Implement", { updatedAt: now - 500 }), run("a3", "Implement", { updatedAt: now - 900 }), S("k", "/w/ir", { subagent: true, parent: "p", updatedAt: now - 1000 })];
    const t = buildTree(list, ws, "未归类", now);
    expect(t.kids.get("p")?.map((s) => s.id)).toEqual(["k"]);
    expect(t.runs.get("p")?.map((r) => ({ ...r, working: r.working.map((s) => s.id) }))).toEqual([{ id: "wf_1", name: "fix-caret", phase: "Implement", done: 1, total: 3, working: ["a2", "a3"] }]);
    expect(flat(t).map((s) => s.id)).toEqual(["p", "k", "a2", "a3"]);
    // Its last agent done, the run is gone.
    expect(buildTree(list.map((s) => (s.workflow ? { ...s, done: true } : s)), ws, "未归类", now).runs.size).toBe(0);
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
  test("the key: send idle; in a turn, stop with nothing typed; words steer by default, Ctrl+Enter queues; the setting turns it round; ! or no queue steers", () => {
    expect(keyMode(false, "hi", 0, true)).toBe("send");
    expect(keyMode(true, " ", 0, true)).toBe("stop");
    expect(keyMode(true, "hi", 0, true)).toBe("steer");
    expect(keyMode(true, "", 1, true)).toBe("steer");
    expect(keyMode(true, "hi", 0, true, true, true)).toBe("queue");
    expect(keyMode(true, "hi", 0, true, false)).toBe("queue");
    expect(keyMode(true, "hi", 0, true, false, true)).toBe("steer");
    expect(keyMode(true, "!hi", 0, true, false)).toBe("steer");
    expect(keyMode(true, "!hi", 0, true, true, true)).toBe("steer");
    expect(keyMode(true, "hi", 0, false, false)).toBe("steer");
    expect(keyMode(true, "/compact", 0, true)).toBe("queue"); // a command waits for the turn's end
    expect(keyMode(true, "/compact", 0, true, true, true)).toBe("queue");
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
    // Every agent has the common ones (the user, 2026-11-06: Codex had none); described here.
    const pi = { ...S("a", "/w"), agent: "pi" as const, asks: [], tasks: [], denials: [], queue: [], end: 0, proc: 0 };
    expect(commandsOf(pi).map((c) => c.name)).toEqual(["clear", "compact", "context", "model", "mode"]);
    const codex = commandsOf({ ...pi, agent: "codex", commands: [{ name: "clear" }, { name: "compact" }, { name: "effort" }, { name: "review", description: "own" }] });
    expect(codex.map((c) => [c.name, !!c.description])).toEqual([["clear", true], ["compact", true], ["effort", true], ["review", true]]);
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

describe("model key", () => {
  const claude: Model[] = [
    { value: "default", name: "Default (recommended)", description: "Opus 5.5 · Best for everyday, complex tasks", resolved: "claude-opus-5-5", efforts: ["low", "medium", "high", "xhigh", "max"] },
    { value: "opus", name: "Opus 5.5", resolved: "claude-opus-5-5", efforts: ["low", "medium", "high", "xhigh", "max"] },
    { value: "haiku", name: "Haiku 5.5", resolved: "claude-haiku-5-5", efforts: ["low", "medium", "high", "xhigh", "max"] },
    { value: "claude-opus-4-6", name: "Opus 4.6", resolved: "claude-opus-4-6", efforts: ["low", "medium", "high", "max"] },
    { value: "claude-haiku-4-5", name: "Haiku 4.5", resolved: "claude-haiku-4-5", noEffort: true },
  ];
  const base = { agent: "claude" as const, models: claude, efforts: ["low", "medium", "high", "xhigh", "max"] };
  test("a new session names the concrete model and the level in use, not just Default", () => {
    // Before get_settings says: the default entry's alias.
    expect(modelView({ ...base }).name).toBe("Opus 5.5");
    const v = modelView({ ...base, model: "claude-opus-5-5", effortNow: "medium" });
    expect([v.name, v.value, v.level, v.isDefault]).toEqual(["Opus 5.5", "default", "medium", true]);
    // No alias: the description's head; nothing: the id as the agent named it.
    expect(modelView({ ...base, models: [{ value: "default", name: "Default", description: "Opus 9 · x" }] }).name).toBe("Opus 9");
    expect(modelView({ ...base, model: "claude-new-1" }).name).toBe("claude-new-1");
    // Without the session's list (the fallback), nothing is known yet.
    expect(modelView({ agent: "claude" }).name).toBe("");
  });
  test("a model set: ticked by its value, its own levels; one that takes none has no slider", () => {
    const v = modelView({ ...base, pick: "opus", model: "claude-opus-5-5", effort: "high", effortNow: "low" });
    expect([v.name, v.value, v.level]).toEqual(["Opus 5.5", "opus", "high"]);
    // Two aliases of one id: the one set is ticked.
    const two = [...claude, { value: "best", name: "Best", resolved: "claude-opus-5-5" }];
    expect(modelView({ ...base, models: two, pick: "best", model: "claude-opus-5-5" }).value).toBe("best");
    expect(modelView({ ...base, pick: "claude-opus-4-6", model: "claude-opus-4-6" }).levels).toEqual(["low", "medium", "high", "max"]);
    expect(modelView({ ...base, pick: "claude-haiku-4-5", model: "claude-haiku-4-5" }).levels).toEqual([]);
    // A level set that this model does not take: the one the agent says it runs at.
    expect(modelView({ ...base, pick: "claude-opus-4-6", model: "claude-opus-4-6", effort: "xhigh", effortNow: "high" }).level).toBe("high");
    expect(modelView({ ...base, pick: "claude-opus-4-6", model: "claude-opus-4-6", effort: "xhigh" }).level).toBe("");
    // Just picked: shown before the state says so.
    expect(modelView({ ...base, model: "claude-opus-5-5" }, "haiku").name).toBe("Haiku 5.5");
    // Codex: every entry its own id.
    const codex = modelView({ agent: "codex", model: "gpt-6-luna", models: [{ value: "gpt-6-luna", name: "Luna", resolved: "gpt-6-luna", efforts: ["low", "high"] }], efforts: ["minimal", "low"], effortNow: "low" });
    expect([codex.name, codex.value, codex.levels, codex.level]).toEqual(["Luna", "gpt-6-luna", ["low", "high"], "low"]);
  });
  test("the effort slider rests on the level's stop; with none, on medium's, not known", () => {
    const l = ["low", "medium", "high", "xhigh", "max"];
    expect(restAt(l, "xhigh")).toEqual({ i: 3, known: true });
    expect(restAt(l)).toEqual({ i: 1, known: false });
    expect(restAt(["low", "high"], "medium")).toEqual({ i: 0, known: false });
    expect(restAt(["off", "minimal", "low", "medium", "high", "xhigh", "max"], "off")).toEqual({ i: 0, known: true });
    expect([en["effort.xhigh"], zh["effort.xhigh"]]).toContain(levelName("xhigh"));
    expect(levelName("weird")).toBe("weird");
  });
});

describe("liquid", () => {
  test("shades every point in range, the same for the same time", async () => {
    const { shade, slosh } = await import("../src/chat/liquid");
    const a = new Uint8ClampedArray(4);
    const b = new Uint8ClampedArray(4);
    for (const [u, v, t] of [[0, 0, 0], [1, 1, 3.7], [0.5, 0.2, 42]] as const) {
      shade(u, v, t, 0.5, 0.1, a, 0);
      shade(u, v, t, 0.5, 0.1, b, 0);
      expect([...a]).toEqual([...b]);
      expect(a[3]).toBeGreaterThan(190);
    }
    // The slosh swings and settles.
    const s = { push: 0, vel: 1 };
    for (let i = 0; i < 300; i++) slosh(s, 1 / 60);
    expect(Math.abs(s.push) + Math.abs(s.vel)).toBeLessThan(0.01);
  });
});

describe("goo", () => {
  test("the knob leads, the front lags, all come to rest where the knob is to be", async () => {
    const { SPRINGS, step, still } = await import("../src/chat/goo");
    const ss = SPRINGS.map((p) => ({ ...p, x: 0, v: 0 }));
    for (let i = 0; i < 8; i++) for (const s of ss) step(s, 0.5, 1 / 60);
    expect(ss[0]!.x).toBeGreaterThan(ss[1]!.x + 0.05);
    for (let i = 0; i < 180; i++) for (const s of ss) step(s, 0.5, 1 / 60);
    expect(ss.every((s) => still(s, 0.5) || Math.abs(s.x - 0.5) < 1e-3)).toBe(true);
  });
});

describe("windowFor", () => {
  const w = (model?: string) => ({ group: "weekly", percent: 0, ...(model ? { model } : {}) });
  test("a window of all models always; one model's only where it is in use", () => {
    expect(windowFor(w(), [])).toBe(true);
    expect(windowFor(w("Fable"), ["claude-fable-5-1"])).toBe(true);
    expect(windowFor(w("Fable"), [undefined, "default", "Fable 5.1"])).toBe(true);
    expect(windowFor(w("Fable"), ["claude-opus-5-5", "opus", "Opus"])).toBe(false);
    expect(windowFor(w("Fable"), [undefined, "", undefined])).toBe(false);
    expect(windowFor(w("GPT-5.3-Codex-Spark"), ["gpt-5.3-codex-spark"])).toBe(true);
    expect(windowFor(w("GPT-5.3-Codex-Spark"), ["gpt-5.3-codex"])).toBe(false);
  });
});

describe("shortId", () => {
  test("eight characters, more only while another id shares them", () => {
    const a = "1a2b3c4d-0000-4000-8000-000000000001";
    const b = "1a2b3c4d-9000-4000-8000-000000000002";
    expect(shortId(a, [a])).toBe("1a2b3c4d");
    expect(shortId(a, [a, b])).toBe("1a2b3c4d-0");
    expect(shortId("abc", ["abc"])).toBe("abc");
  });
});

describe("mentions", () => {
  test("a dropped session's words, in either language, cut out of the text", () => {
    expect(mentions("ask 「Fix the login」（Claude 会话 ca5187d2） now")).toEqual(["ask ", { title: "Fix the login", agent: "Claude", id: "ca5187d2", text: "「Fix the login」（Claude 会话 ca5187d2）" }, " now"]);
    expect(mentions('"Run tests" (DeepSeek Harness session 1a2b3c4d-9)')).toEqual([{ title: "Run tests", agent: "DeepSeek Harness", id: "1a2b3c4d-9", text: '"Run tests" (DeepSeek Harness session 1a2b3c4d-9)' }]);
    expect(mentions("plain 「quoted」 words (session 12)")).toEqual(["plain 「quoted」 words (session 12)"]);
  });
});

describe("@ in the composer", () => {
  test("the @word the caret ends", () => {
    expect(atWord("@", 1)).toEqual({ from: 0, q: "" });
    expect(atWord("ask @sid", 8)).toEqual({ from: 4, q: "sid" });
    expect(atWord("ask @sid more", 8)).toEqual({ from: 4, q: "sid" });
    expect(atWord("mail a@b", 8)).toBeNull();
    expect(atWord("ask @sid more", 13)).toBeNull();
    expect(atWord("ask @si d", 7)).toEqual({ from: 4, q: "si" });
  });
  test("the sessions it may name", () => {
    const s = (id: string, title: string, updatedAt: number, more: Partial<Session> = {}) => ({ id, agent: "claude", cwd: "/home/k/" + id, title, status: "idle", hosted: true, live: false, updatedAt, ...more }) as Session;
    const list = [s("a1", "Sidebar work", 1), s("b2", "the sidebar bug", 5), s("c3", "Notes", 9), s("d4", "Sidebar old", 3, { archived: true }), s("e5", "Sidebar sub", 4, { subagent: true }), s("here", "Sidebar here", 9)];
    expect(atSessions(list, "", "here").map((x) => x.id)).toEqual(["c3", "b2", "a1"]);
    expect(atSessions(list, "side", "here").map((x) => x.id)).toEqual(["a1", "b2"]);
    expect(atSessions(list, "c3", "here").map((x) => x.id)).toEqual(["c3"]);
    expect(atSessions(list, "nothing", "here")).toEqual([]);
  });
});
