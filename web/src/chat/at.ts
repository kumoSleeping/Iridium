// What @ may name in the composer (docs/plan.md "@"), pure for tests: the sessions, this machine's
// groups, each machine's workspaces and groups listed there; a path typed after the @ (/…, ~/…,
// <machine>:…), that folder and the folders in it. A name is only a reference: it delivers
// nothing, notifies no one, grants nothing.
import type { DshWorkspace, Session, Workspace } from "../api/types.gen";
import type { Machine } from "../machines";
import { homeAt } from "../machines";
import { isHostName, splitPlace, type Place } from "../places";
import { listedRemote, machineOf, remotePath, standing } from "../tree";
import { tilde } from "../ui";
import { atSessions } from "./refs";

/** A place named: machine its ID (none for this machine), path '/'-separated, name as it is said. */
export type NamedPlace = Place & { name: string };
/** The place a group's or a folder's row dragged here is (PLACE_DRAG's data: {path, name}, path the
 *  page's); none if it is not one. */
export function placeDropped(data: string): NamedPlace | undefined {
  try {
    const v = JSON.parse(data) as { path?: unknown; name?: unknown };
    if (typeof v.path !== "string" || !v.path) return undefined;
    const mid = machineOf(v.path);
    const path = mid ? v.path.slice(mid.length + 1) : v.path;
    return { ...(mid ? { machine: mid } : {}), path, name: typeof v.name === "string" && v.name ? v.name : baseName(path) };
  } catch {
    return undefined;
  }
}

/** A path's last folder: its name ("C:" for a drive, "/" for the root). */
export const baseName = (path: string) => path.replace(/(.)\/+$/, "$1").split("/").pop() || path;

/** A '/'-separated path as machine m writes it (none: this machine's, as it is). */
export const nativeAt = (m: Pick<Machine, "sep"> | undefined, path: string) => (m?.sep === "\\" ? path.replaceAll("/", "\\") : path);

/** A head of the list: 会话, 本机, a machine (its face, name, lamp), a typed path at a machine. */
export type AtHead = { kind: "sessions" } | { kind: "local" } | { kind: "machine"; m: Machine } | { kind: "path"; m?: Machine; path: string };
/** A row: a session, or a place (hint: its path, faint at the end; self: the folder a typed path
 *  is, drawn by its name alone). off: drawn faint, not to be picked. */
export type AtRow = { key: string; off?: boolean } & ({ kind: "session"; s: Session } | { kind: "place"; p: NamedPlace; hint?: string; self?: boolean });
export type AtSection = { key: string; head: AtHead; rows: AtRow[]; off?: boolean };

/** What the list is drawn from. dsh: each machine's DSH workspaces, as asked for (by machine ID). */
export type AtData = { list: Session[]; here: string; workspaces: Workspace[]; machines: Machine[]; dsh: Record<string, DshWorkspace[] | undefined> };

/** Places q finds: names that start with it first, then names, then paths that have it; as they were
 *  given in each. */
function rankPlaces<T extends { p: NamedPlace; hint?: string }>(xs: T[], q: string): T[] {
  const w = q.toLowerCase();
  if (!w) return xs;
  const rank = (x: T) => {
    const n = x.p.name.toLowerCase();
    if (n.startsWith(w)) return 0;
    if (n.includes(w)) return 1;
    if (x.p.path.toLowerCase().includes(w) || (x.hint ?? "").toLowerCase().includes(w)) return 2;
    return 3;
  };
  return xs
    .map((x) => [rank(x), x] as const)
    .filter(([r]) => r < 3)
    .sort((a, b) => a[0] - b[0])
    .map(([, x]) => x);
}

/** The list for @q (no path typed): 会话, the newest four not here (more as q narrows them), those
 *  listed only; 本机, this machine's groups, the latest at work first; then each machine, its DSH
 *  workspaces and its groups listed, one row a path. A machine away: faint, none to pick. Sections
 *  q leaves empty are left out. */
export function atSections(d: AtData, q: string): AtSection[] {
  const st = standing(d.list, d.workspaces);
  const listed = new Set([...st.rows, ...[...st.hung].filter(([at]) => st.rows.has(at)).flatMap(([, ss]) => ss.map((s) => s.id))]);
  const sessions = atSessions(d.list.filter((s) => listed.has(s.id)), q, d.here, q ? 8 : 4);
  const out: AtSection[] = [];
  if (sessions.length) out.push({ key: "sessions", head: { kind: "sessions" }, rows: sessions.map((s) => ({ key: `s:${s.id}`, kind: "session", s })) });
  const latest = new Map<number, number>();
  for (const s of d.list) if (s.workspace) latest.set(s.workspace, Math.max(latest.get(s.workspace) ?? 0, s.updatedAt));
  const local = d.workspaces
    .filter((w) => w.id && !remotePath(w.path))
    .sort((a, b) => (latest.get(b.id) ?? 0) - (latest.get(a.id) ?? 0))
    .map((w) => ({ p: { path: w.path, name: w.name }, hint: tilde(w.path) }));
  const here = rankPlaces(local, q);
  if (here.length) out.push({ key: "local", head: { kind: "local" }, rows: here.map((x): AtRow => ({ key: `p::${x.p.path}`, kind: "place", ...x })) });
  const shown = listedRemote(d.list, d.workspaces);
  for (const m of d.machines) {
    const pre = `${m.id}:`;
    const seen = new Set<string>();
    const rows: { p: NamedPlace; hint: string }[] = [];
    const add = (path: string, name: string) => {
      if (seen.has(path)) return;
      seen.add(path);
      rows.push({ p: { machine: m.id, path, name }, hint: nativeAt(m, path) });
    };
    for (const w of d.workspaces) if (w.path.startsWith(pre) && shown.has(w.path)) add(w.path.slice(pre.length), w.name);
    for (const x of d.dsh[m.id] ?? []) add(x.path, d.workspaces.find((w) => w.path === pre + x.path)?.name || x.title || baseName(x.path));
    const found = rankPlaces(rows, q);
    if (!found.length) continue;
    const off = !m.online;
    out.push({ key: `m:${m.id}`, head: { kind: "machine", m }, off, rows: found.map((x): AtRow => ({ key: `p:${m.id}:${x.p.path}`, kind: "place", ...x, ...(off ? { off } : {}) })) });
  }
  return out;
}

