// The usage (plan "用量"): the tokens the turns used, by day, terminal, agent and model (GET
// /api/usage). One page (#/settings/usage, from the settings), one chart for everyone (the user,
// 2026-11-08): the total of those shown, a bar a day stacked by terminal, each terminal in its own
// colour; under it the terminals, the most first, each its bar in its colour (no dot, no tip: the
// user, 2026-11-08). A row clicked takes it out of the chart (and the total) or puts it back;
// right-clicked, it is the only one left in (again: everyone). 全选 over the list and the chip at
// the top put everyone back. Leaving or
// coming back, the bars and the total glide to the new figures. A bar pressed narrows the page to
// that day. A terminal's models are in its line and its tip; no page of its own. Over it all, the
// harnesses (the user, 2026-11-08): all four at first; pressed, one leaves the whole page (chart,
// total, list) or comes back, and the last one taken out puts all four back; right-clicked, it is
// the only one left in (again: all four).
import { keepPreviousData, useQuery, useQueryClient } from "@tanstack/react-query";
import { CircleHelp, LoaderCircle, UserX, X } from "lucide-react";
import { useEffect, useMemo, useRef, useState, type CSSProperties, type ReactNode } from "react";
import { api } from "./api/client";
import type { Agent, Usage, UsageRow } from "./api/types.gen";
import { t, useLang } from "./i18n";
import { LocalMark } from "./me";
import { reduced } from "./motion";
import { errText } from "./toast";
import { agentName, AGENTS, Avatar, cn, Logo, Tip, tokens } from "./ui";

const dollars = (n: number) => (n >= 100 ? `$${Math.round(n)}` : `$${n.toFixed(2)}`);

/** 1: the last 24 hours, a bar an hour (the user, 2026-11-08); else a bar a day. */
const RANGES = [1, 7, 30] as const;

/** The last days' usage. The last answer stays while another range loads. */
const usageQ = (days: number) => ({ queryKey: ["usage", days], queryFn: () => api.usage(days, undefined, days === 1 ? "hour" : undefined), staleTime: 30_000 });
const useUsage = (days: number) => useQuery({ ...usageQ(days), placeholderData: keepPreviousData });
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

type ModelSum = { key: string; agent: Agent; model: string; sum: Sum };
type TerminalSum = { id: string; sum: Sum; models: ModelSum[] };

/** The range by day and terminal; the terminals in the order of the whole range, the most first
 *  (the list and the stacks keep it as a day is chosen: nothing moves), each with its figures for
 *  the day chosen, else the range, and its models the most first. */
