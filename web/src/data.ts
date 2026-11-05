// What the page asks for besides the carried states: this terminal, the agents, the settings, the
// other terminals, the home folder. Each a query; a route an older daemon lacks leaves its part
// empty rather than failing the page. The workspaces and the update are carried on the stream,
// asked for once at first.
import { useQuery } from "@tanstack/react-query";
import { api, missing } from "./api/client";
import type { AgentInfo, Me, Settings, Terminal, UpdateState, Workspace } from "./api/types.gen";
import { useCarried, useUpdateState } from "./store";
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
export const useAgents = () => useQuery({ queryKey: ["agents"], queryFn: orNone(api.agents), staleTime: Infinity });
export const useAgent = (a: string) => useAgents().data?.find((x: AgentInfo) => x.agent === a);
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
export const LOCAL_ME: Me = { id: "local", name: "本机", kind: "channel", local: true, panel: true, public: false, usedToday: 0, rules: { agents: ["claude", "codex", "pi", "dsh"], allDirs: true, dirs: [], workspaces: [], seeOthers: true, filesIn: true, filesOut: true, answer: true, mustApprove: false, dailyTokens: 0, allCommands: true, commands: [] } };

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

export type { Me, Settings, UpdateState, Workspace };
