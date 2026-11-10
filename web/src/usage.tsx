// The usage (plan "用量"): the tokens the turns used and what they cost, by day, terminal, agent
// and model (GET /api/usage). One page (#/settings/usage, from the settings), one chart for
// everyone (the user, 2026-11-08). Its measure (the user, 2026-11-10) is the cost at first (the
// agent's own, else the model's maker's list price: the server's), or the tokens: the figure, the
// bars, the order of the list and their parts all go by it. The figure's line under it has the
// other measure, the turns and the cache's hit rate, then what the page is narrowed to (a day, the
// terminals in), as chips; the tokens have their four parts in a bar under it. The chart is a bar a
// day stacked by terminal, each in its own colour, or (its switch at the line's end) a heat map of
// the weekdays by hour, the range's days added up (none for 1 天: one day's hours are the bars).
// Under it the terminals, the most first, each its bar in its colour: its avatar (ringed in its
// colour) takes it out of the chart and the total or puts it back, right-clicked or held (phones)
// it is the only one left in (again: everyone); the row opens its models, each with its cache's hit
// rate, tokens and cost. 全选 over the list and the chip put everyone back. A bar pressed narrows
// the page to that day. With one terminal in, the bars are its models. Over it all, the harnesses
// (the user, 2026-11-08): all four at first; pressed, one leaves the whole page or comes back, and
// the last one taken out puts all four back; right-clicked, it is the only one left in.
import { keepPreviousData, useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronRight, CircleHelp, LoaderCircle, UserX, X } from "lucide-react";
import { useEffect, useMemo, useRef, useState, type CSSProperties, type MouseEvent, type PointerEvent, type ReactNode } from "react";
import { api } from "./api/client";
import type { Agent, Usage, UsageRow } from "./api/types.gen";
import { hitRate } from "./chat/meters";
import { t, useLang } from "./i18n";
import { LocalMark } from "./me";
import { reduced } from "./motion";
import { errText } from "./toast";
import { agentName, AGENTS, Avatar, cn, keep, kept, Logo, Tip, tokens } from "./ui";

export const dollars = (n: number) => (n >= 1000 ? `$${Math.round(n).toLocaleString("en-US")}` : n >= 100 ? `$${Math.round(n)}` : `$${n.toFixed(2)}`);

/** 1: the last 24 hours, a bar an hour (the user, 2026-11-08); else a bar a day. */
const RANGES = [1, 7, 30] as const;

/** The last days' usage. The last answer stays while another range loads. */
const usageQ = (days: number) => ({ queryKey: ["usage", days], queryFn: () => api.usage(days, undefined, days === 1 ? "hour" : undefined), staleTime: 30_000 });
const useUsage = (days: number) => useQuery({ ...usageQ(days), placeholderData: keepPreviousData });
/** The last days' usage by weekday and hour: the heat map's. */
const heatQ = (days: number) => ({ queryKey: ["usage", days, "weekhour"], queryFn: () => api.usage(days, undefined, "weekhour"), staleTime: 30_000 });
const rangeName = (days: number) => (days === 1 ? t("usage.last24h") : t("usage.lastDays", { n: days }));

type Sum = { turns: number; input: number; output: number; cacheRead: number; cacheWrite: number; cost: number };
const zero = (): Sum => ({ turns: 0, input: 0, output: 0, cacheRead: 0, cacheWrite: 0, cost: 0 });
const add = (s: Sum, r: UsageRow | Sum) => {
  s.turns += r.turns;
  s.input += r.input;
  s.output += r.output;
  s.cacheRead += r.cacheRead;
  s.cacheWrite += r.cacheWrite;
  s.cost += "costUsd" in r ? (r.costUsd ?? 0) : "cost" in r ? r.cost : 0;
};
const totalOf = (s: Sum) => s.input + s.output + s.cacheRead + s.cacheWrite;

/** What the page goes by: what the turns cost, or their tokens. */
type Metric = "cost" | "tokens";
type Measure = { value: (s: Sum) => number; fmt: (n: number) => string };
const MEASURES: Record<Metric, Measure> = { cost: { value: (s) => s.cost, fmt: dollars }, tokens: { value: totalOf, fmt: tokens } };

/** The share of the input read from the cache, 0 to 1; undefined with no input. */
export const hitOf = (s: { input: number; cacheRead: number; cacheWrite: number }) => {
  const all = s.input + s.cacheRead + s.cacheWrite;
  return all > 0 ? s.cacheRead / all : undefined;
};
/** Under this, a model's hit rate is drawn in amber (the user, 2026-11-10). */
const LOW_HIT = 0.8;

type ModelSum = { key: string; agent: Agent; model: string; sum: Sum };
type TerminalSum = { id: string; sum: Sum; models: ModelSum[] };

const MAKERS: Record<string, string> = { gpt: "GPT", deepseek: "DeepSeek", glm: "GLM", qwen: "Qwen", kimi: "Kimi", gemini: "Gemini", grok: "Grok", claude: "" };

/** A model as people say it: no provider, no date; versions with dots ("mistral/deepseek-v4-1-flash-1017":
 *  DeepSeek V4.1 Flash; claude-opus-5-5: Opus 5.5; gpt-6.1-sol: GPT-6.1 Sol). */
export function prettyModel(model: string, agent?: Agent): string {
  const bare = model.split("/").pop()?.replace(/[@:].*$/, "") ?? "";
  if (!bare) return agent ? agentName(agent) : model;
  const words = bare.split(/[-_\s]+/).filter(Boolean);
  if (words.length > 1 && /^(\d{4}|\d{8})$/.test(words[words.length - 1]!)) words.pop();
  // Numbers in a row are one version: 5, 5: 5.5; v4, 1: v4.1.
  const parts: string[] = [];
  for (const w of words) {
    const last = parts[parts.length - 1];
    if (/^\d+$/.test(w) && last && /^v?\d+(\.\d+)*$/i.test(last)) parts[parts.length - 1] = `${last}.${w}`;
    else parts.push(w);
  }
  const out = parts.map((p) => {
    const lower = p.toLowerCase();
    if (lower in MAKERS) return MAKERS[lower]!;
    return /^v\d/i.test(p) ? `V${p.slice(1)}` : p[0]!.toUpperCase() + p.slice(1);
  });
  // GPT's version joins it: GPT-6.1.
  if (out[0] === "GPT" && out[1] && /^\d/.test(out[1])) out.splice(0, 2, `GPT-${out[1]}`);
  return out.filter(Boolean).join(" ") || bare;
}

