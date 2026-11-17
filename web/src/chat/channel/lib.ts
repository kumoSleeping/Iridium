// A channel of several agents (docs/channels.md), what the page reads of it, pure for tests: its
// messages as the log gives them (Satori's message.list: only what was said), grouped under their
// speakers; the @nick that calls an agent, found in words as the server finds it
// (internal/deliver Mentions) and put into the composer; the agents listed under a channel's row;
// a channel's lamp.
import type { ChannelMessage } from "../../api/client";
import type { Agent, FileRef, Session, Status } from "../../api/types.gen";
import { blockOf } from "../../satori";

/** Who said a message: an agent of the channel (its session, the nick it is called by, what it
 *  ran as), a person (their tag), or Iridium itself. */
export type Author = { kind: "agent"; session: string; nick: string; name: string } | { kind: "person"; id: string; name: string } | { kind: "iridium" };

/** A message of a channel: n, its number there. kind: as its event said it (user, progress, reply,
 *  ask, error, note); the log itself does not say. */
/** Where an agent's message is from in its own record (docs/channels.md "指向"): said straight
 *  out, the block; with ir, the lines from (where it was called) to to (where it spoke). */
export type Src = { session: string; block?: string; from?: number; to?: number };

/** input: a person's message, the input it was sent as (a session's record has it by that id);
 *  an agent's sent with ir, the same. */
export type Msg = { n: number; text: string; files: FileRef[]; time?: number; author: Author; kind?: string; did?: Did; event?: ChEvent; src?: Src; quote?: number; input?: string };

/** The line of a block's at ("<line>.<n>"). */
export const lineOf = (at: string) => Number.parseInt(at, 10);

/** The work under an agent's message from what its record has around it (newest last): with ir,
 *  the blocks of lines from..to; said straight out, those after the last thing it said or was
 *  given before the block. Its thinking, tool calls, and words it said on the way. */
export function workOf<B extends { at: string; kind: string; text?: string }>(blocks: B[], src: Src, said?: string): B[] {
  // The words said themselves are drawn under the work: not again in it (a reply said for the
  // agent at its turn's end is the last words of its stretch).
  const same = (b: B) => b.kind === "assistant" && !!said?.trim() && b.text?.trim() === said.trim();
  if (src.from !== undefined && src.to !== undefined) return blocks.filter((b) => lineOf(b.at) >= src.from! && lineOf(b.at) < src.to! && b.kind !== "user" && b.kind !== "system" && !same(b));
  if (!src.block) return [];
  const end = lineOf(src.block);
  const out: B[] = [];
  for (let i = blocks.length - 1; i >= 0; i--) {
    const b = blocks[i]!;
    if (lineOf(b.at) >= end) continue;
    if (b.kind === "user" || b.kind === "system" || (b.kind === "assistant" && b.text?.trim())) break;
    out.unshift(b);
  }
  return out;
}

/** Something that happened to a channel, a line of Iridium's own in it (internal/channel Event):
 *  join, leave (nick), rename (from, nick), recv (nick, recv), people (added, removed: member
 *  strings; everyone), undelivered (seq, nick), stopped (seq), silent (seq, nick: called for it,
 *  it said nothing in the channel; reason: why, its turn ending in an error), profile (a card
 *  changed: member's, by nick, or the channel's own with no member; what: the fields, "name",
 *  "intro", "avatar", "background"). by: who did it, by
 *  name (the host's as Iridium, however it was kept: 主机 before 2026-11-15). */
export type ChEvent = { type: string; member?: string; nick?: string; name?: string; from?: string; recv?: string; added?: string[]; removed?: string[]; everyone?: boolean; seq?: number; reason?: string; by?: string; what?: string[] };

/** A piece of an event's line: words, an agent by its nick, people by their member strings. */
export type EventPart = string | { nick: string } | { people: string[] };

/** The line's words by event (i18n: channel.event.*), their slots in braces: {nick} {from}
 *  {recv} {people} {seq} {reason} {by}; reasons: the server's usual reasons (余额不足…) in them. */
