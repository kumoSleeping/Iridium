// A channel's agents as faces (docs/channels.md): beside what each said, its harness's logo on a
// disc, its lamp on the corner. A press opens its card (as QQ's: talk to it in its own channel,
// call it here); the right button, or a finger held down, its menu: call, compact, clear, model (the
// composer's model menu, its effort track in it), approval mode, stop, what it receives, 编辑资料, its own session, 注入记录 (what the
// channel gave it), out. The same menu is its row's among the members (top.tsx): one
// implementation (MemberItems; its list, menu.ts). Its context and usage are not here: they
// have their keys after its model's name (docs/前端设计规范.md "一样东西一个地方").
import { Menu } from "@base-ui/react/menu";
import { Popover } from "@base-ui/react/popover";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { AtSign, MessageCircle, Box, Check, ChevronRight, CircleStop, Eraser, FoldVertical, Mail, PencilLine, Inbox, ScrollText, ShieldCheck, UserMinus } from "lucide-react";
import { useRef, useState, type CSSProperties, type ReactNode, type RefObject } from "react";
import { api } from "../../api/client";
import { stopTurn } from "../live";
import type { ChannelMembers, SessionState as LiveSession, Session } from "../../api/types.gen";
import { CardHead, MainKey, SecondKey } from "../../card";
import { t, useLang } from "../../i18n";
import { ModeMark, modeName } from "../../modes";
import { useList, useSession } from "../../store";
import { errText, toast } from "../../toast";
import { openChat } from "../../route";
import { agentName, cn, Lamp, Logo, own, PopSurface, sessionStatusText, useLongPress, useMenuAt } from "../../ui";
import { levelName } from "../effort";
import { LongWords } from "../longwords";
import { MODEL_POPUP, SessionModels, useModelMenu, useMode } from "../meters";
import { EditBody } from "./edit";
import { Face } from "./face";
import { useMembers } from "./gate";
import { memberError } from "./key";
import { givenKind, givenParts, givenText, ownAgent, type AgentRow, type GivenKind } from "./lib";
import { memberMenu, type MenuKey } from "./menu";
import { CARD_POP, ProfileCard, useCard, type CardKey } from "./profilecard";

const ITEM = "flex h-8 cursor-default items-center gap-2.5 rounded-md px-2.5 text-base outline-none select-none data-highlighted:bg-fill data-popup-open:bg-fill pointer-coarse:h-11";
const POPUP = "g-unfold glass thick float rounded-lg text-ink outline-none";
const BUSY = new Set(["running", "compacting", "starting", "waiting"]);

/** A member's state before the stream brings it, from its row. */
const ghostOf = (row: Session): LiveSession => ({ ...row, asks: [], tasks: [], denials: [], queue: [], end: 0, proc: -1 });

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
          if (!own(e)) return;
          e.preventDefault();
          setDetail(false);
          setMenuAt({ x: e.clientX, y: e.clientY });
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
      <At anchor={face} open={detail} onOpenChange={setDetail} className={CARD_POP}>
        <AgentCard channel={channel} a={a} call={call} record={record} may={may} talk={talk} done={() => setDetail(false)} />
      </At>
      <MemberMenu channel={channel} a={a} may={may} at={menuAt} close={() => setMenuAt(null)} call={call} record={record} ask={(kind) => setAsked({ kind, a })} />
      <MemberAsks channel={channel} anchor={face} asked={asked} done={() => setAsked(null)} />
    </>
  );
}

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
  // What the members' list has of it at once; the card itself, asked for, as it comes.
  const row = useMembers(channel)?.members.find((m) => m.session === a.session);
  const card = useCard(`agent:${a.session}`);
  const keys: CardKey[] = [];
  if (!mine || (talk && may)) keys.push({ icon: <MessageCircle size={14} />, label: t("channel.talk"), run: () => (done(), mine ? talk?.() : openChat(a.session)) });
  if (call && a.nick) keys.push({ icon: <AtSign size={14} />, label: t("channel.call"), run: () => (done(), call(a.nick)) });
  if (may) keys.push({ icon: <PencilLine size={14} />, label: t("profile.edit"), run: edit });
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
      intro={card?.intro ?? row?.intro}
      background={card?.background ?? row?.background}
      keys={keys}
    />
  );
}


