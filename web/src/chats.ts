// The conversations the page holds (docs/api.md "消息页", Rhodium architecture §7.7): each session's
// blocks as fetched, paged back as you scroll up, the drafts after them, and what you sent that the
// transcript has yet to bring. Those seen lately stay in memory, up to KEPT_BLOCKS blocks in all, so
// going back to one draws it at once and fetches only what came since. Also what is fetched ahead
// (plan "弱网"): a row the mouse rests on, a session whose turn ends off screen, and those the page
// warms of its own (src/warm.ts picks them): each one's newest page, held apart till it is visited.
import { api } from "./api/client";
import type { Block, DraftEvent, MessagePage, Session, SessionState } from "./api/types.gen";
import { getCarried, setCarried } from "./store";

/** A message of yours sent from this page, till the transcript has it: input, the id the server gave
 *  it (Claude's uuid, which the user block carries); text, as typed; files, upload refs with their
 *  names; from, where the transcript ended as it was sent. failed: why it did not go. */
export type Sent = { key: string; text: string; files: { ref: string; name: string; type: string; size?: number; url?: string }[]; from: number; input?: string; failed?: string; queued?: boolean; steer?: boolean };

/** Where a block's transcript line begins: its at up to the dot. */
export const lineOf = (b: { at: string }) => parseInt(b.at);

/** A /compact you sent: no bubble, the chat's compaction line says it (chat/said.tsx Compaction). */
export const compactSent = (s: Sent) => /^\/compact(\s|$)/.test(s.text.trim());

/** Those sent not yet in the transcript: each finds its own among the user blocks written since it
 *  was sent, by its input id, else by its words; a /compact, the compaction written since. A
 *  failed one stays, unless its block came after all (only the answer was lost): by its id alone. */
export function unsent(sent: Sent[], blocks: Block[]): Sent[] {
  let past = 0;
  return sent.filter((s) => {
    s.from = Math.max(s.from, past);
    if (s.failed !== undefined) return !(s.input && blocks.some((b) => b.kind === "user" && b.id === s.input));
    if (compactSent(s)) return !blocks.some((b) => b.kind === "system" && b.event === "compact" && lineOf(b) >= s.from);
    const b = blocks.find((b) => b.kind === "user" && ((s.input && b.id === s.input) || (lineOf(b) >= s.from && b.text !== undefined && s.text && b.text.trim().startsWith(s.text.trim().slice(0, 200)))));
    if (!b) return true;
    past = lineOf(b) + 1;
    return false;
  });
}

type Get = (id: string, q: { before?: number; after?: number }) => Promise<MessagePage>;

/** A draft: what the agent writes ahead of its transcript, message n of process proc. whole: no
 *  more text comes to it. */
export type Draft = { proc: number; n: number; text: string; whole: boolean; tool?: { id: string; name: string } };

/** How many letters a draft and a block must share at the head to be taken as one. */
const HEAD = 32;

/** Whether draft d and a block are the same words, never empty: the same; or one begins with the
 *  other's first HEAD letters, the shorter having that many (a few, as "Let me", begin too many
 *  blocks); or, d sealed, the block ends with it (the page joined it partway). */
