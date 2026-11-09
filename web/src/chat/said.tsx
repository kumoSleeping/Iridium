// The blocks of a chat by source (plan "消息来源", web/sample), QQ-like: each of the user's side with
// its sender's face, name and badge. Your words (from any of your panels or terminals) a bubble on
// the right, the terminal's avatar and its device; a letter on the right too (the user, 2026-11-06),
// for what another session's ir (its agent's logo, a bot badge, a key to go there), a plugin or a
// peer sent; Iridium's own lines
// centred and faint; the agent's words as Markdown with the files they name; tool calls and
// thoughts folded to a line.
import { Archive, ArchiveRestore, ArrowUpRight, BookOpen, Bot, BotMessageSquare, ChartColumn, Check, ChevronRight, CircleStop, CircleX, Clock, CloudDownload, CloudUpload, Copy, Cpu, Earth, FileCode, FileDiff, FilePen, Files as FilesIcon, FileText, FlaskConical, FolderGit2, FolderOpen, Folders, FoldVertical, GitBranch, GitCommitHorizontal, GitPullRequest, Globe, Hammer, History, Hourglass, KeyRound, Laptop, List, ListChecks, LoaderCircle, LockKeyhole, MessageCircleQuestionMark, MessageSquarePlus, Monitor, MonitorSmartphone, Network, Package, PencilLine, Plug, RotateCcw, RotateCw, Search, Send, ServerCog, Smartphone, SquareTerminal, Terminal, Trash2, type LucideIcon, UserRound, Workflow as WorkflowIcon, Wrench, X } from "lucide-react";
import { Children, forwardRef, memo, useEffect, useMemo, useRef, useState, type CSSProperties, type ReactNode } from "react";
import { Popover } from "@base-ui/react/popover";
import { api } from "../api/client";
import type { Block, FileRef, Result } from "../api/types.gen";
import type { Sent } from "../chats";
import { t } from "../i18n";
import { Avatar, Badge, cn, IconKey, Logo, tilde, Tip } from "../ui";
import { useHome } from "../data";
import { LocalMark } from "../me";
import { go } from "../route";
import { Files, Picture, previews, SentFile } from "./files";
import { Pictures } from "./pictures";
import { cmdOfCall, isShell, type Cmd } from "./cmd";
import { argOf, diffSum, foldLine, foldSaid, kindOf, runCmds, type Diff, type Kind, type Shown, type Step, type Tool } from "./fold";
import { Code } from "./hl";
import { Markdown } from "./markdown";
import { partialInput } from "./partial";
import { IdsNamed, MentionCard, Mentioned } from "./mention";
import { leadingMentions, type Source, withoutFiles, withoutNameLines } from "./refs";

type You = Extract<Source, { kind: "you" }>;
type From = Extract<Source, { kind: "letter" }>;

/** One formatter per language: making one per call is slow, and a chat calls it for every block. */
const clocks = new Map<string, Intl.DateTimeFormat>();
const clock = (ms?: number) => {
  if (!ms) return "";
  const l = document.documentElement.lang === "en" ? "en" : "zh-CN";
  let f = clocks.get(l);
  if (!f) clocks.set(l, (f = new Intl.DateTimeFormat(l, { hour: "2-digit", minute: "2-digit" })));
  return f.format(ms);
};

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
  if (who.agent)
    return (
      <span className="grid shrink-0 place-items-center" style={{ width: FACE, height: FACE }} aria-label={t(who.workflow ? "msg.workflow" : "msg.agent")}>
        <Logo agent={who.agent} size={24} />
      </span>
    );
  if (!who.channel)
    // A session not in the list: a terminal-like mark and the bot.
    return (
      <span className="relative grid shrink-0 place-items-center rounded-full bg-fill-2 text-ink-2" style={{ width: FACE, height: FACE }}>
        <SquareTerminal size={14} strokeWidth={2} aria-hidden />
        {badge}
      </span>
    );
  return <Avatar src={who.avatar} name={who.name} size={FACE} channel badgeSize={BADGE} />;
}

