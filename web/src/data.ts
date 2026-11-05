// What the page asks for besides the carried states: workspaces, this terminal, the settings, the
// update state, the other terminals. Each a query; a route not served yet (src/api/client.ts
// "assumed") leaves its part empty rather than failing the page.
import { useQuery } from "@tanstack/react-query";
import type { Endpoint, Me, Settings, UpdateState, Workspace } from "./api/assumed";
import { api, missing } from "./api/client";
import type { Session } from "./api/types.gen";

/** Undefined on a 404: the route is not there yet. */
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

export const useWorkspaces = () => useQuery({ queryKey: ["workspaces"], queryFn: orNone(api.workspaces), staleTime: 60_000 });
export const useSettings = () => useQuery({ queryKey: ["settings"], queryFn: orNone(api.settings), staleTime: 60_000 });
export const useUpdate = () => useQuery({ queryKey: ["update"], queryFn: orNone(api.update), staleTime: 3_600_000 });
export const useEndpoints = () => useQuery({ queryKey: ["endpoints"], queryFn: orNone(api.endpoints), staleTime: 300_000 });

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

/** A workspace the list names that the page does not know: the workspaces are asked for again. */
export function knowsAll(list: Session[] | undefined, ws: Workspace[] | null | undefined) {
  if (!list || ws === null || ws === undefined) return true;
  const ids = new Set(ws.map((w) => w.id));
  return list.every((s) => !s.workspace || ids.has(s.workspace));
}
