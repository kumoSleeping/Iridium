// The composer's meters (plan "网页"), in a row under it: the approval-mode key, the model key, the
// quota bars after them, a bar and its percent per window, amber nearly full, red full, faded when
// last read long ago or no process runs, each window in a popover; at the right the context ring,
// its breakdown in a popover. On a phone no percents in the row, and the bars stacked in one place,
// one over another (the user, 2026-11-06); the popovers still say them.
import { Menu } from "@base-ui/react/menu";
import { Popover } from "@base-ui/react/popover";
import { Check, ChevronDown, ChevronRight, Workflow } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { Fragment, useLayoutEffect, useRef, useState, type CSSProperties, type MouseEvent, type ReactNode } from "react";
import type { SessionState as LiveSession } from "../api/types.gen";
import { api } from "../api/client";
import type { Agent, AgentQuota, Context, ContextCategory, InputTokens, Model, Preset } from "../api/types.gen";
import { parse, type El } from "../satori";
import { useAgent } from "../data";
import { t, useLang } from "../i18n";
import { useContextState, useQuota } from "../store";
import { errText, toast } from "../toast";
import { agentName, ago, cn, PHONE, PopSurface, Tip, tokens as tok, useLongPress, useMedia, useMinute } from "../ui";
import { choicesQ } from "./choices";
import { EffortSlider, levelName, noteLevelNames } from "./effort";
import { hasMark, ModeMark, modeName } from "../modes";
import { openUsage } from "../route";
import { quotaKey, windowFor, windowName } from "../quota";

const k = (n: number) => (n >= 100_000 ? `${Math.round(n / 1000)}k` : n >= 1000 ? `${(n / 1000).toFixed(1)}k` : `${n}`);

type CtxKind = "free" | "reserve" | "mcp" | "memory" | "agents" | "tools" | "system" | "messages" | "other";
/** What a category is, by the names Claude gives. */
function ctxKind(c: ContextCategory): CtxKind {
  const n = `${c.id} ${c.name}`.toLowerCase();
  if (c.kind === "free" || n.includes("free")) return "free";
  if (c.kind === "buffer" || n.includes("buffer") || n.includes("compact")) return "reserve";
  if (n.includes("mcp")) return "mcp";
  if (n.includes("memory")) return "memory";
  if (n.includes("agent")) return "agents";
  if (n.includes("tool")) return "tools";
  if (n.includes("system")) return "system";
  if (n.includes("message")) return "messages";
  return "other";
}
/** A category's name in the page's language; any other as given. */
const ctxName = (c: ContextCategory) => {
  const kind = ctxKind(c);
  return kind === "other" ? c.name : t(`ctx.${kind}`);
};

/** Each category its own colour, whatever its size (a colour follows what it is, never its rank):
 *  the three that are most of a context in the chart's first three hues, the only three that stay
 *  apart for every eye in both themes (validated, all pairs); the smaller ones in greys of their
 *  own, each named in the list under the bar (the user, 2026-11-08: the popover in colour). */
const TONE: Record<Exclude<CtxKind, "free" | "reserve">, string> = {
  messages: "var(--series-1)",
  tools: "var(--series-2)",
  system: "var(--series-3)",
  mcp: "var(--ctx-2)",
  memory: "var(--ctx-3)",
  agents: "var(--ctx-4)",
  other: "var(--ctx-5)",
};
const HATCH = "repeating-linear-gradient(135deg, var(--ink-4) 0 1.5px, transparent 1.5px 4px)";

/** The parts in the bar's order: what is used, largest first, each in its category's colour; then
 *  the reserve, then free; with only a total, what is used and free. */
type Part = { key: string; name: string; tokens: number; tone: string; ring?: boolean };
export function ctxParts(c: Context): Part[] {
  const cats = c.categories ?? [];
  const used = cats.filter((x) => x.kind === "used" && x.tokens > 0).sort((a, b) => b.tokens - a.tokens);
  const parts: Part[] = used.map((x) => {
    const kind = ctxKind(x);
    return { key: x.id || x.name, name: ctxName(x), tokens: x.tokens, tone: TONE[kind === "free" || kind === "reserve" ? "other" : kind] };
  });
  // Only a total (the agent tells no categories): what is used, one part, so the bar is there
  // all the same (the user, 2026-11-12).
  if (!parts.length && c.used > 0) parts.push({ key: "used", name: t("ctx.inUse"), tokens: c.used, tone: "var(--ink-2)" });
  for (const x of cats.filter((x) => x.kind === "buffer" && x.tokens > 0)) parts.push({ key: x.id || x.name, name: ctxName(x), tokens: x.tokens, tone: HATCH, ring: true });
  const free = cats.find((x) => x.kind === "free");
  const freeTokens = free?.tokens ?? (c.max ? Math.max(0, c.max - c.used - parts.filter((p) => p.ring).reduce((n, p) => n + p.tokens, 0)) : 0);
  if (freeTokens > 0) parts.push({ key: "free", name: t("ctx.free"), tokens: freeTokens, tone: "var(--fill-2)", ring: true });
  return parts;
}

/** The cache's hit rate over a session's input so far, to a tenth of a percent; "" with no input. */
export function hitRate(t?: InputTokens): string {
  const all = t ? t.input + t.cacheRead + t.cacheWrite : 0;
  return all > 0 ? `${(Math.floor((t!.cacheRead / all) * 1000) / 10).toFixed(1)}%` : "";
}

