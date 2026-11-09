// The sidebar's shape (plan "会话", "网页"): workspace → folder → session. A folder is a session's
// directory under its workspace's root, shown even with one session in it: only those at the root
// sit right under the workspace (the user, 2026-11-06). Sessions keep the list's order (pinned
// first, then the latest); folders follow the sessions at the root, by their latest session, or
// where they were dragged to; those pinned come before them (reorder.ts). Sessions in no workspace go under 未归类; archived
// ones (no shown marker) only under 归档; those that wait on you only in 需要你, back in their place
// once answered (the user's decision, 2026-11-05). A session's row says its folder where the tree does
// not: those under 需要你 and 归档, out of their place (2026-11-05).
// A subagent is no row of its own, nor in any folder: it sits under the session that started it,
// and only while it works; done, it is gone, as Claude Code lets it go (the user, 2026-11-06).
// Those under one session keep the order they came in, the newest last, not the order they last
// wrote in, which swapped them back and forth (the user, 2026-11-08); so do a workflow run's agents
// (2026-11-09).
import type { Places, Workspace } from "./api/types.gen";
import type { Session } from "./api/types.gen";

/** pinned: above the sessions at its group's root (reorder.ts). */
export type Folder = { key: string; name: string; path: string; sessions: Session[]; pinned?: boolean };
export type Row = { kind: "session"; s: Session } | { kind: "folder"; f: Folder };
/** hidden: a hidden group listed while one of its sessions runs elsewhere (its menu shows it again).
 *  machine: the machine its folder is at, by ID; none for this machine's. */
export type WsNode = { key: string; id: number; name: string; root: string; rows: Row[]; count: number; hidden?: boolean; machine?: string };
/** A workflow run at work under the session that started it, as Claude Code's /workflows shows it:
 *  its name, the phase its latest agent at work is in, how many of the agents seen so far are done,
 *  and those at work. */
export type Run = { id: string; name: string; phase: string; done: number; total: number; working: Session[] };

/** kids: the subagents at work, by the session that started them, but a workflow's: those are in
 *  runs, one row a run (the user, 2026-11-06). hidden: the sessions of hidden groups, not listed,
 *  in 归档 or found by the jump box. */
export type Tree = { workspaces: WsNode[]; archived: Session[]; where: Map<string, string>; kids: Map<string, Session[]>; runs: Map<string, Run[]>; hidden: Set<string> };

/** How long a subagent may write nothing and still count as at work: one its parent dropped (a
 *  stop, a crash) never writes its final answer. */
export const SUB_QUIET = 5 * 60_000;

/** A subagent at work: not done, written lately, the session that started it with its process
 *  alive. Not that session's turn: background agents and workflows work on after it ends. */
export const working = (s: Session, parent: Session | undefined, now: number) => !s.done && !!parent?.live && parent.status !== "error" && now - s.updatedAt < SUB_QUIET;

/** The path a session is grouped by, as the server's (remote.GroupPath): its cwd, or at another
 *  machine "<machine>:<cwd, '/'-separated>", as that machine's groups' roots are written. */
export const groupPath = (s: Pick<Session, "cwd" | "machine">) => (s.machine ? `${s.machine}:${s.cwd.replaceAll("\\", "/")}` : s.cwd);

/** A folder at another machine: its path is "<machine>:…", never absolute here. */
export const remotePath = (path: string) => path !== "" && !path.startsWith("/");

/** The machine a path is at ("<machine>:…"), by ID; undefined for this machine's. */
export const machineOf = (path: string) => (remotePath(path) ? path.slice(0, path.indexOf(":")) : undefined);

const under = (path: string, root: string) => root !== "" && (path === root || path.startsWith(root.endsWith("/") ? root : root + "/"));
const rel = (path: string, root: string) => (path === root ? "" : path.slice(root.length).replace(/^\/+/, ""));

/** The order subagents and runs were first seen in, by id ("run:" before a run's): the list says
 *  nothing of when one began. Those first seen together (the page opened) go by when they last
 *  wrote, the earliest first. */
const SEEN = new Map<string, number>();
function bySeen<T>(xs: T[], key: (x: T) => string, at: (x: T) => number, seen: Map<string, number>): T[] {
  const fresh = xs.filter((x) => !seen.has(key(x))).sort((a, b) => at(a) - at(b));
  for (const x of fresh) seen.set(key(x), seen.size);
  return [...xs].sort((a, b) => seen.get(key(a))! - seen.get(key(b))!);
}