export type EventWords = Record<"join" | "leave" | "rename" | "recv" | "everyone" | "added" | "removed" | "undelivered" | "stopped" | "silent" | "silentWhy" | "by" | "sep" | "all" | "at", string> & {
  reasons?: Record<string, string>;
  /** A card changed: a member's, the channel's; each with who did it in its words, and without. */
  profile?: string;
  profileChannel?: string;
  profileNoBy?: string;
  profileChannelNoBy?: string;
  /** The fields of a card by name ({what}), and what stands between them. */
  what?: Record<string, string>;
  whatSep?: string;
};

/** An event as the pieces of its line, in the page's words; none for one not known (its message's
 *  own text is shown then). */
export function eventParts(e: ChEvent, w: EventWords): EventPart[] | undefined {
  const fill = (tpl: string): EventPart[] =>
    tpl
      .split(/(\{[a-z]+\})/)
      .filter(Boolean)
      .map((x): EventPart => {
        switch (x) {
          case "{nick}":
            return { nick: e.nick ?? "" };
          case "{from}":
            return `@${e.from ?? ""}`;
          case "{recv}":
            return e.recv === "all" ? w.all : w.at;
          case "{seq}":
            return String(e.seq ?? "");
          case "{by}":
            return e.by === "主机" || e.by === "本机" ? "Iridium" : (e.by ?? "");
          case "{reason}":
            return w.reasons?.[e.reason ?? ""] ?? e.reason ?? "";
          case "{added}":
            return { people: e.added ?? [] };
          case "{removed}":
            return { people: e.removed ?? [] };
          case "{what}":
            return (e.what ?? []).map((x) => w.what?.[x] ?? x).join(w.whatSep ?? ", ");
        }
        return x;
      });
  let out: EventPart[];
  switch (e.type) {
    case "join":
    case "leave":
    case "rename":
    case "recv":
    case "undelivered":
    case "stopped":
      out = fill(w[e.type]);
      break;
    case "silent":
      out = fill(e.reason ? w.silentWhy : w.silent);
      break;
    case "people": {
      const bits = [e.everyone ? fill(w.everyone) : [], e.added?.length ? fill(w.added) : [], e.removed?.length ? fill(w.removed) : []].filter((b) => b.length);
      if (!bits.length) return undefined;
      out = bits.flatMap((b, i) => (i ? [w.sep, ...b] : b));
      break;
    }
    case "profile": {
      // Who did it is in the line's own words, not after it.
      const tpl = e.member ? (e.by ? w.profile : w.profileNoBy) : e.by ? w.profileChannel : w.profileChannelNoBy;
      if (!tpl || !e.what?.length) return undefined;
      out = fill(tpl);
      break;
    }
    default:
      return undefined;
  }
  if (e.by && e.type !== "profile") out.push(...fill(w.by));
  // Words side by side are one piece.
  return out.reduce<EventPart[]>((all, p) => (typeof p === "string" && typeof all.at(-1) === "string" ? [...all.slice(0, -1), (all.at(-1) as string) + p] : [...all, p]), []);
}

/** A person's entry by name: an app's all its name, a login its own; not known, as written. */
export function personName(member: string, apps: { id: string; name: string }[], logins: { app: string; id: string; name: string }[]): string {
  const i = member.indexOf("/");
  const app = member.slice(0, i), login = member.slice(i + 1);
  if (i < 0) return member;
  if (login === "*") return apps.find((a) => a.id === app)?.name ?? member;
  return logins.find((l) => l.app === app && l.id === login)?.name ?? member;
}

/** What an agent did before it said a message (docs/channels.md "活动"): its tool calls counted,
 *  the programs its commands began with (cmds, "go 4, git 2"), the files it changed. */
export type Did = { bash: number; failed: number; cmds: string; read: number; web: number; agents: number; other: number; files: number; add: number; del: number; paths: { path: string; add: number; del: number }[] };

const ATTR = /([a-z]+)="([^"]*)"/g;
const unesc = (s: string) => s.replace(/&quot;/g, '"').replace(/&lt;/g, "<").replace(/&gt;/g, ">").replace(/&amp;/g, "&");

