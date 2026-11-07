// What the page asks for besides the carried states: this terminal, the agents, the settings, the
// other terminals, the home folder. Each a query; a route an older daemon lacks leaves its part
// empty rather than failing the page. The workspaces and the update are carried on the stream,
// asked for once at first.
import { useQuery } from "@tanstack/react-query";
import { api, missing } from "./api/client";
import type { AgentInfo, LastSaid, Me, Session, Settings, Terminal, UpdateState, Workspace } from "./api/types.gen";
import { sourceOf } from "./chat/refs";
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
/** Push's keys and each workspace's events, the daemon's own (GET /api/push): the settings show them. */
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

/** 本机, as the page is with no gate in front (and while /api/me has not answered). */
export const LOCAL_ME: Me = { id: "local", name: "本机", kind: "channel", local: true, panel: true, public: false, usedToday: 0, rules: { claudeOwner: { by: "local", at: 0 } } };

/** This terminal: GET /api/me, else the gate's session, else 本机. */
export const useMe = () =>
  useQuery({
    queryKey: ["me"],
    staleTime: 300_000,
    queryFn: async (): Promise<Me> => {
      const me = await orNone(api.me)().catch(() => null);
      if (me) return me;
      const s = await orNone(api.gateSession)().catch(() => null);
      if (s && !s.local) return { ...LOCAL_ME, id: "", name: s.name ?? "", kind: "panel", local: false, public: true };
      return LOCAL_ME;
    },
  });

/** The interactive terminals and the unused invites (GET /api/terminals): what a message's source
 *  shows (avatar, kind) and the settings' 终端 page. Only for a terminal that manages. */
export function useTerminals() {
  const me = useMe().data;
  const manages = !!me && (me.local || me.kind === "panel");
  return useQuery({ queryKey: ["terminals"], queryFn: orNone(api.terminals), staleTime: 60_000, enabled: manages });
}
export const useTerminalList = (): Terminal[] => useTerminals().data?.terminals ?? [];

/** The last thing said, on one line: yours after 你：, another's after its name. Yours: none tagged
 *  (but in a subagent), or a panel's or 本机's. An agent's (a subagent's prompt, another session's
 *  ir) goes by that session's name, as the chat's letter does (sourceOf). of: the session it is in. */
export function useSaidLine() {
  const terminals = useTerminalList();
  const sessions = useList() ?? [];
  const yours = (from?: string) => !from || from === "本机" || terminals.some((x) => x.name === from && (x.kind === "panel" || x.local));
  const by = (l: LastSaid, of?: Pick<Session, "subagent" | "parent" | "workflow">) => {
    if ((!l.from && of?.subagent) || /^(?:ir|session):/.test(l.from ?? "")) {
      const src = sourceOf(l.from, undefined, LOCAL_ME, terminals, sessions, [], of);
      return src.kind === "letter" && src.name ? `${src.name}: ` : "";
    }
    return yours(l.from) ? t("jump.you") : `${l.from}: `;
  };
  return (l?: LastSaid, of?: Pick<Session, "subagent" | "parent" | "workflow">) => l && `${l.role === "user" ? by(l, of) : ""}${l.text}`;
}

export type { Me, Settings, UpdateState, Workspace };
