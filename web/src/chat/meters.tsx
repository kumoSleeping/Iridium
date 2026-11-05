// The composer's meters (plan "网页"), in a row under it: the approval-mode key, the model key, the
// quota bars after them, a bar and its percent per window, amber nearly full, red full, faded when
// last read long ago or no process runs; at the right the context ring, its breakdown in a popover.
import { Menu } from "@base-ui/react/menu";
import { Popover } from "@base-ui/react/popover";
import { Check, ChevronDown, FilePen, ListChecks, ShieldCheck, ShieldOff, ShieldQuestion, ShieldX, SlidersHorizontal, Sparkles } from "lucide-react";
import { useState, type CSSProperties, type ReactNode } from "react";
import type { SessionState as LiveSession } from "../api/types.gen";
import { api } from "../api/client";
import type { Agent, AgentQuota, Context, ContextCategory, Model } from "../api/types.gen";
import { useAgent, useSettings } from "../data";
import { t, useLang } from "../i18n";
import { useContextState, useQuota } from "../store";
import { errText, toast } from "../toast";
import { cn, PopSurface, Tip } from "../ui";
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

/** The agent's quota bars, after the model key in the row under the composer. */
export function QuotaBars({ agent, live }: { agent: Agent; live: boolean }) {
  const lang = useLang();
  const quota = useQuota();
  const hidden = useSettings().data?.quotaHidden ?? [];
  const q: AgentQuota | undefined = quota?.agents.filter((a) => a.agent === agent).sort((a, b) => b.at - a.at)[0];
  const windows = (q?.windows ?? []).filter((w) => !hidden.includes(quotaKey(q!, w)));
  if (!q || !windows.length) return null;
  // Faded once no process of the agent runs (the daemon says, else this session), or read long ago.
  const stale = !(q.live ?? live) || Date.now() - q.at > STALE;
  return (
    <div className={cn("flex shrink-0 items-center gap-3 px-1.5 transition-opacity", stale && "opacity-50")}>
      {windows.map((w) => {
        const pct = Math.round(w.percent);
        const label = `${windowName(agent, w)} · ${t("quota.tip", { pct, time: resetIn(w.resetsAt, lang) })}`;
        const tone = pct >= 95 ? "bg-err" : pct >= 80 ? "bg-need" : "bg-ink-3";
        return (
          <Tip key={quotaKey(q, w)} label={label}>
            <span role="meter" aria-label={label} aria-valuenow={pct} aria-valuemin={0} aria-valuemax={100} className="relative flex h-7 items-center gap-1.5 pointer-coarse:h-9">
              <span className="relative h-1 w-9 overflow-hidden rounded-full bg-fill-2">
                <span className={cn("absolute inset-y-0 left-0 w-full origin-left rounded-full transition-transform duration-(--dur-3)", tone)} style={{ transform: `scaleX(${Math.min(100, pct) / 100})` }} />
              </span>
              <span className={cn("text-xs tabular-nums", pct >= 95 ? "text-err" : pct >= 80 ? "text-need" : "text-ink-3")}>{pct}%</span>
            </span>
          </Tip>
        );
      })}
    </div>
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

export function ModeKey({ mode, open, onOpenChange }: { mode: ReturnType<typeof useMode>; open?: boolean; onOpenChange?: (o: boolean) => void }) {
  useLang();
  if (!mode.modes.length) return null;
  const cur = mode.modes.find((m) => m.id === mode.now) ?? { id: mode.now };
  return (
    <Menu.Root open={open} onOpenChange={onOpenChange}>
      <Tip label={t("composer.mode")}>
        <Menu.Trigger
          render={
            <button type="button" aria-label={`${t("composer.mode")}: ${modeName(cur)}`} className="g-press flex h-7 items-center gap-1.5 rounded-md px-2 text-xs text-ink-2 hover:bg-fill hover:text-ink data-popup-open:bg-fill pointer-coarse:h-9">
              {MODE_ICON[cur.id] ?? <ShieldQuestion size={14} />}
              <span key={cur.id} className="g-fade">
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
                <Menu.RadioItem key={m.id} value={m.id} style={{ "--i": i } as CSSProperties} className="flex h-8 cursor-default items-center gap-2.5 rounded-md pr-2 pl-2.5 text-base outline-none select-none data-highlighted:bg-fill pointer-coarse:h-11">
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

/** The model and its thinking level, under the composer; picked, in use at once. */
export function ModelKey({ s, open, onOpenChange }: { s: LiveSession; open?: boolean; onOpenChange?: (o: boolean) => void }) {
  useLang();
  const [want, setWant] = useState<string | null>(null);
  const models = s.models ?? MODELS[s.agent] ?? [];
  if (want !== null && s.model === want) setWant(null);
  const now = want ?? s.model ?? "";
  const cur = models.find((m) => m.value === now || m.name === now);
  const efforts = s.efforts ?? [];
  if (!models.length && !now) return null;
  const set = async (p: { model?: string; effort?: string }) => {
    if (p.model) setWant(p.model);
    try {
      if (p.model !== undefined) await api.model(s.id, p.model);
      if (p.effort !== undefined) await api.effort(s.id, p.effort);
    } catch (e) {
      setWant(null);
      toast(t("err.model"), { error: true, description: errText(e) });
    }
  };
  return (
    <Menu.Root open={open} onOpenChange={onOpenChange}>
      <Tip label={t("composer.model")}>
        <Menu.Trigger
          disabled={!models.length}
          render={
            <button type="button" aria-label={`${t("composer.model")}: ${cur?.name ?? now}`} className="g-press flex h-7 min-w-0 items-center gap-1 rounded-md px-2 text-xs text-ink-3 hover:bg-fill hover:text-ink data-popup-open:bg-fill disabled:hover:bg-transparent pointer-coarse:h-9">
              <Sparkles size={13} className="shrink-0" />
              <span className="truncate">{cur?.name ?? (now || t("composer.defaultModel"))}</span>
              {s.effort && <span className="shrink-0 text-ink-4">{s.effort}</span>}
              {models.length > 0 && <ChevronDown size={12} className="shrink-0 text-ink-4" />}
            </button>
          }
        />
      </Tip>
      <Menu.Portal>
        <Menu.Positioner side="top" align="start" sideOffset={6} collisionPadding={8} className="z-50">
          <Menu.Popup finalFocus={false} className="g-unfold glass thick float max-h-[min(420px,60vh)] min-w-52 overflow-y-auto rounded-lg p-1.5 text-ink outline-none">
            <Menu.RadioGroup value={cur?.value ?? now} onValueChange={(v) => void set({ model: v as string })} className="g-stagger flex flex-col">
              {models.map((m, i) => (
                <Menu.RadioItem key={m.value} value={m.value} style={{ "--i": i } as CSSProperties} className="flex min-h-8 cursor-default items-center gap-2.5 rounded-md pr-2 pl-2.5 text-base outline-none select-none data-highlighted:bg-fill pointer-coarse:min-h-11">
                  <span className="flex min-w-0 flex-1 flex-col py-1">
                    <span>{m.name}</span>
                    {m.description && <span className="truncate text-xs text-ink-3">{m.description}</span>}
                  </span>
                  <Menu.RadioItemIndicator className="text-ink">
                    <Check size={14} />
                  </Menu.RadioItemIndicator>
                </Menu.RadioItem>
              ))}
            </Menu.RadioGroup>
            {efforts.length > 1 && (
              <div className="mt-1 flex gap-0.5 rounded-md bg-fill p-0.5 shadow-[0_-0.5px_0_var(--edge)]" role="radiogroup" aria-label={t("composer.effort")}>
                {efforts.map((e) => (
                  <button key={e} type="button" role="radio" aria-checked={s.effort === e} onClick={() => void set({ effort: e })} className={cn("g-press h-7 flex-1 rounded-[5px] px-1.5 text-xs", s.effort === e ? "bg-paper text-ink shadow-[0_0_0_0.5px_var(--edge)]" : "text-ink-3 hover:text-ink")}>
                    {e}
                  </button>
                ))}
              </div>
            )}
          </Menu.Popup>
        </Menu.Positioner>
      </Menu.Portal>
    </Menu.Root>
  );
}
