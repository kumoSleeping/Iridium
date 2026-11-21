// Reactions and messages taken back (docs/channels.md "表情" "撤回"), as the page holds them: each
// message's reactions by its channel and number, from what the log and a record bring (message.list
// and block.list _data) and from the events (reaction-added, reaction-removed); which block of a
// session's record is which message of its channel; the numbers taken back, and the blocks of
// the records kept here that they were, taken out.
import { useSyncExternalStore } from "react";
import { seenBlocks, seenLog, type ChannelMessage, type ReactionSum } from "../api/client";
import { peekChat } from "../chats";
import { events, type SatoriEvent } from "../stream";

export type { ReactionSum };
export type Reactor = { id: string; name?: string };

/** list with user's emoji put on (add) or taken off: each emoji once, in the order first put on;
 *  one with nobody left goes. The same array when nothing changed. */
export function reacted(list: ReactionSum[], emoji: string, user: Reactor, add: boolean): ReactionSum[] {
  const i = list.findIndex((r) => r.emoji === emoji);
  const r = list[i];
  if (add) {
    if (r?.users.some((u) => u.id === user.id)) return list;
    if (!r) return [...list, { emoji, count: 1, users: [user] }];
    return list.map((x, j) => (j === i ? { ...x, count: x.count + 1, users: [...x.users, user] } : x));
  }
  if (!r?.users.some((u) => u.id === user.id)) return list;
  const users = r.users.filter((u) => u.id !== user.id);
  return users.length ? list.map((x, j) => (j === i ? { ...x, count: users.length, users } : x)) : list.filter((_, j) => j !== i);
}

/** Where a block of session's record is in its channel, as the page asks: an agent's words by
 *  their place ("b:<at>"), a person's by their input ("i:<id>"). */
export const blockKey = (b: { at?: string; input?: string }) => (b.at ? `b:${b.at}` : b.input ? `i:${b.input}` : "");

/** The keys a message of channel's log is known by among its record's blocks: its agent's words
 *  said straight out (their block, the channel being that agent's session), a person's input. */
export function keysOf(channel: string, d: ChannelMessage["_data"]): string[] {
  const out: string[] = [];
  if (d?.block && d.session === channel) out.push(blockKey({ at: d.block }));
  if (d?.input) out.push(blockKey({ input: d.input }));
  return out;
}

/** A reaction an agent was given, alone in its channel (no channel's context): the message's
 *  number, who put which emoji on it, its words as the agent was given them. null: not one. */
export function reactionLine(text: string): { seq: number; who: string; emoji: string; words: string } | null {
  const m = /^#(\d+) \[([\s\S]+?) reacted (\S+) to: ([\s\S]*)\]$/.exec(text.trim());
  return m ? { seq: Number(m[1]), who: m[2]!, emoji: m[3]!, words: m[4]! } : null;
}

/** A block of a session's record as a message of its channel: its place (at), and a person's
 *  words their input. */
export type BlockOf = { at?: string; input?: string };

/** What a block may be named by to the server where its number is not known: what its message is
 *  from (a person's input; an agent's words, "b:<session>:<at>"). */
export const refOf = (channel: string, k: BlockOf) => k.input ?? (k.at ? `b:${channel}:${k.at}` : "");

const sums = new Map<string, ReactionSum[]>();
const seqs = new Map<string, number>();
const gone = new Set<string>();
const subs = new Set<() => void>();
let version = 0;
const changed = () => {
  version++;
  for (const f of subs) f();
};
const at = (channel: string, seq: number) => `${channel} ${seq}`;

/** The reactions on message seq of channel, as held. */
export const reactionsOf = (channel: string, seq: number | undefined): ReactionSum[] => (seq ? (sums.get(at(channel, seq)) ?? NONE) : NONE);
const NONE: ReactionSum[] = [];
/** The number in channel of the block known by key (blockKey), if known. */
export const seqOf = (channel: string, key: string) => seqs.get(`${channel} ${key}`);
/** Whether message seq of channel was taken back. */
export const isRecalled = (channel: string, seq: number) => gone.has(at(channel, seq));

function useHeld<T>(read: () => T): T {
  return useSyncExternalStore(
    (f) => (subs.add(f), () => void subs.delete(f)),
    read,
    read,
  );
}
export const useReactions = (channel: string, seq: number | undefined) => useHeld(() => reactionsOf(channel, seq));
/** The number of block k in channel: as block.list gave it (by its place), as the log and the
 *  events did (an agent's words by their place, a person's by their input). */
