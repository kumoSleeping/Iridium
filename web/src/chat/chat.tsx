// A session's chat (plan "网页", web/sample): a frosted top bar, the conversation by source, newest at
// the bottom and paged back as you scroll up, the agent's words as it writes them, and the bottom
// stack: the queue, the confirmation cards, the composer, the mode and model keys. Its blocks come
// from the transcript (GET messages, after= what the open session's state says was written); its
// drafts from the stream. Loaded only once a chat opens or the page is idle.
import { Menu } from "@base-ui/react/menu";
import { ChevronLeft, ChevronRight, Ellipsis, FolderInput } from "lucide-react";
import { useEffect, useLayoutEffect, useRef, useState, type CSSProperties, type DragEvent } from "react";
import type { SessionState as LiveSession } from "../api/types.gen";
import { api } from "../api/client";
import type { Session } from "../api/types.gen";
import { chatOf, type Sent } from "../chats";
import { useMe, useTerminalList, useWorkspaces } from "../data";
import { t, useLang } from "../i18n";
import { pushIn } from "../motion";
import { openChat } from "../route";
import { MenuItems, Rename, sessionActions } from "../sidebar";
import { useConfirms, useLink, useList, useSession } from "../store";
import { drafts, opened } from "../stream";
import { errText, toast } from "../toast";
import { agentName, cn, IconKey, LogoLamp, statusText, tilde, Tip, titleOf } from "../ui";
import { Asks } from "./asks";
import { Composer, type ComposerHandle } from "./composer";
import { folded } from "./fold";
import { Draft } from "./markdown";
import { ContextRing, ModeKey, ModelKey, QuotaBars, useMode } from "./meters";
import { Queue } from "./queue";
import { sourceOf } from "./refs";
import { Pending, Said } from "./said";
import { Stick } from "./stick";

const BUSY = new Set(["running", "compacting", "starting", "waiting"]);

