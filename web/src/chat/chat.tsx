// A session's chat (plan "网页", web/sample): a frosted top bar, the conversation by source, newest at
// the bottom and paged back as you scroll up, the agent's words as it writes them, and the bottom
// stack: the queue, the confirmation cards, the composer, the mode and model keys. Its blocks come
// from the transcript (GET messages, after= what the open session's state says was written); its
// drafts from the stream. Loaded only once a chat opens or the page is idle.
import { Menu } from "@base-ui/react/menu";
import { BarChart3, Ellipsis, ListTree, Plus, Settings } from "lucide-react";
import type { Status } from "../api/types.gen";
import { useEffect, useLayoutEffect, useMemo, useRef, useState, type DragEvent } from "react";
import type { SessionState as LiveSession } from "../api/types.gen";
import { api } from "../api/client";
import { LinkDot } from "../link";
import type { Session } from "../api/types.gen";
import { WsPlus } from "../create";
import { chatOf, compactSent, stateAhead, type Draft as Drafted, type Sent } from "../chats";
import { LOCAL_ME, useAgent, useMe, useWhoList, useWorkspaces } from "../data";
import { t, useLang } from "../i18n";
import { pushIn } from "../motion";
import { openChat, openUsage } from "../route";
import { PULL } from "../sheet";
import { MenuItems, Rename, sessionActions, sessionTags } from "../sidebar";
import { useConfirms, useList, useMachines, useSession } from "../store";
import { drafts, opened } from "../stream";
import { errText } from "../toast";
import { groupPath } from "../tree";
import { agentName, cn, Lamp, lampOf, LogoLamp, sessionStatusText, tilde, Tip, titleOf, untitled } from "../ui";
import { MachineFace, shownPath } from "../machines";
import { openPlace } from "../places";
import { Asks, Denials } from "./asks";
import { renameOnly } from "./cmd";
import { Composer, type ComposerHandle } from "./composer";
import { folded, type Tool } from "./fold";
import { ToolDraft, Waiting } from "./live";
import { Draft } from "./markdown";
import { ContextRing, ModeKey, ModelKey, modelView, PresetKey, QuotaBars, useMode } from "./meters";
import { Queue } from "./queue";
import { ShellRuns } from "./shell";
import { Crash } from "./crash";
import { placesIn, PLACE_DRAG, SESSION_DRAG, sourceOf, yours } from "./refs";
import { placeDropped, type NamedPlace } from "./at";
import { PlaceIcon, placeOf } from "./mention";
import { spotsOf, type Spot } from "./where";
import { Compaction, Pending, Running, Said } from "./said";
import { Stick, toEnd } from "./stick";

const BUSY = new Set(["running", "compacting", "starting", "waiting"]);
/** A turn is on: at work, or waiting on an ask. Waiting only on a step auto mode denied, the turn is
 *  over (session/release.go): no stop key, no activity row. */
const turning = (s: { status: string; asks: unknown[]; denials?: unknown[] }) => BUSY.has(s.status) && !(s.status === "waiting" && !s.asks.length && !!s.denials?.length);

/** The bottom stack's height as last measured: a chat opens padded by it, not by a guess and then
 *  moved (one frame drawn short or long, the conversation jumping after). */
let lastStackH = 140;

/** A session's state before the stream (or chats.ts stateAhead) brings it, from its row: the chat is
 *  drawn whole at once, its composer and conversation, its keys after (ghost). */
const ghostOf = (row: Session): LiveSession => ({ ...row, asks: [], tasks: [], denials: [], queue: [], end: 0, proc: -1 });

/** sheet: on a phone, the sidebar's sheet is out this tall (sheet.tsx): the bottom stack sinks out
 *  under it; the conversation stays where it is, not pushed up with it (the user, 2026-11-06). onSheet: on a phone, brings it out,
 *  from the bar's ⋯ menu; onMe, there too, this terminal's own sheet (个人). */