/** machines: the other machines' IDs, in the order their sections follow this machine's. */
/** places: where the rows were put by hand (the carried "places"): the groups' rows by key, the
 *  folders by path. */
export function buildTree(all: Session[], workspaces: Workspace[], unsorted: string, now = Date.now(), seen = SEEN, machines: string[] = [], places: Places = { groups: [], order: {}, pinned: [] }): Tree {
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
    for (const r of live) {
      r.phase = [...r.working].sort((a, b) => b.updatedAt - a.updatedAt)[0]!.workflow?.phase ?? "";
      r.working = bySeen(r.working, (s) => s.id, (s) => s.updatedAt, seen);
    }
    if (live.length) runs.set(parent, bySeen(live, (r) => `run:${r.id}`, (r) => Math.min(...r.working.map((s) => s.updatedAt)), seen));
    else runs.delete(parent);
  }
  for (const [parent, list] of kids) kids.set(parent, bySeen(list, (s) => s.id, (s) => s.updatedAt, seen));
  // One that waits on you stays in its place, its lamp breathing amber: no 需要你 group of its own
  // (the user, 2026-11-08).
  // A group hidden from its menu (the user, 2026-11-08): its sessions nowhere, but while one of
  // them runs elsewhere (open in DSH Desktop, started there); then it is listed whole.
  const hidden = new Set<string>();
  const shut = new Set(workspaces.filter((w) => w.hidden && !list.some((s) => s.workspace === w.id && !s.archived && s.live && s.status === "elsewhere")).map((w) => w.id));
  for (const s of all) if (s.workspace && shut.has(s.workspace)) hidden.add(s.id);
  const live = list.filter((s) => !s.archived && !hidden.has(s.id));
  const byWs = new Map<number, Session[]>();
  const where = new Map<string, string>();
  for (const s of live) {
    const ws = workspaces.some((w) => w.id === s.workspace) ? s.workspace! : 0;
    byWs.set(ws, [...(byWs.get(ws) ?? []), s]);
  }
  const node = (key: string, id: number, name: string, root: string, sessions: Session[], hidden?: boolean, at = machineOf(root) ?? ""): WsNode => {
    const machine = at || undefined;
    const folders = new Map<string, Folder>();
    const rows: Row[] = [];
    for (const s of sessions) {
      const p = groupPath(s);
      const r = under(p, root) ? rel(p, root) : p;
      // In DSH's 未分组 at its machine: in 未归类 a row of its own, as DSH lists it (the user,
      // 2026-11-08), not one folder each. One of this machine's in a group at another (a
      // coordinator there) is a row of its own too, marked on its logo, not in a folder.
      if (r === "" || (id === 0 && s.loose) || (remotePath(root) && !remotePath(p))) {
        rows.push({ kind: "session", s });
        continue;
      }
      let f = folders.get(r);
      if (!f) folders.set(r, (f = { key: `${key}/${r}`, name: r, path: p, sessions: [], ...(places.pinned.includes(p) ? { pinned: true } : {}) }));
      f.sessions.push(s);
    }
    const sub = [...folders.values()].sort((a, b) => folderKey(b, places.order) - folderKey(a, places.order));
    const folder = (f: Folder): Row => ({ kind: "folder", f });
    // Pinned folders above the sessions at the root, the rest under them.
    return { key, id, name, root, rows: [...sub.filter((f) => f.pinned).map(folder), ...rows, ...sub.filter((f) => !f.pinned).map(folder)], count: sessions.length, ...(hidden ? { hidden } : {}), ...(machine ? { machine } : {}) };
  };
  // A group is a folder sessions are in (the user, 2026-11-08): one with none shown is not listed.
  // A session is listed with those of its group at the machine it runs at (the user, 2026-11-09): a
  // group with sessions at two machines is listed twice, by the same name, each time with those at
  // that machine, this machine's first. Which group a session is in is not changed (ir ls --here, a
  // new session in its starter's group): only where it is drawn. One of this machine's in a group at
  // another (a coordinator there, opened by that group's "+") stays in that group, marked on its
  // logo (the user, 2026-11-09).
  // Where a machine comes: this one first, then the others in the order they are listed.
  const machineAt = (m?: string) => (m ? machines.indexOf(m) + 1 || machines.length + 1 : 0);
  const nodes = workspaces
    .filter((w) => byWs.get(w.id)?.length)
    .flatMap((w) => {
      const own = machineOf(w.path) ?? "";
      const by = new Map<string, Session[]>();
      for (const s of byWs.get(w.id)!) {
        const at = s.machine ?? own;
        by.set(at, [...(by.get(at) ?? []), s]);
      }
      return [...by].sort(([a], [b]) => machineAt(a || undefined) - machineAt(b || undefined)).map(([m, ss]) => node(m === own ? `w${w.id}` : `w${w.id}@${m}`, w.id, w.name, w.path, ss, w.hidden, m));
    });
  // 未归类 has no root: its folders are grouped under the directory all its sessions share, and shown
  // by their whole path (~ for the home directory, not its bare name).
  // One 未归类 a machine, at the foot of its section.
  const none = new Map<string, Session[]>();
  for (const s of byWs.get(0) ?? []) none.set(s.machine ?? "", [...(none.get(s.machine ?? "") ?? []), s]);
  for (const [m, ss] of none) nodes.push(node(m ? `w0:${m}` : "w0", 0, unsorted, m ? `${m}:${commonDir(ss.map(groupPath).map((p) => p.slice(m.length + 1)))}` : commonDir(ss.map(groupPath)), ss));
  // The groups' rows in the order they were dragged to, whichever machine each is at (the user,
  // 2026-11-09, no longer a section a machine); those never dragged after them, as the groups are
  // listed. 未归类 last, one a machine, this machine's first, then the others' as listed.
  const n = places.groups.length;
  const rank = (w: WsNode) => (w.id === 0 ? n + 2 + machineAt(w.machine) : places.groups.indexOf(w.key) + 1 || n + 1);
  nodes.sort((a, b) => rank(a) - rank(b));
  // The last at work first, pinned or not: a pin is for the list in use (the user, 2026-11-08).
  const archived = list.filter((s) => s.archived && !hidden.has(s.id)).sort((a, b) => b.updatedAt - a.updatedAt);
  // Out of their place: the workspace and the folder in it, or the directory's name in none.
  for (const s of archived) {
    const p = groupPath(s);
    const w = workspaces.find((w) => w.id === s.workspace && under(p, w.path));
    const r = w && rel(p, w.path);
    where.set(s.id, w ? (r ? `${w.name}/${r}` : w.name) : (p.split("/").filter(Boolean).pop() ?? p));
  }
  return { workspaces: nodes, archived, where, kids, runs, hidden };
}

