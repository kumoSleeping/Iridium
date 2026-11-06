// The blocks of a chat by source (plan "消息来源", web/sample), QQ-like: each of the user's side with
// its sender's face, name and badge. Your words (from any of your panels or terminals) a bubble on
// the right, the terminal's avatar and its device; a letter on the left for what another session's
// ir (its agent's logo, a bot badge, a key to go there), a plugin or a peer sent; Iridium's own lines
// centred and faint; the agent's words as Markdown with the files they name, in a bubble as yours
// are, on the left, its logo beside (the user, 2026-11-06); tool calls and thoughts folded to a line.
import { Archive, ArchiveRestore, ArrowUpRight, BookOpen, Bot, Brain, Check, ChevronRight, Clock, CircleStop, CircleX, CloudDownload, CloudUpload, Earth, FileCode, FileDiff, FilePen, Files as FilesIcon, FileText, FolderGit2, FlaskConical, FolderOpen, GitBranch, GitCommitHorizontal, GitPullRequest, Globe, Hammer, History, Hourglass, KeyRound, List, ListChecks, LoaderCircle, MessageCircleQuestionMark, MessageSquarePlus, Monitor, Network, Package, Plug, RotateCcw, RotateCw, Search, Send, ServerCog, Smartphone, SquareTerminal, Trash2, UserRound, Workflow as WorkflowIcon, Wrench, X, type LucideIcon } from "lucide-react";
import { Children, forwardRef, memo, useEffect, useState, type CSSProperties, type ReactNode } from "react";
import { api } from "../api/client";
import type { Agent, Block, FileRef, Result } from "../api/types.gen";
import { landed, type Sent } from "../chats";
import { t } from "../i18n";
import { agentName, Avatar, Badge, cn, IconKey, Logo, Tip } from "../ui";
import { useHome } from "../data";
import { LocalMark } from "../me";
import { Files, Picture, previews, size } from "./files";
import { cmdOfCall, type Cmd } from "./cmd";
import { argOf, diffSum, foldLine, foldSaid, kindOf, runCmd, type Diff, type Kind, type Shown, type Step, type Tool } from "./fold";
import { Markdown } from "./markdown";
import { filesIn, type Source } from "./refs";

type You = Extract<Source, { kind: "you" }>;
type From = Extract<Source, { kind: "letter" }>;

const clock = (ms?: number) => (ms ? new Date(ms).toLocaleTimeString(document.documentElement.lang === "en" ? "en" : "zh-CN", { hour: "2-digit", minute: "2-digit" }) : "");

const FACE = 28;
const BADGE = 12;
const DEVICES: Record<You["device"], [LucideIcon, Parameters<typeof t>[0]]> = { computer: [Monitor, "device.computer"], phone: [Smartphone, "device.phone"] };
const youName = (who: You) => (who.local ? t("me.local") : who.name);

/** A sender's face: a terminal's avatar (本机 its mark) with its device on the corner; a session's
 *  agent logo on a disc with a bot (a workflow's mark when it is a workflow's prompt); a plugin's or
 *  peer's avatar with its plug. */