/** The <iridium:activity> of a message's content, if it has one. */
export function didOf(content: string): Did | undefined {
  const at = content.lastIndexOf("<iridium:activity");
  if (at < 0) return undefined;
  const rest = content.slice(at);
  const head = rest.slice(0, rest.indexOf(">") + 1);
  const a: Record<string, string> = {};
  for (const m of head.matchAll(ATTR)) a[m[1]!] = unesc(m[2]!);
  const num = (k: string) => Number(a[k] ?? 0) || 0;
  const paths = [...rest.matchAll(/<iridium:file ([^>]*)\/>/g)].map((f) => {
    const fa: Record<string, string> = {};
    for (const m of f[1]!.matchAll(ATTR)) fa[m[1]!] = unesc(m[2]!);
    return { path: fa.path ?? "", add: Number(fa.add ?? 0) || 0, del: Number(fa.del ?? 0) || 0 };
  });
  return { bash: num("bash"), failed: num("failed"), cmds: a.cmds ?? "", read: num("read"), web: num("search") + num("fetch"), agents: num("agents"), other: num("other"), files: num("files"), add: num("add"), del: num("del"), paths };
}

const AGENT = "agent:";

/** m of the log as the page draws it; none for one without a number. */
export function msgOf(m: ChannelMessage): Msg | undefined {
  const n = Number(m.id);
  if (!Number.isInteger(n) || n <= 0) return undefined;
  const b = blockOf({ id: m.id, content: m.content, created_at: m.created_at });
  const u = m.user;
  const author: Author = u?.id.startsWith(AGENT) ? { kind: "agent", session: u.id.slice(AGENT.length), nick: u.nick ?? "", name: u.name ?? "" } : !u || u.id === "iridium" ? { kind: "iridium" } : { kind: "person", id: u.id, name: u.name ?? "" };
  // The message it answers: Satori's <quote id/> at its start.
  const q = /^<quote id="(\d+)"\s*\/>/.exec(m.content);
  return { n, text: b.text ?? "", files: b.files ?? [], author, ...(q ? { quote: Number(q[1]) } : {}), ...(m.created_at ? { time: m.created_at } : {}), ...(m._data?.kind ? { kind: m._data.kind } : {}), ...(m._data?.input ? { input: m._data.input } : {}), ...(author.kind === "agent" && didOf(m.content) ? { did: didOf(m.content)! } : {}), ...(m._data?.event && typeof m._data.event === "object" ? { event: m._data.event as ChEvent } : {}), ...(author.kind === "agent" && m._data?.session ? { src: { session: m._data.session, ...(m._data.block ? { block: m._data.block } : {}), ...(typeof m._data.from === "number" ? { from: m._data.from } : {}), ...(m._data.to ? { to: m._data.to } : {}) } } : {}) };
}

/** The key the work under a message from src is kept by (page.tsx Work; chats.ts firstScreen
 *  puts in what the first screen brought). */
export const workKey = (src: Src) => ["work", src.session, src.block ?? "", src.from ?? 0, src.to ?? 0];

const sameSrc = (a?: Src, b?: Src) => a?.session === b?.session && a?.block === b?.block && a?.from === b?.from && a?.to === b?.to;

/** have with more put in: each number once (the later one wins: a message read again, changed:
 *  its words, or where it is from in its agent's record, or a person's input id, which one heard
 *  as it was said may not have), in order. The same array when nothing changed. */
export function merge(have: Msg[], more: Msg[]): Msg[] {
  if (!more.length) return have;
  const by = new Map(have.map((m) => [m.n, m]));
  let changed = false;
  for (const m of more) {
    const was = by.get(m.n);
    if (was && was.text === m.text && was.files.length === m.files.length && was.kind === (m.kind ?? was.kind) && !was.did === !m.did && !was.event === !m.event && was.quote === m.quote && (!m.src || sameSrc(was.src, m.src)) && (!m.input || was.input === m.input)) continue;
    // What a reading does not say (the kind, the source) stays as it was known.
    by.set(m.n, was ? { ...m, ...(!m.kind && was.kind ? { kind: was.kind } : {}), ...(!m.src && was.src ? { src: was.src } : {}), ...(!m.input && was.input ? { input: was.input } : {}) } : m);
    changed = true;
  }
  return changed ? [...by.values()].sort((a, b) => a.n - b.n) : have;
}

/** Messages one after another under one head: an agent's, while the same agent speaks; Iridium's
 *  lines in a row close together; a person's each by itself. */
