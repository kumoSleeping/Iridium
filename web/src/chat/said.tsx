// The blocks of a chat by source (plan "消息来源", web/sample): your words a bubble (another panel's
// avatar on its corner when from there); a letter card for what another session's ir, a plugin or a
// peer sent, its logo or avatar and name, a key to go there; Iridium's own lines centred and faint;
// the agent's words as Markdown with the files they name; tool calls and thoughts folded to a line.
import { ArrowUpRight, Bot, Brain, ChevronRight, CircleX, FilePen, FileText, Globe, ListChecks, LoaderCircle, MessageCircleQuestionMark, Plug, RotateCcw, RotateCw, Search, SquareTerminal, Wrench, X, type LucideIcon } from "lucide-react";
import { forwardRef, memo, useEffect, useState, type ReactNode } from "react";
import { api } from "../api/client";
import type { Block, Result } from "../api/types.gen";
import type { Sent } from "../chats";
import { t } from "../i18n";
import { Avatar, cn, IconKey, Logo, Tip } from "../ui";
import { Files, Picture } from "./files";
import { argOf, foldLine, kindOf, kindOfStep, type Kind, type Shown, type Step, type Tool } from "./fold";
import { Markdown } from "./markdown";
import { filesIn, type Source } from "./refs";

const clock = (ms?: number) => (ms ? new Date(ms).toLocaleTimeString(document.documentElement.lang === "en" ? "en" : "zh-CN", { hour: "2-digit", minute: "2-digit" }) : "");

/** Your words: a bubble; from another panel, its avatar on the corner. */
export const YouBubble = forwardRef<HTMLDivElement, { text: string; panel?: { name: string; avatar?: string }; faint?: boolean; failed?: boolean; children?: ReactNode; time?: number }>(function YouBubble({ text, panel, faint, failed, children, time }, ref) {
  return (
    <div className="group/you flex justify-end pl-10">
      <div ref={ref} className={cn("relative max-w-[min(560px,100%)] px-3.5 py-2", faint && "opacity-60")} title={time ? clock(time) : undefined}>
        <span data-bubble-bg className={cn("absolute inset-0 rounded-xl bg-fill", failed && "shadow-[inset_0_0_0_1px_var(--err)]")} aria-hidden />
        {text && <span className="relative block text-md whitespace-pre-wrap [overflow-wrap:anywhere]">{text}</span>}
        {children && <span className="relative mt-1 flex flex-wrap gap-1.5">{children}</span>}
        {panel && (
          <Tip label={t("msg.fromPanel", { name: panel.name })}>
            <span className="absolute -top-1.5 -left-1.5 grid rounded-full" style={{ boxShadow: "0 0 0 2px var(--paper)" }} aria-label={t("msg.fromPanel", { name: panel.name })}>
              <Avatar src={panel.avatar} name={panel.name} size={18} />
            </span>
          </Tip>
        )}
      </div>
    </div>
  );
});

