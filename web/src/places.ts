// A place (a folder at this machine or at another, docs/plan.md "@"): pressed where it is named (a
// card in a chat, the chat's 在哪干活), it is shown in the list: its group opened and scrolled to, as
// a listed group holds it (sidebar.tsx); else the top "+" opens there (create.tsx). Nothing is kept.

/** machine: its ID, none for this machine; path: '/'-separated, as the page writes paths. */
export type Place = { machine?: string; path: string };

const opens = new Set<(p: Place) => void>();
const creates = new Set<(p: Place) => void>();

/** The place shown in the list, or the "+" opened there when no listed group holds it. */
export function openPlace(p: Place): void {
  if (opens.size) opens.forEach((f) => f(p));
  else createAt(p);
}

/** The top "+" opened at the place's machine, in its folder. */
export function createAt(p: Place): void {
  creates.forEach((f) => f(p));
}

/** The list's: shows a place (openPlace). Returns what stops it. */
export function onOpenPlace(fn: (p: Place) => void): () => void {
  opens.add(fn);
  return () => void opens.delete(fn);
}

/** The top "+"'s: opens at a place (createAt). Returns what stops it. */
export function onCreateAt(fn: (p: Place) => void): () => void {
  creates.add(fn);
  return () => void creates.delete(fn);
}

// ---- writing a place: "<app name>:<path>", the host "host:" (internal/place, the same cases) ----

/** The host's reserved names; host is how a place is written out. */
export const HOST_NAMES = ["host", "主机", "本机"];
export const isHostName = (name: string) => HOST_NAMES.some((h) => h.toLowerCase() === name.toLowerCase());

/** s cut at its first colon into an app's name and the path there; none if it has no prefix: no
 *  colon, nothing before it, a single ASCII letter (a Windows drive), a slash, backslash or leading ~. */
export function splitPlace(s: string): { name: string; path: string } | null {
  const i = s.indexOf(":");
  if (i <= 0) return null;
  const name = s.slice(0, i);
  if (/^[A-Za-z]$/.test(name) || /[/\\]/.test(name) || name.startsWith("~")) return null;
  return { name, path: s.slice(i + 1) };
}

/** s resolved: its prefix by lookup (a name to its app's ID), the host by its names; no prefix,
 *  self (the caller's own: none for the host). null: the prefix names no app. */
export function parsePlace(s: string, self: string | undefined, lookup: (name: string) => string | undefined): Place | null {
  const cut = splitPlace(s);
  if (!cut) return self ? { machine: self, path: s } : { path: s };
  if (isHostName(cut.name)) return { path: cut.path };
  const id = lookup(cut.name);
  return id === undefined ? null : { machine: id, path: cut.path };
}

/** p written out, always prefixed: its app's name (its ID if none), or the host's (host). */
export function formatPlace(p: Place, nameOf: (id: string) => string | undefined, host = "host"): string {
  if (!p.machine) return `${host}:${p.path}`;
  return `${nameOf(p.machine) || p.machine}:${p.path}`;
}

/** p as stored (a group's path): "<app ID>:<path with />", the host's its path alone. */
export const placeKey = (p: Place) => (p.machine ? `${p.machine}:${p.path.replaceAll("\\", "/")}` : p.path);

/** The place stored as k. */
export function placeFromKey(k: string): Place {
  if (k.startsWith("/") || k.startsWith("~")) return { path: k };
  const i = k.indexOf(":");
  return i > 0 ? { machine: k.slice(0, i), path: k.slice(i + 1) } : { path: k };
}
