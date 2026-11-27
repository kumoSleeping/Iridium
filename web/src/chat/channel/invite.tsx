// Adding agents to a channel (docs/channels.md "邀请"; kumo, 2026-11-25), under the + beside the
// members' key at the bar's right (top.tsx). At its top the computers, one picked (AddAgents);
// under them the templates (roles: face and name, a press adds one as that role) and the kinds
// of agent, each row naming the model a press of it uses ("Claude · Opus 5.5": never "default",
// the user, 2026-11-16) and opening the model and effort menu the chat's own model key has. A
// kind's row pressed adds it with that model; a model picked there adds it with that one (kumo,
// 2026-11-15: no filled 添加 key). On a phone (no hover) a press opens the menu, a row there adds.
// The agent joins the channel as a session of its own, ready. The same kind may be added again.
// One of another computer runs there, in that computer's home folder (docs/channels.md "别的电脑
// 的 agent").
import { Menu } from "@base-ui/react/menu";
import { useQueryClient } from "@tanstack/react-query";
import { useQuery } from "../../api/query";
import { LoaderCircle, Mail, Plus, UsersRound } from "lucide-react";
import { useEffect, useRef, useState, type CSSProperties, type ReactNode } from "react";
import { api } from "../../api/client";
import type { Agent, ChannelMembers, Role, Trigger } from "../../api/types.gen";
import { agentsQ, useMachines } from "../../data";
import { t, useLang } from "../../i18n";
import { MachineFace, type Machine } from "../../machines";
import { picked, RoleFace, RoleLine, roleStart, useRoles, type RoleNow } from "../../roles";
import { useList, useSession } from "../../store";
import { toast } from "../../toast";
import { AGENTS, agentName, cn, Logo, MenuPortal, useLongPress } from "../../ui";
import { useMembers } from "./gate";
import { memberError } from "./key";
import { Deeper, usePages } from "./pages";
import { choicesQ, draftDefault, draftEffort, draftName, rememberEffort, useLastEffort, type At } from "../choices";
import { CARD_POP, ProfileCard } from "./profilecard";
import { DraftModels, type Draft } from "./pick";
import { ahead as figAhead } from "../../splash/engine";
import { panelGone, point, unpoint, type Fig } from "../../splash/figure";

/** A row pointed at or come to by the keys: the splash draws what it would add (splash/). */
const pointing = (f: Fig) => ({ onPointerEnter: () => point(f), onFocus: () => point(f) });

/** A row's menu beside it, opened by pointing at it as before, and by a finger held on the row
 *  or the right button too (kumo, 2026-11-25: on a wide touch screen, an unfolded phone, there is
 *  no pointing, and a press adds at once: there was no way to the models). The press that ends a
 *  hold adds nothing. */
function useHeld() {
  const [open, setOpen] = useState(false);
  const set = setOpen;
  const lp = useLongPress(() => set(true), { suppressTouchClick: true });
  // Whether its menu was out as the row was pressed: then a press adds; with none out, a press
  // brings it out, not adding at once (kumo, 2026-11-25: on an unfolded phone, wide but a finger,
  // a tap added with no way to its models). A mouse's pointing brings it out before it presses.
  const was = useRef(false);
  const press = (add: () => void) => () => (was.current ? add() : set(true));
  return { open, set, press, keys: { ...lp, onPointerDown: (e: React.PointerEvent) => ((was.current = open), lp.onPointerDown?.(e)), onContextMenu: (e: React.MouseEvent) => void (lp.onContextMenu(e) || (e.preventDefault(), set(true))) } };
}

/** In a page of the panel (the portrait layout, where nothing is pointed at): while the page is
 *  there the splash draws what it is of. */
function Points({ fig }: { fig: Fig }) {
  useEffect(() => (point(fig), () => unpoint(fig.key)), [fig.key]);
  return null;
}

export const ITEM = "flex h-8 cursor-default items-center gap-2.5 rounded-md px-2.5 text-base outline-none select-none data-highlighted:bg-fill data-popup-open:bg-fill pointer-coarse:h-9";
export const POPUP = "g-unfold glass thick float rounded-lg text-ink outline-none";

/** How many agents a channel holds (internal/server MaxAgents). */
export const MAX_AGENTS = 8;

/** What wakes one added beside its @ (kumo, 2026-11-25): only the one adding it (self: the
 *  channel's first agent's at first), only an @ (at: one added after it), or every member's
 *  words (all). A press goes on to the next. */
