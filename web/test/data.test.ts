// The page's data against a mocked daemon: the API module's requests, a chat's pages and drafts,
// what you sent finding its block, uploads in chunks that resume, the carried-state store.
import { createHash } from "node:crypto";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import { api } from "../src/api/client";
import type { Block, MessagePage } from "../src/api/types.gen";
import { chatOf, KEPT_BLOCKS, Messages, unsent, watchTurns, type Sent } from "../src/chats";
import { Sha256 } from "../src/chat/sha256";
import { nextChunk, upload } from "../src/chat/upload";
import { getCarried, onCarried, setCarried } from "../src/store";
import { keysFor, streamURL } from "../src/stream";

type Call = { method: string; url: string; body?: unknown; headers: Headers };
let calls: Call[] = [];
let answer: (c: Call) => Response | Promise<Response>;
const json = (v: unknown, status = 200) => new Response(JSON.stringify(v), { status, headers: { "content-type": "application/json" } });

beforeEach(() => {
  calls = [];
  answer = () => json({ ok: true });
  vi.stubGlobal("fetch", async (url: string, init: RequestInit = {}) => {
    const body = typeof init.body === "string" ? JSON.parse(init.body) : init.body;
    const c = { method: init.method ?? "GET", url, body, headers: new Headers(init.headers) };
    calls.push(c);
    return answer(c);
  });
  localStorage.clear();
});
afterEach(() => vi.unstubAllGlobals());

describe("api", () => {
  test("send carries files only when there are some; answers go to the card", async () => {
    await api.send("s1", "hi");
    await api.send("s1", "hi", ["r1"], true);
    await api.answer({ card: "ask:s1:q", option: "always" });
    await api.answer({ card: "ask:s1:r", choices: ["0.1"], words: ["", "mine"] });
    await api.editQueued("s1", "in 1", "again");
    await api.putSettings({ idleReap: "2h", modes: { claude: "plan" } });
    await api.answerConfirm("c1", true);
    expect(calls.map((c) => [c.method, c.url, c.body])).toEqual([
      ["POST", "/api/sessions/s1/send", { text: "hi" }],
      ["POST", "/api/sessions/s1/send", { text: "hi", attachments: ["r1"], steer: true }],
      ["POST", "/api/answer", { card: "ask:s1:q", option: "always" }],
      ["POST", "/api/answer", { card: "ask:s1:r", choices: ["0.1"], words: ["", "mine"] }],
      ["PUT", "/api/sessions/s1/queue/in%201", { text: "again" }],
      ["PUT", "/api/settings", { idleReap: "2h", modes: { claude: "plan" } }],
      ["POST", "/api/confirms/c1", { allow: true }],
    ]);
    expect(calls.every((c) => c.headers.get("x-requested-with") === "XMLHttpRequest")).toBe(true);
  });
  test("pages: newest, before=, after=; a refusal throws with its error", async () => {
    answer = () => json({ blocks: [], start: 0, end: 0, more: false });
    await api.messages("s 1");
    await api.messages("s1", { before: 10 });
    await api.messages("s1", { after: 20 });
    expect(calls.map((c) => c.url)).toEqual(["/api/sessions/s%201/messages", "/api/sessions/s1/messages?before=10", "/api/sessions/s1/messages?after=20"]);
    answer = () => json({ error: "the session's agent is not running" }, 409);
    await expect(api.interrupt("s1")).rejects.toMatchObject({ status: 409, message: "the session's agent is not running" });
  });
  test("files and uploads by the session", () => {
    expect(api.fileURL("s 1", "r/1")).toBe("/api/sessions/s%201/files/r%2F1");
    expect(api.fileURL("s", "r", true)).toBe("/api/sessions/s/files/r?download");
    expect(api.thumbURL("s", "r")).toBe("/api/sessions/s/files/r/thumb?size=1024");
  });
  test("writes that may go twice go again after a 502", async () => {
    let n = 0;
    answer = () => (n++ === 0 ? json({ error: "bad gateway" }, 502) : json({ ok: true }));
    await api.mode("s1", "plan");
    expect(calls.length).toBe(2);
  }, 10_000);
});