export type Group = { key: string; author: Author; msgs: Msg[] };

/** Where each reader's mark goes (docs/channels.md "已读"): under the group that holds the last
 *  message it has read, by the group's key; one that has read nothing shown, or all of what is
 *  before the first group, has none. skip: the member that is looking. */
export function readMarks<R extends { member: string; seq: number }>(groups: Group[], readers: R[], skip?: string): Map<string, R[]> {
  const out = new Map<string, R[]>();
  for (const r of readers) {
    if (r.member === skip || r.seq < 1) continue;
    let at: Group | undefined;
    for (const g of groups) {
      if (g.msgs[0]!.n > r.seq) break;
      at = g;
    }
    if (at) out.set(at.key, [...(out.get(at.key) ?? []), r]);
  }
  return out;
}

export function groupMessages(msgs: Msg[]): Group[] {
  const out: Group[] = [];
  for (const m of msgs) {
    const last = out.at(-1);
    if (last && m.author.kind === "agent" && last.author.kind === "agent" && last.author.session === m.author.session) last.msgs.push(m);
    else if (last && m.author.kind === "iridium" && last.author.kind === "iridium") last.msgs.push(m);
    else out.push({ key: `m${m.n}`, author: m.author, msgs: [m] });
  }
  return out;
}

const NICK_RUNE = /[\p{L}\p{N}_-]/u;
const NICK = /^[\p{L}\p{N}_-]+$/u;

/** A nick: letters, digits, "_" and "-", at most 32 of them (internal/deliver ValidNick). */
export const validNick = (n: string) => NICK.test(n) && [...n].length <= 32;

/** The names that call more than one in a channel (internal/deliver AllMembers, AllAgents). */
export const ALL_MEMBERS = "全体成员";
export const ALL_AGENTS = "全体Agent";

/** Where words name one of nicks (an agent's nick, a person's or an app's name, the names of
 *  all): "@name" standing by itself (not inside a word, an address or a longer name), outside
 *  code (a fenced block, an inline span) and quoted lines; case does not matter. An agent's nick
 *  as the server routes by (internal/deliver Mentions), each place it is said; a person's name
 *  may have what a nick may not (a space, a dot): the longest that fits is the one named. */
export function nickSpans(text: string, nicks: string[]): { from: number; to: number; nick: string }[] {
  if (!nicks.length || !text.includes("@")) return [];
  const names = [...new Set(nicks.filter(Boolean))].map((n) => [n.toLowerCase(), n] as const).sort((a, b) => b[0].length - a[0].length);
  const out: { from: number; to: number; nick: string }[] = [];
  let fenced = false;
  let at = 0;
  for (const line of text.split("\n")) {
    const start = at;
    at += line.length + 1;
    const t = line.trim();
    if (t.startsWith("```") || t.startsWith("~~~")) {
      fenced = !fenced;
      continue;
    }
    if (fenced || t.startsWith(">")) continue;
    const low = line.toLowerCase();
    let code = false;
    for (let i = 0; i < line.length; i++) {
      const c = line[i];
      if (c === "`") code = !code;
      if (code || c !== "@") continue;
      const before = [...line.slice(Math.max(0, i - 2), i)].at(-1);
      if (before !== undefined && (NICK_RUNE.test(before) || before === "@" || before === "/" || before === ".")) continue;
      const hit = names.find(([w]) => {
        if (!low.startsWith(w, i + 1)) return false;
        const after = [...line.slice(i + 1 + w.length, i + 3 + w.length)][0];
        return after === undefined || !NICK_RUNE.test(after);
      });
      if (!hit) continue;
      out.push({ from: start + i, to: start + i + 1 + hit[0].length, nick: hit[1] });
      i += hit[0].length;
    }
  }
  return out;
}

/** Words cut at the nicks they call: plain runs and nicks, in order. */
export function nickParts(text: string, nicks: string[]): (string | { nick: string })[] {
  const out: (string | { nick: string })[] = [];
  let at = 0;
  for (const s of nickSpans(text, nicks)) {
    if (s.from > at) out.push(text.slice(at, s.from));
    out.push({ nick: s.nick });
    at = s.to;
  }
  if (at < text.length) out.push(text.slice(at));
  return out;
}