export function ChatPage({ id, phone, onBack }: { id: string; phone: boolean; onBack: () => void }) {
  useLang();
  const s = useSession(id);
  const list = useList();
  const row = list?.find((x) => x.id === id);
  const [chat] = useState(() => chatOf(id));
  const log = chat.log;
  const [, redraw] = useState(0);
  const changed = () => redraw((n) => n + 1);
  const me = useMe().data;
  const terminals = useTerminalList();
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
  useEffect(() => void (s && log.procIs(s.proc), changed()), [s?.proc]);
  // The turn ended: what was drafted is in the transcript now.
  const busy = !!s && BUSY.has(s.status);
  const wasBusy = useRef(busy);
  useEffect(() => {
    if (wasBusy.current && !busy) void log.newer().then(() => (log.dropDrafts(), changed()), () => {});
    wasBusy.current = busy;
  }, [busy]);

  const source = (b: { from?: string; fromTerminal?: string }) => sourceOf(b.from, b.fromTerminal, me?.id ?? "local", terminals, list ?? []);

  // The bottom stack's height pads the conversation's end.
  const stack = useRef<HTMLDivElement>(null);
  const [stackH, setStackH] = useState(140);
  useLayoutEffect(() => {
    const el = stack.current;
    if (!el) return;
    const ro = new ResizeObserver(() => setStackH(el.offsetHeight));
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  // A message sent: at the bottom, the conversation pushed up by it as it fades in.
  const bubbles = useRef(new Map<string, HTMLDivElement>());
  const fly = useRef<string | null>(null);
  const scroller = useRef<HTMLDivElement>(null);
  const column = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => {
    const el = fly.current && bubbles.current.get(fly.current);
    if (!el) return;
    fly.current = null;
    const sc = scroller.current;
    if (sc) sc.scrollTop = sc.scrollHeight;
    if (column.current) pushIn(el, column.current);
  });

  const composer = useRef<ComposerHandle>(null);
  const [modelOpen, setModelOpen] = useState(false);
  const [modeOpen, setModeOpen] = useState(false);
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

  if (!s) {
    const gone = list !== undefined && !row;
    return (
      <div className="relative h-full">
        <Bar phone={phone} onBack={onBack} s={row} />
        {gone && <p className="g-fade m-0 pt-24 text-center text-sm text-ink-3">{t("chat.gone")}</p>}
      </div>
    );
  }
  const elsewhere = s.status === "elsewhere";
  return (
    <div className="relative h-full" onDragOver={files} onDrop={(e) => files(e) && composer.current?.attach([...e.dataTransfer.files])}>
      <Stick scroller={scroller} tail={`${log.blocks.at(-1)?.at} ${log.drafts.length} ${log.drafts.at(-1)?.text.length} ${log.sent.length}`} older={() => log.older()} place={chat.place} bottom={stackH} style={{ paddingTop: 64, paddingBottom: stackH + 16 }}>
        <div ref={column} className="mx-auto flex max-w-[760px] flex-col gap-5 px-4 sm:px-6">
          {log.loaded && !log.blocks.length && !log.drafts.length && !log.sent.length && <p className="g-fade m-0 pt-16 text-center text-sm text-ink-4">{t("chat.empty")}</p>}
          {shown.map((b) => (
            <Said key={b.at} b={b} session={id} cwd={cwd} source={source} onJump={openChat} />
          ))}
          {log.drafts.map((d) => (
            <div key={`${d.proc}.${d.n}`} className="g-fade min-w-0 pr-6">
              <Draft text={d.text} />
            </div>
          ))}
          {log.sent.map((x) => (
            <Pending
              key={x.key}
              s={x}
              bubbleRef={(el) => void (el ? bubbles.current.set(x.key, el) : bubbles.current.delete(x.key))}
              retry={() => retry(x)}
              drop={() => ((log.sent = log.sent.filter((y) => y !== x)), changed())}
            />
          ))}
          {s.status === "error" && s.error && (
            <p className="m-0 text-center text-xs text-err" title={s.error}>
              {t("chat.crashed")} · <span className="font-mono">{s.error.slice(0, 160)}</span>
            </p>
          )}
        </div>
      </Stick>
      <Bar phone={phone} onBack={onBack} s={s} />
      <div ref={stack} className="absolute inset-x-0 bottom-0 z-10 px-2.5 pb-[max(6px,env(safe-area-inset-bottom))] sm:px-5">
        {!elsewhere && !s.subagent && <div aria-hidden className="pointer-events-none absolute inset-x-0 bottom-0 h-[calc(36px+22px+max(6px,env(safe-area-inset-bottom)))] bg-[linear-gradient(to_bottom,transparent,var(--paper)_14px)]" />}
        <div className="relative mx-auto max-w-[760px]">
          <Queue session={id} queue={s.queue ?? []} steered={(q) => onSent({ key: crypto.randomUUID(), text: q.text, files: [], from: 0, input: q.id, steer: true })} />
          <Asks session={id} asks={s.asks} cwd={cwd} confirms={confirms.filter((c) => c.session === id)} />
          {s.subagent ? (
            <div className="glass float flex h-12 items-center justify-center gap-1 rounded-xl text-sm text-ink-3">
              {t("chat.subagent")}
              {s.parent && (
                <button type="button" className="cursor-pointer text-ink-2 underline-offset-2 hover:underline" onClick={() => openChat(s.parent!)}>
                  {t("chat.subagentParent")}
                </button>
              )}
            </div>
          ) : elsewhere ? (
            <div className="glass float flex h-12 items-center justify-center rounded-xl text-sm text-ink-3">{t("chat.elsewhere")}</div>
          ) : (
            <Under s={s} composer={composer} onSent={onSent} onSentDone={(x) => ((log.sent = log.sent.filter((y) => y !== x || !x.queued)), changed())} modelOpen={modelOpen} setModelOpen={setModelOpen} modeOpen={modeOpen} setModeOpen={setModeOpen} />
          )}
        </div>
      </div>
    </div>
  );
}

/** The composer and, under it, the approval-mode key, the model key, the quota bars, and at the
 *  right the context ring (back under it, user 2026-11-06). Behind the row, from the composer's middle
 *  down, paper: the conversation scrolled under the stack stops there and never runs into the row. */
function Under({ s, composer, onSent, onSentDone, modelOpen, setModelOpen, modeOpen, setModeOpen }: { s: LiveSession; composer: React.RefObject<ComposerHandle | null>; onSent: (x: Sent) => void; onSentDone: (x: Sent) => void; modelOpen: boolean; setModelOpen: (o: boolean) => void; modeOpen: boolean; setModeOpen: (o: boolean) => void }) {
  const mode = useMode(s);
  return (
    <>
      <Composer ref={composer} s={s} turn={BUSY.has(s.status)} onSent={onSent} onSentDone={onSentDone} cycleMode={mode.cycle} openModel={() => setModelOpen(true)} openMode={() => setModeOpen(true)} />
      <div className="flex h-9 min-w-0 items-center gap-0.5 px-1">
        <ModeKey mode={mode} open={modeOpen} onOpenChange={setModeOpen} />
        <ModelKey s={s} open={modelOpen} onOpenChange={setModelOpen} />
        <QuotaBars agent={s.agent} live={s.live} />
        <span className="ml-auto flex shrink-0">
          <ContextRing session={s.id} summary={s.context} />
        </span>
      </div>
    </>
  );
}

/** The top bar: back (phone), the logo with its lamp, the name, the folder; the link's dot; more. */
function Bar({ s, phone, onBack }: { s?: Session; phone: boolean; onBack: () => void }) {
  const link = useLink();
  const ws = useWorkspaces().data ?? [];
  const [renaming, setRenaming] = useState(false);
  const linkLabel = link.live === false ? t("top.offline") : link.rtt !== null ? t("top.online", { ms: link.rtt }) : t("top.connecting");
  return (
    <header className="glass absolute inset-x-0 top-0 z-20 flex h-12 items-center gap-1 pr-2 pl-2 shadow-[0_0.5px_0_var(--edge)] pointer-coarse:h-13">
      {phone && (
        <IconKey label={t("nav.back")} onClick={onBack} tipSide="bottom" className="-mr-1">
          <ChevronLeft size={20} />
        </IconKey>
      )}
      <div className={cn("flex min-w-0 flex-1 items-center gap-2.5", !phone && "pl-2")}>
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
            {!phone && !renaming && <span className="min-w-0 shrink-[2] truncate font-mono text-xs text-ink-3">{tilde(s.cwd)}</span>}
          </>
        )}
      </div>
      <Tip label={linkLabel} side="bottom">
        <span className="mx-1.5 flex items-center gap-1.5 text-xs text-ink-3 tabular-nums" role="status" aria-label={linkLabel}>
          <span className={cn("size-1.5 rounded-full", link.live === false ? "bg-err" : link.live ? "bg-ink-3" : "bg-ink-4")} />
          {!phone && (link.live === false ? t("top.offlineShort") : link.rtt !== null ? `${link.rtt} ms` : "")}
        </span>
      </Tip>
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