/** How full a context is, as a ring alone (no number): pct of it used. */
export function Ring({ pct, size = 16 }: { pct: number; size?: number }) {
  const r = 7;
  const circ = 2 * Math.PI * r;
  return (
    <svg width={size} height={size} viewBox="0 0 18 18" className="shrink-0 -rotate-90" aria-hidden>
      <circle cx="9" cy="9" r={r} fill="none" stroke="var(--fill-2)" strokeWidth="2.25" />
      <circle cx="9" cy="9" r={r} fill="none" stroke={pct >= 90 ? "var(--need)" : "var(--ink-2)"} strokeWidth="2.25" strokeLinecap="round" strokeDasharray={`${(circ * pct) / 100} ${circ}`} />
    </svg>
  );
}

/** Session's context as a ring alone, before an agent's name (the user, 2026-11-13); none known,
 *  nothing. */
export function ContextMark({ session, summary, size = 14 }: { session: string; summary?: Context; size?: number }) {
  const c = useContextState(session) ?? summary;
  if (!c || !c.max) return null;
  const pct = Math.min(100, Math.round((c.used / c.max) * 100));
  return (
    <span role="img" aria-label={`${t("ctx.title")} ${pct}%`} className="grid shrink-0">
      <Ring pct={pct} size={size} />
    </span>
  );
}

/** tokens: the session's input so far, its cache's hit rate under the parts (the user, 2026-11-10:
 *  the total, in the popover only, never in the row). */
/** compact: the ring alone, as on a phone, small (after an agent's model name). */
export function ContextRing({ session, summary, tokens, open, onOpenChange, compact }: { session: string; summary?: Context; tokens?: InputTokens; open?: boolean; onOpenChange?: (o: boolean) => void; compact?: boolean }) {
  useLang();
  const phone = useMedia(PHONE) || !!compact;
  // The breakdown when the stream carries one; the total the session's state has, else.
  const c = useContextState(session) ?? summary;
  if (!c || !c.max) return null;
  const pct = Math.min(100, Math.round((c.used / c.max) * 100));
  const label = `${t("ctx.title")} · ${t("ctx.used", { used: k(c.used), total: k(c.max) })}`;
  return (
    <Popover.Root open={open} onOpenChange={onOpenChange}>
      <Tip label={label}>
        <Popover.Trigger
          render={
            <button type="button" aria-label={label} className={cn("g-press flex shrink-0 items-center gap-1.5 rounded-md text-xs text-ink-3 tabular-nums hover:bg-fill hover:text-ink-2 data-popup-open:bg-fill", compact ? "-my-1 h-6 px-1 pointer-coarse:h-8" : "h-7 px-1.5 pointer-coarse:h-9")}>
              <Ring pct={pct} size={compact ? 13 : 16} />
              {!phone && `${pct}%`}
            </button>
          }
        />
      </Tip>
      <PopSurface side={compact ? "bottom" : "top"} align={compact ? "start" : "end"} className="w-[min(300px,calc(100vw-16px))] p-3.5">
        <ContextDetail c={c} tokens={tokens} />
      </PopSurface>
    </Popover.Root>
  );
}

/** The context's breakdown, as the ring's popover says it: how full, the parts, the cache. */
export function ContextDetail({ c, tokens }: { c: Context; tokens?: InputTokens }) {
  const parts = ctxParts(c);
  const hit = hitRate(tokens);
  return (
    <>
      <div className="flex items-baseline justify-between">
        <span className="text-sm font-semibold">{t("ctx.title")}</span>
        <span className="text-xs text-ink-3 tabular-nums">{t("ctx.used", { used: k(c.used), total: k(c.max ?? 0) })}</span>
      </div>
      {parts.length > 1 && <Meter parts={parts} label={k} />}
      {/* The cache: a section as the context's above it, its title and rate, a bar of what was
          read from the cache, written to it, and neither, a line each (the user, 2026-11-12:
          not a footnote under a rule). */}
      {hit && tokens && (
        <section className="mt-4" aria-label={t("ctx.cacheHit")}>
          <div className="flex items-baseline justify-between">
            <span className="text-sm font-semibold">{t("ctx.cacheHit")}</span>
            <span className="text-xs text-ink-3 tabular-nums">{hit}</span>
          </div>
          <Meter parts={cacheParts(tokens)} label={tok} />
        </section>
      )}
    </>
  );
}

/** What a session's input was: read from the cache, written to it, neither; in greys, the read the
 *  darkest, the uncached as the context's free. */
const cacheParts = (x: InputTokens): Part[] =>
  [
    { key: "read", name: t("ctx.cacheRead"), tokens: x.cacheRead, tone: "var(--ctx-2)" },
    { key: "write", name: t("ctx.cacheWrite"), tokens: x.cacheWrite, tone: "var(--ctx-5)" },
    { key: "input", name: t("ctx.uncached"), tokens: x.input, tone: "var(--fill-2)", ring: true },
  ].filter((p) => p.tokens > 0);

/** A bar of parts, each its share in its tone, every one a rounded rectangle of the same corners
 *  (the user, 2026-11-12: not a half circle at the ends and square ones between), and under it a
 *  line each: its dot, name and size. */
function Meter({ parts, label }: { parts: Part[]; label: (n: number) => string }) {
  return (
    <>
      <div className="mt-2.5 flex h-2 gap-[2px]" role="img" aria-label={parts.map((p) => `${p.name} ${label(p.tokens)}`).join(", ")}>
        {parts.map((p, i) => (
          <Tip key={p.key} label={`${p.name} · ${label(p.tokens)}`}>
            <span
              className="g-fade h-full rounded-[2px]"
              style={{ flexGrow: p.tokens, flexBasis: 0, minWidth: 4, background: p.tone, animationDelay: `${i * 20}ms` } as CSSProperties}
            />
          </Tip>
        ))}
      </div>
      <ul className="m-0 mt-3 grid list-none gap-1 p-0">
        {parts.map((p) => (
          <li key={p.key} className="flex items-center gap-2 text-sm">
            <span className="size-2 shrink-0 rounded-full" style={{ background: p.tone, boxShadow: p.ring ? "inset 0 0 0 1px var(--ink-4)" : undefined }} />
            <span className="flex-1 text-ink-2">{p.name}</span>
            <span className="text-ink tabular-nums">{label(p.tokens)}</span>
          </li>
        ))}
      </ul>
    </>
  );
}

