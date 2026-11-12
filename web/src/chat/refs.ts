// Two readings of what a chat shows, pure for tests: who a message of the user's is from (plan
// "消息来源"), and the files a reply names (plan "网页": `![](路径)`, `[名](路径)` and absolute paths
// in inline code).
import type { Agent, Device, Session } from "../api/types.gen";
import { titleOf } from "../ui";

/** A user block's source as the page draws it (QQ-like: a face, a name, a badge on the face):
 *  your words (typed here, on another panel, or in a terminal: that terminal's name, avatar and
 *  device), a letter (from another session's ir: its agent and session; from a plugin or peer), or
 *  Iridium's own line. local: 本机 (the machine's terminal, or this page with no gate). */
export type Source =
  | { kind: "you"; name: string; avatar?: string; device: Device; local?: boolean }
  | { kind: "letter"; name: string; avatar?: string; channel: boolean; type?: string; agent?: Agent; session?: string; workflow?: boolean; where?: string }
  | { kind: "iridium" };

/** What sourceOf needs to know of who sent a message: an app, a login of ir_web (web), or the host. */
export type Who = { id: string; name: string; avatar?: string; device?: Device; host?: boolean; web?: boolean; type?: string };

const HOST: Who = { id: "local", name: "主机", host: true };

/** Your words from terminal x. */
export const yours = (x: Who): Extract<Source, { kind: "you" }> => ({ kind: "you", name: x.name, avatar: x.avatar, device: x.device ?? "computer", ...(x.host ? { local: true } : {}) });

/** Who wrote a message: from, the tag Iridium wrote it with, and terminal, the sending terminal's ID
 *  (Block.from, fromTerminal). me: this panel ("local" with no gate). An agent's ir from another
 *  session is tagged "ir:<session>"; a block with no tag was typed in the agent's own terminal, on
 *  the machine (本机) — except in a subagent, where it is its parent's prompt (of: the session shown,
 *  whose workspace a sender's name leaves out). */
export function sourceOf(from: string | undefined, terminal: string | undefined, me: Who, terminals: Who[], sessions: Session[], workspaces: { id: number; name: string }[] = [], of?: Pick<Session, "subagent" | "parent" | "workflow" | "workspace">): Source {
  const here = of ? (of.workspace ?? 0) : undefined; // 0: 未归类 (the field left out)
  if (from === "Iridium") return { kind: "iridium" };
  // A subagent's prompt is written by the session that started it (an Agent tool call, a workflow).
  if (!from && of?.subagent) {
    const p = of.parent ? sessions.find((s) => s.id === of.parent) : undefined;
    const wf = of.workflow ? { workflow: true } : {};
    const name = p ? senderName(p, workspaces, here) : (of.parent?.slice(0, 8) ?? "");
    const named = of.workflow?.name ? (name ? `${name} · ${of.workflow.name}` : of.workflow.name) : name;
    return { kind: "letter", name: named, channel: false, ...(p ? { agent: p.agent, session: p.id, where: p.cwd } : of.parent ? { session: of.parent } : {}), ...wf };
  }
  // Another session's ir: that session, its agent's logo, a key to go there.
  const id = from && /^(?:ir|session):(.+)$/.exec(from)?.[1];
  if (id) {
    const s = sessions.find((s) => s.id === id || s.id.startsWith(id));
    return s ? { kind: "letter", name: senderName(s, workspaces, here), channel: false, agent: s.agent, session: s.id, where: s.cwd } : { kind: "letter", name: id.slice(0, 8), channel: false };
  }
  const local = terminals.find((x) => x.host) ?? (me.host ? me : HOST);
  if (!from) return yours(local);
  if ((terminal ?? "") === me.id) return yours(me);
  // By ID; by name only without one (a name is unique only in its scope: ir_web's logins first).
  const named = terminals.filter((x) => x.name.toLowerCase() === from.toLowerCase());
  const e = terminal ? terminals.find((x) => x.id === terminal) : (named.find((x) => x.web || x.host) ?? named[0]);
  if (e?.id === me.id) return yours(me);
  // Another login of ir_web, or the host (its page with no gate, a person's ir): your words too.
  if (e?.web || e?.host) return yours(e);
  if (!e && (terminal === "local" || from === "主机" || from === "本机")) return yours(local);
  // A plugin writes "[name·sender]": the plugin's avatar, the whole tag as the name.
  return { kind: "letter", name: from, avatar: e?.avatar, channel: true, ...(e?.type ? { type: e.type } : {}) };
}

