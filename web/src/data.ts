// What the page asks for besides the carried states: this terminal, the agents, the settings, the
// other terminals, the home folder. Each a query; a route an older daemon lacks leaves its part
// empty rather than failing the page. The workspaces and the update are carried on the stream,
// asked for once at first.
import { useQueryClient } from "@tanstack/react-query";
import { useQuery } from "./api/query";
import { useEffect, useMemo } from "react";
import { api, ApiError, missing, speakAs } from "./api/client";
import type { AgentInfo, App, Apps, LastSaid, Me, Session, Settings, UpdateState, Workspace } from "./api/types.gen";
import type { Machine } from "./machines";
import { sourceOf, type Who } from "./chat/refs";
import { t } from "./i18n";
import { useCarried, useList, useUpdateState } from "./store";
import { setHome } from "./ui";
import { useGuilds } from "./guilds";

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

/** The workspaces (folder groups), in their order: as the stream carries them, else as asked for. */
export function useWorkspaces(): { data: Workspace[] | null | undefined } {
  const carried = useGuilds()?.guilds;
  const q = useQuery({ queryKey: ["workspaces"], queryFn: orNone(api.workspaces), staleTime: Infinity, enabled: carried === undefined });
  return { data: carried ?? q.data };
}
export const agentsQ = { queryKey: ["agents"], staleTime: 3_600_000, ...keptQuery("agents", orNone(api.agents)) };
/** One's own card: 编辑资料 of one's own card. */
export const selfQ = { queryKey: ["self"], queryFn: () => api.self(), retry: false };
export const useAgents = () => useQuery(agentsQ);
export const useAgent = (a: string) => useAgents().data?.find((x: AgentInfo) => x.agent === a);
export const useSettings = () => useQuery({ queryKey: ["settings"], staleTime: 60_000, ...keptQuery("settings", orNone(api.settings)) });
/** The update: as the stream carries it, else as asked for. */
export function useUpdate(): { data: UpdateState | null | undefined } {
  const carried = useUpdateState();
  const q = useQuery({ queryKey: ["update"], queryFn: orNone(api.update), staleTime: 3_600_000, enabled: carried === undefined });
  return { data: carried ?? q.data };
}
/** The home folder, as the folder picker resolves ~. */
export const useHome = () => useQuery({ queryKey: ["folders", "~"], queryFn: () => api.folders("~"), staleTime: 60_000, select: (f) => (setHome(f.path), f.path) });

/** The host, as the page is with no gate in front (and while /v1/internal/me has not answered). */
export const LOCAL_ME: Me = { id: "local", name: "Iridium", app: "local", host: true, public: false, usedToday: 0 };

/** What this browser kept from the page before, for its first draw: who this is, whose faces the
 *  messages have. A channel is drawn from this device's copy at once as the page loads; without
 *  these it was drawn as the host's, everyone's words on the left under a letter, till the two
 *  answered (kumo, 2026-11-25). No more than names and faces; asked for afresh at once. */
function kept<T>(key: string): T | undefined {
  try {
    const v = localStorage.getItem(key);
    return v ? (JSON.parse(v) as T) : undefined;
  } catch {
    return undefined;
  }
}
function keep<T>(key: string, v: T): T {
  try {
    localStorage.setItem(key, JSON.stringify(v));
  } catch {
    // A full or closed store: not kept.
  }
  return v;
}
/** A query kept in this browser too (kumo, 2026-11-27: 管理 and the usage drew empty, then filled
 *  as their answers came): drawn at once from the copy the page before kept, asked afresh at once
 *  all the same, the answer kept again. name: its place in the store. */
export function keptQuery<T>(name: string, fn: () => Promise<T>) {
  const key = `ir:q1:${name}`;
  return {
    queryFn: async () => keep(key, await fn()),
    initialData: () => kept<T>(key),
    initialDataUpdatedAt: 0,
  };
}
const ME_KEY = "ir:me1";
const WHO_KEY = "ir:who1";

