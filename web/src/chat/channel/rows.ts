// The conversation's rows (docs/next/ui.md §3 "行表"), pure for tests: a channel's messages, each
// number once, put in as the log, the events and this device's copy bring them; the bubbles of
// what was sent from this page till each is a message; their order; and the runs of one sender
// under one head. The table that holds them is log.ts ChannelLog; the page only draws what it gives.
import type { Ask } from "../../api/types.gen";
import type { Author, Msg } from "./lib";

/** A message as compared: one read again the same changes nothing. */
const sig = (m: Msg) => JSON.stringify([m.n, m.text, m.files, m.time, m.author, m.kind, m.did, m.work, m.writing, m.event, m.src, m.quote, m.input, m.card, m.ask, m.answer, m.asked]);

/** have with more put in: each number once, in order. A reading replaces what was held, but what it
 *  does not say (the kind, where it is from in its agent's record, a person's input id) stays as
 *  known. keep: the numbers whose copy held stays as it is (an event told of it after the reading
 *  was asked for: the reading may be older, and what changes it later comes by an event too). The
 *  same array when nothing changed. */
export function upsert(have: Msg[], more: Msg[], keep?: (n: number) => boolean): Msg[] {
  if (!more.length) return have;
  const by = new Map(have.map((m) => [m.n, m]));
  let changed = false;
  for (const m of more) {
    const was = by.get(m.n);
    if (was && keep?.(m.n)) continue;
    const next = was ? { ...m, ...(!m.kind && was.kind ? { kind: was.kind } : {}), ...(!m.src && was.src ? { src: was.src } : {}), ...(!m.input && was.input ? { input: was.input } : {}) } : m;
    if (was && sig(was) === sig(next)) continue;
    by.set(m.n, next);
    changed = true;
  }
  return changed ? [...by.values()].sort((a, b) => a.n - b.n) : have;
}

/** A bubble of what was sent from this page: its key, where it is shown (place: between the
 *  numbers it was sent after and before), its input id, and its number once the server said it. */
export type Bubble = { key: string; place?: number; input?: string; seq?: number };

/** Where a message sent from this page is shown, and the key it is drawn under: its bubble's
 *  (kumo, 2026-11-24: 已画的不换位). */
export type Placed = { at: number; key: string; made?: number };

/** The key a bubble is drawn under, and the message it becomes. */
export const bubbleKey = (b: { key: string }) => `s:${b.key}`;

/** The message that a bubble is, by its input id or its number: never by its words. */
export const bubbleMsg = (b: Bubble, msgs: readonly Msg[]): Msg | undefined => msgs.findLast((m) => (!!b.input && m.input === b.input) || (!!b.seq && m.n === b.seq));

/** A row of the conversation: a message, or a bubble not yet one. t: the time it is put in a run
 *  and under a time by (grouped): a message's, or, for one sent from this page, when it was sent
 *  here, as its bubble was (kumo, 2026-11-26: drawn, it is not moved for the server's clock). */
export type Line<S> = { key: string; m: Msg; sent?: undefined; t?: number } | { key: string; sent: S; m?: undefined; t?: number };

/** The rows in the order shown: the messages by their numbers, one sent from here at its bubble's
 *  place (placed); the bubbles at theirs (one with none at the end). Messages before bubbles at
 *  the same place; each kind in the order given. A page opened again has the server's order. */
export function ordered<S extends Bubble & { made?: number }>(msgs: readonly Msg[], placed: ReadonlyMap<number, Placed>, sent: readonly S[]): Line<S>[] {
  const all: { k: number; line: Line<S> }[] = [
    ...msgs.map((m) => {
      const p = placed.get(m.n), t = p?.made ?? m.time;
      return { k: p?.at ?? m.n, line: { key: p?.key ?? `m${m.n}`, m, ...(t ? { t } : {}) } };
    }),
    ...sent.map((s) => ({ k: s.place ?? Infinity, line: { key: bubbleKey(s), sent: s, ...(s.made ? { t: s.made } : {}) } })),
  ];
  if (placed.size || sent.length) all.sort((a, b) => a.k - b.k);
  return all.map((x) => x.line);
}

export type Person = Extract<Author, { kind: "person" }>;

/** Who a message is from, as heads are shared: an agent by its session, a script by its name,
 *  Iridium's lines all one, a person by its id; the one looking (mine) is "me", as its bubbles are. */
export function senderOf(m: Msg, mine: (a: Person) => boolean): string {
  const a = m.author;
  switch (a.kind) {
    case "agent":
      return `a:${a.session}`;
    case "script":
      return `s:${a.name}`;
    case "iridium":
      return "i";
    case "person":
      return mine(a) ? "me" : `p:${a.id}`;
  }
}