/** A session as the sender of a letter, as the sidebar calls it (the user, 2026-11-06): its given
 *  name, else the opening of its first message, else 新会话. Not its agent: its face is the
 *  agent's logo. Its workspace only when it is not here, the workspace of the chat shown (none
 *  known: none added): "Run the holder's tests · KDJ". */
export function senderName(s: Pick<Session, "id" | "name" | "title" | "cwd" | "workspace">, workspaces: { id: number; name: string }[] = [], here?: number): string {
  const name = titleOf(s);
  if (here === undefined || (s.workspace ?? 0) === (here ?? 0)) return name;
  const where = (s.workspace && workspaces.find((w) => w.id === s.workspace)?.name) || s.cwd.split("/").filter(Boolean).pop();
  return where ? `${name} · ${where}` : name;
}

/** The type a session's row is dragged with (sidebar.tsx), its id the data. */
export const SESSION_DRAG = "application/x-iridium-session";
/** The type a group's or a folder's row is dragged with (sidebar.tsx): {path, name} as JSON, path
 *  the page's ("<machine>:<path>" at another machine, '/'-separated). */
export const PLACE_DRAG = "application/x-iridium-place";

/** The shortest start of id, at least 8 characters, no other of ids shares: what ir takes for a
 *  session (internal/cli short). */
export function shortId(id: string, ids: string[]): string {
  for (let n = 8; n < id.length; n++) if (!ids.some((o) => o !== id && o.startsWith(id.slice(0, n)))) return id.slice(0, n);
  return id;
}

/** A session or a place named in words (docs/plan.md "@", 2026-11-12): a link of Iridium's in the
 *  words where it was put, the same written by you and by an agent. A session [title](ir:session/<id>),
 *  its id or the start of one; a place [name](ir:folder/<where>), where a path here
 *  (ir:folder//home/otae/ir) or <machine>:<path> at another (ir:folder/otae-2:C:\Code\Ds), the
 *  path as that machine writes it, <…> round it when it has a space or a bracket. agent: a session's
 *  agent where known without the list (an ir new step's). text: the link as written. */
export type Mention =
  | { kind: "session"; title: string; id: string; agent?: string; text: string }
  | { kind: "place"; name: string; machine?: string; path: string; text: string };

const REF = /\[([^[\]\n]{0,200})\]\((?:<(ir:(?:session|folder)\/[^<>\n]+)>|(ir:(?:session|folder)\/[^\s()<>]+))\)/g;

/** What an ir:session/… or ir:folder/… link names, its words label; none if it is not one. */
export function refOf(url: string, label: string, text = ""): Mention | undefined {
  const sid = /^ir:session\/([0-9A-Za-z_-]{4,64})$/.exec(url)?.[1];
  if (sid) return { kind: "session", title: label, id: sid, text };
  const where = /^ir:folder\/(.+)$/.exec(url)?.[1];
  if (!where) return undefined;
  if (where.startsWith("/")) return { kind: "place", name: label, path: where, text };
  const i = where.indexOf(":");
  const machine = where.slice(0, i), path = where.slice(i + 1);
  return i > 0 && !machine.includes("/") && path ? { kind: "place", name: label, machine, path, text } : undefined;
}

/** A message's text cut at the sessions and places it names: plain runs and mentions, in order. */
export function mentions(text: string): (string | Mention)[] {
  if (!text.includes("](")) return text ? [text] : [];
  const out: (string | Mention)[] = [];
  let at = 0;
  for (const m of text.matchAll(REF)) {
    const r = refOf(m[2] ?? m[3]!, m[1]!, m[0]);
    if (!r) continue;
    if (m.index > at) out.push(text.slice(at, m.index));
    out.push(r);
    at = m.index + m[0].length;
  }
  if (at < text.length) out.push(text.slice(at));
  return out;
}

/** A name as a link's words: on one line, no square brackets (as their full-width kin), at most 200. */
function sayable(name: string): string {
  return [...name.replaceAll("[", "［").replaceAll("]", "］").replace(/\s+/g, " ").trim()].slice(0, 200).join("");
}