export type Wake = "self" | "at" | "all";
export const WAKES: Wake[] = ["self", "at", "all"];
const wakeOn = (w: Wake, me?: string): Trigger[] => (w === "all" ? [{ message: { from: ["*"] } }] : w === "self" && me ? [{ message: { from: [me] } }] : []);

/** Adds an agent to channel id and keeps the members it answers with; it says hello as it
 *  joins (greet; kumo, 2026-11-16). as: as a role (what
 *  roleStart gives; busy is then "role:<id>"). m: at that computer, in its home folder (busy:
 *  "<machine>/<agent>"). wake: what wakes it beside its @ (Wake; none given, only its @). attach:
 *  the key it starts with pressed (a button's id, pick.tsx). */
export function useAdd(id: string, wake?: Wake, done?: () => void) {
  const qc = useQueryClient();
  const me = useMembers(id)?.me;
  const [busy, setBusy] = useState<string | null>(null);
  const add = async (agent: Agent, model: string, effort: string, preset = "", as?: ReturnType<typeof roleStart>, m?: Machine, attach = "") => {
    setBusy(as ? `role:${as.role}` : m ? `${m.id}/${agent}` : agent);
    try {
      qc.setQueryData<ChannelMembers>(["members", id], await api.addMember(id, { ...(as ?? { agent, ...(model ? { model } : {}), ...(effort ? { effort } : {}), ...(preset ? { preset } : {}) }), ...(attach ? { attach } : {}), ...(m ? { machine: m.id, cwd: m.home } : {}), ...(wake ? { on: wakeOn(wake, me) } : {}), greet: true }));
      // Added: the panel goes, its menus with it (kumo, 2026-11-25: it stayed over the channel).
      done?.();
    } catch (e) {
      toast(memberError(e), { error: true });
    } finally {
      setBusy(null);
    }
  };
  return { busy, add };
}

/** The agents to add, inside a menu (kumo, 2026-11-25): the computers in one row at its top
 *  (scrolled sideways, no bar; the channel's own picked as it opens), then the templates (roles)
 *  and the kinds of agent of the one picked. Every template and every kind any computer has
 *  keeps its row whichever is picked, one the picked computer lacks drawn faint and not pressed:
 *  the menu is as tall for each computer. */
/** The tab of the apps among the computers' (AddAgents). */
const APPS = "\u0000apps";

