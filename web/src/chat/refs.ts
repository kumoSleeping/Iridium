// Two readings of what a chat shows, pure for tests: who a message of the user's is from (plan
// "消息来源"), and the files a reply names (plan "网页": `![](路径)`, `[名](路径)` and absolute paths
// in inline code).
import type { Agent, Device, Session, Terminal } from "../api/types.gen";
import { titleOf } from "../ui";

/** A user block's source as the page draws it (QQ-like: a face, a name, a badge on the face):
 *  your words (typed here, on another panel, or in a terminal: that terminal's name, avatar and
 *  device), a letter (from another session's ir: its agent and session; from a plugin or peer), or
 *  Iridium's own line. local: 本机 (the machine's terminal, or this page with no gate). */
export type Source =
  | { kind: "you"; name: string; avatar?: string; device: Device; local?: boolean }
  | { kind: "letter"; name: string; avatar?: string; channel: boolean; agent?: Agent; session?: string; workflow?: boolean; where?: string }
  | { kind: "iridium" };

/** What sourceOf needs to know of a terminal. */
export type Who = Pick<Terminal, "id" | "name" | "avatar" | "device" | "local">;

const LOCAL_TERMINAL: Who = { id: "local", name: "本机", local: true };

/** Your words from terminal x. */
export const yours = (x: Who): Extract<Source, { kind: "you" }> => ({ kind: "you", name: x.name, avatar: x.avatar, device: x.device ?? "computer", ...(x.local ? { local: true } : {}) });

/** Who wrote a message: from, the tag Iridium wrote it with, and terminal, the sending terminal's ID
 *  (Block.from, fromTerminal). me: this panel ("local" with no gate). An agent's ir from another
 *  session is tagged "ir:<session>"; a block with no tag was typed in the agent's own terminal, on
 *  the machine (本机) — except in a subagent, where it is its parent's prompt (of: the session shown,
 *  whose workspace a sender's name leaves out). */
export function sourceOf(from: string | undefined, terminal: string | undefined, me: Who, terminals: Terminal[], sessions: Session[], workspaces: { id: number; name: string }[] = [], of?: Pick<Session, "subagent" | "parent" | "workflow" | "workspace">): Source {
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
  const local = terminals.find((x) => x.local) ?? (me.local ? me : LOCAL_TERMINAL);
  if (!from) return yours(local);
  if ((terminal ?? "") === me.id) return yours(me);
  const e = terminals.find((x) => x.id === terminal) ?? terminals.find((x) => x.name.toLowerCase() === from.toLowerCase());
  if (e?.id === me.id) return yours(me);
  // Another panel, or 本机 (this machine: its page with no gate, a person's ir): your words too.
  if (e?.kind === "panel" || e?.local) return yours(e);
  if (!e && (terminal === "local" || from === "本机")) return yours(local);
  // A plugin writes "[name·sender]": the plugin's avatar, the whole tag as the name.
  return { kind: "letter", name: from, avatar: e?.avatar, channel: true };
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

/** A session named in a message, as a row dropped on the composer writes it (i18n
 *  composer.mention): 「title」（Claude 会话 1a2b3c4d）, or "title" (Claude session 1a2b3c4d). A
 *  place (composer.mentionFolder, composer.mentionWorkspace, docs/plan.md "@"): a folder here,
 *  文件夹「ir」（/home/otae/ir）, or one at another machine by its name, its path as it writes it,
 *  工作区「Ds_Benchmark」（otae-2:C:\Code\Ds_Benchmark）; folder "ir" (/home/otae/ir) and workspace
 *  "Ds_Benchmark" (otae-2:C:\Code\Ds_Benchmark) in English. machine: none for this machine. */
export type Mention =
  | { kind: "session"; title: string; agent: string; id: string; text: string }
  | { kind: "place"; name: string; machine?: string; path: string; text: string };
// A place's path may hold brackets: it ends at the last closing one before a space or the next
// opening one (2026-11-10), so one after another, and words after, still part.
const ZH_PATH = "([^\\n]{1,400}?)）(?![^\\s（]*）)";
const EN_PATH = "([^\\n]{1,400}?)\\)(?![^\\s(]*\\))";
const MACHINE = "([^\\s:「」（）\"()]{1,64}):";
const MENTION = new RegExp(
  [
    `文件夹「([^」\\n]{1,200})」（(?=/)${ZH_PATH}`,
    `工作区「([^」\\n]{1,200})」（${MACHINE}${ZH_PATH}`,
    `folder "([^"\\n]{1,200})" \\((?=/)${EN_PATH}`,
    `workspace "([^"\\n]{1,200})" \\(${MACHINE}${EN_PATH}`,
    "「([^」\\n]{1,200})」（([^（）\\n]{1,40}?) 会话 ([0-9A-Za-z_-]{8,64})）",
    '"([^"\\n]{1,200})" \\(([^()\\n]{1,40}?) session ([0-9A-Za-z_-]{8,64})\\)',
  ].join("|"),
  "g",
);

/** The mention a match of MENTION is, by which of its forms matched. */
function mentionOf(m: RegExpMatchArray): Mention {
  const g = m as unknown as (string | undefined)[];
  const text = m[0];
  if (g[1] !== undefined) return { kind: "place", name: g[1], path: g[2]!, text };
  if (g[3] !== undefined) return { kind: "place", name: g[3], machine: g[4]!, path: g[5]!, text };
  if (g[6] !== undefined) return { kind: "place", name: g[6], path: g[7]!, text };
  if (g[8] !== undefined) return { kind: "place", name: g[8], machine: g[9]!, path: g[10]!, text };
  return { kind: "session", title: g[11] ?? g[14]!, agent: g[12] ?? g[15]!, id: g[13] ?? g[16]!, text };
}

/** A message's text cut at the sessions and places it names: plain runs and mentions, in order. */
export function mentions(text: string): (string | Mention)[] {
  const out: (string | Mention)[] = [];
  let at = 0;
  for (const m of text.matchAll(MENTION)) {
    if (m.index > at) out.push(text.slice(at, m.index));
    out.push(mentionOf(m));
    at = m.index + m[0].length;
  }
  if (at < text.length) out.push(text.slice(at));
  return out;
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

/** A message's text split as the composer put it together: the sessions named before the words
 *  (cards there, as they were in the composer), the words after them. */
export function leadingMentions(text: string): { named: Mention[]; words: string } {
  const named: Mention[] = [];
  const parts = mentions(text);
  let i = 0;
  for (; i < parts.length; i++) {
    const p = parts[i]!;
    if (typeof p !== "string") named.push(p);
    else if (p.trim()) break;
  }
  if (!named.length) return { named, words: text };
  return { named, words: parts.slice(i).map((p) => (typeof p === "string" ? p : p.text)).join("").trimStart() };
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
