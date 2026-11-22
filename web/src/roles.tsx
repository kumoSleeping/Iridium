// Roles (docs/channels.md "角色"; kumo, 2026-11-16): a card (name, intro, persona, face,
// background) kept with an agent and a model, to start sessions as; no effort, chosen at each
// start as for any agent (otae, 2026-11-21). What every place that shows them shares: the list,
// one's face, its line ("DeepSeek · Flash · 高"), its card, the menu that picks the model and the
// effort for one start, and what a start as one sends. Shown on the
// "+" (create.tsx), first in a channel's 邀请 (chat/channel/invite.tsx), kept in the settings
// (rolespage.tsx).
import { FaceGlow } from "./glow";
import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { LoaderCircle } from "lucide-react";
import { lazy, Suspense, useState } from "react";
import { api, missing, queryClient } from "./api/client";
import type { Model, Role } from "./api/types.gen";
import { choicesQ, draftDefault, type At } from "./chat/choices";
import { t } from "./i18n";
import { zh, type Key } from "./i18n/zh";
import { getCarried, useCarried } from "./store";
import { agentName, cn, Logo } from "./ui";

/** The model and effort menu an agent not yet started has (the invite menu's): fetched when a
 *  role's menu first opens, so the first screen does not carry it. */
const DraftModels = lazy(() => import("./chat/channel/pick").then((m) => ({ default: m.DraftModels })));

/** The roles, in their order; none on a daemon that has no such route. Asked again as any role
 *  changes: the carried "roles", a count (internal/server rolesChanged). */
export function useRoles(): Role[] {
  const v = useCarried<number>("roles") ?? 0;
  const q = useQuery({ ...rolesQ(v), placeholderData: keepPreviousData });
  return q.data ?? NONE;
}

/** The roles as asked for, by the carried "roles" count. */
const rolesQ = (v: number) => ({ queryKey: ["roles", v], queryFn: () => api.roles().catch((e) => (missing(e) ? [] : Promise.reject(e))), staleTime: 30_000 });

/** The roles asked for ahead (kumo, 2026-11-16: a row that comes after the "+" opened pushes
 *  the popover; docs/前端设计规范.md "后到的不撑开"): once the page is idle, and as the "+"
 *  is pointed at or pressed. */
export const prefetchRoles = () => void queryClient.prefetchQuery(rolesQ(getCarried<number>("roles") ?? 0));
const NONE: Role[] = [];

/** What a start as a role takes for that one session: its model and preset ("" or none: the
 *  role's), its effort ("" or none: the agent's own). */
export type RolePick = { model?: string; effort?: string; preset?: string };

/** A role as it would start: the role, and the effort of that start. */
export type RoleNow = Role & { effort?: string };

/** The role as it would start with pick. */
export const picked = (role: Role, pick: RolePick = {}): RoleNow => ({ ...role, model: pick.model || role.model, effort: pick.effort || undefined, preset: pick.preset || role.preset });

/** The entry of models the role's model is: by its value, else the id it resolves to. */
const modelOf = (role: Role, models?: Model[]) => (role.model ? (models?.find((m) => m.value === role.model) ?? models?.find((m) => m.resolved === role.model)) : undefined);

/** The role's model as shown: its name among its kind's models, else its id. */
export const roleModel = (role: Role, models?: Model[]) => modelOf(role, models)?.name ?? role.model ?? "";

/** An effort by its name in the page's language; one the page has no name for as its model
 *  names it (dsh's providers name theirs), else as it is. */
export const effortName = (level: string, m?: Model) => (`effort.${level}` in zh ? t(`effort.${level}` as Key) : (m?.effortNames?.[level] ?? level));

/** A role's line: its agent, model and the effort picked for a start ("DeepSeek · Flash · 高");
 *  what is not set is left out. agent false: its agent is shown beside it (its logo), not named. */
export const roleLine = (role: RoleNow, models?: Model[], agent = true) => [agent ? agentName(role.agent) : "", roleModel(role, models), role.effort ? effortName(role.effort, modelOf(role, models)) : ""].filter(Boolean).join(" · ");

/** What starting as role sends, in a new session (NewSession) or an invite (NewMember): the
 *  role, and only what pick changes of it. */
export function roleStart(role: Role, pick: RolePick = {}): { role: string; model?: string; effort?: string; preset?: string } {
  const differs = (v: string | undefined, own: string | undefined) => (v && v !== (own ?? "") ? v : undefined);
  const model = differs(pick.model, role.model);
  const effort = pick.effort || undefined;
  const preset = differs(pick.preset, role.preset);
  return { role: role.id, ...(model && { model }), ...(effort && { effort }), ...(preset && { preset }) };
}

/** A role's face, round: its own, else its agent's logo. */
export function RoleFace({ role, size, className }: { role: Role; size: number; className?: string }) {
  const [broken, setBroken] = useState<string | null>(null);
  return (
    <span className={cn("relative grid shrink-0 place-items-center overflow-hidden rounded-full bg-fill", className)} style={{ width: size, height: size }}>
      {role.avatar && broken !== role.avatar ? <img src={role.avatar} alt="" draggable={false} onError={() => setBroken(role.avatar!)} className="size-full object-cover" /> : <Logo agent={role.agent} size={Math.round(size * 0.64)} />}
    </span>
  );
}

/** A role's background as a banner filling its parent; none: its face, out of focus. */
export function RoleBanner({ role, className }: { role: Role; className?: string }) {
  const [broken, setBroken] = useState<string | null>(null);
  const has = role.background && broken !== role.background;
  return (
    <span className={cn("relative block overflow-hidden bg-fill-2", className)}>
      {has ? (
        <img src={role.background} alt="" draggable={false} onError={() => setBroken(role.background!)} className="size-full object-cover" />
      ) : (
        <FaceGlow>
          <RoleFace role={role} size={32} />
        </FaceGlow>
      )}
    </span>
  );
}

/** A role's line where its agent would run (at), its model by its kind's name for it. logo:
 *  where room is short, the agent by its logo, not its name. */
export function RoleLine({ role, at, logo, className }: { role: RoleNow; at: At; logo?: boolean; className?: string }) {
  const models = useQuery(choicesQ(role.agent, at)).data?.models;
  if (!logo) return <span className={className}>{roleLine(role, models)}</span>;
  return (
    <span className={cn("flex min-w-0 items-center gap-1", className)}>
      <Logo agent={role.agent} size={12} className="shrink-0" />
      <span className="sr-only">{agentName(role.agent)}</span>
      <span className="min-w-0 truncate">{roleLine(role, models, false)}</span>
    </span>
  );
}

/** The model and effort a start as role takes, in a menu's popup: the role's model ticked till
 *  changed, the agent's own effort till one is picked, as for any agent. A model picked or a
 *  level let go of is told (set), nothing is started. */
export function RoleModels({ role, at, pick, set }: { role: Role; at: At; pick: RolePick; set: (p: RolePick) => void }) {
  const asked = useQuery(choicesQ(role.agent, at)).data;
  const now = picked(role, pick);
  return (
    <Suspense fallback={<LoaderCircle size={15} className="mx-auto my-2.5 animate-spin text-ink-4" />}>
      {/* The agent's own default comes as "": named here, the role's model being another. */}
      <DraftModels agent={role.agent} at={at} model={now.model ?? ""} effort={now.effort ?? ""} preset={now.preset} set={(x) => set({ ...pick, ...x, ...(x.model === "" ? { model: draftDefault(asked).def?.value ?? "" } : {}) })} />
    </Suspense>
  );
}