/** How words name session id (its shortest start ir takes), titled title. */
export const sessionLink = (title: string, id: string) => `[${sayable(title)}](ir:session/${id})`;

/** How words name a place: here by its path; at another machine by machine (its name, or its ID if
 *  the name has a colon, a slash or a space) and path as that machine writes it. */
export function placeLink(name: string, path: string, machine?: string): string {
  const where = machine ? `${machine}:${path}` : path;
  const url = `ir:folder/${where.replace(/[<>\n]/g, "")}`;
  return `[${sayable(name) || "/"}](${/[\s()]/.test(url) ? `<${url}>` : url})`;
}

const ATTR = /([\w:-]+)\s*=\s*(?:"([^"]*)"|'([^']*)')/g;
const unescape = (v: string) => v.replace(/&(lt|gt|quot|apos|amp);/g, (_, e: string) => ({ lt: "<", gt: ">", quot: '"', apos: "'", amp: "&" })[e]!);
const attrs = (s: string) => Object.fromEntries([...s.matchAll(ATTR)].map((m) => [m[1]!, unescape(m[2] ?? m[3] ?? "")]));
const SATORI = /<(img|image|video|audio|file)\b([^<>]*?)\/?>(?:<\/\1>)?|<sharp\b([^<>]*?)(?:\/>|>([^<>]*)<\/sharp>)|<a\b([^<>]*?)>([^<>]*)<\/a>/g;

/** A reply's Satori elements of Iridium's as the Markdown they stand for (the system prompt lets an
 *  agent write either): <img src="ir:id"/> (image, video, audio) a picture's link, <file
 *  src="ir:id" title/> a file's, <sharp id name/> a session's, <a href="ir:…"> the link. Others,
 *  and any in code, stay as written. */