/** The address a nick's link in Markdown has (chat/markdown.tsx draws it: NickLink). */
export const nickURL = (nick: string) => `ir:member/${encodeURIComponent(nick)}`;
export const nickOfURL = (url: unknown) => {
  const m = typeof url === "string" ? /^ir:member\/([^/\s]+)$/.exec(url) : null;
  try {
    return m ? decodeURIComponent(m[1]!) : undefined;
  } catch {
    return undefined;
  }
};

/** An agent's Markdown with each nick it calls a link, drawn as the nick's mark. */
export function linkNicks(text: string, nicks: string[]): string {
  let out = "";
  let at = 0;
  for (const s of nickSpans(text, nicks)) {
    out += `${text.slice(at, s.from)}[@${s.nick}](${nickURL(s.nick)})`;
    at = s.to;
  }
  return out + text.slice(at);
}

/** "@nick " put into text over [a, b) (the caret, a selection, an @word being typed): a space
 *  before it unless at a line's start or after one; the caret after it. */
export function insertNick(text: string, a: number, b: number, nick: string): { text: string; caret: number } {
  const head = text.slice(0, a);
  const before = head + (head && !/\s$/.test(head) ? " " : "");
  const words = `@${nick} `;
  return { text: before + words + text.slice(b).replace(/^ /, ""), caret: before.length + words.length };
}

/** A member as the @ list and the words that name it show it: an agent; or (person) a login or
 *  an app, its name as nick, its app (or 应用) as name, its avatar. */
export type AgentRow = { session: string; member: string; agent: Agent; nick: string; name: string; status: Status; recv: string; avatar?: string; person?: boolean };

/** Whether user (a Satori user id) may be talked to alone (docs/channels.md "私聊"): the host, or
 *  one of logins; not an app, nor an author that is no login (the server has no one to open the
 *  channel with). */
export const mayDM = (user: string, logins: { id: string }[]) => user === "local" || logins.some((l) => l.id === user);

/** The Satori user id of a person's member string: a login's own id ("<app>/<login>"), an app's
 *  for all of its logins ("<app>/*"). */
export function userOf(member: string): string {
  const i = member.indexOf("/");
  const login = member.slice(i + 1);
  return i < 0 || login !== "*" ? login : member.slice(0, i);
}

/** Whether a member is the channel's own agent: the session the channel is. It speaks there
 *  straight out, and is not taken out of it. */
export const ownAgent = (channel: string, m: { session?: string | null }) => !!m.session && m.session === channel;

/** The agents an @q may call: nicks that start with it first, then nicks and names that have it. */
export function matchAgents<T extends { nick: string; name: string }>(rows: T[], q: string): T[] {
  const w = q.toLowerCase();
  const rank = (r: T) => (!w || r.nick.toLowerCase().startsWith(w) ? 0 : r.nick.toLowerCase().includes(w) ? 1 : r.name.toLowerCase().includes(w) ? 2 : 3);
  return rows
    .map((r) => [rank(r), r] as const)
    .filter(([n]) => n < 3)
    .sort((a, b) => a[0] - b[0])
    .map(([, r]) => r);
}

/** Whether a session is a channel of several agents. */
export const isMulti = (s?: Pick<Session, "agents">) => (s?.agents?.length ?? 0) > 1;

/** One ask in a channel's queue (docs/channels.md "提问"): whose, and when the page first saw it. */
export type Queued<T> = { session: string; ask: T; at: number };

/** The asks of a channel's agents as one queue, oldest first (kumo, 2026-11-15). An ask has no
 *  time of its own: the first time the page sees it (seen, by card, kept across draws; those gone
 *  are dropped from it), in the agents' order for those seen at once. */
export function askQueue<T extends { requestId: string; card?: { id: string } }>(per: { session: string; asks: T[] }[], seen: Map<string, number>, now = Date.now()): Queued<T>[] {
  const out: Queued<T>[] = [];
  const here = new Set<string>();
  for (const p of per)
    for (const a of p.asks) {
      const key = a.card?.id ?? `ask:${p.session}:${a.requestId}`;
      here.add(key);
      if (!seen.has(key)) seen.set(key, now);
      out.push({ session: p.session, ask: a, at: seen.get(key)! });
    }
  for (const k of [...seen.keys()]) if (!here.has(k)) seen.delete(k);
  return out.sort((a, b) => a.at - b.at);
}

