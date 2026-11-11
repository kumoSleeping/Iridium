// The sidebar's shape (plan "会话", "网页"): workspace → folder → session. A folder is a session's
// directory under its workspace's root, shown even with one session in it: only those at the root
// sit right under the workspace (the user, 2026-11-06); one in another folder listed, under that
// one (2026-11-10). Sessions keep the list's order (pinned
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
// This machine's groups are always listed; a group at another machine only while a person opened a
// session in it (Session.own) not archived; a session one started (ir new, Session.launcher) hangs
// under the topmost listed one it came from, a whole session, listed always (docs/exec.md
// "远端工作区", the user, 2026-11-10). One 未归类, this machine's.
import type { Workspace } from "./api/types.gen";
import type { Places } from "./guilds";
import type { Session } from "./api/types.gen";

/** pinned: above the sessions at its group's root, or its parent's (reorder.ts). sessions: its
 *  own; children: the folders in it (pinned first), each named by its path under it. */
export type Folder = { key: string; name: string; path: string; sessions: Session[]; children: Folder[]; pinned?: boolean };
export type Row = { kind: "session"; s: Session } | { kind: "folder"; f: Folder };
/** machine: the machine its folder is at, by ID; none for this machine's, nor 未归类. only: it holds
 *  only the sessions right in its folder (Workspace.only). */
export type WsNode = { key: string; id: number; name: string; root: string; rows: Row[]; count: number; machine?: string; only?: boolean };
/** A workflow run at work under the session that started it, as Claude Code's /workflows shows it:
 *  its name, the phase its latest agent at work is in, how many of the agents seen so far are done,
 *  and those at work. */
export type Run = { id: string; name: string; phase: string; done: number; total: number; working: Session[] };

/** kids: the subagents at work, by the session that started them, but a workflow's: those are in
 *  runs, one row a run (the user, 2026-11-06). hung: the sessions others started, under the topmost
 *  listed one (standing). unlisted: those not listed, in 归档 or found by the jump box (a group at
 *  another machine no one opened a session in, their subagents too). */
export type Tree = { workspaces: WsNode[]; archived: Session[]; where: Map<string, string>; kids: Map<string, Session[]>; runs: Map<string, Run[]>; hung: Map<string, Session[]>; unlisted: Set<string> };

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

/** Where the sessions not archived stand in the list (docs/exec.md "远端工作区"). rows: listed on
 *  their own, in their group (or 未归类). hung: the dispatched ones (a launcher, not own) under the
 *  topmost listed session they came from, a chain all under it, in the list's order. own: opened by
 *  a person as the page takes it: Session.own, or one whose launcher is not listed (archived, gone,
 *  a subagent, in a cycle, in a group not listed), so none is lost. groups: the groups listed (this
 *  machine's, and those at another holding an own one, or one of this machine's sessions, or a
 *  launcher not listed with one it started beside it). Not in any: not listed. The server's
 *  session.standOf is the same, for what it pushes (internal/session/testdata/standing.json). */
export type Standing = { rows: Set<string>; hung: Map<string, Session[]>; own: Set<string>; groups: Set<number> };

/** Where each session stands (Standing). workspaces: the groups, by whose paths a group is at
 *  another machine (without them, by its sessions' machine); a session in none known is in 未归类. */