/** Who this page is: GET /v1/internal/me, else the gate's session (a login of ir_web), else the host. */
export const useMe = () =>
  useQuery({
    queryKey: ["me"],
    staleTime: 300_000,
    initialData: () => {
      const me = kept<Me>(ME_KEY);
      if (me) speakAs(me.app, me.login || me.app);
      return me;
    },
    initialDataUpdatedAt: 0,
    queryFn: async (): Promise<Me> => {
      // Refused or not there: what the gate says, else the host's page. The network or the daemon
      // a moment away (no answer, a 5xx) is neither: asked again, not taken for five minutes as
      // "this is the host" (one's own words drawn as another's, the host's keys shown).
      const away = (e: unknown) => !(e instanceof ApiError) || e.status >= 500;
      const soft = <T,>(e: unknown): T | null => {
        if (away(e)) throw e;
        return null;
      };
      const me = await orNone(api.me)().catch((e) => soft<Me>(e));
      if (me) {
        speakAs(me.app, me.login || me.app);
        return keep(ME_KEY, me);
      }
      const s = await orNone(api.gateSession)().catch((e) => soft<Awaited<ReturnType<typeof api.gateSession>>>(e));
      if (s && !s.local) return { ...LOCAL_ME, id: "", name: s.name ?? "", app: "ir_web", host: false, web: true, public: true };
      return LOCAL_ME;
    },
  });

/** The host and ir_web's logins manage the apps (until S2's permissions). */
export const manages = (me?: Me | null) => !!me && (!!me.host || !!me.web);

/** The last count of app changes acted on (useApps). */
let terminalsSeen: number | undefined;

/** The apps (the host and ir_web among them) with their computers, the logins and the unused invites
 *  (GET /v1/internal/apps): what a message's source shows (avatar), the computers new sessions may start at
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
  return useQuery({ queryKey: ["apps"], staleTime: 60_000, enabled: may, ...keptQuery("apps", orNone(api.apps)) });
}

/** A computer (k_glossary "电脑"): the host, or an app that paired as one or has said its system.
 *  Not among a channel's people: it is not invited (the owner, 2026-11-14). */
export const isComputer = (a: Pick<App, "host" | "computer" | "type">) => !!a.host || !!a.computer || a.type === "computer";

const NO_MACHINES: Machine[] = [];
const machinesOf = new WeakMap<Apps, { all: Machine[]; paired: Machine[] }>();
/** The apps' computers as the page's machines (machines.tsx): connected or not; the agents turned
 *  off there left out (not offered for new sessions). Set includeHost to include Iridium. */
export function useMachines(includeHost = false): Machine[] {
  const d = useApps().data;
  if (!d) return NO_MACHINES;
  let ms = machinesOf.get(d);
  if (!ms) {
    const all = d.apps
      .filter((a) => a.computer)
      .map((a) => ({ ...a.computer!, id: a.host ? "" : a.id, name: a.name, avatar: a.avatar, online: a.online, agents: a.computer!.agents.filter((x) => !x.off) }))
      .sort((a, b) => a.name.toLowerCase().localeCompare(b.name.toLowerCase()));
    ms = { all, paired: all.filter((m) => m.id) };
    machinesOf.set(d, ms);
  }
  return includeHost ? ms.all : ms.paired;
}

/** Everyone a message may come from: the apps and the host, and ir_web's logins (web). */
/** None: the same one each time. */
const NO_WHO: Who[] = [];
export function useWhoList(): Who[] {
  const d = useApps().data;
  // Not come yet: the one kept from the page before.
  const was = useMemo(() => kept<Who[]>(WHO_KEY) ?? NO_WHO, []);
  // The same list while the apps are the same: what is drawn from it is not drawn again each time.
  const now = useMemo(() => (d ? [...d.apps.filter((a) => !a.web).map((a) => ({ id: a.id, name: a.name, avatar: a.avatar, device: a.device, host: a.host, type: a.type })), ...d.logins.map((l) => ({ id: l.id, name: l.name, avatar: l.avatar, device: l.device, web: true }))] : undefined), [d]);
  useEffect(() => void (now && keep(WHO_KEY, now)), [now]);
  return now ?? was;
}

/** The last thing said, on one line: yours after 你：, another's after its name. Yours: none tagged
 *  (but in a subagent), or a panel's or 本机's. An agent's (a subagent's prompt, another session's
 *  ir) goes by that session's name, as the chat's letter does (sourceOf). of: the session it is in. */
export function useSaidLine() {
  const terminals = useWhoList();
  const sessions = useList() ?? [];
  const me = useMe().data ?? LOCAL_ME;
  // By the sender's ID where the transcript has it: a name is unique only among the apps, or among
  // ir_web's logins. Yours: this login's, or the machine's own; another login's goes by its name
  // (kumo, 2026-11-16: every login's was 你).
  const mine = (x: Who) => x.id === me.id || !!x.host;
  const yours = (l: LastSaid) => {
    const e = l.fromTerminal && terminals.find((x) => x.id === l.fromTerminal);
    if (e) return mine(e);
    if (l.fromTerminal === "local" || l.fromTerminal === me.id) return true;
    return !l.from || l.from === "Iridium" || l.from === "主机" || l.from === "本机" || (!l.fromTerminal && terminals.some((x) => x.name === l.from && mine(x)));
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