function resetIn(at: number | undefined, lang: string): string {
  if (!at) return "";
  const d = new Date(at);
  const today = new Date().toDateString() === d.toDateString();
  return d.toLocaleString(lang === "zh" ? "zh-CN" : "en", today ? { hour: "2-digit", minute: "2-digit" } : { weekday: "short", hour: "2-digit", minute: "2-digit" });
}

const STALE = 15 * 60_000;

/** A window's fill and text tones: red full, amber nearly full. */
const quotaTone = (pct: number) => (pct >= 95 ? ["bg-err", "text-err"] : pct >= 80 ? ["bg-need", "text-need"] : ["bg-ink-3", "text-ink-3"]) as [string, string];

/** The agent's quota bars, after the model key in the row under the composer; clicked, each window
 *  in a popover as Claude Code's /usage: its name, a wide bar, the percent and when it resets
 *  (the user, 2026-11-06), no heading nor account over them (the user, 2026-11-08: "用量" is the
 *  usage page's). /usage in the composer opens it too, with nothing read yet as well. Held down,
 *  or right-clicked, the bars open the usage instead (the user, 2026-11-08); the popover's foot
 *  has a key to it too, as a hold is not seen. */
/** using: what the session names the model it uses (chat.tsx): a window of one model shows only
 *  where that model is in use. machine: where the session runs; the windows are that machine's
 *  login's (an executor's may be another account), not this one's. */
/** compact: the phone's bars wherever it is (after an agent's model name, the user, 2026-11-13);
 *  pressed, the popover. */
