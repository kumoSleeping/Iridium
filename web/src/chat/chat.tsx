// The chat pane: every conversation (0, 1, or several agents) is drawn by ChannelPage, backed by
// the channel's log alone — no separate single-chat page, no transcript/channel merging.
// Read-only TranscriptView serves subagents, external (elsewhere) sessions, and viewing an agent's
// raw session record (?record=1).
import { Menu } from "@base-ui/react/menu";
import { Popover } from "@base-ui/react/popover";
import { ArrowLeft, BarChart3, Ellipsis, Gamepad2, House, ListTree, Plus, Settings } from "lucide-react";
import { lazy, memo, Suspense, useEffect, useMemo, useRef, useState, type ComponentProps } from "react";
import { lazyAhead, useAhead } from "../prefetch";
import type { Device, Session, SessionState as LiveSession } from "../api/types.gen";
import { keepPreviousData } from "@tanstack/react-query";
import { api } from "../api/client";
import { useQuery } from "../api/query";
import { AppKey, WinKeys } from "../appkey";
import { AssembledChip } from "../assembled";
import { chatOf, firstScreen, fresh, stateAhead, type Draft as Drafted } from "../chats";
import { newChannel } from "../create";
import { LOCAL_ME, useMachines, useMe, useWhoList, useWorkspaces } from "../data";
import { t, useLang } from "../i18n";
import { LinkDot } from "../link";
import { back as goBack, go, openChat, openRecord, openUsage } from "../route";
import { MenuItems, Rename, sessionActions, sessionTags, SideKey } from "../sidebar";
import { useList, useSession } from "../store";
import { drafts, opened } from "../stream";
import { groupPath } from "../tree";
import { agentName, cn, IrMark, lampOf, LogoLamp, PopSurface, sessionStatusText, Tip, titleOf, untitled, MenuPortal } from "../ui";
import { VoiceDock } from "../voice/dock";
import { openGame } from "../game/open";
import { Activity } from "./atwork";
import { ChannelCard } from "./channel/channelcard";
import { EditBody } from "./channel/edit";
import { closeEdit, useEditing } from "./channel/editing";
import { Face, FaceStack } from "./channel/face";
import { isMulti } from "./channel/lib";
import { ChannelPage } from "./channel/page";
import { CARD_POP } from "./channel/profilecard";
import { renameOnly } from "./cmd";
import { Crash } from "./crash";
import { folded, foldKey } from "./fold";
import { Draft } from "./markdown";
import { sourceOf, withDevice } from "./refs";
import { SystemPrompt } from "./context";
import { givenOf } from "./given";
import { Said } from "./said";
import { Stick } from "./stick";
import { pressAdd } from "./channel/adding";

// Top bar's channel controls, loaded lazily: ⋯'s 编辑资料 on a phone (ChannelMenu) and the
// viewers/members keys at the bar's right (TopRight).
// ChannelMenu's code asked for ahead (its ⋯ pointed at, or idle): a lazy part is its fallback in its
// first draw even when its code is there, and the menu grew by its row a moment after it opened.
const ChannelMenu = lazyAhead(() => import("./channel/top").then((m) => m.default));
const TopRight = lazy(() => import("./channel/top").then((m) => ({ default: m.TopRight })));

const BUSY = new Set(["running", "compacting", "starting", "waiting"]);
const turning = (s: { status: string }) => BUSY.has(s.status);
const ghostOf = (row: Session): LiveSession => ({ ...row, asks: [], tasks: [], queue: [], end: 0, proc: -1 });
const renamesDraft = (d: Drafted) => !!d.tool && renameOnly({ name: d.tool.name, input: d.text });

/** sheet: on a phone, the sidebar's sheet is out this tall (sheet.tsx): the bottom stack sinks out
 *  under it. record: true opens the read-only TranscriptView for this session. */