/** A model's key: its harness and its name as people say it, so one model under two routes or
 *  spellings (deepseek-flash, deepseek-official/deepseek-flash) is one line. */
const modelKey = (r: UsageRow) => `${r.agent}\u0000${prettyModel(r.model, r.agent)}`;

/** One terminal's range by day and model, the models the most first: the chart's parts when it is
 *  the only terminal in. A model's key is modelKey, as in summarize. */
function modelsOf(rows: UsageRow[], terminal: string, value: (s: Sum) => number) {
  const perDay = new Map<string, Map<string, Sum>>();
  const range = new Map<string, Sum>();
  for (const r of rows) {
    if (r.terminal !== terminal) continue;
    const k = modelKey(r);
    const d = perDay.get(r.day) ?? new Map<string, Sum>();
    perDay.set(r.day, d);
    const ds = d.get(k) ?? zero();
    d.set(k, ds);
    add(ds, r);
    const rs = range.get(k) ?? zero();
    range.set(k, rs);
    add(rs, r);
  }
  const order = [...range.keys()].sort((a, b) => value(range.get(b)!) - value(range.get(a)!) || a.localeCompare(b));
  return { perDay, order };
}

/** The range by day and terminal; the terminals in the order of the whole range, the most first
 *  (the list and the stacks keep it as a day is chosen: nothing moves), each with its figures for
 *  the day chosen, else the range, and its models the most first. */
function summarize(rows: UsageRow[], day: string | null, value: (s: Sum) => number) {
  const perDay = new Map<string, Map<string, Sum>>();
  const range = new Map<string, Sum>();
  const terms = new Map<string, { sum: Sum; models: Map<string, ModelSum> }>();
  for (const r of rows) {
    const d = perDay.get(r.day) ?? new Map<string, Sum>();
    perDay.set(r.day, d);
    const ds = d.get(r.terminal) ?? zero();
    d.set(r.terminal, ds);
    add(ds, r);
    const rs = range.get(r.terminal) ?? zero();
    range.set(r.terminal, rs);
    add(rs, r);
    const tm = terms.get(r.terminal) ?? { sum: zero(), models: new Map() };
    terms.set(r.terminal, tm);
    if (day && r.day !== day) continue;
    add(tm.sum, r);
    const k = modelKey(r);
    const m = tm.models.get(k) ?? { key: k, agent: r.agent, model: r.model, sum: zero() };
    tm.models.set(k, m);
    add(m.sum, r);
  }
  const order = [...range.keys()].sort((a, b) => value(range.get(b)!) - value(range.get(a)!) || a.localeCompare(b));
  const byTerminal: TerminalSum[] = order.map((id) => {
    const x = terms.get(id)!;
    return { id, sum: x.sum, models: [...x.models.values()].sort((a, b) => value(b.sum) - value(a.sum) || totalOf(b.sum) - totalOf(a.sum)) };
  });
  return { perDay, order, byTerminal };
}

const SLOTS = 8;
const fnv = (s: string) => {
  let h = 0x811c9dc5;
  for (let i = 0; i < s.length; i++) h = Math.imul(h ^ s.charCodeAt(i), 0x01000193);
  return h >>> 0;
};

/** Each terminal's colour (the user, 2026-11-08), from its ID alone so it is the same on every
 *  device and range: the slot its ID hashes to, else the next free one, the IDs taken in order.
 *  Past eight, or who is not known: grey. */
function paint(ids: string[]): Map<string, string> {
  const out = new Map<string, string>();
  const used = new Set<number>();
  for (const id of [...ids].sort()) {
    if (!id || used.size >= SLOTS) {
      out.set(id, "var(--ink-4)");
      continue;
    }
    let i = fnv(id) % SLOTS;
    while (used.has(i)) i = (i + 1) % SLOTS;
    used.add(i);
    out.set(id, `var(--series-${i + 1})`);
  }
  return out;
}

const hourly = (key: string) => key.includes("T");
/** A formatter for the page's language, made once: a new one each call was the chart's cost. */
const fmts = new Map<string, Intl.DateTimeFormat>();
const fmt = (kind: "hm" | "md" | "mdw" | "w" | "ws", o: Intl.DateTimeFormatOptions) => {
  const k = `${document.documentElement.lang} ${kind}`;
  let f = fmts.get(k);
  if (!f) fmts.set(k, (f = new Intl.DateTimeFormat(document.documentElement.lang || undefined, o)));
  return f;
};
/** An hour's key ("2026-11-07T05") as the page's language writes its start: 05:00. */
const hourLabel = (key: string) => fmt("hm", { hour: "2-digit", minute: "2-digit" }).format(new Date(`${key}:00`));
/** A bar's key as the page's language writes it: a day ("2026-11-07") 11月7日周六, Sat, Nov 7; an
 *  hour ("2026-11-07T05") 11月7日 05:00. */
const dayLabel = (day: string, weekday = true) =>
  hourly(day)
    ? `${fmt("md", { month: "numeric", day: "numeric" }).format(new Date(`${day}:00`))} ${hourLabel(day)}`
    : (weekday ? fmt("mdw", { month: "numeric", day: "numeric", weekday: "short" }) : fmt("md", { month: "numeric", day: "numeric" })).format(new Date(`${day}T00:00`));
/** A day's weekday, one letter. */
const weekdayOf = (day: string) => fmt("w", { weekday: "narrow" }).format(new Date(`${day}T00:00`));
/** A weekday (1 Monday … 7 Sunday) as the page's language writes it, short (周一, Mon) or narrow. */
const weekdayName = (wd: number, narrow = false) => {
  const d = new Date(2026, 10, 8 + wd); // 2026-11-09 was a Monday
  return narrow ? fmt("w", { weekday: "narrow" }).format(d) : fmt("ws", { weekday: "short" }).format(d);
};

/** A terminal in the usage: its avatar and name; "" (who began the turns not known) and removed
 *  ones said so. */