export function QuotaBars({ agent, machine, live, using, open, onOpenChange, compact }: { agent: Agent; machine?: string; live: boolean; using: (string | undefined)[]; open?: boolean; onOpenChange?: (o: boolean) => void; compact?: boolean }) {
  const lang = useLang();
  const phone = useMedia(PHONE) || !!compact;
  useMinute(); // "read 5 min ago" and the fading, again each minute
  const quota = useQuota();
  // Held down (a finger), or the right button: the usage, at the bars (the user, 2026-11-08).
  const key = useRef<HTMLButtonElement>(null);
  const toUsage = (at?: { x: number; y: number }) => {
    const b = key.current?.getBoundingClientRect();
    onOpenChange?.(false);
    openUsage(at ?? (b && { x: b.left, y: b.top - 8 }), machine);
  };
  const lp = useLongPress(() => toUsage());
  const hold = { ...lp, onContextMenu: (e: MouseEvent) => (e.preventDefault(), toUsage({ x: e.clientX, y: e.clientY })) };
  const q: AgentQuota | undefined = quota?.agents.filter((a) => a.agent === agent && (a.machine ?? "") === (machine ?? "")).sort((a, b) => b.at - a.at)[0];
  const windows = (q?.windows ?? []).filter((w) => windowFor(w, using)); // all shown: no setting hides one (the user, 2026-11-06)
  // dsh has no windows but money: its balance where the bars would be (the user, 2026-11-08).
  const balance = q?.balance ?? [];
  if (compact && (!q || (!windows.length && !balance.length))) return null;
  if (!q || (!windows.length && !balance.length))
    // No bars, but /usage still opens the popover, from where they would be.
    return (
      <Popover.Root open={open} onOpenChange={onOpenChange}>
        <Popover.Trigger nativeButton={false} render={<span aria-hidden className="h-7 w-0 shrink-0 pointer-coarse:h-9" />} />
        <PopSurface side="top" align="start" className="w-[min(300px,calc(100vw-16px))] p-3.5">
          <p className="m-0 text-sm text-ink-3">{t("quota.none")}</p>
        </PopSurface>
      </Popover.Root>
    );
  // Faded once no process of the agent runs (the daemon says, else this session), or read long ago.
  const idle = !(q.live ?? live);
  const stale = idle || Date.now() - q.at > STALE;
  const title = windows.length ? t("quota.title") : t("quota.balance");
  const label = `${title}: ${[...windows.map((w) => `${windowName(agent, w)} ${Math.round(w.percent)}%`), ...balance.map((b) => money(b.currency, b.total))].join(", ")}`;
  return (
    <Popover.Root open={open} onOpenChange={onOpenChange}>
      <Tip label={title}>
        <Popover.Trigger
          render={
            <button ref={key} type="button" aria-label={label} {...hold} className={cn("g-press flex shrink-0 items-center rounded-md select-none hover:bg-fill data-popup-open:bg-fill [-webkit-touch-callout:none]", compact ? "-my-1 h-6 px-1 pointer-coarse:h-8" : "h-7 px-1.5 pointer-coarse:h-9")}>
              {!windows.length ? (
                <span className={cn("text-xs text-ink-3 tabular-nums transition-opacity", stale && "opacity-50")}>{balance.map((b) => money(b.currency, b.total)).join(" · ")}</span>
              ) : phone ? (
                <span className={cn("flex w-9 flex-col gap-[3px] transition-opacity", stale && "opacity-50")}>
                  {windows.map((w) => {
                    const pct = Math.round(w.percent);
                    return (
                      <span key={quotaKey(q, w)} className="relative block h-[3px] overflow-hidden rounded-full bg-fill-2">
                        <span className={cn("absolute inset-y-0 left-0 w-full origin-left rounded-full transition-transform duration-(--dur-3)", quotaTone(pct)[0])} style={{ transform: `scaleX(${Math.min(100, pct) / 100})` }} />
                      </span>
                    );
                  })}
                </span>
              ) : (
                <span className={cn("flex items-center gap-3 transition-opacity @max-md:gap-2", stale && "opacity-50")}>
                  {windows.map((w) => {
                    const pct = Math.round(w.percent);
                    const [fill, text] = quotaTone(pct);
                    return (
                      <span key={quotaKey(q, w)} className="flex items-center gap-1.5 @max-md:gap-1">
                        {/* Shorter as the composer narrows (its @container): the row fits, not scrolled. */}
                        <span className="relative h-1 w-9 overflow-hidden rounded-full bg-fill-2 @max-lg:w-6 @max-md:w-4">
                          <span className={cn("absolute inset-y-0 left-0 w-full origin-left rounded-full transition-transform duration-(--dur-3)", fill)} style={{ transform: `scaleX(${Math.min(100, pct) / 100})` }} />
                        </span>
                        <span className={cn("text-xs tabular-nums", text)}>{pct}%</span>
                      </span>
                    );
                  })}
                </span>
              )}
            </button>
          }
        />
      </Tip>
      <PopSurface side="top" align="start" className="w-[min(300px,calc(100vw-16px))] p-3.5">
        {balance.length > 0 && (
          <ul className={cn("m-0 grid list-none gap-2 p-0", windows.length > 0 && "mb-3")}>
            {balance.map((b) => (
              <li key={b.currency} className="grid gap-0.5">
                <div className="flex items-baseline justify-between gap-3 text-sm">
                  <span className="min-w-0 truncate text-ink-2">{t("quota.balance")}</span>
                  <span className="text-base font-semibold tabular-nums">{money(b.currency, b.total)}</span>
                </div>
                <span className="text-xs text-ink-3 tabular-nums">{t("quota.balanceParts", { paid: money(b.currency, b.toppedUp), given: money(b.currency, b.granted) })}</span>
              </li>
            ))}
          </ul>
        )}
        <ul className="m-0 grid list-none gap-3 p-0">
          {windows.map((w) => {
            const pct = Math.round(w.percent);
            const [fill, text] = quotaTone(pct);
            const name = windowName(agent, w);
            const time = resetIn(w.resetsAt, lang);
            return (
              <li key={quotaKey(q, w)} className="grid gap-1.5">
                <div className="flex items-baseline justify-between gap-3 text-sm">
                  <span className="min-w-0 truncate text-ink-2">{name}</span>
                  <span className={cn("tabular-nums", pct >= 80 ? text : "text-ink")}>{pct}%</span>
                </div>
                <span role="meter" aria-label={name} aria-valuenow={pct} aria-valuemin={0} aria-valuemax={100} className="relative block h-1.5 overflow-hidden rounded-full bg-fill-2">
                  <span className={cn("absolute inset-y-0 left-0 w-full origin-left rounded-full", pct >= 80 ? fill : "bg-ink-2")} style={{ transform: `scaleX(${Math.min(100, pct) / 100})` }} />
                </span>
                {time && <span className="text-xs text-ink-3 tabular-nums">{t("quota.resets", { time })}</span>}
              </li>
            );
          })}
        </ul>
        <div className="mt-3 flex items-center gap-3">
          <p className={cn("m-0 min-w-0 flex-1 text-xs", stale ? "text-ink-4" : "text-ink-3")}>
            {t("quota.read", { ago: ago(q.at) })}
            {idle && ` · ${t("quota.idle")}`}
          </p>
          <button type="button" onClick={(e) => toUsage({ x: e.clientX, y: e.clientY })} className="g-press -my-1 -mr-1.5 flex shrink-0 items-center gap-0.5 rounded-md px-1.5 py-1 text-xs text-ink-2 hover:bg-fill hover:text-ink">
            {t("quota.stats")}
            <ChevronRight size={12} />
          </button>
        </div>
      </PopSurface>
    </Popover.Root>
  );
}

/** An amount in its currency: ¥9.83, $1.20. */
const money = (currency: string, n: number) => {
  try {
    return n.toLocaleString(document.documentElement.lang || undefined, { style: "currency", currency, currencyDisplay: "narrowSymbol" });
  } catch {
    return `${n.toFixed(2)} ${currency}`;
  }
};

/** An agent's approval modes, in the order the key cycles them: as GET /v1/internal/agents lists them, else
 *  Claude's own. */
const MODES: Partial<Record<Agent, string[]>> = { claude: ["manual", "acceptEdits", "plan", "auto", "dontAsk", "bypassPermissions"], codex: ["untrusted", "on-request", "granular", "auto"], pi: ["auto", "ask"] };
type Mode = { id: string };

export { modeName };

/** Sets the mode; what it shows meanwhile is the one picked. */
export function useMode(s: LiveSession) {
  const [want, setWant] = useState<string | null>(null);
  const info = useAgent(s.agent);
  const modes: Mode[] = (s.modes ?? info?.modes ?? MODES[s.agent] ?? []).map((id) => ({ id }));
  const now = want ?? s.mode ?? info?.defaultMode ?? modes[0]?.id ?? "default";
  if (want !== null && s.mode === want) setWant(null);
  const set = async (m: string) => {
    if (m === now) return;
    setWant(m);
    try {
      await api.mode(s.id, m);
      // Not every agent switches at once (GET /v1/internal/agents modeSwitch): say when it does.
      if (info?.modeSwitch === "next turn") toast(t("mode.nextTurn"));
      else if (info?.modeSwitch === "restart") toast(t("mode.restart"));
    } catch (e) {
      setWant(null);
      toast(t("err.mode"), { error: true, description: errText(e) });
    }
  };
  const cycle = () => modes.length > 1 && void set(modes[(modes.findIndex((m) => m.id === now) + 1) % modes.length]!.id);
  return { agent: s.agent, modes, now, set, cycle };
}

