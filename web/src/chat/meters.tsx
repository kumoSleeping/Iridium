// The composer's meters (plan "网页"), in a row under it: the approval-mode key, the model key, the
// quota bars after them, a bar and its percent per window, amber nearly full, red full, faded when
// last read long ago or no process runs, each window in a popover; at the right the context ring,
// its breakdown in a popover.
import { Menu } from "@base-ui/react/menu";
import { Popover } from "@base-ui/react/popover";
import { Check, ChevronDown, FilePen, ListChecks, ShieldCheck, ShieldOff, ShieldQuestion, ShieldX, SlidersHorizontal, Sparkles, Workflow } from "lucide-react";
import { useLayoutEffect, useRef, useState, type CSSProperties, type ReactNode } from "react";
import type { SessionState as LiveSession } from "../api/types.gen";
import { api } from "../api/client";
import type { Agent, AgentQuota, Context, ContextCategory, Model } from "../api/types.gen";
import { useAgent, useSettings } from "../data";
import { t, useLang } from "../i18n";
import { useContextState, useQuota } from "../store";
import { errText, toast } from "../toast";
import { ago, cn, PopSurface, Switch, Tip, useMinute } from "../ui";
import { EffortSlider, levelName } from "./effort";
import { modeName } from "../modes";
import { quotaKey, windowName } from "../quota";

const k = (n: number) => (n >= 100_000 ? `${Math.round(n / 1000)}k` : n >= 1000 ? `${(n / 1000).toFixed(1)}k` : `${n}`);

/** A category's name in the page's language, for the names Claude gives; any other as given. */
function ctxName(c: ContextCategory): string {
  const n = `${c.id} ${c.name}`.toLowerCase();
  if (c.kind === "free" || n.includes("free")) return t("ctx.free");
  if (c.kind === "buffer" || n.includes("buffer") || n.includes("compact")) return t("ctx.reserve");
  if (n.includes("mcp")) return t("ctx.mcp");
  if (n.includes("memory")) return t("ctx.memory");
  if (n.includes("agent")) return t("ctx.agents");
  if (n.includes("tool")) return t("ctx.tools");
  if (n.includes("system")) return t("ctx.system");
  if (n.includes("message")) return t("ctx.messages");
  return c.name;
}

const RAMP = ["var(--ctx-1)", "var(--ctx-2)", "var(--ctx-3)", "var(--ctx-4)", "var(--ctx-5)"];
const HATCH = "repeating-linear-gradient(135deg, var(--ink-4) 0 1.5px, transparent 1.5px 4px)";

/** The parts in the bar's order: what is used, largest first, the darkest; then the reserve, then free. */
type Part = { key: string; name: string; tokens: number; tone: string; ring?: boolean };
export function ctxParts(c: Context): Part[] {
  const cats = c.categories ?? [];
  const used = cats.filter((x) => x.kind === "used" && x.tokens > 0).sort((a, b) => b.tokens - a.tokens);
  const parts: Part[] = used.map((x, i) => ({ key: x.id || x.name, name: ctxName(x), tokens: x.tokens, tone: RAMP[Math.min(i, RAMP.length - 1)]! }));
  for (const x of cats.filter((x) => x.kind === "buffer" && x.tokens > 0)) parts.push({ key: x.id || x.name, name: ctxName(x), tokens: x.tokens, tone: HATCH, ring: true });
  const free = cats.find((x) => x.kind === "free");
  const freeTokens = free?.tokens ?? (c.max ? Math.max(0, c.max - c.used - parts.filter((p) => p.ring).reduce((n, p) => n + p.tokens, 0)) : 0);
  if (freeTokens > 0) parts.push({ key: "free", name: t("ctx.free"), tokens: freeTokens, tone: "var(--fill-2)", ring: true });
  return parts;
}