function who(u: Usage | undefined, id: string): { name: string; mark: (size: number) => ReactNode } {
  const x = u?.terminals[id];
  if (x) return { name: x.local ? t("me.local") : x.name, mark: (n) => (x.local ? <LocalMark size={n} /> : <Avatar src={x.avatar} name={x.name} size={n} channel={x.kind === "channel"} />) };
  const Icon = id ? UserX : CircleHelp;
  return {
    name: id ? t("usage.removed") : t("usage.unknown"),
    mark: (n) => (
      <span className="grid shrink-0 place-items-center rounded-full bg-fill-2 text-ink-3" style={{ width: n, height: n }} aria-hidden>
        <Icon size={Math.round(n * 0.55)} strokeWidth={1.75} />
      </span>
    ),
  };
}

/** A figure that runs to its new value rather than jumping (eased, 420ms); at once with reduced
 *  motion, and when what it measures changes (jump: another key). */
function useGlide(to: number, ready = true, jump = "") {
  const [v, setV] = useState(to);
  const from = useRef(to);
  // Till the first answer there is nothing to run from: it is shown as it comes.
  const was = useRef(ready);
  const key = useRef(jump);
  useEffect(() => {
    const start = from.current;
    const first = !was.current && ready;
    was.current = ready;
    const other = key.current !== jump;
    key.current = jump;
    if (start === to || reduced() || first || !ready || other) {
      from.current = to;
      setV(to);
      return;
    }
    const t0 = performance.now();
    let raf = 0;
    const step = (now: number) => {
      const k = Math.min(1, (now - t0) / 420);
      const e = 1 - (1 - k) ** 3;
      from.current = start + (to - start) * e;
      setV(from.current);
      if (k < 1) raf = requestAnimationFrame(step);
    };
    raf = requestAnimationFrame(step);
    return () => cancelAnimationFrame(raf);
  }, [to, jump]);
  // Measured anew, the old figure is not drawn for a frame.
  return key.current === jump ? v : to;
}

/** A choice kept on this device: the page opens as it was left. */
function useKept<T extends string>(key: string, values: readonly T[], first: T) {
  const [v, setV] = useState<T>(() => {
    const k = kept(key) as T | null;
    return k && values.includes(k) ? k : first;
  });
  return [v, (n: T) => (setV(n), keep(key, n))] as const;
}

/** A panel of its own (no way back to the settings: it is not one of their pages): at the point it
 *  was asked from with the sidebar shown, the phone sheet's 用量 page. */