export function ChatPage({ id, sheet = 0, onSheet, onMe }: { id: string; sheet?: number; onSheet?: () => void; onMe?: () => void }) {
  useLang();
  const real = useSession(id);
  const list = useList();
  const row = list?.find((x) => x.id === id);
  const ghost = !real && !!row;
  const ghostS = useMemo(() => row && ghostOf(row), [row]);
  const s = real ?? ghostS;
  // Gone only once asked and not there, or there and then not: a session just made is in no list
  // yet, and is not to show "no such session" as it opens.
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
  const terminals = useWhoList();
  const workspaces = useWorkspaces().data;
  const confirms = useConfirms();

  // The newest page, or what was kept; then what came since.
  useEffect(() => {
    let live = true;
    const more = () => void (chat.coming ?? log.newer()).then(() => live && changed(), () => {});
    more();
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
  // The transcript grew past what is shown: the blocks since, from the state's tail when it has
  // them, else fetched.
  const end = s?.end ?? 0;
  useEffect(() => {
    if (!log.loaded || end <= log.end) return;
    if (!log.fromTail(s?.tail)) return void log.newer().then(changed, () => {});
    changed();
  }, [end]);
  useEffect(() => void (real && log.procIs(real.proc), changed()), [real?.proc]);
  // The turn ended: what was drafted is in the transcript now. Only those drafted by then go: the
  // next turn (queued, sent at once) may begin to write while the page fetches.
  const busy = !!s && turning(s);
  const wasBusy = useRef(busy);
  useEffect(() => {
    if (wasBusy.current && !busy) {
      const upTo = log.mark();
      // A /compact that wrote no compaction (it had nothing to compact) is done too.
      void log.newer().then(() => (log.dropDrafts(upTo), (log.sent = log.sent.filter((x) => x.failed !== undefined || !compactSent(x))), changed()), () => {});
    }
    wasBusy.current = busy;
  }, [busy]);
  // Compacting, or a /compact on its way: the compaction's line, since when.
  const compacting = s?.status === "compacting" || log.sent.some((x) => x.failed === undefined && compactSent(x));
  const compactSince = useRef(0);
  if (!compacting) compactSince.current = 0;
  else if (!compactSince.current) compactSince.current = Date.now();
  // Its agent cleared itself (Claude's own /clear): the conversation goes on in another session,
  // and the page with it, as after Iridium's /clear. Only as it happens here, not on opening one
  // that went on before.
  const seenNext = useRef<{ id: string; next?: string } | null>(null);
  useEffect(() => {
    if (!s) return;
    const was = seenNext.current;
    seenNext.current = { id, next: s.next };
    if (was?.id === id && s.next && s.next !== was.next) openChat(s.next);
  }, [id, !!s, s?.next]);

  // Who sent what; this panel as /api/me has it (its name, avatar, device).
  const self = me ?? LOCAL_ME;
  const source = (b: { from?: string; fromTerminal?: string }) => sourceOf(b.from, b.fromTerminal, self, terminals, list ?? [], workspaces ?? [], s);

  // The bottom stack's height pads the conversation's end.
  const stack = useRef<HTMLDivElement>(null);
  const [stackH, setStackH] = useState(lastStackH);
  useLayoutEffect(() => {
    const el = stack.current;
    if (!el) return;
    // Measured now, before the first paint, then as it changes.
    const measure = () => setStackH((lastStackH = el.offsetHeight));
    measure();
    const ro = new ResizeObserver(measure);
    ro.observe(el);
    return () => ro.disconnect();
  }, [!!s]); // the stack is drawn only once the session or its row has come

  // A message sent: at the bottom, the conversation pushed up by it (the chat's glide) as it fades in.
  const bubbles = useRef(new Map<string, HTMLDivElement>());
  const fly = useRef<string | null>(null);
  const scroller = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => {
    const el = fly.current && bubbles.current.get(fly.current);
    if (!el) return;
    fly.current = null;
    toEnd(scroller.current);
    pushIn(el);
  });

  const composer = useRef<ComposerHandle>(null);
  // A note put into this chat (notes.tsx putIntoSession): its words in the composer, on a line of
  // their own; the event cancelled says it was taken.
  useEffect(() => {
    const put = (e: Event) => {
      if (!composer.current) return;
      composer.current.put((e as CustomEvent<string>).detail);
      e.preventDefault();
    };
    addEventListener("ir:note", put);
    return () => removeEventListener("ir:note", put);
  }, []);
  const [modelOpen, setModelOpen] = useState(false);
  const [modeOpen, setModeOpen] = useState(false);
  const [usageOpen, setUsageOpen] = useState(false);
  const shown = folded(log.blocks);
  const cwd = s?.cwd ?? row?.cwd ?? "";
  // 在哪干活 under the title (plan, 2026-11-10): the places this one sent sessions to, and those its
  // messages name (as far as they are loaded), as the page has them.
  const machines = useMachines();
  const said = `${log.blocks.length}.${log.blocks[0]?.at}.${log.blocks.at(-1)?.at}.${log.sent.length}`;
  const named = useMemo(() => {
    const texts = [...log.blocks.filter((b) => b.kind === "user").map((b) => b.text ?? ""), ...log.sent.map((x) => x.text)];
    const out: NamedPlace[] = [];
    for (const p of texts.flatMap(placesIn)) {
      const at = placeOf(p, machines);
      if (at && !out.some((o) => o.machine === at.machine && o.path === at.path)) out.push({ ...at, name: p.name });
    }
    return out;
  }, [said, machines]);
  const spots = useMemo(() => (row ? spotsOf(row, list ?? [], named, workspaces ?? []) : []), [row, list, named, workspaces]);
  // The bar a row taller with them: the conversation padded as much, and kept where it was as the
  // row comes or goes (the bar's 48px and a gap; the row's 32px).
  const padTop = spots.length ? 96 : 64;
  const padWas = useRef(padTop);
  useLayoutEffect(() => {
    const d = padTop - padWas.current;
    padWas.current = padTop;
    if (d && scroller.current) scroller.current.scrollTop += d;
  }, [padTop]);

  const onSent = (sent: Sent) => {
    sent.from = log.end;
    log.sent.push(sent);
    fly.current = sent.key;
    changed();
  };
  const retry = async (sent: Sent) => {
    sent.failed = undefined;
    changed();
    try {
      sent.input = (await api.send(id, sent.text, sent.files.map((f) => f.ref))).input;
    } catch (e) {
      sent.failed = errText(e);
    }
    changed();
  };
  const files = (e: DragEvent) => e.dataTransfer.types.includes("Files") && (e.preventDefault(), true);
  // A session's row dropped here (sidebar.tsx): named in the composer, for this one to write to it
  // with ir (the user, 2026-11-06): a card among the attachments, one piece (the user, 2026-11-08).
  const session = (e: DragEvent) => e.dataTransfer.types.includes(SESSION_DRAG) && (e.preventDefault(), (e.dataTransfer.dropEffect = "copy"), true);
  // A group's or a folder's row (2026-11-10): that place named, as @ names it.
  const place = (e: DragEvent) => e.dataTransfer.types.includes(PLACE_DRAG) && (e.preventDefault(), (e.dataTransfer.dropEffect = "copy"), true);
  const drop = (e: DragEvent) => {
    if (files(e)) return composer.current?.attach([...e.dataTransfer.files]);
    if (place(e)) {
      const p = placeDropped(e.dataTransfer.getData(PLACE_DRAG));
      return p && composer.current?.name(p);
    }
    if (!session(e)) return;
    const to = list?.find((x) => x.id === e.dataTransfer.getData(SESSION_DRAG));
    if (!to || to.id === id) return;
    composer.current?.name(to.id);
  };

  if (!s) {
    const gone = lost && list !== undefined && !row;
    return (
      <div className="relative h-full">
        <Bar s={row} onSheet={onSheet} onMe={onMe} />
        {gone && <p className="g-fade m-0 pt-24 text-center text-sm text-ink-3">{t("chat.gone")}</p>}
      </div>
    );
  }
  const elsewhere = s.status === "elsewhere";
  // What changes as the conversation grows: it keeps to the bottom, the dots wait again.
  const grew = `${log.blocks.at(-1)?.at} ${log.drafts.length} ${log.drafts.at(-1)?.text.length} ${log.drafts.at(-1)?.whole} ${log.sent.length}`;
  return (
    <div className="relative h-full" data-chat-drop={id} onDragOver={(e) => files(e) || session(e) || place(e)} onDrop={drop}>
      <Stick scroller={scroller} tail={grew} older={() => log.older()} place={chat.place} bottom={stackH} quiet={!!s?.asks.length} style={{ paddingTop: padTop, paddingBottom: stackH + 20 }}>
        <div className="mx-auto flex max-w-[760px] flex-col gap-5 px-4 sm:px-6">
          {/* In a turn, the calls being written or run and the dots share one row at the end, as
              high whatever it shows: the conversation does not jump as a call is drafted, runs and
              folds into the line above (the user, 2026-11-06). */}
          {(busy ? shown.filter((b) => b.kind !== "running") : shown).map((b) => (
            <Said key={b.at} b={b} session={id} cwd={cwd} source={source} onJump={openChat} />
          ))}
          {log.drafts
            .filter((d) => !d.tool || (!busy && !renamesDraft(d)))
            .map((d) =>
              d.tool ? (
                <ToolDraft key={`${d.proc}.${d.n}`} name={d.tool.name} input={d.text} whole />
              ) : (
                <div key={`${d.proc}.${d.n}`} className="g-fade min-w-0 pr-6">
                  <Draft text={d.text} live={!d.whole && busy} />
                </div>
              ),
            )}
          {log.sent.filter((x) => x.failed !== undefined || !compactSent(x)).map((x) => (
            <Pending
              key={x.key}
              s={x}
              who={yours(self)}
              bubbleRef={(el) => void (el ? bubbles.current.set(x.key, el) : bubbles.current.delete(x.key))}
              retry={() => retry(x)}
              drop={() => ((log.sent = log.sent.filter((y) => y !== x)), changed())}
            />
          ))}
          {/* The turn's row, after what you sent: kept, one line high, even out of a turn, so a
              message sent or the dots coming do not move the conversation (the user, 2026-11-06). */}
          {/* Close under what is above it (the user, 2026-11-06): a call's line by the calls before. */}
          {log.loaded && compacting ? (
            <div className="-mt-1">
              <Compaction since={compactSince.current} />
            </div>
          ) : log.loaded && (busy ? <Activity calls={log.drafts.filter((d) => d.tool && !renamesDraft(d))} running={shown.flatMap((b) => (b.kind === "running" ? [b.tool] : []))} cwd={cwd} quiet={s.status !== "waiting" && !log.drafts.some((d) => !d.whole && !d.tool)} grew={grew} /> : s.denials?.length ? <Denials session={id} denials={s.denials} cwd={cwd} stuck={s.status === "waiting" && !s.asks.length} /> : <div aria-hidden className="-mt-3 h-7 pointer-coarse:h-9" />)}
          {s.status === "error" && s.error && <Crash error={s.error} />}
        </div>
      </Stick>
      <Bar s={s} onSheet={onSheet} onMe={onMe} spots={spots} asked={confirms} />
      {/* Under the phone's sheet the stack stays where it is, covered, and is hidden once the sheet is
          up (its glass not drawn again as the chat moves under it); only one taller than the sheet,
          which would show over it or at its round corners, sinks (the user, 2026-11-06). */}
      <div ref={stack} {...{ [PULL]: "" }} data-covered={sheet > 0 || undefined} data-sunk={(sheet > 0 && stackH > sheet - 16) || undefined} inert={sheet > 0} className="g-turn-stay g-sink vt-stack absolute inset-x-0 bottom-0 z-10 px-2.5 pb-[max(14px,calc(env(safe-area-inset-bottom)+6px))] sm:px-5">
        {/* Paper from the composer's middle down: the conversation scrolled under it stops there. */}
        {!elsewhere && !s.subagent && <div aria-hidden className="pointer-events-none absolute inset-x-0 bottom-0 h-[calc(44px+max(14px,calc(env(safe-area-inset-bottom)+6px)))] bg-[linear-gradient(to_bottom,transparent,var(--paper)_14px)]" />}
        <div className="relative mx-auto max-w-[760px]">
          {!s.subagent && !elsewhere && <ShellRuns s={s} />}
          <Queue session={id} queue={s.queue ?? []} steered={(q) => onSent({ key: crypto.randomUUID(), text: q.text, files: [], from: 0, input: q.id, steer: true })} />
          <Asks session={id} asks={s.asks} cwd={cwd} confirms={confirms.filter((c) => c.session === id)} />
          {s.subagent ? (
            <div className="glass float rest flex h-12 items-center justify-center gap-1 rounded-[20px] text-sm text-ink-3">
              {t("chat.subagent")}
              {s.parent && (
                <button type="button" className="cursor-pointer text-ink-2 underline-offset-2 hover:underline" onClick={() => openChat(s.parent!)}>
                  {t("chat.subagentParent")}
                </button>
              )}
            </div>
          ) : elsewhere ? (
            <div className="glass float rest flex h-12 items-center justify-center rounded-[20px] text-sm text-ink-3">{t("chat.elsewhere")}</div>
          ) : (
            <Under s={s} ghost={ghost} composer={composer} onSent={onSent} onSentDone={(x) => ((log.sent = log.sent.filter((y) => y !== x || !x.queued)), changed())} modelOpen={modelOpen} setModelOpen={setModelOpen} modeOpen={modeOpen} setModeOpen={setModeOpen} usageOpen={usageOpen} setUsageOpen={setUsageOpen} />
          )}
        </div>
      </div>
    </div>
  );
}

/** The composer, with in its foot the approval-mode key (back, its name alone, the user,
 *  2026-11-08), the model key, the quota bars, and at the right the context ring, then send. */
function Under({ s, ghost, composer, onSent, onSentDone, modelOpen, setModelOpen, modeOpen, setModeOpen, usageOpen, setUsageOpen }: { s: LiveSession; ghost: boolean; composer: React.RefObject<ComposerHandle | null>; onSent: (x: Sent) => void; onSentDone: (x: Sent) => void; modelOpen: boolean; setModelOpen: (o: boolean) => void; modeOpen: boolean; setModeOpen: (o: boolean) => void; usageOpen: boolean; setUsageOpen: (o: boolean) => void }) {
  const mode = useMode(s);
  const agentInfo = useAgent(s.agent);
  const [ctxOpen, setCtxOpen] = useState(false);
  return (
    <>
      <Composer
        ref={composer}
        s={s}
        turn={turning(s)}
        onSent={onSent}
        onSentDone={onSentDone}
        cycleMode={mode.cycle}
        openModel={() => setModelOpen(true)}
        openMode={() => setModeOpen(true)}
        openUsage={() => setUsageOpen(true)}
        openContext={() => !!s.context?.max && (setCtxOpen(true), true)}
        cleared={openChat}
        tools={
          <>
            {/* Not guessed from the row: the mode and the model come with the state, gliding in. */}
            {!ghost && <ModeKey mode={mode} open={modeOpen} onOpenChange={setModeOpen} />}
            {!ghost && <ModelKey s={s} open={modelOpen} onOpenChange={setModelOpen} />}
            {/* dsh: its preset, then its balance where the bars would be (the user, 2026-11-08). */}
            {agentInfo?.presets ? <PresetKey s={s} /> : null}
            <QuotaBars agent={s.agent} machine={s.machine} live={s.live} using={[s.model, s.pick, modelView(s).name]} open={usageOpen} onOpenChange={setUsageOpen} />
          </>
        }
        end={<ContextRing session={s.id} summary={s.context} tokens={s.tokens} open={ctxOpen} onOpenChange={setCtxOpen} />}
      />
    </>
  );
}

/** The top bar: the logo with its lamp, the name (its folder only as its tooltip, the user,
 *  2026-11-08); more. The link's dot is the bar's own, left of ⋯ (link.tsx). Under them, while
 *  there are any, the places it works in (spots, Where). */
function Bar({ s, onSheet, onMe, spots, asked }: { s?: Session; onSheet?: () => void; onMe?: () => void; spots?: Spot[]; asked?: { session?: string }[] }) {
  const ws = useWorkspaces().data ?? [];
  const machines = useMachines();
  const [renaming, setRenaming] = useState(false);
  // ⋯'s "new session": the "+" of the session's workspace, its folder already this one's, under ⋯.
  const [making, setMaking] = useState(false);
  // Not there as the bar was first drawn: faded in as it comes.
  const late = useRef(!s).current ? "g-fade" : "";
  const more = useRef<HTMLButtonElement>(null);
  // The usage under ⋯, its right edge on ⋯'s, as the menu was (the user, 2026-11-08).
  const usageHere = () => {
    const b = more.current?.getBoundingClientRect();
    openUsage(b && { x: b.right, y: b.bottom + 6, end: true });
  };
  // ⋯'s items: making, the sheet and 我 on a phone, usage; then the session's own.
  const lead = [{ icon: <Plus size={15} />, label: t("menu.new"), run: () => requestAnimationFrame(() => setMaking(true)) }, ...(onSheet ? [{ icon: <ListTree size={15} />, label: t("nav.sidebar"), run: onSheet }] : []), ...(onMe ? [{ icon: <Settings size={15} />, label: t("nav.me"), run: onMe }] : []), { icon: <BarChart3 size={15} />, label: t("usage.title"), run: usageHere }];
  const acts = s ? sessionActions(s, () => setRenaming(true)) : [];
  return (
    <header className="g-turn-stay vt-bar glass absolute inset-x-0 top-0 z-20 shadow-[0_0.5px_0_var(--edge)]">
      <div className="flex h-12 items-center gap-1 pr-2 pl-2 pointer-coarse:h-13">
        <div className="flex min-w-0 flex-1 items-center gap-2.5 pl-2">
          {s && (
            <>
              <Tip label={`${agentName(s.agent)} · ${sessionStatusText(s)}`} side="bottom">
                {/* On a phone the logo is a key too: the session list's sheet, as ⋯'s first item (the
                    user, 2026-11-06). Its tap area wider than it, the bar not moved. */}
                {onSheet ? (
                  <button type="button" aria-label={t("nav.sidebar")} onClick={onSheet} className={cn("g-press -m-2 grid place-items-center rounded-md p-2 hover:bg-fill", late)}>
                    {/* Iridium's mark, not the agent's (the user, 2026-11-06): it opens the list. */}
                    <LogoLamp agent={s.agent} status={lampOf(s)} size={18} ir />
                  </button>
                ) : (
                  <span className={cn("grid", late)}>
                    <LogoLamp agent={s.agent} status={lampOf(s)} size={18} />
                  </span>
                )}
              </Tip>
              {renaming ? (
                <span className="min-w-0 flex-1">
                  <Rename s={s} done={() => setRenaming(false)} className="h-7 font-semibold" />
                </span>
              ) : (
                <h1 className={cn("m-0 min-w-0 truncate text-base font-semibold", late, untitled(s) && "text-ink-3")} title={s.machine ? shownPath(groupPath(s), machines).real : tilde(s.cwd)} onDoubleClick={() => setRenaming(true)}>
                  {titleOf(s)}
                </h1>
              )}
            </>
          )}
        </div>
        {/* With the sidebar shown, the link's dot is at its top (sidebar.tsx). */}
        {onSheet && <LinkDot />}
        {/* ⋯'s place kept while the session comes: the dot beside it does not move as it does. */}
        {!s && <span aria-hidden className="size-8 shrink-0 pointer-coarse:size-10" />}
        {s && (
          <span className="relative grid">
            {/* Anchored on ⋯ itself, under it: clicks go through to ⋯. */}
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
                  <MenuItems lead={lead} items={acts} tags={sessionTags(s)} />
                </Menu.Positioner>
              </Menu.Portal>
            </Menu.Root>
          </span>
        )}
      </div>
      {!!spots?.length && <Where spots={spots} asked={asked ?? []} />}
    </header>
  );
}

/** A chip's lamp for the sessions sent there: amber while one needs you, else green while one
 *  works, else none (as a launcher folded in the list shows them). */
function lampFor(ss: Session[], asked: { session?: string }[]): Status {
  if (ss.some((s) => lampOf(s) === "waiting" || asked.some((c) => c.session === s.id))) return "waiting";
  return ss.some((s) => ["running", "compacting", "starting"].includes(lampOf(s))) ? "running" : "idle";
}

/** 在哪干活 (plan, 2026-11-10): a row of chips under the title, in line with it, scrolled sideways
 *  when they do not fit (on a phone, from the bar's edge). Its own folder; the places it sent
 *  sessions to (another machine's with its face and name), how many, their lamp, pressed the first
 *  of them; those it named and sent none to, faint, dashed. Its own and those named, pressed, show
 *  in the list (places.ts openPlace). */
function Where({ spots, asked }: { spots: Spot[]; asked: { session?: string }[] }) {
  const ms = useMachines();
  return (
    <div role="list" aria-label={t("chat.where")} className="g-fade flex h-8 min-w-0 items-start gap-1.5 overflow-x-auto pr-3 pl-9 [scrollbar-width:none] [&::-webkit-scrollbar]:hidden">
      {spots.map((x) => {
        const shown = shownPath(x.place.machine ? `${x.place.machine}:${x.place.path}` : x.place.path, ms);
        const m = shown.m;
        const where = m ? `${m.name} · ${shown.real}` : tilde(shown.real);
        const n = x.sessions.length;
        const lamp = n ? lampFor(x.sessions, asked) : "idle";
        const label = x.kind === "named" ? `${where} · ${t("chat.whereNamed")}` : n ? `${where} · ${t("nav.hungCount", { n })}` : where;
        return (
          <Tip key={`${x.kind}:${x.key}`} label={label} side="bottom">
            <button
              type="button"
              role="listitem"
              onClick={() => (x.kind === "sent" ? openChat(x.sessions[0]!.id) : openPlace(x.place))}
              className={cn("g-press flex h-6 shrink-0 items-center gap-1.5 rounded-full px-2 text-xs transition-colors duration-(--dur-2)", x.kind === "named" ? "border border-dashed border-edge text-ink-3 hover:text-ink-2" : "bg-fill text-ink-2 hover:bg-fill-2 hover:text-ink")}
            >
              {m && x.kind !== "named" ? <MachineFace m={m} size={14} className={cn(!m.online && "opacity-50 grayscale")} /> : <PlaceIcon remote={!!x.place.machine} label={m?.name} size={13} />}
              <span className="max-w-48 truncate">{m ? `${m.name} · ${x.name}` : x.name}</span>
              {n > 0 && <span className="text-ink-3 tabular-nums">{n}</span>}
              {lamp !== "idle" && <Lamp status={lamp} />}
            </button>
          </Tip>
        );
      })}
    </div>
  );
}


/** A call being written that only names the session (ir rename): left out, as its block is. */
const renamesDraft = (d: Drafted) => !!d.tool && renameOnly({ name: d.tool.name, input: d.text });

/** The turn's row at the end of the conversation: the call being written, else the one running
 *  (the newest, how many more beside it), else the dots once it has gone quiet; one line high, even
 *  empty, while the turn lasts. */
function Activity({ calls, running, cwd, quiet, grew }: { calls: Drafted[]; running: Tool[]; cwd: string; quiet: boolean; grew: string }) {
  const writing = calls.at(-1);
  const run = running.at(-1);
  return (
    <div className="-mt-3 flex h-7 min-w-0 items-center pointer-coarse:h-9">
      {writing?.tool ? (
        <div key={writing.tool.id} className="min-w-0 flex-1">
          <ToolDraft name={writing.tool.name} input={writing.text} whole={writing.whole} />
        </div>
      ) : run ? (
        <div key={run.id ?? run.at} className="g-fade flex min-w-0 flex-1 items-center gap-2">
          <div className="min-w-0 flex-1">
            <Running tool={run} cwd={cwd} />
          </div>
          {running.length > 1 && <span className="shrink-0 text-xs text-ink-4 tabular-nums">+{running.length - 1}</span>}
        </div>
      ) : (
        <Waiting on={quiet} quiet={grew} />
      )}
    </div>
  );
}