function summarize(rows: UsageRow[], day: string | null) {
  const perDay = new Map<string, Map<string, Sum>>();
  const range = new Map<string, number>();
  const terms = new Map<string, { sum: Sum; models: Map<string, ModelSum> }>();
  for (const r of rows) {
    const d = perDay.get(r.day) ?? new Map<string, Sum>();
    perDay.set(r.day, d);
    const ds = d.get(r.terminal) ?? zero();
    d.set(r.terminal, ds);
    add(ds, r);
    range.set(r.terminal, (range.get(r.terminal) ?? 0) + r.input + r.output + r.cacheRead + r.cacheWrite);
    const tm = terms.get(r.terminal) ?? { sum: zero(), models: new Map() };
    terms.set(r.terminal, tm);
    if (day && r.day !== day) continue;
    add(tm.sum, r);
    const k = `${r.agent}\u0000${r.model}`;
    const m = tm.models.get(k) ?? { key: k, agent: r.agent, model: r.model, sum: zero() };
    tm.models.set(k, m);
    add(m.sum, r);
  }
  const order = [...range.keys()].sort((a, b) => range.get(b)! - range.get(a)! || a.localeCompare(b));
  const byTerminal: TerminalSum[] = order.map((id) => {
    const x = terms.get(id)!;
    return { id, sum: x.sum, models: [...x.models.values()].sort((a, b) => totalOf(b.sum) - totalOf(a.sum)) };
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
const fmt = (kind: "hm" | "md" | "mdw" | "w", o: Intl.DateTimeFormatOptions) => {
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
 *  motion. */
function useGlide(to: number, ready = true) {
  const [v, setV] = useState(to);
  const from = useRef(to);
  // Till the first answer there is nothing to run from: it is shown as it comes.
  const was = useRef(ready);
  useEffect(() => {
    const start = from.current;
    const first = !was.current && ready;
    was.current = ready;
    if (start === to || reduced() || first || !ready) {
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
  }, [to]);
  return v;
}

/** A panel of its own (no way back to the settings: it is not one of their pages): at the point it
 *  was asked from with the sidebar shown, the phone sheet's 用量 page. */
export function UsagePage() {
  useLang();
  const [days, setDays] = useState<number>(7);
  const [day, setDay] = useState<string | null>(null);
  // Those taken out of the chart; none at first: everyone is in, and so is one first seen later.
  const [off, setOff] = useState<ReadonlySet<string>>(() => new Set());
  // The harnesses taken out of the whole page; none at first.
  const [hidden, setHidden] = useState<ReadonlySet<Agent>>(() => new Set());
  const q = useUsage(days);
  const u = q.data;
  // The range of what is shown: a range chosen shows only once its figures came, the page changing
  // all at once, the bars rising (the user, 2026-11-08: not half the old and half the new).
  const range = u ? (hourly(u.days[0] ?? "") ? 1 : u.days.length) : days;
  // The other ranges asked for once this one is in: chosen, they are at hand.
  const qc = useQueryClient();
  const came = !!u;
  useEffect(() => {
    if (!came) return;
    const id = setTimeout(() => RANGES.forEach((n) => void qc.prefetchQuery(usageQ(n))), 300);
    return () => clearTimeout(id);
  }, [came, qc]);
  const rows = useMemo(() => (u?.rows ?? []).filter((r) => !hidden.has(r.agent)), [u, hidden]);
  const s = useMemo(() => summarize(rows, day), [rows, day]);
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
  const everyone = () => setOff(new Set());
  // Only this one in; when it already is, everyone back.
  const only = (id: string) => setOff(solo === id ? new Set() : new Set(s.order.filter((o) => o !== id)));
  // The last one shown taken out: everyone back, never an empty chart.
  const toggle = (id: string) => setOff(on(id) && shown.length === 1 ? new Set() : (o) => {
    const n = new Set(o);
    if (!n.delete(id)) n.add(id);
    return n;
  });
  const sum = zero();
  for (const x of s.byTerminal) if (on(x.id)) add(sum, x.sum);
  const whole = s.byTerminal.reduce((a, x) => a + totalOf(x.sum), 0);
  const total = useGlide(totalOf(sum), !!u);
  const chip = (key: string, label: ReactNode, clear: () => void, mark?: ReactNode) => (
    <button key={key} type="button" onClick={clear} className="g-pop g-press -my-1 flex h-6 min-w-0 items-center gap-1.5 rounded-full bg-fill px-2 text-ink-2 hover:bg-fill-2">
      {mark}
      <span className="truncate">{label}</span>
      <X size={12} className="shrink-0" />
    </button>
  );
  return (
    <div className="flex h-full flex-col">
      <header className="flex h-10.5 shrink-0 items-center gap-1 px-3 pt-1.5">
        <h1 className="m-0 min-w-0 flex-1 truncate text-base font-semibold">{t("usage.title")}</h1>
        {/* Only once it has been a moment: a quick answer shows no spinner flashing. */}
        {q.isFetching && u && <span className="g-fade mr-1.5 grid [animation-delay:200ms]"><LoaderCircle size={14} className="animate-spin text-ink-4" /></span>}
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
            <div className="flex min-h-4 min-w-0 items-center gap-1.5 text-xs text-ink-3">
              {day ? chip("day", dayLabel(day), () => setDay(null)) : <span className="shrink-0">{rangeName(range)}</span>}
              {!all &&
                chip(
                  "who",
                  solo !== undefined ? who(u, solo).name : t("usage.picked", { n: shown.length }),
                  everyone,
                  solo !== undefined && <Dot color={colors.get(solo)!} on />,
                )}
            </div>
            {/* Till the first answer, the page as it will be, in faint blocks of the same size: nothing
                moves as it comes, the figures fading in over them and the bars rising. */}
            {u ? (
              <div className="g-fade">
                {/* The figure leads: proportional digits, as a figure that stands alone. */}
                <div className="text-[30px] leading-9 font-semibold tracking-tight" aria-live="polite">
                  {tokens(Math.round(total))}
                </div>
                <div className="h-4 text-xs text-ink-3 tabular-nums">
                  {t("usage.tokensTurns", { n: sum.turns })}
                  {sum.cost > 0 && ` · ${dollars(sum.cost)}`}
                </div>
              </div>
            ) : (
              <div aria-hidden className="g-wait">
                <div className="flex h-9 items-center">
                  <span className="block h-7 w-24 rounded-md bg-fill" />
                </div>
                <div className="flex h-4 items-center">
                  <span className="block h-2.5 w-28 rounded-full bg-fill" />
                </div>
              </div>
            )}
            {u ? <Stacks key={range} u={u} days={u.days} perDay={s.perDay} order={s.order} colors={colors} on={on} sel={day} onSel={setDay} week={range === 7} /> : <StacksWaiting days={days} />}
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
                  <div key={i} className="flex items-center gap-2.5 py-2">
                    <span className="block size-6 shrink-0 rounded-full bg-fill" />
                    <span className="flex min-w-0 flex-1 flex-col gap-1.5">
                      <span className="block h-3 rounded-full bg-fill" style={{ width: i ? "40%" : "55%" }} />
                      <span className="block h-2 w-1/3 rounded-full bg-fill" />
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
                onToggle={() => toggle(x.id)}
                onOnly={() => only(x.id)}
              />
            ))}
          </section>
        </div>
      </div>
    </div>
  );
}

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

/** The harnesses as a row of their bare marks (no box: the user, 2026-11-08), each one pressed in
 *  or out of the whole page, faint and grey when out; right-clicked, the only one in. */
function Harnesses({ on, onToggle, onOnly }: { on: (a: Agent) => boolean; onToggle: (a: Agent) => void; onOnly: (a: Agent) => void }) {
  return (
    <div role="group" aria-label={t("usage.harness")} className="-mr-1 flex items-center">
      {AGENTS.map((a) => (
        <Tip key={a} label={agentName(a)}>
          <button
            type="button"
            aria-pressed={on(a)}
            aria-label={agentName(a)}
            onClick={() => onToggle(a)}
            onContextMenu={(e) => {
              e.preventDefault();
              onOnly(a);
            }}
            className={cn(
              "g-press grid size-7 place-items-center rounded-full transition-[opacity,filter,background-color] duration-(--dur-2) hover:bg-fill pointer-coarse:size-9",
              !on(a) && "opacity-30 grayscale hover:opacity-60",
            )}
          >
            <Logo agent={a} size={15} />
          </button>
        </Tip>
      ))}
    </div>
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
function Stacks({ u, days, perDay, order, colors, on, sel, onSel, week }: { u: Usage; days: string[]; perDay: Map<string, Map<string, Sum>>; order: string[]; colors: Map<string, string>; on: (id: string) => boolean; sel: string | null; onSel: (d: string | null) => void; week: boolean }) {
  const hours = hourly(days[0] ?? "");
  // Drawn first at nothing, the bars rise to their figures (their height's glide).
  const [risen, setRisen] = useState(false);
  useEffect(() => {
    const f = requestAnimationFrame(() => setRisen(true));
    return () => cancelAnimationFrame(f);
  }, []);
  const room = CHART_H - 14; // the scale's label above the highest bar
  const totals = days.map((d) => {
    let n = 0;
    for (const [id, s] of perDay.get(d) ?? []) if (on(id)) n += totalOf(s);
    return n;
  });
  const max = Math.max(...totals);
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
          <span className="absolute right-0 bottom-0.5 text-[10px] leading-none text-ink-4 tabular-nums" aria-label={t("usage.scale", { n: tokens(line) })}>
            {tokens(line)}
          </span>
        </div>
        <div className={cn("relative flex h-full items-end", week ? "gap-1.5" : "gap-0.5")}>
          {days.map((d, i) => {
            const day = perDay.get(d);
            const v = totals[i]!;
            const chosen = sel === d;
            const cost = [...(day ?? [])].reduce((a, [id, s]) => a + (on(id) ? s.cost : 0), 0);
            const label = `${dayLabel(d)} · ${t("usage.tokens", { n: tokens(v) })}${cost ? ` · ${dollars(cost)}` : ""}`;
            return (
              <Tip key={d} label={<BarTip label={label} u={u} day={day} order={order} colors={colors} on={on} />}>
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
                      const h = risen && x && on(id) && max ? (totalOf(x) / max) * room : 0;
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

/** The chart's height, whatever the range: another range changes no height. */
const CHART_H = 136;

/** A bar's tip: the day's total, then each one's. Drawn only as it opens: 30 bars' lists made with
 *  every draw made a range chosen stutter. */
function BarTip({ label, u, day, order, colors, on }: { label: string; u: Usage; day: Map<string, Sum> | undefined; order: string[]; colors: Map<string, string>; on: (id: string) => boolean }) {
  const parts = order.filter((id) => on(id) && day?.get(id) && totalOf(day.get(id)!) > 0);
  return (
    <span className="flex flex-col gap-0.5 tabular-nums">
      <span className="font-medium">{label}</span>
      {parts.length > 1 &&
        parts.slice(0, 6).map((id) => (
          <span key={id} className="flex items-center gap-1.5">
            <Dot color={colors.get(id)!} on />
            <span className="min-w-0 flex-1 truncate">{who(u, id).name}</span>
            <span className="ml-3">{tokens(totalOf(day!.get(id)!))}</span>
          </span>
        ))}
    </span>
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

/** A model without its provider ("opencode-go/deepseek-flash": deepseek-flash). */
const shortModel = (m: ModelSum) => m.model.split("/").pop() || agentName(m.agent);

/** A terminal: its avatar and name, its models in a line under it, its total and its part of
 *  everyone's in its colour. Clicked, it leaves the chart (its line faint) or comes back;
 *  right-clicked, it is the only one in. */
function TerminalRow({ x, u, color, whole, on, many, onToggle, onOnly }: { x: TerminalSum; u: Usage | undefined; color: string; whole: number; on: boolean; many: boolean; onToggle: () => void; onOnly: () => void }) {
  const w = who(u, x.id);
  const v = totalOf(x.sum);
  const body = (
    <span className={cn("flex min-w-0 flex-1 items-center gap-2.5 transition-opacity duration-(--dur-2)", !on && "opacity-45")}>
      {w.mark(24)}
      <span className="min-w-0 flex-1">
        <span className="flex items-baseline gap-2">
          <span className="min-w-0 flex-1 truncate text-sm font-medium">{w.name}</span>
          {x.sum.cost > 0 && <span className="shrink-0 text-xs text-ink-3 tabular-nums">{dollars(x.sum.cost)}</span>}
          <span className="shrink-0 text-sm font-medium tabular-nums">{tokens(v)}</span>
        </span>
        <span className="block truncate text-xs text-ink-3 tabular-nums">
          {x.models.length ? x.models.slice(0, 3).map((m) => `${shortModel(m)} ${tokens(totalOf(m.sum))}`).join(" · ") : t("usage.turns", { n: 0 })}
        </span>
        <span className="mt-1 block h-1 overflow-hidden rounded-full bg-fill" aria-hidden>
          <span className="block h-full rounded-full transition-[width] duration-(--dur-3) ease-(--ease-out)" style={{ width: `${whole && v ? Math.max(1, (v / whole) * 100) : 0}%`, background: color }} />
        </span>
      </span>
    </span>
  );
  if (!many) return <div className="g-fade -mx-1.5 flex items-center rounded-md px-1.5 py-2">{body}</div>;
  return (
    <button
      type="button"
      aria-pressed={on}
      aria-label={w.name}
      onClick={onToggle}
      onContextMenu={(e) => {
        e.preventDefault();
        onOnly();
      }}
      className="g-fade g-press -mx-1.5 flex min-w-0 items-center rounded-md px-1.5 py-2 text-left outline-offset-1 hover:bg-fill"
    >
      {body}
    </button>
  );
}