export function fromSatori(text: string): string {
  if (!text.includes("<")) return text;
  return text
    .split(/(```[\s\S]*?(?:```|$)|`[^`\n]*`)/)
    .map((part, i) =>
      i % 2
        ? part
        : part.replace(SATORI, (all, file?: string, fa?: string, sa?: string, sk?: string, aa?: string, ak?: string) => {
            if (file) {
              const a = attrs(fa ?? "");
              const id = /^ir:([a-z2-7]{10})$/.exec(a.src ?? "")?.[1];
              if (!id) return all;
              const words = (a.title ?? a.alt ?? "").replace(/[[\]\n]/g, " ");
              return file === "file" ? `[${words}](ir:${id})` : `![${words}](ir:${id})`;
            }
            if (sa !== undefined) {
              const a = attrs(sa);
              return a.id && /^[0-9A-Za-z_-]{4,64}$/.test(a.id) ? sessionLink(a.name || unescape(sk ?? "") || a.id, a.id) : all;
            }
            const href = attrs(aa ?? "").href ?? "";
            if (!/^ir:/.test(href)) return all;
            const words = unescape(ak ?? "").replace(/[[\]\n]/g, " ");
            return /[\s()]/.test(href) ? `[${words}](<${href}>)` : `[${words}](${href})`;
          }),
    )
    .join("");
}

/** A place named in words, as the composer sends it: local, a folder here; remote, at the machine
 *  named (its name as written), its path as that machine writes it. */
export type PlaceNamed = { kind: "local" | "remote"; machine?: string; path: string; name: string };

/** The places text names, each once, in order (the chat's 在哪干活). */
export function placesIn(text: string): PlaceNamed[] {
  const out: PlaceNamed[] = [];
  for (const p of mentions(text)) {
    if (typeof p === "string" || p.kind !== "place") continue;
    const x: PlaceNamed = p.machine ? { kind: "remote", machine: p.machine, path: p.path, name: p.name } : { kind: "local", path: p.path, name: p.name };
    if (!out.some((o) => o.kind === x.kind && o.machine?.toLowerCase() === x.machine?.toLowerCase() && o.path === x.path)) out.push(x);
  }
  return out;
}

/** The slot a mention's mark (an agent's logo, a folder) is drawn in, in the composer's box. */
export const MARK = "\u2003";

/** The composer's words as its box holds them (the user, 2026-11-12): each mention its mark's slot
 *  and its name, the link behind it kept aside. refs: where each is, in value (from, to) and in
 *  the words (at, end). */
export type Boxed = { value: string; refs: { from: number; to: number; at: number; end: number; m: Mention }[] };

export function boxed(words: string): Boxed {
  let value = "";
  let at = 0;
  const refs: Boxed["refs"] = [];
  for (const p of mentions(words)) {
    if (typeof p === "string") {
      value += p;
      at += p.length;
      continue;
    }
    const shown = MARK + (p.kind === "session" ? p.title : p.name);
    refs.push({ from: value.length, to: value.length + shown.length, at, end: at + p.text.length, m: p });
    value += shown;
    at += p.text.length;
  }
  return { value, refs };
}

/** Where the box's position i is in the words (inside a mention: its start). */
export function wordsAt(b: Boxed, i: number): number {
  let d = 0;
  for (const r of b.refs) {
    if (r.from >= i) break;
    if (r.to > i) return r.at;
    d += r.end - r.at - (r.to - r.from);
  }
  return i + d;
}

/** Where the words' position i is in the box (inside a link: its start). */
export function boxAt(b: Boxed, i: number): number {
  let d = 0;
  for (const r of b.refs) {
    if (r.at >= i) break;
    if (r.end > i) return r.from;
    d += r.end - r.at - (r.to - r.from);
  }
  return i - d;
}

/** The words after the box was edited from what boxed(words) gave to value: a mention the edit
 *  touches goes whole (one Backspace takes it), letters typed inside one go after it. caret: where
 *  the box's caret then is. at: the box's caret after the edit, which settles where letters
 *  like their neighbours went (an a typed before an a). */
export function edited(words: string, value: string, at?: number): { words: string; caret: number } {
  const b = boxed(words);
  const was = b.value;
  // What follows the caret is what was there; then what precedes the change.
  let s = 0;
  while (s < was.length && s < value.length - (at ?? 0) && was[was.length - 1 - s] === value[value.length - 1 - s]) s++;
  let p = 0;
  while (p < was.length - s && p < value.length - s && was[p] === value[p]) p++;
  const typed = value.slice(p, value.length - s);
  let lo = p, hi = was.length - s;
  for (const r of b.refs) {
    if (lo === hi) {
      if (r.from < lo && lo < r.to) lo = hi = r.to;
    } else if (r.from < hi && r.to > lo) {
      lo = Math.min(lo, r.from);
      hi = Math.max(hi, r.to);
    }
  }
  const head = words.slice(0, wordsAt(b, lo)) + typed;
  return { words: head + words.slice(wordsAt(b, hi)), caret: boxed(head).value.length };
}

/** The caret moved into a mention's name: to its nearer edge. */
export function outOf(b: Boxed, i: number): number {
  const r = b.refs.find((r) => r.from < i && i < r.to);
  return r ? (i - r.from < r.to - i ? r.from : r.to) : i;
}

const LOCAL = (u: string) => !/^[a-z][a-z0-9+.-]*:/i.test(u) || u.startsWith("file:");
const FILE_LINE = /^[ \t]*\[(文件|附件)\] `([^`\n]+)`[ \t]*$/gm;

/** A message's text as the page shows it beside its files (docs/api.md "认文件"): each line
 *  [文件] `path` (and [附件] `path` in what you sent) that gave a file the server found, by its
 *  absolute path in paths, left out, the file showing as itself; a picture of a local path, which
 *  the page cannot load, as that path in code. Fenced code stays as it is. */
export function withoutFiles(text: string, paths: ReadonlySet<string>, home?: string): string {
  const parts = text.split(/(```[\s\S]*?(?:```|$))/);
  const out = parts.map((part) => {
    if (part.startsWith("```")) return part;
    let p = part.replace(FILE_LINE, (all, _word: string, raw: string) => {
      const x = raw.trim();
      return paths.has(x.startsWith("~/") && home ? home + x.slice(1) : x) ? "" : all;
    });
    p = p.replace(/!\[([^\]]*)\]\(<?([^)\s>]+)>?\)/g, (all, _alt: string, path: string) => (LOCAL(path) ? `\`${path.replace(/^file:\/\//, "")}\`` : all));
    return p;
  });
  return out.join("").replace(/\n{3,}/g, "\n\n").trim();
}

