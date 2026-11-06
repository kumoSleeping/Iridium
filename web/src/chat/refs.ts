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
 *  name, else the opening of its first message, else its folder. Not its agent: its face is the
 *  agent's logo. Its workspace only when it is not here, the workspace of the chat shown (none
 *  known: none added): "Run the holder's tests · KDJ". */
export function senderName(s: Pick<Session, "id" | "name" | "title" | "cwd" | "workspace">, workspaces: { id: number; name: string }[] = [], here?: number): string {
  const name = titleOf(s) || s.id.slice(0, 8);
  if (here === undefined || (s.workspace ?? 0) === (here ?? 0)) return name;
  const where = (s.workspace && workspaces.find((w) => w.id === s.workspace)?.name) || s.cwd.split("/").filter(Boolean).pop();
  return where ? `${name} · ${where}` : name;
}

/** The type a session's row is dragged with (sidebar.tsx), its id the data. */
export const SESSION_DRAG = "application/x-iridium-session";

/** The shortest start of id, at least 8 characters, no other of ids shares: what ir takes for a
 *  session (internal/cli short). */
export function shortId(id: string, ids: string[]): string {
  for (let n = 8; n < id.length; n++) if (!ids.some((o) => o !== id && o.startsWith(id.slice(0, n)))) return id.slice(0, n);
  return id;
}

/** A path as a message writes it, made absolute the way the server reads it (docs/api.md "认文件"):
 *  file:// and a #line or :line:col dropped, ~/ the home folder, the rest from the session's folder. */
export function resolve(p: string, cwd: string, home?: string): string {
  let x = p.replace(/^file:\/\//, "").replace(/#.*$/, "").replace(/(:\d+){1,2}$/, "");
  if (x.startsWith("~/") && home) x = home + x.slice(1);
  else if (!x.startsWith("/")) x = `${cwd.replace(/\/+$/, "")}/${x.replace(/^\.\//, "")}`;
  return x;
}

const LOCAL = (u: string) => !/^[a-z][a-z0-9+.-]*:/i.test(u) || u.startsWith("file:");

/** A message's text as the page shows it beside its files: pictures taken out (they show under it,
 *  from the server's list), file links left as their names. labels: what a link called each file,
 *  by its absolute path. Fenced code stays as it is. */
export function filesIn(text: string, cwd = "", home?: string): { text: string; labels: Map<string, string> } {
  const labels = new Map<string, string>();
  const parts = text.split(/(```[\s\S]*?(?:```|$))/);
  const out = parts.map((part) => {
    if (part.startsWith("```")) return part;
    let p = part.replace(/!\[([^\]]*)\]\(<?([^)\s>]+)>?\)/g, (all, _alt: string, path: string) => (LOCAL(path) ? "" : all));
    p = p.replace(/(?<!!)\[([^\]]+)\]\(<?([^)\s>]+)>?\)/g, (all, label: string, path: string) => (LOCAL(path) ? (labels.set(resolve(path, cwd, home), label), label) : all));
    return p;
  });
  return { text: out.join("").replace(/\n{3,}/g, "\n\n").trim(), labels };
}
