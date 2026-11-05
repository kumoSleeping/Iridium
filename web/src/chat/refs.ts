// Two readings of what a chat shows, pure for tests: who a message of the user's is from (plan
// "消息来源"), and the files a reply names (plan "网页": `![](路径)`, `[名](路径)` and absolute paths
// in inline code).
import type { Endpoint } from "../api/assumed";
import type { Agent, Session } from "../api/types.gen";

/** A user block's source as the page draws it: your words (typed here, on another panel, or in a
 *  terminal), a letter (from another session's ir, a plugin, a peer), or Iridium's own line. */
export type Source =
  | { kind: "you"; panel?: { name: string; avatar?: string } }
  | { kind: "letter"; name: string; avatar?: string; channel: boolean; agent?: Agent; session?: string }
  | { kind: "iridium" };

/** mine: the names this panel writes as ("网页" while there is no gate). */
export function sourceOf(from: string | undefined, mine: string[], endpoints: Endpoint[], sessions: Session[]): Source {
  if (!from || mine.includes(from)) return { kind: "you" };
  if (from === "Iridium") return { kind: "iridium" };
  const e = endpoints.find((e) => e.name.toLowerCase() === from.toLowerCase());
  if (e?.kind === "panel") return { kind: "you", panel: { name: e.name, avatar: e.avatar } };
  // Another session's ir: the session by id or name, its agent's logo, a key to go there.
  const id = /^(?:ir|session):(.+)$/.exec(from)?.[1];
  const s = sessions.find((s) => s.id === (id ?? e?.session) || (!id && !e && s.name === from));
  if (s) return { kind: "letter", name: sessionName(s), channel: false, agent: s.agent, session: s.id };
  return { kind: "letter", name: from, avatar: e?.avatar, channel: true };
}

const sessionName = (s: Session) => s.name || s.title || s.cwd.split("/").pop() || s.id.slice(0, 8);

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