export function UsagePage() {
  useLang();
  const [days, setDays] = useState<number>(7);
  const [day, setDay] = useState<string | null>(null);
  const [metric, setMetric] = useKept<Metric>("usage.metric", ["cost", "tokens"], "cost");
  const [view, setView] = useKept<"bars" | "heat">("usage.view", ["bars", "heat"], "bars");
  // Those taken out of the chart; none at first: everyone is in, and so is one first seen later.
  const [off, setOff] = useState<ReadonlySet<string>>(() => new Set());
  // The harnesses taken out of the whole page; none at first.
  const [hidden, setHidden] = useState<ReadonlySet<Agent>>(() => new Set());
  // The terminals whose models are open.
  const [open, setOpen] = useState<ReadonlySet<string>>(() => new Set());
  const q = useUsage(days);
  const u = q.data;
  const m = MEASURES[metric];
  // The range of what is shown: a range chosen shows only once its figures came, the page changing
  // all at once, the bars rising (the user, 2026-11-08: not half the old and half the new).
  const range = u ? (hourly(u.days[0] ?? "") ? 1 : u.days.length) : days;
  const heat = view === "heat" && range > 1;
  const hq = useQuery({ ...heatQ(range), enabled: heat, placeholderData: keepPreviousData });
  // The other ranges asked for once this one is in: chosen, they are at hand.
  const qc = useQueryClient();
  const came = !!u;
  useEffect(() => {
    if (!came) return;
    const id = setTimeout(() => {
      RANGES.forEach((n) => void qc.prefetchQuery(usageQ(n)));
      if (view === "heat") RANGES.forEach((n) => n > 1 && void qc.prefetchQuery(heatQ(n)));
    }, 300);
    return () => clearTimeout(id);
  }, [came, qc, view]);
  const rows = useMemo(() => (u?.rows ?? []).filter((r) => !hidden.has(r.agent)), [u, hidden]);
  const s = useMemo(() => summarize(rows, day, m.value), [rows, day, m]);
  // Colours from every terminal in the range, so a harness taken out recolours no one.
  const ids = useMemo(() => [...new Set((u?.rows ?? []).map((r) => r.terminal))].join("\u0000"), [u]);
  const colors = useMemo(() => paint(ids ? ids.split("\u0000") : []), [ids]);
  const harnesses = AGENTS.filter((a) => !hidden.has(a));
  const toggleHarness = (a: Agent) =>
    setHidden(harnesses.length === 1 && !hidden.has(a) ? new Set() : (o) => {
      const n = new Set(o);
      if (!n.delete(a)) n.add(a);
      return n;
    });
  // Only this harness in; when it already is, all four back.
  const onlyHarness = (a: Agent) => setHidden(harnesses.length === 1 && harnesses[0] === a ? new Set() : new Set(AGENTS.filter((o) => o !== a)));
  const on = (id: string) => !off.has(id);
  const shown = s.order.filter(on);
  const many = s.order.length > 1;
  const all = shown.length === s.order.length;
  const solo = shown.length === 1 && many ? shown[0] : undefined;
  // Only one terminal in: the chart is its models instead, the rest of the page as it was.
  const pick = shown.length === 1 ? shown[0] : undefined;
  const models = useMemo(() => (pick !== undefined ? modelsOf(rows, pick, m.value) : undefined), [rows, pick, m]);
  const modelColors = useMemo(() => paint(models?.order ?? []), [models]);
  const parts = models
    ? { perDay: models.perDay, order: models.order, colors: modelColors, on: () => true, name: (id: string) => { const [agent, model] = id.split("\u0000") as [Agent, string]; return prettyModel(model, agent); } }
    : { perDay: s.perDay, order: s.order, colors, on, name: (id: string) => who(u, id).name };
  const everyone = () => setOff(new Set());
  // Only this one in; when it already is, everyone back.
  const only = (id: string) => setOff(solo === id ? new Set() : new Set(s.order.filter((o) => o !== id)));
  // The last one shown taken out: everyone back, never an empty chart.
  const toggle = (id: string) => setOff(on(id) && shown.length === 1 ? new Set() : (o) => {
    const n = new Set(o);
    if (!n.delete(id)) n.add(id);
    return n;
  });
  const flip = (id: string) =>
    setOpen((o) => {
      const n = new Set(o);
      if (!n.delete(id)) n.add(id);
      return n;
    });
  const sum = zero();
  for (const x of s.byTerminal) if (on(x.id)) add(sum, x.sum);
  const whole = s.byTerminal.reduce((a, x) => a + m.value(x.sum), 0);
  const total = useGlide(m.value(sum), !!u, metric);
  const hit = hitRate(sum);
  const chip = (key: string, label: ReactNode, clear: () => void, mark?: ReactNode) => (
    <button key={key} type="button" onClick={clear} className="g-pop g-press flex h-5 min-w-0 shrink items-center gap-1 rounded-full bg-fill px-2 text-ink-2 hover:bg-fill-2">
      {mark}
      <span className="truncate">{label}</span>
      <X size={11} className="shrink-0" />
    </button>
  );
  const other = metric === "cost" ? t("usage.tokens", { n: tokens(totalOf(sum)) }) : sum.cost > 0 ? dollars(sum.cost) : "";
  return (
    <div className="flex h-full flex-col">
      <header className="flex h-10.5 shrink-0 items-center gap-1 px-3 pt-1.5">
        <h1 className="m-0 min-w-0 flex-1 truncate text-base font-semibold">{t("usage.title")}</h1>
        {/* Only once it has been a moment: a quick answer shows no spinner flashing. */}
        {(q.isFetching || (heat && hq.isFetching)) && u && <span className="g-fade mr-1.5 grid [animation-delay:200ms]"><LoaderCircle size={14} className="animate-spin text-ink-4" /></span>}
      </header>
      {/* The page's two choices on a line of their own under the title, as plain words and marks
          (the user, 2026-11-08): no box in a box, and room for the title in the narrow dialog. */}
      <div className="flex h-9 shrink-0 items-center justify-between gap-2 px-3">
        <Ranges value={days} onChange={(n) => (setDays(n), setDay(null))} />
        <Harnesses on={(a) => !hidden.has(a)} onToggle={toggleHarness} onOnly={onlyHarness} />
      </div>
      <div className="no-bar min-h-0 flex-1 overflow-y-auto pt-3 pb-4">
        <div className="flex flex-col gap-4 px-3">
          {q.isError && <p className="m-0 text-sm text-ink-3">{errText(q.error)}</p>}
          <section className="flex flex-col gap-3" aria-label={t("usage.title")}>
            {/* Till the first answer, the page as it will be, in faint blocks of the same size: nothing
                moves as it comes, the figures fading in over them and the bars rising. */}
            {u ? (
              <div className="g-fade flex flex-col">
                <div className="flex items-end justify-between gap-2">
                  {/* The figure leads: proportional digits, as a figure that stands alone. */}
                  <div className="min-w-0 truncate text-[30px] leading-9 font-semibold tracking-tight" aria-live="polite">
                    {metric === "cost" ? dollars(total) : tokens(Math.round(total))}
                  </div>
                  <Pills
                    label={t("usage.metric")}
                    value={metric}
                    onChange={setMetric}
                    options={[
                      { value: "cost", label: t("usage.cost") },
                      { value: "tokens", label: t("usage.tokensMetric") },
                    ]}
                  />
                </div>
                <div className="mt-0.5 flex h-6 min-w-0 items-center gap-1.5 text-xs text-ink-3 tabular-nums">
                  <span className="min-w-0 shrink truncate">{[other, t("usage.turns", { n: sum.turns }), hit && t("usage.hit", { n: hit })].filter(Boolean).join(" · ")}</span>
                  {day && chip("day", dayLabel(day), () => setDay(null))}
                  {!all &&
                    chip(
                      "who",
                      solo !== undefined ? who(u, solo).name : t("usage.picked", { n: shown.length }),
                      everyone,
                      solo !== undefined && <Dot color={colors.get(solo)!} on />,
                    )}
                  <span className="ml-auto" />
                  {range > 1 && (
                    <Pills
                      label={t("usage.view")}
                      value={view}
                      onChange={setView}
                      options={[
                        { value: "bars", label: t("usage.bars"), icon: <BarsIcon /> },
                        { value: "heat", label: t("usage.heat"), icon: <HeatIcon /> },
                      ]}
                    />
                  )}
                </div>
                {metric === "tokens" && <Mix sum={sum} />}
              </div>
            ) : (
              <div aria-hidden className="g-wait">
                <div className="flex h-9 items-center">
                  <span className="block h-7 w-24 rounded-md bg-fill" />
                </div>
                <div className="mt-0.5 flex h-6 items-center">
                  <span className="block h-2.5 w-40 rounded-full bg-fill" />
                </div>
              </div>
            )}
            {!u ? (
              <StacksWaiting days={days} />
            ) : heat ? (
              <Heat u={hq.data} hidden={hidden} off={off} m={m} color={solo !== undefined ? colors.get(solo)! : "var(--series-1)"} />
            ) : (
              <Stacks key={range} days={u.days} perDay={parts.perDay} order={parts.order} colors={parts.colors} on={parts.on} name={parts.name} sel={day} onSel={setDay} week={range === 7} m={m} />
            )}
          </section>

          <section className="flex flex-col" aria-label={t("usage.byTerminal")}>
            {many && (
              <div className="mb-0.5 flex h-6 items-center text-xs text-ink-3">
                <span className="min-w-0 flex-1">{t("usage.byTerminal")}</span>
                <button
                  type="button"
                  onClick={everyone}
                  tabIndex={all ? -1 : undefined}
                  aria-hidden={all || undefined}
                  className={cn("g-press -mr-1.5 rounded-md px-1.5 py-0.5 text-ink-2 transition-opacity duration-(--dur-2) hover:bg-fill hover:text-ink", all && "pointer-events-none opacity-0")}
                >
                  {t("usage.selectAll")}
                </button>
              </div>
            )}
            {u && s.order.length === 0 && <p className="g-fade m-0 py-6 text-center text-sm text-ink-3">{t("usage.none")}</p>}
            {!u && !q.isError && (
              <div aria-hidden className="g-wait">
                {[0, 1].map((i) => (
                  <div key={i} className="flex items-center gap-3 py-2">
                    <span className="block size-7 shrink-0 rounded-full bg-fill" />
                    <span className="flex min-w-0 flex-1 flex-col gap-1.5">
                      <span className="block h-3 rounded-full bg-fill" style={{ width: i ? "40%" : "55%" }} />
                      <span className="block h-1 rounded-full bg-fill" />
                    </span>
                  </div>
                ))}
              </div>
            )}
            {s.byTerminal.map((x) => (
              <TerminalRow
                key={x.id}
                x={x}
                u={u}
                color={colors.get(x.id)!}
                whole={whole}
                on={on(x.id)}
                many={many}
                open={open.has(x.id)}
                metric={metric}
                onToggle={() => toggle(x.id)}
                onOnly={() => only(x.id)}
                onOpen={() => flip(x.id)}
              />
            ))}
          </section>
        </div>
      </div>
    </div>
  );
}