export function AddAgents({ id, wake, setWake, done, apps }: { id: string; wake: Wake; setWake: (w: Wake) => void; done?: () => void; apps?: ReactNode }) {
  useLang();
  const row = useList()?.find((x) => x.id === id);
  const own = (useSession(id) ?? row)?.machine ?? "";
  const machines = useMachines(true);
  const [picked, setPicked] = useState(own);
  const onApps = picked === APPS;
  const m = machines.find((x) => x.id === picked);
  // The tabs (kumo, 2026-11-25): Iridium's own first, then the apps (the people: 网页, the Bots),
  // then the other computers.
  const host = machines.find((x) => !x.id);
  const others = machines.filter((x) => x.id);
  const waking = { wake, setWake };
  const info = useQuery(agentsQ).data;
  const local = info?.filter((a) => !a.missing).map((a) => a.agent) ?? [];
  const kindsOf = (x?: Machine): Agent[] => (!x || !x.id ? local : x.online ? x.agents.map((a) => a.agent as Agent) : []);
  const has = kindsOf(m);
  // The rows: the kinds any computer has, in the kinds' own order.
  const every = new Set<Agent>([...local, ...machines.flatMap((x) => (x.id ? x.agents.map((a) => a.agent as Agent) : []))]);
  const kinds = AGENTS.filter((k) => every.has(k));
  const roles = useRoles();
  const { busy, add } = useAdd(id, wake, done);
  const full = (row?.agents?.length ?? 1) >= MAX_AGENTS;
  const off = full || busy !== null;
  // Where one added runs when not at the channel's own computer: the one picked, its home folder.
  const to = m && picked !== own ? m : undefined;
  // The panel gone: the splash is the channel's own again. The templates' faces asked for as it
  // is drawn: one pointed at is there to be drawn.
  useEffect(() => () => panelGone(), []);
  useEffect(() => roles.forEach((r) => r.avatar && figAhead({ key: r.id, src: r.avatar })), [roles]);
  return (
    <>
      {(machines.length > 1 || apps) && (
        // As wide as the menu is without it: the row scrolls, it does not widen the menu.
        <div className="no-bar mb-1 flex w-0 min-w-full gap-1 overflow-x-auto">
          {host && (
            <button type="button" aria-pressed={picked === ""} onClick={() => setPicked("")} className={cn("g-press flex h-7 shrink-0 items-center gap-1.5 rounded-md px-2 text-sm text-ink-2 outline-none select-none hover:bg-fill disabled:opacity-40 disabled:grayscale aria-pressed:bg-fill aria-pressed:text-ink pointer-coarse:h-9")}>
              <MachineFace m={host} size={14} />
              <span className="max-w-32 truncate">{t("term.local")}</span>
            </button>
          )}
          {apps && (
            <button type="button" aria-pressed={onApps} onClick={() => setPicked(APPS)} className={cn("g-press flex h-7 shrink-0 items-center gap-1.5 rounded-md px-2 text-sm text-ink-2 outline-none select-none hover:bg-fill disabled:opacity-40 disabled:grayscale aria-pressed:bg-fill aria-pressed:text-ink pointer-coarse:h-9")}>
              <UsersRound size={14} />
              <span className="max-w-32 truncate">{t("channel.app")}</span>
            </button>
          )}
          {others.map((x) => (
            <button key={x.id} type="button" disabled={!x.online} aria-pressed={x.id === picked} onClick={() => setPicked(x.id)} className={cn("g-press flex h-7 shrink-0 items-center gap-1.5 rounded-md px-2 text-sm text-ink-2 outline-none select-none hover:bg-fill disabled:opacity-40 disabled:grayscale aria-pressed:bg-fill aria-pressed:text-ink pointer-coarse:h-9")}>
              <MachineFace m={x} size={14} />
              <span className="max-w-32 truncate">{x.name}</span>
            </button>
          ))}
        </div>
      )}
      {/* The agents and the apps in one place, as tall as the taller: the panel is as tall for
          each tab. */}
      <div className="grid">
        <div inert={onApps || undefined} className={cn("col-start-1 row-start-1 flex min-w-0 flex-col", onApps && "invisible")}>
      {roles.map((r, i) => (
        <RoleRow key={r.id} role={r} at={to ? { machine: to.id } : { channel: id }} i={i} off={off || !has.includes(r.agent) || (!!r.machine && r.machine !== picked)} busy={busy === `role:${r.id}`} add={(effort) => void add(r.agent, "", "", "", roleStart(r, { effort }), to)} {...waking} />
      ))}
      {roles.length > 0 && <div role="separator" className="mx-2.5 my-1 border-t border-(--edge)" />}
      {kinds.map((k, i) => (
        <Kind key={`${picked}/${k}`} id={id} k={k} m={to} lacks={!has.includes(k)} i={roles.length + i} off={off || !has.includes(k)} busy={busy === (to ? `${to.id}/${k}` : k)} add={add} {...waking} />
      ))}
        </div>
        {apps && (
          <div inert={!onApps || undefined} className={cn("col-start-1 row-start-1 flex min-w-0 flex-col", !onApps && "invisible")}>
            {apps}
          </div>
        )}
      </div>
    </>
  );
}

/** One template's row: its face and name alone (kumo, 2026-11-25: a template, no model to pick
 *  as it is added; its model and effort are changed on the member afterwards). Pressed, it adds
 *  one as the role; pointed at, its card beside it (RolePreview), 添加 there too. In the phone's
 *  layout (Paged): pressed, its card is the panel's next page, added from there. */