/** One message of the user's side, QQ-like: the face beside it, the name above, yours on the right.
 *  jump: a key to the sending session, on the face and the name. Your own face opens the settings
 *  (the user, 2026-11-08). */
const Sender = forwardRef<HTMLDivElement, { who: You | From; title?: string; jump?: () => void; children: ReactNode }>(function Sender({ who, title, jump, children }, ref) {
  const mine = who.kind === "you";
  const name = mine ? youName(who) : who.name;
  // A session's letter: its folder too, in the tip; the name is its title alone.
  const whole = !mine && who.where ? `${name} · ${tilde(who.where)}` : name;
  const tip = mine ? t("msg.fromPanel", { name: deviceOf(who) ? `${name} · ${t(deviceOf(who)![1])}` : name }) : jump ? t("msg.jump", { name: whole }) : who.channel ? t("msg.viaChannel", { name }) : whole;
  const face = <Face who={who} />;
  const press = jump ?? (mine ? () => go({ name: "settings" }) : undefined);
  // Every sender on the right, a letter as your words are: on the left it read as the agent's own
  // (the user, 2026-11-06).
  return (
    <div ref={ref} className="flex flex-row-reverse items-start gap-2.5 pl-6 sm:pl-10" title={title}>
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
      <div className="flex min-w-0 flex-col items-end gap-1">
        {jump ? (
          <button type="button" onClick={jump} className="group flex max-w-full items-center gap-0.5 rounded-sm px-0.5 text-xs leading-4 text-ink-3 hover:text-ink-2">
            <span className="min-w-0 truncate">{name}</span>
            <ArrowUpRight size={11} className="shrink-0 text-ink-4 group-hover:text-ink-2" />
          </button>
        ) : (
          <span className="max-w-full truncate px-0.5 text-xs leading-4 text-ink-3">{name}</span>
        )}
        {children}
      </div>
    </div>
  );
});

/** Your words: a bubble on the right, under your terminal's name, its face beside. */
/** Where a message of yours is, as a small mark outside its bubble's lower left corner, no words (the
 *  user, 2026-11-06; its tip says): going, a ring breathing; waiting in the queue, a clock. Taken,
 *  nothing: no green check (the user, 2026-11-08). The bubble itself is drawn as it will stay. */
type Going = "sending" | "steering" | "queued";
const GOING: Record<Going, string> = { sending: "msg.sending", steering: "msg.steering", queued: "msg.queued" };
function SendState({ state }: { state: Going }) {
  const label = t(GOING[state] as "msg.sending");
  return (
    <span role="status" aria-label={label} title={label} className="absolute bottom-2 -left-[18px] grid size-3 place-items-center text-ink-3">
      {state === "queued" ? <Clock size={10} strokeWidth={2.25} className="g-fade" /> : <span className="send-ring" />}
    </span>
  );
}

export const YouBubble = forwardRef<HTMLDivElement, { text: string; who: You; failed?: boolean; children?: ReactNode; time?: number; going?: Going }>(function YouBubble({ text: all, who, failed, children, time, going }, ref) {
  // The sessions named before the words: cards with the files, as they were in the composer.
  const { named, words: text } = useMemo(() => leadingMentions(all), [all]);
  const row = named.length + Children.count(children) > 0;
  return (
    <Sender ref={ref} who={who} title={time ? clock(time) : undefined}>
      <div className="relative max-w-[min(560px,100%)] px-3.5 py-2">
        {going && <SendState state={going} />}
        <span data-bubble-bg className={cn("absolute inset-0 rounded-xl rounded-tr-sm bg-fill", failed && "shadow-[inset_0_0_0_1px_var(--err)]")} aria-hidden />
        {text && <span className="relative block text-md whitespace-pre-wrap [overflow-wrap:anywhere]"><Mentioned text={text} /></span>}
        {/* Not for no files: a message sent and the same one landed are as high (an empty list was
            a row of its own, the bubble shrinking as it landed). */}
        {row && (
          <span className={cn("relative flex flex-wrap gap-1.5", text && "mt-1")}>
            {named.map((m, i) => (
              <MentionCard key={i} m={m} />
            ))}
            {children}
          </span>
        )}
      </div>
    </Sender>
  );
});

