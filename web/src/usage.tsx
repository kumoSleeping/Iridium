// The usage (plan "用量"): the tokens the turns used, by day, terminal, agent and model (GET
// /api/usage). One page (#/settings/usage, from the 终端 panel; #/settings/usage/<id> one
// terminal's), one way through it (the user, 2026-11-08: no detours): the range's total, a bar a
// day, then the terminals, the most first; a bar or a terminal pressed narrows it to that day or
// that terminal, a chip at the top to undo it; a terminal's is its models. The finer figures are
// in a model's tip. One colour: the bars are ink, the agents' marks the only colour.
import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { ChevronLeft, ChevronRight, CircleHelp, LoaderCircle, UserX, X } from "lucide-react";
import { useMemo, useState, type ReactNode } from "react";
import { api } from "./api/client";
import type { Agent, Usage, UsageRow } from "./api/types.gen";
import { t, useLang } from "./i18n";
import { LocalMark, Segmented } from "./me";
import { route } from "./route";
import { errText } from "./toast";
import { agentName, Avatar, cn, IconKey, Logo, Tip, tokens } from "./ui";

const dollars = (n: number) => (n >= 100 ? `$${Math.round(n)}` : `$${n.toFixed(2)}`);

const RANGES = [7, 30] as const;

/** The last days' usage; terminal: one only. The last answer stays while another range loads. */
export const useUsage = (days: number, terminal?: string) =>
  useQuery({ queryKey: ["usage", days, terminal ?? ""], queryFn: () => api.usage(days, terminal), staleTime: 30_000, placeholderData: keepPreviousData });

type Sum = { turns: number; input: number; output: number; cacheRead: number; cacheWrite: number; cost: number };
const zero = (): Sum => ({ turns: 0, input: 0, output: 0, cacheRead: 0, cacheWrite: 0, cost: 0 });
const add = (s: Sum, r: UsageRow) => {
  s.turns += r.turns;
  s.input += r.input;
  s.output += r.output;
  s.cacheRead += r.cacheRead;
  s.cacheWrite += r.cacheWrite;
  s.cost += r.costUsd ?? 0;
};
export const totalOf = (s: Sum) => s.input + s.output + s.cacheRead + s.cacheWrite;

type ModelSum = { key: string; agent: Agent; model: string; sum: Sum };
type TerminalSum = { id: string; sum: Sum; models: ModelSum[] };

