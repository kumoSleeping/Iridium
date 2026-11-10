// 在哪干活 (docs/plan.md, 2026-11-10), pure for tests: the places under a chat's title, shown only
// as the session dispatched work (sessions hung under it, tree.ts standing) or named a place (@):
// its own folder first; each place it dispatched to, the sessions there; the places it named and
// sent no one to yet.
import type { Session, Workspace } from "../api/types.gen";
import type { Place } from "../places";
import { groupPath, machineOf, standing } from "../tree";
import { baseName, type NamedPlace } from "./at";

/** One place: own, the session's folder (with those it sent there); sent, one it dispatched to,
 *  sessions those hung there; named, one it named and sent no one to. key: its group path
 *  ("<machine>:<path>" at another machine). */
export type Spot = { kind: "own" | "sent" | "named"; key: string; place: Place; name: string; sessions: Session[] };

/** Those id started, and those they started, all the way down, as they hang in the list (not
 *  subagents, not taken out by hand), in its order; a cycle stops. workspaces: as the list has. */
export function dispatchedFrom(id: string, list: Session[], workspaces?: Workspace[]): Session[] {
  const byId = new Map(list.map((s) => [s.id, s]));
  const st = standing(list, workspaces);
  // Up its launchers to id, through dispatched ones only: one taken out by hand (own, as the list
  // has it) or a subagent between ends the chain, as it does in the list.
  const from = (s: Session) => {
    let cur: Session | undefined = s;
    for (let n = 0; cur?.launcher && n <= list.length; n++) {
      if (cur.launcher === id) return true;
      cur = byId.get(cur.launcher);
      if (!cur || cur.subagent || st.own.has(cur.id)) return false;
    }
    return false;
  };
  return [...st.hung.values()].flat().filter(from);
}

const inside = (path: string, root: string) => path === root || path.startsWith(root.endsWith("/") ? root : root + "/");

/** A group path as a place: its machine, its path there, its name (its group's, if one is rooted
 *  there; else its last folder). */
function placeAt(key: string, workspaces: Workspace[]): Omit<Spot, "kind" | "sessions"> {
  const m = machineOf(key);
  const path = m ? key.slice(m.length + 1) : key;
  return { key, place: m ? { machine: m, path } : { path }, name: workspaces.find((w) => w.path === key)?.name || baseName(path) };
}

/** The places s works in (Spot), or none: none dispatched, none named. named: the places its
 *  messages name, as the page has them (machine by ID, '/'-separated). A place named that one it
 *  dispatched to is in, or is its own folder, is not drawn again; its own folder alone, none. */
export function spotsOf(s: Session, list: Session[], named: NamedPlace[], workspaces: Workspace[] = []): Spot[] {
  if (s.subagent) return [];
  const sent = dispatchedFrom(s.id, list, workspaces);
  if (!sent.length && !named.length) return [];
  const out: Spot[] = [{ kind: "own", ...placeAt(groupPath(s), workspaces), sessions: [] }];
  for (const c of sent) {
    const key = groupPath(c);
    const x = out.find((o) => o.key === key);
    if (x) x.sessions.push(c);
    else out.push({ kind: "sent", ...placeAt(key, workspaces), sessions: [c] });
  }
  for (const p of named) {
    const key = p.machine ? `${p.machine}:${p.path}` : p.path;
    if (out.some((o) => o.key === key || (o.kind === "sent" && inside(o.key, key)))) continue;
    out.push({ kind: "named", key, place: p.machine ? { machine: p.machine, path: p.path } : { path: p.path }, name: p.name, sessions: [] });
  }
  // Only its own folder named: nothing to say.
  return out.length > 1 || out[0]!.sessions.length ? out : [];
}
