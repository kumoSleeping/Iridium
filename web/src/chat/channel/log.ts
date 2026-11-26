// A channel's log as the page holds it (docs/channels.md), the one table the conversation is drawn
// from (docs/next/ui.md §3 "行表"): the newest page, those before it as you scroll up, and what is
// said since: an event's message put in as it comes (made, changed: the whole of it again; taken
// back), what the events missed fetched after the last number held; this device's copy before the
// server's; and what was sent from this page, a bubble till it is a message. Each number once.
// Order and heads are rows.ts's.
import { api, type ChannelMessage, type ChannelPage } from "../../api/client";
import type { Sent } from "../../chats";
import { msgOf, type Msg } from "./lib";
import { bubbleKey, bubbleMsg, grouped, ordered, upsert, type Group, type Person, type Placed } from "./rows";

type Get = (id: string, q: { before?: number; after?: number }) => Promise<ChannelPage>;

const read = (ms: ChannelMessage[]) => ms.map(msgOf).filter((m): m is Msg => !!m);

/** What an event did to a message. */
export type Told = "message-created" | "message-updated" | "message-deleted";

/** The bubbles sent after the same message, in the order sent (Sent.place). */
let placeN = 0;

export class ChannelLog {
  msgs: Msg[] = [];
  /** Whether there are earlier messages than those held. */
  more = false;
  /** Whether the newest page has come. */
  loaded = false;
  /** Bumped at each change, for a view to draw again. */
  version = 0;
  /** When it was last read to its end (ms): a page that opens right after asks for nothing. */
  came = 0;
  /** What was sent from this page that the log has yet to bring, in the order sent: a bubble
   *  each. One goes as its message comes (by its input id, or its number once the server said
   *  it); a failed one stays, to be sent again. */
  sent: Sent[] = [];
  /** Where the messages sent from this page are shown, and their keys: their bubbles'. */
  readonly placed = new Map<number, Placed>();
  /** The messages this page saw being written: drawn as they were written to the end. */
  readonly wrote = new Set<number>();
  /** The messages that came by an event since they were last drawn: each comes in once. */
  readonly born = new Set<number>();
  private coming: Promise<void> | undefined;
  private again = false;
  /** Where to read on from, when an event's message came further on than the next number: the
   *  last number held before it. */
  private gap: number | null = null;
  /** Events told so far, and the last that told of each number: a reading asked for before
   *  that does not undo it. */
  private told = 0;
  private touched = new Map<number, number>();

  constructor(
    readonly id: string,
    private readonly get: Get = (id, q) => api.channelMessages(id, q),
  ) {}

  /** The last number held; 0 with none. */
  get end() {
    return this.msgs.at(-1)?.n ?? 0;
  }

  /** The numbers taken back (docs/channels.md "撤回"): out of the log, and kept out of it; a line
   *  of Iridium's own that says so too, the page not having heard it go. */
  readonly recalled = new Set<number>();

  /** Puts more in; since: the events told when the reading they are from was asked for (none: an
   *  event's own). */
  private put(more: Msg[], since = Infinity) {
    for (const m of more) if (m.event?.type === "recall") this.recalled.add(m.event.seq!);
    let next = upsert(this.msgs, more, (n) => (this.touched.get(n) ?? 0) > since);
    if (this.recalled.size) next = next.filter((m) => !this.recalled.has(m.n));
    if (next.length === this.msgs.length && next.every((m, i) => m === this.msgs[i])) return;
    this.msgs = next;
    this.settle();
    this.version++;
  }

  /** Message n taken back: out, for good. */
  drop(n: number) {
    this.recalled.add(n);
    this.put([]);
  }

  /** How many times the server's page brought messages past this device's copy: said while the
   *  page was away, come all at once as it is opened (the view does not go down to them: stick.tsx).
   *  The copy's last message grown since (an agent's, written on and its steps after it, the page
   *  loaded again in its turn) as well: the reader scrolls to it, the page does not (kumo,
   *  2026-11-26: loaded again, it glided down by itself). */
  late = 0;
  /** The messages put in from this device's copy (device/chats.ts), not yet the server's. */
  restored = false;
  /** This device's copy of the newest messages, before the server's. */
  restore(msgs: Msg[], more: boolean) {
    if (this.loaded) return;
    Object.assign(this, { msgs, more, loaded: true, restored: true });
    this.version++;
  }

