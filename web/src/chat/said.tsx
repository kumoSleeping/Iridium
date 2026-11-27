// The blocks of a chat by source (plan "消息来源", web/sample), QQ-like: each of the user's side with
// its sender's face, name and badge. Your words (from any of your panels or terminals) a bubble on
// the right, the terminal's avatar and its device; a letter on the right too (the user, 2026-11-06),
// for what another session's ir (its agent's logo, a bot badge, a key to go there), a plugin or a
// peer sent; Iridium's own lines
// centred and faint; the agent's words as Markdown with the files they name; tool calls and
// thoughts folded to a line.
import { LongWords } from "./longwords";
import { Forwards } from "./forwardcard";
import { splitForwards } from "./forward";
import { FOLD_AT, linesOf } from "./channel/folds";
import { Archive, ArchiveRestore, ArrowUpRight, BookOpen, Bot, BotMessageSquare, ChartColumn, ChevronRight, CircleStop, CircleX, Clock, CloudDownload, CloudUpload, Cpu, Earth, FileCode, FileDiff, FilePen, Files as FilesIcon, FileText, FlaskConical, FolderGit2, FolderOpen, Folders, FoldVertical, GitBranch, GitCommitHorizontal, GitPullRequest, Globe, Hammer, History, Hourglass, KeyRound, Laptop, Link2, List, ListChecks, LoaderCircle, LockKeyhole, MessageCircleQuestionMark, MessageSquarePlus, Monitor, MonitorSmartphone, MousePointerClick, Network, Package, PencilLine, Plug, RotateCcw, RotateCw, Search, Send, ServerCog, SlidersHorizontal, Smartphone, SquareTerminal, Terminal, Trash2, type LucideIcon, UserRound, Workflow as WorkflowIcon, Wrench, X } from "lucide-react";
import { Copyable } from "./copyable";
import { Dispatch, Sent as SentTo, TaskBack } from "./dispatched";
import { handBack, toldOf } from "./dispatch";
import { Children, forwardRef, memo, useContext, useEffect, useLayoutEffect, useMemo, useRef, useState, type CSSProperties, type ReactNode } from "react";
import { givenTurn, linkNicks } from "./channel/lib";
import { AfterContext, ChannelContext } from "./context";
import { parseGiven, type GivenRec } from "./given";
import { reactionLine } from "./react";
import { Nicks, NickWords, SeqRefs } from "./channel/nick";
import { Face as ChannelFace } from "./channel/face";
import { CARD_POP } from "./channel/profilecard";
import { Popover } from "@base-ui/react/popover";
import { api } from "../api/client";
import type { Block, Device, FileRef, Result } from "../api/types.gen";
import type { Sent } from "../chats";
import { errText, toast } from "../toast";
import { t } from "../i18n";
import { agentName, Avatar, Badge, cn, IconKey, Logo, own, PopSurface, tilde, Tip, titleOf, useEver, useLongPress, useMenuAt, PopoverPortal } from "../ui";
import { useHome, useMachines } from "../data";
import { LocalMark } from "../me";
import { go } from "../route";
import { Files, isLink, Picture, previews, SentFile, shownAs } from "./files";
import { Pictures } from "./pictures";
import { cmdOfCall, cpOfCall, isShell, newOfCall, type Cmd, type CpSide } from "./cmd";
import { argOf, bgLaunch, diffSum, foldLine, foldSaid, foldSpan, inputOf, kindOf, runCmds, unifiedDiff, type Diff, type Kind, type Shown, type Step, type Tool } from "./fold";
import { Code } from "./hl";
import { Markdown, Streamed } from "./markdown";
import { partialInput } from "./partial";
import { machineNamed, MentionCard, NamedCards, Worded } from "./mention";
import { draftWords, fromSatori, type Mention, sessionOf, type Source, withoutFiles, withoutNameLines } from "./refs";
import { MachineFace } from "../machines";
import { useList } from "../store";
import type { Agent } from "../api/types.gen";

type You = Extract<Source, { kind: "you" }>;
type From = Extract<Source, { kind: "letter" }>;

/** One formatter per language: making one per call is slow, and a chat calls it for every block. */
const clocks = new Map<string, Intl.DateTimeFormat>();
export const clock = (ms?: number) => {
  if (!ms) return "";
  const l = document.documentElement.lang === "en" ? "en" : "zh-CN";
  let f = clocks.get(l);
  if (!f) clocks.set(l, (f = new Intl.DateTimeFormat(l, { hour: "2-digit", minute: "2-digit" })));
  return f.format(ms);
};

/** The day of ms where it is not today (StarFreedomX, 2026-11-27: a time alone, no day): 11月25日,
 *  Nov 25; the year too where it is not this one. short: 11/25, for a face's narrow place. */
const days = new Map<string, Intl.DateTimeFormat>();
export const dayOf = (ms?: number, short?: boolean) => {
  if (!ms) return "";
  const d = new Date(ms);
  const now = new Date();
  if (d.toDateString() === now.toDateString()) return "";
  const l = document.documentElement.lang === "en" ? "en" : "zh-CN";
  const other = d.getFullYear() !== now.getFullYear();
  const key = `${l} ${other} ${!!short}`;
  let f = days.get(key);
  const month = short ? "numeric" : "short";
  if (!f) days.set(key, (f = new Intl.DateTimeFormat(l, other ? { year: short ? "2-digit" : "numeric", month, day: "numeric" } : { month, day: "numeric" })));
  return f.format(ms);
};

/** When a message was said, on its head's inner side (beside the name, toward the middle; kumo,
 *  2026-11-16), its number in the channel before it (n; kumo, 2026-11-26): not shown till a
 *  pointer is over its row (WhenRow), or a finger presses the row's blank (where there is no
 *  hover), which shows it till pressed again: a line of a run, its own alone (pressWhen). Its place is kept: showing it moves nothing. lead: the head of a run of several
 *  messages (an agent's): shown over the head or the first message alone ([data-lead]), each
 *  other one with its own (Gutter). */
export const When = ({ time, n, lead }: { time?: number; n?: number; lead?: boolean }) =>
  time ? (
    <span className={cn("shrink-0 text-xs leading-4 text-ink-4 tabular-nums opacity-0 transition-opacity duration-(--dur-2) group-data-[when]/msg:opacity-100", lead ? "group-has-[[data-lead]:hover]/msg:opacity-100 group-has-[[data-lead][data-when]]/msg:opacity-100" : "group-hover/msg:opacity-100")}>
      {[n !== undefined && `#${n}`, dayOf(time), clock(time)].filter(Boolean).join(" ")}
    </span>
  ) : null;

/** The time and number of a message with no head of its own (one under another's head: kumo,
 *  2026-11-26, a pointer over it showed none, or the first's), in the empty place of the face
 *  beside it, as Slack does: shown over the message itself (group/line), or as When is by a
 *  press. Put in an element that is relative; right: the face's place on the right. slot: put in
 *  that place itself (a sender's row of its own, group/msg), not beside it, kept to it: what is
 *  wider than it goes toward the bubble, never out past the face's edge (kumo, 2026-11-27). */