/** The approval mode, under the composer, its name alone: no icon (the user, 2026-11-08); on a phone,
 *  its mark alone instead. The menu lists both. A mode picked closes the menu. */
export function ModeKey({ mode, open, onOpenChange }: { mode: ReturnType<typeof useMode>; open?: boolean; onOpenChange?: (o: boolean) => void }) {
  useLang();
  const phone = useMedia(PHONE);
  if (!mode.modes.length) return null;
  const label = modeName({ id: mode.now });
  const marked = phone && hasMark(mode.agent, mode.now);
  return (
    <Menu.Root open={open} onOpenChange={onOpenChange}>
      <Tip label={t("composer.mode")}>
        <Menu.Trigger
          render={
            <button type="button" aria-label={`${t("composer.mode")}: ${label}`} className="g-press flex h-7 min-w-0 shrink-0 items-center gap-1 rounded-md px-2 text-xs whitespace-nowrap text-ink-3 hover:bg-fill hover:text-ink data-popup-open:bg-fill pointer-coarse:h-9">
              {marked ? (
                <ModeMark key={mode.now} agent={mode.agent} id={mode.now} size={16} className="g-fade shrink-0" />
              ) : (
                <span key={label} className="g-fade truncate">
                  {label}
                </span>
              )}
              <ChevronDown size={12} className="shrink-0 text-ink-4" />
            </button>
          }
        />
      </Tip>
      <Menu.Portal>
        <Menu.Positioner side="top" align="start" sideOffset={6} collisionPadding={8} className="z-50">
          <Menu.Popup finalFocus={false} className="g-unfold glass thick float min-w-52 rounded-lg p-1.5 text-ink outline-none">
            <Menu.RadioGroup value={mode.now} onValueChange={(v) => void mode.set(v as string)} className="g-stagger flex flex-col">
              {mode.modes.map((m, i) => (
                <Menu.RadioItem key={m.id} value={m.id} closeOnClick style={{ "--i": i } as CSSProperties} className="flex h-8 cursor-default items-center gap-2.5 rounded-md pr-2 pl-2.5 text-base outline-none select-none data-highlighted:bg-fill pointer-coarse:h-11">
                  <ModeMark agent={mode.agent} id={m.id} size={16} className="shrink-0 text-ink-3" />
                  <span className="flex-1">{modeName(m)}</span>
                  <Menu.RadioItemIndicator className="text-ink">
                    <Check size={14} />
                  </Menu.RadioItemIndicator>
                </Menu.RadioItem>
              ))}
            </Menu.RadioGroup>
          </Menu.Popup>
        </Menu.Positioner>
      </Menu.Portal>
    </Menu.Root>
  );
}

/** The models an agent offers when neither the session nor a session of its kind before it lists
 *  them. Which one it runs with none set is not known: none is ticked. */
const MODELS: Partial<Record<Agent, Model[]>> = {
  claude: [
    { value: "opus", name: "Opus" },
    { value: "sonnet", name: "Sonnet" },
    { value: "haiku", name: "Haiku" },
  ],
};

/** A list that scrolls inside a popup: its edges fade where there is more to see; opened, the
 *  checked item is brought into view. */
function Scrolled({ className, children }: { className?: string; children: ReactNode }) {
  const ref = useRef<HTMLDivElement>(null);
  const [edge, setEdge] = useState({ top: false, bottom: false });
  const look = () => {
    const el = ref.current;
    if (!el) return;
    const top = el.scrollTop > 1;
    const bottom = el.scrollTop + el.clientHeight < el.scrollHeight - 1;
    setEdge((e) => (e.top === top && e.bottom === bottom ? e : { top, bottom }));
  };
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    const on = el.querySelector<HTMLElement>("[data-checked]");
    if (on) el.scrollTop = Math.max(0, on.offsetTop - (el.clientHeight - on.offsetHeight) / 2);
    look();
    const ro = new ResizeObserver(look);
    ro.observe(el);
    return () => ro.disconnect();
  }, []);
  const mask = `linear-gradient(to bottom, ${edge.top ? "transparent" : "#000"}, #000 24px, #000 calc(100% - 24px), ${edge.bottom ? "transparent" : "#000"})`;
  return (
    <div ref={ref} onScroll={look} className={cn("no-bar relative overflow-y-auto overscroll-contain", className)} style={{ maskImage: mask, WebkitMaskImage: mask }}>
      {children}
    </div>
  );
}

/** ran: the model its kind last ran there, for an agent that names no default; effortOwn: the
 *  level a session given no effort ran at with the default model (AgentChoices, for a session
 *  whose agent has said nothing yet). */
type ModelOf = Pick<LiveSession, "agent" | "model" | "pick" | "models" | "efforts" | "effort" | "effortNow"> & { ran?: string; effortOwn?: string };

/** What the model key shows, from the session's state (want: a model just picked, not yet in it).
 *  No model is called "default" (the user, 2026-11-16): def is the one the agent runs with none
 *  set (the row marked default, else ran). cur: the entry in use: the one set while it is what
 *  runs, else by the id in use (its value, the full id it resolves to), else, nothing said yet,
 *  def; one in use that the agent's list lacks is a row of its own, by its id. name: its name;
 *  "" when nothing is known. value: the menu's tick. levels: the efforts the model takes (none:
 *  it takes none), else the agent's. level: the one in use: set, if this model takes it, else
 *  the agent's own; "" not known. */