export function standing(all: Session[], workspaces?: Workspace[]): Standing {
  const live = all.filter((s) => !s.subagent && !s.archived);
  const byId = new Map(live.map((s) => [s.id, s]));
  const paths = workspaces && new Map(workspaces.map((w) => [w.id, w.path]));
  const wsOf = (s: Session) => {
    const id = s.workspace ?? 0;
    return !paths || paths.has(id) ? id : 0;
  };
  const remote = (s: Session) => {
    const id = wsOf(s);
    return id && paths ? remotePath(paths.get(id)!) : !!s.machine;
  };
  const own = new Set<string>();
  // Started by another (its ir new), not taken out from under it by hand, nor taken as own here.
  const dispatched = (s: Session) => !!s.launcher && !s.own && !own.has(s.id);
  // The topmost one a chain of launchers goes up to: the first not dispatched, or whose launcher
  // is not listed (archived, gone, a subagent); in a cycle, its least ID.
  const top = new Map<string, string>();
  const tops = () => {
    top.clear();
    for (const s of live) {
      const path: Session[] = [];
      const seen = new Set<string>();
      let cur = s;
      let t: string;
      for (;;) {
        const known = top.get(cur.id);
        if (known !== undefined) {
          t = known;
          break;
        }
        if (seen.has(cur.id)) {
          t = path.slice(path.indexOf(cur)).map((x) => x.id).sort()[0]!;
          break;
        }
        seen.add(cur.id);
        path.push(cur);
        const up = dispatched(cur) ? byId.get(cur.launcher!) : undefined;
        if (!up) {
          t = cur.id;
          break;
        }
        cur = up;
      }
      for (const x of path) top.set(x.id, t);
    }
  };
  tops();
  const isTop = (s: Session) => top.get(s.id) === s.id;
  for (const s of live) if (isTop(s) && (s.own || dispatched(s))) own.add(s.id);
  // A group at another machine listed while it holds an own one, or one of this machine's.
  const groups = new Set<number>();
  const count = (s: Session) => {
    const id = wsOf(s);
    if (id && (!remote(s) || !s.machine || own.has(s.id))) groups.add(id);
  };
  for (const s of live) if (isTop(s)) count(s);
  const shown = (s: Session) => !remote(s) || (wsOf(s) ? groups.has(wsOf(s)) : own.has(s.id));
  // Those whose launcher is not listed (in a group at another machine no one opened). One in its
  // launcher's group lists that group, and so hangs under it after all; the others, their
  // launcher still not listed then, are as a person opened them: on their own, their groups listed
  // for them, the top of those they started. Decided all at once, so a group is listed only for
  // one that stays own.
  const up = (s: Session) => byId.get(s.launcher!)!;
  const orphans = live.filter((s) => !isTop(s) && isTop(up(s)) && !shown(up(s)));
  for (const s of orphans) if (wsOf(s) && wsOf(s) === wsOf(up(s))) groups.add(wsOf(s));
  const taken = orphans.filter((s) => !shown(up(s)));
  for (const s of taken) own.add(s.id);
  for (const s of taken) count(s);
  if (taken.length) tops();
  const rows = new Set<string>();
  const hung = new Map<string, Session[]>();
  const hang = (at: string, s: Session) => hung.set(at, [...(hung.get(at) ?? []), s]);
  for (const s of live) {
    const t = byId.get(top.get(s.id)!)!;
    if (s === t) {
      if (shown(s)) rows.add(s.id);
    } else if (shown(t)) hang(t.id, s);
    else if (shown(s)) rows.add(s.id);
  }
  return { rows, hung, own, groups };
}

/** Whether a person opened s, as the page takes it (standing): never a subagent. list: the list. */
export const isOwn = (s: Session, list: Session[]) => !s.subagent && (s.archived ? !!s.own : standing(list).own.has(s.id));

/** The sessions others started, under the topmost listed one they came from (standing).
 *  workspaces: the groups, as the list is built with them. */
export const hungUnder = (list: Session[], workspaces?: Workspace[]) => standing(list, workspaces).hung;

/** The groups at other machines listed (a person opened a session there), by their paths
 *  ("<machine>:<path>"). */
export function listedRemote(list: Session[], workspaces: Workspace[]): Set<string> {
  const st = standing(list, workspaces);
  return new Set(workspaces.filter((w) => remotePath(w.path) && st.groups.has(w.id) && list.some((s) => st.rows.has(s.id) && s.workspace === w.id)).map((w) => w.path));
}

/** The order subagents and runs were first seen in, by id ("run:" before a run's): the list says
 *  nothing of when one began. Those first seen together (the page opened) go by when they last
 *  wrote, the earliest first. */
const SEEN = new Map<string, number>();
function bySeen<T>(xs: T[], key: (x: T) => string, at: (x: T) => number, seen: Map<string, number>): T[] {
  const fresh = xs.filter((x) => !seen.has(key(x))).sort((a, b) => at(a) - at(b));
  for (const x of fresh) seen.set(key(x), seen.size);
  return [...xs].sort((a, b) => seen.get(key(a))! - seen.get(key(b))!);
}

