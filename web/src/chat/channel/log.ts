// A channel's log as the page holds it (docs/channels.md): the newest page, those before it as
// you scroll up, and what is said since: an event's message put in as it comes, what the events
// missed fetched after the last number held. Each number once.
import { api, type ChannelMessage, type ChannelPage } from "../../api/client";
import type { Sent } from "../../chats";
import { merge, msgOf, type Msg } from "./lib";

type Get = (id: string, q: { before?: number; after?: number }) => Promise<ChannelPage>;

const read = (ms: ChannelMessage[]) => ms.map(msgOf).filter((m): m is Msg => !!m);

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
  private coming: Promise<void> | undefined;
  private again = false;
  /** Where to read on from, when an event's message came further on than the next number: the
   *  last number held before it. */
  private gap: number | null = null;

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

  private put(more: Msg[]) {
    for (const m of more) if (m.event?.type === "recall") this.recalled.add(m.event.seq!);
    let next = merge(this.msgs, more);
    if (this.recalled.size) next = next.filter((m) => !this.recalled.has(m.n));
    if (next.length === this.msgs.length && next.every((m, i) => m === this.msgs[i])) return;
    this.msgs = next;
    this.version++;
  }

  /** Message n taken back: out, for good. */
  drop(n: number) {
    this.recalled.add(n);
    this.put([]);
  }

  /** A page read elsewhere (the first screen's, chats.ts): the newest, with those before it that
   *  came along. Held already: what it has that is new goes in. */
  take(p: ChannelPage) {
    const first = this.msgs[0]?.n;
    if (!this.loaded || (first !== undefined && p.start < first)) this.more = p.more;
    if (!this.loaded) this.version++;
    this.loaded = true;
    this.put(read(p.data));
    this.came = Date.now();
  }

  /** The newest page at first; then what was said after the last held, page after page. One at a
   *  time: asked while one is out, it goes once more after it. */
  newer(): Promise<void> {
    if (this.coming) {
      this.again = true;
      return this.coming;
    }
    const run = async () => {
      do {
        this.again = false;
        if (!this.loaded) {
          const p = await this.get(this.id, {});
          this.loaded = true;
          this.more = p.more;
          this.version++;
          this.put(read(p.data));
          continue;
        }
        let from = this.gap ?? this.end;
        this.gap = null;
        for (;;) {
          const p = await this.get(this.id, { after: from });
          const added = read(p.data);
          this.put(added);
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

  /** A message an event brought: put in; one further on than the next number means some were
   *  missed, and they are fetched. A message taken back (message-deleted): out. */
  heard(m: ChannelMessage, deleted = false) {
    if (deleted) return this.drop(Number(m.id));
    const it = msgOf(m);
    if (!it || !this.loaded) return void this.newer().catch(() => {});
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
    const p = await this.get(this.id, { before: first });
    if (this.msgs[0]?.n !== first) return true;
    const added = read(p.data).filter((m) => m.n < first);
    this.more = p.more && added.length > 0;
    this.version++;
    this.put(added);
    return added.length > 0;
  }
}

/** A channel as kept: its log, its scroll place as it was left, what was sent from here that the
 *  log has yet to bring. */
export type Kept = { log: ChannelLog; place: { top: number | null }; sent: Sent[] };

/** The channels seen lately, so going back to one draws it at once. */
const kept = new Map<string, Kept>();
const KEPT = 8;

/** Channels seen here with another agent: the log stays in once it was, so their words do not
 *  go as an agent leaves (and come back as it returns). */
export const several = new Set<string>();

/** The log of channel id if it is held and its newest page has come. */
export const heldLog = (id: string) => (kept.get(id)?.log.loaded ? kept.get(id)!.log : undefined);

export function channelLog(id: string): Kept {
  let it = kept.get(id);
  it ??= { log: new ChannelLog(id), place: { top: null }, sent: [] };
  kept.delete(id);
  kept.set(id, it);
  for (const k of kept.keys()) {
    if (kept.size <= KEPT) break;
    if (k !== id) kept.delete(k);
  }
  return it;
}