/** The deepest directory all paths are in ("" for none but the root); another machine's paths
 *  ("<machine>:C:/…", "<machine>:/…") keep their machine in front. */
export function commonDir(paths: string[]): string {
  if (!paths.length) return "";
  const split = paths.map((p) => p.split("/").filter(Boolean));
  const first = split[0]!;
  let n = 0;
  while (n < first.length && split.every((p) => p[n] === first[n])) n++;
  // A lone directory is its own parent's folder: its name shows.
  if (paths.every((p) => p === paths[0]) && n > 0) n--;
  return n ? (paths[0]!.startsWith("/") ? "/" : "") + first.slice(0, n).join("/") : "";
}

/** How many sessions a folder, or a group's root, shows while the rest are folded (the user,
 *  2026-11-09): with more, the first so many as listed (the latest, pinned first) and a row for the
 *  rest, opened by hand. One kept (pinned, open, at work, waiting on you) is shown wherever it is.
 *  Not one folded alone: a row for one would hide nothing. */
export const FOLD_SHOW = 5;
export function foldRest(ss: Session[], kept: (s: Session) => boolean, n = FOLD_SHOW): { shown: Session[]; rest: number } {
  if (ss.length <= n + 1) return { shown: ss, rest: 0 };
  const shown = ss.filter((s, i) => i < n || kept(s));
  return shown.length >= ss.length - 1 ? { shown: ss, rest: 0 } : { shown, rest: ss.length - shown.length };
}

const latest = (f: Folder) => Math.max(...f.sessions.map((s) => s.updatedAt));
/** What a folder is sorted by among those in its group: where it was dragged to, else its latest
 *  session's activity (reorder.ts). */
export const folderKey = (f: Folder, order: Places["order"]) => order[f.path] || latest(f);

/** The sessions as the sidebar shows them from top to bottom, for Alt+↑↓ and the jump box. */
export const flat = (t: Tree) => [...t.workspaces.flatMap((w) => w.rows.flatMap((r) => (r.kind === "session" ? [r.s] : r.f.sessions)))].flatMap((s) => [s, ...(t.kids.get(s.id) ?? []), ...(t.runs.get(s.id) ?? []).flatMap((r) => r.working)]);
