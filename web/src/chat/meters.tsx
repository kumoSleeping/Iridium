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
import type { Agent, AgentInfo, AgentQuota, Context, ContextCategory, Feature, InputTokens, Model, Preset } from "../api/types.gen";
import { useAgent } from "../data";
import { t, useLang } from "../i18n";
import type { Key } from "../i18n/zh";
import { useContextState, useQuota } from "../store";
import { errText, toast } from "../toast";
import { ago, cn, PHONE, PopSurface, Tip, tokens as tok, useLongPress, useMedia, useMinute } from "../ui";
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
 *  the reserve, then free. */
type Part = { key: string; name: string; tokens: number; tone: string; ring?: boolean };
export function ctxParts(c: Context): Part[] {
  const cats = c.categories ?? [];
  const used = cats.filter((x) => x.kind === "used" && x.tokens > 0).sort((a, b) => b.tokens - a.tokens);
  const parts: Part[] = used.map((x) => {
    const kind = ctxKind(x);
    return { key: x.id || x.name, name: ctxName(x), tokens: x.tokens, tone: TONE[kind === "free" || kind === "reserve" ? "other" : kind] };
  });
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

/** tokens: the session's input so far, its cache's hit rate under the parts (the user, 2026-11-10:
 *  the total, in the popover only, never in the row). */
export function ContextRing({ session, summary, tokens, open, onOpenChange }: { session: string; summary?: Context; tokens?: InputTokens; open?: boolean; onOpenChange?: (o: boolean) => void }) {
  useLang();
  const phone = useMedia(PHONE);
  // The breakdown when the stream carries one; the total the session's state has, else.
  const c = useContextState(session) ?? summary;
  if (!c || !c.max) return null;
  const pct = Math.min(100, Math.round((c.used / c.max) * 100));
  const r = 7;
  const circ = 2 * Math.PI * r;
  const label = `${t("ctx.title")} · ${t("ctx.used", { used: k(c.used), total: k(c.max) })}`;
  const parts = ctxParts(c);
  const hit = hitRate(tokens);
  return (
    <Popover.Root open={open} onOpenChange={onOpenChange}>
      <Tip label={label}>
        <Popover.Trigger
          render={
            <button type="button" aria-label={label} className="g-press flex h-7 shrink-0 items-center gap-1.5 rounded-md px-1.5 text-xs text-ink-3 tabular-nums hover:bg-fill hover:text-ink-2 data-popup-open:bg-fill pointer-coarse:h-9">
              <svg width="16" height="16" viewBox="0 0 18 18" className="-rotate-90">
                <circle cx="9" cy="9" r={r} fill="none" stroke="var(--fill-2)" strokeWidth="2.25" />
                <circle cx="9" cy="9" r={r} fill="none" stroke={pct >= 90 ? "var(--need)" : "var(--ink-2)"} strokeWidth="2.25" strokeLinecap="round" strokeDasharray={`${(circ * pct) / 100} ${circ}`} />
              </svg>
              {!phone && `${pct}%`}
            </button>
          }
        />
      </Tip>
      <PopSurface side="top" align="end" className="w-[min(300px,calc(100vw-16px))] p-3.5">
        <div className="flex items-baseline justify-between">
          <span className="text-sm font-semibold">{t("ctx.title")}</span>
          <span className="text-xs text-ink-3 tabular-nums">{t("ctx.used", { used: k(c.used), total: k(c.max) })}</span>
        </div>
        {parts.length > 1 && (
          <>
            <div className="mt-2.5 flex h-2 gap-[2px]" role="img" aria-label={parts.map((p) => `${p.name} ${k(p.tokens)}`).join(", ")}>
              {parts.map((p, i) => (
                <Tip key={p.key} label={`${p.name} · ${k(p.tokens)}`}>
                  <span
                    className={cn("g-fade h-full", i === 0 && "rounded-l-full", i === parts.length - 1 && "rounded-r-full")}
                    style={{ flexGrow: p.tokens, flexBasis: 0, minWidth: 3, background: p.tone, animationDelay: `${i * 20}ms` } as CSSProperties}
                  />
                </Tip>
              ))}
            </div>
            <ul className="m-0 mt-3 grid list-none gap-1 p-0">
              {parts.map((p) => (
                <li key={p.key} className="flex items-center gap-2 text-sm">
                  <span className="size-2 shrink-0 rounded-full" style={{ background: p.tone, boxShadow: p.ring ? "inset 0 0 0 1px var(--ink-4)" : undefined }} />
                  <span className="flex-1 text-ink-2">{p.name}</span>
                  <span className="text-ink tabular-nums">{k(p.tokens)}</span>
                </li>
              ))}
            </ul>
          </>
        )}
        {hit && tokens && (
          <div className="mt-3 border-t border-edge pt-2.5">
            <div className="flex items-baseline justify-between text-sm">
              <span className="text-ink-2">{t("ctx.cacheHit")}</span>
              <span className="text-ink tabular-nums">{hit}</span>
            </div>
            <div className="mt-0.5 text-xs text-ink-3 tabular-nums">{t("ctx.cacheParts", { read: tok(tokens.cacheRead), write: tok(tokens.cacheWrite), input: tok(tokens.input) })}</div>
          </div>
        )}
      </PopSurface>
    </Popover.Root>
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
export function QuotaBars({ agent, machine, live, using, open, onOpenChange }: { agent: Agent; machine?: string; live: boolean; using: (string | undefined)[]; open?: boolean; onOpenChange?: (o: boolean) => void }) {
  const lang = useLang();
  const phone = useMedia(PHONE);
  useMinute(); // "read 5 min ago" and the fading, again each minute
  const quota = useQuota();
  // Held down (a finger), or the right button: the usage, at the bars (the user, 2026-11-08).
  const key = useRef<HTMLButtonElement>(null);
  const toUsage = (at?: { x: number; y: number }) => {
    const b = key.current?.getBoundingClientRect();
    onOpenChange?.(false);
    openUsage(at ?? (b && { x: b.left, y: b.top - 8 }));
  };
  const lp = useLongPress(() => toUsage());
  const hold = { ...lp, onContextMenu: (e: MouseEvent) => (e.preventDefault(), toUsage({ x: e.clientX, y: e.clientY })) };
  const q: AgentQuota | undefined = quota?.agents.filter((a) => a.agent === agent && (a.machine ?? "") === (machine ?? "")).sort((a, b) => b.at - a.at)[0];
  const windows = (q?.windows ?? []).filter((w) => windowFor(w, using)); // all shown: no setting hides one (the user, 2026-11-06)
  // dsh has no windows but money: its balance where the bars would be (the user, 2026-11-08).
  const balance = q?.balance ?? [];
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
            <button ref={key} type="button" aria-label={label} {...hold} className="g-press flex h-7 shrink-0 items-center rounded-md px-1.5 select-none hover:bg-fill data-popup-open:bg-fill pointer-coarse:h-9 [-webkit-touch-callout:none]">
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

/** The models an agent offers when the session does not list them. */
const MODELS: Partial<Record<Agent, Model[]>> = {
  claude: [
    { value: "default", name: "Default" },
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

type ModelOf = Pick<LiveSession, "agent" | "model" | "pick" | "models" | "efforts" | "effort" | "effortNow">;

/** What the model key shows, from the session's state (want: a model just picked, not yet in it).
 *  cur: the entry in use, by value, else by the full id an alias resolves to (the default entry only
 *  when no alias has it). name: its name; for the default entry, or nothing set yet, the concrete
 *  model (the alias it resolves to, else its description's head, "Opus 5.5 · ..."); else the id as
 *  the agent named it; "" when nothing is known. isDefault: nothing set, the agent's own. value: the
 *  menu's tick. levels: the efforts the model takes (none: it takes none), else the agent's.
 *  level: the one in use: set, if this model takes it, else the agent's own. */
export function modelView(s: ModelOf, want?: string | null) {
  const models = s.models ?? MODELS[s.agent] ?? [];
  noteLevelNames(models);
  const pick = want ?? s.pick ?? "";
  const now = want ?? s.model ?? "";
  const def = models.find((m) => m.value === "default");
  const resolvedTo = (id?: string) => (id ? (models.find((m) => m.value !== "default" && m.resolved === id) ?? models.find((m) => m.resolved === id)) : undefined);
  const isDefault = !pick || pick === "default";
  // The value set, while what is in use is it (two aliases may resolve to one id).
  const picked = isDefault ? undefined : models.find((m) => m.value === pick && (!now || now === pick || !m.resolved || m.resolved === now));
  const cur = picked ?? ((now && (models.find((m) => m.value === now) ?? resolvedTo(now) ?? models.find((m) => m.name === now))) || (isDefault && !now ? def : undefined));
  const head = (d?: string) => d?.split(" · ")[0]?.trim() || undefined;
  let name = cur?.name ?? now;
  if (cur && cur === def) {
    const alias = cur.resolved ? models.find((m) => m.value !== "default" && m.resolved === cur.resolved) : undefined;
    name = alias?.name ?? head(cur.description) ?? (cur.resolved || (s.models ? cur.name : ""));
  }
  const value = isDefault && def ? "default" : (cur?.value ?? pick ?? now);
  const levels = cur?.efforts?.length ? cur.efforts : cur?.noEffort ? [] : (s.efforts ?? []);
  // The level set, while this model takes it; else the one the agent says it runs at.
  const level = (s.effort && (!levels.length || levels.includes(s.effort)) ? s.effort : s.effortNow) || "";
  return { models, cur, name, isDefault, value, levels, level };
}

// Features the page names itself, an agent's own (Claude's UltraCode): their words from i18n, not
// the server's. A plugin's (one of DSH Desktop's, where Desktop holds the session) come with their own.
const NATIVE: Record<string, { name: () => string; tip: (f: Feature) => string; err: Key }> = {
  "claude.ultracode": {
    name: () => t("composer.ultracode"),
    tip: (f) => t(!f.available ? (f.on ? "composer.ultracodeIdle" : "composer.ultracodeUnavailable") : "composer.ultracodeHint"),
    err: "err.ultracode",
  },
};

/** The features at the effort track: the session's; before its agent runs (none yet), the ones its
 *  agent has, off (Claude's UltraCode). */
function effortFeatures(s: LiveSession, info?: AgentInfo): Feature[] {
  const fs = (s.features ?? []).filter((f) => f.place === "effort");
  const ids = info?.features ?? (info?.ultracode ? ["claude.ultracode"] : []);
  for (const id of ids) {
    const n = NATIVE[id];
    if (n && !fs.some((f) => f.id === id)) fs.push({ id, name: n.name(), on: false, available: true, place: "effort", tone: "warm" });
  }
  return fs;
}

type FeatureWant = { on: boolean; option?: string };

/** The model, its thinking level and its features (UltraCode), under the composer, each in use at
 *  once. A model picked closes the menu; the level and a feature at its foot are tuned in place, it
 *  stays. */
export function ModelKey({ s, open, onOpenChange }: { s: LiveSession; open?: boolean; onOpenChange?: (o: boolean) => void }) {
  useLang();
  const [want, setWant] = useState<string | null>(null);
  const [featWant, setFeatWant] = useState<Record<string, FeatureWant>>({});
  const info = useAgent(s.agent);
  if (want !== null && (s.pick === want || s.model === want)) setWant(null);
  // A switch flipped shows at once, till the session says it is so (or the switch failed).
  const base = effortFeatures(s, info);
  const feats = base.map((f) => {
    const w = featWant[f.id];
    return w ? { ...f, on: w.on, option: w.option ?? f.option } : f;
  });
  const reached = Object.entries(featWant).filter(([id, w]) => {
    const f = base.find((x) => x.id === id);
    return !f || (f.on === w.on && (w.option === undefined || f.option === w.option));
  }).map(([id]) => id);
  if (reached.length) setFeatWant((m) => Object.fromEntries(Object.entries(m).filter(([id]) => !reached.includes(id))));
  const v = modelView(s, want);
  const { models, levels, level } = v;
  const grouped = new Set(models.map((m) => m.group).filter(Boolean)).size > 1;
  if (!models.length && !v.name) return null;
  const label = v.name || t("composer.defaultModel");
  const pickModel = async (model: string) => {
    setWant(model);
    try {
      await api.model(s.id, model);
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
  const flip = async (f: Feature, u: FeatureWant) => {
    setFeatWant((m) => ({ ...m, [f.id]: u }));
    try {
      await api.setFeature(s.id, f.id, u.option ? { on: u.on, option: u.option } : { on: u.on });
    } catch (e) {
      setFeatWant((m) => Object.fromEntries(Object.entries(m).filter(([id]) => id !== f.id)));
      toast(t(NATIVE[f.id]?.err ?? "err.feature", { name: f.name }), { error: true, description: errText(e) });
    }
  };
  const nameOf = (f: Feature) => NATIVE[f.id]?.name() ?? f.name;
  const tone = (f: Feature) => (f.tone === "warm" ? "text-[#ff4a17]" : "text-ink");
  // The first one at the track's right (more have no place yet).
  const feat = feats[0];
  const tip = feat && [nameOf(feat), NATIVE[feat.id]?.tip(feat) ?? (feat.available ? feat.hint : feat.reason)].filter(Boolean).join(" · ");
  // UltraCode: one key, its mark, pressed or not (the user, 2026-11-06); lit warm when on. A
  // plugin's the same (the user, 2026-11-09). On but not available: still switched off here (UltraCode
  // on a model that cannot run it); a plugin's not (Desktop is not holding the session).
  const featKey = feat && (
    <Tip label={tip}>
      <button
        type="button"
        aria-pressed={feat.on}
        aria-label={nameOf(feat)}
        disabled={!feat.available && !(feat.on && NATIVE[feat.id])}
        onClick={() => void flip(feat, { on: !feat.on })}
        className={cn("g-press grid size-7 shrink-0 place-items-center rounded-md transition-colors duration-(--dur-2) disabled:opacity-40 pointer-coarse:size-10", feat.on ? cn("bg-fill hover:bg-fill-2", tone(feat)) : "text-ink-4 hover:bg-fill hover:text-ink-2")}
      >
        <Workflow size={16} aria-hidden className={cn("transition-transform duration-(--dur-2) ease-(--ease-spring)", feat.on && "scale-110")} />
      </button>
    </Tip>
  );
  // On: said in words beside the level too (the user, 2026-11-06); on but not in effect (this model
  // cannot run it), muted.
  const featTag = feat?.on && <span className={cn("g-fade text-xs font-medium", feat.active || feat.available ? tone(feat) : "text-ink-4")}>{nameOf(feat)}</span>;
  // On, its options a row under the track (a plugin's sub-modes; the user, 2026-11-09).
  const featOptions = feat?.on && !!feat.options?.length && (
    <div className="g-fade flex items-center gap-1 px-2.5 pb-2">
      {feat.options.map((o) => (
        <button
          key={o.id}
          type="button"
          onClick={() => void flip(feat, { on: true, option: o.id })}
          className={cn("g-press h-7 flex-1 rounded-md text-xs transition-colors duration-(--dur-2) pointer-coarse:h-9", feat.option === o.id ? "bg-fill-2 font-medium text-ink" : "text-ink-3 hover:bg-fill hover:text-ink-2")}
        >
          {o.name}
        </button>
      ))}
    </div>
  );
  const on = feats.filter((f) => f.on);
  return (
    <Menu.Root open={open} onOpenChange={onOpenChange}>
      <Tip label={t("composer.model")}>
        <Menu.Trigger
          disabled={!models.length}
          render={
            <button type="button" aria-label={`${t("composer.model")}: ${label}${level ? ` · ${levelName(level)}` : ""}${on.map((f) => ` · ${nameOf(f)}`).join("")}`} className="g-press flex h-7 min-w-0 items-center gap-1 rounded-md px-2 text-xs text-ink-3 hover:bg-fill hover:text-ink data-popup-open:bg-fill disabled:hover:bg-transparent pointer-coarse:h-9">
              {/* No icon: the model's name says what this is. */}
              <span key={label} className="g-fade truncate">
                {label}
              </span>
              {level && levels.length > 0 && (
                <span key={level} className="g-fade shrink-0 text-ink-4">
                  {levelName(level)}
                </span>
              )}
              {on.length > 0 && <Workflow size={12} aria-hidden className={cn("g-pop shrink-0", on.some((f) => f.active) ? "text-ink-2" : "text-ink-4")} />}
              {models.length > 0 && <ChevronDown size={12} className="shrink-0 text-ink-4" />}
            </button>
          }
        />
      </Tip>
      <Menu.Portal>
        <Menu.Positioner side="top" align="start" sideOffset={6} collisionPadding={8} className="z-50">
          <Menu.Popup finalFocus={false} className="g-unfold glass thick float flex max-h-[min(640px,calc(var(--available-height)-40px))] w-72 max-w-[calc(100vw-16px)] flex-col rounded-lg p-1.5 text-ink outline-none">
            {/* The models scroll inside, the foot stays; as high as there is room above, short of the
                top by 40px, at most 640px (the user, 2026-11-06). */}
            <Scrolled className="-mr-1 min-h-0 pr-1">
              <Menu.RadioGroup value={v.value} onValueChange={(x) => void pickModel(x as string)} className="g-stagger flex flex-col">
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
            {(levels.length > 1 || feat) && (
              // The foot: tuned in place, never closing the menu; the model's own levels drawn anew.
              // UltraCode a switch at the track's right, not a row of its own (the user, 2026-11-06).
              <div className="mt-1 shrink-0 border-t border-(--edge) pt-0.5">
                {levels.length > 1 ? (
                  <>
                    <EffortSlider key={levels.join(" ")} levels={levels} value={level || undefined} set={setEffort} aside={featKey} tag={featTag} />
                    {featOptions}
                  </>
                ) : (
                  <>
                    <div className="flex h-10 items-center justify-between gap-3 px-2.5">
                      <span className="text-xs text-ink-3">{feat && nameOf(feat)}</span>
                      {featKey}
                    </div>
                    {featOptions}
                  </>
                )}
              </div>
            )}
          </Menu.Popup>
        </Menu.Positioner>
      </Menu.Portal>
    </Menu.Root>
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
const presetName = (id: string, p?: Preset) => p?.name || (PRESET_KEY[id] ? t(PRESET_KEY[id][0]) : id);
const presetHint = (id: string, p?: Preset) => (p?.name ? p.description : PRESET_KEY[id] ? t(PRESET_KEY[id][1]) : p?.description);

/** The agent preset (dsh: the tools and prompt it runs with), where the quota bars would be: dsh
 *  has no quota (the user, 2026-11-06). Those the dsh where the session runs registers (its
 *  machine's DSH Desktop's own, the user, 2026-11-08). Taken at launch: picked on a running agent,
 *  it restarts and the session goes on. Picked before the first message only, as in dsh's own app:
 *  a started session's preset is a label (the server refuses a switch; internal/session SetPreset). */
export function PresetKey({ s }: { s: LiveSession }) {
  useLang();
  const q = useQuery({ queryKey: ["presets", s.id], queryFn: () => api.presets(s.id), staleTime: 30_000, retry: false });
  const [want, setWant] = useState<string | null>(null);
  const list = q.data?.presets ?? [];
  const def = q.data?.default ?? "";
  const now = want ?? (s.preset || def || list[0]?.id || "");
  if (want !== null && (s.preset || def) === want) setWant(null);
  const byId = (id: string) => list.find((p) => p.id === id);
  if (!now) return null;
  const pick = async (p: string) => {
    if (p === now) return;
    setWant(p);
    try {
      await api.preset(s.id, p === def ? "" : p);
      if (s.live) toast(t("preset.restart"));
    } catch (e) {
      setWant(null);
      toast(t("err.preset"), { error: true, description: errText(e) });
    }
  };
  const label = presetName(now, byId(now));
  if (s.last || !list.length)
    return (
      <Tip label={t("preset.fixed")}>
        <span className="flex h-7 min-w-0 shrink-0 cursor-default items-center rounded-md px-2 text-xs text-ink-3 select-none pointer-coarse:h-9">
          <span className="truncate">{label}</span>
        </span>
      </Tip>
    );
  return (
    <Menu.Root>
      <Tip label={t("composer.preset")}>
        <Menu.Trigger
          render={
            <button type="button" aria-label={`${t("composer.preset")}: ${label}`} className="g-press flex h-7 min-w-0 shrink-0 items-center gap-1 rounded-md px-2 text-xs text-ink-3 hover:bg-fill hover:text-ink data-popup-open:bg-fill pointer-coarse:h-9">
              <span key={label} className="g-fade truncate">
                {label}
              </span>
              <ChevronDown size={12} className="shrink-0 text-ink-4" />
            </button>
          }
        />
      </Tip>
      <Menu.Portal>
        <Menu.Positioner side="top" align="start" sideOffset={6} collisionPadding={8} className="z-50">
          <Menu.Popup finalFocus={false} className="g-unfold glass thick float flex w-72 max-w-[calc(100vw-16px)] flex-col rounded-lg p-1.5 text-ink outline-none">
            <Menu.RadioGroup value={now} onValueChange={(x) => void pick(x as string)} className="g-stagger flex flex-col">
              {list.map(({ id }, i) => (
                <Menu.RadioItem key={id} value={id} closeOnClick style={{ "--i": i } as CSSProperties} className="flex min-h-8 cursor-default items-center gap-2.5 rounded-md pr-2 pl-2.5 text-base outline-none select-none data-highlighted:bg-fill pointer-coarse:min-h-11">
                  <span className="flex min-w-0 flex-1 flex-col py-1">
                    <span className="truncate">{presetName(id, byId(id))}</span>
                    {presetHint(id, byId(id)) && <span className="text-xs text-ink-3">{presetHint(id, byId(id))}</span>}
                  </span>
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
