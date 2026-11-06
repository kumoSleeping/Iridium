// A session's chat (plan "网页", web/sample): a frosted top bar, the conversation by source, newest at
// the bottom and paged back as you scroll up, the agent's words as it writes them, and the bottom
// stack: the queue, the confirmation cards, the composer, the mode and model keys. Its blocks come
// from the transcript (GET messages, after= what the open session's state says was written); its
// drafts from the stream. Loaded only once a chat opens or the page is idle.
import { Menu } from "@base-ui/react/menu";
import { ChevronRight, Ellipsis, FolderInput } from "lucide-react";
import { useEffect, useLayoutEffect, useMemo, useRef, useState, type CSSProperties, type DragEvent } from "react";
import type { SessionState as LiveSession } from "../api/types.gen";
import { api } from "../api/client";
import type { Session } from "../api/types.gen";
import { chatOf, stateAhead, type Draft as Drafted, type Sent } from "../chats";
import { LOCAL_ME, useMe, useTerminalList, useWorkspaces } from "../data";
import { t, useLang } from "../i18n";
import { pushIn } from "../motion";
import { openChat } from "../route";
import { MenuItems, Rename, sessionActions } from "../sidebar";
import { useConfirms, useList, useSession } from "../store";
import { drafts, opened } from "../stream";
import { errText, toast } from "../toast";
import { agentName, cn, LogoLamp, statusText, tilde, Tip, titleOf } from "../ui";
import { Asks } from "./asks";
import { Composer, type ComposerHandle } from "./composer";
import { folded, type Tool } from "./fold";
import { ToolDraft, Waiting } from "./live";
import { Draft } from "./markdown";
import { ContextRing, ModeKey, ModelKey, modelView, QuotaBars, useMode } from "./meters";
import { Queue } from "./queue";
import { SESSION_DRAG, shortId, sourceOf, yours } from "./refs";
import { Pending, Running, Said } from "./said";
import { Stick, toEnd } from "./stick";

const BUSY = new Set(["running", "compacting", "starting", "waiting"]);

/** The bottom stack's height as last measured: a chat opens padded by it, not by a guess and then
 *  moved (one frame drawn short or long, the conversation jumping after). */
let lastStackH = 140;

/** A session's state before the stream (or chats.ts stateAhead) brings it, from its row: the chat is
 *  drawn whole at once, its composer and conversation, its keys after (ghost). */
const ghostOf = (row: Session): LiveSession => ({ ...row, asks: [], tasks: [], queue: [], end: 0, proc: -1 });

/** slot: the bar keeps its left free for the sidebar's key (drawer.tsx SideKey), the sidebar not
 *  beside it. */
