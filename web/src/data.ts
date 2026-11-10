// What the page asks for besides the carried states: this terminal, the agents, the settings, the
// other terminals, the home folder. Each a query; a route an older daemon lacks leaves its part
// empty rather than failing the page. The workspaces and the update are carried on the stream,
// asked for once at first.
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect } from "react";
import { api, missing, speakAs } from "./api/client";
import type { AgentInfo, Apps, LastSaid, Me, Session, Settings, UpdateState, Workspace } from "./api/types.gen";
import type { Machine } from "./machines";
import { sourceOf, type Who } from "./chat/refs";
import { t } from "./i18n";
import { useCarried, useList, useUpdateState } from "./store";
import { setHome } from "./ui";

/** null on a 404: the route is not there. */
const orNone =
  <T,>(f: () => Promise<T>) =>
  async (): Promise<T | null> => {
    try {
      return await f();
    } catch (e) {
      if (missing(e)) return null;
      throw e;
    }
  };

/** The workspaces: as the stream carries them, else as asked for. */
export function useWorkspaces(): { data: Workspace[] | null | undefined } {
  const carried = useCarried<Workspace[]>("workspaces");
  const q = useQuery({ queryKey: ["workspaces"], queryFn: orNone(api.workspaces), staleTime: Infinity, enabled: carried === undefined });
  return { data: carried ?? q.data };
}
export const agentsQ = { queryKey: ["agents"], queryFn: orNone(api.agents), staleTime: Infinity };
export const useAgents = () => useQuery(agentsQ);
export const useAgent = (a: string) => useAgents().data?.find((x: AgentInfo) => x.agent === a);
/** Push's keys and each workspace's events, the daemon's own (GET /api/web/push): the settings show them. */
export const pushQ = { queryKey: ["push"], queryFn: orNone(api.push), staleTime: 30_000 };
export const useSettings = () => useQuery({ queryKey: ["settings"], queryFn: orNone(api.settings), staleTime: 60_000 });
/** The update: as the stream carries it, else as asked for. */
export function useUpdate(): { data: UpdateState | null | undefined } {
  const carried = useUpdateState();
  const q = useQuery({ queryKey: ["update"], queryFn: orNone(api.update), staleTime: 3_600_000, enabled: carried === undefined });
  return { data: carried ?? q.data };
}
/** The home folder, as the folder picker resolves ~. */
export const useHome = () => useQuery({ queryKey: ["folders", "~"], queryFn: () => api.folders("~"), staleTime: 60_000, select: (f) => (setHome(f.path), f.path) });

/** The host, as the page is with no gate in front (and while /api/me has not answered). */
export const LOCAL_ME: Me = { id: "local", name: "主机", app: "local", host: true, public: false, usedToday: 0, rules: { claudeOwner: { by: "local", at: 0 } } };

/** Who this page is: GET /api/me, else the gate's session (a login of ir_web), else the host. */
export const useMe = () =>
  useQuery({
    queryKey: ["me"],
    staleTime: 300_000,
    queryFn: async (): Promise<Me> => {
      const me = await orNone(api.me)().catch(() => null);
      if (me) {
        speakAs(me.app, me.login || me.app);
        return me;
      }
      const s = await orNone(api.gateSession)().catch(() => null);
      if (s && !s.local) return { ...LOCAL_ME, id: "", name: s.name ?? "", app: "ir_web", host: false, web: true, public: true };
      return LOCAL_ME;
    },
  });

/** The host and ir_web's logins manage the apps (until S2's permissions). */
export const manages = (me?: Me | null) => !!me && (!!me.host || !!me.web);

/** The last count of app changes acted on (useApps). */
let terminalsSeen: number | undefined;

/** The apps (the host and ir_web among them) with their computers, the logins and the unused invites
 *  (GET /api/apps): what a message's source shows (avatar), the computers new sessions may start at
 *  (machines.tsx) and the settings' apps page. Every caller gets the public part; one that manages,
 *  all (full). */
export function useApps() {
  const me = useMe().data;
  const may = !!me;
  // The stream's count of terminal changes (a Bot's avatar came, one renamed): each new one fetches
  // them again (the user, 2026-11-08: a Bot's avatar did not show till a reload).
  const changed = useCarried<number>("terminals");
  const qc = useQueryClient();
  useEffect(() => {
    if (changed === undefined || changed === terminalsSeen) return;
    const first = terminalsSeen === undefined;
    terminalsSeen = changed; // once for all the hook's users
    if (!first) void qc.invalidateQueries({ queryKey: ["apps"] });
  }, [changed, qc]);
  return useQuery({ queryKey: ["apps"], queryFn: orNone(api.apps), staleTime: 60_000, enabled: may });
}

const NO_MACHINES: Machine[] = [];
const machinesOf = new WeakMap<Apps, Machine[]>();
/** The apps' computers as the page's machines (machines.tsx): connected or not; the agents turned
 *  off there left out (not offered for new sessions). */
export function useMachines(): Machine[] {
  const d = useApps().data;
  if (!d) return NO_MACHINES;
  let ms = machinesOf.get(d);
  if (!ms) {
    ms = d.apps
      .filter((a) => a.computer && !a.host)
      .map((a) => ({ ...a.computer!, id: a.id, name: a.name, avatar: a.avatar, agents: a.computer!.agents.filter((x) => !x.off) }))
      .sort((a, b) => a.name.toLowerCase().localeCompare(b.name.toLowerCase()));
    machinesOf.set(d, ms);
  }
  return ms;
}

/** Everyone a message may come from: the apps and the host, and ir_web's logins (web). */
export function useWhoList(): Who[] {
  const d = useApps().data;
  if (!d) return [];
  return [...d.apps.filter((a) => !a.web).map((a) => ({ id: a.id, name: a.name, avatar: a.avatar, device: a.device, host: a.host, type: a.type })), ...d.logins.map((l) => ({ id: l.id, name: l.name, avatar: l.avatar, device: l.device, web: true }))];
}

/** The last thing said, on one line: yours after 你：, another's after its name. Yours: none tagged
 *  (but in a subagent), or a panel's or 本机's. An agent's (a subagent's prompt, another session's
 *  ir) goes by that session's name, as the chat's letter does (sourceOf). of: the session it is in. */
export function useSaidLine() {
  const terminals = useWhoList();
  const sessions = useList() ?? [];
  // By the sender's ID where the transcript has it: a name is unique only among the apps, or among
  // ir_web's logins.
  const yours = (l: LastSaid) => {
    const e = l.fromTerminal && terminals.find((x) => x.id === l.fromTerminal);
    if (e) return !!(e.web || e.host);
    if (l.fromTerminal === "local") return true;
    return !l.from || l.from === "主机" || l.from === "本机" || (!l.fromTerminal && terminals.some((x) => x.name === l.from && (x.web || x.host)));
  };
  const by = (l: LastSaid, of?: Pick<Session, "subagent" | "parent" | "workflow">) => {
    if ((!l.from && of?.subagent) || /^(?:ir|session):/.test(l.from ?? "")) {
      const src = sourceOf(l.from, undefined, LOCAL_ME, terminals, sessions, [], of);
      return src.kind === "letter" && src.name ? `${src.name}: ` : "";
    }
    return yours(l) ? t("jump.you") : `${l.from}: `;
  };
  return (l?: LastSaid, of?: Pick<Session, "subagent" | "parent" | "workflow">) => l && `${l.role === "user" ? by(l, of) : ""}${l.text}`;
}

export type { Me, Settings, UpdateState, Workspace };
