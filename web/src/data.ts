// What the page asks for besides the carried states: this terminal, the agents, the settings, the
// update state, the other terminals, the home folder. Each a query; a route not served yet (src/api/
// client.ts "assumed") leaves its part empty rather than failing the page. The workspaces are
// carried on the stream, asked for once at first.
import { useQuery } from "@tanstack/react-query";
import type { Endpoint, Me, Settings, UpdateState } from "./api/assumed";
import { api, missing } from "./api/client";
import type { AgentInfo, Workspace } from "./api/types.gen";
import { useCarried } from "./store";
import { setHome } from "./ui";

/** null on a 404: the route is not there yet. */
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
export const useUpdate = () => useQuery({ queryKey: ["update"], queryFn: orNone(api.update), staleTime: 3_600_000 });
export const useEndpoints = () => useQuery({ queryKey: ["endpoints"], queryFn: orNone(api.endpoints), staleTime: 300_000 });
/** The home folder, as the folder picker resolves ~. */
export const useHome = () => useQuery({ queryKey: ["folders", "~"], queryFn: () => api.folders("~"), staleTime: 60_000, select: (f) => (setHome(f.path), f.path) });

/** This terminal: /api/me, else the gate's session (served once the gate is mounted), else 本机. */
export const useMe = () =>
  useQuery({
    queryKey: ["me"],
    staleTime: 300_000,
    queryFn: async (): Promise<Me> => {
      const me = await orNone(api.me)().catch(() => null);
      if (me) return me;
      const s = await orNone(api.gateSession)().catch(() => null);
      if (s && !s.local) return { id: "", name: s.name ?? "", kind: "panel", local: false };
      return { id: "local", name: "本机", kind: "channel", local: true };
    },
  });

export type { Endpoint, Me, Settings, UpdateState, Workspace };