export function ChatPage({ id, record, sheet = 0, onSheet, onMe, onSide }: { id: string; record?: boolean; sheet?: number; onSheet?: () => void; onMe?: () => void; onSide?: () => void }) {
  const list = useList();
  const row = list?.find((x) => x.id === id);
  const real = useSession(id);
  const s = real ?? row;
  if (record || s?.subagent || s?.status === "elsewhere") {
    return <TranscriptView key="record" id={id} sheet={sheet} onSheet={onSheet} onMe={onMe} onSide={onSide} onBack={record ? goBack : undefined} />;
  }
  return (
    <ChannelPage
      key="channel"
      id={id}
      sheet={sheet}
      record={openRecord}
      bar={(cur) => <Bar s={cur} onSheet={onSheet} onMe={onMe} onSide={onSide} />}
    />
  );
}

/** Minimal read-only transcript viewer for subagents, external (elsewhere) sessions, and inspecting
 *  an agent's raw session record (?record=1). Never touches channelLog or merging. */
function TranscriptView({ id, sheet = 0, onSheet, onMe, onSide, onBack }: { id: string; sheet?: number; onSheet?: () => void; onMe?: () => void; onSide?: () => void; onBack?: () => void }) {
  useLang();
  const list = useList();
  const row = list?.find((x) => x.id === id);
  const real = useSession(id);
  const ghost = useMemo(() => row && ghostOf(row), [row]);
  const s = real ?? ghost;
  const [lost, setLost] = useState(false);
  useEffect(() => {
    if (!real) void stateAhead(id).then((there) => !there && setLost(true));
  }, [id]);
  const had = useRef(false);
  if (real) had.current = true;
  else if (had.current && !lost) setLost(true);
  if (real && lost) setLost(false);
  const [chat] = useState(() => chatOf(id));
  const log = chat.log;
  const [, redraw] = useState(0);
  const changed = () => redraw((n) => n + 1);
  const me = useMe().data;
  const self = me ?? LOCAL_ME;
  const terminals = useWhoList();
  const workspaces = useWorkspaces().data;
  const scroller = useRef<HTMLDivElement>(null);

  useEffect(() => {
    let live = true;
    const more = () => void (log.loaded ? log.newer() : firstScreen(id, chat, undefined, true).then(() => (log.loaded ? undefined : log.newer()))).then(() => live && changed(), () => {});
    if (!fresh(log.came)) more();
    const hear = (d: { session: string }) => d.session === id && (log.wrote(d as never), changed());
    const back = () => (log.dropDrafts(), more());
    drafts.add(hear);
    opened.add(back);
    return () => {
      live = false;
      drafts.delete(hear);
      opened.delete(back);
    };
  }, [id]);

  const end = s?.end ?? 0;
  useEffect(() => {
    if (!log.loaded || end <= log.end) return;
    if (!log.fromTail(s?.tail)) return void log.newer().then(changed, () => {});
    changed();
  }, [end, log.loaded]);
  useEffect(() => void (real && (log.procIs(real.proc), changed())), [real?.proc]);

  const busy = !!s && turning(s);
  const wasBusy = useRef(busy);
  useEffect(() => {
    if (wasBusy.current && !busy) {
      const upTo = log.mark();
      void log.newer().then(() => (log.dropDrafts(upTo), changed()), () => {});
    }
    wasBusy.current = busy;
  }, [busy]);

  const shown = useMemo(() => folded(log.blocks, busy), [log.blocks, busy]);
  // What its agent was given, kept (given.ts): asked again as a message comes to it.
  const lastIn = log.blocks.findLast((b) => b.kind === "user")?.id ?? "";
  const injections = useQuery({ queryKey: ["injections", id, lastIn], queryFn: () => api.injections(id), retry: false, placeholderData: keepPreviousData, meta: { inside: true } });
  const given = useMemo(() => givenOf(injections.data), [injections.data]);

  if (!s) {
    const gone = lost && list !== undefined && !row;
    return (
      <div className="relative h-full">
        <Bar s={row} onSheet={onSheet} onMe={onMe} onSide={onSide} onBack={onBack} recordOnly />
        {gone && <p className="g-fade m-0 pt-24 text-center text-sm text-ink-3">{t("chat.gone")}</p>}
      </div>
    );
  }

  const cwd = s.cwd ?? row?.cwd ?? "";
  const asGiven = (b: { id?: string }) => (b.id ? given.byInput.get(b.id) : undefined);
  const running = shown.flatMap((b) => (b.kind === "running" ? [b.tool] : []));
  const rows = shown.filter((b) => b.kind !== "running");
  const textDrafts = log.drafts.filter((d) => !d.tool && d.text.trim());
  const toolDrafts = log.drafts.filter((d) => d.tool && !renamesDraft(d));
  const source = (b: { from?: string; fromTerminal?: string; fromDevice?: Device }) => withDevice(sourceOf(b.from, b.fromTerminal, self, terminals, list ?? [], workspaces ?? [], s), b.fromDevice);
  const elsewhere = s.status === "elsewhere";
  const hasFooter = s.subagent || elsewhere;
  const bottomH = hasFooter ? 72 : 24;
  const grew = `${log.blocks.at(-1)?.at} ${log.drafts.length} ${log.drafts.at(-1)?.text.length}`;

  return (
    <div className="relative h-full">
      <Stick scroller={scroller} tail={grew} older={() => log.older()} draw={changed} place={chat.place} bottom={bottomH} style={{ paddingTop: 64, paddingBottom: bottomH + 20 }}>
        <div data-column className="mx-auto flex max-w-(--chat-w) flex-col gap-5 px-4 sm:px-6">
          {/* Its system prompt, as given, where its record is read to its start. */}
          {given.verbatim && given.system && log.loaded && !log.more && <SystemPrompt text={given.system} />}
          {rows.map((b) => (
            <Said key={b.kind === "fold" ? (foldKey(b.steps) ?? b.at) : b.at} b={b} session={id} cwd={cwd} source={source} onJump={openChat} given={given.verbatim ? asGiven : undefined} />
          ))}
          {textDrafts.map((d) => (
            <div key={`${d.proc}.${d.n}`} className="g-fade min-w-0 pr-6">
              <Draft text={d.text} live={!d.whole && busy} />
            </div>
          ))}
          {log.loaded && busy && (
            <Activity calls={toolDrafts} running={running} cwd={cwd} quiet={s.status !== "waiting" && !textDrafts.some((d) => !d.whole)} grew={grew} under />
          )}
          {s.status === "error" && s.error && <Crash error={s.error} />}
        </div>
      </Stick>
      <Bar s={s} onSheet={onSheet} onMe={onMe} onSide={onSide} onBack={onBack} recordOnly />
      {hasFooter && (
        <div inert={sheet > 0} className="g-turn-stay g-sink vt-stack absolute inset-x-0 bottom-0 z-10 px-2.5 pb-[max(14px,calc(env(safe-area-inset-bottom)+6px))] sm:px-5">
          <div className="relative mx-auto max-w-(--chat-w)">
            {s.subagent ? (
              <div className="glass float rest flex h-12 items-center justify-center gap-1 rounded-[20px] text-sm text-ink-3">
                {t("chat.subagent")}
                {s.parent && (
                  <button type="button" className="cursor-pointer text-ink-2 underline-offset-2 hover:underline" onClick={() => openChat(s.parent!)}>
                    {t("chat.subagentParent")}
                  </button>
                )}
              </div>
            ) : (
              <div className="glass float rest flex h-12 items-center justify-center rounded-[20px] text-sm text-ink-3">{t("chat.elsewhere")}</div>
            )}
          </div>
        </div>
      )}
    </div>
  );
}