function Face({ who }: { who: You | From }) {
  if (who.kind === "you") {
    const [icon, label] = DEVICES[who.device] ?? DEVICES.computer;
    if (!who.local) return <Avatar src={who.avatar} name={who.name} size={FACE} badge={icon} badgeSize={BADGE} badgeLabel={t(label)} />;
    return (
      <span className="relative inline-block shrink-0" style={{ width: FACE, height: FACE }}>
        <LocalMark size={FACE} />
        <Badge icon={icon} size={BADGE} label={t(label)} />
      </span>
    );
  }
  const badge = who.workflow ? <Badge icon={WorkflowIcon} size={BADGE} label={t("msg.workflow")} /> : <Badge icon={Bot} size={BADGE} label={t("msg.agent")} />;
  if (who.agent)
    return (
      <span className="relative grid shrink-0 place-items-center rounded-full bg-fill-2" style={{ width: FACE, height: FACE }}>
        <Logo agent={who.agent} size={16} />
        {badge}
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
 *  jump: a key to the sending session, on the face and the name. */
const Sender = forwardRef<HTMLDivElement, { who: You | From; title?: string; jump?: () => void; children: ReactNode }>(function Sender({ who, title, jump, children }, ref) {
  const mine = who.kind === "you";
  const name = mine ? youName(who) : who.name;
  const tip = mine ? t("msg.fromPanel", { name: `${name} · ${t((DEVICES[who.device] ?? DEVICES.computer)[1])}` }) : jump ? t("msg.jump", { name }) : who.channel ? t("msg.viaChannel", { name }) : name;
  const face = <Face who={who} />;
  return (
    <div ref={ref} className={cn("flex items-start gap-2.5", mine ? "flex-row-reverse pl-6 sm:pl-10" : "pr-6 sm:pr-10")} title={title}>
      <Tip label={tip}>
        {jump ? (
          <button type="button" onClick={jump} aria-label={tip} className="g-press grid shrink-0 rounded-full">
            {face}
          </button>
        ) : (
          <span className="grid shrink-0" aria-label={tip}>
            {face}
          </span>
        )}
      </Tip>
      <div className={cn("flex min-w-0 flex-col gap-1", mine ? "items-end" : "items-start")}>
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
 *  user, 2026-11-06; its tip says): going, a ring breathing; waiting in the queue, a clock; just
 *  taken, a green check that fades. The bubble itself is drawn as it will stay, not faint. */
type Going = "sending" | "steering" | "queued" | "landed";
const GOING: Record<Going, string> = { sending: "msg.sending", steering: "msg.steering", queued: "msg.queued", landed: "msg.landed" };
function SendState({ state }: { state: Going }) {
  const label = t(GOING[state] as "msg.sending");
  return (
    <span role="status" aria-label={label} title={label} className="absolute bottom-2 -left-[18px] grid size-3 place-items-center text-ink-3">
      {state === "queued" ? <Clock size={10} strokeWidth={2.25} className="g-fade" /> : state === "landed" ? <Check size={11} strokeWidth={2.5} className="sent-check text-run" /> : <span className="send-ring" />}
    </span>
  );
}
/** How long after it landed a message still shows its check. */
const CHECK_FOR = 1600;

export const YouBubble = forwardRef<HTMLDivElement, { text: string; who: You; failed?: boolean; children?: ReactNode; time?: number; going?: Going }>(function YouBubble({ text, who, failed, children, time, going }, ref) {
  return (
    <Sender ref={ref} who={who} title={time ? clock(time) : undefined}>
      <div className="relative max-w-[min(560px,100%)] px-3.5 py-2">
        {going && <SendState state={going} />}
        <span data-bubble-bg className={cn("absolute inset-0 rounded-xl rounded-tr-sm bg-fill", failed && "shadow-[inset_0_0_0_1px_var(--err)]")} aria-hidden />
        {text && <span className="relative block text-md whitespace-pre-wrap [overflow-wrap:anywhere]">{text}</span>}
        {/* Not for no files: a message sent and the same one landed are as high (an empty list was
            a row of its own, the bubble shrinking as it landed). */}
        {Children.count(children) > 0 && <span className="relative mt-1 flex flex-wrap gap-1.5">{children}</span>}
      </div>
    </Sender>
  );
});

/** A file in your words: a picture small, opening the original; any other its name and size. */
function Attached({ session, f }: { session: string; f: FileRef }) {
  if (f.kind === "image") return <Picture src={f.thumb ? api.thumbURL(session, f.ref, 256) : api.fileURL(session, f.ref)} full={api.fileURL(session, f.ref)} name={f.name} small under={previews.get(f.path)} />;
  return (
    <a href={api.fileURL(session, f.ref, true)} download={f.name} className="flex max-w-full items-center gap-1 rounded-md bg-paper/60 px-1.5 py-0.5 text-xs text-ink-2 hover:text-ink">
      <FileText size={12} className="shrink-0" />
      <span className="truncate">{f.name}</span>
      <span className="shrink-0 text-ink-3 tabular-nums">{size(f.size)}</span>
    </a>
  );
}

/** A message sent from here the transcript has yet to bring: its mark says where it is; not taken,
 *  red with a retry. */
export function Pending({ s, who, retry, drop, bubbleRef }: { s: Sent; who: You; retry: () => Promise<void>; drop: () => void; bubbleRef?: (el: HTMLDivElement | null) => void }) {
  const [busy, setBusy] = useState(false);
  return (
    <div className="flex flex-col items-end gap-0.5">
      <YouBubble ref={bubbleRef} text={s.text} who={who} failed={s.failed !== undefined} going={s.failed !== undefined ? undefined : s.queued ? "queued" : s.steer ? "steering" : "sending"}>
        {s.files.map((f) =>
          f.url ? (
            <Picture key={f.ref} src={f.url} full={f.url} name={f.name} small />
          ) : (
          <span key={f.ref} className="flex items-center gap-1 rounded-md bg-paper/60 px-1.5 py-0.5 text-xs text-ink-2">
            <FileText size={12} />
            {f.name}
          </span>
          ),
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

/** A letter: from another session's ir, a plugin or a peer. Not your words: on the left, in a card. */
function Letter({ src, text, time, onJump }: { src: From; text: string; time?: number; onJump: (id: string) => void }) {
  return (
    <Sender who={src} title={time ? clock(time) : undefined} jump={src.session ? () => onJump(src.session!) : undefined}>
      <p className="m-0 max-w-[min(560px,100%)] min-w-0 rounded-xl rounded-tl-sm px-3.5 py-2 text-md whitespace-pre-wrap shadow-[inset_0_0_0_1px_var(--edge)] [overflow-wrap:anywhere]">{text}</p>
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

const SYSTEM: Record<string, Parameters<typeof t>[0]> = { compact: "sys.compact", interrupted: "sys.interrupted", unreadable: "sys.unreadable", api_error: "sys.apiError", local_command: "sys.localCommand" };

/** The agent's words, as yours are drawn (the user, 2026-11-06): a grey bubble, on the left, its
 *  name above; its face the agent's logo alone, no disc. What it is writing (chat.tsx's drafts) is
 *  in one too, so the words do not move as the transcript brings them. */
export function AgentBubble({ agent, time, children }: { agent: Agent; time?: number; children: ReactNode }) {
  const name = agentName(agent);
  return (
    <div className="flex min-w-0 items-start gap-2.5 pr-6 sm:pr-10" title={time ? clock(time) : undefined}>
      <span className="grid shrink-0 place-items-center" style={{ width: FACE, height: FACE }} aria-hidden>
        <Logo agent={agent} size={22} />
      </span>
      <div className="flex min-w-0 flex-col items-start gap-1">
        <span className="max-w-full truncate px-0.5 text-xs leading-4 text-ink-3">{name}</span>
        <div className="in-bubble flex max-w-full min-w-0 flex-col items-start gap-2 rounded-xl rounded-tl-sm bg-fill px-3.5 py-2">{children}</div>
      </div>
    </div>
  );
}

/** What the agent said: Markdown, then the files it names. */
const Assistant = memo(function Assistant({ b, session, cwd, agent }: { b: Block; session: string; cwd: string; agent: Agent }) {
  const home = useHome().data;
  const f = filesIn(b.text ?? "", cwd, home);
  if (!f.text && !b.files?.length) return null;
  return (
    <AgentBubble agent={agent} time={b.time}>
      {f.text && <Markdown text={f.text} className="max-w-full" />}
      {!!b.files?.length && <Files session={session} files={b.files} labels={f.labels} />}
    </AgentBubble>
  );
});

export function Said({ b, session, cwd, agent, source, onJump }: { b: Shown; session: string; cwd: string; agent: Agent; source: (b: { from?: string; fromTerminal?: string }) => Source; onJump: (id: string) => void }) {
  switch (b.kind) {
    case "user": {
      const src = source(b);
      const text = b.text ?? "";
      if (src.kind === "iridium") return <Line icon={<RotateCcw size={12} />}>{text}</Line>;
      if (src.kind === "letter") return <Letter src={src} text={text} time={b.time} onJump={onJump} />;
      // The server writes each attachment as a line "[附件] `path`": the file shows as itself instead.
      const paths = new Set(b.files?.map((f) => f.path));
      const words = paths.size ? text.replace(/\n*^\[附件\] `([^`]+)`$/gm, (all, p: string) => (paths.has(p) ? "" : all)).trim() : text;
      return (
        <YouBubble text={words} who={src} time={b.time} going={Date.now() - (landed.get(b.at) ?? 0) < CHECK_FOR ? "landed" : undefined}>
          {b.files?.map((f) => <Attached key={f.ref} session={session} f={f} />)}
        </YouBubble>
      );
    }
    case "assistant":
      return <Assistant b={b} session={session} cwd={cwd} agent={agent} />;
    case "system":
      if (b.event === "iridium") return <Line icon={<RotateCcw size={12} />} title={b.text}>{b.text}</Line>;
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
        <ChevronRight size={12} className="shrink-0 transition-transform group-open/sys:rotate-90" />
      </summary>
      <Clipped text={b.text ?? ""} length={b.length} at={b.at} session={session} error={b.event === "api_error"} />
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
  thinking: [Brain, 320],
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
export const KindIcon = ({ kind, cmd, failed, size = 14, className }: { kind: Kind; cmd?: Cmd; failed?: boolean; size?: number; className?: string }) => {
  const [KindGlyph, hue] = KINDS[kind];
  const Icon = (kind === "command" && cmd && CMDS[cmd]) || KindGlyph;
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

/** A step's line: its icon, its name, what it is about in monospace, an edit's +/−. */
function StepLine({ s, cwd }: { s: Step; cwd?: string }) {
  if (s.kind === "thinking")
    return (
      <>
        <KindIcon kind="thinking" />
        <span className="shrink-0 text-ink-2">{t("msg.thinking")}</span>
        <span className="min-w-0 truncate text-xs">{(s.text ?? "").replace(/[*_`#>]+/g, "").replace(/\s+/g, " ")}</span>
      </>
    );
  const a = argOf(s, cwd);
  return (
    <>
      <KindIcon kind={kindOf(s.name)} cmd={cmdOfCall(s)} failed={failed(s)} />
      <span className="shrink-0 text-ink-2">{s.name}</span>
      <span className={cn("min-w-0 truncate", a.code ? "font-mono text-xs" : "text-xs")}>{a.arg}</span>
      {a.diff && !failed(s) && <DiffStat d={a.diff} />}
      {failed(s) && <CircleX size={13} className="shrink-0 text-err" aria-label={t("msg.failed")} />}
    </>
  );
}

const ROW = "flex h-7 min-w-0 cursor-default list-none items-center gap-2 rounded-sm text-sm text-ink-3 hover:text-ink-2 [&::-webkit-details-marker]:hidden pointer-coarse:h-9";

/** The steps between two things said: one of them is its own line; more fold into one, each name
 *  once with its kind's icon and ×n, then the lines their edits changed, which opens to a line each.
 *  Each opens to what it was given and what came back. */
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
              <KindIcon kind={r.name ? kindOf(r.name) : "thinking"} cmd={r.name ? runCmd(steps, r.name) : undefined} className="mr-2 inline-block align-[-2.5px]" />
              <span className="text-ink-2">{r.name || t("msg.thinking")}</span>
              {r.n > 1 && <span className="tabular-nums"> ×{r.n}</span>}
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
        <ChevronRight size={13} className="shrink-0 text-ink-4 transition-transform duration-(--dur-2) group-open/fold:rotate-90" />
      </summary>
      <div className="g-fade mt-0.5 ml-[7px] flex flex-col pl-3 shadow-[inset_1px_0_0_var(--edge)]">
        {steps.map((s) => (
          <StepView key={s.at} s={s} session={session} cwd={cwd} />
        ))}
      </div>
    </details>
  );
}

function StepView({ s, session, cwd }: { s: Step; session: string; cwd: string }) {
  return (
    <details className="group/step w-full min-w-0">
      <summary className={ROW}>
        <StepLine s={s} cwd={cwd} />
      </summary>
      <div className="g-fade mb-2 ml-[22px] grid min-w-0 gap-1.5">
        {s.kind === "thinking" ? (
          <Clipped text={s.text ?? ""} length={s.length} at={s.at} session={session} prose />
        ) : (
          <>
            <Clipped text={s.input ?? ""} length={s.length} at={s.at} session={session} />
            {s.result ? <ResultView r={s.result} session={session} /> : <span className="text-xs text-ink-3">{t("msg.noResult")}</span>}
          </>
        )}
      </div>
    </details>
  );
}

function ResultView({ r, session }: { r: Result; session: string }) {
  return <Clipped text={r.text} length={r.length} at={r.at} session={session} error={r.error} />;
}

/** Text the page may have only the start of (length: the whole's), with a key to bring the whole. */
function Clipped({ text, length, at, session, error, prose }: { text: string; length?: number; at: string; session: string; error?: boolean; prose?: boolean }) {
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
      {prose ? (
        <Markdown text={shown} className="text-sm text-ink-2" />
      ) : (
        <pre className={cn("m-0 max-h-80 min-w-0 overflow-auto rounded-md bg-fill px-2.5 py-2 font-mono text-xs leading-[18px] whitespace-pre-wrap [overflow-wrap:anywhere]", error ? "text-err" : "text-ink-2")}>{pretty(shown)}</pre>
      )}
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
      <KindIcon kind={kindOf(tool.name)} cmd={cmdOfCall(tool)} />
      <span className="shine shrink-0">{tool.name}</span>
      <span className={cn("shine min-w-0 truncate", a.code ? "font-mono text-xs" : "text-xs")}>{a.arg}</span>
      {secs !== null && <span className="ml-auto shrink-0 text-xs text-ink-3 tabular-nums">{secs < 60 ? `${secs}s` : `${Math.floor(secs / 60)}m${secs % 60}s`}</span>}
    </div>
  );
}