function RoleRow({ role, at, i, off, busy, add, wake, setWake }: { role: Role; at: At; i: number; off: boolean; busy: boolean; add: (effort: string) => void } & Waking) {
  const pages = usePages();
  const held = useHeld();
  const asked = useQuery(choicesQ(role.agent, at)).data;
  const effort = draftEffort(asked, role.model ?? "", useLastEffort(role.agent));
  const shown = effort ? picked(role, { effort }) : role;
  const addRole = () => add(effort);
  const style = { "--i": i } as CSSProperties;
  const cls = cn(ITEM, "data-disabled:opacity-40");
  const of: Fig = { key: `role:${role.id}:${role.avatar ?? ""}`, src: role.avatar || undefined, agent: role.agent };
  const fig = pointing(of);
  const row = (
    <>
      {busy ? <LoaderCircle size={16} className="animate-spin text-ink-3" /> : <RoleFace role={role} size={16} />}
      <span className="min-w-0 flex-1 truncate">{role.name}</span>
    </>
  );
  if (pages)
    return (
      <Deeper row={row} title={role.name} i={i} popup="">
        {/* Its card inside the panel's padding, its corners round as the panel's rows. */}
        <Points fig={of} />
        <div className="overflow-hidden rounded-md">
          <RolePreview role={shown} at={at} off={off} busy={busy} add={() => (addRole(), pages.pop())} wake={wake} setWake={setWake} />
        </div>
      </Deeper>
    );
  return (
    <Menu.SubmenuRoot open={held.open} onOpenChange={held.set}>
      <Menu.SubmenuTrigger disabled={off} onClick={held.press(addRole)} style={style} className={cls} {...fig} {...held.keys}>
        {row}
      </Menu.SubmenuTrigger>
      <MenuPortal>
        <Menu.Positioner className="z-50" sideOffset={4} collisionPadding={8}>
          <Menu.Popup className={cn(POPUP, CARD_POP)}>
            <RolePreview role={shown} at={at} off={off} busy={busy} add={addRole} wake={wake} setWake={setWake} />
          </Menu.Popup>
        </Menu.Positioner>
      </MenuPortal>
    </Menu.SubmenuRoot>
  );
}

/** A template as its card shows it, to be seen, not changed (kumo, 2026-11-25): its face, name,
 *  what it runs as, its persona; 添加 under them. */
export function RolePreview({ role, at, off, busy, add, wake, setWake }: { role: RoleNow; at: At; off: boolean; busy: boolean; add: () => void } & Partial<Waking>) {
  return (
    <ProfileCard
      face={<RoleFace role={role} size={56} />}
      name={role.name}
      sub={<RoleLine role={role} at={at} />}
      more={role.persona ? <p className="m-0 line-clamp-6 pt-2 text-sm leading-5 whitespace-pre-wrap text-ink-2 [overflow-wrap:anywhere]">{role.persona}</p> : undefined}
      keys={[...(wake && setWake ? [{ key: "wake", icon: <Mail size={15} />, label: wakeLabel(wake), run: () => setWake(nextWake(wake)) }] : []), { icon: busy ? <LoaderCircle size={15} className="animate-spin" /> : <Plus size={15} />, label: t("role.add"), run: add, disabled: off, solid: true }]}
    />
  );
}

/** One kind's row: its name and the model a press of it uses (not known yet: the name alone,
 *  and no menu: a press adds it as it is). lacks: the computer picked has no such agent; its
 *  name alone, nothing asked of it. Its models open beside it, with no mark of it at the row's
 *  end (kumo, 2026-11-25); in the phone's layout (Paged), pressed, they are the panel's next page.
 *  */
