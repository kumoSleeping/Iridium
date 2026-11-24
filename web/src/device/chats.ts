// The chats this device keeps (docs/plan.md "本机缓存"): of each session visited, the newest page
// of its record as its first screen draws it (the older pages are not kept: scrolling up asks for
// them), its state and members, and with other agents in its channel the channel's newest
// messages, their states and the work under their words. Opening it draws this at once; the
// server's first screen takes its place as it comes (chats.ts take, log.ts take). Written as a
// turn ends, as the page goes to the background, and when idle; never at each piece. Let go as
// the session is archived or deleted, unused for the days kept, past the size (store.ts sweep),
// and all of it as the login ends. A copy of another format is dropped, never moved on.
import { queryClient } from "../api/client";
import type { Block, ChannelMembers, ChannelReads, Session, SessionState } from "../api/types.gen";
import { heldChats, lineOf, peekChat, type Chat } from "../chats";
import { workKey, type Msg } from "../chat/channel/lib";
import { channelLog, heldLog, keptChannelIds } from "../chat/channel/log";
import { getCarried, onCarried, setCarried } from "../store";
import { listFromDevice } from "./first";
import { asked, db, done, drop, entries, getConf } from "./store";

/** The copy's format: one that is not this is dropped whole. */
export const FORMAT = 2;
/** As much of the record as its newest page (internal/transcript PageMessages, PageBlocks). */
const PAGE_MESSAGES = 20;
const PAGE_BLOCKS = 200;
/** As many of the channel's newest messages. */
const CHANNEL_MESSAGES = 50;

export type Copy = {
  key: string;
  v: number;
  at: number;
  session?: SessionState;
  page?: { blocks: Block[]; start: number; end: number; more: boolean };
  members?: ChannelMembers;
  reads?: ChannelReads;
  chan?: { msgs: Msg[]; more: boolean };
  sessions?: Record<string, SessionState>;
  work?: [unknown[], unknown][];
};

const keyOf = (id: string) => `chat:${id}`;

/** The newest page of blocks, as the server makes one: back to PAGE_MESSAGES things said or
 *  PAGE_BLOCKS blocks, begun at a line's first block. */
export function newestPage(blocks: Block[], start: number, more: boolean): NonNullable<Copy["page"]> {
  let i = blocks.length;
  let said = 0;
  while (i > 0 && blocks.length - i < PAGE_BLOCKS && said < PAGE_MESSAGES) {
    const b = blocks[--i]!;
    if (b.kind === "user" || (b.kind === "assistant" && b.text?.trim())) said++;
  }
  while (i > 0 && i < blocks.length && lineOf(blocks[i]!) === lineOf(blocks[i - 1]!)) i++;
  const kept = blocks.slice(i);
  const end = kept.length ? lineOf(kept.at(-1)!) + 1 : start;
  return { blocks: kept, start: i > 0 && kept.length ? lineOf(kept[0]!) : start, end, more: i > 0 || more };
}

/** What this device keeps of channel id now; null if nothing worth it (its page not come, or the
 *  copy put in not yet checked against the server's). */
export function copyOf(id: string, it?: Chat, now = Date.now()): Copy | null {
  const ch = heldLog(id);
  const log = it?.log;
  const hasCh = !!ch && ch.loaded && !ch.restored;
  const hasRec = !!log && log.loaded && !log.restored && log.blocks.length > 0;
  if (!hasCh && !hasRec) return null;
  const c: Copy = { key: keyOf(id), v: FORMAT, at: now };
  if (hasRec && !hasCh) {
    const page = newestPage(log!.blocks, log!.start, log!.more);
    page.end = log!.end;
    c.page = page;
  }
  const st = getCarried<SessionState>(`session:${id}`);
  if (st) c.session = st;
  const members = queryClient.getQueryData<ChannelMembers>(["members", id]);
  if (members) c.members = members;
  if (hasCh) {
    const msgs = ch!.msgs.slice(-CHANNEL_MESSAGES);
    c.chan = { msgs, more: ch!.more || ch!.msgs.length > msgs.length };
    const reads = getCarried<ChannelReads>(`reads:${id}`);
    if (reads) c.reads = reads;
    const sessions: Record<string, SessionState> = {};
    for (const m of members?.members ?? []) {
      if (!m.session || m.session === id) continue;
      const s = getCarried<SessionState>(`session:${m.session}`);
      if (s) sessions[m.session] = s;
    }
    c.sessions = sessions;
    c.work = msgs.flatMap((m) => {
      if (!m.src) return [];
      const k = workKey(m.src);
      const w = queryClient.getQueryData(k);
      return w === undefined ? [] : [[k, w] as [unknown[], unknown]];
    });
  }
  return c;
}

/** Puts this device's copy of channel id's chat into memory, if there is one and chats are kept:
 *  says whether it did. */