  /** A page read elsewhere (the first screen's, chats.ts; a jump's): the newest, with those before
   *  it that came along. Held already: what it has that is new goes in; over this device's copy,
   *  the server's in its place (taken back, reacted to since). since: the events told when it was
   *  asked for (mark); not known, any message an event told of stays as told. */
  take(p: ChannelPage, since = 0) {
    if (this.restored) {
      this.restored = false;
      const was = this.end;
      const last = this.msgs.at(-1);
      const tail = (m?: Msg) => m && JSON.stringify([m.text, m.files.length, m.work, m.did, m.writing ?? false]);
      const lastWas = tail(last);
      this.more = p.more;
      const next = read(p.data);
      const theirs = new Map(next.map((m) => [m.n, JSON.stringify(m)]));
      const from = next[0]?.n ?? Infinity;
      // Mine from where the server's begin: those it has the same, nothing more.
      const same = this.msgs.every((m) => m.n < from || theirs.get(m.n) === JSON.stringify(m));
      if (!same) this.msgs = this.msgs.filter((m) => m.n < from || (this.touched.get(m.n) ?? 0) > since);
      this.put(next, since);
      if ((was && this.end > was) || (last && tail(this.msgs.find((m) => m.n === last.n)) !== lastWas)) this.late++;
      this.came = Date.now();
      return;
    }
    const first = this.msgs[0]?.n;
    if (!this.loaded || (first !== undefined && p.start < first)) this.more = p.more;
    if (!this.loaded) this.version++;
    this.loaded = true;
    this.put(read(p.data), since);
    this.came = Date.now();
  }

  /** How many events have been told: given to take for a reading asked for now. */
  mark() {
    return this.told;
  }

  /** The first message still being written or at work: what a stream down a while may have
   *  missed the end of. */
  private unsettled() {
    return this.msgs.find((m) => m.writing || m.work?.running)?.n;
  }

  /** The newest page at first; then what was said after the last held, page after page. One at a
   *  time: asked while one is out, it goes once more after it. again: the stream was down, the
   *  messages still being written or at work then are read again too. */
  newer(again = false): Promise<void> {
    if (again && this.loaded) {
      const from = this.unsettled();
      if (from !== undefined) this.gap = Math.min(this.gap ?? this.end, from - 1);
    }
    if (this.coming) {
      this.again = true;
      return this.coming;
    }
    const run = async () => {
      do {
        this.again = false;
        if (!this.loaded) {
          const since = this.told;
          const p = await this.get(this.id, {});
          this.loaded = true;
          this.more = p.more;
          this.version++;
          this.put(read(p.data), since);
          continue;
        }
        let from = this.gap ?? this.end;
        this.gap = null;
        for (;;) {
          const since = this.told;
          const p = await this.get(this.id, { after: from });
          const added = read(p.data);
          this.put(added, since);
          const last = added.at(-1)?.n ?? from;
          if (last <= from) break;
          from = last;
        }
      } while (this.again);
      this.came = Date.now();
    };
    this.coming = run().finally(() => (this.coming = undefined));
    return this.coming;
  }

  /** What an event told of a message: made (once: one held already is as new), changed (the whole
   *  of it again; one not held is put in), taken back. One further on than the next number means
   *  some were missed, and they are fetched. One that is no message of the log (an ask's card: its
   *  line of the log is read in its place) has the log read on. */
  heard(m: ChannelMessage, told: Told = "message-created") {
    if (told === "message-deleted") return this.drop(Number(m.id));
    const it = msgOf(m);
    if (!it) return void (told === "message-created" && this.newer().catch(() => {}));
    if (!this.loaded) return void this.newer().catch(() => {});
    if (told === "message-created" && this.msgs.some((x) => x.n === it.n)) return;
    // Before the first held: there as the page goes up.
    if (it.n < (this.msgs[0]?.n ?? 0)) return;
    this.touched.set(it.n, ++this.told);
    if (it.writing) this.wrote.add(it.n);
    // New under the eyes: drawn coming in, as a bubble sent from here is (page.tsx).
    if (told === "message-created") this.born.add(it.n);
    const end = this.end;
    this.put([it]);
    if (it.n <= end + 1) return;
    this.gap = Math.min(this.gap ?? end, end);
    void this.newer().catch(() => {});
  }