const same = (d: Draft, block: string) => {
  if (d.tool) return false; // a call's input, not words
  const a = d.text.trim(),
    b = block.trim();
  if (!a || !b) return false;
  if (a === b) return true;
  if (a.length >= HEAD && b.startsWith(a.slice(0, HEAD))) return true;
  if (b.length >= HEAD && a.startsWith(b.slice(0, HEAD))) return true;
  return d.whole && a.length >= HEAD && b.endsWith(a);
};

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
  /** Whether the page before the newest is asked for ahead as it comes: not for a chat warmed
   *  that may never be opened; once it is, scrolling up asks (older). */
  back = true;
  /** The page before the one shown, asked for ahead, so scrolling up does not wait on it. */
  private ahead: { before: number; page: Promise<MessagePage> } | null = null;
  private prefetch() {
    if (!this.more || this.ahead?.before === this.start) return;
    const page = this.get(this.id, { before: this.start });
    page.catch(() => {});
    this.ahead = { before: this.start, page };
  }
  /** What the agent said that the transcript brought, in this turn, with no draft for it yet (its
   *  pieces were still on the way, or too few to tell): the draft with the same words goes once it
   *  has them. */
  private loose: string[] = [];
  /** The tool calls the transcript brought in this turn: a call's draft that comes later goes. */
  private looseCalls = new Set<string>();
  private forget(d: { proc: number; n: number }) {
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
      this.took(p.blocks, false);
      if (this.back) this.prefetch();
      return;
    }
    for (;;) {
      const from = this.end;
      const p = await this.get(this.id, { after: from });
      if (p.end <= this.end) return this.recheck();
      // Two fetches may ask from the same place: each block goes in once.
      const added = p.blocks.filter((b) => lineOf(b) >= this.end);
      this.blocks = [...this.blocks, ...added];
      this.end = p.end;
      this.took(added, true);
    }
  }

  /** New blocks: each thing the agent said takes the place of the draft it is, by its words, and of
   *  the drafts before it, which the transcript went past (their blocks were missed, or never come).
   *  One no draft is: new (live), it takes the first draft if that is sealed, by place (one joined
   *  partway has words that tell nothing), else waits in loose for its own. The first page (not
   *  live): only the turn now, after the last thing you said, and none is taken by place (what it
   *  brought may be from before the drafts seen). */
  private took(added: Block[], live: boolean) {
    const from = live ? 0 : added.findLastIndex((b) => b.kind === "user") + 1;
    for (const b of added.slice(from)) {
      // A call: its draft, by its id, and the drafts before it, which the transcript went past.
      if (b.kind === "tool" && b.id) {
        const i = this.drafts.findIndex((d) => d.tool?.id === b.id);
        if (i >= 0) this.drafts.splice(0, i + 1).forEach((d) => this.forget(d));
        else if (live) this.looseCalls.add(b.id);
        continue;
      }
      if (b.kind !== "assistant") continue;
      const text = (b.text ?? "").trim();
      if (!text) continue;
      const i = this.drafts.findIndex((d) => same(d, text));
      if (i >= 0) this.drafts.splice(0, i + 1).forEach((d) => this.forget(d));
      else if (live && this.drafts[0]?.whole && !this.drafts[0].tool) this.forget(this.drafts.shift()!);
      else this.loose.push(text);
    }
    this.sent = unsent(this.sent, this.blocks);
    this.version++;
  }

  /** What was sent, checked again against the blocks it has: an answer (the input id) that came
   *  after its block, a turn that ended with nothing new to fetch. */
  recheck() {
    const n = this.sent.length;
    this.sent = unsent(this.sent, this.blocks);
    if (this.sent.length !== n) this.version++;
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
    this.took(added, true);
    return true;
  }

  /** The page before the blocks shown, asked for ahead if it was; then the one before that is asked
   *  for. Says whether it brought any. */
  async older(): Promise<boolean> {
    if (!this.more) return false;
    const start = this.start;
    this.prefetch();
    const ahead = this.ahead!;
    let p: MessagePage;
    try {
      p = await ahead.page;
    } finally {
      if (this.ahead === ahead) this.ahead = null;
    }
    if (this.start !== start) return true;
    this.blocks = [...p.blocks.filter((b) => lineOf(b) < start), ...this.blocks];
    this.start = Math.min(start, p.start);
    this.more = p.more;
    this.version++;
    this.prefetch();
    return p.blocks.length > 0;
  }

  /** A draft event (docs/api.md): the same proc and n join; another n seals the one before; done
   *  seals its own; another proc drops them all. */
  wrote(d: DraftEvent) {
    if (d.proc === this.gone.proc && d.n <= this.gone.n) return;
    if (d.proc !== this.proc) {
      this.drafts = [];
      this.proc = d.proc;
    }
    const last = this.drafts.at(-1);
    if (last && last.n === d.n) last.text += d.text;
    else {
      if (last) this.seal(last);
      // Sealed with none of its pieces seen (the stream opened since): late ones are not a draft.
      // A call the transcript has already: none either.
      if ((d.done && !d.text && !d.tool) || (d.tool && this.looseCalls.has(d.tool.id))) this.forget(d);
      else this.drafts.push({ proc: d.proc, n: d.n, text: d.text, whole: false, ...(d.tool && { tool: { id: d.tool.id, name: d.tool.name } }) });
    }
    if (last && last.n === d.n && d.tool && !last.tool) last.tool = { id: d.tool.id, name: d.tool.name };
    const it = this.drafts.at(-1);
    if (it?.n === d.n) {
      if (d.done) this.seal(it);
      else this.settle(it);
    }
    this.version++;
  }

  /** Draft d is whole: it goes now if it has no words (the transcript keeps none for it) or the
   *  transcript brought them already. */
  private seal(d: Draft) {
    d.whole = true;
    if (d.tool) return; // its block takes it, by its id
    if (d.text.trim()) this.settle(d);
    else this.drop(d);
  }

  /** Draft d goes if the transcript brought its words already (loose). */
  private settle(d: Draft) {
    const i = this.loose.findIndex((l) => same(d, l));
    if (i < 0) return;
    this.loose.splice(i, 1);
    this.drop(d);
  }

  private drop(d: Draft) {
    this.drafts = this.drafts.filter((x) => x !== d);
    this.forget(d);
  }

  /** The session's process is another now: the drafts of the one before go. */
  procIs(proc: number) {
    if (proc === this.proc) return;
    if (this.drafts.length) this.version++;
    this.drafts = [];
    this.loose = [];
    this.looseCalls.clear();
    this.proc = proc;
  }

  /** Where the drafts are now: given to dropDrafts later, those that came since stay. */
  mark(): { proc: number; n: number } {
    return { proc: this.proc, n: this.drafts.at(-1)?.n ?? (this.gone.proc === this.proc ? this.gone.n : -1) };
  }

  /** The stream opened again, or the turn ended: drafts go, the transcript has them. upTo: only
   *  those up to a mark (the turn that ended; the next one may have begun to write). */
  dropDrafts(upTo?: { proc: number; n: number }) {
    const old = upTo ? this.drafts.filter((d) => d.proc === upTo.proc && d.n <= upTo.n) : this.drafts;
    const rest = this.drafts.filter((d) => !old.includes(d));
    // What waits for a draft is the turn's that ended, unless the next one wrote some already.
    if (!rest.length) {
      this.loose = [];
      this.looseCalls.clear();
    }
    if (!old.length) return;
    old.forEach((d) => this.forget(d));
    this.drafts = rest;
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
  const w = !it ? warm.get(id) : undefined;
  if (w) {
    it = w.it;
    warm.delete(id);
    w.mine = true;
    it.log.back = true;
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

export const peekChat = (id: string) => kept.get(id) ?? (early?.id === id ? early.it : undefined) ?? warm.get(id)?.it;
export const keptChats = () => [...kept.values()];

/** Sessions this page made, till their chat first draws its keys (chat/chat.tsx Under). */
export const born = new Set<string>();

/** Whether the browser asks to save data: then nothing is fetched ahead. */
export const mayPrefetch = (nav: Navigator = navigator) => !(nav as Navigator & { connection?: { saveData?: boolean } }).connection?.saveData;

/** Session id's state, asked for ahead of the stream, which carries it only once its chat is open
 *  and the stream opened again for it: the chat is drawn whole at once, its composer, keys and
 *  conversation, not its top bar alone and the rest after. Never over what the stream brought
 *  meanwhile. Says whether the session is there. One ask at a time per session; low: warming, or
 *  only freshening one held from before. */
const asking = new Map<string, Promise<boolean>>();
export function stateAhead(id: string, low = false): Promise<boolean> {
  let p = asking.get(id);
  if (p) return p;
  const key = `session:${id}`;
  const was = getCarried<SessionState>(key);
  p = api
    .session(id, { priority: low || was ? "low" : "high" } as RequestInit)
    .then(
      (st) => (getCarried(key) === was && setCarried(key, st), true),
      () => !!getCarried(key),
    )
    .finally(() => asking.delete(id));
  asking.set(id, p);
  return p;
}

/** Fetches session id's newest page ahead of its visit, as the mouse rests on its row (plan "弱网"),
 *  unless kept already or coming. One at a time: the one before, still coming, stops. pressed: the
 *  row is pressed, or the turn ended; it is not stopped till it came. */
let pressed: Promise<void> | undefined;
export function ahead(id: string, press = false): void {
  if (!mayPrefetch()) return;
  if (!getCarried(`session:${id}`)) void stateAhead(id);
  if (pressed) return;
  if (!kept.has(id) && early?.id !== id && !warm.has(id)) {
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
  since(it);
}

/** Brings what chat it held has missed since, unless that is coming already: a turn ended off
 *  screen, or the page turns to it (motion.ts pageTurn), which draws it with them, not without and
 *  then jumping. */
export function since(it: Chat): void {
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

/** A chat the page warms of its own (src/warm.ts): seen, the session's updatedAt as its page was
 *  last asked for (a later one: more to fetch); mine, visited (kept now): its fetches are no longer
 *  low and stop with nothing. */
type Warm = { it: Chat; stop: AbortController; seen: number; mine: boolean };
const warm = new Map<string, Warm>();
/** The sessions to warm, first first, with their updatedAt. */
let wanted: [string, number][] = [];
let pumping = false;

/** The chats warmed now, first first (for tests). */
export const warmed = () => [...warm.keys()];

/** Warms these sessions' chats (id → updatedAt), first first, one fetch at a time, low: the newest
 *  page of one not yet held, what came since of one held whose session changed. Those warmed and
 *  not wanted go, stopped if still coming; those kept or fetched ahead are left as they are. */
export function warmTo(want: Map<string, number>): void {
  wanted = [...want];
  for (const [id, w] of warm) {
    if (want.has(id)) continue;
    w.stop.abort();
    warm.delete(id);
  }
  for (const [id] of wanted) {
    if (warm.has(id) || kept.has(id) || early?.id === id) continue;
    const w = { stop: new AbortController(), seen: -1, mine: false } as Warm;
    w.it = newChat(id, (sid, q) => api.messages(sid, q, w.mine ? undefined : ({ signal: w.stop.signal, priority: "low" } as RequestInit)));
    w.it.log.back = false;
    warm.set(id, w);
  }
  void pump();
}

async function pump() {
  if (pumping) return;
  pumping = true;
  try {
    for (;;) {
      if (!mayPrefetch() || (typeof document !== "undefined" && document.hidden)) return;
      const next = wanted.find(([id, at]) => {
        const w = warm.get(id);
        return w && !w.it.coming && at > w.seen;
      });
      if (!next) return;
      const [id, at] = next;
      const w = warm.get(id)!;
      w.seen = at;
      const it = w.it;
      it.coming = it.log
        .newer()
        .catch(() => {})
        .finally(() => (it.coming = undefined));
      await it.coming;
      // Its state too, so opening it draws the composer and its keys at once.
      if (!getCarried(`session:${id}`)) await stateAhead(id, true);
    }
  } finally {
    pumping = false;
  }
}
