// Two readings of what a chat shows, pure for tests: who a message of the user's is from (plan
// "消息来源"), and the files a reply names (plan "网页": `![](路径)`, `[名](路径)` and absolute paths
// in inline code).
import type { Agent, Device, Session } from "../api/types.gen";
import { isHostName, splitPlace } from "../places";
import { titleOf } from "../ui";

/** A user block's source as the page draws it (QQ-like: a face, a name, a badge on the face):
 *  your words (typed here, on another panel, or in a terminal: that terminal's name, avatar and
 *  device), a letter (from another session's ir: its agent and session; from a plugin or peer), or
 *  Iridium's own line. local: 本机 (the machine's terminal, or this page with no gate). */
export type Source =
  | { kind: "you"; name: string; avatar?: string; device: Device; local?: boolean }
  | { kind: "letter"; name: string; avatar?: string; channel: boolean; type?: string; agent?: Agent; session?: string; face?: string; workflow?: boolean; where?: string }
  | { kind: "iridium" };

/** What sourceOf needs to know of who sent a message: an app, a login of ir_web (web), or the host. */
export type Who = { id: string; name: string; avatar?: string; device?: Device; host?: boolean; named?: boolean; web?: boolean; type?: string };

const HOST: Who = { id: "local", name: "Iridium", host: true };

/** Your words from terminal x. The host is drawn as Iridium (local) unless it has a name and face
 *  of its own (named: the desktop app's user, kumo 2026-11-22). */
export const yours = (x: Who): Extract<Source, { kind: "you" }> => ({ kind: "you", name: x.name, avatar: x.avatar, device: x.device ?? "computer", ...(x.host && !x.named ? { local: true } : {}) });

/** Who wrote a message: from, the tag Iridium wrote it with, and terminal, the sending terminal's ID
 *  (Block.from, fromTerminal). me: this panel ("local" with no gate). An agent's ir from another
 *  session is tagged "ir:<session>"; a block with no tag was typed in the agent's own terminal, on
 *  the machine (本机) — except in a subagent, where it is its parent's prompt (of: the session shown,
 *  whose workspace a sender's name leaves out). */