  /** The page before those held. Says whether it brought any. */
  async older(): Promise<boolean> {
    const first = this.msgs[0]?.n;
    if (!this.more || first === undefined) return false;
    const since = this.told;
    const p = await this.get(this.id, { before: first });
    if (this.msgs[0]?.n !== first) return true;
    const added = read(p.data).filter((m) => m.n < first);
    this.more = p.more && added.length > 0;
    this.version++;
    this.put(added, since);
    return added.length > 0;
  }

  /** What was sent from this page: its bubble under what the page has now. */
  send(s: Sent) {
    this.placeAt(s);
    this.sent.push(s);
    this.version++;
  }

  /** The server answered what was sent (its number, or why it failed): its message may be held
   *  already. A failed one is shown even if it was taken for come. */
  answered(s: Sent) {
    if (s.failed !== undefined && !this.sent.includes(s)) this.sent.push(s);
    this.settle();
    this.version++;
  }

  /** Sent again: under what has come since, not back where it failed. */
  resend(s: Sent) {
    this.placeAt(s);
    this.version++;
  }

  /** A failed one let go. */
  unsend(s: Sent) {
    this.sent = this.sent.filter((x) => x !== s);
    this.version++;
  }

  /** The number of the message a bubble became, once it has. */
  became(s: Sent): number | undefined {
    const k = bubbleKey(s);
    for (const [n, p] of this.placed) if (p.key === k) return n;
    return undefined;
  }

  private placeAt(s: Sent) {
    s.from = this.end;
    s.place = this.end + 0.5 + (placeN = (placeN + 1) % 4096) / 8192;
  }

  /** The bubbles whose messages have come: gone, each message where its bubble was, under its key. */
  private settle() {
    if (!this.sent.length) return;
    this.sent = this.sent.filter((s) => {
      const m = bubbleMsg(s, this.msgs);
      if (!m) return true;
      if (s.place !== undefined && !this.placed.has(m.n)) this.placed.set(m.n, { at: s.place, key: bubbleKey(s), ...(s.made ? { made: s.made } : {}) });
      return false;
    });
  }

  private drawn?: { v: number; mine: (a: Person) => boolean; groups: Group<Sent>[] };
  /** The conversation as drawn: its rows in order, each run of one sender under one head. mine:
   *  whether a person is the one looking. */
  groups(mine: (a: Person) => boolean): Group<Sent>[] {
    if (this.drawn?.v === this.version && this.drawn.mine === mine) return this.drawn.groups;
    // An ask is the card over the composer while open; answered, its message is no one's words (kumo, 2026-11-25): no row.
    const groups = grouped(ordered(this.msgs.filter((m) => m.kind !== "ask"), this.placed, this.sent), mine, !this.more);
    this.drawn = { v: this.version, mine, groups };
    return groups;
  }
}

/** A channel as kept: its log, its scroll place as it was left. */
export type Kept = { log: ChannelLog; place: { top: number | null } };

/** The channels seen lately, so going back to one draws it at once. */
const kept = new Map<string, Kept>();
// More than the chats warmed (warm.ts: ten a workspace, each of several agents with its log), or
// going on to warm them let go of the channel just written in (kumo, 2026-11-23: a message sent,
// another chat opened and this one again, the message was gone, then came back with a jump).
const KEPT = 32;

/** The log of channel id if it is held and its newest page has come. */
export const heldLog = (id: string) => (kept.get(id)?.log.loaded ? kept.get(id)!.log : undefined);
export const keptChannelIds = () => [...kept.keys()];
export const anyUnsentChannel = () => [...kept.values()].some((c) => c.log.sent.some((s) => s.failed !== undefined));

export function channelLog(id: string): Kept {
  let it = kept.get(id);
  it ??= { log: new ChannelLog(id), place: { top: null } };
  kept.delete(id);
  kept.set(id, it);
  for (const [k, c] of kept) {
    if (kept.size <= KEPT) break;
    // Not one with words of yours not yet in its log (going, failed): they are nowhere else.
    if (k !== id && !c.log.sent.length) kept.delete(k);
  }
  return it;
}