describe("the event stream", () => {
  test("names the list, the quota, the update, the confirmations and the open session with its context", () => {
    expect(keysFor(null)).toEqual(["list", "workspaces", "colors", "quota", "update", "confirms", "terminals"]);
    expect(streamURL("a")).toBe("/api/events?sub=list&sub=workspaces&sub=colors&sub=quota&sub=update&sub=confirms&sub=terminals&sub=session%3Aa&sub=context%3Aa");
  });
  test("the store: whole values, null drops, listeners told", () => {
    const told: string[] = [];
    const stop = onCarried((k) => told.push(k));
    setCarried("x", { a: 1 });
    setCarried("x", null);
    setCarried("x", null);
    stop();
    expect(told).toEqual(["x", "x"]);
    expect(getCarried("x")).toBeUndefined();
  });
});

const blk = (line: number, kind: Block["kind"], text?: string, more: Partial<Block> = {}): Block => ({ at: `${line}.0`, kind, text, ...more });

describe("Messages", () => {
  test("the newest page, then what came after till nothing new; older pages before", async () => {
    const pages: Record<string, MessagePage> = {
      "": { blocks: [blk(100, "user", "a"), blk(200, "assistant", "b")], start: 100, end: 300, more: true },
      "after=300": { blocks: [blk(300, "user", "c")], start: 300, end: 400, more: false },
      "after=400": { blocks: [], start: 400, end: 400, more: false },
      "before=100": { blocks: [blk(0, "user", "z")], start: 0, end: 100, more: false },
    };
    const m = new Messages("s", async (_, q) => pages[q.after !== undefined ? `after=${q.after}` : q.before !== undefined ? `before=${q.before}` : ""]!);
    await m.newer();
    expect(m.blocks.map((b) => b.text)).toEqual(["a", "b"]);
    await m.newer();
    expect(m.blocks.map((b) => b.text)).toEqual(["a", "b", "c"]);
    expect(m.end).toBe(400);
    expect(await m.older()).toBe(true);
    expect(m.blocks.map((b) => b.text)).toEqual(["z", "a", "b", "c"]);
    expect(await m.older()).toBe(false);
  });
  test("the page before is asked for ahead, once: scrolling up takes it without waiting", async () => {
    const asked: string[] = [];
    const pages: Record<string, MessagePage> = {
      "": { blocks: [blk(200, "user", "b")], start: 200, end: 300, more: true },
      "before=200": { blocks: [blk(100, "user", "a")], start: 100, end: 200, more: true },
      "before=100": { blocks: [blk(0, "user", "z")], start: 0, end: 100, more: false },
    };
    const m = new Messages("s", async (_, q) => {
      const k = q.before !== undefined ? `before=${q.before}` : "";
      asked.push(k);
      return pages[k]!;
    });
    await m.newer();
    expect(asked).toEqual(["", "before=200"]);
    expect(await m.older()).toBe(true);
    // Taken from what was asked ahead; the next one asked for at once.
    expect(asked).toEqual(["", "before=200", "before=100"]);
    expect(await m.older()).toBe(true);
    expect(m.blocks.map((b) => b.text)).toEqual(["z", "a", "b"]);
    // The first page reached: nothing more asked for.
    expect(await m.older()).toBe(false);
    expect(asked).toEqual(["", "before=200", "before=100"]);
  });
  test("drafts: same n joins, another n seals, another proc drops; the transcript's words take a draft's place", async () => {
    let page: MessagePage = { blocks: [], start: 0, end: 0, more: false };
    const m = new Messages("s", async () => page);
    await m.newer();
    m.wrote({ session: "s", proc: 1, n: 1, text: "Hel" });
    m.wrote({ session: "s", proc: 1, n: 1, text: "lo" });
    m.wrote({ session: "s", proc: 1, n: 2, text: "Next" });
    expect(m.drafts.map((d) => [d.text, d.whole])).toEqual([
      ["Hello", true],
      ["Next", false],
    ]);
    page = { blocks: [blk(0, "assistant", "Hello")], start: 0, end: 50, more: false };
    await m.newer();
    expect(m.drafts.map((d) => d.text)).toEqual(["Next"]);
    page = { blocks: [blk(0, "assistant", "Hello"), blk(50, "assistant", "Next, and more")], start: 0, end: 90, more: false };
    await m.newer();
    // Too few words to tell: it waits till it has the block's.
    expect(m.drafts.map((d) => d.text)).toEqual(["Next"]);
    m.wrote({ session: "s", proc: 1, n: 2, text: ", and more" });
    expect(m.drafts).toEqual([]);
    m.wrote({ session: "s", proc: 1, n: 3, text: "x" });
    m.wrote({ session: "s", proc: 2, n: 1, text: "y" });
    expect(m.drafts.map((d) => d.text)).toEqual(["y"]);
  });
  test("a piece of a draft that comes after the transcript had it starts no new draft", async () => {
    let page: MessagePage = { blocks: [], start: 0, end: 0, more: false };
    const m = new Messages("s", async () => page);
    await m.newer();
    m.wrote({ session: "s", proc: 1, n: 4, text: "Rivers carve their valleys over many " });
    page = { blocks: [blk(0, "assistant", "Rivers carve their valleys over many years.")], start: 0, end: 50, more: false };
    await m.newer();
    expect(m.drafts).toEqual([]);
    m.wrote({ session: "s", proc: 1, n: 4, text: "years." });
    expect(m.drafts).toEqual([]);
    m.wrote({ session: "s", proc: 1, n: 5, text: "Next" });
    expect(m.drafts.map((d) => d.text)).toEqual(["Next"]);
    m.dropDrafts();
    m.wrote({ session: "s", proc: 1, n: 5, text: " late" });
    expect(m.drafts).toEqual([]);
  });
  test("done seals a draft; one of only blanks goes at once, the transcript keeps none", async () => {
    const m = new Messages("s", async () => ({ blocks: [], start: 0, end: 0, more: false }));
    await m.newer();
    m.wrote({ session: "s", proc: 1, n: 1, text: "Hi" });
    m.wrote({ session: "s", proc: 1, n: 1, text: "", done: true });
    expect(m.drafts.map((d) => [d.text, d.whole])).toEqual([["Hi", true]]);
    m.wrote({ session: "s", proc: 1, n: 2, text: "\n\n", done: true });
    expect(m.drafts.map((d) => d.text)).toEqual(["Hi"]);
  });
  test("words that end on a colon, then a tool call: sealed at once, no caret while the tool runs", async () => {
    let page: MessagePage = { blocks: [], start: 0, end: 0, more: false };
    const m = new Messages("s", async () => page);
    await m.newer();
    m.wrote({ session: "s", proc: 1, n: 1, text: "记好了。接着处理按钮和侧栏：" });
    expect(m.drafts.map((d) => d.whole)).toEqual([false]); // being written: the caret shows
    // The text block's end (MessageDone), before the tool_use streams: the draft is whole, the
    // caret (chat.tsx: live = !whole && busy) goes though the turn runs on.
    m.wrote({ session: "s", proc: 1, n: 1, text: "", done: true });
    expect(m.drafts.map((d) => [d.text, d.whole])).toEqual([["记好了。接着处理按钮和侧栏：", true]]);
    // Its block lands with the tool call: the draft gives way to it, its words not shown twice.
    page = { blocks: [blk(0, "assistant", "记好了。接着处理按钮和侧栏："), blk(10, "tool", undefined, { id: "t" })], start: 0, end: 20, more: false };
    await m.newer();
    expect(m.drafts).toEqual([]);
  });
  test("a tool call drafted as it is written: its block takes it by its id, and the words before it", async () => {
    let page: MessagePage = { blocks: [], start: 0, end: 0, more: false };
    const m = new Messages("s", async () => page);
    await m.newer();
    m.wrote({ session: "s", proc: 1, n: 1, text: "Let me look at the tree first, then the rest:" });
    m.wrote({ session: "s", proc: 1, n: 1, text: "", done: true });
    m.wrote({ session: "s", proc: 1, n: 2, text: "", tool: { id: "t1", name: "Bash" } });
    m.wrote({ session: "s", proc: 1, n: 2, text: '{"command":"ls -l' });
    expect(m.drafts.map((d) => [d.tool?.id, d.text, d.whole])).toEqual([
      [undefined, "Let me look at the tree first, then the rest:", true],
      ["t1", '{"command":"ls -l', false],
    ]);
    m.wrote({ session: "s", proc: 1, n: 2, text: '"}', done: true });
    expect(m.drafts.at(-1)).toMatchObject({ whole: true, text: '{"command":"ls -l"}' }); // sealed, still shown
    // An assistant block never takes a call's draft by its words or its place.
    page = { blocks: [blk(0, "assistant", "Let me look at the tree first, then the rest:")], start: 0, end: 5, more: false };
    await m.newer();
    expect(m.drafts.map((d) => d.tool?.id)).toEqual(["t1"]);
    page = { blocks: [blk(0, "assistant", "Let me look at the tree first, then the rest:"), blk(5, "tool", undefined, { id: "t1", name: "Bash" })], start: 0, end: 20, more: false };
    await m.newer();
    expect(m.drafts).toEqual([]);
  });
  test("a call the transcript brought before its draft: the draft that comes late is none", async () => {
    let page: MessagePage = { blocks: [], start: 0, end: 0, more: false };
    const m = new Messages("s", async () => page);
    await m.newer();
    page = { blocks: [blk(5, "tool", undefined, { id: "t9", name: "Read" })], start: 0, end: 20, more: false };
    await m.newer();
    m.wrote({ session: "s", proc: 1, n: 4, text: "", tool: { id: "t9", name: "Read" } });
    m.wrote({ session: "s", proc: 1, n: 4, text: '{"file_path":"/a"}' });
    expect(m.drafts).toEqual([]);
  });
  test("a blank draft before a tool call shifts no pairing", async () => {
    let page: MessagePage = { blocks: [], start: 0, end: 0, more: false };
    const m = new Messages("s", async () => page);
    await m.newer();
    m.wrote({ session: "s", proc: 1, n: 1, text: "\n\n" });
    m.wrote({ session: "s", proc: 1, n: 2, text: "Let me look" });
    m.wrote({ session: "s", proc: 1, n: 2, text: "", done: true });
    page = { blocks: [blk(0, "assistant", "Let me look"), blk(10, "tool", undefined, { id: "t" })], start: 0, end: 20, more: false };
    await m.newer();
    expect(m.drafts).toEqual([]);
  });
  test("a block that comes before its draft takes the draft as it is sealed", async () => {
    let page: MessagePage = { blocks: [], start: 0, end: 0, more: false };
    const m = new Messages("s", async () => page);
    await m.newer();
    expect(m.fromTail({ start: 0, end: 20, blocks: [blk(0, "assistant", "Checking.")] })).toBe(true);
    m.wrote({ session: "s", proc: 1, n: 3, text: "Checking.", done: true });
    expect(m.drafts).toEqual([]);
    m.wrote({ session: "s", proc: 1, n: 3, text: " late" });
    expect(m.drafts).toEqual([]);
  });
  test("a block takes its own draft by its words and the stale ones before it", async () => {
    let page: MessagePage = { blocks: [], start: 0, end: 0, more: false };
    const m = new Messages("s", async () => page);
    await m.newer();
    m.wrote({ session: "s", proc: 1, n: 1, text: "Alpha says", done: true });
    m.wrote({ session: "s", proc: 1, n: 2, text: "Beta is writing a longer message, more " });
    page = { blocks: [blk(0, "assistant", "Beta is writing a longer message, more than a head.")], start: 0, end: 20, more: false };
    await m.newer();
    expect(m.drafts).toEqual([]);
  });
  test("a draft joined partway goes with the next block, by place", async () => {
    const m = new Messages("s", async () => ({ blocks: [], start: 0, end: 0, more: false }));
    await m.newer();
    m.wrote({ session: "s", proc: 1, n: 3, text: "the middle and end of it.", done: true });
    expect(m.fromTail({ start: 0, end: 30, blocks: [blk(0, "assistant", "Here is the start, the middle and end of it."), blk(10, "tool", undefined, { id: "t" })] })).toBe(true);
    expect(m.drafts).toEqual([]);
    // A long one, by its tail, even with a draft after it.
    m.wrote({ session: "s", proc: 1, n: 4, text: "and that is how the long message ends, at last.", done: true });
    m.wrote({ session: "s", proc: 1, n: 5, text: "Next" });
    expect(m.fromTail({ start: 0, end: 50, blocks: [blk(30, "assistant", "Started before: and that is how the long message ends, at last.")] })).toBe(true);
    expect(m.drafts.map((d) => d.text)).toEqual(["Next"]);
  });
  test("a few words of a draft are not taken by a block that begins with them", async () => {
    let page: MessagePage = { blocks: [blk(0, "user", "go"), blk(10, "assistant", "I'll start by reading the README.")], start: 0, end: 20, more: false };
    const m = new Messages("s", async () => page);
    // Before the first page: an older block of the turn begins the same.
    m.wrote({ session: "s", proc: 1, n: 1, text: "I'll" });
    await m.newer();
    m.wrote({ session: "s", proc: 1, n: 1, text: " check the tests." });
    expect(m.drafts.map((d) => d.text)).toEqual(["I'll check the tests."]);
    // A new block, the one before (its draft missed), while the next begins.
    m.wrote({ session: "s", proc: 1, n: 1, text: "", done: true });
    m.wrote({ session: "s", proc: 1, n: 2, text: "Let me" });
    page = { blocks: [blk(20, "assistant", "I'll check the tests."), blk(30, "assistant", "Let me look at the config file first.")], start: 20, end: 40, more: false };
    await m.newer();
    m.wrote({ session: "s", proc: 1, n: 2, text: " run the tests now." });
    expect(m.drafts.map((d) => d.text)).toEqual(["Let me run the tests now."]);
  });
  test("the turn's end drops the drafts it had, not the next turn's", async () => {
    const m = new Messages("s", async () => ({ blocks: [], start: 0, end: 0, more: false }));
    await m.newer();
    m.wrote({ session: "s", proc: 1, n: 1, text: "Last words of the turn", done: true });
    const upTo = m.mark();
    m.wrote({ session: "s", proc: 1, n: 2, text: "Next turn" });
    m.dropDrafts(upTo);
    m.wrote({ session: "s", proc: 1, n: 2, text: " goes on" });
    expect(m.drafts.map((d) => d.text)).toEqual(["Next turn goes on"]);
    m.wrote({ session: "s", proc: 1, n: 1, text: " late" });
    expect(m.drafts.map((d) => d.text)).toEqual(["Next turn goes on"]);
  });
  test("done for a draft never seen: its late pieces start none", async () => {
    const m = new Messages("s", async () => ({ blocks: [], start: 0, end: 0, more: false }));
    await m.newer();
    m.wrote({ session: "s", proc: 1, n: 7, text: "", done: true });
    m.wrote({ session: "s", proc: 1, n: 7, text: "tail" });
    expect(m.drafts).toEqual([]);
    m.wrote({ session: "s", proc: 1, n: 8, text: "new" });
    expect(m.drafts.map((d) => d.text)).toEqual(["new"]);
  });
  test("the state's tail brings the blocks since without a fetch, when it reaches back far enough", async () => {
    const m = new Messages("s", async () => ({ blocks: [blk(0, "user", "a"), blk(10, "tool", undefined, { id: "t" })], start: 0, end: 20, more: false }));
    await m.newer();
    expect(m.fromTail({ start: 30, end: 40, blocks: [] })).toBe(false); // a gap: fetch
    expect(m.fromTail({ start: 5, end: 40, blocks: [blk(10, "tool", undefined, { id: "t", result: { at: "25.0", text: "ok" } }), blk(30, "assistant", "b")] })).toBe(true);
    expect(m.blocks.map((b) => [b.at, b.result?.text ?? b.text])).toEqual([
      ["0.0", "a"],
      ["10.0", "ok"],
      ["30.0", "b"],
    ]);
    expect(m.end).toBe(40);
    expect(m.fromTail({ start: 5, end: 40, blocks: null as never })).toBe(false);
  });
  test("what you sent finds its block by its input id, else its words; a failed one stays", () => {
    const sent: Sent[] = [
      { key: "1", text: "hello", files: [], from: 0, input: "u1" },
      { key: "2", text: "second", files: [], from: 0 },
      { key: "3", text: "third", files: [], from: 0, failed: "no" },
    ];
    expect(unsent(sent, [blk(10, "user", "different", { id: "u1" }), blk(20, "user", "second")]).map((s) => s.key)).toEqual(["3"]);
    expect(unsent(sent.slice(1, 2), [blk(10, "user", "nope")]).map((s) => s.key)).toEqual(["2"]);
  });
  test("chats kept lately: up to KEPT_BLOCKS blocks in all, the one asked for always", () => {
    const a = chatOf("keep-a");
    a.log.blocks = Array.from({ length: KEPT_BLOCKS }, (_, i) => blk(i, "user", "x"));
    const b = chatOf("keep-b");
    b.log.blocks = [blk(0, "user", "y")];
    chatOf("keep-c");
    expect(chatOf("keep-a")).not.toBe(a); // pushed out: a fresh one
    expect(chatOf("keep-c").log).toBeInstanceOf(Messages);
  });
  test("a turn that ends is told once, as running goes idle", () => {
    const was = new Map<string, string>();
    const base = { agent: "claude" as const, cwd: "/w", hosted: true, live: true, updatedAt: 0 };
    watchTurns([{ ...base, id: "t", status: "running" }], was, null);
    watchTurns([{ ...base, id: "t", status: "idle" }], was, null);
    expect(was.get("t")).toBe("idle");
  });
});

