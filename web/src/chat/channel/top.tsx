// A channel at the top of the chat (the user, 2026-11-13): who is looking at it now and its
// members (what each receives, each agent's context as a ring) at the bar's right, each a key of
// its own; 邀请 among the members: agents (invite.tsx) and people, an app a submenu of its
// logins. An agent's row opens the menu its face has (members.tsx MemberItems: one
// implementation). The face is the title's key (chat.tsx): its card, 编辑资料 in it; on a phone in ⋯.
import { Menu } from "@base-ui/react/menu";
import { Popover } from "@base-ui/react/popover";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Check, ChevronRight, PencilLine, Plus, UserPlus, Users } from "lucide-react";
import { useRef, useState, type CSSProperties, type ReactNode } from "react";
import { api } from "../../api/client";
import type { Agent, ChannelMembers, Viewer } from "../../api/types.gen";
import { agentsQ, isComputer, useApps, useMachines } from "../../data";
import { t, useLang } from "../../i18n";
import { useMachine } from "../../machines";
import { openChat } from "../../route";
import { useCarried, useList, useSession } from "../../store";
import { working } from "../../tree";
import { toast } from "../../toast";
import { agentName, Avatar, cn, Lamp, PopSurface, Tip } from "../../ui";
import { openEdit } from "./editing";
import { Face, MemberFace } from "./face";
import { ContextMark } from "../meters";
import { mayAct, useMembers } from "./gate";
import { AgentKinds, ITEM, MachineKinds, POPUP } from "./invite";
import { prefetchChoices } from "./pick";
import { memberError } from "./key";
import type { AgentRow } from "./lib";
import { MemberAsks, MemberItems, type Ask } from "./members";
import { useRecvLabel } from "./recv";
import { ScriptFace, ScriptPop, scriptState, useScripts, type ScriptPage } from "./script";
import { recvOf } from "./scripts";
import { actsOf } from "./menu";
import { subName, SubTag } from "../subwork";
import { appAll, hasApp, hasLogin, loginOf, peopleCount, peopleOf, pressApp, pressLogin } from "./setup";

const SUB = "no-bar max-h-[min(480px,var(--available-height))] min-w-52 max-w-[calc(100vw-16px)] overflow-y-auto p-1.5";
const HEAD = "truncate px-2.5 pt-2 pb-0.5 text-xs text-ink-3 select-none first:pt-1";

function Sub({ icon, label, lead, now, i, children }: { icon: ReactNode; label: string; lead?: ReactNode; now?: string; i: number; children: ReactNode }) {
  return (
    <Menu.SubmenuRoot>
      <Menu.SubmenuTrigger style={{ "--i": i } as CSSProperties} className={ITEM}>
        <span className="text-ink-2">{icon}</span>
        {lead}
        <span className="min-w-0 flex-1 truncate">{label}</span>
        {now && <span className="max-w-36 truncate text-xs text-ink-3">{now}</span>}
        <ChevronRight size={14} className="text-ink-4" />
      </Menu.SubmenuTrigger>
      <Menu.Portal>
        <Menu.Positioner className="z-50" sideOffset={4} collisionPadding={8}>
          <Menu.Popup className={cn(POPUP, SUB)}>
            <div className="g-stagger flex flex-col">{children}</div>
          </Menu.Popup>
        </Menu.Positioner>
      </Menu.Portal>
    </Menu.SubmenuRoot>
  );
}

const check = (on: boolean) => <span className="grid size-3.5 place-items-center text-ink">{on && <Check size={14} />}</span>;

/** The bar's right (the user, 2026-11-13): who is looking now, their faces, pressed their names;
 *  the members, a key with their count (agents and people) opening the list of them. */
