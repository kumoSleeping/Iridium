// A channel's agents as faces (docs/channels.md): beside what each said, its harness's logo on a
// disc, its lamp on the corner. A press opens its card (as QQ's: talk to it in its own channel,
// call it here); the right button, or a finger held down, its menu: call, compact, clear, model (the
// composer's model menu, its effort track in it), approval mode, stop, 编辑资料, its own session, 注入记录 (what the
// channel gave it), out. The same menu is its row's among the members (top.tsx): one
// implementation (MemberItems; its list, menu.ts). Its context and usage are not here: they
// have their keys after its model's name (docs/前端设计规范.md "一样东西一个地方").
import { Menu } from "@base-ui/react/menu";
import { change } from "../../pending";
import { Popover } from "@base-ui/react/popover";
import { useQueryClient } from "@tanstack/react-query";
import { AtSign, ChevronRight, LayoutGrid, MessageCircle, Box, Check, CircleStop, FoldVertical, Mail, PencilLine, ScrollText, ShieldCheck, UserMinus } from "lucide-react";
import { useRef, useState, type CSSProperties, type ReactNode, type RefObject } from "react";
import { api } from "../../api/client";
import { stopTurn } from "../live";
import type { ChannelMembers, SessionState as LiveSession, Session, Trigger } from "../../api/types.gen";
import { CardHead, MainKey, SecondKey } from "../../card";
import { t, useLang } from "../../i18n";
import { ModeMark, modeName } from "../../modes";
import { useList, useSession } from "../../store";
import { errText, toast } from "../../toast";
import { openChat } from "../../route";
import { agentName, cn, Lamp, own, PopSurface, sessionStatusText, useEver, useLongPress, useMenuAt, usePointAnchor, MenuPortal } from "../../ui";
import { levelName } from "../effort";
import { MODEL_POPUP, SessionModels, useModelMenu, useMode } from "../meters";
import { EditBody } from "./edit";
import { Face } from "./face";
import { prefetchEdit } from "./gate";
import { useAhead } from "../../prefetch";
import { memberError } from "./key";
import { ownAgent, type AgentRow } from "./lib";
import { memberMenu, type MenuKey } from "./menu";
import { recvWords, useRecvGroups, useRecvName, type RecvApp } from "./recv";
import { fromOf, hasMore, recvLabel, toggle, withFrom } from "./scripts";
import { CARD_POP, ProfileCard, type CardKey } from "./profilecard";
import { Deeper, Paged, usePages } from "./pages";

const ITEM = "flex h-8 cursor-default items-center gap-2.5 rounded-md px-2.5 text-base outline-none select-none data-highlighted:bg-fill data-popup-open:bg-fill pointer-coarse:h-9";
const POPUP = "g-unfold glass thick float rounded-lg text-ink outline-none";
const BUSY = new Set(["running", "compacting", "starting", "waiting"]);

/** A member's state before the stream brings it, from its row. */
const ghostOf = (row: Session): LiveSession => ({ ...row, asks: [], tasks: [], queue: [], end: 0, proc: -1 });

/** Something that floats at a member's face: the app's one popover (ui.tsx PopSurface) unfolding
 *  out of it, put away by a press elsewhere or Esc. */
function At({ anchor, open, onOpenChange, children, className }: { anchor: RefObject<HTMLElement | null>; open: boolean; onOpenChange: (o: boolean) => void; children: ReactNode; className?: string }) {
  return (
    <Popover.Root open={open} onOpenChange={onOpenChange}>
      <PopSurface anchor={anchor.current} still className={className}>
        {children}
      </PopSurface>
    </Popover.Root>
  );
}

/** An agent's face beside what it said (the user, 2026-11-13): its harness's logo on a disc, its
 *  lamp on the corner. A press opens its card; the right button, or a finger held down, its menu. */
