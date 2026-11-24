// Folding rules for channel messages (kumo, 2026-11-15, 2026-11-21): long words between agents
// fold (给人的话不折), and long words read before the latest ROUNDS rounds fold of themselves.
// Pure for tests; foldstate.ts keeps what was decided.
import type { Msg } from "./lib";

/** How long words are, in lines, and whether they fold: more than FOLD_AT lines; folded, the
 *  first FOLD_SHOW show. */
export const FOLD_AT = 6;
export const FOLD_SHOW = 3;
export const linesOf = (text: string) => text.replace(/\s+$/, "").split("\n").length;

/** Who a message names: agents (by <at id="agent:…"/>, <at role="agent"/>, @全体Agent, or a nick
 *  of the channel's), people (anyone else, <at type="all"/>, @全体成员). */
export function named(text: string, nicks: ReadonlySet<string>): { agents: number; people: number } {
  let agents = 0;
  let people = 0;
  for (const m of text.matchAll(/<at\s([^>]*)\/?>/g)) {
    const a = m[1]!;
    if (/\brole="agent"/.test(a) || /\bid="agent:/.test(a)) agents++;
    else people++;
  }
  const plain = text.replace(/<at\s[^>]*\/?>/g, " ").replace(/`[^`]*`/g, " ");
  for (const m of plain.matchAll(/(?:^|[^\p{L}\p{N}_.@-])@([\p{L}\p{N}_-]+)/gu)) {
    const w = m[1]!;
    if (w === "全体Agent" || nicks.has(w.toLowerCase())) agents++;
    else people++;
  }
  return { agents, people };
}

/** Words between agents, folded when long (kumo, 2026-11-15: 给人的话不折): an agent's message
 *  that names only agents, or, naming none, one in a stretch an agent began (since its last word
 *  the latest to call it was an agent, no person spoke). Never: a person's, Iridium's, an ask, an
 *  error, one naming a person or all. msgs: the log in order; nicks: the agents', lower case. */
export function betweenAgents(m: Msg, msgs: Msg[], nicks: ReadonlySet<string>): boolean {
  if (m.author.kind !== "agent" || m.kind === "ask" || m.kind === "error") return false;
  const n = named(m.text, nicks);
  if (n.people) return false;
  if (n.agents) return true;
  const self = m.author.session;
  const nick = m.author.nick.toLowerCase();
  for (let i = msgs.findIndex((x) => x.n === m.n) - 1; i >= 0; i--) {
    const x = msgs[i]!;
    if (x.author.kind === "agent" && x.author.session === self) return false;
    if (x.author.kind === "person") return false;
    if (x.author.kind === "agent" && nick && (named(x.text, new Set([nick])).agents > 0 || x.text.includes(`agent:${self}`))) return true;
  }
  return false;
}

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