export const Gutter = ({ time, n, right, slot }: { time?: number; n?: number; right?: boolean; slot?: boolean }) =>
  time || n !== undefined ? (
    <span aria-hidden className={cn("pointer-events-none absolute top-0.5 flex w-max min-w-7 flex-col text-[10px] leading-[13px] whitespace-nowrap text-ink-4 tabular-nums opacity-0 transition-opacity duration-(--dur-2)", slot ? "group-hover/msg:opacity-100 group-data-[when]/msg:opacity-100" : "group-hover/line:opacity-100 group-data-[when]/line:opacity-100", slot ? (right ? "right-0 items-end" : "left-0 items-start") : right ? "left-full ml-2.5 items-start" : "right-full mr-2.5 items-end")}>
      {/* As the head's, its number first (kumo, 2026-11-26: the time over it read the other way). */}
      {n !== undefined && <span>#{n}</span>}
      {time && dayOf(time, true) ? <span>{dayOf(time, true)}</span> : null}
      {time ? <span>{clock(time)}</span> : null}
    </span>
  ) : null;

/** Whether a press at e is on a message's blank: not on its words, a key, a link, a picture, code
 *  or a bubble, and no words being selected. */
function onBlank(e: React.MouseEvent): boolean {
  const el = e.target as Element;
  if (el.closest("button,a,input,textarea,select,label,summary,img,svg,video,audio,pre,code,[role=button],[data-bubble-bg]")) return false;
  if (getSelection()?.toString()) return false;
  // A word under the point: its own box, not the block it is in, which is as wide as the row.
  const d = document as Document & { caretPositionFromPoint?: (x: number, y: number) => { offsetNode: Node; offset: number } | null };
  const at = d.caretPositionFromPoint?.(e.clientX, e.clientY) ?? (() => {
    const r = document.caretRangeFromPoint?.(e.clientX, e.clientY);
    return r ? { offsetNode: r.startContainer, offset: r.startOffset } : null;
  })();
  const node = at?.offsetNode;
  if (node?.nodeType === Node.TEXT_NODE) {
    const len = node.textContent?.length ?? 0;
    const r = document.createRange();
    for (const o of [at!.offset - 1, at!.offset]) {
      if (o < 0 || o >= len) continue;
      r.setStart(node, o);
      r.setEnd(node, o + 1);
      for (const b of r.getClientRects()) if (e.clientX >= b.left && e.clientX <= b.right && e.clientY >= b.top && e.clientY <= b.bottom) return false;
    }
  }
  return true;
}

/** A message's row, where its time (When) shows: over it with a pointer; pressed on its blank,
 *  till pressed there again (a phone has no hover). Spread on the row's element. */
export function useWhenRow() {
  const [on, setOn] = useState(false);
  return {
    className: "group/msg",
    "data-when": on ? "" : undefined,
    onClick: (e: React.MouseEvent) => {
      if (!onBlank(e)) return;
      const at = pressWhen(e.target as Element, noHover());
      if (at === "row") setOn((x) => !x);
    },
  };
}

/** No pointer to hover with (a finger): a press shows the times. With one (kumo, 2026-11-26: a
 *  click on a blank left a whole run's lit till clicked again), a pointer over them does alone. */
const noHover = () => matchMedia("(hover: none)").matches;

/** A press on a row's blank: nothing where there is hover; else the line pressed (a message of a
 *  run, group/line) shows its own time and number alone, toggled on it ("line"); else the row's
 *  ("row"). */
export function pressWhen(target: Element, touch: boolean): "none" | "line" | "row" {
  if (!touch) return "none";
  const line = target.closest(".group\\/line");
  if (!line) return "row";
  line.toggleAttribute("data-when");
  return "line";
}

/** A message's row as an element of its own (useWhenRow), where a hook is not to hand. */
export function MsgRow({ className, children }: { className?: string; children: ReactNode }) {
  const row = useWhenRow();
  return (
    <div {...row} data-row="" className={cn(row.className, className)}>
      {children}
    </div>
  );
}

const FACE = 28;
const BADGE = 12;
const DEVICES: Record<Exclude<You["device"], "none">, [LucideIcon, Parameters<typeof t>[0]]> = { computer: [Monitor, "device.computer"], laptop: [Laptop, "device.laptop"], phone: [Smartphone, "device.phone"] };
/** A sender's device mark and its name; none for a terminal that shows none. */
const deviceOf = (who: You) => (who.device === "none" ? undefined : (DEVICES[who.device] ?? DEVICES.computer));
const youName = (who: You) => (who.local ? t("me.local") : who.name);

/** A sender's face: a terminal's avatar (本机 its mark) with its device on the corner; a session's
 *  agent logo alone, no disc, no badge (the user, 2026-11-06); a plugin's or peer's avatar with its
 *  plug. */
function Face({ who }: { who: You | From }) {
  if (who.kind === "you") {
    const d = deviceOf(who);
    if (!who.local) return <Avatar src={who.avatar} name={who.name} size={FACE} badge={d?.[0]} badgeSize={BADGE} badgeLabel={d && t(d[1])} />;
    return (
      <span className="relative inline-block shrink-0" style={{ width: FACE, height: FACE }}>
        <LocalMark size={FACE} />
        {d && <Badge icon={d[0]} size={BADGE} label={t(d[1])} />}
      </span>
    );
  }
  const badge = who.workflow ? <Badge icon={WorkflowIcon} size={BADGE} label={t("msg.workflow")} /> : <Badge icon={Bot} size={BADGE} label={t("msg.agent")} />;
  if (who.agent) {
    const faceId = who.face ?? who.session;
    return (
      <span className="grid shrink-0 place-items-center rounded-full bg-fill" style={{ width: FACE, height: FACE }} aria-label={t(who.workflow ? "msg.workflow" : "msg.agent")}>
        {faceId ? <ChannelFace id={faceId} agent={who.agent} size={FACE} logo={16} /> : <Logo agent={who.agent} size={16} />}
      </span>
    );
  }
  if (!who.channel) {
    // A session not in the list: try its custom face if its id is known, else a terminal-like mark and the bot.
    const fallback = (
      <span className="relative grid shrink-0 place-items-center rounded-full bg-fill-2 text-ink-2" style={{ width: FACE, height: FACE }}>
        <SquareTerminal size={14} strokeWidth={2} aria-hidden />
        {badge}
      </span>
    );
    const faceId = who.face ?? who.session;
    return faceId ? <ChannelFace id={faceId} agent={"claude" as Agent} size={FACE} none={fallback} /> : fallback;
  }
  return <Avatar src={who.avatar} name={who.name} size={FACE} type={who.type} badgeSize={BADGE} />;
}

/** One message of the user's side, QQ-like: the face beside it, the name above, yours on the right.
 *  jump: a key to the sending session, on the face and the name. Your own face pops your card
 *  (card), the one others see (kumo, 2026-11-16; before, the settings). */
/** left: another's words in a channel others speak in too, on the left: only yours are on the
 *  right there (the user, 2026-11-13). */
/** card: what its face opens, pressed (a person's card in a channel, chat/channel/person.tsx). */
/** time: when it was said, after the name on the line the name already has. menu: what the
 *  face's right button (a finger held down) opens, at that point. bare: under the head of the one before it
 *  from the same sender (docs/next/ui.md §1.5): no face nor name, their room kept. */
type FaceMenu = (at: { x: number; y: number } | null, close: () => void) => ReactNode;
const Sender = forwardRef<HTMLDivElement, { who: You | From; time?: number; n?: number; jump?: () => void; left?: boolean; card?: ReactNode; menu?: FaceMenu; under?: ReactNode; bare?: boolean; children: ReactNode }>(function Sender({ who, time, n, jump, left, card, menu, under, bare, children }, ref) {
  const [menuAt, setMenuAt] = useMenuAt();
  const menuEver = useEver(menuAt !== null);
  // The face's card, made as it is first opened (useEver).
  const [carded, setCarded] = useState(false);
  const cardEver = useEver(carded);
  // Whether it was open as the press on its face began: that press, outside it, has shut it by
  // the time the click comes, and the click is not to open it again.
  const cardWas = useRef<boolean | null>(null);
  const faceKey = useRef<HTMLButtonElement>(null);
  const lp = useLongPress(() => {
    const r = faceKey.current?.getBoundingClientRect();
    setMenuAt(r ? { x: r.left, y: r.bottom + 4 } : { x: 40, y: 80 });
  });
  const menuKeys = menu
    ? {
        ...lp,
        onContextMenu: (e: React.MouseEvent) => {
          if (lp.onContextMenu(e) || !own(e)) return;
          e.preventDefault();
          setMenuAt({ x: e.clientX, y: e.clientY });
        },
      }
    : {};
  const mine = who.kind === "you";
  const name = mine ? youName(who) : who.name;
  // A session's letter: its folder too, in the tip; the name is its title alone.
  const whole = !mine && who.where ? `${name} · ${tilde(who.where)}` : name;
  const tip = mine ? t("msg.fromPanel", { name: deviceOf(who) ? `${name} · ${t(deviceOf(who)![1])}` : name }) : jump ? t("msg.jump", { name: whole }) : who.channel ? t("msg.viaChannel", { name }) : whole;
  const face = <Face who={who} />;
  const press = jump ?? (mine && !left ? () => go({ name: "settings" }) : undefined);
  const row = useWhenRow();
  // Only the one looking on the right; everyone else, a letter too, on the left with the agents
  // (kumo, 2026-11-16; before, every sender was on the right).
  return (
    <div ref={ref} {...row} className={cn(row.className, "flex items-start gap-2.5", left ? "pr-6 sm:pr-10" : "flex-row-reverse pl-6 sm:pl-10")}>
      {bare ? (
        <span className="relative w-7 shrink-0 self-stretch">
          <Gutter time={time} n={n} right={!left} slot />
        </span>
      ) : card ? (
        <>
          <button ref={faceKey} type="button" aria-label={tip} aria-haspopup="dialog" aria-expanded={carded} {...menuKeys} onPointerDown={(e) => ((cardWas.current = carded), menu && lp.onPointerDown(e))} onClick={() => (setCarded(!(cardWas.current ?? carded)), (cardWas.current = null))} className="g-press grid shrink-0 rounded-full select-none [-webkit-touch-callout:none]">
            {face}
          </button>
          {cardEver && (
            <Popover.Root open={carded} onOpenChange={setCarded}>
              <PopSurface anchor={faceKey.current} still align={left ? "start" : "end"} className={CARD_POP}>
                {card}
              </PopSurface>
            </Popover.Root>
          )}
        </>
      ) : (
      <Tip label={tip}>
        {press ? (
          <button type="button" onClick={press} aria-label={tip} className="g-press grid shrink-0 rounded-full">
            {face}
          </button>
        ) : (
          <span className="grid shrink-0" aria-label={tip}>
            {face}
          </span>
        )}
      </Tip>
      )}
      <div className={cn("flex min-w-0 flex-col gap-1", left ? "items-start" : "items-end")}>
        <div className={cn("flex max-w-full min-w-0 items-center gap-1", bare && "hidden")}>
          {/* The time on the inner side: after the name on the left, before it on the right. */}
          {!left && <When time={time} n={n} />}
          {jump ? (
            <button type="button" onClick={jump} className="group flex min-w-0 items-center gap-0.5 rounded-sm px-0.5 text-xs leading-4 text-ink-3 hover:text-ink-2">
              <span className="min-w-0 truncate">{name}</span>
              <ArrowUpRight size={11} className="shrink-0 text-ink-4 group-hover:text-ink-2" />
            </button>
          ) : (
            <span className="min-w-0 truncate px-0.5 text-xs leading-4 text-ink-3">{name}</span>
          )}
          {left && <When time={time} n={n} />}
        </div>
        {children}
        {under}
      </div>
      {menuEver && menu?.(menuAt, () => setMenuAt(null))}
    </div>
  );
});

/** Your words: a bubble on the right, under your terminal's name, its face beside. */
/** Where a message of yours is, as a small mark outside its bubble's lower left corner, no words (the
 *  user, 2026-11-06; its tip says): going, a ring breathing, shown only once it has been going for
 *  1.2 s (kumo, 2026-11-25: one sent sooner shows none); waiting in the queue, a clock. Taken,
 *  nothing: no green check (the user, 2026-11-08). The bubble itself is drawn as it will stay. */
type Going = "sending" | "queued";
const GOING: Record<Going, string> = { sending: "msg.sending", queued: "msg.queued" };
function SendState({ state }: { state: Going }) {
  const label = t(GOING[state] as "msg.sending");
  return (
    <span role="status" aria-label={label} title={label} className={cn("absolute bottom-2 -left-[18px] grid size-3 place-items-center text-ink-3", state === "sending" && "send-late")}>
      {state === "queued" ? <Clock size={10} strokeWidth={2.25} className="g-fade" /> : <span className="send-ring" />}
    </span>
  );
}

/** words: the text drawn another way than Worded's (a channel's, its nicks too: chat/channel). */
export const YouBubble = forwardRef<HTMLDivElement, { text: string; who: You; failed?: boolean; children?: ReactNode; time?: number; n?: number; going?: Going; words?: ReactNode; left?: boolean; card?: ReactNode; menu?: FaceMenu; quote?: ReactNode; under?: ReactNode; bare?: boolean }>(function YouBubble({ text, who, failed, children, time, n, going, words, left, card, menu, quote, under, bare }, ref) {
  // What the words name, where they name it (Worded); the files under the bubble, a picture as
  // large as an agent's (kumo, 2026-11-17: one small in the bubble could not be made out).
  const files = Children.count(children) > 0;
  const bubble = !!text || !files;
  return (
    <Sender ref={ref} who={who} left={left} card={card} menu={menu} under={under} time={time} n={n} bare={bare}>
      {!bubble && quote}
      {bubble && (
        <div className="relative max-w-[min(560px,100%)] px-3.5 py-2">
          {going && <SendState state={going} />}
          <span data-bubble-bg className={cn("absolute inset-0 rounded-xl bg-fill", left ? "rounded-tl-sm" : "rounded-tr-sm", failed && "shadow-[inset_0_0_0_1px_var(--err)]")} aria-hidden />
          {quote && <span className="relative mb-1 block">{quote}</span>}
          {text && (
            <span className="relative block text-md whitespace-pre-wrap [overflow-wrap:anywhere]">
              {words ?? <Worded text={text} />}
            </span>
          )}
        </div>
      )}
      {files && <Sent left={left} going={bubble ? undefined : going}>{children}</Sent>}
    </Sender>
  );
});

/** A message's files under its bubble, as an agent's are (files.tsx Files): a column as wide as
 *  a picture, on the sender's side. going: where it is, when there are no words to mark. */
function Sent({ left, going, children }: { left?: boolean; going?: Going; children: ReactNode }) {
  return (
    <div className={cn("relative flex w-[340px] max-w-full flex-col gap-2", left ? "items-start" : "items-end")}>
      {going && <SendState state={going} />}
      {children}
    </div>
  );
}

/** A file in your words: a picture as an agent's (its copy here under it till the server's
 *  thumbnail has come), opening the original; any other its card. */
export function Attached({ session, f }: { session: string; f: FileRef }) {
  const url = api.fileURL(session, f.ref, false, f.mtime);
  if (f.kind === "image") return <Picture src={shownAs(session, f, url)} full={url} name={f.name} under={previews.get(f.path)} w={f.w} h={f.h} />;
  return <SentFile name={f.name} bytes={f.size} href={api.fileURL(session, f.ref, true, f.mtime)} view={url} mime={f.mime} />;
}

/** A message sent from here the log has yet to bring: its mark says where it is; not taken, red
 *  with a retry. Once the server has answered (s.at), nothing shows beside the bubble ("Taken,
 *  nothing"). bare: under the head of the one before it, its own not drawn. */
/** quote: the message it answers, over its words in the bubble, there from its
 *  first frame: it came only with the landed one, which jumped as it did (kumo, 2026-11-25). */
export function Pending({ s, who, retry, drop, takeBack, bubbleRef, words, bare, quote }: { s: Sent; who: You; retry: () => Promise<void>; drop: () => void; takeBack?: () => Promise<unknown>; bubbleRef?: (el: HTMLDivElement | null) => void; words?: ReactNode; bare?: boolean; quote?: ReactNode }) {
  const [busy, setBusy] = useState(false);
  return (
    <div className="flex flex-col items-end gap-0.5">
      <YouBubble ref={bubbleRef} text={s.text} who={who} words={words} bare={bare} quote={quote} failed={s.failed !== undefined} going={s.failed !== undefined ? undefined : s.queued ? "queued" : s.at !== undefined || s.left !== undefined ? undefined : "sending"}>
        {s.files.map((f) =>
          f.url ? <Picture key={f.ref} src={f.url} full={f.url} name={f.name} w={f.w} h={f.h} /> : <SentFile key={f.ref} name={f.name} bytes={f.size} />,
        )}
      </YouBubble>
      {/* One kept for the turn's end or the quota may be taken back while it waits. */}
      {s.failed === undefined && takeBack && (
        <button type="button" disabled={busy} className="g-fade mr-[34px] cursor-pointer text-xs text-ink-3 hover:text-ink-2" onClick={() => (setBusy(true), void takeBack().catch((e) => toast(t("err.action"), { error: true, description: errText(e) })).finally(() => setBusy(false)))}>
          {t("msg.takeBack")}
        </button>
      )}
      {s.failed !== undefined && (
        <div className="mr-[34px] flex items-center gap-0.5">
          <span className="max-w-[280px] truncate text-xs text-err">{s.failed || t("err.send")}</span>
          <IconKey label={t("msg.dropUnsent")} onClick={drop} className="size-7">
            <X size={14} />
          </IconKey>
          <IconKey label={t("msg.retry")} disabled={busy} onClick={() => (setBusy(true), void retry().finally(() => setBusy(false)))} className="size-7 text-err hover:text-err">
            {busy ? <LoaderCircle size={14} className="animate-spin" /> : <RotateCw size={14} />}
          </IconKey>
        </div>
      )}
    </div>
  );
}

/** A letter: from another session's ir, a plugin or a peer. Not your words: on the left with the
 *  agents, in a grey bubble as a person's are (kumo, 2026-11-16: it was on the right). */
export function Letter({ src, text, time, n, onJump, words, quote, card, menu, bare, children }: { src: From; text: string; time?: number; n?: number; onJump: (id: string) => void; words?: ReactNode; quote?: ReactNode; card?: ReactNode; menu?: FaceMenu; bare?: boolean; children?: ReactNode }) {
  // Its files as in your bubble (ir send -a); what it names, where it names it.
  const files = Children.count(children) > 0;
  return (
    <Sender who={src} time={time} n={n} left jump={!card && src.session ? () => onJump(src.session!) : undefined} card={card} menu={menu} bare={bare}>
      {!(text || !files) && quote}
      {(text || !files) && (
        <div className="max-w-[min(560px,100%)] min-w-0 rounded-xl rounded-tl-sm bg-fill px-3.5 py-2">
          {quote && <span className="mb-1 block">{quote}</span>}
          <p className="m-0 text-md whitespace-pre-wrap [overflow-wrap:anywhere]">
            {words ?? <Worded text={text} />}
          </p>
        </div>
      )}
      {files && <Sent left>{children}</Sent>}
    </Sender>
  );
}

/** A quiet line across the chat: Iridium's own words, an event, an interrupted turn. One line,
 *  cut short where it does not fit; pressed, all of it, its height gliding (docs/前端设计规范.md
 *  "点击，不靠悬停", "要有动画"); pressed again, one line. tail: kept whole at the line's end (a
 *  time). more: other words it opens to, under the line (what the record itself says). */
export function Line({ icon, children, error, tail, more }: { icon?: ReactNode; children: ReactNode; error?: boolean; tail?: ReactNode; more?: string }) {
  const [open, setOpen] = useState(false);
  // The whole of it is drawn only once it has been asked for.
  const [ever, setEver] = useState(false);
  const short = useRef<HTMLSpanElement>(null);
  const press = (e: { target: EventTarget }) => {
    // A name in it pressed opens its own card.
    if (e.target instanceof Element && e.target.closest("button, a")) return;
    const el = short.current;
    if (!open && !more && el && el.scrollWidth <= el.clientWidth) return; // all of it is shown
    setEver(true);
    setOpen(!open);
  };
  return (
    <div onClick={press} className={cn("grid justify-items-center py-1 text-xs transition-[grid-template-rows] duration-(--dur-2) ease-(--ease-out)", !open ? "grid-rows-[16px_0fr]" : more ? "grid-rows-[16px_1fr]" : "grid-rows-[0px_1fr]", error ? "text-err" : "text-ink-3")}>
      <div inert={(open && !more) || undefined} className="flex min-h-0 max-w-full min-w-0 items-center gap-1.5 overflow-hidden">
        {icon}
        <span ref={short} className="min-w-0 truncate">
          {children}
        </span>
        {tail}
      </div>
      <div inert={!open || undefined} aria-hidden={!open || undefined} className="min-h-0 max-w-full overflow-hidden text-center whitespace-pre-wrap [overflow-wrap:anywhere]">
        {ever && (more ?? (
          <>
            {children}
            {tail}
          </>
        ))}
      </div>
    </div>
  );
}

export const tok = (n: number) => (n >= 1_000_000 ? `${(n / 1_000_000).toFixed(1)}M` : n >= 1000 ? `${Math.round(n / 1000)}k` : `${n}`);
export const took = (ms: number) => (ms >= 60_000 ? t("sys.compactMin", { m: Math.floor(ms / 60_000), s: Math.round((ms % 60_000) / 1000) }) : t("sys.compactSec", { s: Math.max(1, Math.round(ms / 1000)) }));

/** A compaction, as a rule across the chat: the light and the words between two hairlines. Live,
 *  while it runs: a yellow light breathing and the seconds so far; done, the context before and
 *  after it, how long it took and when (the user, 2026-11-06: a message of its own, not your
 *  /compact, not the dots). The same height either way, the one taking the other's place. Done,
 *  pressed: what it was compacted to, under the rule. */
export function Compaction({ b, since }: { b?: Block; since?: number }) {
  const [now, setNow] = useState(() => Date.now());
  const [open, setOpen] = useState(false);
  useEffect(() => {
    if (b) return;
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(timer);
  }, [!!b]);
  const c = b?.compact;
  const parts: string[] = [];
  if (b) {
    parts.push(t(c?.auto ? "sys.compactAuto" : "sys.compact"));
    if (c?.before) parts.push(c.after ? `${tok(c.before)} → ${tok(c.after)}` : t("sys.compactFrom", { n: tok(c.before) }));
    if (c?.ms) parts.push(took(c.ms));
    if (b.time) parts.push(clock(b.time));
  } else {
    parts.push(t("sys.compacting"));
    if (since && now - since >= 3000) parts.push(took(now - since));
  }
  // What it was compacted to, as far as the page has it: under the rule once it is pressed.
  const sum = b?.text?.trim().slice(0, 600);
  return (
    <div className="g-fade py-1 text-xs text-ink-3" role={b ? undefined : "status"}>
      <div className="flex items-center gap-3" onClick={sum ? () => setOpen(!open) : undefined}>
        <span aria-hidden className="h-px flex-1 bg-edge" />
        <span className="flex min-w-0 items-center gap-2">
          {b ? <FoldVertical size={13} className="shrink-0 text-ink-4" /> : <span aria-hidden className="row-lamp shrink-0" data-s="compacting" />}
          <span className="truncate tabular-nums">{parts.join(" · ")}</span>
        </span>
        <span aria-hidden className="h-px flex-1 bg-edge" />
      </div>
      {sum && (
        <div inert={!open || undefined} aria-hidden={!open || undefined} className="g-reveal" data-open={open ? "" : undefined}>
          <div className="min-h-0 overflow-hidden">
            <p className="m-0 mx-auto max-w-[min(640px,100%)] pt-1.5 leading-5 whitespace-pre-wrap [overflow-wrap:anywhere]">{sum}</p>
          </div>
        </div>
      )}
    </div>
  );
}

const SYSTEM: Record<string, Parameters<typeof t>[0]> = { compact: "sys.compact", interrupted: "sys.interrupted", unreadable: "sys.unreadable", api_error: "sys.apiError", local_command: "sys.localCommand" };

/** What the agent said: Markdown, its file links (ir:<id>) in place, then the files its file lines
 *  give ([文件] `path`). */
/** draft: this page saw it written (chats.ts draftOf), or it is being written still (live): drawn
 *  as what is written is, the same thing from its first words to its block; rise: its ref, for
 *  its coming in. */
export type AsDraft = { live: boolean; rise?: (el: HTMLElement | null) => void };
export const Assistant = memo(function Assistant({ b, session, draft }: { b: Block; session: string; draft?: AsDraft }) {
  const home = useHome().data;
  // The members of its channel it names (agents, people, apps: chat/channel/nick.tsx), links.
  const names = useContext(Nicks)?.names;
  // A merged forward in them: its card under them (forwardcard.tsx).
  const fw = splitForwards(b.text ?? "");
  const said = withoutFiles(withoutNameLines(fromSatori(fw.words)), new Set(b.files?.map((f) => f.path)), home);
  // The numbers it names, up to the newest the page has as it is drawn, its own at least (a
  // message names those before it: drawn before a newer came, it is not drawn again for it).
  const seqs = useContext(SeqRefs);
  const end = seqs ? Math.max(seqs.end, Number(b.at) || 0) : 0;
  const text = names ? linkNicks(said, names, end) : said;
  // A file link's file shows where the words put it; the rest (file lines) under them.
  const under = b.files?.filter((f) => !isLink(f)) ?? [];
  // data-at: where a quote of it is gone to on a session's page (channel/jump.ts).
  return (
    <div data-at={b.at} ref={draft?.rise} className="flex min-w-0 flex-col items-start gap-2 pr-6">
      {draft ? (
        <Streamed text={draft.live ? draftWords(fromSatori(b.text ?? ""), true) : text} live={draft.live} className="w-full" session={session} files={b.files ?? undefined} />
      ) : (
        text && <Markdown text={text} className="w-full" session={session} files={b.files ?? undefined} />
      )}
      {!draft?.live && <Forwards fs={fw.forwards} />}
      {!draft?.live && !!under.length && <Files session={session} files={under} />}
    </div>
  );
});

/** A message of the agent's record as it was given it (Said's given): the whole text, its
 *  sender's tag and all; a channel's message as its head ("[kumo] #12: words") in the bubble and
 *  its <channel-context> under it (context.tsx), then what came after the block. */
function AsGiven({ b, rec, src, session, onJump, card, menu, left }: { b: Block; rec: GivenRec; src: Source; session: string; onJump: (id: string) => void; card?: (b: Block) => ReactNode; menu?: (b: Block) => FaceMenu | undefined; left?: (b: Block) => boolean }) {
  const g = rec.kind === "turn-prefix" ? parseGiven(rec.text, rec.heads) : null;
  const words = g ? g.head : rec.text;
  const under = g && (
    <>
      <ChannelContext g={g} />
      <AfterContext text={g.after} />
    </>
  );
  const files = b.files?.map((f) => <Attached key={f.ref} session={session} f={f} />);
  if (src.kind === "iridium")
    return (
      <div className="flex min-w-0 flex-col items-center gap-2">
        <Line icon={<RotateCcw size={12} />}>{words}</Line>
        {under}
      </div>
    );
  if (src.kind === "letter")
    return (
      <div className="flex min-w-0 flex-col items-end gap-2">
        <Letter src={src} text={words} time={b.time} onJump={onJump}>
          {files}
        </Letter>
        {under}
      </div>
    );
  return (
    <YouBubble text={words} who={src} time={b.time} card={card?.(b)} menu={menu?.(b)} left={left?.(b)} words={<NickWords text={words} />} under={under || undefined}>
      {files}
    </YouBubble>
  );
}

/** card: what a person's face opens (their card, chat.tsx), for one not the one looking. left:
 *  another person's words, on the left: only the one looking is on the right (kumo, 2026-11-16).
 *  given: what the agent was given as the block, kept (given.ts; a channel member's agent's
 *  record): shown as it was given, verbatim. */
export function Said({ b, session, cwd, source, onJump, card, menu, left, given: givenAs }: { b: Shown; session: string; cwd: string; source: (b: { from?: string; fromTerminal?: string; fromDevice?: Device }) => Source; onJump: (id: string) => void; card?: (b: Block) => ReactNode; menu?: (b: Block) => FaceMenu | undefined; left?: (b: Block) => boolean; given?: (b: Block) => GivenRec | undefined }) {
  // In the record of the channel's own session, a block is a message of the channel too: reacted
  // to and taken back as one (react.tsx). Not in another's record shown here (an agent's work).
  const named = useContext(Nicks);
  const here = named?.channel === session;
  switch (b.kind) {
    case "user": {
      const src = source(b);
      const rec = givenAs?.(b);
      if (rec) return <AsGiven b={b} rec={rec} src={src} session={session} onJump={onJump} card={card} menu={menu} left={left} />;
      const text = b.text ?? "";
      // A reaction its agent was given, alone in its channel: who put which emoji on what.
      const rl = src.kind === "iridium" ? reactionLine(text) : null;
      if (rl) return <Line>{`${rl.who} ${rl.emoji} ${rl.words}`}</Line>;
      if (src.kind === "iridium") return <Line icon={<RotateCcw size={12} />}>{text}</Line>;
      // The server writes each attachment as a line "[附件] `path`" (absolute): the file shows as itself instead.
      // A channel's message as the agent was given it (chat/channel/lib givenTurn): its words
      // alone, where what it was given is not kept (AsGiven).
      const given = givenTurn(text);
      const paths = new Set(b.files?.map((f) => f.path));
      const words = given ? given.words : paths.size ? withoutFiles(text, paths) : text;
      if (src.kind === "letter")
        return (
          <Letter src={src} text={words} time={b.time} onJump={onJump}>
            {b.files?.map((f) => <Attached key={f.ref} session={session} f={f} />)}
          </Letter>
        );
      const bubble = (
        <YouBubble text={words} who={src} time={b.time} card={card?.(b)} menu={menu?.(b)} left={left?.(b)} words={<NickWords text={words} />}>
          {b.files?.map((f) => <Attached key={f.ref} session={session} f={f} />)}
        </YouBubble>
      );
      return here && !given ? (
        <Copyable copy={words} act={{ channel: session, key: { at: b.at, ...(b.id ? { input: b.id } : {}) }, may: !!named.may, side: left?.(b) ? "left" : "right" }}>
          {bubble}
        </Copyable>
      ) : (
        bubble
      );
    }
    case "assistant":
      return (
        <Copyable copy={b.text ?? ""} act={here ? { channel: session, key: { at: b.at }, may: !!named.may } : undefined}>
          <Assistant b={b} session={session} />
        </Copyable>
      );
    case "system":
      if (b.event === "iridium") return <Line icon={<RotateCcw size={12} />}>{b.text}</Line>;
      if (b.event === "compact") return <Compaction b={b} />;
      // A background task ended (a workflow, a background agent): its summary as Claude Code
      // says it; a subagent's, what it handed back (dispatched.tsx).
      if (b.event === "task")
        return handBack(b.text) ? (
          <TaskBack b={b} session={session} />
        ) : (
          <Line icon={<WorkflowIcon size={12} />} tail={b.time ? <span className="shrink-0 whitespace-pre"> · {clock(b.time)}</span> : undefined}>
            {b.text}
          </Line>
        );
      return b.event === "api_error" || b.event === "local_command" || b.event === "unreadable" ? (
        <SystemFold b={b} session={session} />
      ) : (
        <Line more={b.text?.trim() || undefined}>
          {t(SYSTEM[b.event ?? ""] ?? "sys.other")}
          {b.time ? ` · ${clock(b.time)}` : ""}
        </Line>
      );
    case "fold":
      // A step auto mode denied is a failed step among the others (red), nothing more.
      return <Fold steps={b.steps} session={session} cwd={cwd} />;
    case "running":
      return <Running tool={b.tool} cwd={cwd} />;
  }
}

/** A system line with words worth reading (an API error, a local command's output): one line that opens. */
function SystemFold({ b, session }: { b: Block; session: string }) {
  return (
    <details className="group/sys text-xs text-ink-3">
      <summary className={cn("flex cursor-default list-none items-center justify-center gap-1.5 py-1 [&::-webkit-details-marker]:hidden", b.event === "api_error" && "text-err")}>
        <span className="min-w-0 truncate">{t(SYSTEM[b.event ?? ""] ?? "sys.other")}</span>
        <ChevronRight size={12} className="fold-chev shrink-0 transition-transform duration-(--dur-3) group-open/sys:rotate-90" />
      </summary>
      <Clipped text={b.text ?? ""} length={b.length} at={b.at} session={session} error={b.event === "api_error"} cap />
    </details>
  );
}

/** Each kind's icon and its hue, muted (.k-tint); other calls stay grey. */
const KINDS: Record<Kind, [LucideIcon, number?]> = {
  read: [FileText, 230],
  search: [Search, 195],
  web: [Globe, 255],
  edit: [FilePen, 60],
  command: [SquareTerminal, 285],
  agent: [Bot, 350],
  todo: [ListChecks, 140],
  ask: [MessageCircleQuestionMark, 85],
  mcp: [Plug, 110],
  other: [Wrench],
  thinking: [Cpu, 320],
};
/** A shell call's icon by what its command does; in the command kind's hue. */
const CMDS: Record<Cmd, LucideIcon> = {
  "ir-send": Send,
  "ir-archive": Archive,
  "ir-unarchive": ArchiveRestore,
  "ir-new": MessageSquarePlus,
  "ir-wait": Hourglass,
  "ir-read": BookOpen,
  "ir-list": List,
  "ir-find": Search,
  "ir-stop": CircleStop,
  "ir-files": FilesIcon,
  "ir-access": KeyRound,
  "ir-profile": UserRound,
  "ir-rename": PencilLine,
  "ir-ws": Folders,
  "ir-machines": MonitorSmartphone,
  "ir-secret": LockKeyhole,
  "ir-usage": ChartColumn,
  "ir-bot": BotMessageSquare,
  "ir-share": Link2,
  "ir-settings": SlidersHorizontal,
  "ir-answer": MousePointerClick,
  git: GitBranch,
  "git-commit": GitCommitHorizontal,
  "git-push": CloudUpload,
  "git-pull": CloudDownload,
  "git-diff": FileDiff,
  "git-log": History,
  "git-status": FolderGit2,
  "git-branch": GitBranch,
  gh: GitPullRequest,
  test: FlaskConical,
  build: Hammer,
  install: Package,
  net: Earth,
  remote: Network,
  search: Search,
  read: FileText,
  list: FolderOpen,
  file: FilesIcon,
  delete: Trash2,
  script: FileCode,
  service: ServerCog,
  wait: Hourglass,
  stop: CircleStop,
};
/** shell: a shell's call, whose command is of no known sort: a shell's own glyph (the user,
 *  2026-11-09). */
export const KindIcon = ({ kind, cmd, shell, failed, size = 14, className }: { kind: Kind; cmd?: Cmd; shell?: boolean; failed?: boolean; size?: number; className?: string }) => {
  const [KindGlyph, hue] = KINDS[kind];
  const Icon = (kind === "command" && cmd && CMDS[cmd]) || (shell ? Terminal : KindGlyph);
  const tint = !failed && hue !== undefined;
  return <Icon size={size} className={cn("shrink-0", failed ? "text-err" : tint ? "k-tint" : "text-ink-3", className)} style={tint ? ({ "--k-h": hue } as CSSProperties) : undefined} aria-hidden />;
};
/** An edit's lines added and taken, green and red; a side at 0 left out. */
export const DiffStat = ({ d }: { d: Diff }) => (
  <span className="shrink-0 font-mono text-xs tabular-nums">
    {d.added > 0 && <span className="text-plus">+{d.added}</span>}
    {d.added > 0 && d.taken > 0 && " "}
    {d.taken > 0 && <span className="text-minus">−{d.taken}</span>}
  </span>
);
const failed = (s: Step) => s.kind === "tool" && !!s.result?.error;

/** A step's icon: its kind's, a shell command's sort's, red if it failed. */
export const StepIcon = ({ s, className }: { s: Step; className?: string }) =>
  s.kind === "thinking" ? <KindIcon kind="thinking" className={className} /> : <KindIcon kind={kindOf(s.name)} cmd={cmdOfCall(s)} shell={isShell(s.name)} failed={failed(s)} className={className} />;

/** A step's line: its icon (bare: none, its row has it), its name, what it is about in monospace,
 *  an edit's +/−. */
export function StepLine({ s, cwd, bare }: { s: Step; cwd?: string; bare?: boolean }) {
  if (s.kind === "thinking")
    return (
      <>
        {!bare && <StepIcon s={s} />}
        <span className="shrink-0 text-ink-2">{t("msg.thinking")}</span>
        <span className="min-w-0 truncate text-xs">{(s.text ?? "").replace(/[*_`#>]+/g, "").replace(/\s+/g, " ")}</span>
      </>
    );
  const a = argOf(s, cwd);
  const cp = cpOfCall(s);
  // Sent to the background: done here as it began, what it does goes on (the work bar has it).
  const bg = bgLaunch(s);
  return (
    <>
      {!bare && <StepIcon s={s} />}
      {bg ? <span className="shrink-0 text-ink-2">{t(bg === "agent" ? "work.agentSent" : "work.bgShell")}</span> : !isShell(s.name) && <span className="shrink-0 text-ink-2">{s.name}</span>}
      {cp ? <CpArg from={cp.from} to={cp.to} /> : <span className={cn("min-w-0 truncate", a.code ? "font-mono text-xs" : "text-xs")}>{a.arg}</span>}
      {a.diff && !failed(s) && <DiffStat d={a.diff} />}
      {failed(s) && <CircleX size={13} className="shrink-0 text-err" aria-label={t("msg.failed")} />}
    </>
  );
}

export const ROW = "flex h-7 min-w-0 cursor-default list-none items-center gap-2 rounded-sm text-sm text-ink-3 hover:text-ink-2 [&::-webkit-details-marker]:hidden pointer-coarse:h-9";

/** The steps between two things said: one resting step is its own line; more fold into one
 *  summary line that expands in place into a height-capped inner-scroll list (each step one line,
 *  clicking any step opens its detail card). */
export function Fold({ steps, session, cwd }: { steps: Step[]; session: string; cwd: string }) {
  const one = steps.length === 1 ? steps[0]! : undefined;
  // A call to a subagent: the agent's words to it, not a step's line (dispatched.tsx).
  if (one?.kind === "tool" && toldOf(one)) return <Dispatch call={one as Tool} session={session} />;
  if (one) return <StepView s={one} session={session} cwd={cwd} />;
  const opened = steps.flatMap((s) => (s.kind === "tool" && newOfCall(s)) || []);
  return (
    <div className="min-w-0">
      <StepsFold steps={steps} session={session} cwd={cwd} />
      {/* The sessions its ir new steps opened, under its line. */}
      {opened.length > 0 && <Opened opened={opened} />}
    </div>
  );
}

/** How long, short: 0.2s, 41s, 3m05s. */
export const lasted = (ms: number) => (ms < 950 ? `${(ms / 1000).toFixed(1)}s` : ms < 60_000 ? `${Math.round(ms / 1000)}s` : `${Math.floor(ms / 60_000)}m${String(Math.round((ms % 60_000) / 1000)).padStart(2, "0")}s`);

/** How long a step took, once it is done. */
export const tookOf = (s: Step) => (s.kind === "tool" && s.time && s.result?.time ? s.result.time - s.time : undefined);

/** In-place expandable step list with capped inner scroll and edge fades, and one shared StepCard
 *  popover. */
function StepsFold({ steps, session, cwd }: { steps: Step[]; session: string; cwd: string }) {
  const [open, setOpen] = useState(false);
  const ever = useEver(open);
  const revealRef = useRef<HTMLDivElement>(null);
  const listRef = useRef<HTMLDivElement>(null);
  const [edges, setEdges] = useState({ top: false, bottom: false });
  // Selected step in the shared StepCard popover (by its Step.at key), plus its row anchor rect.
  const [sel, setSel] = useState<{ at: string; rect: DOMRect } | null>(null);
  const everCard = useEver(sel !== null);
  const wasSelAt = useRef<string | null>(null);

  const runs = useMemo(() => foldLine(steps), [steps]);
  const diff = useMemo(() => diffSum(runs), [runs]);
  const span = useMemo(() => foldSpan(steps), [steps]);

  const syncEdges = (el: HTMLDivElement) => {
    const over = el.scrollHeight - el.clientHeight > 2;
    const top = over && el.scrollTop > 2;
    const bottom = over && el.scrollHeight - el.clientHeight - el.scrollTop > 2;
    setEdges((prev) => (prev.top === top && prev.bottom === bottom ? prev : { top, bottom }));
  };

  // The newest step in view as the list opens.
  useLayoutEffect(() => {
    const el = listRef.current;
    if (!open || !el) return;
    el.scrollTop = el.scrollHeight;
    syncEdges(el);
  }, [open, steps.length]);

  const toggleOpen = () => {
    if (open) setSel(null);
    setOpen(!open);
  };

  const selStep = sel ? steps.find((s) => s.at === sel.at) : undefined;
  const anchor = useMemo(() => (sel ? { getBoundingClientRect: () => sel.rect } : null), [sel]);

  return (
    <div className="min-w-0">
      <button type="button" aria-expanded={open} aria-label={foldSaid(runs)} onClick={toggleOpen} className={cn(ROW, "-my-0.5 w-full min-w-0 text-left outline-none focus-visible:text-ink-2", open && "text-ink-2")}>
        <span className="min-w-0 truncate">
          {runs.map((r, i) => (
            <span key={i} className={cn(i > 0 && "ml-3")}>
              {(r.name ? runCmds(steps, r.name) : [{ cmd: undefined, n: 1, failed: false }]).map((g, j, gs) => (
                <span key={j} className={cn("inline-block", j < gs.length - 1 ? "mr-1.5" : !isShell(r.name) && "mr-2")}>
                  <KindIcon kind={r.name ? kindOf(r.name) : "thinking"} cmd={g.cmd} shell={isShell(r.name)} failed={g.failed} className="inline-block align-[-2.5px]" />
                  {(gs.length > 1 || isShell(r.name)) && g.n > 1 && <span className={cn("text-xs tabular-nums", g.failed && "text-err")}>×{g.n}</span>}
                </span>
              ))}
              {/* A shell's run is its icons alone, each with how many (the user, 2026-11-09): no
                  room after the last for a name, only the gap between runs. The failed calls an
                  icon of their own, red, with how many (the user, 2026-11-13). */}
              {!isShell(r.name) && <span className="text-ink-2">{r.name || t("msg.thinking")}</span>}
              {!isShell(r.name) && r.n > 1 && runCmds(steps, r.name).length < 2 && <span className="tabular-nums"> ×{r.n}</span>}
            </span>
          ))}
        </span>
        {diff && <DiffStat d={diff} />}
        {span !== undefined && <span className="ml-auto shrink-0 pl-2 text-xs text-ink-4 tabular-nums">{lasted(span)}</span>}
        <ChevronRight size={13} className={cn("shrink-0 text-ink-4 transition-transform duration-(--dur-2)", span === undefined && "ml-auto", open && "rotate-90")} />
      </button>
      <div ref={revealRef} inert={!open || undefined} aria-hidden={!open || undefined} className="g-reveal" data-open={open ? "" : undefined}>
        <div className="min-h-0 min-w-0 overflow-hidden">
          {ever && (
            <div className="mt-0.5 ml-[7px] pl-3 shadow-[inset_1px_0_0_var(--edge)]">
              <div className="relative min-w-0">
                <div aria-hidden className={cn("pointer-events-none absolute inset-x-0 top-0 z-10 h-5 bg-[linear-gradient(to_bottom,var(--paper),transparent)] transition-opacity duration-(--dur-1)", edges.top ? "opacity-100" : "opacity-0")} />
                <div ref={listRef} onScroll={(e) => syncEdges(e.currentTarget)} className="steps-scroll flex max-h-[min(48dvh,380px)] min-w-0 flex-col overflow-y-auto pr-1">
                  {steps.map((s, index) => {
                    if (s.kind === "tool" && toldOf(s)) return <Dispatch key={s.at} call={s as Tool} session={session} />;
                    if (s.kind === "tool" && s.result?.images?.length) return <StepView key={s.at} s={s} session={session} cwd={cwd} lifted num={index + 1} />;
                    const took = tookOf(s);
                    const activeRow = sel?.at === s.at;
                    return (
                      <button
                        key={s.at}
                        type="button"
                        aria-haspopup="dialog"
                        aria-expanded={activeRow}
                        onPointerDown={() => (wasSelAt.current = sel?.at ?? null)}
                        onClick={(e) => {
                          if (e.detail > 0 && wasSelAt.current === s.at) return setSel(null);
                          setSel(activeRow ? null : { at: s.at, rect: e.currentTarget.getBoundingClientRect() });
                        }}
                        className={cn(ROW, "w-full shrink-0 px-1.5 text-left outline-none focus-visible:text-ink-2", activeRow && "text-ink")}
                      >
                        <span className="w-6 shrink-0 text-right text-xs text-ink-4 tabular-nums">{index + 1}</span>
                        <StepLine s={s} cwd={cwd} />
                        {took !== undefined && <span className="ml-auto shrink-0 pl-2 text-xs text-ink-4 tabular-nums">{lasted(took)}</span>}
                      </button>
                    );
                  })}
                </div>
                <div aria-hidden className={cn("pointer-events-none absolute inset-x-0 bottom-0 z-10 h-5 bg-[linear-gradient(to_top,var(--paper),transparent)] transition-opacity duration-(--dur-1)", edges.bottom ? "opacity-100" : "opacity-0")} />
              </div>
            </div>
          )}
        </div>
      </div>
      {everCard && (
        <Popover.Root open={!!selStep} onOpenChange={(o) => !o && setSel(null)}>
          <PopoverPortal>
            <Popover.Positioner anchor={anchor} side={sel && sel.rect.top + sel.rect.height / 2 > window.innerHeight / 2 ? "top" : "bottom"} align="start" sideOffset={4} collisionPadding={8} collisionAvoidance={{ side: "none", align: "shift", fallbackAxisSide: "none" }} className="z-50">
              <Popover.Popup initialFocus={(type) => type === "keyboard"} className="g-unfold glass thick float flex max-h-[min(72dvh,620px,var(--available-height))] w-[min(620px,calc(100vw-16px))] flex-col overflow-hidden rounded-xl text-ink outline-none">
                {selStep && <StepCard key={selStep.at} s={selStep} session={session} />}
              </Popover.Popup>
            </Popover.Positioner>
          </PopoverPortal>
        </Popover.Root>
      )}
    </div>
  );
}



/** A step's line; pressed, what it was given and what came back open in a card at the press, like a
 *  context menu, instead of under the line (the user, 2026-11-08): the conversation stays put.
 *  One whose result gave the model pictures is otherwise (otae, 2026-11-10, again 2026-11-23):
 *  they are shut at first, and the whole line opens and shuts them, no card; its icon, hovered,
 *  turns to the chevron (the user, 2026-11-09: the tool's own icon otherwise). */
function StepView({ s, session, cwd, lifted, num }: { s: Step; session: string; cwd: string; lifted?: boolean; num?: number }) {
  const images = s.kind === "tool" ? s.result?.images : undefined;
  const [shown, setShown] = useState(false);
  const opened = !lifted && s.kind === "tool" ? newOfCall(s) : undefined;
  if (!images?.length)
    return opened ? (
      <div className="min-w-0">
        <StepCall s={s} session={session} cwd={cwd} />
        <Opened opened={[opened]} />
      </div>
    ) : (
      <StepCall s={s} session={session} cwd={cwd} />
    );
  return (
    <div className="min-w-0 shrink-0">
      {/* Touch has no hover: the chevron shows always, the line where it was. */}
      <button
        type="button"
        aria-expanded={shown}
        aria-label={t(shown ? "msg.hidePictures" : "msg.showPictures")}
        onClick={() => setShown(!shown)}
        className={cn("group/step flex w-full min-w-0 items-center gap-2 text-left outline-none", num !== undefined && "px-1.5")}
      >
        {num !== undefined && <span className="w-6 shrink-0 text-right text-xs text-ink-4 tabular-nums">{num}</span>}
        <span className="grid h-7 shrink-0 place-items-center rounded-sm group-hover/step:bg-fill group-focus-visible/step:bg-fill pointer-coarse:h-9 [&>*]:[grid-area:1/1] [&>*]:transition-[opacity,rotate] [&>*]:duration-(--dur-1)">
          <StepIcon s={s} className="group-hover/step:opacity-0 group-focus-visible/step:opacity-0 pointer-coarse:opacity-0" />
          <ChevronRight size={14} aria-hidden className={cn("text-ink-3 opacity-0 group-hover/step:opacity-100 group-focus-visible/step:opacity-100 pointer-coarse:opacity-100", shown && "rotate-90")} />
        </span>
        <span className={cn(ROW, "min-w-0 flex-1 group-hover/step:text-ink-2", shown && "text-ink-2")}>
          <StepLine s={s} cwd={cwd} bare />
        </span>
      </button>
      {shown && <Pictures session={session} s={s} />}
    </div>
  );
}

/** A step's line as a key to its card (StepView). bare: without its icon, which its row has. */
function StepCall({ s, session, cwd, bare }: { s: Step; session: string; cwd: string; bare?: boolean }) {
  // Where it was pressed stays as the card goes: put away, it folds back into that point, not
  // first thrown to the window's corner by an anchor gone (the user, 2026-11-08).
  const [open, setOpen] = useState(false);
  const [at, setAt] = useState<{ x: number; y: number; h: number } | null>(null);
  const row = useRef<HTMLButtonElement>(null);
  const toggle = (o: boolean, d: { event?: Event }) => {
    setOpen(o);
    if (!o) return;
    const r = row.current?.getBoundingClientRect();
    const e = d.event as MouseEvent | undefined;
    const pointed = e && "clientX" in e && e.detail > 0;
    setAt({ x: pointed ? e.clientX : (r?.left ?? 0) + 22, y: r?.top ?? (pointed ? e.clientY : 0), h: r?.height ?? 0 });
  };
  // Its line's height at the press: the card is under the line or over it, never on it, so the
  // line can be pressed again to put the card away (kumo, 2026-11-24: over the line when there
  // was no room under, the pointer was left on the card).
  const anchor = useMemo(() => (at ? { getBoundingClientRect: () => DOMRect.fromRect({ x: at.x, y: at.y, width: 0, height: at.h }) } : null), [at]);
  return (
    <Popover.Root open={open} onOpenChange={toggle}>
      <Popover.Trigger ref={row} className={cn(ROW, "w-full shrink-0 text-left outline-none focus-visible:text-ink-2", open && "text-ink-2")}>
        <StepLine s={s} cwd={cwd} bare={bare} />
      </Popover.Trigger>
      <PopoverPortal>
        <Popover.Positioner anchor={anchor} side={at && at.y + at.h / 2 > window.innerHeight / 2 ? "top" : "bottom"} align="start" sideOffset={4} collisionPadding={8} collisionAvoidance={{ side: "none", align: "shift", fallbackAxisSide: "none" }} className="z-50">
          <Popover.Popup initialFocus={(type) => type === "keyboard"} className="g-unfold glass thick float flex max-h-[min(72dvh,620px,var(--available-height))] w-[min(620px,calc(100vw-16px))] flex-col overflow-hidden rounded-xl text-ink outline-none">
            <StepCard s={s} session={session} />
          </Popover.Popup>
        </Popover.Positioner>
      </PopoverPortal>
    </Popover.Root>
  );
}

/** The sessions ir new steps opened (newOfCall: none before one is done), a card each under the
 *  step's line, as a step's pictures are (plan "在哪干活", 2026-11-10). */
function Opened({ opened }: { opened: { id: string; agent: string }[] }) {
  const list = useList();
  return (
    <div className="mt-0.5 mb-1.5 ml-[7px] flex flex-wrap gap-1.5 pl-3 shadow-[inset_1px_0_0_var(--edge)]">
      {opened.map((n) => {
        const x = list && sessionOf(n.id, list);
        const m: Mention = { kind: "session", title: (x && (titleOf(x) || tilde(x.cwd))) || n.id, agent: agentName((x?.agent ?? n.agent) as Agent), id: n.id, text: n.id };
        return <MentionCard key={n.id} m={m} />;
      })}
    </div>
  );
}

/** An ir cp's from → to: a side at another machine its face (by the name ir was given) and its
 *  path there; one here as written, ~ for the home. */
function CpArg({ from, to }: { from: CpSide; to: CpSide }) {
  const ms = useMachines();
  const side = (x: CpSide) => {
    const m = x.machine ? machineNamed(x.machine, ms) : undefined;
    return (
      <span className="flex min-w-0 shrink items-center gap-1">
        {m && <MachineFace m={m} size={12} className={cn(!m.online && "opacity-50 grayscale")} />}
        <span className="min-w-0 truncate">{x.machine && !m ? `${x.machine}:${x.path}` : x.machine ? x.path : tilde(x.path)}</span>
      </span>
    );
  };
  return (
    <span className="flex min-w-0 items-center gap-1.5 font-mono text-xs">
      {side(from)}
      <span aria-hidden className="shrink-0 text-ink-4">→</span>
      {side(to)}
    </span>
  );
}

/** The card: no head and no Input/Output labels (kumo, 2026-11-24: its line is right above it,
 *  the blocks themselves are enough). Shell calls show their command directly without a "command"
 *  field label. All of it shown, however long: the card scrolls (kumo, 2026-11-25). */
/** A part of a step's card as a terminal writes it (kumo, 2026-11-25): the command after a "$",
 *  what came back under it after a "⎿", lighter; one font, no rule between. The marks themselves
 *  in the words' own ink, bold: in a lighter one they were hardly seen (kumo, 2026-11-25). */
const Lead = ({ mark, dim, children }: { mark: string; dim?: boolean; children: ReactNode }) => (
  <div className={cn("flex min-w-0 items-start", dim && "[&_pre]:text-ink-3")}>
    <span aria-hidden className="w-3.5 shrink-0 py-1 text-center font-mono text-xs leading-[18px] font-semibold text-ink select-none">{mark}</span>
    <div className="min-w-0 flex-1">{children}</div>
  </div>
);

export function StepCard({ s, session }: { s: Step; session: string }) {
  const rawCmd = (() => {
    if (s.kind !== "tool" || !isShell(s.name)) return undefined;
    const i = inputOf(s.input);
    const v = i.command ?? i.cmd;
    return typeof v === "string" && v ? v : undefined;
  })();
  const hasOut = s.kind === "tool" && s.result && (s.result.text.trim() !== "" || !!s.result.length);
  return (
    <div className="grid min-h-0 gap-1 overflow-y-auto overscroll-contain px-3 py-3">
      {s.kind === "thinking" ? (
        <Clipped text={s.text ?? ""} length={s.length} at={s.at} session={session} prose asked />
      ) : (
        <>
          {rawCmd !== undefined ? (
            <Lead mark="$">
              <Clipped text={s.input ?? ""} length={s.length} at={s.at} session={session} shell asked />
            </Lead>
          ) : (
            <Clipped text={s.input ?? ""} length={s.length} at={s.at} session={session} fields asked />
          )}
          {hasOut && s.result ? (
            <Lead mark="⎿" dim>
              {s.name === "Agent" || s.name === "Task" ? <SubResult r={s.result} session={session} fold={false} /> : <Clipped text={s.result!.text} length={s.result!.length} at={s.result!.at} session={session} error={s.result!.error} asked />}
            </Lead>
          ) : null}
          {/* Nothing came back (a cd), or not yet: no line for it, no 暂无结果 (kumo, 2026-11-25). */}
          {/* The sessions it names by id, each a card going there (ir send, ir wait); not a list's
              many (ir ls): the sidebar has those. ir new's is under its line (Opened). */}
          {!newOfCall(s) && <NamedCards text={s.input ?? ""} also={s.result?.text} />}
        </>
      )}
    </div>
  );
}

/** An input object's fields, each its name and its value: a short one on the line, a long one or
 *  one of many lines in a block; an edit's old/new pair as a unified diff. A command in a shell's
 *  colours, a file's text in its language's (by its extension). Anything else as it is. */
function Fields({ text }: { text: string }) {
  let v: unknown;
  try {
    v = JSON.parse(text);
  } catch {
    // Cut short (the page has only its start): its string fields as far as they go.
    const part = text.trimStart().startsWith("{") ? partialInput(text) : {};
    if (!Object.keys(part).length) return <Pre text={pretty(text)} />;
    v = part;
  }
  if (!v || typeof v !== "object" || Array.isArray(v)) return <Pre text={pretty(text)} />;
  const rows = Object.entries(v as Record<string, unknown>).filter(([k]) => k !== "_diff");
  const file = rows.find(([k, x]) => FILE.has(k) && typeof x === "string")?.[1] as string | undefined;
  const oldRow = rows.find(([k, x]) => OLD.has(k) && typeof x === "string");
  const newRow = rows.find(([k, x]) => EDIT_NEW.has(k) && typeof x === "string");
  const hasEditDiff = !!(oldRow && newRow);
  if (!rows.length) return <span className="text-xs text-ink-4">{"{}"}</span>;
  return (
    <div className="grid min-w-0 gap-2">
      {rows.map(([k, x]) => {
        if (hasEditDiff && k === newRow[0]) return null;
        if (hasEditDiff && k === oldRow[0]) return <DiffBlock key="diff" oldText={oldRow[1] as string} newText={newRow[1] as string} />;
        const str = typeof x === "string" ? x : JSON.stringify(x, null, 2);
        const block = OLD.has(k) || NEW.has(k);
        const inline = !block && !SHELL.has(k) && !str.includes("\n") && str.length <= 80;
        const lang = typeof x !== "string" ? undefined : SHELL.has(k) ? "bash" : block ? file : undefined;
        return inline ? (
          <div key={k} className="flex min-w-0 items-baseline gap-2 text-xs">
            <span className="shrink-0 font-mono text-ink-4">{k}</span>
            <span className={cn("min-w-0 font-mono [overflow-wrap:anywhere]", typeof x === "string" ? "text-ink-2" : "text-ink-3")}>{str}</span>
          </div>
        ) : (
          <div key={k} className="grid min-w-0 gap-1">
            <span className="font-mono text-xs text-ink-4">{k}</span>
            <Pre text={str} lang={lang} />
          </div>
        );
      })}
    </div>
  );
}
const OLD = new Set(["old_string", "old_str", "oldText"]);
const EDIT_NEW = new Set(["new_string", "new_str", "newText"]);
const NEW = new Set([...EDIT_NEW, "content"]);
const SHELL = new Set(["command", "cmd", "script"]);
const FILE = new Set(["file_path", "path", "filePath", "notebook_path"]);

/** Unified line diff for Edit calls (old_string → new_string), replacing side-by-side red/green
 *  bordered boxes (kumo, 2026-11-24). */
function DiffBlock({ oldText, newText }: { oldText: string; newText: string }) {
  const lines = useMemo(() => unifiedDiff(oldText, newText), [oldText, newText]);
  return (
    <pre className="m-0 min-w-0 overflow-x-auto py-1.5 font-mono text-xs leading-[18px]">
      {lines.map((l, i) => (
        <div
          key={i}
          className={cn(
            "flex min-w-0 px-2.5 whitespace-pre-wrap [overflow-wrap:anywhere]",
            l.kind === "add" && "bg-plus/12 text-ink",
            l.kind === "del" && "bg-minus/12 text-ink",
            l.kind === "eq" && "text-ink-3",
          )}
        >
          <span aria-hidden className={cn("w-4 shrink-0 select-none", l.kind === "add" ? "text-plus" : l.kind === "del" ? "text-minus" : "text-ink-4")}>
            {l.kind === "add" ? "+" : l.kind === "del" ? "−" : " "}
          </span>
          <span className="min-w-0 flex-1">{l.line || "\u00a0"}</span>
        </div>
      ))}
    </pre>
  );
}

function Pre({ text, error, cap, lang }: { text: string; error?: boolean; cap?: boolean; lang?: string }) {
  return (
    <pre
      className={cn(
        "m-0 min-w-0 py-1 pl-2.5 font-mono text-xs leading-[18px] whitespace-pre-wrap [overflow-wrap:anywhere]",
        error ? "text-err" : "text-ink-2",
        cap && "max-h-80 overflow-auto",
      )}
    >
      <Code text={text} lang={error ? undefined : lang} label />
    </pre>
  );
}

function ResultView({ r, session }: { r: Result; session: string }) {
  return <Clipped text={r.text} length={r.length} at={r.at} session={session} error={r.error} />;
}

/** What a step gave back, a subagent's folded when long: opened, its whole is brought with it, no
 *  key of its own beside the fold's (kumo, 2026-11-21). */
function SubResult({ r, session, fold }: { r: Result; session: string; fold: boolean }) {
  const [opened, setOpened] = useState(false);
  const lines = linesOf(r.text);
  if (!fold || lines <= FOLD_AT) return <ResultView r={r} session={session} />;
  return (
    <LongWords fold lines={lines} onOpen={() => setOpened(true)}>
      <Clipped text={r.text} length={r.length} at={r.at} session={session} error={r.error} asked={opened} />
    </LongWords>
  );
}

/** Text the page may have only the start of (length: the whole's), with a key to bring the whole.
 *  fields: an input, shown field by field; cap: inline in the conversation, scrolled past 20 lines.
 *  asked: in a fold whose opening brings the whole (false till then): no key of its own. */
function Clipped({ text, length, at, session, error, prose, fields, shell, cap, asked }: { text: string; length?: number; at: string; session: string; error?: boolean; prose?: boolean; fields?: boolean; shell?: boolean; cap?: boolean; asked?: boolean }) {
  // Asked for ahead (wholeAhead: its row pointed at or pressed): had as it opens, or once it comes.
  const [whole, setWhole] = useState<string | undefined>(() => wholes.get(wholeKey(session, at)));
  const [state, setState] = useState<"idle" | "busy" | "failed">("idle");
  const more = () => {
    setState("busy");
    wholeOf(session, at).then(
      (d) => (setWhole(d.text), setState("idle")),
      () => setState("failed"),
    );
  };
  const cut = length !== undefined && length > 0 && whole === undefined;
  useEffect(() => void (asked && cut && state === "idle" && more()), [asked]);
  const shown = whole ?? text;
  const cmd = shell ? (() => { const i = inputOf(shown); const v = i.command ?? i.cmd; return typeof v === "string" && v ? v : undefined; })() : undefined;
  return (
    <div className="grid min-w-0 gap-1">
      {prose ? <Markdown text={shown} className="text-sm text-ink-2" /> : cmd !== undefined ? <Pre text={cmd} cap={cap} lang="bash" /> : fields || shell ? <Fields text={shown} /> : <Pre text={pretty(shown)} error={error} cap={cap} lang="auto" />}
      {cut && asked === undefined && (
        <button type="button" onClick={more} disabled={state === "busy"} className="g-press justify-self-start rounded-sm text-xs text-ink-3 underline decoration-ink-4 underline-offset-2 hover:text-ink">
          {state === "busy" ? <LoaderCircle size={12} className="inline animate-spin" /> : t("msg.whole", { n: length })}
        </button>
      )}
      {state === "failed" && <span className="text-xs text-err">{t("err.whole")}</span>}
    </div>
  );
}

// A block's whole text, kept once read; coming: being read.
const wholes = new Map<string, string>();
const coming = new Map<string, Promise<{ text: string }>>();
const wholeKey = (session: string, at: string) => `${session} ${at}`;
function wholeOf(session: string, at: string): Promise<{ text: string }> {
  const k = wholeKey(session, at);
  const had = wholes.get(k);
  if (had !== undefined) return Promise.resolve({ text: had });
  let p = coming.get(k);
  if (!p) {
    p = api.block(session, at).then((d) => (wholes.set(k, d.text), d));
    coming.set(k, p);
    void p.catch(() => {}).finally(() => coming.delete(k));
  }
  return p;
}
/** What step s's card shows cut short (a long command, a long output), read whole ahead of the
 *  card: as its row is pointed at or pressed, so the card opens with it and is not drawn again
 *  (kumo, 2026-11-25: 一打开内容变了). Settles once they are had, or could not be. */
export function wholeAhead(session: string, s: Step): Promise<unknown> {
  const parts: string[] = [];
  if (s.length && s.at) parts.push(s.at);
  const r = s.kind === "tool" ? s.result : undefined;
  if (r?.length && r.at) parts.push(r.at);
  return Promise.all(parts.map((at) => wholeOf(session, at).catch(() => {})));
}

/** JSON as an object's lines; any other text as it is. */
function pretty(s: string) {
  const v = s.trim();
  if (!v.startsWith("{") && !v.startsWith("[")) return s;
  try {
    return JSON.stringify(JSON.parse(v), null, 2);
  } catch {
    return s;
  }
}

/** A call still running: a light sweeping across it, how long it has run. */
export function Running({ tool, cwd }: { tool: Tool; cwd: string }) {
  const [now, setNow] = useState(Date.now);
  useEffect(() => {
    const tick = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(tick);
  }, []);
  const a = argOf(tool, cwd);
  if (toldOf(tool)) return <SentTo call={tool} live />;
  const secs = tool.time ? Math.max(0, Math.round((now - tool.time) / 1000)) : null;
  return (
    <div className="flex h-7 min-w-0 items-center gap-2 text-sm" aria-live="off">
      <KindIcon kind={kindOf(tool.name)} cmd={cmdOfCall(tool)} shell={isShell(tool.name)} />
      {!isShell(tool.name) && <span className="shine shrink-0">{tool.name}</span>}
      <span className={cn("shine min-w-0 truncate", a.code ? "font-mono text-xs" : "text-xs")}>{a.arg}</span>
      {secs !== null && <span className="ml-auto shrink-0 text-xs text-ink-3 tabular-nums">{secs < 60 ? `${secs}s` : `${Math.floor(secs / 60)}m${secs % 60}s`}</span>}
    </div>
  );
}
