// The composer's meters (plan "网页"): the context ring beside the send key, its breakdown in a
// popover; the quota bars on the composer's top-right, one thin bar per window, amber nearly full,
// red full, faded when last read long ago or no process runs; the approval-mode key and the model
// key under the composer.
import { Menu } from "@base-ui/react/menu";
import { Popover } from "@base-ui/react/popover";
import { Check, ChevronDown, FilePen, ListChecks, ShieldCheck, ShieldOff, ShieldQuestion, Sparkles } from "lucide-react";
import { useState, type CSSProperties, type ReactNode } from "react";
import type { AccountQuota, LiveSession, Mode, Model } from "../api/assumed";
import { api } from "../api/client";
import type { Agent, Context, ContextCategory } from "../api/types.gen";
import { useSettings } from "../data";
import { t, useLang } from "../i18n";
import { useContextState, useQuota } from "../store";
import { errText, toast } from "../toast";
import { cn, PopSurface, Tip } from "../ui";
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

export function ContextRing({ session, open, onOpenChange }: { session: string; open?: boolean; onOpenChange?: (o: boolean) => void }) {
  useLang();
  const c = useContextState(session);
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
            <button type="button" aria-label={label} className="g-press flex h-8 items-center gap-1.5 rounded-md px-1.5 text-xs text-ink-3 tabular-nums hover:bg-fill hover:text-ink-2 data-popup-open:bg-fill pointer-coarse:h-10">
              <svg width="18" height="18" viewBox="0 0 18 18" className="-rotate-90">
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

/** The agent's quota bars, on the composer's top-right. */
export function QuotaBars({ agent, live }: { agent: Agent; live: boolean }) {
  const lang = useLang();
  const quota = useQuota();
  const hidden = useSettings().data?.quotaHidden ?? [];
  const q: AccountQuota | undefined = quota?.agents.filter((a) => a.agent === agent).sort((a, b) => b.at - a.at)[0];
  const windows = (q?.windows ?? []).filter((w) => !hidden.includes(quotaKey(q!, w)));
  if (!q || !windows.length) return null;
  const stale = !live || Date.now() - q.at > STALE;
  return (
    <div className={cn("absolute top-[3px] right-4 flex gap-1 transition-opacity", stale && "opacity-50")}>
      {windows.map((w) => {
        const pct = Math.round(w.percent);
        const label = `${windowName(agent, w)} · ${t("quota.tip", { pct, time: resetIn(w.resetsAt, lang) })}`;
        const tone = pct >= 95 ? "bg-err" : pct >= 80 ? "bg-need" : "bg-ink-3";
        return (
          <Tip key={quotaKey(q, w)} label={label}>
            <span role="meter" aria-label={label} aria-valuenow={pct} aria-valuemin={0} aria-valuemax={100} className="relative flex h-3 w-7 items-center before:absolute before:-inset-x-0.5 before:-inset-y-2 before:content-['']">
              <span className="relative h-[3px] w-full overflow-hidden rounded-full bg-fill-2">
                <span className={cn("absolute inset-y-0 left-0 w-full origin-left rounded-full transition-transform duration-(--dur-3)", tone)} style={{ transform: `scaleX(${Math.min(100, pct) / 100})` }} />
              </span>
            </span>
          </Tip>
        );
      })}
    </div>
  );
}

/** An agent's approval modes as it names them, when the session does not list its own. */
const MODES: Partial<Record<Agent, string[]>> = {
  claude: ["default", "acceptEdits", "plan", "bypassPermissions"],
  codex: ["untrusted", "on-request", "never"],
};
export const modesOf = (s: LiveSession): Mode[] => s.modes ?? (MODES[s.agent] ?? []).map((id) => ({ id }));

const MODE_ICON: Record<string, ReactNode> = {
  default: <ShieldQuestion size={14} />,
  untrusted: <ShieldQuestion size={14} />,
  "on-request": <ShieldCheck size={14} />,
  acceptEdits: <FilePen size={14} />,
  plan: <ListChecks size={14} />,
  bypassPermissions: <ShieldOff size={14} />,
  never: <ShieldOff size={14} />,
};
const MODE_KEY: Record<string, Parameters<typeof t>[0]> = {
  default: "mode.default",
  acceptEdits: "mode.acceptEdits",
  plan: "mode.plan",
  bypassPermissions: "mode.bypass",
  untrusted: "mode.untrusted",
  "on-request": "mode.onRequest",
  never: "mode.never",
};
export const modeName = (m: Mode) => m.name ?? (MODE_KEY[m.id] ? t(MODE_KEY[m.id]!) : m.id);

/** Sets the mode; what it shows meanwhile is the one picked. */
export function useMode(s: LiveSession) {
  const [want, setWant] = useState<string | null>(null);
  const modes = modesOf(s);
  const now = want ?? s.mode ?? modes[0]?.id ?? "default";
  if (want !== null && s.mode === want) setWant(null);
  const set = async (m: string) => {
    if (m === now) return;
    setWant(m);
    try {
      await api.mode(s.id, m);
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
  const efforts = cur?.efforts ?? [];
  if (!models.length && !now) return null;
  const set = async (p: { model?: string; effort?: string }) => {
    if (p.model) setWant(p.model);
    try {
      await api.model(s.id, p);
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
