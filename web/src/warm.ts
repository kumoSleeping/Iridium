// Chats warmed ahead (plan "弱网", the user, 2026-11-06): once the page is idle, the newest page of
// the chats you are likely to open next, so opening one draws it at once. Each workspace has five
// places: its sessions by their latest activity, those next to the open one in the sidebar first;
// the workspace you were last in goes first, then the others by their latest. Apart from those five,
// a session an agent made (a subagent, a workflow's agent, one started by another session) is
// warmed from the start while it works, and let go once it is done. The open chat is no one's
// place. src/chats.ts holds them and fetches them, one at a time, at low priority.
import type { Session } from "./api/types.gen";
import { mayPrefetch, warmTo } from "./chats";
import { settled } from "./prefetch";
import { working, type Tree } from "./tree";
import { keep, kept } from "./ui";

/** How many sessions a workspace warms. */
export const PER_WORKSPACE = 5;
/** How many sessions agents made are warmed at most, the latest first. */
export const MADE_MAX = 12;

/** Whether an agent made s and it works now: a subagent at work, or a session another started,
 *  in its turn. */
export function made(s: Session, byId: Map<string, Session>, now: number): boolean {
  if (s.archived) return false;
  if (s.subagent) return !!s.parent && working(s, byId.get(s.parent), now);
  return !!s.launcher && (s.status === "starting" || s.status === "running" || s.status === "compacting");
}

/** The sessions to warm, first first, with their updatedAt: the last workspace's five, those agents
 *  made, then the other workspaces' five. lastWs: the workspace you were last in (0: 未归类). */
export function pick(all: Session[], tree: Tree, open: string | null, lastWs: number | null, now = Date.now()): Map<string, number> {
  const byId = new Map(all.map((s) => [s.id, s]));
  const places = tree.workspaces.map((w) => {
    // As the sidebar lists them.
    const order = w.rows.flatMap((r) => (r.kind === "session" ? [r.s] : r.f.sessions));
    const at = order.findIndex((s) => s.id === open);
    const near = at < 0 ? [] : [order[at - 1], order[at + 1]].filter((s): s is Session => !!s);
    const rest = order.filter((s) => s.id !== open && !near.includes(s)).sort((a, b) => b.updatedAt - a.updatedAt);
    return { id: w.id, latest: Math.max(0, ...order.map((s) => s.updatedAt)), sessions: [...near, ...rest].slice(0, PER_WORKSPACE) };
  });
  const first = places.find((p) => p.id === lastWs);
  const others = places.filter((p) => p !== first).sort((a, b) => b.latest - a.latest);
  const byMade = all.filter((s) => s.id !== open && made(s, byId, now)).sort((a, b) => b.updatedAt - a.updatedAt).slice(0, MADE_MAX);
  const want = new Map<string, number>();
  for (const s of [...(first?.sessions ?? []), ...byMade, ...others.flatMap((p) => p.sessions)]) if (!want.has(s.id)) want.set(s.id, s.updatedAt);
  return want;
}

/** The workspace of session id as the sidebar has it (0: 未归类), if it is listed. */
export function workspaceOf(tree: Tree, id: string): number | undefined {
  for (const w of tree.workspaces) if (w.rows.some((r) => (r.kind === "session" ? r.s.id === id : r.f.sessions.some((s) => s.id === id)))) return w.id;
  return undefined;
}

let lastWs: number | null = (() => {
  const v = kept("lastWorkspace");
  return v === null || isNaN(+v) ? null : +v;
})();
let latest: (() => void) | undefined;
let timer: ReturnType<typeof setTimeout> | undefined;
let ready = false;
/** How long the list must hold still before the warmed set is picked again. */
const QUIET = 1_000;

/** The list or the open chat changed: once the page is idle, and the list holds still a moment,
 *  the chats to warm are picked again. Nothing in a hidden tab or while the browser saves data;
 *  coming back to the tab picks again. */
export function warmUp(all: Session[] | undefined, tree: Tree, open: string | null): void {
  if (!all) return;
  if (open) {
    const ws = workspaceOf(tree, open);
    if (ws !== undefined && ws !== lastWs) keep("lastWorkspace", String((lastWs = ws)));
  }
  latest = () => {
    if (document.hidden || !mayPrefetch()) return;
    warmTo(pick(all, tree, open, lastWs));
  };
  clearTimeout(timer);
  if (!ready) return void settled().then(() => ((ready = true), later()));
  later();
}

const later = () => {
  clearTimeout(timer);
  timer = setTimeout(() => latest?.(), QUIET);
};

if (typeof document !== "undefined") document.addEventListener("visibilitychange", () => void (!document.hidden && ready && later()));