/** talk: 发消息 for the channel's own agent, here already (its composer). */
export function AgentFace({ channel, a, call, record, may, talk, size = 28 }: { channel: string; a: AgentRow; call?: (nick: string) => void; record?: (session: string) => void; may: boolean; talk?: () => void; size?: number }) {
  const face = useRef<HTMLButtonElement>(null);
  const [menuAt, setMenuAt] = useMenuAt();
  const [detail, setDetail] = useState(false);
  const [asked, setAsked] = useState<Ask | null>(null);
  const detailEver = useEver(detail);
  const menuEver = useEver(menuAt !== null);
  const lp = useLongPress(() => {
    const r = face.current?.getBoundingClientRect();
    setMenuAt(r ? { x: r.left, y: r.bottom + 4 } : { x: 40, y: 80 });
  });
  const label = [a.nick, a.name, a.status !== "idle" && sessionStatusText(a)].filter(Boolean).join(" · ");
  return (
    <>
      <button
        ref={face}
        type="button"
        aria-label={label}
        onClick={() => setDetail(!detail)}
        {...lp}
        onContextMenu={(e) => {
          if (lp.onContextMenu(e) || !own(e)) return;
          e.preventDefault();
          setDetail(false);
          setMenuAt({ x: e.clientX, y: roomBelow(e.clientY) });
        }}
        style={{ width: size, height: size }}
        className="g-press grid shrink-0 place-items-center rounded-full bg-fill select-none hover:bg-fill-2 [-webkit-touch-callout:none]"
      >
        {/* The lamp on the round face's own lower right edge, whatever is drawn in it (a logo is
            smaller than the face). */}
        <span className="relative grid size-full place-items-center">
          <Face id={a.session} agent={a.agent} size={size} logo={Math.round(size * 0.57)} />
          {a.status !== "idle" && (
            <span className="absolute right-0 bottom-0 grid rounded-full bg-paper p-px">
              <Lamp status={a.status} />
            </span>
          )}
        </span>
      </button>
      {detailEver && (
        <At anchor={face} open={detail} onOpenChange={setDetail} className={CARD_POP}>
          <AgentCard channel={channel} a={a} call={call} record={record} may={may} talk={talk} done={() => setDetail(false)} />
        </At>
      )}
      {menuEver && <MemberMenu channel={channel} a={a} may={may} at={menuAt} close={() => setMenuAt(null)} call={call} record={record} ask={(kind) => setAsked({ kind, a })} />}
      <MemberAsks channel={channel} anchor={face} asked={asked} done={() => setAsked(null)} />
    </>
  );
}

/** Where a menu opened by the mouse at y begins: there, or higher where too little of the window
 *  is below it for the menu, so that it is still below its start, whole (kumo, 2026-11-26: not
 *  from the pointer at any cost). A finger's menu stays under the face, the finger above it. */
const MENU_ROOM = 360;
const roomBelow = (y: number) => Math.max(8, Math.min(y, innerHeight - 8 - Math.min(MENU_ROOM, innerHeight - 16)));

/** An agent's card (the user, 2026-11-13, as QQ's; the one card, profilecard.tsx): its
 *  background, its face, nick, what it runs as, its state, its intro; not its context or usage,
 *  which have their own keys after its model's name (docs/前端设计规范.md "一样东西一个地方").
 *  Keys to talk to it in its own channel (its session: one channel bound to it), to call it here
 *  (@nick), 编辑资料 for one of the channel's people; in its own channel, to see its record. */
export function AgentCard({ channel, a, call, record, may, talk, done }: { channel: string; a: AgentRow; call?: (nick: string) => void; record?: (session: string) => void; may: boolean; talk?: () => void; done: () => void }) {
  // 编辑资料: the card turns into it, in its own popover (kumo, 2026-11-16: never a page).
  const [edit, setEdit] = useState(false);
  if (edit) return <EditBody editing={{ channel, agent: a.session }} back={() => setEdit(false)} done={done} />;
  return <CardOf channel={channel} a={a} call={call} record={record} may={may} talk={talk} done={done} edit={() => setEdit(true)} />;
}