const WORKING = new Set<Status>(["running", "compacting", "starting"]);

/** A channel's lamp, from its agents': amber while one waits on you, else green while one works,
 *  else its own session's. */
export function channelLamp(own: Status, members: Status[]): Status {
  if (members.includes("waiting")) return "waiting";
  if (WORKING.has(own)) return own;
  return members.some((s) => WORKING.has(s)) ? "running" : own;
}

/** A message sent from here, till the channel's log has it: seq, its number once the server
 *  answered; from, the log's last number as it was sent. */
export type Waiting = { text: string; seq?: number; from: number; failed?: string };

/** Whether what was sent is still not in the log: by its number once known; before that, by a
 *  person's same words said since it was sent. A failed one stays, to be sent again. */
export function stillWaiting(s: Waiting, msgs: Msg[]): boolean {
  if (s.failed !== undefined) return true;
  if (s.seq) return !msgs.some((m) => m.n === s.seq);
  return !msgs.some((m) => m.n > s.from && m.author.kind === "person" && m.text.trim() === s.text.trim());
}

/** The states the stream is asked for besides the channel's own: its other agents' sessions
 *  first, then their contexts, as far as it has room (stream.ts keysFor). */
export const watchExtra = (channel: string, sessions: string[]) => {
  const rest = sessions.filter((s) => s !== channel);
  return [...rest.map((s) => `session:${s}`), ...rest.map((s) => `context:${s}`)];
};

/** What an agent was given of a channel, as its own record has it (internal/ctx/assemble
 *  ChannelTurn: "#n: words", then a <channel-context> block): the message's number and its
 *  words alone; channel: the channel it was called in, when that is not its own (it is in it by a
 *  subscription, docs/channels.md). null: not such a message. */
export type GivenTurn = { seq: number; words: string; context: string; channel?: { id: string; name: string } };

export function givenTurn(text: string): GivenTurn | null {
  const m = /^#(\d+): ([\s\S]*?)\n\n<channel-context>\n([\s\S]*)$/.exec(text);
  if (!m) return null;
  const c = /^You are \S+, called in the channel "([\s\S]*?)"(?: \(([0-9a-zA-Z_-]+)\))?, which people/.exec(m[3]!);
  // What Iridium added: the block's lines, and what follows it (how to read more).
  const context = m[3]!.replace(/\n?<\/channel-context>/, "").trim();
  return { seq: Number(m[1]), words: m[2]!, context, ...(c ? { channel: { id: c[2] ?? "", name: c[1]! } } : {}) };
}

/** A part of what an agent was given (Injection.record): where it is from, the values its slots
 *  took, the messages it cites (#n). */
export type GivenPart = { source: string; values: [string, string][]; refs: string[] };

/** What an injection was (its record's kind): the system prompt, a channel's message, another
 *  line given (a message, Iridium's own), what ir was answered with. */
export type GivenKind = "system" | "turn" | "message" | "ir";
export function givenKind(record: unknown): GivenKind {
  const k = (record as { kind?: unknown } | null)?.kind;
  return k === "system" ? "system" : k === "turn-prefix" ? "turn" : k === "tool-result" ? "ir" : "message";
}
/** The whole text it was given (records kept before it was kept: none). */
export const givenText = (record: unknown): string => {
  const x = (record as { text?: unknown } | null)?.text;
  return typeof x === "string" ? x : "";
};

export function givenParts(record: unknown): GivenPart[] {
  const parts = (record as { parts?: unknown } | null)?.parts;
  if (!Array.isArray(parts)) return [];
  return parts.map((p: { source?: unknown; values?: unknown; refs?: unknown }) => ({
    source: typeof p.source === "string" ? p.source : "",
    values: p.values && typeof p.values === "object" ? Object.entries(p.values as Record<string, unknown>).map(([k, v]) => [k, String(v)] as [string, string]) : [],
    refs: Array.isArray(p.refs) ? p.refs.flatMap((r: { kind?: unknown; id?: unknown }) => (r?.kind === "message" && r.id !== undefined ? [`#${String(r.id)}`] : [])) : [],
  }));
}