export function ContextRing({ session, summary, open, onOpenChange }: { session: string; summary?: Context; open?: boolean; onOpenChange?: (o: boolean) => void }) {
  useLang();
  // The breakdown when the stream carries one; the total the session's state has, else.
  const c = useContextState(session) ?? summary;
  if (!c || !c.max) return null;
  const pct = Math.min(100, Math.round((c.used / c.max) * 100));
  const r = 7;
  const circ = 2 * Math.PI * r;
  const label = `${t("ctx.title")} · ${t("ctx.used", { used: k(c.used), total: k(c.max) })}`;
  const parts = ctxParts(c);
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
              {pct}%
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
 *  (the user, 2026-11-06). /usage in the composer opens it too, with nothing read yet as well. */
export function QuotaBars({ agent, live, open, onOpenChange }: { agent: Agent; live: boolean; open?: boolean; onOpenChange?: (o: boolean) => void }) {
  const lang = useLang();
  useMinute(); // "read 5 min ago" and the fading, again each minute
  const quota = useQuota();
  const hidden = useSettings().data?.quotaHidden ?? [];
  const q: AgentQuota | undefined = quota?.agents.filter((a) => a.agent === agent).sort((a, b) => b.at - a.at)[0];
  const windows = (q?.windows ?? []).filter((w) => !hidden.includes(quotaKey(q!, w)));
  if (!q || !windows.length)
    // No bars, but /usage still opens the popover, from where they would be.
    return (
      <Popover.Root open={open} onOpenChange={onOpenChange}>
        <Popover.Trigger nativeButton={false} render={<span aria-hidden className="h-7 w-0 shrink-0 pointer-coarse:h-9" />} />
        <PopSurface side="top" align="start" className="w-[min(300px,calc(100vw-16px))] p-3.5">
          <span className="text-sm font-semibold">{t("quota.title")}</span>
          <p className="m-0 mt-2 text-sm text-ink-3">{t("quota.none")}</p>
        </PopSurface>
      </Popover.Root>
    );
  // Faded once no process of the agent runs (the daemon says, else this session), or read long ago.
  const idle = !(q.live ?? live);
  const stale = idle || Date.now() - q.at > STALE;
  const label = `${t("quota.title")}: ${windows.map((w) => `${windowName(agent, w)} ${Math.round(w.percent)}%`).join(", ")}`;
  return (
    <Popover.Root open={open} onOpenChange={onOpenChange}>
      <Tip label={t("quota.title")}>
        <Popover.Trigger
          render={
            <button type="button" aria-label={label} className="g-press flex h-7 shrink-0 items-center rounded-md px-1.5 hover:bg-fill data-popup-open:bg-fill pointer-coarse:h-9">
              <span className={cn("flex items-center gap-3 transition-opacity", stale && "opacity-50")}>
                {windows.map((w) => {
                  const pct = Math.round(w.percent);
                  const [fill, text] = quotaTone(pct);
                  return (
                    <span key={quotaKey(q, w)} className="flex items-center gap-1.5">
                      <span className="relative h-1 w-9 overflow-hidden rounded-full bg-fill-2">
                        <span className={cn("absolute inset-y-0 left-0 w-full origin-left rounded-full transition-transform duration-(--dur-3)", fill)} style={{ transform: `scaleX(${Math.min(100, pct) / 100})` }} />
                      </span>
                      <span className={cn("text-xs tabular-nums", text)}>{pct}%</span>
                    </span>
                  );
                })}
              </span>
            </button>
          }
        />
      </Tip>
      <PopSurface side="top" align="start" className="w-[min(300px,calc(100vw-16px))] p-3.5">
        <div className="flex items-baseline justify-between gap-3">
          <span className="text-sm font-semibold">{t("quota.title")}</span>
          {q.account && <span className="min-w-0 truncate text-xs text-ink-3">{q.account}</span>}
        </div>
        <ul className="m-0 mt-3 grid list-none gap-3 p-0">
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
        <p className={cn("m-0 mt-3 text-xs", stale ? "text-ink-4" : "text-ink-3")}>
          {t("quota.read", { ago: ago(q.at) })}
          {idle && ` · ${t("quota.idle")}`}
        </p>
      </PopSurface>
    </Popover.Root>
  );
}

/** An agent's approval modes, in the order the key cycles them: as GET /api/agents lists them, else
 *  Claude's own. */
const MODES: Partial<Record<Agent, string[]>> = { claude: ["manual", "acceptEdits", "plan", "auto", "dontAsk", "bypassPermissions"], codex: ["untrusted", "on-request", "granular", "auto"], pi: ["auto", "ask"] };
type Mode = { id: string };

const MODE_ICON: Record<string, ReactNode> = {
  default: <ShieldQuestion size={14} />,
  manual: <ShieldQuestion size={14} />,
  ask: <ShieldQuestion size={14} />,
  untrusted: <ShieldQuestion size={14} />,
  "on-request": <ShieldCheck size={14} />,
  granular: <SlidersHorizontal size={14} />,
  acceptEdits: <FilePen size={14} />,
  plan: <ListChecks size={14} />,
  auto: <Sparkles size={14} />,
  dontAsk: <ShieldX size={14} />,
  bypassPermissions: <ShieldOff size={14} />,
  never: <ShieldOff size={14} />,
};
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
      // Not every agent switches at once (GET /api/agents modeSwitch): say when it does.
      if (info?.modeSwitch === "next turn") toast(t("mode.nextTurn"));
      else if (info?.modeSwitch === "restart") toast(t("mode.restart"));
    } catch (e) {
      setWant(null);
      toast(t("err.mode"), { error: true, description: errText(e) });
    }
  };
  const cycle = () => modes.length > 1 && void set(modes[(modes.findIndex((m) => m.id === now) + 1) % modes.length]!.id);
  return { modes, now, set, cycle };
}