function CardOf({ channel, a, call, record, may, talk, done, edit }: Parameters<typeof AgentCard>[0] & { edit: () => void }) {
  const mine = ownAgent(channel, a);
  const qc = useQueryClient();
  const keys: CardKey[] = [];
  if (!mine || (talk && may)) keys.push({ icon: <MessageCircle size={14} />, label: t("channel.talk"), run: () => (done(), mine ? talk?.() : openChat(a.session)) });
  if (call && a.nick) keys.push({ icon: <AtSign size={14} />, label: t("channel.call"), run: () => (done(), call(a.nick)) });
  if (may) keys.push({ icon: <PencilLine size={14} />, label: t("profile.edit"), run: edit, ahead: () => prefetchEdit(qc, channel, a.session, true) });
  if (mine && record) keys.push({ icon: <ScrollText size={14} />, label: t("channel.session"), run: () => (done(), record(a.session)) });
  return (
    <ProfileCard
      face={
        <span className="grid size-14 place-items-center rounded-full bg-fill">
          <Face id={a.session} agent={a.agent} size={56} logo={30} />
        </span>
      }
      name={a.nick || agentName(a.agent)}
      sub={a.name || agentName(a.agent)}
      state={
        <>
          <Lamp status={a.status} />
          {sessionStatusText(a)}
        </>
      }
      keys={keys}
    />
  );
}


/** A submenu of a few to pick one of: its name, the one picked faint at the row's end. */
function Pick({ icon, label, now, value, options, set, i }: { icon: ReactNode; label: string; now?: string; value: string; options: { value: string; label: string; mark?: ReactNode; group?: string }[]; set: (v: string) => void; i: number }) {
  return (
    <Deeper
      row={
        <>
          <span className="text-ink-2">{icon}</span>
          <span className="flex-1">{label}</span>
          {now && <span className="max-w-36 truncate text-xs text-ink-3">{now}</span>}
        </>
      }
      title={label}
      i={i}
      popup={cn(POPUP, "no-bar max-h-[min(480px,var(--available-height))] min-w-52 max-w-[calc(100vw-16px)] overflow-y-auto p-1.5")}
    >
      <Menu.RadioGroup value={value} onValueChange={(v) => set(v as string)} className="g-stagger flex flex-col">
        {options.map((o, j) => (
          <div key={o.value} className="contents">
            {o.group && o.group !== options[j - 1]?.group && <div className="truncate px-2.5 pt-2 pb-0.5 text-xs text-ink-3 select-none first:pt-1">{o.group}</div>}
            <Menu.RadioItem value={o.value} closeOnClick style={{ "--i": j } as CSSProperties} className="flex min-h-8 cursor-default items-center gap-2.5 rounded-md pr-2 pl-2.5 text-base outline-none select-none data-highlighted:bg-fill pointer-coarse:min-h-11">
              {o.mark}
              <span className="min-w-0 flex-1 truncate py-1">{o.label}</span>
              <Menu.RadioItemIndicator className="text-ink">
                <Check size={14} />
              </Menu.RadioItemIndicator>
            </Menu.RadioItem>
          </div>
        ))}
      </Menu.RadioGroup>
    </Deeper>
  );
}

/** 唤醒 (docs/channels.md "唤醒"): whose words wake it. What names it always does (its row
 *  checked, not to be pressed); then every member, web logins, other agents; non-web apps live
 *  under the bottom 应用 entry, switching pages in place (never a 3rd-level popup; kumo, 2026-11-27).
 *  The rest of what wakes it is in 编辑资料 (onedit.tsx). */