export function TopRight({ id }: { id: string }) {
  useLang();
  const qc = useQueryClient();
  const list = useList();
  // The first screen can arrive before the sidebar's list.
  const row = useSession(id) ?? list?.find((x) => x.id === id);
  const here = useMachine(row?.machine);
  const local = (useQuery(agentsQ).data ?? []).filter((a) => !a.missing).map((a) => a.agent);
  const kinds = row?.machine ? (here?.online ? here.agents.map((a) => a.agent as Agent) : []) : local;
  const machines = useMachines(!!row?.machine);
  // The other computers' kinds too, where they would run (invite.tsx).
  const ahead = () => {
    prefetchChoices(qc, kinds, { channel: id });
    for (const m of machines) if (m.online && m.id !== row?.machine) prefetchChoices(qc, m.agents.map((a) => a.agent as Agent), { machine: m.id });
  };
  const members = useMembers(id);
  const viewers = useCarried<Viewer[]>(`viewers:${id}`) ?? [];
  const agents = (members?.members ?? []).filter((m) => m.session);
  // How many its members are, on the key itself: its agents and its people (each login once).
  const logins = useApps().data?.logins ?? [];
  const count = agents.length + peopleCount(peopleOf(members), logins);
  // What an agent's menu asks with from the list (清空, 移出, 注入记录): the cards the face's
  // menu asks with, under the members' key.
  const key = useRef<HTMLButtonElement>(null);
  const [asked, setAsked] = useState<Ask | null>(null);
  // A script's card, or a new one, out of the members' key (script.tsx): its pages in one popover.
  const [script, setScript] = useState<ScriptPage | null>(null);
  // Asked for as the bar is drawn: the list and each card open whole.
  useScripts(id);
  return (
    <span className="g-fade flex shrink-0 items-center gap-0.5">
      {viewers.length > 0 && (
        // Pressed, not hovered (a phone has no hover): the names in a popover.
        <Popover.Root>
          <Popover.Trigger
            render={
              <button type="button" aria-label={t("channel.viewing")} className="g-press flex h-8 items-center rounded-md px-1.5 hover:bg-fill data-popup-open:bg-fill pointer-coarse:h-10">
                {viewers.slice(0, 4).map((v, i) => (
                  <span key={v.member} className="grid rounded-full ring-2 ring-paper" style={{ marginLeft: i ? -5 : 0, zIndex: 4 - i }}>
                    <MemberFace member={v.member} name={v.name} size={18} />
                  </span>
                ))}
                {viewers.length > 4 && <span className="ml-1 text-xs text-ink-3 tabular-nums">+{viewers.length - 4}</span>}
              </button>
            }
          />
          <PopSurface align="end" sideOffset={6} className="no-bar max-h-[min(480px,var(--available-height))] min-w-48 max-w-[calc(100vw-16px)] overflow-y-auto p-1.5">
            <div className={HEAD}>{t("channel.viewing")}</div>
            {viewers.map((v) => (
              <div key={v.member} className="flex h-8 items-center gap-2.5 px-2.5 text-base pointer-coarse:h-11">
                <MemberFace member={v.member} name={v.name} size={18} />
                <span className="min-w-0 flex-1 truncate">{v.name}</span>
              </div>
            ))}
          </PopSurface>
        </Popover.Root>
      )}
      {/* Opened, each kind's models are asked for, for 邀请's menus to pop at once (pick.tsx). */}
      <Menu.Root onOpenChange={(open) => open && ahead()}>
        <Tip label={t("channel.members")} side="bottom">
          <Menu.Trigger
            render={
              <button ref={key} type="button" aria-label={t("channel.members")} className="g-press flex h-8 items-center gap-1 rounded-md px-2 text-ink-2 hover:bg-fill hover:text-ink data-popup-open:bg-fill pointer-coarse:h-10">
                <Users size={16} />
                <span className="text-xs tabular-nums">{count}</span>
              </button>
            }
          />
        </Tip>
        <Menu.Portal>
          <Menu.Positioner side="bottom" align="end" sideOffset={6} collisionPadding={8} className="z-50">
            <Menu.Popup finalFocus={false} className={cn(POPUP, SUB)}>
              <div className="g-stagger flex flex-col">
                <MemberList id={id} ask={setAsked} script={(p) => requestAnimationFrame(() => setScript(p))} />
              </div>
            </Menu.Popup>
          </Menu.Positioner>
        </Menu.Portal>
      </Menu.Root>
      <MemberAsks channel={id} anchor={key} asked={asked} done={() => setAsked(null)} />
      <ScriptPop channel={id} anchor={key} page={script} done={() => setScript(null)} align="end" />
    </span>
  );
}

/** The members: each agent a submenu, the menu its face has (members.tsx MemberItems; what it
 *  asks with, ask, drawn by the bar), what it receives at its row's end; the scripts of its IR,
 *  each pressed its card (script), and 新建脚本; then 邀请. */
function MemberList({ id, ask, script }: { id: string; ask: (a: Ask) => void; script: (p: ScriptPage) => void }) {
  const list = useList();
  const { short } = useRecvLabel(id);
  const scripts = useScripts(id) ?? [];
  const members = useMembers(id);
  const may = mayAct(members);
  const agents = (members?.members ?? []).filter((m) => m.session);
  const own = list?.find((x) => x.id === id);
  const subs = (list ?? []).filter((x) => x.subagent && x.parent === id && working(x, own, Date.now()));
  // What the channel's page lends: its composer's @, its way to a session's record.
  const lent = actsOf(id);
  return (
    <>
      <div className={HEAD}>{t("channel.agents")}</div>
      {agents.map((m) => {
        const row = list?.find((x) => x.id === m.session);
        const agent = m.agent ?? row?.agent ?? "claude";
        const a: AgentRow = { session: m.session!, member: m.member, agent, nick: m.nick ?? "", name: m.name ?? agentName(agent), status: m.status ?? row?.status ?? "idle", recv: recvOf(m.recv) };
        return (
          <Sub key={m.member} icon={<Face id={m.session!} agent={agent} size={16} />} lead={<ContextMark session={m.session!} />} label={m.nick || agentName(agent)} now={short(a.recv)} i={0}>
            <MemberItems channel={id} a={a} may={may} call={lent.call} record={lent.record} ask={(kind) => ask({ kind, a })} />
          </Sub>
        );
      })}
      {/* Its subagents at work (kumo, 2026-11-15): members that only read, pressed their record;
          no @, no menu. */}
      {subs.length > 0 && <div className={HEAD}>{t("channel.subsAtWork")}</div>}
      {subs.map((x) => (
        <Menu.Item key={x.id} onClick={() => openChat(x.id)} className={ITEM}>
          <SubTag agent={x.agent} name={subName(x)} className="flex-1 gap-2.5" />
          <Lamp status="running" />
        </Menu.Item>
      ))}
      {/* The scripts of its IR (docs/channels.md "脚本"): a row each, its face, name, intro, state. */}
      {(scripts.length > 0 || may) && <div className={HEAD}>{t("script.group")}</div>}
      {scripts.map((x) => {
        const state = scriptState(x);
        return (
          <Menu.Item key={x.name} onClick={() => script({ k: "card", name: x.name })} className={ITEM}>
            <ScriptFace avatar={x.avatar} size={16} />
            <span className="shrink-0">{x.name}</span>
            <span className="min-w-0 flex-1 truncate text-xs text-ink-3">{x.intro}</span>
            {state && <span className={cn("shrink-0 text-xs", x.enabled ? "text-err" : "text-ink-3")}>{state}</span>}
          </Menu.Item>
        );
      })}
      {may && (
        <Menu.Item onClick={() => script({ k: "new" })} className={ITEM}>
          <span className="text-ink-2">
            <Plus size={15} />
          </span>
          <span className="flex-1">{t("script.new")}</span>
        </Menu.Item>
      )}
      {/* No 人 / 所有人 here (kumo, 2026-11-16): who the people are is 邀请's to show and set. */}
      {may && <Invite id={id} />}
    </>
  );
}

