// The sidebar's shape (plan "会话", "网页"): workspace → folder → session. A folder is a session's
// directory under its workspace's root; one with a single session is not shown, the session sitting
// in its place. Sessions keep the list's order (pinned first, then the latest); folders follow the
// sessions at the root, by their latest session. Sessions in no workspace go under 未归类; archived
// ones (no shown marker) only under 归档; those that wait on you only in 需要你, back in their place
// once answered (the user's decision, 2026-11-05). A session's row says its folder where the tree does
// not: one in a folder of one, and those under 需要你 and 归档, out of their place (2026-11-05).
import type { Workspace } from "./api/types.gen";
import type { Session } from "./api/types.gen";

export type Folder = { key: string; name: string; path: string; sessions: Session[] };
export type Row = { kind: "session"; s: Session } | { kind: "folder"; f: Folder };
export type WsNode = { key: string; id: number; name: string; root: string; rows: Row[]; count: number };
export type Tree = { needs: Session[]; workspaces: WsNode[]; archived: Session[]; where: Map<string, string> };

const under = (path: string, root: string) => root !== "" && (path === root || path.startsWith(root.endsWith("/") ? root : root + "/"));
const rel = (path: string, root: string) => (path === root ? "" : path.slice(root.length).replace(/^\/+/, ""));

export function buildTree(list: Session[], workspaces: Workspace[], unsorted: string): Tree {
  const live = list.filter((s) => !s.archived);
  const needs = live.filter((s) => s.status === "waiting");
  const byWs = new Map<number, Session[]>();
  const where = new Map<string, string>();
  for (const s of live) {
    if (s.status === "waiting") continue;
    const ws = workspaces.some((w) => w.id === s.workspace) ? s.workspace! : 0;
    byWs.set(ws, [...(byWs.get(ws) ?? []), s]);
  }
  const node = (key: string, id: number, name: string, root: string, sessions: Session[]): WsNode => {
    const folders = new Map<string, Folder>();
    const rows: Row[] = [];
    for (const s of sessions) {
      const r = under(s.cwd, root) ? rel(s.cwd, root) : s.cwd;
      if (r === "") {
        rows.push({ kind: "session", s });
        continue;
      }
      let f = folders.get(r);
      if (!f) folders.set(r, (f = { key: `${key}/${r}`, name: r, path: under(s.cwd, root) ? s.cwd : r, sessions: [] }));
      f.sessions.push(s);
    }
    const multi: Folder[] = [];
    // A folder of one: its session in the list's order among the root's.
    const single = new Set<string>();
    for (const f of folders.values()) if (f.sessions.length === 1) (single.add(f.sessions[0]!.id), where.set(f.sessions[0]!.id, id === 0 ? f.path : f.name));
    const ordered = sessions.filter((s) => rows.some((r) => r.kind === "session" && r.s === s) || single.has(s.id)).map((s): Row => ({ kind: "session", s }));
    for (const f of folders.values()) if (f.sessions.length > 1) multi.push(f);
    multi.sort((a, b) => latest(b) - latest(a));
    return { key, id, name, root, rows: [...ordered, ...multi.map((f): Row => ({ kind: "folder", f }))], count: sessions.length };
  };
  // A workspace with no session yet shows too: one was just made.
  const nodes = workspaces.map((w) =>node(`w${w.id}`, w.id, w.name, w.path, byWs.get(w.id) ?? []));
  // 未归类 has no root: its folders are grouped under the directory all its sessions share, and shown
  // by their whole path (~ for the home directory, not its bare name).
  const none = byWs.get(0);
  if (none?.length) nodes.push(node("w0", 0, unsorted, commonDir(none.map((s) => s.cwd)), none));
  const archived = list.filter((s) => s.archived);
  // Out of their place: the workspace and the folder in it, or the directory's name in none.
  for (const s of [...needs, ...archived]) {
    const w = workspaces.find((w) => w.id === s.workspace && under(s.cwd, w.path));
    const r = w && rel(s.cwd, w.path);
    where.set(s.id, w ? (r ? `${w.name}/${r}` : w.name) : (s.cwd.split("/").filter(Boolean).pop() ?? s.cwd));
  }
  return { needs, workspaces: nodes, archived, where };
}

/** The deepest directory all paths are in ("" for none but the root). */
export function commonDir(paths: string[]): string {
  if (!paths.length) return "";
  const split = paths.map((p) => p.split("/").filter(Boolean));
  const first = split[0]!;
  let n = 0;
  while (n < first.length && split.every((p) => p[n] === first[n])) n++;
  // A lone directory is its own parent's folder: its name shows.
  if (paths.every((p) => p === paths[0]) && n > 0) n--;
  return n ? "/" + first.slice(0, n).join("/") : "";
}

const latest = (f: Folder) => Math.max(...f.sessions.map((s) => s.updatedAt));

/** The sessions as the sidebar shows them from top to bottom, 需要你 first, for Alt+↑↓ and the jump
 *  box. */
export const flat = (t: Tree) => [...t.needs, ...t.workspaces.flatMap((w) => w.rows.flatMap((r) => (r.kind === "session" ? [r.s] : r.f.sessions)))];