/** A message sent from here the transcript has yet to bring: faint; not taken, red with a retry. */
export function Pending({ s, retry, drop, bubbleRef }: { s: Sent; retry: () => Promise<void>; drop: () => void; bubbleRef?: (el: HTMLDivElement | null) => void }) {
  const [busy, setBusy] = useState(false);
  return (
    <div className="flex flex-col items-end gap-0.5">
      <YouBubble ref={bubbleRef} text={s.text} faint={s.failed === undefined} failed={s.failed !== undefined}>
        {s.files.map((f) => (
          <span key={f.ref} className="flex items-center gap-1 rounded-md bg-paper/60 px-1.5 py-0.5 text-xs text-ink-2">
            <FileText size={12} />
            {f.name}
          </span>
        ))}
      </YouBubble>
      {s.failed !== undefined && (
        <div className="-mr-1 flex items-center gap-0.5">
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

/** A letter: from another session's ir, a plugin or a peer. Not your words. */
function Letter({ src, text, onJump }: { src: Extract<Source, { kind: "letter" }>; text: string; onJump: (id: string) => void }) {
  const head = (
    <>
      {src.agent ? (
        <Logo agent={src.agent} size={15} />
      ) : !src.avatar && src.name === "本机" ? (
        // The local ir, from a terminal: the terminal's mark.
        <span className="grid size-[18px] shrink-0 place-items-center rounded-full bg-fill-2 text-ink-2">
          <SquareTerminal size={11} strokeWidth={2.25} />
        </span>
      ) : (
        <Avatar src={src.avatar} name={src.name} size={18} channel={src.channel} />
      )}
      <span className="min-w-0 truncate text-sm font-medium text-ink-2">{src.name}</span>
    </>
  );
  return (
    <div className="flex pr-10">
      <div className="max-w-[min(560px,100%)] min-w-0 rounded-lg px-3.5 pt-2.5 pb-2.5 shadow-[0_0_0_1px_var(--edge)]">
        {src.session ? (
          <Tip label={t("msg.jump", { name: src.name })}>
            <button type="button" onClick={() => onJump(src.session!)} className="group -mx-1 flex max-w-full items-center gap-2 rounded-sm px-1 hover:bg-fill">
              {head}
              <ArrowUpRight size={13} className="shrink-0 text-ink-4 group-hover:text-ink-2" />
            </button>
          </Tip>
        ) : (
          <Tip label={src.channel ? t("msg.viaChannel", { name: src.name }) : src.name}>
            <span className="flex max-w-full items-center gap-2">{head}</span>
          </Tip>
        )}
        <p className="m-0 mt-1.5 text-md whitespace-pre-wrap [overflow-wrap:anywhere]">{text}</p>
      </div>
    </div>
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

/** What the agent said: Markdown, then the files it names. */
const Assistant = memo(function Assistant({ text, session }: { text: string; session: string }) {
  const f = filesIn(text);
  return (
    <div className="flex min-w-0 flex-col items-start gap-2 pr-6">
      {f.text && <Markdown text={f.text} className="w-full" />}
      {f.refs.length > 0 && <Files session={session} refs={f.refs} />}
    </div>
  );
});

export function Said({ b, session, cwd, source, onJump }: { b: Shown; session: string; cwd: string; source: (from?: string) => Source; onJump: (id: string) => void }) {
  switch (b.kind) {
    case "user": {
      const src = source(b.from);
      const text = b.text ?? "";
      if (src.kind === "iridium") return <Line icon={<RotateCcw size={12} />}>{text}</Line>;
      if (src.kind === "letter") return <Letter src={src} text={text} onJump={onJump} />;
      return <YouBubble text={text} panel={src.panel} time={b.time} />;
    }
    case "assistant":
      return <Assistant text={b.text ?? ""} session={session} />;
    case "system":
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

const KINDS: Record<Kind, LucideIcon> = { read: FileText, search: Search, web: Globe, edit: FilePen, command: SquareTerminal, agent: Bot, todo: ListChecks, ask: MessageCircleQuestionMark, mcp: Plug, other: Wrench, thinking: Brain };
const KindIcon = ({ kind, failed }: { kind: Kind; failed?: boolean }) => {
  const Icon = KINDS[kind];
  return <Icon size={14} className={cn("shrink-0", failed ? "text-err" : "text-ink-3")} aria-hidden />;
};
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
      <KindIcon kind={kindOf(s.name)} failed={failed(s)} />
      <span className="shrink-0 text-ink-2">{s.name}</span>
      <span className={cn("min-w-0 truncate", a.code ? "font-mono text-xs" : "text-xs")}>{a.arg}</span>
      {a.stat && <span className="shrink-0 font-mono text-xs tabular-nums">{a.stat}</span>}
      {failed(s) && <CircleX size={13} className="shrink-0 text-err" aria-label={t("msg.failed")} />}
    </>
  );
}

const ROW = "flex h-7 min-w-0 cursor-default list-none items-center gap-2 rounded-sm text-sm text-ink-3 hover:text-ink-2 [&::-webkit-details-marker]:hidden pointer-coarse:h-9";

/** The steps between two things said: one of them is its own line; more fold into one, the icons
 *  of their kinds and their names, which opens to a line each. Each opens to what it was given and
 *  what came back. */
function Fold({ steps, session, cwd }: { steps: Step[]; session: string; cwd: string }) {
  if (steps.length === 1) return <StepView s={steps[0]!} session={session} cwd={cwd} />;
  const runs = foldLine(steps);
  const kinds = [...new Set(steps.map(kindOfStep))].slice(0, 3);
  const bad = steps.filter(failed).length;
  return (
    <details className="group/fold -my-0.5 w-full min-w-0">
      <summary className={ROW} aria-label={`${t("msg.tools")}: ${runs.map((r) => r.name || t("msg.thinking")).join(", ")}`}>
        <span className="flex shrink-0 gap-1">
          {kinds.map((k) => (
            <KindIcon key={k} kind={k} />
          ))}
        </span>
        <span className="min-w-0 truncate">
          {runs.map((r, i) => (
            <span key={i}>
              {i > 0 && <span className="text-ink-4">{t("msg.sep")}</span>}
              <span className="text-ink-2">{r.name || t("msg.thinking")}</span>
              {r.n > 1 && <span className="tabular-nums"> ×{r.n}</span>}
            </span>
          ))}
        </span>
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
  // A result that is a picture's path (a screenshot tool's) shows the picture.
  if (/^\/\S+\.(png|jpe?g|webp|gif)$/i.test(r.text.trim())) return <Picture src={api.thumbURL(session, r.text.trim())} full={api.fileURL(session, r.text.trim())} name={r.text.trim().split("/").pop()!} />;
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
function Running({ tool, cwd }: { tool: Tool; cwd: string }) {
  const [now, setNow] = useState(Date.now);
  useEffect(() => {
    const tick = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(tick);
  }, []);
  const a = argOf(tool, cwd);
  const secs = tool.time ? Math.max(0, Math.round((now - tool.time) / 1000)) : null;
  return (
    <div className="flex h-7 min-w-0 items-center gap-2 text-sm" aria-live="off">
      <KindIcon kind={kindOf(tool.name)} />
      <span className="shine shrink-0">{tool.name}</span>
      <span className={cn("shine min-w-0 truncate", a.code ? "font-mono text-xs" : "text-xs")}>{a.arg}</span>
      {secs !== null && <span className="ml-auto shrink-0 text-xs text-ink-3 tabular-nums">{secs < 60 ? `${secs}s` : `${Math.floor(secs / 60)}m${secs % 60}s`}</span>}
    </div>
  );
}