/** places: where the rows were put by hand (from the carried "guilds"): the groups' rows by key, the
 *  folders by path. */
export function buildTree(all: Session[], workspaces: Workspace[], unsorted: string, now = Date.now(), seen = SEEN, places: Places = { groups: [], order: {}, pinned: [] }): Tree {
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
  const st = standing(all, workspaces);
  const live = list.filter((s) => st.rows.has(s.id));
  const byWs = new Map<number, Session[]>();
  const where = new Map<string, string>();
  for (const s of live) {
    const ws = workspaces.some((w) => w.id === s.workspace) ? s.workspace! : 0;
    byWs.set(ws, [...(byWs.get(ws) ?? []), s]);
  }
  const node = (key: string, id: number, name: string, root: string, sessions: Session[], only?: boolean): WsNode => {
    const machine = id ? machineOf(root) : undefined;
    const folders = new Map<string, Folder>();
    const rows: Row[] = [];
    for (const s of sessions) {
      const p = groupPath(s);
      const r = under(p, root) ? rel(p, root) : p;
      if (r === "") {
        rows.push({ kind: "session", s });
        continue;
      }
      let f = folders.get(r);
      if (!f) folders.set(r, (f = { key: `${key}/${r}`, name: r, path: p, sessions: [], children: [], ...(places.pinned.includes(p) ? { pinned: true } : {}) }));
      f.sessions.push(s);
    }
    // A folder in another one listed is under it, named by its path there: the nearest one listed,
    // the directories between with no session of their own in its name, not rows of their own
    // ("b/c" under "a" for a/b/c) (the user, 2026-11-10).
    const top: Folder[] = [];
    for (const [r, f] of folders) {
      const segs = r.split("/");
      let parent: Folder | undefined;
      for (let i = segs.length - 1; i > 0 && !parent; i--) {
        const up = segs.slice(0, i).join("/");
        parent = folders.get(up);
        if (parent) f.name = r.slice(up.length + 1);
      }
      (parent ? parent.children : top).push(f);
    }
    const arrange = (fs: Folder[]): Folder[] => {
      const sorted = fs.sort((a, b) => folderKey(b, places.order) - folderKey(a, places.order));
      for (const f of sorted) f.children = arrange(f.children);
      return [...sorted.filter((f) => f.pinned), ...sorted.filter((f) => !f.pinned)];
    };
    const sub = arrange(top);
    const folder = (f: Folder): Row => ({ kind: "folder", f });
    // Pinned folders above the sessions at the root, the rest under them.
    return { key, id, name, root, rows: [...sub.filter((f) => f.pinned).map(folder), ...rows, ...sub.filter((f) => !f.pinned).map(folder)], count: sessions.length, ...(machine ? { machine } : {}), ...(only ? { only } : {}) };
  };
  // A group is a folder sessions are in (the user, 2026-11-08): one with none shown is not listed.
  // One row a group, wherever its sessions run (2026-11-10: no group listed at two machines).
  const nodes = workspaces.filter((w) => byWs.get(w.id)?.length).map((w) => node(`w${w.id}`, w.id, w.name, w.path, byWs.get(w.id)!, w.only));
  // 未归类 has no root: its folders are grouped under the directory all its sessions share, and shown
  // by their whole path (~ for the home directory, not its bare name). One, this machine's: one of
  // another machine's in it only as a person opened it (2026-11-10).
  const none = byWs.get(0);
  if (none?.length) nodes.push(node("w0", 0, unsorted, commonDir(none.map(groupPath)), none));
  // The groups' rows in the order they were dragged to, whichever machine each is at (the user,
  // 2026-11-09, no longer a section a machine); those never dragged after them, as the groups are
  // listed. 未归类 last.
  const n = places.groups.length;
  const rank = (w: WsNode) => (w.id === 0 ? n + 2 : places.groups.indexOf(w.key) + 1 || n + 1);
  nodes.sort((a, b) => rank(a) - rank(b));
  // In 归档: one of this machine's, one a person opened, one in a group listed, one another started
  // (restored with it); not those of a group at another machine no one opened (DSH Desktop's
  // archive there).
  const kept = (s: Session) => !s.machine || !!s.own || (!!s.workspace && st.groups.has(s.workspace)) || (!!s.launcher && byId.has(s.launcher));
  // The last at work first, pinned or not: a pin is for the list in use (the user, 2026-11-08).
  const archived = list.filter((s) => s.archived && kept(s)).sort((a, b) => b.updatedAt - a.updatedAt);
  // Out of their place: the workspace and the folder in it, or the directory's name in none.
  for (const s of archived) {
    const p = groupPath(s);
    const w = workspaces.find((w) => w.id === s.workspace && under(p, w.path));
    const r = w && rel(p, w.path);
    where.set(s.id, w ? (r ? `${w.name}/${r}` : w.name) : (p.split("/").filter(Boolean).pop() ?? p));
  }
  const hung = new Map([...st.hung].filter(([at]) => st.rows.has(at)));
  const listed = new Set([...live, ...[...hung.values()].flat(), ...archived].map((s) => s.id));
  const unlisted = new Set(all.filter((s) => (s.subagent ? !!s.parent && byId.has(s.parent) && !listed.has(s.parent) : !listed.has(s.id))).map((s) => s.id));
  return { workspaces: nodes, archived, where, kids, runs, hung, unlisted };
}

