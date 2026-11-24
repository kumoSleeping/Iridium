// The chat pane: every conversation (0, 1, or several agents) is drawn by ChannelPage, backed by
// the channel's log alone — no separate single-chat page, no transcript/channel merging.
// Read-only TranscriptView serves subagents, external (elsewhere) sessions, and viewing an agent's
// raw session record (?record=1).
import { Menu } from "@base-ui/react/menu";
import { Popover } from "@base-ui/react/popover";
import { ArrowLeft, BarChart3, Ellipsis, FolderOpen, Globe, ListTree, Plus, Settings } from "lucide-react";
import { lazy, memo, Suspense, useContext, useEffect, useMemo, useRef, useState, type ComponentProps } from "react";
import type { Device, Session, SessionState as LiveSession } from "../api/types.gen";
import { queryClient } from "../api/client";
import { AppKey, WinKeys } from "../appkey";
import { chatOf, firstScreen, fresh, stateAhead, type Draft as Drafted } from "../chats";
import { WsPlus } from "../create";
import { LOCAL_ME, useMachines, useMe, useWhoList, useWorkspaces } from "../data";
import { t, useLang } from "../i18n";
import { LinkDot } from "../link";
import { shownPath } from "../machines";
import { back as goBack, openChat, openRecord, openUsage } from "../route";
import { MenuItems, Rename, sessionActions, sessionTags } from "../sidebar";
import { useList, useSession } from "../store";
import { carries, drafts, opened } from "../stream";
import { toast } from "../toast";
import { groupPath } from "../tree";
import { agentName, cn, IrMark, lampOf, LogoLamp, PopSurface, sessionStatusText, tilde, Tip, titleOf, untitled } from "../ui";
import { VoiceDock } from "../voice/dock";
import { Activity, TurnFrame } from "./atwork";
import { ChannelCard } from "./channel/channelcard";
import { EditBody } from "./channel/edit";
import { closeEdit, useEditing } from "./channel/editing";
import { Face, FaceStack } from "./channel/face";
import { isMulti } from "./channel/lib";
import { ChannelPage } from "./channel/page";
import { CARD_POP } from "./channel/profilecard";
import { renameOnly } from "./cmd";
import { Crash } from "./crash";
import { dispatched, type Start } from "./dispatch";
import { folded, foldKey } from "./fold";
import { Draft } from "./markdown";
import { PublicLink, publicLinkQ } from "./publiclink";
import { sourceOf, withDevice } from "./refs";
import { Said, When } from "./said";
import { Stick } from "./stick";
import { Dispatching, SubFaceKey, SubHead, useSub } from "./subwork";

// Top bar's channel controls, loaded lazily: ⋯'s 编辑资料 on a phone (ChannelMenu) and the
// viewers/members keys at the bar's right (TopRight).
const ChannelMenu = lazy(() => import("./channel/top"));
const TopRight = lazy(() => import("./channel/top").then((m) => ({ default: m.TopRight })));

const BUSY = new Set(["running", "compacting", "starting", "waiting"]);
const turning = (s: { status: string; asks: unknown[]; denials?: unknown[] }) => BUSY.has(s.status) && !(s.status === "waiting" && !s.asks.length && !!s.denials?.length);
const ghostOf = (row: Session): LiveSession => ({ ...row, asks: [], tasks: [], denials: [], queue: [], end: 0, proc: -1 });
const renamesDraft = (d: Drafted) => !!d.tool && renameOnly({ name: d.tool.name, input: d.text });

/** sheet: on a phone, the sidebar's sheet is out this tall (sheet.tsx): the bottom stack sinks out
 *  under it. record: true opens the read-only TranscriptView for this session. */
export function ChatPage({ id, record, sheet = 0, onSheet, onMe }: { id: string; record?: boolean; sheet?: number; onSheet?: () => void; onMe?: () => void }) {
  const list = useList();
  const row = list?.find((x) => x.id === id);
  const real = useSession(id);
  const s = real ?? row;
  if (record || s?.subagent || s?.status === "elsewhere") {
    return <TranscriptView key="record" id={id} sheet={sheet} onSheet={onSheet} onMe={onMe} onBack={record ? goBack : undefined} />;
  }
  return (
    <ChannelPage
      key="channel"
      id={id}
      sheet={sheet}
      record={openRecord}
      bar={(cur) => <Bar s={cur} onSheet={onSheet} onMe={onMe} />}
      subTurn={(sub) => <SubTurn s={sub} />}
    />
  );
}