/** Every day's sum, the whole range's, by terminal (each by model) and by model, the most first. */
function summarize(rows: UsageRow[], day: string | null) {
  const perDay = new Map<string, Sum>();
  const all = zero();
  const terms = new Map<string, { id: string; sum: Sum; models: Map<string, ModelSum> }>();
  const models = new Map<string, ModelSum>();
  for (const r of rows) {
    const d = perDay.get(r.day) ?? zero();
    perDay.set(r.day, d);
    add(d, r);
    if (day && r.day !== day) continue;
    add(all, r);
    const k = `${r.agent}\u0000${r.model}`;
    const tm = terms.get(r.terminal) ?? { id: r.terminal, sum: zero(), models: new Map() };
    terms.set(r.terminal, tm);
    add(tm.sum, r);
    const tmm = tm.models.get(k) ?? { key: k, agent: r.agent, model: r.model, sum: zero() };
    tm.models.set(k, tmm);
    add(tmm.sum, r);
    const m = models.get(k) ?? { key: k, agent: r.agent, model: r.model, sum: zero() };
    models.set(k, m);
    add(m.sum, r);
  }
  const most = <T extends { sum: Sum }>(xs: Iterable<T>) => [...xs].sort((a, b) => totalOf(b.sum) - totalOf(a.sum));
  const byTerminal: TerminalSum[] = most(terms.values()).map((x) => ({ id: x.id, sum: x.sum, models: most(x.models.values()) }));
  return { perDay, all, byTerminal, byModel: most(models.values()) };
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

export function UsagePage({ onBack }: { onBack: () => void }) {
  useLang();
  // Its terminal as it opened: the route may move on while it slides away. Opened for one, it is
  // that one's only; else one pressed narrows it.
  const [only] = useState(() => {
    const r = route();
    return r.name === "usage" ? r.terminal : undefined;
  });
  const [days, setDays] = useState<number>(7);
  const [day, setDay] = useState<string | null>(null);
  const [picked, setPicked] = useState<string | null>(null);
  const q = useUsage(days, only);
  const u = q.data;
  const terminal = only ?? picked;
  const rows = useMemo(() => (u?.rows ?? []).filter((r) => !picked || r.terminal === picked), [u, picked]);
  const s = useMemo(() => summarize(rows, day), [rows, day]);
  // One terminal, or the only one there was: its models; else the terminals.
  const one = terminal ?? (s.byTerminal.length === 1 ? s.byTerminal[0]!.id : undefined);
  const w = one !== undefined ? who(u, one) : null;
  const chip = (label: ReactNode, clear: () => void) => (
    <button type="button" onClick={clear} className="g-pop g-press -my-1 flex h-6 min-w-0 items-center gap-1 rounded-full bg-fill px-2 text-ink-2 hover:bg-fill-2">
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
            <div className="flex min-w-0 items-center gap-1.5 text-xs text-ink-3">
              {only && w && (
                <span className="flex min-w-0 items-center gap-1.5 text-ink-2">
                  {w.mark(16)}
                  <span className="truncate">{w.name}</span>
                </span>
              )}
              {picked && chip(who(u, picked).name, () => setPicked(null))}
              {day ? chip(dayLabel(day), () => setDay(null)) : <span className="shrink-0">{t("usage.lastDays", { n: days })}</span>}
            </div>
            <div>
              {/* The figure leads: proportional digits, as a figure that stands alone. */}
              <div className="text-[30px] leading-9 font-semibold tracking-tight">{tokens(totalOf(s.all))}</div>
              <div className="text-xs text-ink-3 tabular-nums">
                {t("usage.tokensTurns", { n: s.all.turns })}
                {s.all.cost > 0 && ` · ${dollars(s.all.cost)}`}
              </div>
            </div>
            {u && <Bars days={u.days} perDay={s.perDay} sel={day} onSel={setDay} height={days === 7 ? 88 : 72} labels={days === 7} />}
          </section>

          <section key={`${one ?? ""}-${day ?? ""}-${days}`} className="g-fade flex flex-col" aria-label={w ? w.name : t("usage.byTerminal")}>
            {u && s.all.turns === 0 && <p className="m-0 py-6 text-center text-sm text-ink-3">{t("usage.none")}</p>}
            {one !== undefined
              ? (s.byTerminal[0]?.models ?? []).map((m) => <ModelRow key={m.key} m={m} whole={totalOf(s.all)} />)
              : s.byTerminal.map((x) => <TerminalRow key={x.id} x={x} u={u} whole={totalOf(s.all)} onPick={() => setPicked(x.id)} />)}
          </section>
        </div>
      </div>
    </div>
  );
}

/** A bar a day, the tokens' height, from one baseline; pressed, a day is chosen (pressed again,
 *  the range); each says its day and total. labels: each day's weekday under it (a week), else the
 *  range's ends. */
function Bars({ days, perDay, sel, onSel, height, labels }: { days: string[]; perDay: Map<string, Sum>; sel: string | null; onSel: (d: string | null) => void; height: number; labels?: boolean }) {
  const max = Math.max(1, ...days.map((d) => totalOf(perDay.get(d) ?? zero())));
  const today = days[days.length - 1];
  return (
    <div className="flex flex-col gap-1">
      <div role="group" aria-label={t("usage.chart")} className="flex items-end gap-0.5 border-b border-fill-2" style={{ height }}>
        {days.map((d) => {
          const sum = perDay.get(d);
          const v = sum ? totalOf(sum) : 0;
          const on = sel === d;
          const label = `${dayLabel(d)} · ${t("usage.tokens", { n: tokens(v) })}${sum?.cost ? ` · ${dollars(sum.cost)}` : ""}`;
          return (
            <Tip key={d} label={label}>
              <button type="button" aria-label={label} aria-pressed={on} onClick={() => onSel(on ? null : d)} className="group flex h-full min-w-0 flex-1 items-end justify-center outline-offset-1">
                <span
                  className={cn(
                    "block w-full max-w-6 rounded-t-[3px] transition-[height,background-color] duration-(--dur-3) ease-(--ease-out)",
                    !v ? "bg-transparent" : on ? "bg-ink" : sel ? "bg-ink-4 group-hover:bg-ink-3" : "bg-ink-3 group-hover:bg-ink-2",
                  )}
                  style={{ height: v ? Math.max(3, Math.round((v / max) * (height - 4))) : 0 }}
                />
              </button>
            </Tip>
          );
        })}
      </div>
      {labels ? (
        <div className="flex gap-0.5 text-[11px] text-ink-3 tabular-nums">
          {days.map((d) => (
            <span key={d} className={cn("min-w-0 flex-1 truncate text-center", (d === sel || (!sel && d === today)) && "font-medium text-ink-2")}>
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

/** A share of the whole, as a thin bar: a part, no axis. */
function Share({ v, whole }: { v: number; whole: number }) {
  return (
    <span className="block h-1 overflow-hidden rounded-full bg-fill" aria-hidden>
      <span className="block h-full rounded-full bg-ink-3 transition-[width] duration-(--dur-3) ease-(--ease-out)" style={{ width: `${whole ? Math.max(1, (v / whole) * 100) : 0}%` }} />
    </span>
  );
}

/** A terminal: its total, its part of the whole; pressed, the page narrows to it. */
function TerminalRow({ x, u, whole, onPick }: { x: TerminalSum; u: Usage | undefined; whole: number; onPick: () => void }) {
  const w = who(u, x.id);
  const v = totalOf(x.sum);
  return (
    <button type="button" onClick={onPick} className="g-press -mx-1.5 flex items-center gap-2.5 rounded-md px-1.5 py-2 text-left hover:bg-fill">
      {w.mark(24)}
      <span className="min-w-0 flex-1">
        <span className="flex items-baseline gap-2">
          <span className="min-w-0 flex-1 truncate text-sm font-medium">{w.name}</span>
          {x.sum.cost > 0 && <span className="shrink-0 text-xs text-ink-3 tabular-nums">{dollars(x.sum.cost)}</span>}
          <span className="shrink-0 text-sm font-medium tabular-nums">{tokens(v)}</span>
        </span>
        <span className="mt-1 block">
          <Share v={v} whole={whole} />
        </span>
      </span>
      <ChevronRight size={15} className="shrink-0 text-ink-4" />
    </button>
  );
}

/** A model without its provider ("opencode-go/deepseek-flash": deepseek-flash); the tip has it whole. */
const shortModel = (m: ModelSum) => m.model.split("/").pop() || agentName(m.agent);

/** One agent's model: its total and cost, its turns, its part of the whole; the finer figures in
 *  its tip. */
function ModelRow({ m, whole }: { m: ModelSum; whole: number }) {
  const s = m.sum;
  const v = totalOf(s);
  const full = [
    `${agentName(m.agent)} · ${m.model || "—"}`,
    `${t("usage.in", { n: s.input.toLocaleString() })} · ${t("usage.out", { n: s.output.toLocaleString() })}`,
    `${t("usage.cacheRead", { n: s.cacheRead.toLocaleString() })} · ${t("usage.cacheWrite", { n: s.cacheWrite.toLocaleString() })}`,
  ];
  return (
    <Tip
      label={
        <span className="flex flex-col tabular-nums">
          {full.map((l) => (
            <span key={l}>{l}</span>
          ))}
        </span>
      }
      align="start"
    >
      <div tabIndex={0} className="flex items-start gap-2.5 rounded-md py-2 outline-offset-1">
        <span className="grid h-5 w-6 shrink-0 place-items-center">
          <Logo agent={m.agent} size={15} />
        </span>
        <span className="min-w-0 flex-1">
          <span className="flex items-baseline gap-2">
            <span className="min-w-0 flex-1 truncate text-sm font-medium">{shortModel(m)}</span>
            {s.cost > 0 && <span className="shrink-0 text-xs text-ink-3 tabular-nums">{dollars(s.cost)}</span>}
            <span className="shrink-0 text-sm font-medium tabular-nums">{tokens(v)}</span>
          </span>
          <span className="block truncate text-xs text-ink-3 tabular-nums">{t("usage.turns", { n: s.turns })}</span>
          <span className="mt-1.5 block">
            <Share v={v} whole={whole} />
          </span>
        </span>
      </div>
    </Tip>
  );
}