function WakePick({ channel, a, set, i }: { channel: string; a: AgentRow; set: (on: Trigger[]) => void; i: number }) {
  const outer = usePages();
  const { main, apps } = useRecvGroups(channel, a.session);
  const name = useRecvName(channel);
  const from = fromOf(a.on);
  const flip = (entry: string) => () => set(withFrom(a.on, toggle(from, entry)));
  const row = (key: string, on: boolean, face: ReactNode, label: string, run?: () => void, j = 0) => (
    <Menu.Item key={key} closeOnClick={false} disabled={!run} onClick={run} style={{ "--i": j } as CSSProperties} className={cn(ITEM, "data-disabled:text-ink-2")}>
      <span className="grid size-3.5 shrink-0 place-items-center text-ink">{on && <Check size={14} />}</span>
      {face}
      <span className="min-w-0 flex-1 truncate">{label}</span>
    </Menu.Item>
  );
  const appHits = (app: RecvApp) => (app.direct ? (from.includes(app.direct) ? 1 : 0) : (app.all && from.includes(app.all) ? 1 : 0) + app.logins.filter((l) => from.includes(l.entry)).length);
  const appsHits = apps.reduce((n, x) => n + appHits(x), 0);
  const body = (
    <div className="g-stagger flex flex-col">
      {row("@", true, <AtSign size={15} className="shrink-0 text-ink-2" />, t("on.at"))}
      {main.map((o, j) => row(o.entry, from.includes(o.entry), o.face, o.label, flip(o.entry), j + 1))}
      {apps.length > 0 && (
        <>
          <div role="separator" className="mx-2.5 my-1 border-t border-(--edge)" />
          <Deeper
            bare
            title={t("channel.app")}
            i={main.length + 1}
            popup=""
            row={
              <>
                <span className="grid size-3.5 shrink-0 place-items-center text-ink">{appsHits > 0 && <Check size={14} />}</span>
                <LayoutGrid size={15} className="shrink-0 text-ink-2" />
                <span className="min-w-0 flex-1 truncate">{t("channel.app")}</span>
                {appsHits > 0 && <span className="text-xs text-ink-3 tabular-nums">{appsHits}</span>}
                <ChevronRight size={14} className="shrink-0 text-ink-4" />
              </>
            }
          >
            <div className="g-stagger flex flex-col">
              {apps.map((app, j) =>
                app.direct ? (
                  row(app.direct, from.includes(app.direct), app.face, app.name, flip(app.direct), j)
                ) : (
                  <Deeper
                    key={app.id}
                    bare
                    title={app.name}
                    i={j}
                    popup=""
                    row={
                      <>
                        <span className="grid size-3.5 shrink-0 place-items-center text-ink">{appHits(app) > 0 && <Check size={14} />}</span>
                        {app.face}
                        <span className="min-w-0 flex-1 truncate">{app.name}</span>
                        {appHits(app) > 0 && <span className="text-xs text-ink-3 tabular-nums">{appHits(app)}</span>}
                        <ChevronRight size={14} className="shrink-0 text-ink-4" />
                      </>
                    }
                  >
                    <div className="g-stagger flex flex-col">
                      {app.all && row(app.all, from.includes(app.all), app.face, t("channel.everyone"), flip(app.all), 0)}
                      {app.logins.map((l, k) => row(l.entry, from.includes(l.entry), l.face, l.label, flip(l.entry), k + (app.all ? 1 : 0)))}
                    </div>
                  </Deeper>
                ),
              )}
            </div>
          </Deeper>
        </>
      )}
    </div>
  );
  return (
    <Deeper
      row={
        <>
          <span className="text-ink-2">
            <Mail size={15} />
          </span>
          <span className="flex-1">{t("channel.recv")}</span>
          <span className="max-w-36 truncate text-xs text-ink-3">{hasMore(a.on) ? t("on.more", { label: recvLabel(from, name, recvWords()) }) : recvLabel(from, name, recvWords())}</span>
        </>
      }
      title={t("channel.recv")}
      i={i}
      popup={cn(POPUP, "no-bar max-h-[min(480px,var(--available-height))] min-w-52 max-w-[calc(100vw-16px)] overflow-x-hidden overflow-y-auto p-1.5")}
    >
      {outer ? body : <Paged force>{body}</Paged>}
    </Deeper>
  );
}