export function modelView(s: ModelOf, want?: string | null) {
  let models = s.models ?? MODELS[s.agent] ?? [];
  noteLevelNames(models);
  // "default" from a state kept from before: none set.
  const named = (x?: string | null) => (x && x !== "default" ? x : "");
  const pick = named(want ?? s.pick);
  const now = named(want ?? s.model);
  const def = models.find((m) => m.default) ?? (s.ran ? models.find((m) => m.value === s.ran) : undefined);
  // The value set, while what is in use is it (two aliases may resolve to one id).
  const picked = pick ? models.find((m) => m.value === pick && (!now || now === pick || !m.resolved || m.resolved === now)) : undefined;
  // None set: of two aliases of the id in use, the agent's own.
  const own = !pick && def && (def.value === now || def.resolved === now) ? def : undefined;
  const used = now ? (own ?? models.find((m) => m.value === now) ?? models.find((m) => m.resolved === now) ?? models.find((m) => m.name === now)) : undefined;
  let cur = picked ?? used ?? (!pick && !now ? def : undefined);
  if (!cur && now && s.models) {
    cur = { value: now, name: now };
    models = [cur, ...models];
  }
  const name = cur?.name ?? now;
  const value = cur?.value ?? pick;
  const levels = cur?.efforts?.length ? cur.efforts : cur?.noEffort ? [] : (s.efforts ?? []);
  const takes = (l?: string) => (l && (!levels.length || levels.includes(l)) ? l : "");
  // The level set, while this model takes it; else the one the agent says it runs at; before it
  // says, the one its kind runs the default model at.
  const level = takes(s.effort) || s.effortNow || (cur && cur === def ? takes(s.effortOwn) : "");
  return { models, cur, def, name, value, levels, level };
}

/** A button attached at the effort track (Claude's UltraCode, a plugin's: what the session's
 *  `attached` says, docs/next/plugin.md): pressed as "att:<session>:<from>:<id>"; its mark a data:
 *  image in it (<img>). */
type AttachedKey = { press: string; label: string; pressed: boolean; disabled: boolean; title?: string; icon?: string };