/** The approval mode, under the composer; a mode picked closes the menu. */
export function ModeKey({ mode, open, onOpenChange }: { mode: ReturnType<typeof useMode>; open?: boolean; onOpenChange?: (o: boolean) => void }) {
  useLang();
  if (!mode.modes.length) return null;
  const cur = mode.modes.find((m) => m.id === mode.now) ?? { id: mode.now };
  return (
    <Menu.Root open={open} onOpenChange={onOpenChange}>
      <Tip label={`${t("composer.mode")}: ${modeName(cur)}`}>
        <Menu.Trigger
          render={
            <button type="button" aria-label={`${t("composer.mode")}: ${modeName(cur)}`} className="g-press flex h-7 shrink-0 items-center gap-1.5 rounded-md px-2 text-xs whitespace-nowrap text-ink-2 hover:bg-fill hover:text-ink data-popup-open:bg-fill pointer-coarse:h-9">
              {MODE_ICON[cur.id] ?? <ShieldQuestion size={14} />}
              {/* Its name only where the composer has room; narrow, the icon says it, the tip names it
                  (the user, 2026-11-06). */}
              <span key={cur.id} className="g-fade hidden @min-[640px]:inline">
                {modeName(cur)}
              </span>
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
                  <span className="text-ink-2">{MODE_ICON[m.id] ?? <ShieldQuestion size={14} />}</span>
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
    <div ref={ref} onScroll={look} className={cn("relative overflow-y-auto overscroll-contain", className)} style={{ maskImage: mask, WebkitMaskImage: mask }}>
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

/** The model, its thinking level and UltraCode, under the composer, each in use at once. A model
 *  picked closes the menu; the level and UltraCode at its foot are tuned in place, it stays. */
export function ModelKey({ s, open, onOpenChange }: { s: LiveSession; open?: boolean; onOpenChange?: (o: boolean) => void }) {
  useLang();
  const [want, setWant] = useState<string | null>(null);
  const [ultraWant, setUltraWant] = useState<boolean | null>(null);
  const info = useAgent(s.agent);
  if (want !== null && (s.pick === want || s.model === want)) setWant(null);
  const ultra = s.ultracode ?? (info?.ultracode ? { on: false, active: false, available: true } : undefined);
  if (ultraWant !== null && ultra?.on === ultraWant) setUltraWant(null);
  const v = modelView(s, want);
  const { models, levels, level } = v;
  if (!models.length && !v.name) return null;
  const label = v.name || t("composer.defaultModel");
  const ultraOn = ultraWant ?? ultra?.on ?? false;
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
  const flip = async (on: boolean) => {
    setUltraWant(on);
    try {
      await api.ultracode(s.id, on);
    } catch (e) {
      setUltraWant(null);
      toast(t("err.ultracode"), { error: true, description: errText(e) });
    }
  };
  const ultraTip = ultra && `UltraCode · ${!ultra.available ? t(ultraOn ? "composer.ultracodeIdle" : "composer.ultracodeUnavailable") : t("composer.ultracodeHint")}`;
  const ultraKey = ultra && (
    <Tip label={ultraTip}>
      <span className="flex shrink-0 items-center gap-1.5">
        <Workflow size={14} aria-hidden className={cn("shrink-0", ultraOn ? "text-ink-2" : "text-ink-4")} />
        <Switch on={ultraOn} disabled={!ultra.available && !ultraOn} label={t("composer.ultracode")} onChange={(on) => void flip(on)} className="liquid" />
      </span>
    </Tip>
  );
  // UltraCode on: said in words beside the level too (the user, 2026-11-06); on but not in effect
  // (this model cannot run it), muted.
  const ultraTag = ultraOn && (
    <span className="g-fade flex items-baseline gap-1.5 text-xs font-medium">
      <span className={ultra?.active || ultra?.available ? "liquid-text" : "text-ink-4"}>UltraCode</span>
      <span aria-hidden className="text-ink-4">·</span>
    </span>
  );
  return (
    <Menu.Root open={open} onOpenChange={onOpenChange}>
      <Tip label={t("composer.model")}>
        <Menu.Trigger
          disabled={!models.length}
          render={
            <button type="button" aria-label={`${t("composer.model")}: ${label}${level ? ` · ${levelName(level)}` : ""}${ultraOn ? " · UltraCode" : ""}`} className="g-press flex h-7 min-w-0 items-center gap-1 rounded-md px-2 text-xs text-ink-3 hover:bg-fill hover:text-ink data-popup-open:bg-fill disabled:hover:bg-transparent pointer-coarse:h-9">
              {/* No icon: the mode key has the same sparkle; the model's name says what this is. */}
              <span key={label} className="g-fade truncate">
                {label}
              </span>
              {level && levels.length > 0 && (
                <span key={level} className="g-fade shrink-0 text-ink-4">
                  {levelName(level)}
                </span>
              )}
              {ultraOn && <Workflow size={12} aria-hidden className={cn("g-pop shrink-0", ultra?.active ? "text-ink-2" : "text-ink-4")} />}
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
                  // A pick closes the menu (Base UI keeps a radio item's open by default).
                  <Menu.RadioItem key={m.value} value={m.value} closeOnClick style={{ "--i": i } as CSSProperties} className="flex min-h-8 cursor-default items-center gap-2.5 rounded-md pr-2 pl-2.5 text-base outline-none select-none data-highlighted:bg-fill pointer-coarse:min-h-11">
                    <span className="flex min-w-0 flex-1 flex-col py-1">
                      <span className="truncate">{m.name}</span>
                      {m.description && <span className="truncate text-xs text-ink-3">{m.description}</span>}
                    </span>
                    <Menu.RadioItemIndicator className="text-ink">
                      <Check size={14} />
                    </Menu.RadioItemIndicator>
                  </Menu.RadioItem>
                ))}
              </Menu.RadioGroup>
            </Scrolled>
            {(levels.length > 1 || ultra) && (
              // The foot: tuned in place, never closing the menu; the model's own levels drawn anew.
              // UltraCode a switch at the track's right, not a row of its own (the user, 2026-11-06).
              <div className="mt-1 shrink-0 border-t border-(--edge) pt-0.5">
                {levels.length > 1 ? (
                  <EffortSlider key={levels.join(" ")} levels={levels} value={level || undefined} set={setEffort} aside={ultraKey} tag={ultraTag} />
                ) : (
                  <div className="flex h-10 items-center justify-between gap-3 px-2.5">
                    <span className="text-xs text-ink-3">{t("composer.ultracode")}</span>
                    {ultraKey}
                  </div>
                )}
              </div>
            )}
          </Menu.Popup>
        </Menu.Positioner>
      </Menu.Portal>
    </Menu.Root>
  );
}
