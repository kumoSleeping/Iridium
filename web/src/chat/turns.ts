// The conversation of one agent's chat as turns (the user, 2026-11-13): what the agent did and said
// between two messages is one piece under its face; what it did for another channel that called
// it (docs/channels.md), from the call to the next message, one piece that folds.

/** A row of the chat: agent, the agent's own (its words, its steps, what it is writing); works,
 *  on a message: it is a call from another channel. */
export type Row<T, W> = { key: string; agent: boolean; works?: W; node: T };

/** one: a row by itself. agent: the agent's rows on end. works: a call and the rows after it. */
export type Turn<T, W> = { kind: "one"; key: string; node: T } | { kind: "agent"; key: string; rows: Row<T, W>[] } | { kind: "works"; key: string; works: W; rows: Row<T, W>[] };

/** A turn of the agent's is keyed by the row before it, not by its own first row: it stays the
 *  same turn as what is being written becomes a block. */
export function turnsOf<T, W>(rows: Row<T, W>[]): Turn<T, W>[] {
  const out: Turn<T, W>[] = [];
  let before = "start";
  for (const r of rows) {
    const last = out.at(-1);
    if (r.agent) {
      if (last && last.kind !== "one") last.rows.push(r);
      else out.push({ kind: "agent", key: `${before}:a`, rows: [r] });
      continue;
    }
    before = r.key;
    out.push(r.works !== undefined ? { kind: "works", key: r.key, works: r.works, rows: [] } : { kind: "one", key: r.key, node: r.node });
  }
  return out;
}

/** A turn's rows split for its 过程 fold (said.tsx Earlier): those before its last words, when
 *  they are more than one line already (words, or several rows); words, how many of them are
 *  words. null: none to fold. */
export function earlierOf<R>(rs: R[], isWords: (r: R) => boolean): { before: R[]; after: R[]; words: number } | null {
  const last = rs.findLastIndex(isWords);
  const before = last > 0 ? rs.slice(0, last) : [];
  const words = before.filter(isWords).length;
  if (!words && before.length < 2) return null;
  return { before, after: rs.slice(last), words };
}

/** The turn the agent is at, among turns that end with others' words (those said while it was on
 *  go under it, channel/merge.ts slotsOf): the last that is not such words, its row at work under
 *  it and over them. other: whether a turn is others' words. -1: none. */
export function tailOf<T, W>(turns: Turn<T, W>[], other: (key: string) => boolean): number {
  return turns.findLastIndex((x) => !(x.kind === "one" && other(x.key)));
}

/** Which turn is the one at work, as a page follows it: key, the turn tailOf says (as turnsOf
 *  keys the agent's); under: the turn at work is not that one but one to come, under the others'
 *  words at the end (it began as they were there, and what began it is not in the record yet). */
export type LiveAt = { key: string; under: boolean };
/** was: as last drawn (null: not at work, or not seen yet); idle: this page saw the chat at rest
 *  before; trailing: others' words follow the turn. A turn seen at work stays the one at work as
 *  words come under it; one that begins with words already under the turn before is a new one. */
export function liveAt(was: LiveAt | null, busy: boolean, key: string, trailing: boolean, idle: boolean): LiveAt | null {
  if (!busy) return null;
  if (was && was.key === key) return was;
  return { key, under: !was && idle && trailing };
}

/** A turn's key as the agent's turn there has it, or will have (a row by itself: the turn after it). */
export const turnKey = <T, W>(x: Turn<T, W>) => (x.kind === "one" ? `${x.key}:a` : x.key);

/** Where one that began work at a turn (its turnKey then) is drawn: after that turn, wherever it
 *  is now; after them all when it is not on the page. By the turn it is drawn before. */
export function slotAfter<T, W>(turns: Turn<T, W>[], key: string): number {
  const i = turns.findLastIndex((x) => turnKey(x) === key);
  return i < 0 ? turns.length : i + 1;
}

/** Where what is drawn before turn slot goes among the page's rows (turn i is at i): one right
 *  after the turn at work (tail) is under its row at work and over what you sent into it; with
 *  words of yours waiting to begin a turn, over those. */
export function rowPlace(slot: number, tail: number, before: boolean): number {
  return slot === tail + 1 ? tail + (before ? 0.05 : 0.25) : slot - 0.5;
}