export async function restore(id: string, it: Chat): Promise<boolean> {
  const conf = await getConf();
  if (!conf.chats) return false;
  const d = await db();
  if (!d) return false;
  const c = await asked(d.transaction("chats").objectStore("chats").get(keyOf(id)) as IDBRequest<Copy | undefined>).catch(() => undefined);
  if (!c) return false;
  // Older than what the server's list says of it: not drawn, to be put right a moment later
  // (kumo, 2026-11-23: a message missing, then coming in with the page jumping). The server's
  // is waited for instead. A list still this device's own tells nothing.
  const row = listFromDevice() ? undefined : getCarried<Session[]>("list")?.find((s) => s.id === id);
  if (row && row.updatedAt > c.at + 1000) return false;
  if (c.v !== FORMAT) {
    void drop([c.key]);
    return false;
  }
  const key = `session:${id}`;
  if (c.session && !getCarried(key)) setCarried(key, c.session);
  if (c.page && !it.log.loaded) {
    it.log.take(c.page);
    it.log.restored = true;
    it.log.came = 0;
  }
  if (c.members && !queryClient.getQueryData(["members", id])) queryClient.setQueryData(["members", id], c.members);
  if (c.chan) {
    channelLog(id).log.restore(c.chan.msgs, c.chan.more);
    if (c.reads && !getCarried(`reads:${id}`)) setCarried(`reads:${id}`, c.reads);
    for (const [x, st] of Object.entries(c.sessions ?? {})) if (!getCarried(`session:${x}`)) setCarried(`session:${x}`, st);
    for (const [k, w] of c.work ?? []) if (queryClient.getQueryData(k) === undefined) queryClient.setQueryData(k, w);
  }
  void used(c.key);
  return true;
}

/** Marks key used now (opened). */
async function used(key: string) {
  const d = await db();
  if (!d) return;
  const t = d.transaction("entries", "readwrite");
  const s = t.objectStore("entries");
  const q = s.get(key);
  q.onsuccess = () => q.result && s.put({ ...q.result, used: Date.now() });
  await done(t).catch(() => {});
}

/** The version of each chat as last written, by session: one unchanged is not written again. */
const written = new Map<string, string>();
const versionOf = (id: string, it?: Chat) => `${it?.log.version ?? 0}.${heldLog(id)?.version ?? 0}`;

/** Writes the copies of these chats (those kept in memory, by default) that changed. */
export async function save(ids?: string[]) {
  const conf = await getConf();
  if (!conf.chats) return;
  const d = await db();
  if (!d) return;
  const now = Date.now();
  const copies: Copy[] = [];
  const allIds = ids ?? [...new Set([...keptChannelIds(), ...heldChats().map((c) => c.log.id)])];
  for (const id of allIds) {
    const it = peekChat(id);
    const v = versionOf(id, it);
    if (written.get(id) === v) continue;
    const c = copyOf(id, it, now);
    if (!c) continue;
    copies.push(c);
    written.set(id, v);
  }
  if (!copies.length) return;
  const t = d.transaction(["chats", "entries"], "readwrite");
  for (const c of copies) {
    let size = 0;
    try {
      size = JSON.stringify(c).length * 2;
    } catch {
      continue;
    }
    t.objectStore("chats").put(c);
    t.objectStore("entries").put({ key: c.key, cat: "chat", size, used: now });
  }
  await done(t).catch(() => {
    for (const c of copies) written.delete(c.key.slice(5));
  });
}

/** The chats this device keeps whose sessions are archived or gone from the list: let go. */
export async function unload(list: Session[]) {
  if (!list.length) return;
  const live = new Map(list.map((s) => [s.id, s]));
  const gone = (await entries()).filter((e) => {
    if (e.cat !== "chat") return false;
    const s = live.get(e.key.slice(5));
    return !s || s.archived;
  });
  await drop(gone.map((e) => e.key));
  for (const e of gone) written.delete(e.key.slice(5));
}

const IDLE_EVERY = 30_000;

/** Session id's copy written when the page is idle (its first screen just came). */
const due = new Set<string>();
let dueAt: ReturnType<typeof setTimeout> | undefined;
export function saveSoon(id: string) {
  due.add(id);
  if (dueAt !== undefined) return;
  const go = () => {
    dueAt = undefined;
    const ids = [...due];
    due.clear();
    void save(ids).catch(() => {});
  };
  // A few together, once things are still.
  dueAt = setTimeout(() => (typeof requestIdleCallback === "function" ? requestIdleCallback(go, { timeout: 3_000 }) : go()), 800);
}
const BUSY = new Set(["running", "compacting"]);

/** Writes as turns end, as the page goes to the background, and when idle; lets go of the
 *  archived and the deleted as the list says. */
export function watch() {
  const was = new Map<string, string>();
  let unloading = 0;
  const soon = (ids: string[]) => {
    const go = () => void save(ids).catch(() => {});
    if (typeof requestIdleCallback === "function") requestIdleCallback(go, { timeout: 5_000 });
    else setTimeout(go, 500);
  };
  onCarried((k) => {
    if (k !== "list" || listFromDevice()) return;
    const list = getCarried<Session[]>("list") ?? [];
    const ended: string[] = [];
    for (const s of list) {
      const before = was.get(s.id);
      was.set(s.id, s.status);
      if (before && BUSY.has(before) && !BUSY.has(s.status) && (heldLog(s.id) || peekChat(s.id))) ended.push(s.id);
    }
    if (ended.length) soon(ended);
    if (Date.now() - unloading > 10_000) {
      unloading = Date.now();
      void unload(list).catch(() => {});
    }
  });
  document.addEventListener("visibilitychange", () => {
    if (document.hidden) void save().catch(() => {});
  });
  // The app quit (a web view gone with no visibilitychange): what is held written as it goes.
  addEventListener("pagehide", () => void save().catch(() => {}));
  setInterval(() => {
    const list = getCarried<Session[]>("list") ?? [];
    const busy = new Set(list.filter((s) => BUSY.has(s.status)).map((s) => s.id));
    soon([...new Set([...keptChannelIds(), ...heldChats().map((c) => c.log.id)])].filter((id) => !busy.has(id)));
  }, IDLE_EVERY);
}