/** What a member's menu asks with a card or a popover at its anchor: 移出 (asked first),
 *  编辑资料. */
export type AskKind = "remove" | "edit";
export type Ask = { kind: AskKind; a: AgentRow };

/** The cards and the popover a member's menu opens, at anchor (the face; the members' key for
 *  the list): one of them at a time. */
export function MemberAsks({ channel, anchor, asked, done }: { channel: string; anchor: RefObject<HTMLElement | null>; asked: Ask | null; done: () => void }) {
  // The last one asked stays drawn while it folds away.
  const last = useRef(asked);
  if (asked) last.current = asked;
  const x = asked ?? last.current;
  if (!x) return null;
  const open = (kind: AskKind) => asked?.kind === kind;
  const set = (o: boolean) => !o && done();
  return (
    <>
      <RemoveAsk channel={channel} member={x.a.member} nick={x.a.nick} anchor={anchor} open={open("remove")} onOpenChange={set} />
      {/* 编辑资料 from the menu: out of the face (or the members' key), as the card's own. */}
      <At anchor={anchor} open={open("edit")} onOpenChange={set} className={CARD_POP}>
        <EditBody editing={{ channel, agent: x.a.session }} done={done} />
      </At>

    </>
  );
}

/** A member's menu, at a point: its items (MemberItems) in a menu of their own. */
function MemberMenu({ at, close, ...items }: { at: { x: number; y: number } | null; close: () => void } & Parameters<typeof MemberItems>[0]) {
  const anchor = usePointAnchor(at);
  return (
    <Menu.Root open={at !== null} onOpenChange={(o) => !o && close()}>
      <MenuPortal>
        {/* Always below what opened it, never turned up over the finger (kumo, 2026-11-26: a menu
            turned up had 移出 under the finger as it let go): too little room, it is lower, its
            items scrolled, inside the window. */}
        <Menu.Positioner anchor={anchor} side="bottom" align="start" sideOffset={4} collisionPadding={8} collisionAvoidance={{ side: "none", align: "shift" }} className="z-50">
          <Menu.Popup finalFocus={false} className={cn(POPUP, "no-bar max-h-(--available-height) min-w-52 overflow-x-hidden overflow-y-auto p-1.5")}>
            {/* In the phone's layout its next levels are its pages, no menu beside a row (kumo, 2026-11-25). */}
            <Paged>
              <div className="g-stagger flex flex-col">
                <MemberItems {...items} />
              </div>
            </Paged>
          </Menu.Popup>
        </Menu.Positioner>
      </MenuPortal>
    </Menu.Root>
  );
}

/** An agent's menu's items, inside a menu: the face's (MemberMenu) and its row's submenu among
 *  the members (top.tsx), the same in both (menu.ts memberMenu: which, in what order). Its
 *  model, effort and mode are its session's own, set as the one-agent chat's keys set them.
 *  may: the one looking is of the channel's people; else only what reads is offered. call:
 *  "@nick" into the composer; record: its session's record, where the page has its own way
 *  there; ask: 移出, 编辑资料, opened by the one who draws the menu (MemberAsks). */
export function MemberItems({ channel, a, may, call, record, ask }: { channel: string; a: AgentRow; may: boolean; call?: (nick: string) => void; record?: (session: string) => void; ask: (kind: AskKind) => void }) {
  const real = useSession(a.session);
  const row = useList()?.find((x) => x.id === a.session);
  const s = real ?? (row && ghostOf(row));
  if (!s) return null;
  return <Items channel={channel} a={a} s={s} may={may} call={call} record={record} ask={ask} />;
}

