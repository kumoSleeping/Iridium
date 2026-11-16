// One page for a session, one agent or several (kumo, 2026-11-15: 会话页不换页，多一个 agent 只是
// 多一个成员): the own agent's record as its chat draws it, and what its channel's log has that the
// record does not, each in its place by time. Pure: chat.tsx draws what these say.
import { givenTurn, type Msg } from "./lib";

/** The log's messages the record does not hold: not the own agent's (its words are its record's),
 *  not a person's it was given (the record has the input, by its id) or that was read from it, not those before the record's
 *  first block as far as it is loaded (since: none when the record is loaded to its start). */
export function foreignOf(msgs: Msg[], own: string, inputs: ReadonlySet<string>, since?: number): Msg[] {
  return msgs.filter((m) => {
    if (m.author.kind === "agent" && m.author.session === own) return false;
    // A person's: sent (it has its input), unless given to it; read from its record (typed at its
    // terminal: no input), the record's.
    if (m.author.kind === "person" && (!m.input || inputs.has(m.input))) return false;
    if (since !== undefined && (m.time ?? 0) < since) return false;
    return true;
  });
}

/** Whether a user block of the own agent's record is its own channel's message as the agent was
 *  given it: the log has it, by who said it, and the record does not draw it again. Known by the
 *  relay the record says it is (satori.ts relays, a page read with block.list), else by its words
 *  ("#n: …" with the channel's block and no other channel named: a block the session's state
 *  brought, which says no relay; drawn, a person's words showed twice). */
export function givenHere(own: string, relay: { channel: string } | undefined, text: string | undefined): boolean {
  if (relay) return relay.channel === own;
  const g = givenTurn(text ?? "");
  return !!g && !g.channel;
}

/** When a row was written, for slotsOf: a time; undefined, or since alone: no time of its own
 *  (steps folded, a subagent at work); Infinity: what is being written. */
export type RowTime = number | undefined | { since?: number };

/** Where each message goes among rows by their times (no time of its own: as the next row with
 *  one — a turn's thinking is written just before its words, so it goes with them, not with the
 *  turn before; with none after — the turn's steps while it is still at them, or one that ended
 *  on a step — since when it began itself, else as the row before; Infinity: at the end, what is
 *  being written): before the first row later than it, in the log's order. By the row it goes
 *  before (rows.length: after them all). */
export function slotsOf<M extends { time?: number }>(times: RowTime[], ms: M[]): Map<number, M[]> {
  const ahead: (number | undefined)[] = [];
  let next: number | undefined;
  for (let i = times.length - 1; i >= 0; i--) {
    const t = times[i];
    if (typeof t === "number") ahead[i] = next = t;
    else ahead[i] = next ?? t?.since;
  }
  const eff: number[] = [];
  let last = -Infinity;
  for (const t of ahead) eff.push(t === undefined ? last : (last = t === Infinity ? Infinity : Math.max(last, t)));
  const out = new Map<number, M[]>();
  for (const m of ms) {
    const at = m.time ?? Infinity;
    let i = eff.findIndex((t) => t > at);
    if (i < 0) i = times.length;
    out.set(i, [...(out.get(i) ?? []), m]);
  }
  return out;
}

/** A row the log knows by its number: anchors for the read marks, in the page's order. */
export type Anchor = { key: string; seq: number };

/** Where each reader's mark goes: under the last row it has read (by key), none for one that has
 *  read none of those shown. skip: the member looking. */
export function marksAt<R extends { member: string; seq: number }>(anchors: Anchor[], readers: R[], skip?: string): Map<string, R[]> {
  const out = new Map<string, R[]>();
  for (const r of readers) {
    if (r.member === skip || r.seq < 1) continue;
    let at: Anchor | undefined;
    for (const a of anchors) if (a.seq <= r.seq && (!at || a.seq >= at.seq)) at = a;
    if (at) out.set(at.key, [...(out.get(at.key) ?? []), r]);
  }
  return out;
}

/** The own agent's rows the log knows by number: a said message by the block it was read from
 *  (src.block), else by the last row before where its stretch ends (src.to, a line); a person's
 *  by the input it was (the user block's id). */
export function ownAnchors(msgs: Msg[], own: string, rows: { key: string; line: number; id?: string }[]): Anchor[] {
  const out: Anchor[] = [];
  for (const m of msgs) {
    let key: string | undefined;
    if (m.author.kind === "agent" && m.author.session === own && m.src) {
      if (m.src.block) key = rows.find((r) => r.key === m.src!.block)?.key;
      else if (m.src.to !== undefined) key = rows.filter((r) => r.line < m.src!.to!).at(-1)?.key;
    } else if (m.author.kind === "person" && m.input) key = rows.find((r) => r.id === m.input)?.key;
    if (key) out.push({ key, seq: m.n });
  }
  return out;
}

/** The hand-over (kumo, 2026-11-15): an agent called by a message is shown at work only once that
 *  message is on the page, and, if the own agent said it, its words are whole (written to their
 *  end, not still coming): the words first, then the one called. Held at most HOLD from when it
 *  began to work, so a message that never shows hides no work. */
export const HOLD = 1500;
export type Hold = { since: number; called?: number };
export function held(h: Hold, shown: (seq: number) => boolean, writing: boolean, now: number): boolean {
  if (now - h.since >= HOLD) return false;
  if (writing) return true;
  return h.called !== undefined && !shown(h.called);
}

/** The message that called agent nick: the latest naming @nick, or all the agents (@全体Agent,
 *  @全体成员, as the log's text has them), by another. */
export function callerOf(msgs: Msg[], nick: string, session: string): number | undefined {
  if (!nick) return undefined;
  // Escaped as a pattern's own characters: under "u" an escape that means nothing (\-) throws, and
  // a default nick has a "-" (Pi-9579).
  const word = (w: string) => new RegExp(`(^|[^\\p{L}\\p{N}_-])@${w.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}(?![\\p{L}\\p{N}_-])`, "u");
  const calls = [word(nick), word("全体Agent"), word("全体成员")];
  for (let i = msgs.length - 1; i >= 0; i--) {
    const m = msgs[i]!;
    if (m.author.kind === "agent" && m.author.session === session) continue;
    if (m.author.kind === "iridium") continue;
    if (calls.some((c) => c.test(m.text))) return m.n;
  }
  return undefined;
}

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

/** How long words are, in lines, and whether they fold: more than FOLD_AT lines; folded, the
 *  first FOLD_SHOW show. */
export const FOLD_AT = 6;
export const FOLD_SHOW = 3;
export const linesOf = (text: string) => text.replace(/\s+$/, "").split("\n").length;