/** The group listed that holds path (a group path, "<machine>:…" at another machine), as the server
 *  groups: the one of that folder, else the deepest whose folder it is in and that holds more than
 *  its own (not /, a home, a drive's root, one split: Workspace.only). None: no group listed does. */
export function groupHolding(nodes: WsNode[], path: string): WsNode | undefined {
  let best: WsNode | undefined;
  for (const w of nodes) {
    if (w.id === 0 || !(w.root === path || (!w.only && under(path, w.root)))) continue;
    if (!best || w.root.length > best.root.length) best = w;
  }
  return best;
}

/** The folders of group w that hold path, from its own row down to the deepest one listed in
 *  another (each opened to show it); none: right at the group's root, or in no folder listed. */
export function foldersHolding(w: WsNode, path: string): Folder[] {
  const chain: Folder[] = [];
  let fs = w.rows.flatMap((r) => (r.kind === "folder" ? [r.f] : []));
  for (let f; (f = fs.find((x) => under(path, x.path))); fs = f.children) chain.push(f);
  return chain;
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

/** A folder's sessions and its folders', as the sidebar shows them: the pinned folders, its own,
 *  the other folders. */
export const folderSessions = (f: Folder): Session[] => [...f.children.filter((c) => c.pinned).flatMap(folderSessions), ...f.sessions, ...f.children.filter((c) => !c.pinned).flatMap(folderSessions)];
/** A folder and those in it, at any depth. */
export const allFolders = (fs: Folder[]): Folder[] => fs.flatMap((f) => [f, ...allFolders(f.children)]);

const latest = (f: Folder) => Math.max(...folderSessions(f).map((s) => s.updatedAt));
/** What a folder is sorted by among those in its group: where it was dragged to, else its latest
 *  session's activity (reorder.ts). */
export const folderKey = (f: Folder, order: Places["order"]) => order[f.path] || latest(f);

/** A session's row and those drawn under it: its subagents at work, its runs' agents, then the
 *  sessions hung under it (dispatched), each with its own. */
const withUnder = (t: Tree, s: Session): Session[] => [s, ...(t.kids.get(s.id) ?? []), ...(t.runs.get(s.id) ?? []).flatMap((r) => r.working)];
export const family = (t: Tree, s: Session): Session[] => [...withUnder(t, s), ...(t.hung.get(s.id) ?? []).flatMap((h) => withUnder(t, h))];

/** The sessions as the sidebar shows them from top to bottom, for Alt+↑↓ and the jump box. */
export const flat = (t: Tree) => t.workspaces.flatMap((w) => w.rows.flatMap((r) => (r.kind === "session" ? [r.s] : folderSessions(r.f)))).flatMap((s) => family(t, s));
