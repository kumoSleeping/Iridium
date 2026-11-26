// The device's own cache (docs/plan.md "本机缓存"): what a sweep lets go, what the worker keeps,
// and that the worker and the page have one database.
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, test } from "vitest";
import type { Block } from "../src/api/types.gen";
import { DB, DB_VERSION, DEFAULTS, IMAGES, plan, STORES, usageOf, type Entry } from "../src/device/store";

const DAY = 86_400_000;
const MB = 1024 * 1024;
const now = 100 * DAY;
const e = (key: string, size: number, daysAgo: number, cat: Entry["cat"] = "image"): Entry => ({ key, cat, size, used: now - daysAgo * DAY });

describe("a sweep", () => {
  test("lets go of what was not used for the days kept", () => {
    const es = [e("a", 1, 8), e("b", 1, 6.9), e("c", 1, 7.01, "chat"), e("d", 1, 0)];
    expect(plan(es, now, DEFAULTS).sort()).toEqual(["a", "c"]);
    expect(plan(es, now, { ...DEFAULTS, days: 1 }).sort()).toEqual(["a", "b", "c"]);
  });
  test("past the size, the least lately used first, till the rest is within it", () => {
    const es = [e("new", 60 * MB, 0), e("old", 60 * MB, 3), e("mid", 60 * MB, 1, "face"), e("chat", 30 * MB, 2, "chat")];
    // 210 MB over 200: the oldest alone.
    expect(plan(es, now, DEFAULTS)).toEqual(["old"]);
    // Within 100: old, chat, mid let go, new left.
    expect(plan(es, now, { ...DEFAULTS, capMB: 100 })).toEqual(["old", "chat", "mid"]);
    expect(plan(es, now, { ...DEFAULTS, capMB: 1000 })).toEqual([]);
  });
  test("the old ones out first count no more toward the size", () => {
    const es = [e("stale", 150 * MB, 9), e("a", 100 * MB, 1), e("b", 90 * MB, 0)];
    expect(plan(es, now, DEFAULTS)).toEqual(["stale"]);
  });
});

test("the usage by kind", () => {
  expect(usageOf([e("a", 10, 0), e("b", 5, 0, "face"), e("c", 7, 0, "image"), e("d", 3, 0, "chat")])).toEqual({
    image: { n: 2, bytes: 17 },
    face: { n: 1, bytes: 5 },
    chat: { n: 1, bytes: 3 },
  });
});

describe("the worker", () => {
  const src = readFileSync(join(__dirname, "../public/sw.js"), "utf8");
  const worker = new Function("self", `${src}; return { category, IMAGES, DB, DB_VERSION, STORES };`)({ addEventListener() {} }) as {
    category: (p: string) => string | null;
    IMAGES: string;
    DB: string;
    DB_VERSION: number;
    STORES: string[];
  };
  test("has the page's database and cache", () => {
    expect([worker.IMAGES, worker.DB, worker.DB_VERSION, worker.STORES]).toEqual([IMAGES, DB, DB_VERSION, [...STORES]]);
  });
  test("keeps the faces and the pictures, nothing else", () => {
    const kinds = {
      "/v1/internal/channels/abc/avatar": "face",
      "/v1/internal/channels/abc/background": "face",
      "/v1/internal/people/u1/background": "face",
      "/v1/internal/roles/r1/avatar": "face",
      "/_ir/avatar/bot": "face",
      "/v1/internal/face": "face",
      "/v1/internal/sessions/s1/image": "image",
      "/v1/internal/sessions/s1/files/f1/thumb": "image",
      "/v1/internal/sessions/s1/files/f1": null,
      "/v1/internal/sessions/s1": null,
      "/v1/internal/uploads/u1": null,
      "/assets/index.js": null,
      "/": null,
    };
    for (const [path, kind] of Object.entries(kinds)) expect([path, worker.category(path)]).toEqual([path, kind]);
  });
});