function Items({ channel, a, s, may, call, record, ask }: Parameters<typeof MemberItems>[0] & { s: LiveSession }) {
  useLang();
  const qc = useQueryClient();
  // What its items open in the popover at its face, asked as the menu opens: 编辑资料.
  useAhead(() => prefetchEdit(qc, channel, a.session, true), [a.session, channel]);
  const mode = useMode(s);
  const m = useModelMenu(s);
  const mine = ownAgent(channel, a);
  const act = (f: () => Promise<unknown>, err: string) => () => void f().catch((e) => toast(err, { error: true, description: errText(e) }));
  // Whose words wake it, as each is pressed: shown at once, then as the server has it; a ring
  // it makes told.
  const setOn = (on: Trigger[]) => {
    const was = qc.getQueryData<ChannelMembers>(["members", channel]);
    if (was) qc.setQueryData<ChannelMembers>(["members", channel], { ...was, members: was.members.map((m) => (m.member === a.member ? { ...m, on } : m)) });
    void api.patchMember(channel, a.member, { on }).then(
      (ms) => (qc.setQueryData<ChannelMembers>(["members", channel], ms), ms.warn?.length && toast(t("on.loop"), { description: ms.warn.join("\n") })),
      (e) => (was && qc.setQueryData<ChannelMembers>(["members", channel], was), toast(t("err.action"), { error: true, description: memberError(e) })),
    );
  };
  // Each after a frame: the menu shuts first, and what opens is not shut with it.
  const later = (f: () => void) => () => requestAnimationFrame(f);
  // Its session: by the page's own way there; with none, opened, unless it is this one.
  const view = record ? () => record(a.session) : mine ? undefined : () => openChat(a.session);
  const item = (i: number, icon: ReactNode, label: string, run: () => void, o: { tone?: string; disabled?: boolean; note?: string; key?: string; keep?: boolean } = {}) => (
    <Menu.Item key={o.key ?? label} onClick={run} closeOnClick={!o.keep} disabled={o.disabled} style={{ "--i": i } as CSSProperties} className={cn(ITEM, o.tone, "data-disabled:text-ink-3")}>
      <span className={o.disabled ? "text-ink-4" : (o.tone ?? "text-ink-2")}>{icon}</span>
      <span className="flex-1">{label}</span>
      {o.note && <span className="pl-3 text-xs text-ink-3">{o.note}</span>}
    </Menu.Item>
  );
  const draw: Record<MenuKey, (i: number, disabled?: boolean) => ReactNode> = {
    call: (i) => item(i, <AtSign size={15} />, t("channel.call"), later(() => call?.(a.nick))),
    compact: (i) => item(i, <FoldVertical size={15} />, t("channel.compact"), act(() => api.send(a.session, "/compact"), t("err.action"))),
    // The composer's model menu itself (kumo, 2026-11-16): its models, the effort track, UltraCode.
    model: (i) => (
      <Deeper
        key="model"
        row={
          <>
            <span className="text-ink-2">
              <Box size={15} />
            </span>
            <span className="flex-1">{t("composer.model")}</span>
            <span className="max-w-40 truncate text-xs text-ink-3">{[m.label, m.level && m.levels.length > 0 ? levelName(m.level) : ""].filter(Boolean).join(" · ")}</span>
          </>
        }
        title={t("composer.model")}
        i={i}
        popup={MODEL_POPUP}
      >
        <SessionModels m={m} />
      </Deeper>
    ),
    mode: (i) => (
      <Pick
        key="mode"
        icon={<ShieldCheck size={15} />}
        label={t("composer.mode")}
        now={modeName({ id: mode.now })}
        value={mode.now}
        options={mode.modes.map((m) => ({ value: m.id, label: modeName(m), mark: <ModeMark agent={mode.agent} id={m.id} size={16} className="shrink-0 text-ink-3" /> }))}
        set={(m) => void mode.set(m)}
        i={i}
      />
    ),
    stop: (i) => item(i, <CircleStop size={15} />, t("channel.stop"), () => void stopTurn(a.session)),
    recv: (i) => <WakePick key="recv" channel={channel} a={a} set={setOn} i={i} />,
    edit: (i) => item(i, <PencilLine size={15} />, t("profile.edit"), later(() => ask("edit"))),
    session: (i) => item(i, <ScrollText size={15} />, t("channel.session"), () => view?.()),
    // The session the channel is stays in it: shown, not offered.
    remove: (i, disabled) => item(i, <UserMinus size={15} />, t("channel.remove"), later(() => ask("remove")), disabled ? { disabled, note: t("channel.ownAgent") } : { tone: "text-err" }),
  };
  const list = memberMenu({ may, own: mine, call: !!call && !!a.nick, models: m.models.length, modes: mode.modes.length, busy: BUSY.has(s.status), session: !!view });
  return <>{list.map((x, i) => draw[x.key](i, x.disabled))}</>;
}