describe("uploads", () => {
  test("sha256 fed in pieces is the whole's", () => {
    const data = new Uint8Array(200_003).map((_, i) => (i * 31) % 251);
    const h = new Sha256();
    for (let at = 0; at < data.length; at += 7_777) h.update(data.subarray(at, at + 7_777));
    expect(h.hex()).toBe(createHash("sha256").update(data).digest("hex"));
    expect(new Sha256().hex()).toBe(createHash("sha256").digest("hex"));
  });
  test("chunks are sized from the speed, 1MB to 50MB", () => {
    expect(nextChunk(8 << 20, 1000)).toBe(50 << 20);
    expect(nextChunk(8 << 20, 120_000)).toBe(1 << 20);
    expect(nextChunk(8 << 20, 15_000)).toBe(8 << 20);
  });
  test("an upload goes in chunks, then resumes from what the server has", async () => {
    const size = 3 * 1024 * 1024 + 5;
    const file = new File([new Uint8Array(size).map((_, i) => i % 256)], "big.bin", { lastModified: 1 });
    let received = 0;
    let fail = true;
    const up = () => ({ id: "up1", session: "s1", name: "big.bin", size, received, done: false, max: 1 << 31 });
    answer = async (c) => {
      if (c.method === "POST" && c.url === "/api/sessions/s1/uploads") {
        expect(c.body).toEqual({ name: "big.bin", size });
        return json(up());
      }
      if (c.method === "GET" && c.url === "/api/uploads/up1") return json(up());
      if (c.method === "PUT") {
        const offset = Number(new URL(c.url, "http://x").searchParams.get("offset"));
        const n = (c.body as Blob).size;
        // The first chunk lands but half, its answer lost; the next asks from the wrong place once.
        if (fail) {
          fail = false;
          received = offset + Math.floor(n / 2);
          throw new TypeError("network");
        }
        if (offset !== received) return json({ ...up(), error: "offset" }, 409);
        received = offset + n;
        return json(up());
      }
      if (c.url === "/api/uploads/up1/complete") {
        expect((c.body as { sha256: string }).sha256).toBe(createHash("sha256").update(new Uint8Array(await file.arrayBuffer())).digest("hex"));
        return json({ ...up(), done: true, file: { ref: "r1", name: "big.bin", path: "/w/.iridium/uploads/big.bin", size, mime: "application/octet-stream", kind: "file", mtime: 0 } });
      }
      return json({ error: "?" }, 404);
    };
    const seen: number[] = [];
    const done = await upload("s1", file, (sent) => seen.push(sent));
    expect(done.ref).toBe("r1");
    expect(seen.at(-1)).toBe(size);
    // The task is gone once done.
    expect(Object.keys(localStorage).filter((k) => k.startsWith("upload:"))).toEqual([]);
  }, 20_000);
});