/** Minimal read-only transcript viewer for subagents, external (elsewhere) sessions, and inspecting
 *  an agent's raw session record (?record=1). Never touches channelLog or merging. */
function TranscriptView({ id, sheet = 0, onSheet, onMe, onBack }: { id: string; sheet?: number; onSheet?: () => void; onMe?: () => void; onBack?: () => void }) {
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

  if (!s) {
    const gone = lost && list !== undefined && !row;
    return (
      <div className="relative h-full">
        <Bar s={row} onSheet={onSheet} onMe={onMe} onBack={onBack} recordOnly />
        {gone && <p className="g-fade m-0 pt-24 text-center text-sm text-ink-3">{t("chat.gone")}</p>}
      </div>
    );
  }

  const cwd = s.cwd ?? row?.cwd ?? "";
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
      <Stick scroller={scroller} tail={grew} older={() => log.older()} busy={busy} draw={changed} place={chat.place} bottom={bottomH} style={{ paddingTop: 64, paddingBottom: bottomH + 20 }}>
        <div data-column className="mx-auto flex max-w-(--chat-w) flex-col gap-5 px-4 sm:px-6">
          {rows.map((b) => (
            <Said key={b.kind === "fold" ? (foldKey(b.steps) ?? b.at) : b.at} b={b} session={id} cwd={cwd} source={source} onJump={openChat} />
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
      <Bar s={s} onSheet={onSheet} onMe={onMe} onBack={onBack} recordOnly />
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
const Bar = memo(function Bar({ s, onSheet, onMe, onBack, recordOnly }: { s?: Session; onSheet?: () => void; onMe?: () => void; onBack?: () => void; recordOnly?: boolean }) {
  const list = useList();
  const of = s && (list?.find((x) => x.id === s.id) ?? s);
  const stack = s && of && isMulti(of) ? of.agents!.map((a) => ({ id: a.session, nick: a.nick, agent: list?.find((x) => x.id === a.session)?.agent ?? a.agent ?? s.agent })) : undefined;
  const soloRow = s && of && of.bare && of.agents?.length === 1 ? of.agents[0] : undefined;
  const soloAgent = soloRow ? (list?.find((x) => x.id === soloRow.session)?.agent ?? soloRow.agent ?? s?.agent) : undefined;
  const solo = soloRow && soloAgent ? { id: soloRow.session, agent: soloAgent } : undefined;
  const ws = useWorkspaces().data ?? [];
  const machines = useMachines();
  const [renaming, setRenaming] = useState(false);
  const [making, setMaking] = useState(false);
  const [publicOpen, setPublicOpen] = useState(false);
  const late = useRef(!s).current ? "g-fade" : "";
  const more = useRef<HTMLButtonElement>(null);
  const usageHere = () => {
    const b = more.current?.getBoundingClientRect();
    openUsage(b && { x: b.right, y: b.bottom + 6, end: true }, s?.machine);
  };
  const folder = s ? (s.machine ? shownPath(groupPath(s), machines).real : tilde(s.cwd)) : "";
  const where = folder ? [{ icon: <FolderOpen size={15} />, label: folder, run: () => void navigator.clipboard.writeText(folder).then(() => toast(t("toast.copied")), () => {}) }] : [];
  const lead = [...where, { icon: <Plus size={15} />, label: t("menu.new"), run: () => requestAnimationFrame(() => setMaking(true)) }, ...(onSheet ? [{ icon: <ListTree size={15} />, label: t("nav.sidebar"), run: onSheet }] : []), ...(onMe ? [{ icon: <Settings size={15} />, label: t("nav.me"), run: onMe }] : []), { icon: <BarChart3 size={15} />, label: t("usage.title"), run: usageHere }];
  const acts = s ? [...sessionActions(s, () => setRenaming(true)), { icon: <Globe size={15} />, label: t("public.menu"), run: () => void Promise.race([queryClient.prefetchQuery(publicLinkQ(s.id)), new Promise((r) => setTimeout(r, 300))]).then(() => setPublicOpen(true)) }] : [];
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
            </>
          )}
        </div>
        {s && !recordOnly && (
          <Suspense fallback={null}>
            <TopRight id={s.id} />
          </Suspense>
        )}
        {onSheet && <LinkDot />}
        {!s && <span aria-hidden className="size-8 shrink-0 pointer-coarse:size-10" />}
        {onSheet && <VoiceDock side="bottom" />}
        {s && (
          <span className="relative grid">
            <WsPlus
              key={s.id}
              w={{ id: s.workspace ?? 0, name: ws.find((w) => w.id === s.workspace)?.name ?? "", path: groupPath(s) }}
              trigger={<span aria-hidden className="pointer-events-none absolute inset-0" />}
              open={making}
              setOpen={setMaking}
              opened={(id) => openChat(id)}
            />
            <Menu.Root>
              <Tip label={t("top.more")} side="bottom">
                <Menu.Trigger
                  render={
                    <button ref={more} type="button" aria-label={t("top.more")} className={cn("g-press grid size-8 place-items-center rounded-md text-ink-2 hover:bg-fill hover:text-ink data-popup-open:bg-fill pointer-coarse:size-10", late)}>
                      <Ellipsis size={17} />
                    </button>
                  }
                />
              </Tip>
              <Menu.Portal>
                <Menu.Positioner side="bottom" align="end" sideOffset={6} collisionPadding={8} className="z-50">
                  <MenuItems lead={lead} items={acts} tags={sessionTags(s)}>
                    {!recordOnly && (
                      <Suspense fallback={null}>
                        <ChannelMenu id={s.id} from={lead.length} face={!!onSheet} />
                      </Suspense>
                    )}
                  </MenuItems>
                </Menu.Positioner>
              </Menu.Portal>
            </Menu.Root>
            <PublicLink key={`public:${s.id}`} session={s.id} anchor={more} open={publicOpen} onOpenChange={setPublicOpen} />
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

const noSource = () => ({ kind: "you" }) as ReturnType<ComponentProps<typeof Said>["source"]>;

/** A subagent at work as its own turn under the conversation. */
function SubTurn({ s }: { s: Session }) {
  useLang();
  const { parent, d } = useContext(Dispatching);
  const start: Start | undefined = s.call ? d?.starts.find((x) => x.id === s.call) : undefined;
  const sub = useSub(parent ?? s.parent, { name: start?.name ?? "", model: start?.model, session: s });
  const st = useSession(s.id);
  const [chat] = useState(() => chatOf(s.id));
  const log = chat.log;
  const [, redraw] = useState(0);
  const changed = () => redraw((n) => n + 1);
  useEffect(() => {
    let live = true;
    const more = () => void log.newer().then(() => live && changed(), () => {});
    if (!log.loaded || !fresh(log.came)) more();
    opened.add(more);
    return () => {
      live = false;
      opened.delete(more);
    };
  }, [s.id]);
  const carried = carries(`session:${s.id}`);
  const end = st?.end ?? 0;
  useEffect(() => {
    if (!log.loaded || (carried && end <= log.end)) return;
    let live = true;
    void log.newer().then(() => live && changed(), () => {});
    return () => void (live = false);
  }, [carried ? end : s.updatedAt, log.loaded]);
  const shown = folded(log.blocks).filter((b) => b.kind !== "user");
  const running = shown.flatMap((b) => (b.kind === "running" ? [b.tool] : []));
  const rows = shown.filter((b) => b.kind !== "running");
  const cwd = st?.cwd ?? s.cwd;
  const node = (b: (typeof rows)[number]) => <Said key={b.kind === "fold" ? (foldKey(b.steps) ?? b.at) : b.at} b={b} session={s.id} cwd={cwd} source={noSource} onJump={openChat} />;
  const face = <SubFaceKey sub={sub} />;
  const time = rows.map((b) => (b.kind === "fold" ? b.steps[0]?.time : "time" in b ? b.time : undefined)).find(Boolean);
  const head = (
    <SubHead name={sub.name}>
      <When time={time} />
    </SubHead>
  );
  const activity = (under: boolean) => <Activity calls={[]} running={running} cwd={cwd} quiet grew={`${s.id} ${log.end} ${running.length}`} under={under} />;
  return (
    <Dispatching.Provider value={{ parent: s.id, by: sub.name, d: dispatched(shown) }}>
      <div role="group" aria-label={`${sub.name} · ${t("sub.working")}`} className="flex min-w-0 flex-col gap-5">
        {rows.length ? (
          <>
            <TurnFrame face={face} head={head}>
              {rows.map(node)}
            </TurnFrame>
            {activity(false)}
          </>
        ) : (
          <TurnFrame face={face} head={head}>
            {activity(true)}
          </TurnFrame>
        )}
      </div>
    </Dispatching.Provider>
  );
}
