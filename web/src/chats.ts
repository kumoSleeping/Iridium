// The conversations the page holds (docs/api.md "消息页", Rhodium architecture §7.7): each session's
// blocks as fetched, paged back as you scroll up, the drafts after them, and what you sent that the
// transcript has yet to bring. Those seen lately stay in memory, up to KEPT_BLOCKS blocks in all, so
// going back to one draws it at once and fetches only what came since. Also what is fetched ahead
// (plan "弱网"): a row the mouse rests on, and a session whose turn ends off screen.
import { api } from "./api/client";
import type { Block, DraftEvent, MessagePage, Session } from "./api/types.gen";

/** A message of yours sent from this page, till the transcript has it: input, the id the server gave
 *  it (Claude's uuid, which the user block carries); text, as typed; files, upload refs with their
 *  names; from, where the transcript ended as it was sent. failed: why it did not go. */
export type Sent = { key: string; text: string; files: { ref: string; name: string; type: string }[]; from: number; input?: string; failed?: string; queued?: boolean };

/** Where a block's transcript line begins: its at up to the dot. */
export const lineOf = (b: { at: string }) => parseInt(b.at);

/** Those sent not yet in the transcript: each finds its own among the user blocks written since it
 *  was sent, by its input id, else by its words. A failed one stays. */
export function unsent(sent: Sent[], blocks: Block[]): Sent[] {
  let past = 0;
  return sent.filter((s) => {
    s.from = Math.max(s.from, past);
    if (s.failed !== undefined) return true;
    const b = blocks.find((b) => b.kind === "user" && ((s.input && b.id === s.input) || (lineOf(b) >= s.from && b.text !== undefined && s.text && b.text.trim().startsWith(s.text.trim().slice(0, 200)))));
    if (!b) return true;
    past = lineOf(b) + 1;
    return false;
  });
}

type Get = (id: string, q: { before?: number; after?: number }) => Promise<MessagePage>;

/** A draft: what the agent writes ahead of its transcript, message n of process proc. */
export type Draft = { proc: number; n: number; text: string; whole: boolean };

export class Messages {
  blocks: Block[] = [];
  drafts: Draft[] = [];
  sent: Sent[] = [];
  start = 0;
  end = 0;
  more = false;
  /** Whether the newest page has come. */
  loaded = false;
  proc = -1;
  /** Bumped at each change, for a view to draw again. */
  version = 0;
  /** The last draft the transcript has: a piece of it that comes late (the stream batches drafts,
   *  the transcript may be read first) is not a new draft. */
  private gone = { proc: -1, n: -1 };
  private forget(d: Draft) {
    if (d.proc !== this.gone.proc) this.gone = { proc: d.proc, n: -1 };
    this.gone.n = Math.max(this.gone.n, d.n);
  }

  constructor(
    readonly id: string,
    private readonly get: Get = (id, q) => api.messages(id, q),
  ) {}

  /** Brings the blocks written since; the newest page at first. A turn may write many pages
   *  between two states: read on until a page brings nothing new. */
  async newer(): Promise<void> {
    if (!this.loaded) {
      const p = await this.get(this.id, {});
      if (this.loaded) return this.newer(); // another came first
      Object.assign(this, { blocks: p.blocks, start: p.start, end: p.end, more: p.more, loaded: true });
      this.took(p.blocks);
      return;
    }
    for (;;) {
      const from = this.end;
      const p = await this.get(this.id, { after: from });
      if (p.end <= this.end) return;
      // Two fetches may ask from the same place: each block goes in once.
      const added = p.blocks.filter((b) => lineOf(b) >= this.end);
      this.blocks = [...this.blocks, ...added];
      this.end = p.end;
      this.took(added);
    }
  }

  /** New blocks: each thing the agent said takes the place of the first draft it is. */
  private took(added: Block[]) {
    for (const b of added) {
      if (b.kind !== "assistant") continue;
      const d = this.drafts[0];
      if (!d) continue;
      const head = (s: string) => s.trim().slice(0, 32);
      if (d.whole || (b.text ?? "").trim().startsWith(head(d.text)) || d.text.trim().startsWith(head(b.text ?? ""))) this.forget(this.drafts.shift()!);
    }
    this.sent = unsent(this.sent, this.blocks);
    this.version++;
  }

  /** The blocks the session's state carries (SessionState.tail, with the files they name), when
   *  they reach back to what is shown: those after it go on, those already shown are replaced (a
   *  tool's result came). Says whether it could; else the page asks for them. */
  fromTail(t: { start: number; end: number; blocks: Block[] | null } | undefined): boolean {
    if (!t?.blocks || !this.loaded || this.end < t.start || this.end > t.end) return false;
    if (t.end <= this.end) return true;
    const byAt = new Map(t.blocks.map((b) => [b.at, b]));
    this.blocks = this.blocks.map((b) => byAt.get(b.at) ?? b);
    const added = t.blocks.filter((b) => lineOf(b) >= this.end);
    this.blocks = [...this.blocks, ...added];
    this.end = t.end;
    this.took(added);
    return true;
  }