/** The top bar: optional back button (TranscriptView), the channel/agent face, the name, members
 *  and more. */
const Bar = memo(function Bar({ s, onSheet, onMe, onSide, onBack, recordOnly }: { s?: Session; onSheet?: () => void; onMe?: () => void; onSide?: () => void; onBack?: () => void; recordOnly?: boolean }) {
  const list = useList();
  const of = s && (list?.find((x) => x.id === s.id) ?? s);
  const stack = s && of && isMulti(of) ? of.agents!.map((a) => ({ id: a.session, nick: a.nick, agent: list?.find((x) => x.id === a.session)?.agent ?? a.agent ?? s.agent })) : undefined;
  const soloRow = s && of && of.bare && of.agents?.length === 1 ? of.agents[0] : undefined;
  const soloAgent = soloRow ? (list?.find((x) => x.id === soloRow.session)?.agent ?? soloRow.agent ?? s?.agent) : undefined;
  const solo = soloRow && soloAgent ? { id: soloRow.session, agent: soloAgent } : undefined;
  const machines = useMachines();
  const [renaming, setRenaming] = useState(false);
  const late = useRef(!s).current ? "g-fade" : "";
  const more = useRef<HTMLButtonElement>(null);
  const moreAhead = useAhead(ChannelMenu.ahead, []);
  const usageHere = () => openUsage(s?.machine);
  // 回到主页 (kumo, 2026-11-25): the pane with no chat open, its splash.
  const lead = [{ icon: <House size={15} />, label: t("menu.home"), run: () => go({ name: "home" }) }, onSheet ? { icon: <Plus size={15} />, label: t("channel.addAgent"), run: () => s && requestAnimationFrame(() => pressAdd(s.id)) } : { icon: <Plus size={15} />, label: t("menu.new"), run: () => s && void newChannel(groupPath(s), machines, s.workspace ?? undefined).then((id) => id && openChat(id)) }, ...(onSheet ? [{ icon: <ListTree size={15} />, label: t("nav.sidebar"), run: onSheet }] : []), ...(onMe ? [{ icon: <Settings size={15} />, label: t("nav.me"), run: onMe }] : []), { icon: <BarChart3 size={15} />, label: t("usage.title"), run: usageHere }, { icon: <Gamepad2 size={15} />, label: t("game.open"), run: openGame }];
  const acts = s ? [...sessionActions(s, () => setRenaming(true))] : [];
  return (
    <header data-app-drag className="g-turn-stay vt-bar glass absolute inset-x-0 top-0 z-20 shadow-[0_0.5px_0_var(--edge)]">
      <div className="flex h-12 items-center gap-1 pr-2 pl-2 pointer-coarse:h-13">
        <div className="flex min-w-0 flex-1 items-center gap-2.5 pl-2">
          {onBack && (
            <button type="button" aria-label={t("nav.back")} onClick={onBack} className="g-press -ml-1 grid size-8 shrink-0 place-items-center rounded-md text-ink-2 hover:bg-fill hover:text-ink pointer-coarse:size-10">
              <ArrowLeft size={17} />
            </button>
          )}
          {s && (
            <>
              {onSheet && !onBack && (
                <button type="button" aria-label={t("nav.sidebar")} onClick={onSheet} className={cn("g-press -m-2 grid place-items-center rounded-md p-2 hover:bg-fill", late)}>
                  <IrMark size={18} className="text-ink" />
                </button>
              )}
              {/* A computer's with the sidebar shut (kumo, 2026-11-26): [Ir] as on a phone, where the
                  sidebar's head has it (both 16px in, a 48px strip), so it stays put as it is pressed. */}
              {!onSheet && !onBack && onSide && <SideKey label={t("nav.unshut")} onClick={onSide} />}
              <Tip label={stack ? stack.map((a) => a.nick).join(" · ") : `${agentName(solo?.agent ?? s.agent)} · ${sessionStatusText(s)}`} side="bottom">
                <FaceKey s={s} stack={stack} solo={solo} className={late} />
              </Tip>
              {renaming ? (
                <span className="min-w-0 flex-1">
                  <Rename s={s} done={() => setRenaming(false)} className="h-7 font-semibold" />
                </span>
              ) : (
                <h1 className={cn("m-0 min-w-0 truncate text-base font-semibold", late, untitled(s) && "text-ink-3")} onDoubleClick={() => setRenaming(true)}>
                  {titleOf(s)}
                </h1>
              )}
              <AssembledChip />
            </>
          )}
          {/* A phone's link, after the name (kumo, 2026-11-25): its round trip grows and shrinks
              out of the name's room, the keys at the right staying where they are. */}
          {onSheet && <LinkDot className="shrink-0" />}
          {onSide && (
            <span className="flex shrink-0">
              <LinkDot className="shrink-0" />
            </span>
          )}
        </div>
        {s && !recordOnly && (
          <Suspense fallback={null}>
            <TopRight id={s.id} />
          </Suspense>
        )}
        {!s && <span aria-hidden className="size-8 shrink-0 pointer-coarse:size-10" />}
        {onSheet && <VoiceDock side="bottom" />}
        {s && (
          <span className="relative grid">
            <Menu.Root>
              <Tip label={t("top.more")} side="bottom">
                <Menu.Trigger
                  render={
                    <button ref={more} type="button" aria-label={t("top.more")} {...moreAhead} className={cn("g-press grid size-8 place-items-center rounded-md text-ink-2 hover:bg-fill hover:text-ink data-popup-open:bg-fill pointer-coarse:size-10", late)}>
                      <Ellipsis size={17} />
                    </button>
                  }
                />
              </Tip>
              <MenuPortal>
                <Menu.Positioner side="bottom" align="end" sideOffset={6} collisionPadding={8} className="z-50">
                  <MenuItems lead={lead} items={acts} tags={sessionTags(s)} className="min-w-56">
                    {!recordOnly && (
                      <Suspense fallback={null}>
                        <ChannelMenu id={s.id} from={lead.length} face={!!onSheet} />
                      </Suspense>
                    )}
                  </MenuItems>
                </Menu.Positioner>
              </MenuPortal>
            </Menu.Root>
            <EditAt id={s.id} anchor={more} />
          </span>
        )}
        <AppKey />
        <WinKeys />
      </div>
    </header>
  );
});