export const blockSeq = (channel: string, k: BlockOf) => (k.at ? seqOf(channel, blockKey({ at: k.at })) : undefined) ?? (k.input ? seqOf(channel, blockKey({ input: k.input })) : undefined);
export const useBlockSeq = (channel: string, k: BlockOf | undefined) => useHeld(() => (k ? blockSeq(channel, k) : undefined));
export const useRecalled = (channel: string, seq: number | undefined) => useHeld(() => !!seq && isRecalled(channel, seq));
/** Bumped at each change: what reads several of these draws again. */
export const useHeldVersion = () => useHeld(() => version);

/** Takes in a message as the log gives it: its reactions as they were then (none: none), what
 *  its blocks are, and a line of Iridium's own that says one was taken back. */
function take(channel: string, m: ChannelMessage, whole: boolean) {
  const n = Number(m.id);
  if (!Number.isInteger(n) || n <= 0) return;
  for (const k of keysOf(channel, m._data)) seqs.set(`${channel} ${k}`, n);
  const rs = m._data?.reactions;
  if (rs?.length) sums.set(at(channel, n), rs);
  else if (whole) sums.delete(at(channel, n));
  const ev = m._data?.event as { type?: string; seq?: number } | undefined;
  if (ev?.type === "recall" && ev.seq) recall(channel, ev.seq, undefined);
}

/** Message seq of channel was taken back: its reactions go, and the block of a record kept here
 *  that it was (from: what it was from, when told). */
function recall(channel: string, seq: number, from: { block?: string; input?: string; session?: string } | undefined) {
  gone.add(at(channel, seq));
  sums.delete(at(channel, seq));
  const k = { ...(from?.block && from.session === channel ? { at: from.block } : {}), ...(from?.input ? { input: from.input } : {}) };
  if (!k.at && !k.input) {
    // Not told what it was from: as its blocks are known here.
    for (const [key, n] of seqs) if (n === seq && key.startsWith(`${channel} `)) Object.assign(k, key.slice(channel.length + 1).startsWith("b:") ? { at: key.slice(channel.length + 3) } : { input: key.slice(channel.length + 3) });
  }
  const log = peekChat(channel)?.log;
  if (!log) return;
  const next = log.blocks.filter((b) => !((k.at && b.at === k.at) || (k.input && b.kind === "user" && b.id === k.input)));
  if (next.length === log.blocks.length) return;
  log.blocks = next;
  log.version++;
  for (const f of recalls) f(channel);
}

/** Told the channel whose kept record lost a block to a message taken back: its chat draws again. */
export const recalls = new Set<(channel: string) => void>();

/** Takes in an event: an emoji put on or taken off; a message said (what its block is), or taken
 *  back. */
export function hear(e: SatoriEvent) {
  const ch = e.channel?.id;
  const n = Number(e.message?.id);
  if (!ch || !Number.isInteger(n) || n <= 0) return;
  switch (e.type) {
    case "reaction-added":
    case "reaction-removed": {
      const emoji = e.emoji?.id;
      if (!emoji || !e.user) return;
      for (const k of keysOf(ch, e.message?._data)) seqs.set(`${ch} ${k}`, n);
      const was = sums.get(at(ch, n)) ?? NONE;
      const now = reacted(was, emoji, { id: e.user.id, ...(e.user.name ? { name: e.user.name } : {}) }, e.type === "reaction-added");
      if (now === was) return;
      if (now.length) sums.set(at(ch, n), now);
      else sums.delete(at(ch, n));
      return changed();
    }
    case "message-created":
      if (e.message) take(ch, e.message as ChannelMessage, false);
      return changed();
    case "message-deleted":
      recall(ch, n, e.message?._data);
      return changed();
  }
}

events.add(hear);
seenLog.add((channel, ms) => {
  for (const m of ms) take(channel, m, true);
  changed();
});
seenBlocks.add((session, ms) => {
  let any = false;
  for (const m of ms) {
    const n = m._data?.seq;
    if (!n) continue;
    any = true;
    seqs.set(`${session} ${blockKey({ at: m.id })}`, n);
    if (m._data?.reactions?.length) sums.set(at(session, n), m._data.reactions);
    else sums.delete(at(session, n));
  }
  if (any) changed();
});