/** The files a queued message has (lines [附件] `path`, attach.go) and its words without them. */
export function attachedOf(text: string): { paths: string[]; words: string } {
  const paths = [...text.matchAll(FILE_LINE)].filter((m) => m[1] === "附件").map((m) => m[2]!.trim());
  return { paths, words: paths.length ? withoutFiles(text, new Set(paths)) : text };
}

/** A session's id, or its start (8 characters at least, as ir prints it), standing alone in text:
 *  not inside a path, a word or a longer hash. */
export const SID = /(?<![\w/.-])[0-9a-f]{8}(?:-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}|[0-9a-f]{0,24})(?![\w/-]|\.\w)/g;

/** text cut where it names a session of list by its id or the start of one: the parts that do as
 *  {id, s}. A start more than one session has, or none, is text. */
export function sessionIds<S extends { id: string }>(text: string, list: readonly S[]): (string | { id: string; s: S })[] {
  const out: (string | { id: string; s: S })[] = [];
  let last = 0;
  for (const m of text.matchAll(new RegExp(SID))) { // a copy: matchAll starts at the pattern's lastIndex
    const s = sessionOf(m[0], list);
    if (!s) continue;
    if (m.index > last) out.push(text.slice(last, m.index));
    out.push({ id: m[0], s });
    last = m.index + m[0].length;
  }
  if (last < text.length) out.push(text.slice(last));
  return out;
}

/** The one session of list whose id starts with id, if just one. */
export function sessionOf<S extends { id: string }>(id: string, list: readonly S[]): S | undefined {
  let found: S | undefined;
  for (const s of list) {
    if (!s.id.startsWith(id)) continue;
    if (found) return undefined;
    found = s;
  }
  return found;
}

const NAME_LINE = /^[ \t]*\[标题\].*(?:\n|$)/gm;

/** A reply without its title lines (plan "会话标题"): a line [标题] text the agent ends a reply
 *  with names the session, it is not said. Fenced code stays as it is. */
export function withoutNameLines(text: string): string {
  if (!text.includes("[标题]")) return text;
  return text
    .split(/(```[\s\S]*?(?:```|$))/)
    .map((part) => (part.startsWith("```") ? part : part.replace(NAME_LINE, "")))
    .join("")
    .trimEnd();
}

/** A draft as it is shown (chat/markdown.tsx Draft): without its title lines, as it will be once
 *  whole, so the chat does not shrink by a line as it lands; while words still come (live), a
 *  last line that may yet become one ("[", "[标"…) is held back too. */
export function draftWords(text: string, live: boolean): string {
  let out = withoutNameLines(text);
  if (!live) return out;
  const at = out.lastIndexOf("\n") + 1;
  const last = out.slice(at).trimStart();
  const fenced = (out.match(/^ {0,3}```/gm)?.length ?? 0) % 2 === 1;
  if (last && "[标题]".startsWith(last) && !fenced) out = out.slice(0, at).trimEnd();
  return out;
}

/** The @word the caret ends, to name a session by (the composer's @ list): where its @ is and what
 *  follows it; none unless the @ starts the words or follows a space. */
export function atWord(text: string, caret: number): { from: number; q: string } | null {
  const m = /(?:^|\s)@([^\s@]*)$/.exec(text.slice(0, caret));
  return m ? { from: caret - m[1]!.length - 1, q: m[1]! } : null;
}

/** The sessions @q may name, at most n: not here, not a subagent, not archived; titles that start
 *  with it first, then titles, folders, agents and ids that have it; the newest first in each. */
export function atSessions(list: Session[], q: string, here: string, n = 8): Session[] {
  const w = q.toLowerCase();
  const rank = (s: Session) => {
    const title = titleOf(s).toLowerCase();
    if (!w || title.startsWith(w)) return 0;
    if (title.includes(w)) return 1;
    if (s.cwd.toLowerCase().includes(w) || s.agent.includes(w) || s.id.startsWith(w)) return 2;
    return 3;
  };
  return list
    .filter((s) => s.id !== here && !s.subagent && !s.archived)
    .map((s) => [rank(s), s] as const)
    .filter(([r]) => r < 3)
    .sort((a, b) => a[0] - b[0] || b[1].updatedAt - a[1].updatedAt)
    .slice(0, n)
    .map(([, s]) => s);
}
