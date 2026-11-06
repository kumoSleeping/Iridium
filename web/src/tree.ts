// The sidebar's shape (plan "会话", "网页"): workspace → folder → session. A folder is a session's
// directory under its workspace's root, shown even with one session in it: only those at the root
// sit right under the workspace (the user, 2026-11-06). Sessions keep the list's order (pinned
// first, then the latest); folders follow the sessions at the root, by their latest session. Sessions in no workspace go under 未归类; archived
// ones (no shown marker) only under 归档; those that wait on you only in 需要你, back in their place
// once answered (the user's decision, 2026-11-05). A session's row says its folder where the tree does
// not: those under 需要你 and 归档, out of their place (2026-11-05).
// A subagent is no row of its own, nor in any folder: it sits under the session that started it,
// and only while it works; done, it is gone, as Claude Code lets it go (the user, 2026-11-06).
import type { Workspace } from "./api/types.gen";
import type { Session } from "./api/types.gen";

export type Folder = { key: string; name: string; path: string; sessions: Session[] };
export type Row = { kind: "session"; s: Session } | { kind: "folder"; f: Folder };
export type WsNode = { key: string; id: number; name: string; root: string; rows: Row[]; count: number };
/** A workflow run at work under the session that started it, as Claude Code's /workflows shows it:
 *  its name, the phase its latest agent at work is in, how many of the agents seen so far are done,
 *  and those at work. */
export type Run = { id: string; name: string; phase: string; done: number; total: number; working: Session[] };

/** kids: the subagents at work, by the session that started them, but a workflow's: those are in
 *  runs, one row a run (the user, 2026-11-06). */
export type Tree = { needs: Session[]; workspaces: WsNode[]; archived: Session[]; where: Map<string, string>; kids: Map<string, Session[]>; runs: Map<string, Run[]> };

/** How long a subagent may write nothing and still count as at work: one its parent dropped (a
 *  stop, a crash) never writes its final answer. */
export const SUB_QUIET = 5 * 60_000;

/** A subagent at work: not done, written lately, the session that started it with its process
 *  alive. Not that session's turn: background agents and workflows work on after it ends. */
export const working = (s: Session, parent: Session | undefined, now: number) => !s.done && !!parent?.live && parent.status !== "error" && now - s.updatedAt < SUB_QUIET;

const under = (path: string, root: string) => root !== "" && (path === root || path.startsWith(root.endsWith("/") ? root : root + "/"));
const rel = (path: string, root: string) => (path === root ? "" : path.slice(root.length).replace(/^\/+/, ""));

export function buildTree(all: Session[], workspaces: Workspace[], unsorted: string, now = Date.now()): Tree {
  const list = all.filter((s) => !s.subagent);
  const byId = new Map(list.map((s) => [s.id, s]));
  const kids = new Map<string, Session[]>();
  const runs = new Map<string, Run[]>();
  for (const s of all) {
    if (!s.subagent || !s.parent || !byId.has(s.parent)) continue;
    const at = working(s, byId.get(s.parent), now);
    const w = s.workflow;
    if (!w) {
      if (at) kids.set(s.parent, [...(kids.get(s.parent) ?? []), s]);
      continue;
    }
    const list = runs.get(s.parent) ?? [];
    let r = list.find((x) => x.id === w.run);
    if (!r) list.push((r = { id: w.run, name: w.name || w.run, phase: "", done: 0, total: 0, working: [] }));
    runs.set(s.parent, list);
    r.total++;
    if (s.done) r.done++;
    if (at) r.working.push(s);
  }
  // A run shows while one of its agents works; its phase, the latest one's at work.
  for (const [parent, list] of runs) {
    const live = list.filter((r) => r.working.length);
    for (const r of live) r.phase = [...r.working].sort((a, b) => b.updatedAt - a.updatedAt)[0]!.workflow?.phase ?? "";
    if (live.length) runs.set(parent, live);
    else runs.delete(parent);
  }
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
    const sub = [...folders.values()].sort((a, b) => latest(b) - latest(a));
    return { key, id, name, root, rows: [...rows, ...sub.map((f): Row => ({ kind: "folder", f }))], count: sessions.length };
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
  return { needs, workspaces: nodes, archived, where, kids, runs };
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
export const flat = (t: Tree) => [...t.needs, ...t.workspaces.flatMap((w) => w.rows.flatMap((r) => (r.kind === "session" ? [r.s] : r.f.sessions)))].flatMap((s) => [s, ...(t.kids.get(s.id) ?? []), ...(t.runs.get(s.id) ?? []).flatMap((r) => r.working)]);