export function sourceOf(from: string | undefined, terminal: string | undefined, me: Who, terminals: Who[], sessions: Session[], workspaces: { id: number; name: string }[] = [], of?: Pick<Session, "subagent" | "parent" | "workflow" | "workspace">): Source {
  const here = of ? (of.workspace ?? 0) : undefined; // 0: 未归类 (the field left out)
  // Iridium's own words by their terminal: the host's have the tag Iridium too.
  if (from === "Iridium" && terminal === "Iridium") return { kind: "iridium" };
  // A subagent's prompt is written by the session that started it (an Agent tool call, a workflow).
  if (!from && of?.subagent) {
    const p = of.parent ? sessions.find((s) => s.id === of.parent) : undefined;
    const wf = of.workflow ? { workflow: true } : {};
    const name = p ? senderName(p, workspaces, here) : (of.parent?.slice(0, 8) ?? "");
    const named = of.workflow?.name ? (name ? `${name} · ${of.workflow.name}` : of.workflow.name) : name;
    return { kind: "letter", name: named, channel: false, ...(p ? { agent: p.agent, session: p.id, where: p.cwd } : of.parent ? { session: of.parent } : {}), ...wf };
  }
  // Another session's ir: that session, its face, a key to go there.
  const id = from && /^(?:ir|session):(.+)$/.exec(from)?.[1];
  if (id) {
    const s = sessions.find((s) => s.id === id || s.id.startsWith(id));
    if (!s) return { kind: "letter", name: id.slice(0, 8), channel: false };
    const solo = s.bare && s.agents?.length === 1 ? s.agents[0] : undefined;
    const host = s.spawned?.channel ? sessions.find((x) => x.id === s.spawned!.channel) : undefined;
    const named = !s.name && !s.title && host ? host : s;
    const agent = s.agent || (solo ? (sessions.find((x) => x.id === solo.session)?.agent ?? solo.agent) : undefined);
    const jump = host?.bare && host.agents?.length === 1 ? host.id : s.id;
    const face = solo ? solo.session : s.id;
    return { kind: "letter", name: senderName(named, workspaces, here), channel: false, ...(agent ? { agent } : {}), session: jump, ...(face !== jump ? { face } : {}), where: s.cwd };
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
  if (!e && (terminal === "local" || from === "Iridium" || from === "主机" || from === "本机")) return yours(local);
  // A plugin writes "[name·sender]": the plugin's avatar, the whole tag as the name.
  return { kind: "letter", name: from, avatar: e?.avatar, channel: true, ...(e?.type ? { type: e.type } : {}) };
}

/** src with the device its words were sent from (a browser's passkey's), when it is a person's
 *  and that is known: the badge on the face is that device, not the one the login is on now. */
export const withDevice = (src: Source, device?: Device): Source => (device && src.kind === "you" && !src.local ? { ...src, device } : src);

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
  // Local POSIX, UNC, or Windows drive paths before checking for a "<machine>:<path>" prefix.
  if (where.startsWith("/") || where.startsWith("\\\\") || /^[A-Za-z]:[/\\]/.test(where)) {
    return { kind: "place", name: label, path: where, text };
  }
  const place = splitPlace(where);
  if (!place?.path) return undefined;
  return { kind: "place", name: label, ...(!isHostName(place.name) && { machine: place.name }), path: place.path, text };
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

/** Satori's elements Markdown has its own way for, or none (the user, 2026-11-13: either may be
 *  written, Iridium reads both): <at> as "@name" (Markdown has no at; <at type="all"/>
 *  @全体成员, <at role="agent"/> @全体Agent, an agent's by nick), <img src> (any src) a picture,
 *  <a href> a link, <b> <strong> <i> <em> <s> <del> <code> <br/> and <quote> (with words) as
 *  Markdown writes them, <quote id/> (what a message answers, shown apart) and <u> <spl> left
 *  as their words. In code they stay as written. */
const MORE = /<at\b([^<>]*?)(?:\/>|>([^<>]*)<\/at>)|<img\b([^<>]*?)\/?>(?:<\/img>)?|<a\b([^<>]*?)>([^<>]*)<\/a>|<(b|strong|i|em|s|del|code|u|ins|spl)>([^<>]*)<\/\6>|<br\s*\/?>|<quote\b[^<>]*\/>|<quote>([^<>]*)<\/quote>/g;
const WRAP: Record<string, string> = { b: "**", strong: "**", i: "*", em: "*", s: "~~", del: "~~", code: "`" };

function moreSatori(part: string, nick?: (session: string) => string | undefined): string {
  return part.replace(MORE, (all, at?: string, atWords?: string, img?: string, aa?: string, ak?: string, tag?: string, inner?: string, quote?: string) => {
    if (at !== undefined) {
      const a = attrs(at);
      if (a.type === "all") return "@全体成员";
      if (a.role === "agent") return "@全体Agent";
      const n = a.id?.startsWith("agent:") ? nick?.(a.id.slice(6)) : undefined;
      return `@${n || a.name || unescape(atWords ?? "") || a.id || ""}`;
    }
    if (img !== undefined) {
      const a = attrs(img);
      return a.src ? `![${(a.title ?? a.alt ?? "").replace(/[[\]\n]/g, " ")}](${/[\s()]/.test(a.src) ? `<${a.src}>` : a.src})` : all;
    }
    if (aa !== undefined) {
      const href = attrs(aa).href ?? "";
      const words = unescape(ak ?? "").replace(/[[\]\n]/g, " ");
      return href ? `[${words || href}](${/[\s()]/.test(href) ? `<${href}>` : href})` : words;
    }
    if (tag !== undefined) return `${WRAP[tag] ?? ""}${unescape(inner ?? "")}${WRAP[tag] ?? ""}`;
    if (all.startsWith("<br")) return "\n";
    if (quote !== undefined) return unescape(quote).split("\n").map((l) => `> ${l}`).join("\n") + "\n";
    return ""; // <quote id/>
  });
}

/** A reply's Satori elements of Iridium's as the Markdown they stand for (the system prompt lets an
 *  agent write either): <img src="ir:id"/> (image, video, audio) a picture's link, <file
 *  src="ir:id" title/> a file's, <sharp id name/> a session's, <a href="ir:…"> the link; then
 *  the rest Markdown has a way for (moreSatori). Any in code stay as written. nick: an agent's
 *  nick by its session, for <at id="agent:…"/>. */
export function fromSatori(text: string, nick?: (session: string) => string | undefined): string {
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
    .map((part, i) => (i % 2 ? part : moreSatori(part, nick)))
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

/** The @word the caret ends, to name a member or a session by (the composer's lists): where its
 *  mark is and what follows it; none after what an address or a path has in it. */
export function atWord(text: string, caret: number, mark: "@" | "&" = "@"): { from: number; q: string } | null {
  // "你好@" opens it as "你好 @" does (kumo, 2026-11-16), and "Todo@" too (kumo, 2026-11-27);
  // "x.y@" and "a/@" do not. A name picked gets a space before it (insertNick), as the server's @
  // wants. & stays shut inside a Latin word ("A&B").
  const m = (mark === "@" ? /(?:^|[^_.\-@/])@([^\s@]*)$/ : /(?:^|[^A-Za-z0-9_.\-&/])&([^\s&]*)$/).exec(text.slice(0, caret));
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