/** The sessions archiving session id archives too: those it started that hang under it (its ir
 *  new, not taken out), and theirs (the server's cascade, SessionPatch.archived). */
const hungUnder = (id: string, list: Session[]): number => {
  const kids = list.filter((s) => s.launcher === id && !s.own && !s.archived);
  return kids.reduce((n, s) => n + 1 + hungUnder(s.id, list), 0);
};

/** 移出, from wherever it is asked (the face's menu, the members' list at the bar's right): the
 *  one card at anchor, the member out only once it is confirmed (docs/前端设计规范.md "一样东西
 *  一个实现"). A session started for this channel (spawned here) is archived with it, the main
 *  key, those it started too, how many said; 仅移出 keeps it (it shows in its folder then). One
 *  that was not (brought in by an ir) only leaves: its session stays (otae, 2026-11-23). */
export function RemoveAsk({ channel, member, nick, anchor, open, onOpenChange }: { channel: string; member: string; nick: string; anchor: RefObject<HTMLElement | null>; open: boolean; onOpenChange: (o: boolean) => void }) {
  const qc = useQueryClient();
  const list = useList();
  const [busy, setBusy] = useState<"" | "out" | "archive">("");
  const done = () => onOpenChange(false);
  const id = member.startsWith("agent:") ? member.slice("agent:".length) : "";
  const s = id ? list?.find((x) => x.id === id) : undefined;
  const forHere = !!s && s.spawned?.channel === channel;
  const hung = forHere && list ? hungUnder(id, list) : 0;
  const out = async (archive: boolean) => {
    setBusy(archive ? "archive" : "out");
    try {
      // 移出并归档 in the one request (the server archives it first, then takes it out): the list
      // never has it out and not yet archived, shown in its folder a moment (kumo, 2026-11-26).
      const ms = archive ? await change(id, { archived: true }, () => api.removeMember(channel, member, true)) : await api.removeMember(channel, member);
      qc.setQueryData<ChannelMembers>(["members", channel], ms);
      done();
    } catch (e) {
      toast(t("err.action"), { error: true, description: memberError(e) });
    } finally {
      setBusy("");
    }
  };
  const title = t("channel.removeTitle", { nick });
  return (
    <At anchor={anchor} open={open} onOpenChange={onOpenChange} className="@container w-[min(340px,calc(100vw-16px))] p-2.5 pl-3">
      <div role="alertdialog" aria-label={title}>
        <CardHead
          icon={<UserMinus size={16} />}
          title={title}
          summary={forHere ? (hung ? t("channel.removeArchiveHintN", { n: hung }) : t("channel.removeArchiveHint")) : t("channel.removeHint")}
          keys={
            forHere ? (
              <>
                <SecondKey onClick={done}>{t("me.cancel")}</SecondKey>
                <SecondKey disabled={!!busy} onClick={() => void out(false)}>
                  {t("channel.removeOnly")}
                </SecondKey>
                <MainKey busy={busy === "archive"} disabled={!!busy} onClick={() => void out(true)}>
                  {t("channel.removeArchive")}
                </MainKey>
              </>
            ) : (
              <>
                <SecondKey onClick={done}>{t("me.cancel")}</SecondKey>
                <MainKey busy={busy === "out"} onClick={() => void out(false)}>
                  {t("channel.remove")}
                </MainKey>
              </>
            )
          }
        />
      </div>
    </At>
  );
}