/** 邀请, among the members (the user, 2026-11-13): agents (invite.tsx), people (an app a
 *  submenu of its logins). */
function Invite({ id }: { id: string }) {
  const qc = useQueryClient();
  const list = useList();
  const row = useSession(id) ?? list?.find((x) => x.id === id);
  const here = useMachine(row?.machine);
  const members = useMembers(id);
  const d = useApps().data;
  const set = (ms: ChannelMembers) => void qc.setQueryData<ChannelMembers>(["members", id], ms);
  const run = (f: () => Promise<ChannelMembers>) => void f().then(set, (e) => toast(memberError(e), { error: true }));
  const people = peopleOf(members);
  // No computers: they are not invited (the owner, 2026-11-14).
  const apps = (d?.apps ?? []).filter((a) => !isComputer(a));
  const ids = apps.map((a) => a.id);
  const loginsOf = (app: string) => (d?.logins ?? []).filter((l) => l.app === app);
  const idsOf = (app: string) => loginsOf(app).map((l) => l.id);
  const setPeople = (p: string[]) => run(() => api.setPeople(id, p));
  return (
      <Sub icon={<UserPlus size={15} />} label={t("channel.invite")} i={0}>
        <div className={HEAD}>{row?.machine ? here?.name : t("term.local")}</div>
        <AgentKinds id={id} />
        {apps.map((a) => {
          const name = a.web ? t("term.panel") : a.name;
          const logins = loginsOf(a.id);
          return (
            <Sub key={a.id} icon={<Avatar src={a.avatar} name={a.name} size={16} type={a.type} host={!!a.host} />} label={name} now={hasApp(people, a.id) ? t("channel.everyone") : String(logins.filter((l) => hasLogin(people, a.id, l.id)).length || "")} i={0}>
              <Menu.Item closeOnClick={false} disabled={members?.me === appAll(a.id)} onClick={() => setPeople(pressApp(people, ids, idsOf, a.id, members?.me))} className={ITEM}>
                {check(hasApp(people, a.id))}
                <span className="flex-1 font-medium">{t("channel.everyone")}</span>
              </Menu.Item>
              {logins.map((l) => (
                <Menu.Item key={l.id} closeOnClick={false} disabled={members?.me === loginOf(a.id, l.id)} onClick={() => setPeople(pressLogin(people, ids, idsOf, a.id, l.id, members?.me))} className={ITEM}>
                  {check(hasLogin(people, a.id, l.id))}
                  <Avatar src={l.avatar} name={l.name} size={16} />
                  <span className="min-w-0 flex-1 truncate">{l.name}</span>
                </Menu.Item>
              ))}
            </Sub>
          );
        })}
        {/* The other computers' agents, last (kumo, 2026-11-18). */}
        <MachineKinds id={id} />
      </Sub>
  );
}

/** ⋯'s part: on a phone, where the title's logo opens the list, 编辑资料 (out of ⋯: chat.tsx
 *  EditAt; elsewhere the face's card turns into it). from: the place of its item among the menu's, for the stagger. */
export default function ChannelMenu({ id, from = 0, face }: { id: string; from?: number; face?: boolean }) {
  useLang();
  const members = useMembers(id);
  const may = mayAct(members);
  if (!may || !face) return null;
  return (
    <Menu.Item onClick={() => requestAnimationFrame(() => openEdit({ channel: id }))} style={{ "--i": from } as CSSProperties} className={ITEM}>
      <span className="text-ink-2">
        <PencilLine size={15} />
      </span>
      {t("profile.edit")}
    </Menu.Item>
  );
}