function Kind({ id, k, m, lacks, i, off, busy, add, wake, setWake }: { id: string; k: Agent; m?: Machine; lacks?: boolean; i: number; off: boolean; busy: boolean; add: ReturnType<typeof useAdd>["add"] } & Waking) {
  const pages = usePages();
  const held = useHeld();
  // Where its models are asked: the channel's own place, or the computer it would run at.
  const at = m ? { machine: m.id } : { channel: id };
  const remembered = useLastEffort(k);
  const [p, setP] = useState<{ model: string } & Draft>({ model: "" });
  const effort = p.effort ?? remembered;
  const asked = useQuery({ ...choicesQ(k, at), enabled: !lacks }).data;
  const { send } = draftDefault(asked);
  const row = (
    <>
      {busy ? <LoaderCircle size={16} className="animate-spin text-ink-3" /> : <Logo agent={k} size={16} />}
      <span className="min-w-0 flex-1 truncate">{lacks ? agentName(k) : draftName(k, asked)}</span>
    </>
  );
  const style = { "--i": i } as CSSProperties;
  const cls = cn(ITEM, "data-disabled:opacity-40");
  const plain = () => void add(k, p.model || send, draftEffort(asked, p.model || send, effort), p.preset, undefined, m, p.attach);
  const pick = (x: Draft) => {
    if (x.effort) rememberEffort(k, x.effort);
    if (x.model !== undefined) {
      void add(k, x.model, draftEffort(asked, x.model, x.effort ?? effort), p.preset, undefined, m, p.attach);
      return;
    }
    setP({ ...p, ...x });
  };
  const of: Fig = { key: `kind:${k}`, agent: k };
  const fig = pointing(of);
  if (pages && asked?.models.length)
    return (
      <Deeper row={row} title={agentName(k)} i={i} popup="" disabled={off}>
        <Points fig={of} />
        <KindPage k={k} at={at} add={(model, eff, preset, attach) => (void add(k, model || send, draftEffort(asked, model || send, eff), preset, undefined, m, attach), pages.pop())} off={off} wake={wake} setWake={setWake} />
      </Deeper>
    );
  if (!asked?.models.length)
    return (
      <Menu.Item closeOnClick={false} disabled={off} onClick={plain} style={style} className={cls} {...fig}>
        {row}
      </Menu.Item>
    );
  return (
    <Menu.SubmenuRoot open={held.open} onOpenChange={held.set}>
      <Menu.SubmenuTrigger disabled={off} onClick={held.press(plain)} style={style} className={cls} {...fig} {...held.keys}>
        {row}
      </Menu.SubmenuTrigger>
      <MenuPortal>
        <Menu.Positioner className="z-50" sideOffset={4} collisionPadding={8}>
          <Menu.Popup className={cn(POPUP, "flex max-h-[min(560px,var(--available-height))] w-[min(300px,calc(100vw-16px))] flex-col p-1.5")}>
            <DraftModels agent={k} at={at} model={p.model} effort={effort} preset={p.preset} attach={p.attach ?? ""} set={pick} />
            <AddFoot add={plain} off={off} wake={wake} setWake={setWake} />
          </Menu.Popup>
        </Menu.Positioner>
      </MenuPortal>
    </Menu.SubmenuRoot>
  );
}

/** A kind's models as the panel's page (the phone's layout: no menu beside the row): one picked
 *  adds it with that one (add), the effort set first. */
function KindPage({ k, at, add, off, wake, setWake }: { k: Agent; at: At; add: (model: string, effort: string, preset?: string, attach?: string) => void; off: boolean } & Waking) {
  const remembered = useLastEffort(k);
  const [p, setP] = useState<{ model: string } & Draft>({ model: "" });
  const effort = p.effort ?? remembered;
  const pick = (x: Draft) => {
    if (x.effort) rememberEffort(k, x.effort);
    if (x.model !== undefined) {
      add(x.model, x.effort ?? effort, p.preset, p.attach);
      return;
    }
    setP({ ...p, ...x });
  };
  return (
    <>
      <DraftModels agent={k} at={at} model={p.model} effort={effort} preset={p.preset} attach={p.attach ?? ""} set={pick} />
      <AddFoot add={() => add(p.model, effort, p.preset, p.attach)} off={off} wake={wake} setWake={setWake} />
    </>
  );
}

type Waking = { wake: Wake; setWake: (w: Wake) => void };
const nextWake = (w: Wake) => WAKES[(WAKES.indexOf(w) + 1) % WAKES.length]!;
/** "唤醒：仅 @ 时" (kumo, 2026-11-25: a colon after 唤醒). */
const wakeLabel = (w: Wake) => t("wake.label", { w: t(`wake.${w}`) });

/** Under a kind's models (kumo, 2026-11-25): what wakes the one added, a press going on to the
 *  next, and at the right 添加, solid, with the model it would use as it is. */
function AddFoot({ add, off, wake, setWake }: { add: () => void; off: boolean } & Waking) {
  return (
    <>
      <div role="separator" className="mx-2.5 my-1 shrink-0 border-t border-(--edge)" />
      <div className="flex shrink-0 gap-1">
        <Menu.Item closeOnClick={false} onClick={() => setWake(nextWake(wake))} aria-label={wakeLabel(wake)} className={cn(ITEM, "min-w-0 flex-1")}>
          <Mail size={15} className="shrink-0 text-ink-2" />
          <span key={wake} className="g-fade truncate">{wakeLabel(wake)}</span>
        </Menu.Item>
        <Menu.Item closeOnClick={false} disabled={off} onClick={add} className={cn(ITEM, "min-w-0 shrink-0 justify-center bg-ink px-3.5 text-paper data-disabled:opacity-40 data-highlighted:bg-ink/85 data-popup-open:bg-ink")}>
          <Plus size={15} />
          <span className="truncate">{t("role.add")}</span>
        </Menu.Item>
      </div>
    </>
  );
}