/** A file in your words, as it was in the composer: a picture small, opening the original; any
 *  other its card. */
function Attached({ session, f }: { session: string; f: FileRef }) {
  const url = api.fileURL(session, f.ref, false, f.mtime);
  if (f.kind === "image") return <Picture src={f.thumb ? api.thumbURL(session, f.ref, 256, f.mtime) : url} full={url} name={f.name} small under={previews.get(f.path)} w={f.w} h={f.h} />;
  return <SentFile name={f.name} bytes={f.size} href={api.fileURL(session, f.ref, true, f.mtime)} />;
}

/** A message sent from here the transcript has yet to bring: its mark says where it is; not taken,
 *  red with a retry. */
export function Pending({ s, who, retry, drop, bubbleRef }: { s: Sent; who: You; retry: () => Promise<void>; drop: () => void; bubbleRef?: (el: HTMLDivElement | null) => void }) {
  const [busy, setBusy] = useState(false);
  return (
    <div className="flex flex-col items-end gap-0.5">
      <YouBubble ref={bubbleRef} text={s.text} who={who} failed={s.failed !== undefined} going={s.failed !== undefined ? undefined : s.queued ? "queued" : s.steer ? "steering" : "sending"}>
        {s.files.map((f) =>
          f.url ? <Picture key={f.ref} src={f.url} full={f.url} name={f.name} small /> : <SentFile key={f.ref} name={f.name} bytes={f.size} />,
        )}
      </YouBubble>
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

/** A letter: from another session's ir, a plugin or a peer. Not your words: on the left, in a grey
 *  bubble as yours are (the user, 2026-11-06). */
function Letter({ src, text, time, onJump, children }: { src: From; text: string; time?: number; onJump: (id: string) => void; children?: ReactNode }) {
  // Its files as in your bubble (ir send -a): they were not shown at all.
  const files = Children.count(children) > 0;
  return (
    <Sender who={src} title={time ? clock(time) : undefined} jump={src.session ? () => onJump(src.session!) : undefined}>
      <div className="max-w-[min(560px,100%)] min-w-0 rounded-xl rounded-tr-sm bg-fill px-3.5 py-2">
        {text && <p className="m-0 text-md whitespace-pre-wrap [overflow-wrap:anywhere]"><Mentioned text={text} /></p>}
        {files && <div className={cn("flex flex-wrap gap-1.5", text && "mt-1")}>{children}</div>}
      </div>
    </Sender>
  );
}

/** A quiet line across the chat: Iridium's own words, a compaction, an interrupted turn. */
function Line({ icon, children, error, title }: { icon?: ReactNode; children: ReactNode; error?: boolean; title?: string }) {
  return (
    <div className={cn("flex items-center justify-center gap-1.5 py-1 text-xs", error ? "text-err" : "text-ink-3")} title={title}>
      {icon}
      <span className="min-w-0 truncate">{children}</span>
    </div>
  );
}

const tok = (n: number) => (n >= 1_000_000 ? `${(n / 1_000_000).toFixed(1)}M` : n >= 1000 ? `${Math.round(n / 1000)}k` : `${n}`);
const took = (ms: number) => (ms >= 60_000 ? t("sys.compactMin", { m: Math.floor(ms / 60_000), s: Math.round((ms % 60_000) / 1000) }) : t("sys.compactSec", { s: Math.max(1, Math.round(ms / 1000)) }));

/** A compaction, as a rule across the chat: the light and the words between two hairlines. Live,
 *  while it runs: a yellow light breathing and the seconds so far; done, the context before and
 *  after it, how long it took and when (the user, 2026-11-06: a message of its own, not your
 *  /compact, not the dots). The same height either way, the one taking the other's place. */
export function Compaction({ b, since }: { b?: Block; since?: number }) {
  const [now, setNow] = useState(() => Date.now());
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
  return (
    <div className="g-fade flex items-center gap-3 py-1 text-xs text-ink-3" role={b ? undefined : "status"} title={b?.text ? b.text.slice(0, 600) : undefined}>
      <span aria-hidden className="h-px flex-1 bg-edge" />
      <span className="flex min-w-0 items-center gap-2">
        {b ? <FoldVertical size={13} className="shrink-0 text-ink-4" /> : <span aria-hidden className="row-lamp shrink-0" data-s="compacting" />}
        <span className="truncate tabular-nums">{parts.join(" · ")}</span>
      </span>
      <span aria-hidden className="h-px flex-1 bg-edge" />
    </div>
  );
}

const SYSTEM: Record<string, Parameters<typeof t>[0]> = { compact: "sys.compact", interrupted: "sys.interrupted", unreadable: "sys.unreadable", api_error: "sys.apiError", local_command: "sys.localCommand" };

/** What the agent said: Markdown, then the files it gives ([文件] `path` lines). */
const Assistant = memo(function Assistant({ b, session }: { b: Block; session: string }) {
  const home = useHome().data;
  const text = withoutFiles(withoutNameLines(b.text ?? ""), new Set(b.files?.map((f) => f.path)), home);
  return (
    <div className="flex min-w-0 flex-col items-start gap-2 pr-6">
      {text && <Markdown text={text} className="w-full" />}
      {!!b.files?.length && <Files session={session} files={b.files} />}
    </div>
  );
});

export function Said({ b, session, cwd, source, onJump }: { b: Shown; session: string; cwd: string; source: (b: { from?: string; fromTerminal?: string }) => Source; onJump: (id: string) => void }) {
  switch (b.kind) {
    case "user": {
      const src = source(b);
      const text = b.text ?? "";
      if (src.kind === "iridium") return <Line icon={<RotateCcw size={12} />}>{text}</Line>;
      // The server writes each attachment as a line "[附件] `path`" (absolute): the file shows as itself instead.
      const paths = new Set(b.files?.map((f) => f.path));
      const words = paths.size ? withoutFiles(text, paths) : text;
      if (src.kind === "letter")
        return (
          <Letter src={src} text={words} time={b.time} onJump={onJump}>
            {b.files?.map((f) => <Attached key={f.ref} session={session} f={f} />)}
          </Letter>
        );
      return (
        <YouBubble text={words} who={src} time={b.time} >
          {b.files?.map((f) => <Attached key={f.ref} session={session} f={f} />)}
        </YouBubble>
      );
    }
    case "assistant":
      return <Assistant b={b} session={session} />;
    case "system":
      if (b.event === "iridium") return <Line icon={<RotateCcw size={12} />} title={b.text}>{b.text}</Line>;
      if (b.event === "compact") return <Compaction b={b} />;
      // A background task ended (a workflow, a background agent): its summary as Claude Code says it.
      if (b.event === "task")
        return (
          <Line icon={<WorkflowIcon size={12} />} title={b.text}>
            <span className="max-w-[min(560px,100%)] truncate">{b.text}</span>
            {b.time ? ` · ${clock(b.time)}` : ""}
          </Line>
        );
      return b.event === "api_error" || b.event === "local_command" || b.event === "unreadable" ? (
        <SystemFold b={b} session={session} />
      ) : (
        <Line title={b.text}>
          {t(SYSTEM[b.event ?? ""] ?? "sys.other")}
          {b.time ? ` · ${clock(b.time)}` : ""}
        </Line>
      );
    case "fold":
      // A step auto mode denied waits above the composer, not here (chat/asks.tsx DenialCard).
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
const DiffStat = ({ d }: { d: Diff }) => (
  <span className="shrink-0 font-mono text-xs tabular-nums">
    {d.added > 0 && <span className="text-plus">+{d.added}</span>}
    {d.added > 0 && d.taken > 0 && " "}
    {d.taken > 0 && <span className="text-minus">−{d.taken}</span>}
  </span>
);
const failed = (s: Step) => s.kind === "tool" && !!s.result?.error;

/** A step's icon: its kind's, a shell command's sort's, red if it failed. */
const StepIcon = ({ s, className }: { s: Step; className?: string }) =>
  s.kind === "thinking" ? <KindIcon kind="thinking" className={className} /> : <KindIcon kind={kindOf(s.name)} cmd={cmdOfCall(s)} shell={isShell(s.name)} failed={failed(s)} className={className} />;

/** A step's line: its icon (bare: none, its row has it), its name, what it is about in monospace,
 *  an edit's +/−. */
function StepLine({ s, cwd, bare }: { s: Step; cwd?: string; bare?: boolean }) {
  if (s.kind === "thinking")
    return (
      <>
        {!bare && <StepIcon s={s} />}
        <span className="shrink-0 text-ink-2">{t("msg.thinking")}</span>
        <span className="min-w-0 truncate text-xs">{(s.text ?? "").replace(/[*_`#>]+/g, "").replace(/\s+/g, " ")}</span>
      </>
    );
  const a = argOf(s, cwd);
  return (
    <>
      {!bare && <StepIcon s={s} />}
      {!isShell(s.name) && <span className="shrink-0 text-ink-2">{s.name}</span>}
      <span className={cn("min-w-0 truncate", a.code ? "font-mono text-xs" : "text-xs")}>{a.arg}</span>
      {a.diff && !failed(s) && <DiffStat d={a.diff} />}
      {failed(s) && <CircleX size={13} className="shrink-0 text-err" aria-label={t("msg.failed")} />}
    </>
  );
}

const ROW = "flex h-7 min-w-0 cursor-default list-none items-center gap-2 rounded-sm text-sm text-ink-3 hover:text-ink-2 [&::-webkit-details-marker]:hidden pointer-coarse:h-9";

/** The steps between two things said: one of them is its own line; more fold into one, each name
 *  once with its kind's icon (a shell's, one per call when they do different things) and ×n, then
 *  the lines their edits changed, which opens to a line each.
 *  Each, pressed, shows what it was given and what came back in a card (StepView). */
function Fold({ steps, session, cwd }: { steps: Step[]; session: string; cwd: string }) {
  if (steps.length === 1) return <StepView s={steps[0]!} session={session} cwd={cwd} />;
  const runs = foldLine(steps);
  const diff = diffSum(runs);
  const bad = steps.filter(failed).length;
  return (
    <details className="group/fold -my-0.5 w-full min-w-0">
      <summary className={ROW} aria-label={foldSaid(runs)}>
        <span className="min-w-0 truncate">
          {runs.map((r, i) => (
            <span key={i} className={cn(i > 0 && "ml-3")}>
              {(r.name ? runCmds(steps, r.name) : [{ cmd: undefined, n: 1 }]).map((g, j, gs) => (
                <span key={j} className={cn("inline-block", j < gs.length - 1 ? "mr-1.5" : !isShell(r.name) && "mr-2")}>
                  <KindIcon kind={r.name ? kindOf(r.name) : "thinking"} cmd={g.cmd} shell={isShell(r.name)} className="inline-block align-[-2.5px]" />
                  {(gs.length > 1 || isShell(r.name)) && g.n > 1 && <span className="text-xs tabular-nums">×{g.n}</span>}
                </span>
              ))}
              {/* A shell's run is its icons alone, each with how many (the user, 2026-11-09): no
                  room after the last for a name, only the gap between runs. */}
              {!isShell(r.name) && <span className="text-ink-2">{r.name || t("msg.thinking")}</span>}
              {!isShell(r.name) && r.n > 1 && <span className="tabular-nums"> ×{r.n}</span>}
            </span>
          ))}
        </span>
        {diff && <DiffStat d={diff} />}
        {bad > 0 && (
          <span className="flex shrink-0 items-center gap-0.5 text-xs text-err tabular-nums">
            <CircleX size={13} aria-label={t("msg.failed")} />
            {bad > 1 && bad}
          </span>
        )}
        <ChevronRight size={13} className="fold-chev shrink-0 text-ink-4 transition-transform duration-(--dur-2) group-open/fold:rotate-90" />
      </summary>
      <div className="mt-0.5 ml-[7px] flex flex-col pl-3 shadow-[inset_1px_0_0_var(--edge)]">
        {steps.map((s) => (
          <StepView key={s.at} s={s} session={session} cwd={cwd} />
        ))}
      </div>
    </details>
  );
}

/** A step's line; pressed, what it was given and what came back open in a card at the press, like a
 *  context menu, instead of under the line (the user, 2026-11-08): the conversation stays put.
 *  The pictures its result gave the model show under it, open at first; its icon, hovered, turns
 *  to the chevron that folds them (the user, 2026-11-09: the tool's own icon otherwise). */
function StepView({ s, session, cwd }: { s: Step; session: string; cwd: string }) {
  const images = s.kind === "tool" ? s.result?.images : undefined;
  const [shown, setShown] = useState(true);
  if (!images?.length) return <StepCall s={s} session={session} cwd={cwd} />;
  return (
    <div className="min-w-0">
      <div className="group/step flex min-w-0 items-center gap-2">
        {/* Touch has no hover: the chevron shows always, its key as wide as a finger, the line
            where it was. */}
        <button
          type="button"
          aria-expanded={shown}
          aria-label={t(shown ? "msg.hidePictures" : "msg.showPictures")}
          onClick={() => setShown(!shown)}
          className="group/pic grid h-7 shrink-0 place-items-center rounded-sm outline-none hover:bg-fill focus-visible:bg-fill pointer-coarse:-mx-3 pointer-coarse:h-9 pointer-coarse:px-3 [&>*]:[grid-area:1/1] [&>*]:transition-[opacity,rotate] [&>*]:duration-(--dur-1)"
        >
          <StepIcon s={s} className="group-hover/step:opacity-0 group-focus-visible/pic:opacity-0 pointer-coarse:opacity-0" />
          <ChevronRight size={14} aria-hidden className={cn("text-ink-3 opacity-0 group-hover/step:opacity-100 group-focus-visible/pic:opacity-100 pointer-coarse:opacity-100", shown && "rotate-90")} />
        </button>
        <StepCall s={s} session={session} cwd={cwd} bare />
      </div>
      {shown && <Pictures session={session} s={s} />}
    </div>
  );
}

/** A step's line as a key to its card (StepView). bare: without its icon, which its row has. */
function StepCall({ s, session, cwd, bare }: { s: Step; session: string; cwd: string; bare?: boolean }) {
  // Where it was pressed stays as the card goes: put away, it folds back into that point, not
  // first thrown to the window's corner by an anchor gone (the user, 2026-11-08).
  const [open, setOpen] = useState(false);
  const [at, setAt] = useState<{ x: number; y: number } | null>(null);
  const row = useRef<HTMLButtonElement>(null);
  const toggle = (o: boolean, d: { event?: Event }) => {
    setOpen(o);
    if (!o) return;
    const r = row.current?.getBoundingClientRect();
    const e = d.event as MouseEvent | undefined;
    const pointed = e && "clientX" in e && e.detail > 0;
    setAt({ x: pointed ? e.clientX : (r?.left ?? 0) + 22, y: r?.bottom ?? (pointed ? e.clientY : 0) });
  };
  const anchor = useMemo(() => at && { getBoundingClientRect: () => DOMRect.fromRect({ x: at.x, y: at.y, width: 0, height: 0 }) }, [at]);
  return (
    <Popover.Root open={open} onOpenChange={toggle}>
      <Popover.Trigger ref={row} className={cn(ROW, "w-full text-left outline-none focus-visible:text-ink-2", open && "text-ink-2")}>
        <StepLine s={s} cwd={cwd} bare={bare} />
      </Popover.Trigger>
      <Popover.Portal>
        <Popover.Positioner anchor={anchor} side="bottom" align="start" sideOffset={4} collisionPadding={8} className="z-50">
          <Popover.Popup initialFocus={(type) => type === "keyboard"} className="g-unfold glass thick float flex max-h-[min(72dvh,620px,var(--available-height))] w-[min(620px,calc(100vw-16px))] flex-col overflow-hidden rounded-xl text-ink outline-none">
            <StepCard s={s} session={session} />
          </Popover.Popup>
        </Popover.Positioner>
      </Popover.Portal>
    </Popover.Root>
  );
}

/** The card: no head (its line is right above it, a press elsewhere puts it away; the user,
 *  2026-11-08), only its input field by field and its output, each with a key to copy it; a
 *  thought its words. */
function StepCard({ s, session }: { s: Step; session: string }) {
  return (
    <>
      <div className="grid min-h-0 gap-4 overflow-y-auto overscroll-contain px-3 pt-3 pb-3.5">
        {s.kind === "thinking" ? (
          <Clipped text={s.text ?? ""} length={s.length} at={s.at} session={session} prose />
        ) : (
          <>
            <Part label={t("msg.input")} copy={s.input}>
              <Clipped text={s.input ?? ""} length={s.length} at={s.at} session={session} fields />
            </Part>
            <Part label={t("msg.output")} copy={s.result?.text} error={s.result?.error}>
              {s.result ? <ResultView r={s.result} session={session} /> : <span className="text-xs text-ink-3">{t("msg.noResult")}</span>}
            </Part>
            {/* The sessions it names by id, each a card going there (ir send, ir wait, ir new); not
                a list's many (ir ls): the sidebar has those. */}
            <IdsNamed text={s.input ?? ""} also={s.result?.text} />
          </>
        )}
      </div>
    </>
  );
}

/** One part of the card: its small label, a key to copy it, its body. */
function Part({ label, copy, error, children }: { label: string; copy?: string; error?: boolean; children: ReactNode }) {
  const [copied, setCopied] = useState(false);
  return (
    <section className="grid min-w-0 gap-1.5">
      <div className="flex h-5 items-center gap-2">
        <span className={cn("text-[11px] font-medium tracking-wide text-ink-4 uppercase", error && "text-err")}>{label}</span>
        {error && <CircleX size={12} className="text-err" aria-label={t("msg.failed")} />}
        <span className="flex-1" />
        {!!copy && (
          <button type="button" onClick={() => void navigator.clipboard.writeText(copy).then(() => (setCopied(true), setTimeout(() => setCopied(false), 1500)))} className="g-press flex h-5 items-center gap-1 rounded-sm px-1 text-[11px] text-ink-4 hover:text-ink-2">
            {copied ? <Check size={12} /> : <Copy size={12} />}
            {copied ? t("msg.copied") : t("msg.copy")}
          </button>
        )}
      </div>
      {children}
    </section>
  );
}

/** An input object's fields, each its name and its value: a short one on the line, a long one or
 *  one of many lines in a block; an edit's old text red, its new text green. A command in a shell's
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
  const rows = Object.entries(v as Record<string, unknown>);
  const file = rows.find(([k, x]) => FILE.has(k) && typeof x === "string")?.[1] as string | undefined;
  if (!rows.length) return <span className="text-xs text-ink-4">{"{}"}</span>;
  return (
    <div className="grid min-w-0 gap-2">
      {rows.map(([k, x]) => {
        const str = typeof x === "string" ? x : JSON.stringify(x, null, 2);
        const tone = OLD.has(k) ? "minus" : NEW.has(k) ? "plus" : undefined;
        const inline = !tone && !SHELL.has(k) && !str.includes("\n") && str.length <= 80;
        const lang = typeof x !== "string" ? undefined : SHELL.has(k) ? "bash" : OLD.has(k) || NEW.has(k) ? file : undefined;
        return inline ? (
          <div key={k} className="flex min-w-0 items-baseline gap-2 text-xs">
            <span className="shrink-0 font-mono text-ink-4">{k}</span>
            <span className={cn("min-w-0 font-mono [overflow-wrap:anywhere]", typeof x === "string" ? "text-ink-2" : "text-ink-3")}>{str}</span>
          </div>
        ) : (
          <div key={k} className="grid min-w-0 gap-1">
            <span className="font-mono text-xs text-ink-4">{k}</span>
            <Pre text={str} tone={tone} lang={lang} />
          </div>
        );
      })}
    </div>
  );
}
const OLD = new Set(["old_string", "old_str", "oldText"]);
const NEW = new Set(["new_string", "new_str", "newText", "content"]);
const SHELL = new Set(["command", "cmd", "script"]);
const FILE = new Set(["file_path", "path", "filePath", "notebook_path"]);

function Pre({ text, error, tone, cap, lang }: { text: string; error?: boolean; tone?: "plus" | "minus"; cap?: boolean; lang?: string }) {
  return (
    <pre
      className={cn(
        "m-0 min-w-0 rounded-md bg-fill px-2.5 py-2 font-mono text-xs leading-[18px] whitespace-pre-wrap [overflow-wrap:anywhere]",
        error ? "text-err" : "text-ink-2",
        cap && "max-h-80 overflow-auto",
        tone === "plus" && "shadow-[inset_2px_0_0_var(--plus)]",
        tone === "minus" && "shadow-[inset_2px_0_0_var(--minus)]",
      )}
    >
      <Code text={text} lang={error ? undefined : lang} label />
    </pre>
  );
}

function ResultView({ r, session }: { r: Result; session: string }) {
  return <Clipped text={r.text} length={r.length} at={r.at} session={session} error={r.error} />;
}

/** Text the page may have only the start of (length: the whole's), with a key to bring the whole.
 *  fields: an input, shown field by field; cap: inline in the conversation, scrolled past 20 lines. */
function Clipped({ text, length, at, session, error, prose, fields, cap }: { text: string; length?: number; at: string; session: string; error?: boolean; prose?: boolean; fields?: boolean; cap?: boolean }) {
  const [whole, setWhole] = useState<string>();
  const [state, setState] = useState<"idle" | "busy" | "failed">("idle");
  const more = () => {
    setState("busy");
    api.block(session, at).then(
      (d) => (setWhole(d.text), setState("idle")),
      () => setState("failed"),
    );
  };
  const shown = whole ?? text;
  return (
    <div className="grid min-w-0 gap-1">
      {prose ? <Markdown text={shown} className="text-sm text-ink-2" /> : fields ? <Fields text={shown} /> : <Pre text={pretty(shown)} error={error} cap={cap} lang="auto" />}
      {length !== undefined && length > 0 && whole === undefined && (
        <button type="button" onClick={more} disabled={state === "busy"} className="g-press justify-self-start rounded-sm text-xs text-ink-3 underline decoration-ink-4 underline-offset-2 hover:text-ink">
          {state === "busy" ? <LoaderCircle size={12} className="inline animate-spin" /> : t("msg.whole", { n: length })}
        </button>
      )}
      {state === "failed" && <span className="text-xs text-err">{t("err.whole")}</span>}
    </div>
  );
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