/** A key's tap and its hold: a right click, or a touch held still (phones, where a long press
 *  sends no context menu: iOS). The tap after a hold is no tap. */
function useHold(onTap: () => void, onHold: () => void) {
  const timer = useRef(0);
  const at = useRef<{ x: number; y: number } | null>(null);
  const held = useRef(false);
  const hold = () => {
    clearTimeout(timer.current);
    if (held.current) return;
    held.current = true;
    navigator.vibrate?.(10);
    onHold();
  };
  const cancel = () => {
    clearTimeout(timer.current);
    at.current = null;
  };
  useEffect(() => () => clearTimeout(timer.current), []);
  return {
    onPointerDown: (e: PointerEvent) => {
      held.current = false;
      cancel();
      if (e.pointerType === "mouse") return;
      at.current = { x: e.clientX, y: e.clientY };
      timer.current = window.setTimeout(hold, 450);
    },
    onPointerMove: (e: PointerEvent) => {
      if (at.current && Math.hypot(e.clientX - at.current.x, e.clientY - at.current.y) > 8) cancel();
    },
    onPointerUp: cancel,
    onPointerCancel: cancel,
    // A touch's own context menu (Android) is the hold; a mouse's right click is one at once.
    onContextMenu: (e: MouseEvent) => {
      e.preventDefault();
      if (at.current) hold();
      else if (!held.current) onHold();
    },
    onClick: () => {
      if (held.current) held.current = false;
      else onTap();
    },
  };
}

/** Pressed and held keys: no text selected, no callout over the avatar. */
const HOLDS = "touch-manipulation select-none [-webkit-touch-callout:none]";

/** The ranges as words, the one chosen in ink on a soft pill that glides to the next. */
function Ranges({ value, onChange }: { value: number; onChange: (n: number) => void }) {
  const at = Math.max(0, RANGES.indexOf(value as (typeof RANGES)[number]));
  return (
    <div role="radiogroup" aria-label={t("usage.range")} className="relative -ml-1 grid grid-cols-3">
      <span aria-hidden className="absolute inset-y-0 left-0 w-1/3 rounded-full bg-fill transition-transform duration-(--dur-3) ease-(--ease-out)" style={{ transform: `translateX(${at * 100}%)` }} />
      {RANGES.map((n) => (
        <button
          key={n}
          type="button"
          role="radio"
          aria-checked={n === value}
          aria-label={rangeName(n)}
          onClick={() => onChange(n)}
          className={cn("g-press relative h-7 px-2.5 text-xs whitespace-nowrap tabular-nums transition-colors duration-(--dur-2) pointer-coarse:h-9", n === value ? "font-medium text-ink" : "text-ink-3 hover:text-ink")}
        >
          {t("usage.days", { n })}
        </button>
      ))}
    </div>
  );
}

/** A small switch: words, or marks with their names as tips, the one chosen on a lighter pill
 *  inside a soft one. */
function Pills<T extends string>({ label, value, onChange, options }: { label: string; value: T; onChange: (v: T) => void; options: { value: T; label: string; icon?: ReactNode }[] }) {
  return (
    <div role="radiogroup" aria-label={label} className="flex shrink-0 rounded-full bg-fill p-0.5">
      {options.map((o) => {
        const key = (
          <button
            key={o.value}
            type="button"
            role="radio"
            aria-checked={o.value === value}
            aria-label={o.label}
            onClick={() => onChange(o.value)}
            className={cn(
              "g-press grid h-5 place-items-center rounded-full text-[11px] transition-colors duration-(--dur-2) pointer-coarse:h-7",
              o.icon ? "w-6 pointer-coarse:w-8" : "px-2.5",
              o.value === value ? "bg-fill-2 font-medium text-ink" : "text-ink-3 hover:text-ink",
            )}
          >
            {o.icon ?? o.label}
          </button>
        );
        return o.icon ? (
          <Tip key={o.value} label={o.label}>
            {key}
          </Tip>
        ) : (
          key
        );
      })}
    </div>
  );
}

function BarsIcon() {
  return (
    <svg width="12" height="12" viewBox="0 0 12 12" aria-hidden fill="currentColor">
      <rect x="0.5" y="5.5" width="3" height="6" rx="1" />
      <rect x="4.5" y="1.5" width="3" height="10" rx="1" />
      <rect x="8.5" y="3.5" width="3" height="8" rx="1" />
    </svg>
  );
}

function HeatIcon() {
  return (
    <svg width="12" height="12" viewBox="0 0 12 12" aria-hidden fill="currentColor">
      {[0, 1, 2].flatMap((r) => [0, 1, 2].map((c) => <rect key={`${r}${c}`} x={0.5 + c * 4} y={0.5 + r * 4} width="3" height="3" rx="0.8" opacity={(r + c) % 2 ? 0.45 : 1} />))}
    </svg>
  );
}

/** The harnesses as a row of their bare marks (no box: the user, 2026-11-08), each one pressed in
 *  or out of the whole page, faint and grey when out; right-clicked, the only one in. */
function Harnesses({ on, onToggle, onOnly }: { on: (a: Agent) => boolean; onToggle: (a: Agent) => void; onOnly: (a: Agent) => void }) {
  return (
    <div role="group" aria-label={t("usage.harness")} className="-mr-1 flex items-center">
      {AGENTS.map((a) => (
        <HarnessKey key={a} a={a} on={on(a)} onToggle={() => onToggle(a)} onOnly={() => onOnly(a)} />
      ))}
    </div>
  );
}