/** How far apart two messages of one sender may be and still be one run under one head (kumo,
 *  2026-11-26: a message of yesterday read as one of just now). */
export const RUN_GAP = 5 * 60_000;
/** How far apart two messages are for the time to be said between them, as a line across; and a
 *  day changing between them. */
export const MARK_GAP = 60 * 60_000;
const dayOf = (t: number) => new Date(t).toDateString();

/** Rows one after another from one sender, under one head (docs/next/ui.md §1.5: nobody else's
 *  between), each within RUN_GAP of the one before. author: the first's; none for a bubble of the
 *  one looking. key: the first row's. mark: the time said over it (a while, or a day, since the
 *  row before; the channel's first). bare: the same sender's run going on with only Iridium's
 *  lines between (an agent joined): no head again. What a row gets is its own and the row
 *  before's alone: one coming later changes none drawn (kumo, 2026-11-26). start: the first row
 *  given is the channel's first. */
export type Group<S> = { key: string; author?: Author; lines: Line<S>[]; mark?: number; bare?: boolean };

export function grouped<S>(lines: readonly Line<S>[], mine: (a: Person) => boolean, start = false): Group<S>[] {
  const out: Group<S>[] = [];
  let last = "", lastT: number | undefined, run = "", runT: number | undefined;
  for (const l of lines) {
    const who = l.m ? senderOf(l.m, mine) : "me";
    const t = l.t;
    const g = out.at(-1);
    const known = t !== undefined && lastT !== undefined;
    const mark = t !== undefined && (known ? t - lastT! >= MARK_GAP || dayOf(t) !== dayOf(lastT!) : start && !out.length) ? t : undefined;
    const apart = mark !== undefined || (known && t - lastT! > RUN_GAP);
    if (g && who === last && !apart) g.lines.push(l);
    else {
      const bare = who !== "i" && last === "i" && who === run && mark === undefined && t !== undefined && runT !== undefined && t - runT <= RUN_GAP;
      out.push({ key: l.key, ...(l.m ? { author: l.m.author } : {}), lines: [l], ...(mark !== undefined ? { mark } : {}), ...(bare ? { bare } : {}) });
    }
    last = who;
    if (t !== undefined) lastT = t;
    if (who !== "i") (run = who), (runT = t ?? runT);
  }
  return out;
}

/** The messages whose count of what was done before them (did, as older messages say it) is the
 *  work the same agent's message before them carries (work): shown there, not twice. */
export function doneBefore(msgs: readonly Msg[]): Set<number> {
  const out = new Set<number>();
  const last = new Map<string, Msg>();
  for (const m of msgs) {
    if (m.author.kind !== "agent") continue;
    if (last.get(m.author.session)?.work) out.add(m.n);
    last.set(m.author.session, m);
  }
  return out;
}

/** Where each reader's mark goes (docs/channels.md "已读"): under the group that holds the last
 *  message it has read, by the group's key; one that has read nothing shown, or all of what is
 *  before the first group, has none. skip: the member that is looking. */
export function readMarks<S, R extends { member: string; seq: number }>(groups: readonly Group<S>[], readers: R[], skip?: string): Map<string, R[]> {
  const out = new Map<string, R[]>();
  for (const r of readers) {
    if (r.member === skip || r.seq < 1) continue;
    let at: Group<S> | undefined;
    for (const g of groups) {
      const first = g.lines.find((l) => l.m)?.m;
      if (!first) continue;
      if (first.n > r.seq) break;
      at = g;
    }
    if (at) out.set(at.key, [...(out.get(at.key) ?? []), r]);
  }
  return out;
}

/** The asks open in the channel, as its messages say (docs/next/ui.md: an ask is a message that
 *  carries the ask while it is open, and none once the update that ends it came): by the session
 *  of the agent that asks, oldest first. state: what each agent's own state says is open, taken
 *  only for an ask whose message is not among those held (said before the page's first). */
export function openAsks(msgs: readonly Msg[], state: readonly { session: string; asks: readonly Ask[] }[]): Map<string, Ask[]> {
  const out = new Map<string, Ask[]>();
  const cards = new Set<string>();
  for (const m of msgs) {
    if (m.card) cards.add(m.card);
    if (m.ask && m.author.kind === "agent") out.set(m.author.session, [...(out.get(m.author.session) ?? []), m.ask]);
  }
  for (const s of state)
    for (const a of s.asks) {
      const have = out.get(s.session) ?? [];
      if (!cards.has(`ask:${s.session}:${a.requestId}`) && !have.some((x) => x.requestId === a.requestId)) out.set(s.session, [...have, a]);
    }
  return out;
}
