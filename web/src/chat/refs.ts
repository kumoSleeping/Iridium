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

export type FileRef = { path: string; image: boolean; label?: string };

const IMAGE = /\.(png|jpe?g|gif|webp|avif|bmp|svg)$/i;
export const VIDEO = /\.(mp4|webm|mov|m4v|ogv)$/i;
export const AUDIO = /\.(mp3|wav|ogg|oga|m4a|flac|aac|opus)$/i;
const ABS = /^(?:\/|~\/)[^\s<>"'`]*[^\s<>"'`.,;:!?)\]]$/;

/** The files a reply names, in order, once each; and its text with image references taken out
 *  (they show as thumbnails under it) and file links left as their names. */
export function filesIn(text: string): { refs: FileRef[]; text: string } {
  const refs: FileRef[] = [];
  const add = (path: string, image: boolean, label?: string) => {
    if (!ABS.test(path) || refs.some((r) => r.path === path)) return;
    refs.push({ path, image: image || IMAGE.test(path), label });
  };
  // Fenced code is code: nothing in it is a file to show.
  const parts = text.split(/(```[\s\S]*?(?:```|$))/);
  const out = parts.map((part) => {
    if (part.startsWith("```")) return part;
    let p = part.replace(/!\[([^\]]*)\]\(<?([^)\s>]+)>?\)/g, (all, alt: string, path: string) => (ABS.test(path) ? (add(path, true, alt || undefined), "") : all));
    p = p.replace(/(?<!!)\[([^\]]+)\]\(<?([^)\s>]+)>?\)/g, (all, label: string, path: string) => (ABS.test(path) ? (add(path, false, label), label) : all));
    for (const m of p.matchAll(/`([^`\n]+)`/g)) if (ABS.test(m[1]!)) add(m[1]!, false);
    return p;
  });
  return { refs, text: out.join("").replace(/\n{3,}/g, "\n\n").trim() };
}
