// Old words folded of themselves (kumo, 2026-11-21, over 给人的话不折 for history read): a message
// long (more than FOLD_AT lines), read already (at or before the reader's place), and before the
// latest ROUNDS rounds, a round beginning at each person's message, is drawn folded as long words
// between agents are (longwords.tsx). Pure for tests; foldstate.ts keeps what was decided.
import { FOLD_AT, linesOf } from "./merge";
import type { Msg } from "./lib";

export const ROUNDS = 5;

/** Where the latest rounds begin among rows in order (starts: which begin a round): the index of
 *  the rounds-th last start; 0 with fewer, nothing before the latest rounds. */
export function recentFrom(starts: readonly boolean[], rounds = ROUNDS): number {
  let seen = 0;
  for (let i = starts.length - 1; i >= 0; i--) if (starts[i] && ++seen === rounds) return i;
  return 0;
}

/** Whether a message is long enough to fold of itself. */
export const longWords = (text: string) => linesOf(text) > FOLD_AT;

/** The numbers of msgs (the log in order) that fold of themselves, read up to read. The last
 *  answer kept: a page draws again many times a second while an agent writes. */
let last: { msgs: readonly Msg[]; read: number; rounds: number; out: Set<number> } | undefined;
export function oldRead(msgs: readonly Msg[], read: number, rounds = ROUNDS): Set<number> {
  if (last && last.msgs === msgs && last.read === read && last.rounds === rounds) return last.out;
  const out = oldOf(msgs, read, rounds);
  last = { msgs, read, rounds, out };
  return out;
}

function oldOf(msgs: readonly Msg[], read: number, rounds: number): Set<number> {
  const from = recentFrom(msgs.map((m) => m.author.kind === "person"), rounds);
  const out = new Set<number>();
  for (let i = 0; i < from; i++) {
    const m = msgs[i]!;
    if (m.n <= read && longWords(m.text)) out.add(m.n);
  }
  return out;
}
