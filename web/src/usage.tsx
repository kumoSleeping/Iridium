// The usage (plan "用量"): the tokens the turns used, by day, terminal, agent and model (GET
// /api/usage). One page (#/settings/usage, from the settings), one chart for everyone (the user,
// 2026-11-08): the total of those shown, a bar a day stacked by terminal, each terminal in its own
// colour; under it the terminals, the most first, each its colour's dot. A row pressed takes it
// out of the chart (and the total) or puts it back; its 仅此 shows it alone, and on the one shown
// alone it reads 全部; 全选 over the list and the chip at the top put everyone back. Leaving or
// coming back, the bars and the total glide to the new figures. A bar pressed narrows the page to
// that day. A terminal's models are in its line and its tip; no page of its own.
import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { ChevronLeft, CircleHelp, LoaderCircle, UserX, X } from "lucide-react";
import { useEffect, useMemo, useRef, useState, type CSSProperties, type ReactNode } from "react";
import { api } from "./api/client";
import type { Agent, Usage, UsageRow } from "./api/types.gen";
import { t, useLang } from "./i18n";
import { LocalMark, Segmented } from "./me";
import { reduced } from "./motion";
import { errText } from "./toast";
import { agentName, Avatar, cn, IconKey, Tip, tokens } from "./ui";

const dollars = (n: number) => (n >= 100 ? `$${Math.round(n)}` : `$${n.toFixed(2)}`);

const RANGES = [7, 30] as const;

/** The last days' usage. The last answer stays while another range loads. */
const useUsage = (days: number) => useQuery({ queryKey: ["usage", days], queryFn: () => api.usage(days), staleTime: 30_000, placeholderData: keepPreviousData });

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

/** A day ("2026-11-07") as the page's language writes it: 11月7日周六, Sat, Nov 7. */
const dayLabel = (day: string, weekday = true) => new Date(`${day}T00:00`).toLocaleDateString(document.documentElement.lang || undefined, { month: "numeric", day: "numeric", ...(weekday ? { weekday: "short" } : {}) });

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
function useGlide(to: number) {
  const [v, setV] = useState(to);
  const from = useRef(to);
  useEffect(() => {
    const start = from.current;
    if (start === to || reduced()) {
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

export function UsagePage({ onBack }: { onBack: () => void }) {
  useLang();
  const [days, setDays] = useState<number>(7);
  const [day, setDay] = useState<string | null>(null);
  // Those taken out of the chart; none at first: everyone is in, and so is one first seen later.
  const [off, setOff] = useState<ReadonlySet<string>>(() => new Set());
  const q = useUsage(days);
  const u = q.data;
  const s = useMemo(() => summarize(u?.rows ?? [], day), [u, day]);
  const colors = useMemo(() => paint(s.order), [s.order]);
  const on = (id: string) => !off.has(id);
  const shown = s.order.filter(on);
  const many = s.order.length > 1;
  const all = shown.length === s.order.length;
  const solo = shown.length === 1 && many ? shown[0] : undefined;
  const everyone = () => setOff(new Set());
  const only = (id: string) => setOff(new Set(s.order.filter((x) => x !== id)));
  // The last one shown taken out: everyone back, never an empty chart.
  const toggle = (id: string) => setOff(on(id) && shown.length === 1 ? new Set() : (o) => {
    const n = new Set(o);
    if (!n.delete(id)) n.add(id);
    return n;
  });
  const sum = zero();
  for (const x of s.byTerminal) if (on(x.id)) add(sum, x.sum);
  const whole = s.byTerminal.reduce((a, x) => a + totalOf(x.sum), 0);
  const total = useGlide(totalOf(sum));
  const chip = (key: string, label: ReactNode, clear: () => void, mark?: ReactNode) => (
    <button key={key} type="button" onClick={clear} className="g-pop g-press -my-1 flex h-6 min-w-0 items-center gap-1.5 rounded-full bg-fill px-2 text-ink-2 hover:bg-fill-2">
      {mark}
      <span className="truncate">{label}</span>
      <X size={12} className="shrink-0" />
    </button>
  );
  return (
    <div className="flex h-full flex-col">
      <header className="flex h-10.5 shrink-0 items-center gap-1 px-1.5 pt-1.5">
        <IconKey label={t("nav.back")} onClick={onBack} tipSide="bottom" className="size-7">
          <ChevronLeft size={16} />
        </IconKey>
        <h1 className="m-0 min-w-0 flex-1 truncate text-base font-semibold">{t("usage.title")}</h1>
        {q.isFetching && <LoaderCircle size={14} className="animate-spin text-ink-4" />}
        <Segmented
          label={t("usage.range")}
          value={String(days)}
          onChange={(v) => (setDays(Number(v)), setDay(null))}
          items={RANGES.map((n) => ({ v: String(n), label: t("usage.lastDays", { n }), icon: <span className="px-0.5 text-xs tabular-nums">{t("usage.days", { n })}</span> }))}
        />
      </header>
      <div className="no-bar min-h-0 flex-1 overflow-y-auto pt-3 pb-4">
        <div className="flex flex-col gap-4 px-3">
          {q.isError && <p className="m-0 text-sm text-ink-3">{errText(q.error)}</p>}
          <section className="flex flex-col gap-3" aria-label={t("usage.title")}>
            <div className="flex min-h-4 min-w-0 items-center gap-1.5 text-xs text-ink-3">
              {day ? chip("day", dayLabel(day), () => setDay(null)) : <span className="shrink-0">{t("usage.lastDays", { n: days })}</span>}
              {!all &&
                chip(
                  "who",
                  solo !== undefined ? who(u, solo).name : t("usage.picked", { n: shown.length }),
                  everyone,
                  solo !== undefined && <Dot color={colors.get(solo)!} on />,
                )}
            </div>
            <div>
              {/* The figure leads: proportional digits, as a figure that stands alone. */}
              <div className="text-[30px] leading-9 font-semibold tracking-tight" aria-live="polite">
                {tokens(Math.round(total))}
              </div>
              <div className="text-xs text-ink-3 tabular-nums">
                {t("usage.tokensTurns", { n: sum.turns })}
                {sum.cost > 0 && ` · ${dollars(sum.cost)}`}
              </div>
            </div>
            {u && <Stacks u={u} days={u.days} perDay={s.perDay} order={s.order} colors={colors} on={on} sel={day} onSel={setDay} week={days === 7} />}
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
            {u && s.order.length === 0 && <p className="m-0 py-6 text-center text-sm text-ink-3">{t("usage.none")}</p>}
            {s.byTerminal.map((x) => (
              <TerminalRow
                key={x.id}
                x={x}
                u={u}
                color={colors.get(x.id)!}
                whole={whole}
                on={on(x.id)}
                many={many}
                alone={solo === x.id}
                onToggle={() => toggle(x.id)}
                onOnly={() => (solo === x.id ? everyone() : only(x.id))}
              />
            ))}
          </section>
        </div>
      </div>
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
  const H = week ? 136 : 120;
  const room = H - 14; // the scale's label above the highest bar
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
      <div role="group" aria-label={t("usage.chart")} className="relative border-b border-fill-2" style={{ height: H }}>
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
            const parts = order.filter((id) => on(id) && day?.get(id) && totalOf(day.get(id)!) > 0);
            return (
              <Tip
                key={d}
                label={
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
                }
              >
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
                      const h = x && on(id) && max ? (totalOf(x) / max) * room : 0;
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
              {d === today ? t("usage.today") : new Date(`${d}T00:00`).toLocaleDateString(document.documentElement.lang || undefined, { weekday: "narrow" })}
            </span>
          ))}
        </div>
      ) : (
        <div className="flex justify-between text-[11px] text-ink-3 tabular-nums">
          <span>{dayLabel(days[0]!, false)}</span>
          <span>{t("usage.today")}</span>
        </div>
      )}
    </div>
  );
}