function HarnessKey({ a, on, onToggle, onOnly }: { a: Agent; on: boolean; onToggle: () => void; onOnly: () => void }) {
  const hold = useHold(onToggle, onOnly);
  return (
    <Tip label={agentName(a)}>
      <button
        type="button"
        aria-pressed={on}
        aria-label={agentName(a)}
        {...hold}
        className={cn(
          "g-press grid size-7 place-items-center rounded-full transition-[opacity,filter,background-color] duration-(--dur-2) hover:bg-fill pointer-coarse:size-9",
          HOLDS,
          !on && "opacity-30 grayscale hover:opacity-60",
        )}
      >
        <Logo agent={a} size={15} />
      </button>
    </Tip>
  );
}

/** A terminal's colour as a dot: filled while it is in the chart, a ring of it when out. */
function Dot({ color, on }: { color: string; on: boolean }) {
  return (
    <span
      aria-hidden
      className="block size-2.5 shrink-0 rounded-full transition-[background-color] duration-(--dur-2) ease-(--ease-out)"
      style={{ color, background: on ? color : "transparent", boxShadow: "inset 0 0 0 1.5px currentColor" }}
    />
  );
}

const MIX = [
  { key: "usage.cacheRead", of: (s: Sum) => s.cacheRead, tone: "var(--ink-4)" },
  { key: "usage.cacheWrite", of: (s: Sum) => s.cacheWrite, tone: "var(--ink-3)" },
  { key: "usage.input", of: (s: Sum) => s.input, tone: "var(--ink-2)" },
  { key: "usage.output", of: (s: Sum) => s.output, tone: "var(--ink)" },
] as const;

/** The tokens' four parts: a bar of them, the cache's reads first, and each one's figure. */
function Mix({ sum }: { sum: Sum }) {
  const all = totalOf(sum);
  return (
    <div className="mt-2 flex flex-col gap-1.5 tabular-nums">
      <div className="flex h-1.5 gap-0.5 overflow-hidden rounded-full bg-fill" aria-hidden>
        {all > 0 && MIX.map((p) => (p.of(sum) > 0 ? <span key={p.key} className="block h-full min-w-0.5 transition-[flex-grow] duration-(--dur-3) ease-(--ease-out)" style={{ flexGrow: p.of(sum) / all, background: p.tone }} /> : null))}
      </div>
      <div className="grid grid-cols-2 gap-x-4 gap-y-0.5 text-xs text-ink-3">
        {MIX.map((p) => (
          <span key={p.key} className="flex min-w-0 items-center gap-1.5">
            <span aria-hidden className="block size-2 shrink-0 rounded-[2px]" style={{ background: p.tone }} />
            <span className="min-w-0 flex-1 truncate">{t(p.key)}</span>
            <span className="text-ink-2">{tokens(p.of(sum))}</span>
          </span>
        ))}
      </div>
    </div>
  );
}

/** A round step for the scale's line under the highest bar: 1, 2 or 5 times a power of ten. */
function niceUnder(max: number) {
  const p = 10 ** Math.floor(Math.log10(max));
  return [5, 2, 1].map((k) => k * p).find((v) => v <= max * 0.92) ?? p / 2;
}

/** A bar a day, stacked by terminal from one baseline in the list's order (the most at the
 *  bottom), each part in its terminal's colour; the scale is the highest day of those shown, so
 *  taking someone out or putting them back, every part glides to its new height. One quiet line
 *  marks a round figure. Pressed, a day is chosen (pressed again, the range); its tip has the day's
 *  total and each one's. week: each day's weekday under it, else the range's ends. */
function Stacks({ days, perDay, order, colors, on, name, sel, onSel, week, m }: { days: string[]; perDay: Map<string, Map<string, Sum>>; order: string[]; colors: Map<string, string>; on: (id: string) => boolean; name: (id: string) => string; sel: string | null; onSel: (d: string | null) => void; week: boolean; m: Measure }) {
  const hours = hourly(days[0] ?? "");
  // Drawn first at nothing, the bars rise to their figures (their height's glide).
  const [risen, setRisen] = useState(false);
  useEffect(() => {
    const f = requestAnimationFrame(() => setRisen(true));
    return () => cancelAnimationFrame(f);
  }, []);
  const room = CHART_H - 14; // the scale's label above the highest bar
  const sums = days.map((d) => {
    const all = zero();
    for (const [id, s] of perDay.get(d) ?? []) if (on(id)) add(all, s);
    return all;
  });
  const max = Math.max(...sums.map(m.value));
  const line = max > 0 ? niceUnder(max) : 0;
  const today = days[days.length - 1];
  return (
    <div className="flex flex-col gap-1">
      <div role="group" aria-label={t("usage.chart")} className="relative border-b border-fill-2" style={{ height: CHART_H }}>
        <div
          aria-hidden
          className={cn("pointer-events-none absolute inset-x-0 border-t border-dashed border-fill-2 transition-[bottom,opacity] duration-(--dur-3) ease-(--ease-out)", !line && "opacity-0")}
          style={{ bottom: max ? (line / max) * room : 0 }}
        >
          <span className="absolute right-0 bottom-0.5 text-[10px] leading-none text-ink-4 tabular-nums" aria-label={t("usage.scale", { n: m.fmt(line) })}>
            {m.fmt(line)}
          </span>
        </div>
        <div className={cn("relative flex h-full items-end", week ? "gap-1.5" : "gap-0.5")}>
          {days.map((d, i) => {
            const day = perDay.get(d);
            const all = sums[i]!;
            const chosen = sel === d;
            const label = `${dayLabel(d)} · ${t("usage.tokens", { n: tokens(totalOf(all)) })}${all.cost > 0 ? ` · ${dollars(all.cost)}` : ""}`;
            return (
              <Tip key={d} label={<BarTip label={label} day={day} order={order} colors={colors} on={on} name={name} m={m} />}>
                <button
                  type="button"
                  aria-label={label}
                  aria-pressed={chosen}
                  onClick={() => onSel(chosen ? null : d)}
                  className={cn("flex h-full min-w-0 flex-1 items-end justify-center outline-offset-1 transition-opacity duration-(--dur-2)", sel && !chosen ? "opacity-35 hover:opacity-60" : "hover:opacity-85")}
                >
                  <span className={cn("flex w-full flex-col-reverse overflow-hidden rounded-t-[4px]", week ? "max-w-9" : "max-w-6")}>
                    {order.map((id) => {
                      const x = day?.get(id);
                      const h = risen && x && on(id) && max ? (m.value(x) / max) * room : 0;
                      // A 1.5px gap over each part (its top border, see-through); none on a sliver.
                      return (
                        <span
                          key={id}
                          className="block shrink-0 border-t-transparent bg-clip-content transition-[height,border-top-width] duration-(--dur-3) ease-(--ease-out)"
                          style={{ height: h > 0 ? Math.max(2, h) : 0, borderTopWidth: h >= 6 ? 1.5 : 0, borderTopStyle: "solid", background: colors.get(id), backgroundClip: "content-box" } as CSSProperties}
                        />
                      );
                    })}
                  </span>
                </button>
              </Tip>
            );
          })}
        </div>
      </div>
      {week ? (
        <div className="flex gap-1.5 text-[11px] text-ink-3 tabular-nums">
          {days.map((d) => (
            <span key={d} className={cn("min-w-0 flex-1 truncate text-center transition-colors duration-(--dur-2)", (d === sel || (!sel && d === today)) && "font-medium text-ink-2")}>
              {d === today ? t("usage.today") : weekdayOf(d)}
            </span>
          ))}
        </div>
      ) : (
        <div className="flex justify-between text-[11px] text-ink-3 tabular-nums">
          <span>{hours ? hourLabel(days[0]!) : dayLabel(days[0]!, false)}</span>
          <span>{hours ? t("usage.thisHour") : t("usage.today")}</span>
        </div>
      )}
    </div>
  );
}