/** The <img> in a button, a data: image: its mark. Nothing fetched from elsewhere. */
const iconIn = (els: El[]): string | undefined => {
  for (const e of els) {
    const src = e.type === "img" ? e.attrs.src : undefined;
    if (typeof src === "string" && /^data:image\//i.test(src)) return src;
    const inner = iconIn(e.children);
    if (inner) return inner;
  }
};

/** A key's mark: its own (a plugin's), drawn as a mask in the text's colour (an image's script never
 *  runs, its colours never show), else UltraCode's. */
export function KeyMark({ icon, size, className }: { icon?: string; size: number; className?: string }) {
  if (!icon) return <Workflow size={size} aria-hidden className={className} />;
  const url = `url("${icon.replace(/["\\\n\r]/g, encodeURIComponent)}")`;
  const mask = { maskImage: url, WebkitMaskImage: url, maskSize: "contain", WebkitMaskSize: "contain", maskRepeat: "no-repeat", WebkitMaskRepeat: "no-repeat", maskPosition: "center", WebkitMaskPosition: "center" };
  return <span aria-hidden className={cn("inline-block shrink-0", className)} style={{ width: size, height: size, backgroundColor: "currentColor", ...mask }} />;
}

/** A key with sub-modes is one key: off, or the sub-mode on; a press, the next (off, each sub-mode,
 *  off; a disabled one passed over). Its sub-mode now, the button to press next and what that
 *  turns on and off. Without sub-modes, a switch. */
export function keyCycle(keys: AttachedKey[]): { cur?: AttachedKey; next: AttachedKey; want: Record<string, boolean> } | undefined {
  const [feat, ...opts] = keys;
  if (!feat) return undefined;
  const live = opts.filter((o) => !o.disabled);
  if (!live.length) return { next: feat, want: { [feat.press]: !feat.pressed } };
  const cur = feat.pressed ? (opts.find((o) => o.pressed) ?? undefined) : undefined;
  const after = feat.pressed ? (cur ? live.slice(live.indexOf(cur) + 1)[0] : undefined) : live[0];
  if (!after) return { cur, next: feat, want: { [feat.press]: false } };
  return { cur, next: after, want: { [feat.press]: true, [after.press]: true, ...(cur ? { [cur.press]: false } : {}) } };
}

const textIn = (els: El[]): string => els.map((e) => (e.type === "text" ? (e.text ?? "") : textIn(e.children))).join("");

/** The buttons of the first piece attached at place, in the page's language (else the one for any,
 *  else the first). */
export function attachedKeys(s: LiveSession, place: string, lang: string): AttachedKey[] {
  const a = (s.attached ?? []).find((x) => x.place === place);
  if (!a) return [];
  const c = a.content;
  const text = c[lang] ?? c[""] ?? Object.values(c)[0] ?? "";
  const out: AttachedKey[] = [];
  const walk = (els: El[]) => {
    for (const e of els) {
      if (e.type === "button" && typeof e.attrs.id === "string") {
        const title = e.attrs.title;
        out.push({
          press: `att:${s.id}:${a.from}:${e.attrs.id}`,
          label: textIn(e.children).trim() || e.attrs.id,
          pressed: e.attrs.pressed !== undefined && e.attrs.pressed !== "false",
          disabled: e.attrs.disabled !== undefined && e.attrs.disabled !== "false",
          title: typeof title === "string" ? title : undefined,
          icon: iconIn(e.children),
        });
      } else walk(e.children);
    }
  };
  walk(parse(text));
  return out;
}

/** A session's model menu, wherever it is opened (the composer's key, an agent's face's menu:
 *  kumo, 2026-11-16, one menu, the same in both): its models (those a session of its kind said
 *  last, before its own agent says), the effort track, its features (UltraCode). What a pick
 *  wants is shown at once, till the session says it is so. */
export function useModelMenu(s: LiveSession) {
  const lang = useLang();
  // A model just picked: the row shown, and what was sent for it ("": the agent's own).
  const [want, setWant] = useState<{ value: string; sent: string } | null>(null);
  // A key pressed shows at once, till the session says it is so (or the press failed).
  const [pressWant, setPressWant] = useState<Record<string, boolean>>({});
  const info = useAgent(s.agent);
  if (want !== null && ((s.pick ?? "") === want.sent || s.model === want.value)) setWant(null);
  // Its agent has not said its models yet (not started): those a session of its kind there said
  // last, the one it will run among them.
  const said = !!s.models?.length;
  const asked = useQuery({ ...choicesQ(s.agent, { machine: s.machine || undefined }), enabled: !said }).data;
  const base = attachedKeys(s, "effort", lang);
  // Before its agent runs (none attached yet), Claude's UltraCode, off.
  if (!base.length && info?.ultracode) base.push({ press: "", label: t("composer.ultracode"), pressed: false, disabled: false, title: t("composer.ultracodeHint") });
  const keys = base.map((x) => (x.press in pressWant ? { ...x, pressed: pressWant[x.press] } : x));
  const reached = Object.keys(pressWant).filter((p) => {
    const x = base.find((y) => y.press === p);
    return !x || x.pressed === pressWant[p];
  });
  if (reached.length) setPressWant((m) => Object.fromEntries(Object.entries(m).filter(([p]) => !reached.includes(p))));
  const v = modelView(said || !asked?.models.length ? s : { ...s, models: asked.models, efforts: s.efforts ?? asked.efforts, ran: asked.ran, effortOwn: asked.effort }, want?.value);
  const { models, levels, level } = v;
  // Not known yet: the agent's name alone, till it says its model.
  const label = v.name || agentName(s.agent);
  const pickModel = async (model: string) => {
    // The agent's own default is set as none: it stays the agent's to resolve.
    const sent = models.find((m) => m.value === model)?.default ? "" : model;
    setWant({ value: model, sent });
    try {
      await api.model(s.id, sent);
    } catch (e) {
      setWant(null);
      toast(t("err.model"), { error: true, description: errText(e) });
    }
  };
  const setEffort = async (effort: string) => {
    try {
      await api.effort(s.id, effort);
      return true;
    } catch (e) {
      toast(t("err.effort"), { error: true, description: errText(e) });
      return false;
    }
  };
  // What it is to become shown at once: the button pressed and the sub-modes it turns on or off.
  const press = async (x: AttachedKey, want: Record<string, boolean>) => {
    setPressWant((m) => ({ ...m, ...want }));
    try {
      if (x.press) await api.button(x.press);
      else await api.ultracode(s.id, !!want[x.press]);
    } catch (e) {
      setPressWant((m) => Object.fromEntries(Object.entries(m).filter(([p]) => !(p in want))));
      toast(t("err.feature", { name: x.label }), { error: true, description: errText(e) });
    }
  };
  // The first button the key at the track's right; the rest its sub-modes, shown in it one at a
  // time, a press the next (the user, 2026-11-11: no row of them under the track).
  const feat = keys[0];
  const cycle = keyCycle(keys);
  const cur = cycle?.cur;
  const name = feat && [feat.label, cur?.label].filter(Boolean).join(" · ");
  const tip = feat && [name, feat.title].filter(Boolean).join(" · ");
  // One key, its mark, pressed or not (the user, 2026-11-06); lit warm when on. A plugin's the
  // same (the user, 2026-11-09).
  const featKey = feat && (
    <Tip label={tip}>
      <button
        type="button"
        aria-pressed={feat.pressed}
        aria-label={name}
        disabled={feat.disabled}
        onClick={() => cycle && void press(cycle.next, cycle.want)}
        className={cn("g-press grid size-7 shrink-0 place-items-center rounded-md transition-colors duration-(--dur-2) disabled:opacity-40 pointer-coarse:size-10", feat.pressed ? "bg-fill text-[#ff4a17] hover:bg-fill-2" : "text-ink-4 hover:bg-fill hover:text-ink-2")}
      >
        <span key={cur?.press ?? ""} className="g-pop grid place-items-center">
          <KeyMark icon={cur?.icon ?? feat.icon} size={16} className={cn("transition-transform duration-(--dur-2) ease-(--ease-spring)", feat.pressed && "scale-110")} />
        </span>
      </button>
    </Tip>
  );
  // On: said in words beside the level too (the user, 2026-11-06).
  const featTag = feat?.pressed && (
    <span key={name} className="g-fade text-xs font-medium text-[#ff4a17]">
      {name}
    </span>
  );
  const on = feat?.pressed ? [feat] : [];
  return { v, models, levels, level, label, name, feat, cur, on, featKey, featTag, pickModel, setEffort };
}

/** The model menu's inside for a session (useModelMenu): the composer's key's and an agent's
 *  face's menu's. */
export function SessionModels({ m }: { m: ReturnType<typeof useModelMenu> }) {
  return <ModelList models={m.models} value={m.v.value} pickModel={(x) => void m.pickModel(x)} levels={m.levels} level={m.level} setEffort={m.setEffort} featKey={m.featKey} featTag={m.featTag} foot={!!m.feat} name={m.name} />;
}

/** The model menu's popup, the composer's and a face's menu's submenu alike. */
export const MODEL_POPUP = "g-unfold glass thick float flex max-h-[min(640px,calc(var(--available-height)-40px))] w-72 max-w-[calc(100vw-16px)] flex-col rounded-lg p-1.5 text-ink outline-none";

/** The model, its thinking level and its features (UltraCode), under the composer, each in use at
 *  once. A model picked closes the menu; the level and a feature at its foot are tuned in place, it
 *  stays. */
export function ModelKey({ s, open, onOpenChange }: { s: LiveSession; open?: boolean; onOpenChange?: (o: boolean) => void }) {
  const m = useModelMenu(s);
  const { models, levels, level, label, name, feat, cur, on } = m;
  if (!models.length && !m.v.name) return null;
  return (
    <Menu.Root open={open} onOpenChange={onOpenChange}>
      <Tip label={t("composer.model")}>
        <Menu.Trigger
          disabled={!models.length}
          render={
            <button type="button" aria-label={`${t("composer.model")}: ${label}${level ? ` · ${levelName(level)}` : ""}${on.length ? ` · ${name}` : ""}`} className="g-press flex h-7 min-w-0 items-center gap-1 rounded-md px-2 text-xs text-ink-3 hover:bg-fill hover:text-ink data-popup-open:bg-fill disabled:hover:bg-transparent pointer-coarse:h-9">
              {/* No icon: the model's name says what this is. */}
              <span key={label} className="g-fade truncate">
                {label}
              </span>
              {level && levels.length > 0 && (
                <span key={level} className="g-fade shrink-0 text-ink-4">
                  {levelName(level)}
                </span>
              )}
              {on.length > 0 && <KeyMark key={cur?.press ?? ""} icon={cur?.icon ?? feat?.icon} size={12} className={cn("g-pop shrink-0", on.some((f) => !f.disabled) ? "text-ink-2" : "text-ink-4")} />}
              {models.length > 0 && <ChevronDown size={12} className="shrink-0 text-ink-4" />}
            </button>
          }
        />
      </Tip>
      <Menu.Portal>
        <Menu.Positioner side="top" align="start" sideOffset={6} collisionPadding={8} className="z-50">
          <Menu.Popup finalFocus={false} className={MODEL_POPUP}>
            <SessionModels m={m} />
          </Menu.Popup>
        </Menu.Positioner>
      </Menu.Portal>
    </Menu.Root>
  );
}

/** The model menu's inside (ModelKey's, and a new agent's under 邀请, chat/channel/pick.tsx):
 *  the models, a pick closing it; under them the effort track, tuned in place. foot: a key at the
 *  track's right (featKey) even with no levels. */
export function ModelList({ models, value, pickModel, levels, level, setEffort, featKey, featTag, foot, name }: { models: Model[]; value: string; pickModel: (v: string) => void; levels: string[]; level: string; setEffort: (e: string) => Promise<boolean>; featKey?: ReactNode; featTag?: ReactNode; foot?: boolean; name?: string }) {
  const grouped = new Set(models.map((m) => m.group).filter(Boolean)).size > 1;
  return (
    <>
      {/* The models scroll inside, the foot stays; as high as there is room above, short of the
          top by 40px, at most 640px (the user, 2026-11-06). */}
      <Scrolled className="-mr-1 min-h-0 pr-1">
        <Menu.RadioGroup value={value} onValueChange={(x) => pickModel(x as string)} className="g-stagger flex flex-col">
          {models.map((m, i) => (
            <Fragment key={m.value}>
              {/* Each provider's models under its name (dsh), when there is more than one
                  (the user, 2026-11-09). */}
              {grouped && m.group && m.group !== models[i - 1]?.group && (
                <div className="truncate px-2.5 pt-2 pb-0.5 text-xs text-ink-3 select-none first:pt-1">{m.group}</div>
              )}
              {/* A pick closes the menu (Base UI keeps a radio item's open by default). */}
              <Menu.RadioItem value={m.value} closeOnClick style={{ "--i": i } as CSSProperties} className="flex min-h-8 cursor-default items-center gap-2.5 rounded-md pr-2 pl-2.5 text-base outline-none select-none data-highlighted:bg-fill pointer-coarse:min-h-11">
                {/* The name alone: no model's blurb under it (the user, 2026-11-06). */}
                <span className="min-w-0 flex-1 truncate py-1">{m.name}</span>
                <Menu.RadioItemIndicator className="text-ink">
                  <Check size={14} />
                </Menu.RadioItemIndicator>
              </Menu.RadioItem>
            </Fragment>
          ))}
        </Menu.RadioGroup>
      </Scrolled>
      {(levels.length > 1 || foot) && (
        // The foot: tuned in place, never closing the menu; the model's own levels drawn anew.
        // UltraCode a switch at the track's right, not a row of its own (the user, 2026-11-06).
        <div className="mt-1 shrink-0 border-t border-(--edge) pt-0.5">
          {levels.length > 1 ? (
            <EffortSlider key={levels.join(" ")} levels={levels} value={level || undefined} set={setEffort} aside={featKey} tag={featTag} />
          ) : (
            <div className="flex h-10 items-center justify-between gap-3 px-2.5">
              <span className="text-xs text-ink-3">{name}</span>
              {featKey}
            </div>
          )}
        </div>
      )}
    </>
  );
}

/** The presets dsh ships: a name and what it gives, in the page's language. One that publishes its
 *  own name (a bundle's, the user's) shows as it says (dsh's display rule). */
const PRESET_KEY: Record<string, [Parameters<typeof t>[0], Parameters<typeof t>[0]]> = {
  standard: ["preset.standard", "preset.standardHint"],
  ptc: ["preset.ptc", "preset.ptcHint"],
  minimal: ["preset.minimal", "preset.minimalHint"],
  cordis: ["preset.cordis", "preset.cordisHint"],
};
export const presetName = (id: string, p?: Preset) => p?.name || (PRESET_KEY[id] ? t(PRESET_KEY[id][0]) : id);