/** A path typed after the @: at machine m (none: this machine), the folder dir it goes into and the
 *  start of a name in it (part). /… and ~/… are this machine's, so is host:… (主机:…); <machine>:…
 *  another's, by its name (any case) or ID, its path either way separated. Else none. */
export type AtPath = { m?: Machine; dir: string; part: string };
export function atPath(q: string, machines: Machine[], home?: string): AtPath | null {
  let m: Machine | undefined;
  let rest: string;
  if (q.startsWith("/") || q.startsWith("~")) rest = q;
  else {
    const cut = splitPlace(q);
    if (!cut) return null;
    const name = cut.name;
    if (isHostName(name)) rest = cut.path;
    else {
      m = machines.find((x) => x.id === name) ?? machines.find((x) => x.name.toLowerCase() === name.toLowerCase());
      if (!m) return null;
      rest = cut.path.replaceAll("\\", "/");
    }
  }
  const h = m ? homeAt(m) : home;
  if (rest === "" || rest === "~") rest = "~/";
  if (rest.startsWith("~/") && h) rest = h.replace(/\/+$/, "") + rest.slice(1);
  if (/^[A-Za-z]:$/.test(rest)) rest += "/";
  const cut = rest.lastIndexOf("/");
  if (cut < 0) return { m, dir: h ?? "~", part: rest };
  let dir = rest.slice(0, cut) || "/";
  if (/^[A-Za-z]:$/.test(dir)) dir += "/";
  return { m, dir, part: rest.slice(cut + 1) };
}

/** The section of a typed path: its folder first (its name alone; faint when there is none such),
 *  then the folders in it that the part typed starts, then those that have it. got: the folder's
 *  listing (api.folders): its path as resolved, its folders' names; missing: none such. */
export function pathSection(p: AtPath, got: { path?: string; folders?: string[]; missing?: boolean }): AtSection {
  const dir = got.path ?? p.dir;
  const machine = p.m ? { machine: p.m.id } : {};
  const join = (name: string) => (dir.endsWith("/") ? dir + name : `${dir}/${name}`);
  // Not to be named while it is not a whole path (~ not known yet): the name would not read back.
  const self: AtRow = { key: `f:${dir}`, kind: "place", p: { ...machine, path: dir, name: baseName(dir) }, self: true, ...(got.missing || dir.startsWith("~") ? { off: true } : {}) };
  const w = p.part.toLowerCase();
  const names = (got.missing ? [] : (got.folders ?? []))
    .map((n) => [n.toLowerCase().startsWith(w) ? 0 : n.toLowerCase().includes(w) ? 1 : 2, n] as const)
    .filter(([r]) => r < 2)
    .sort((a, b) => a[0] - b[0])
    .map(([, n]) => n);
  const rows: AtRow[] = [self, ...names.map((n): AtRow => ({ key: `f:${join(n)}`, kind: "place", p: { ...machine, path: join(n), name: n } }))];
  return { key: "path", head: { kind: "path", ...(p.m ? { m: p.m } : {}), path: nativeAt(p.m, dir) }, rows };
}

/** Whether → goes into the folder typed would name (what → puts after the @, typedPath): only with
 *  the caret alone (nothing selected) at the end of the @word (from: its @), no key held with it
 *  (a word or line jump), and only to change what is typed; else → moves the caret as ever. */
export const arrowInto = (text: string, from: number, start: number, end: number, typed: string, held: boolean) => !held && start === end && /^(\s|$)/.test(text.slice(end)) && text.slice(from, end) !== typed;

/** What → puts after the @ to go into place p: its path and a /; at another machine, its name
 *  first (its ID if the name has a space, which would end the @word). */
export function typedPath(p: Place, machines: Machine[]): string {
  const path = p.path.endsWith("/") ? p.path : `${p.path}/`;
  if (!p.machine) return path;
  const m = machines.find((x) => x.id === p.machine);
  return `${m && !/\s/.test(m.name) ? m.name : p.machine}:${path}`;
}