/** The chart's height, whatever the range or view: another changes no height. */
const CHART_H = 136;

/** A bar's tip: the day's total, then each one's. Drawn only as it opens: 30 bars' lists made with
 *  every draw made a range chosen stutter. */
function BarTip({ label, day, order, colors, on, name, m }: { label: string; day: Map<string, Sum> | undefined; order: string[]; colors: Map<string, string>; on: (id: string) => boolean; name: (id: string) => string; m: Measure }) {
  const parts = order.filter((id) => on(id) && day?.get(id) && m.value(day.get(id)!) > 0);
  return (
    <span className="flex flex-col gap-0.5 tabular-nums">
      <span className="font-medium">{label}</span>
      {parts.length > 1 &&
        parts.slice(0, 6).map((id) => (
          <span key={id} className="flex items-center gap-1.5">
            <Dot color={colors.get(id)!} on />
            <span className="min-w-0 flex-1 truncate">{name(id)}</span>
            <span className="ml-3">{m.fmt(m.value(day!.get(id)!))}</span>
          </span>
        ))}
    </span>
  );
}

/** The heat map: a row a weekday (Monday first), a square an hour, the range's days added up (the
 *  user, 2026-11-10: when, not which day); five shades of one colour by the measure, the one
 *  terminal's when only one is in. Over a square (or tapped), the line under it says what it holds. */
function Heat({ u, hidden, off, m, color }: { u: Usage | undefined; hidden: ReadonlySet<Agent>; off: ReadonlySet<string>; m: Measure; color: string }) {
  const [at, setAt] = useState<number | null>(null);
  const cells = useMemo(() => {
    const out = Array.from({ length: 7 * 24 }, zero);
    for (const r of u?.rows ?? []) {
      if (hidden.has(r.agent) || off.has(r.terminal)) continue;
      const [wd, h] = r.day.split("T").map(Number) as [number, number];
      if (wd >= 1 && wd <= 7 && h >= 0 && h < 24) add(out[(wd - 1) * 24 + h]!, r);
    }
    return out;
  }, [u, hidden, off]);
  const values = cells.map(m.value);
  const max = Math.max(0, ...values);
  const level = (v: number) => (v <= 0 || !max ? 0 : Math.min(4, Math.max(1, Math.ceil((v / max) * 4))));
  const shade = (k: number) => (k === 0 ? "var(--fill)" : k === 4 ? color : `color-mix(in srgb, ${color} ${[0, 30, 55, 80][k]}%, var(--fill))`);
  const c = at !== null ? cells[at] : undefined;
  return (
    <div className="flex flex-col gap-1">
      <div role="group" aria-label={t("usage.heat")} className={cn("flex flex-col justify-end border-b border-fill-2 pb-1", !u && "g-wait")} style={{ height: CHART_H }} onPointerLeave={(e) => e.pointerType === "mouse" && setAt(null)}>
        <div className="grid grid-cols-[1rem_1fr] gap-1.5">
          <div className="grid grid-rows-7 gap-[3px] text-[10px] leading-none text-ink-4">
            {[1, 2, 3, 4, 5, 6, 7].map((wd) => (
              <span key={wd} className="flex items-center justify-end">
                {wd % 2 ? weekdayName(wd, true) : ""}
              </span>
            ))}
          </div>
          <div className="grid grid-cols-24 gap-[3px]">
            {values.map((v, i) => (
              <button
                key={i}
                type="button"
                tabIndex={-1}
                aria-hidden
                onPointerEnter={(e) => e.pointerType === "mouse" && setAt(i)}
                onClick={() => setAt(at === i ? null : i)}
                className={cn("aspect-square w-full rounded-[3px] transition-[background-color] duration-(--dur-3) ease-(--ease-out)", at === i && "outline-[1.5px] outline-offset-0 outline-ink outline-solid")}
                style={{ background: shade(level(v)) }}
              />
            ))}
          </div>
        </div>
      </div>
      {/* The hours under it; over a square, what it holds instead. */}
      <div className="h-[16.5px] pl-[1.375rem] text-[11px] text-ink-3 tabular-nums">
        {c && at !== null ? (
          <span className="block truncate text-ink-2">
            {t("usage.heatCell", { w: weekdayName(Math.floor(at / 24) + 1), h: at % 24, h2: (at % 24) + 1 })}
            {` · ${m.fmt(m.value(c))}`}
            <span className="text-ink-3">{m === MEASURES.cost ? ` · ${tokens(totalOf(c))}` : c.cost > 0 ? ` · ${dollars(c.cost)}` : ""}</span>
          </span>
        ) : (
          <span className="flex justify-between">
            {[0, 6, 12, 18, 24].map((h) => (
              <span key={h}>{h}</span>
            ))}
          </span>
        )}
      </div>
    </div>
  );
}