export function ChatPage({ id, phone, slot }: { id: string; phone: boolean; slot: boolean }) {
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
  const terminals = useTerminalList();
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
  const busy = !!s && BUSY.has(s.status);
  const wasBusy = useRef(busy);
  useEffect(() => {
    if (wasBusy.current && !busy) {
      const upTo = log.mark();
      void log.newer().then(() => (log.dropDrafts(upTo), changed()), () => {});
    }
    wasBusy.current = busy;
  }, [busy]);

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
  const [modelOpen, setModelOpen] = useState(false);
  const [modeOpen, setModeOpen] = useState(false);
  const [usageOpen, setUsageOpen] = useState(false);
  const shown = folded(log.blocks);
  const cwd = s?.cwd ?? row?.cwd ?? "";

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
  // with ir (the user, 2026-11-06), as short as says what it is: its title, agent and id.
  const session = (e: DragEvent) => e.dataTransfer.types.includes(SESSION_DRAG) && (e.preventDefault(), (e.dataTransfer.dropEffect = "copy"), true);
  const drop = (e: DragEvent) => {
    if (files(e)) return composer.current?.attach([...e.dataTransfer.files]);
    if (!session(e)) return;
    delete document.documentElement.dataset.drag;
    const to = list?.find((x) => x.id === e.dataTransfer.getData(SESSION_DRAG));
    if (!to || to.id === id) return;
    composer.current?.insert(t("composer.mention", { title: titleOf(to) || tilde(to.cwd), agent: agentName(to.agent), id: shortId(to.id, list!.map((x) => x.id)) }));
  };

  if (!s) {
    const gone = lost && list !== undefined && !row;
    return (
      <div className="relative h-full">
        <Bar phone={phone} s={row} slot={slot} />
        {gone && <p className="g-fade m-0 pt-24 text-center text-sm text-ink-3">{t("chat.gone")}</p>}
      </div>
    );
  }
  const elsewhere = s.status === "elsewhere";
  // What changes as the conversation grows: it keeps to the bottom, the dots wait again.
  const grew = `${log.blocks.at(-1)?.at} ${log.drafts.length} ${log.drafts.at(-1)?.text.length} ${log.drafts.at(-1)?.whole} ${log.sent.length}`;
  return (
    <div className="relative h-full" onDragOver={(e) => files(e) || session(e)} onDrop={drop}>
      <Stick scroller={scroller} tail={grew} older={() => log.older()} place={chat.place} bottom={stackH} style={{ paddingTop: 64, paddingBottom: stackH + 20 }}>
        <div className="mx-auto flex max-w-[760px] flex-col gap-5 px-4 sm:px-6">
          {log.loaded && !log.blocks.length && !log.drafts.length && !log.sent.length && <p className="g-fade m-0 pt-16 text-center text-sm text-ink-4">{t("chat.empty")}</p>}
          {/* In a turn, the calls being written or run and the dots share one row at the end, as
              high whatever it shows: the conversation does not jump as a call is drafted, runs and
              folds into the line above (the user, 2026-11-06). */}
          {(busy ? shown.filter((b) => b.kind !== "running") : shown).map((b) => (
            <Said key={b.at} b={b} session={id} cwd={cwd} source={source} onJump={openChat} />
          ))}
          {log.drafts
            .filter((d) => !d.tool || !busy)
            .map((d) =>
              d.tool ? (
                <ToolDraft key={`${d.proc}.${d.n}`} name={d.tool.name} input={d.text} whole />
              ) : (
                <div key={`${d.proc}.${d.n}`} className="g-fade min-w-0 pr-6">
                  <Draft text={d.text} live={!d.whole && busy} />
                </div>
              ),
            )}
          {log.sent.map((x) => (
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
          {log.loaded && (busy ? <Activity calls={log.drafts.filter((d) => d.tool)} running={shown.flatMap((b) => (b.kind === "running" ? [b.tool] : []))} cwd={cwd} quiet={s.status !== "waiting" && !log.drafts.some((d) => !d.whole && !d.tool)} grew={grew} /> : <div aria-hidden className="-mt-3 h-7 pointer-coarse:h-9" />)}
          {s.status === "error" && s.error && (
            <p className="m-0 text-center text-xs text-err" title={s.error}>
              {t("chat.crashed")} · <span className="font-mono">{s.error.slice(0, 160)}</span>
            </p>
          )}
        </div>
      </Stick>
      <Bar phone={phone} s={s} slot={slot} />
      <div ref={stack} className="g-turn-stay vt-stack absolute inset-x-0 bottom-0 z-10 px-2.5 pb-[max(14px,calc(env(safe-area-inset-bottom)+6px))] sm:px-5">
        {/* Paper from the composer's middle down: the conversation scrolled under it stops there. */}
        {!elsewhere && !s.subagent && <div aria-hidden className="pointer-events-none absolute inset-x-0 bottom-0 h-[calc(44px+max(14px,calc(env(safe-area-inset-bottom)+6px)))] bg-[linear-gradient(to_bottom,transparent,var(--paper)_14px)]" />}
        <div className="relative mx-auto max-w-[760px]">
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

/** The composer, with in its foot the approval-mode key, the model key, the quota bars, and at the
 *  right the context ring, then send (the user, 2026-11-06). */
function Under({ s, ghost, composer, onSent, onSentDone, modelOpen, setModelOpen, modeOpen, setModeOpen, usageOpen, setUsageOpen }: { s: LiveSession; ghost: boolean; composer: React.RefObject<ComposerHandle | null>; onSent: (x: Sent) => void; onSentDone: (x: Sent) => void; modelOpen: boolean; setModelOpen: (o: boolean) => void; modeOpen: boolean; setModeOpen: (o: boolean) => void; usageOpen: boolean; setUsageOpen: (o: boolean) => void }) {
  const mode = useMode(s);
  return (
    <>
      <Composer
        ref={composer}
        s={s}
        turn={BUSY.has(s.status)}
        onSent={onSent}
        onSentDone={onSentDone}
        cycleMode={mode.cycle}
        openModel={() => setModelOpen(true)}
        openMode={() => setModeOpen(true)}
        openUsage={() => setUsageOpen(true)}
        tools={
          <>
            {/* Not guessed from the row: the mode and the model come with the state, gliding in. */}
            {!ghost && <ModeKey mode={mode} open={modeOpen} onOpenChange={setModeOpen} />}
            {!ghost && <ModelKey s={s} open={modelOpen} onOpenChange={setModelOpen} />}
            <QuotaBars agent={s.agent} live={s.live} using={[s.model, s.pick, modelView(s).name]} open={usageOpen} onOpenChange={setUsageOpen} />
          </>
        }
        end={<ContextRing session={s.id} summary={s.context} />}
      />
    </>
  );
}

/** The top bar: the logo with its lamp, the name, the folder; more. The link's dot is the
 *  sidebar's (sidebar.tsx LinkDot). */
function Bar({ s, phone, slot }: { s?: Session; phone: boolean; slot: boolean }) {
  const ws = useWorkspaces().data ?? [];
  const [renaming, setRenaming] = useState(false);
  return (
    <header className="g-turn-stay vt-bar glass absolute inset-x-0 top-0 z-20 flex h-12 items-center gap-1 pr-2 pl-2 shadow-[0_0.5px_0_var(--edge)] pointer-coarse:h-13">
      <div className={cn("flex min-w-0 flex-1 items-center gap-2.5", slot ? "pl-10 pointer-coarse:pl-12" : "pl-2")}>
        {s && (
          <>
            <Tip label={`${agentName(s.agent)} · ${statusText(s.status)}`} side="bottom">
              <span className="grid">
                <LogoLamp agent={s.agent} status={s.status} size={18} />
              </span>
            </Tip>
            {renaming ? (
              <span className="min-w-0 flex-1">
                <Rename s={s} done={() => setRenaming(false)} className="h-7 font-semibold" />
              </span>
            ) : (
              <h1 className="m-0 min-w-0 truncate text-base font-semibold" onDoubleClick={() => setRenaming(true)}>
                {titleOf(s)}
              </h1>
            )}
            {/* The folder whole, the name giving way first; a long one cut at its start, its end showing. */}
            {!phone && !renaming && (
              <span className="max-w-[40%] shrink-0 truncate font-mono text-xs text-ink-3 [direction:rtl]" title={tilde(s.cwd)}>
                <bdi>{tilde(s.cwd)}</bdi>
              </span>
            )}
          </>
        )}
      </div>
      {s && (
        <Menu.Root>
          <Tip label={t("top.more")} side="bottom">
            <Menu.Trigger
              render={
                <button type="button" aria-label={t("top.more")} className="g-press grid size-8 place-items-center rounded-md text-ink-2 hover:bg-fill hover:text-ink data-popup-open:bg-fill pointer-coarse:size-10">
                  <Ellipsis size={17} />
                </button>
              }
            />
          </Tip>
          <Menu.Portal>
            <Menu.Positioner side="bottom" align="end" sideOffset={6} collisionPadding={8} className="z-50">
              <MenuItems items={sessionActions(s, () => setRenaming(true))}>{ws.length > 1 && <MoveTo s={s} ws={ws} />}</MenuItems>
            </Menu.Positioner>
          </Menu.Portal>
        </Menu.Root>
      )}
    </header>
  );
}

/** Moving a session to another workspace: dragged ones stay where they were put (plan "会话"). */
function MoveTo({ s, ws }: { s: Session; ws: { id: number; name: string }[] }) {
  return (
    <Menu.SubmenuRoot>
      <Menu.SubmenuTrigger className="flex h-8 cursor-default items-center gap-2.5 rounded-md px-2.5 text-base outline-none select-none data-highlighted:bg-fill data-popup-open:bg-fill pointer-coarse:h-11">
        <span className="text-ink-2">
          <FolderInput size={15} />
        </span>
        <span className="flex-1">{t("menu.move")}</span>
        <ChevronRight size={14} className="text-ink-4" />
      </Menu.SubmenuTrigger>
      <Menu.Portal>
        <Menu.Positioner className="z-50" sideOffset={4}>
          <Menu.Popup className="g-unfold glass thick float min-w-44 rounded-lg p-1.5 text-ink outline-none">
            {ws.map((w, i) => (
              <Menu.Item
                key={w.id}
                disabled={w.id === s.workspace}
                style={{ "--i": i } as CSSProperties}
                onClick={() => void api.patch(s.id, { workspace: w.id }).catch((e) => toast(t("err.action"), { error: true, description: errText(e) }))}
                className="flex h-8 cursor-default items-center gap-2.5 rounded-md px-2.5 text-base outline-none select-none data-disabled:opacity-40 data-highlighted:bg-fill"
              >
                {w.name}
              </Menu.Item>
            ))}
          </Menu.Popup>
        </Menu.Positioner>
      </Menu.Portal>
    </Menu.SubmenuRoot>
  );
}

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