/** A submenu of a few to pick one of: its name, the one picked faint at the row's end. */
function Pick({ icon, label, now, value, options, set, i }: { icon: ReactNode; label: string; now?: string; value: string; options: { value: string; label: string; mark?: ReactNode; group?: string }[]; set: (v: string) => void; i: number }) {
  return (
    <Menu.SubmenuRoot>
      <Menu.SubmenuTrigger style={{ "--i": i } as CSSProperties} className={ITEM}>
        <span className="text-ink-2">{icon}</span>
        <span className="flex-1">{label}</span>
        {now && <span className="max-w-36 truncate text-xs text-ink-3">{now}</span>}
        <ChevronRight size={14} className="text-ink-4" />
      </Menu.SubmenuTrigger>
      <Menu.Portal>
        <Menu.Positioner className="z-50" sideOffset={4} collisionPadding={8}>
          <Menu.Popup className={cn(POPUP, "no-bar max-h-[min(480px,var(--available-height))] min-w-52 max-w-[calc(100vw-16px)] overflow-y-auto p-1.5")}>
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
          </Menu.Popup>
        </Menu.Positioner>
      </Menu.Portal>
    </Menu.SubmenuRoot>
  );
}

/** What a member's menu asks with a card or a popover at its anchor: 移出 (asked first),
 *  注入记录, 编辑资料. */
export type AskKind = "remove" | "given" | "edit";
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
      <At anchor={anchor} open={open("given")} onOpenChange={set} className="flex max-h-[min(720px,var(--available-height))] w-[min(720px,calc(100vw-16px))] flex-col">
        <Given a={x.a} />
      </At>
      {/* 编辑资料 from the menu: out of the face (or the members' key), as the card's own. */}
      <At anchor={anchor} open={open("edit")} onOpenChange={set} className={CARD_POP}>
        <EditBody editing={{ channel, agent: x.a.session }} done={done} />
      </At>
    </>
  );
}

/** A member's menu, at a point: its items (MemberItems) in a menu of their own. */
function MemberMenu({ at, close, ...items }: { at: { x: number; y: number } | null; close: () => void } & Parameters<typeof MemberItems>[0]) {
  const anchor = at && { getBoundingClientRect: () => DOMRect.fromRect({ x: at.x, y: at.y, width: 0, height: 0 }) };
  return (
    <Menu.Root open={at !== null} onOpenChange={(o) => !o && close()}>
      <Menu.Portal>
        <Menu.Positioner anchor={anchor} side="bottom" align="start" sideOffset={4} collisionPadding={8} className="z-50">
          <Menu.Popup finalFocus={false} className={cn(POPUP, "min-w-52 p-1.5")}>
            <div className="g-stagger flex flex-col">
              <MemberItems {...items} />
            </div>
          </Menu.Popup>
        </Menu.Positioner>
      </Menu.Portal>
    </Menu.Root>
  );
}