/** 编辑资料 asked from a menu with no face to open out of (⋯ on a phone; editing.ts). */
function EditAt({ id, anchor }: { id: string; anchor: React.RefObject<HTMLElement | null> }) {
  const editing = useEditing(id);
  const last = useRef(editing);
  if (editing) last.current = editing;
  return (
    <Popover.Root open={!!editing} onOpenChange={(o) => !o && closeEdit()}>
      <PopSurface anchor={anchor.current} align="end" className={CARD_POP}>
        {last.current && <EditBody key={last.current.agent ?? ""} editing={last.current} done={closeEdit} />}
      </PopSurface>
    </Popover.Root>
  );
}

/** The channel's face before the title, pressed: its card in a popover out of the face. */
function FaceKey({ s, stack, solo, className, ...rest }: { s: Session; stack?: { id: string; agent: Session["agent"] }[]; solo?: { id: string; agent: Session["agent"] } } & ComponentProps<"button">) {
  const [open, setOpen] = useState(false);
  const fallback = stack ? <FaceStack members={stack} size={18} /> : solo ? <Face id={solo.id} agent={solo.agent} size={18} /> : undefined;
  const face = <LogoLamp agent={solo?.agent ?? s.agent} status={lampOf(s)} size={18} mark={<Face key={s.id} id={s.id} agent={solo?.agent ?? s.agent} size={18} channel none={fallback} />} />;
  return (
    <Popover.Root open={open} onOpenChange={setOpen}>
      <Popover.Trigger
        render={
          <button {...rest} type="button" aria-label={titleOf(s)} className={cn("g-press -m-1 grid place-items-center rounded-full p-1 hover:bg-fill data-popup-open:bg-fill", className)}>
            {face}
          </button>
        }
      />
      <PopSurface className={CARD_POP}>
        <ChannelCard id={s.id} agent={solo?.agent ?? s.agent} done={() => setOpen(false)} />
      </PopSurface>
    </Popover.Root>
  );
}