describe("a chat's copy", async () => {
  const { newestPage } = await import("../src/device/chats");
  const { Messages } = await import("../src/chats");
  const { ChannelLog } = await import("../src/chat/channel/log");
  const blk = (line: number, kind: Block["kind"], text?: string, sub = 0): Block => ({ at: `${line}.${sub}`, kind, text });

  test("keeps the newest page: back to 20 things said, from a line's first block", () => {
    const blocks: Block[] = [];
    for (let i = 0; i < 30; i++) blocks.push(blk(i * 2, "user", `q${i}`), blk(i * 2 + 1, "tool", undefined, 0), blk(i * 2 + 1, "assistant", `a${i}`, 1));
    const p = newestPage(blocks, 0, false);
    expect(p.blocks[0]!.at).toBe("40.0");
    expect(p.blocks.length).toBe(30);
    expect([p.start, p.end, p.more]).toEqual([40, 60, true]);
    // A short chat whole.
    const few = blocks.slice(0, 6);
    expect(newestPage(few, 0, false)).toEqual({ blocks: few, start: 0, end: 4, more: false });
  });

  test("the server's page where it differs; only what is new where it agrees", () => {
    const mine = { blocks: [blk(0, "user", "hi"), blk(1, "assistant", "hello")], start: 0, end: 2, more: false };
    const m = new Messages("s", async () => ({ blocks: [], start: 0, end: 2, more: false }));
    m.take(mine);
    m.restored = true;
    const v = m.version;
    m.take({ blocks: [...mine.blocks, blk(2, "user", "more")], start: 0, end: 3, more: false });
    expect(m.blocks.map((b) => b.text)).toEqual(["hi", "hello", "more"]);
    expect(m.version).toBeGreaterThan(v);
    expect(m.restored).toBe(false);
    // Taken again, held: nothing.
    m.take({ blocks: [blk(9, "user", "x")], start: 9, end: 10, more: false });
    expect(m.blocks.length).toBe(3);

    const n = new Messages("s", async () => ({ blocks: [], start: 0, end: 2, more: false }));
    n.take(mine);
    n.restored = true;
    // Compacted meanwhile: the block at 1 is another.
    n.take({ blocks: [blk(0, "user", "hi"), blk(1, "assistant", "summary")], start: 0, end: 2, more: true });
    expect(n.blocks.map((b) => b.text)).toEqual(["hi", "summary"]);
    expect(n.more).toBe(true);
  });

  test("the channel's copy: a message taken back since goes, a reaction since comes", async () => {
    const msg = (n: number, text: string, reactions?: { emoji: string; count: number; users: { id: string }[] }[]) => ({ id: String(n), content: text, user: { id: "u", name: "u" }, _data: { ...(reactions ? { reactions } : {}) } });
    const log = new ChannelLog("c", async () => ({ data: [], start: 0, end: 0, more: false }) as never);
    const { msgOf } = await import("../src/chat/channel/lib");
    log.restore([msgOf(msg(1, "a") as never)!, msgOf(msg(2, "b") as never)!, msgOf(msg(3, "c") as never)!], false);
    expect(log.msgs.map((m) => m.n)).toEqual([1, 2, 3]);
    log.take({ data: [msg(1, "a"), msg(3, "c"), msg(4, "d")], start: 1, end: 4, more: false } as never);
    expect(log.msgs.map((m) => m.n)).toEqual([1, 3, 4]);
    expect(log.restored).toBe(false);
    expect(log.late).toBe(1);
  });

  test("the channel's copy: its last message written on since is late too; the same, not", async () => {
    const msg = (n: number, text: string) => ({ id: String(n), content: text, user: { id: "u", name: "u" }, _data: {} });
    const { msgOf } = await import("../src/chat/channel/lib");
    const grown = new ChannelLog("c", async () => ({ data: [], start: 0, end: 0, more: false }) as never);
    grown.restore([msgOf(msg(1, "a") as never)!, msgOf(msg(2, "b") as never)!], false);
    grown.take({ data: [msg(1, "a"), msg(2, "b and more")], start: 1, end: 2, more: false } as never);
    expect(grown.late).toBe(1);
    const same = new ChannelLog("c", async () => ({ data: [], start: 0, end: 0, more: false }) as never);
    same.restore([msgOf(msg(1, "a") as never)!, msgOf(msg(2, "b") as never)!], false);
    same.take({ data: [msg(1, "a"), msg(2, "b")], start: 1, end: 2, more: false } as never);
    expect(same.late).toBe(0);
  });
});