/** An agent's menu's items, inside a menu: the face's (MemberMenu) and its row's submenu among
 *  the members (top.tsx), the same in both (menu.ts memberMenu: which, in what order). Its
 *  model, effort and mode are its session's own, set as the one-agent chat's keys set them.
 *  may: the one looking is of the channel's people; else only what reads is offered. call:
 *  "@nick" into the composer; record: its session's record, where the page has its own way
 *  there; ask: 清空, 移出, 注入记录, opened by the one who draws the menu (MemberAsks). */
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
  const mode = useMode(s);
  const m = useModelMenu(s);
  const mine = ownAgent(channel, a);
  const act = (f: () => Promise<unknown>, err: string) => () => void f().catch((e) => toast(err, { error: true, description: errText(e) }));
  const recv = (r: string) =>
    r !== a.recv &&
    void api.patchMember(channel, a.member, { recv: r }).then(
      (ms) => qc.setQueryData<ChannelMembers>(["members", channel], ms),
      (e) => toast(t("err.action"), { error: true, description: memberError(e) }),
    );
  // Each after a frame: the menu shuts first, and what opens is not shut with it.
  const later = (f: () => void) => () => requestAnimationFrame(f);
  // 清空 asked in its own place: pressed, it turns red and asks again, the menu left open; pressed
  // again, /clear (kumo, 2026-11-17: no card for it). The menu shut, it is as it was.
  const [clearing, setClearing] = useState(false);
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
    clear: (i) =>
      clearing
        ? item(i, <Eraser size={15} />, t("channel.clearConfirm"), act(() => api.send(a.session, "/clear"), t("err.action")), { key: "clear", tone: "text-err", note: t("channel.clearHint") })
        : item(i, <Eraser size={15} />, t("channel.clear"), () => setClearing(true), { key: "clear", keep: true }),
    // The composer's model menu itself (kumo, 2026-11-16): its models, the effort track, UltraCode.
    model: (i) => (
      <Menu.SubmenuRoot key="model">
        <Menu.SubmenuTrigger style={{ "--i": i } as CSSProperties} className={ITEM}>
          <span className="text-ink-2">
            <Box size={15} />
          </span>
          <span className="flex-1">{t("composer.model")}</span>
          <span className="max-w-40 truncate text-xs text-ink-3">{[m.label, m.level && m.levels.length > 0 ? levelName(m.level) : ""].filter(Boolean).join(" · ")}</span>
          <ChevronRight size={14} className="text-ink-4" />
        </Menu.SubmenuTrigger>
        <Menu.Portal>
          <Menu.Positioner className="z-50" sideOffset={4} collisionPadding={8}>
            <Menu.Popup className={MODEL_POPUP}>
              <SessionModels m={m} />
            </Menu.Popup>
          </Menu.Positioner>
        </Menu.Portal>
      </Menu.SubmenuRoot>
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
    recv: (i) => (
      <Pick
        key="recv"
        icon={<Mail size={15} />}
        label={t("channel.recv")}
        now={t(a.recv === "all" ? "channel.recvAll" : "channel.recvAt")}
        value={a.recv}
        options={[
          { value: "all", label: t("channel.recvAll") },
          { value: "at", label: t("channel.recvAt") },
        ]}
        set={recv}
        i={i}
      />
    ),
    edit: (i) => item(i, <PencilLine size={15} />, t("profile.edit"), later(() => ask("edit"))),
    session: (i) => item(i, <ScrollText size={15} />, t("channel.session"), () => view?.()),
    given: (i) => item(i, <Inbox size={15} />, t("channel.given"), later(() => ask("given"))),
    // The session the channel is stays in it: shown, not offered.
    remove: (i, disabled) => item(i, <UserMinus size={15} />, t("channel.remove"), later(() => ask("remove")), disabled ? { disabled, note: t("channel.ownAgent") } : { tone: "text-err" }),
  };
  const list = memberMenu({ may, own: mine, call: !!call && !!a.nick, models: m.models.length, modes: mode.modes.length, busy: BUSY.has(s.status), session: !!view });
  return <>{list.map((x, i) => draw[x.key](i, x.disabled))}</>;
}

/** 移出, from wherever it is asked (the face's menu, the members' list at the bar's right): the
 *  one card at anchor, the member out only once it is confirmed (docs/前端设计规范.md "一样东西
 *  一个实现"). The session stays: only its place in the channel goes. */
export function RemoveAsk({ channel, member, nick, anchor, open, onOpenChange }: { channel: string; member: string; nick: string; anchor: RefObject<HTMLElement | null>; open: boolean; onOpenChange: (o: boolean) => void }) {
  const qc = useQueryClient();
  const [busy, setBusy] = useState(false);
  const done = () => onOpenChange(false);
  const out = async () => {
    setBusy(true);
    try {
      qc.setQueryData<ChannelMembers>(["members", channel], await api.removeMember(channel, member));
      done();
    } catch (e) {
      toast(t("err.action"), { error: true, description: memberError(e) });
    } finally {
      setBusy(false);
    }
  };
  const title = t("channel.removeTitle", { nick });
  return (
    <At anchor={anchor} open={open} onOpenChange={onOpenChange} className="@container w-[min(340px,calc(100vw-16px))] p-2.5 pl-3">
      <div role="alertdialog" aria-label={title}>
        <CardHead
          icon={<UserMinus size={16} />}
          title={title}
          summary={t("channel.removeHint")}
          keys={
            <>
              <SecondKey onClick={done}>{t("me.cancel")}</SecondKey>
              <MainKey busy={busy} onClick={() => void out()}>
                {t("channel.remove")}
              </MainKey>
            </>
          }
        />
      </div>
    </At>
  );
}