/** The chart's place while the first answer comes: as tall as it will be, faint bars of no
 *  figure in it, and the line under it. */
function StacksWaiting({ days }: { days: number }) {
  const week = days === 7;
  const n = days === 1 ? 24 : days;
  return (
    <div aria-hidden className="g-wait flex flex-col gap-1">
      <div className={cn("flex items-end border-b border-fill-2", week ? "gap-1.5" : "gap-0.5")} style={{ height: CHART_H }}>
        {Array.from({ length: n }, (_, i) => (
          <span key={i} className="flex h-full min-w-0 flex-1 items-end justify-center">
            <span className={cn("block w-full rounded-t-[4px] bg-fill", week ? "max-w-9" : "max-w-6")} style={{ height: `${18 + ((i * 37) % 45)}%` }} />
          </span>
        ))}
      </div>
      <div className="flex h-[16.5px] items-center">
        <span className="block h-2 w-full rounded-full bg-fill" />
      </div>
    </div>
  );
}

/** A hit rate as a figure, amber under LOW_HIT; a dash with no input. */
function Hit({ s }: { s: Sum }) {
  const h = hitOf(s);
  if (h === undefined) return <span className="text-ink-4">—</span>;
  return <span style={h < LOW_HIT ? { color: "var(--need)" } : undefined}>{hitRate(s)}</span>;
}

/** A terminal: its avatar ringed in its colour (pressed, out of the chart or back; held, the only
 *  one in), its name, the other measure and the measure, its part of everyone's in its colour.
 *  The row pressed opens its models: each one's hit rate, tokens and cost. */
function TerminalRow({ x, u, color, whole, on, many, open, metric, onToggle, onOnly, onOpen }: { x: TerminalSum; u: Usage | undefined; color: string; whole: number; on: boolean; many: boolean; open: boolean; metric: Metric; onToggle: () => void; onOnly: () => void; onOpen: () => void }) {
  const w = who(u, x.id);
  const v = MEASURES[metric].value(x.sum);
  const cost = x.sum.cost > 0 ? dollars(x.sum.cost) : "";
  const main = metric === "cost" ? cost || "—" : tokens(totalOf(x.sum));
  const second = metric === "cost" ? tokens(totalOf(x.sum)) : cost;
  const hit = hitRate(x.sum);
  const avatarHold = useHold(onToggle, onOnly);
  const rowHold = useHold(onOpen, many ? onOnly : onOpen);
  // Its colour as a ring round the avatar, a hair of the page between: dashed and faint when out.
  const mark = (
    <span className="relative grid shrink-0 place-items-center rounded-full p-[3px]">
      <span aria-hidden className={cn("absolute inset-0 rounded-full border-2 transition-opacity duration-(--dur-2)", !on && "border-dashed opacity-60")} style={{ borderColor: color }} />
      {w.mark(26)}
    </span>
  );
  return (
    <div className={cn("g-fade -mx-1.5 flex flex-col rounded-md px-1.5 transition-colors duration-(--dur-2)", open && "bg-fill")}>
      <div className="flex min-w-0 items-center gap-2.5 py-1.5">
        {many ? (
          <Tip label={on ? t("usage.out") : t("usage.in")}>
            <button type="button" aria-pressed={on} aria-label={`${w.name} · ${t("usage.inChart")}`} {...avatarHold} className={cn("g-press shrink-0 rounded-full outline-offset-2", HOLDS)}>
              {mark}
            </button>
          </Tip>
        ) : (
          mark
        )}
        <button type="button" aria-expanded={open} aria-label={w.name} {...rowHold} className={cn("g-press flex min-w-0 flex-1 flex-col py-0.5 text-left outline-offset-2 transition-opacity duration-(--dur-2)", HOLDS, !on && "opacity-45")}>
          <span className="flex w-full items-baseline gap-2">
            <span className="min-w-0 flex-1 truncate text-sm font-medium">{w.name}</span>
            <span className="shrink-0 text-xs text-ink-3 tabular-nums">{[open && hit && t("usage.hitShort", { n: hit }), second].filter(Boolean).join(" · ")}</span>
            <span className="shrink-0 text-sm font-semibold tabular-nums">{main}</span>
          </span>
          <span className="mt-1.5 flex w-full items-center gap-2">
            <span className={cn("block h-1 min-w-0 flex-1 overflow-hidden rounded-full", open ? "bg-fill-2" : "bg-fill")} aria-hidden>
              <span className="block h-full rounded-full transition-[width] duration-(--dur-3) ease-(--ease-out)" style={{ width: `${whole && v ? Math.max(1, (v / whole) * 100) : 0}%`, background: color }} />
            </span>
            <ChevronRight size={12} aria-hidden className={cn("shrink-0 text-ink-4 transition-transform duration-(--dur-2)", open && "rotate-90")} />
          </span>
        </button>
      </div>
      {open && (
        <div className="g-fade mb-2 ml-[2.625rem] grid grid-cols-[0.875rem_minmax(0,1fr)_auto_auto_auto] items-center gap-x-2.5 gap-y-1 text-xs tabular-nums">
          <span />
          <span className="text-[11px] text-ink-4">{t("usage.model")}</span>
          <span className="text-right text-[11px] text-ink-4">{t("usage.colHit")}</span>
          <span className="text-right text-[11px] text-ink-4">{t("usage.tokensMetric")}</span>
          <span className="text-right text-[11px] text-ink-4">{t("usage.cost")}</span>
          {x.models.map((md) => (
            <ModelLine key={md.key} md={md} />
          ))}
        </div>
      )}
    </div>
  );
}

function ModelLine({ md }: { md: ModelSum }) {
  return (
    <>
      <Logo agent={md.agent} size={13} />
      <span className="min-w-0 truncate text-ink-2" title={md.model}>
        {prettyModel(md.model, md.agent)}
      </span>
      <span className="text-right text-ink-3">
        <Hit s={md.sum} />
      </span>
      <span className="text-right text-ink-3">{tokens(totalOf(md.sum))}</span>
      <span className="text-right text-ink-2">{md.sum.cost > 0 ? dollars(md.sum.cost) : "—"}</span>
    </>
  );
}
