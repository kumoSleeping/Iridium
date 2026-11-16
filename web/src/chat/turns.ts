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