const stamp = (ms: number, lang: string) => new Date(ms).toLocaleString(lang === "zh" ? "zh-CN" : "en", { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit", second: "2-digit" });

/** 注入记录 (kumo, 2026-11-16: what it got, all of it): everything its agent was given, the newest
 *  first: its system prompt, each channel's message with what came with it, each other line
 *  (a message, Iridium's own), what its ir was answered with. Each as it got it, whole, folded
 *  when long; where its parts came from under 来源. Inside the popover at its face (At). */
function Given({ a }: { a: AgentRow }) {
  const lang = useLang();
  const q = useQuery({ queryKey: ["injections", a.session], queryFn: () => api.injections(a.session), staleTime: 0, retry: false });
  const label = (k: GivenKind, seq?: number) => (k === "turn" ? (seq ? `#${seq}` : "—") : t(k === "system" ? "channel.givenSystem" : k === "ir" ? "channel.givenIr" : "channel.givenMessage"));
  return (
    <>
      <div className="flex h-10 shrink-0 items-center gap-2 px-3.5">
        <Logo agent={a.agent} size={16} />
        <span className="min-w-0 flex-1 truncate text-sm font-semibold">
          {t("channel.given")} · {a.nick}
        </span>
      </div>
      <div className="no-bar min-h-0 flex-1 overflow-y-auto px-3.5 pb-3.5">
        {q.error ? <p className="m-0 py-6 text-center text-sm text-err">{errText(q.error)}</p> : q.data && !q.data.length ? <p className="m-0 py-6 text-center text-sm text-ink-3">{t("channel.givenNone")}</p> : null}
        <ol className="m-0 flex list-none flex-col gap-4 p-0">
          {q.data?.map((g) => {
            const kind = givenKind(g.record);
            const text = givenText(g.record);
            return (
              <li key={g.id} className="g-fade">
                <div className="flex items-baseline gap-2 text-sm">
                  <span className="font-semibold tabular-nums">{label(kind, g.seq)}</span>
                  <span className="text-xs text-ink-3 tabular-nums">{stamp(g.at, lang)}</span>
                </div>
                {text && (
                  <div className="mt-1.5 rounded-md bg-fill px-2.5 py-2">
                    <LongWords fold lines={text.split("\n").length}>
                      <pre className="m-0 font-mono text-xs leading-5 break-words whitespace-pre-wrap text-ink">{text}</pre>
                    </LongWords>
                  </div>
                )}
                <Sources parts={givenParts(g.record)} open={!text} />
              </li>
            );
          })}
        </ol>
      </div>
    </>
  );
}

/** Where an injection's parts came from: each part's source, the values its slots took, the
 *  messages it cites; folded under 来源 when the text is shown (open: it is all there is). */
function Sources({ parts, open: first }: { parts: ReturnType<typeof givenParts>; open: boolean }) {
  const [open, setOpen] = useState(first);
  if (!parts.length) return null;
  return (
    <div className="mt-1">
      {!first && (
        <button type="button" aria-expanded={open} onClick={() => setOpen(!open)} className="flex items-center gap-0.5 rounded-sm text-xs text-ink-3 hover:text-ink-2">
          {t("channel.givenSources")}
          <ChevronRight size={12} className={cn("transition-transform duration-(--dur-2)", open && "rotate-90")} />
        </button>
      )}
      <div inert={!open || undefined} aria-hidden={!open || undefined} className={cn("grid transition-[grid-template-rows,opacity] duration-(--dur-2) ease-(--ease-out)", open ? "grid-rows-[1fr]" : "grid-rows-[0fr] opacity-0")}>
        <div className="min-h-0 overflow-hidden">
          <ul className="m-0 mt-1 flex list-none flex-col gap-1 p-0">
            {parts.map((p, i) => (
              <li key={i} className="rounded-md bg-fill px-2.5 py-1.5">
                <div className="flex flex-wrap items-baseline gap-x-2 gap-y-0.5">
                  <span className="font-mono text-xs break-all text-ink">{p.source}</span>
                  {p.refs.map((r) => (
                    <span key={r} className="text-xs text-ink-2 tabular-nums">
                      {r}
                    </span>
                  ))}
                </div>
                {p.values.map(([k, val]) => (
                  <div key={k} className="mt-0.5 flex gap-2 text-xs">
                    <span className="shrink-0 text-ink-3">{k}</span>
                    <span className="min-w-0 break-words whitespace-pre-wrap text-ink-2">{val}</span>
                  </div>
                ))}
              </li>
            ))}
          </ul>
        </div>
      </div>
    </div>
  );
}