/** A model without its provider ("opencode-go/deepseek-flash": deepseek-flash). */
const shortModel = (m: ModelSum) => m.model.split("/").pop() || agentName(m.agent);

/** A terminal: its colour's dot, its avatar and name, its models in a line under it, its total
 *  and its part of everyone's in its colour; the finer figures in its tip. Pressed, it leaves the
 *  chart (its dot a ring, its line faint) or comes back. On its right, 仅此 shows it alone (always
 *  there on a touch screen, else as the pointer is over it); on the one alone, 全部. */
function TerminalRow({ x, u, color, whole, on, many, alone, onToggle, onOnly }: { x: TerminalSum; u: Usage | undefined; color: string; whole: number; on: boolean; many: boolean; alone: boolean; onToggle: () => void; onOnly: () => void }) {
  const w = who(u, x.id);
  const v = totalOf(x.sum);
  const tip = (
    <span className="flex flex-col tabular-nums">
      {x.models.map((m) => (
        <span key={m.key}>
          {`${agentName(m.agent)} · ${m.model || "—"} · ${tokens(totalOf(m.sum))}`}
          <span className="block text-ink-3">{`${t("usage.in", { n: m.sum.input.toLocaleString() })} · ${t("usage.out", { n: m.sum.output.toLocaleString() })} · ${t("usage.cacheRead", { n: m.sum.cacheRead.toLocaleString() })} · ${t("usage.cacheWrite", { n: m.sum.cacheWrite.toLocaleString() })}`}</span>
        </span>
      ))}
    </span>
  );
  const body = (
    <span className="flex min-w-0 flex-1 items-center gap-2.5">
      <Dot color={color} on={on} />
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
    </span>
  );
  if (!many)
    return (
      <Tip label={tip} align="start">
        <div tabIndex={0} className="-mx-1.5 flex items-center rounded-md px-1.5 py-2 outline-offset-1">
          {body}
        </div>
      </Tip>
    );
  return (
    <div className="group -mx-1.5 flex items-center gap-1 rounded-md pr-1 hover:bg-fill">
      <Tip label={tip} align="start">
        <button type="button" aria-pressed={on} aria-label={w.name} onClick={onToggle} className="g-press flex min-w-0 flex-1 items-center rounded-md py-2 pl-1.5 text-left outline-offset-1">
          {body}
        </button>
      </Tip>
      <button
        type="button"
        onClick={onOnly}
        aria-label={alone ? t("usage.allOf") : t("usage.onlyOf", { name: w.name })}
        className={cn(
          "g-press h-6 w-11 shrink-0 rounded-full text-xs text-ink-2 transition-[opacity,background-color] duration-(--dur-2) hover:bg-fill-2 hover:text-ink focus-visible:opacity-100",
          alone ? "bg-fill-2 opacity-100" : "opacity-0 group-hover:opacity-100 pointer-coarse:opacity-100",
        )}
      >
        <span key={String(alone)} className="g-fade block">
          {alone ? t("usage.all") : t("usage.only")}
        </span>
      </button>
    </div>
  );
}