  /** The page before the blocks shown. Says whether it brought any. */
  async older(): Promise<boolean> {
    if (!this.more) return false;
    const start = this.start;
    const p = await this.get(this.id, { before: start });
    if (this.start !== start) return true;
    this.blocks = [...p.blocks.filter((b) => lineOf(b) < start), ...this.blocks];
    this.start = Math.min(start, p.start);
    this.more = p.more;
    this.version++;
    return p.blocks.length > 0;
  }

  /** A draft event (docs/api.md): the same proc and n join; another n seals the one before; another
   *  proc drops them all. */
  wrote(d: DraftEvent) {
    if (d.proc === this.gone.proc && d.n <= this.gone.n) return;
    if (d.proc !== this.proc) {
      this.drafts = [];
      this.proc = d.proc;
    }
    const last = this.drafts.at(-1);
    if (last && last.n === d.n) last.text += d.text;
    else {
      if (last) last.whole = true;
      this.drafts.push({ proc: d.proc, n: d.n, text: d.text, whole: false });
    }
    this.version++;
  }

  /** The session's process is another now: the drafts of the one before go. */
  procIs(proc: number) {
    if (proc === this.proc) return;
    if (this.drafts.length) this.version++;
    this.drafts = [];
    this.proc = proc;
  }

  /** The stream opened again, or the turn ended: drafts go, the transcript has them. */
  dropDrafts() {
    if (!this.drafts.length) return;
    this.drafts.forEach((d) => this.forget(d));
    this.drafts = [];
    this.version++;
  }
}

/** How many blocks the chats seen lately hold in all, at most (Rhodium #415). */
export const KEPT_BLOCKS = 2000;

/** A session's chat as kept: its conversation, its scroll place as it was left. */
export type Chat = { log: Messages; place: { top: number | null }; coming?: Promise<void> };
const newChat = (id: string, get?: Get): Chat => ({ log: new Messages(id, get), place: { top: null } });

const kept = new Map<string, Chat>();
/** The one chat fetched ahead last, till its visit takes it: apart from those seen lately. */
let early: { id: string; it: Chat; stop: AbortController } | undefined;

/** The chat of session id, as last seen if kept; kept now, the least lately seen going once the
 *  blocks are over KEPT_BLOCKS, never this one. */
export function chatOf(id: string): Chat {
  let it = kept.get(id);
  if (!it && early?.id === id) {
    it = early.it;
    early = undefined;
  }
  it ??= newChat(id);
  kept.delete(id);
  kept.set(id, it);
  let all = 0;
  for (const c of kept.values()) all += c.log.blocks.length;
  for (const [k, c] of kept) {
    if (all <= KEPT_BLOCKS || c === it) break;
    all -= c.log.blocks.length;
    kept.delete(k);
  }
  return it;
}

export const peekChat = (id: string) => kept.get(id) ?? (early?.id === id ? early.it : undefined);
export const keptChats = () => [...kept.values()];

/** Whether the browser asks to save data: then nothing is fetched ahead. */
export const mayPrefetch = (nav: Navigator = navigator) => !(nav as Navigator & { connection?: { saveData?: boolean } }).connection?.saveData;

/** Fetches session id's newest page ahead of its visit, as the mouse rests on its row (plan "弱网"),
 *  unless kept already or coming. One at a time: the one before, still coming, stops. pressed: the
 *  row is pressed, or the turn ended; it is not stopped till it came. */
let pressed: Promise<void> | undefined;
export function ahead(id: string, press = false): void {
  if (!mayPrefetch() || pressed) return;
  if (!kept.has(id) && early?.id !== id) {
    behind();
    const stop = new AbortController();
    let signal: AbortSignal | undefined = stop.signal;
    const it = newChat(id, (sid, q) => api.messages(sid, q, { signal, priority: "low" } as RequestInit));
    early = { id, it, stop };
    it.coming = it.log
      .newer()
      .catch(() => {})
      .finally(() => ((it.coming = undefined), (signal = undefined)));
  }
  if (press && early?.id === id && early.it.coming) pressed = early.it.coming.then(() => void (pressed = undefined));
}

/** The mouse left the row: what is still coming stops and goes; what came stays. */
export function behind(): void {
  if (!early?.it.coming || pressed) return;
  early.stop.abort();
  early = undefined;
}

/** A turn ended (running to idle or waiting) for a session whose chat is not open (plan "弱网":
 *  "回合结束就把它的对话取好"): its blocks since are fetched now; one not kept as ahead brings it. */
export function turnEnded(id: string, open: string | null): void {
  if (id === open || !mayPrefetch()) return;
  const it = peekChat(id);
  if (!it) return void (early?.it.coming || ahead(id, true));
  if (it.coming || !it.log.loaded) return;
  it.coming = it.log
    .newer()
    .catch(() => {})
    .finally(() => (it.coming = undefined));
}

/** Watches the list for turns that end: tells turnEnded. */
export function watchTurns(list: Session[] | undefined, was: Map<string, string>, open: string | null) {
  for (const s of list ?? []) {
    const before = was.get(s.id);
    was.set(s.id, s.status);
    if ((before === "running" || before === "compacting") && (s.status === "idle" || s.status === "waiting")) turnEnded(s.id, open);
  }
}

/** Whether a message you sent failed and waits to be tried again, in any chat kept: one loading the
 *  page again would lose (src/upgrade.ts). */
export const anyUnsent = () => [...kept.values()].some((c) => c.log.sent.some((s) => s.failed !== undefined));
