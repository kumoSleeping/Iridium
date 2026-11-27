// What an agent did after a message of its own, as one line under its words (docs/next/ui.md §3
// "在干活"; a message of kind "work", no words, is this line alone). One line high whatever it says:
// where the agent works when that is not the channel's folder, then while it runs its step and the
// call, the stop key at the right; done, how many steps and how long. What older messages say they
// did before them (<iridium:activity>) is the same line. Pressed, the steps open under it in place,
// one a row, read from the agent's own record over the lines the work spans; a step pressed, its
// card (input, output, diff) from that row. Which lines are open is kept in this browser.
import { Popover } from "@base-ui/react/popover";
import { ChevronRight, FoldVertical, Plug } from "lucide-react";
import { useEffect, useMemo, useRef, useState, useSyncExternalStore, type CSSProperties, type ReactNode, type RefObject } from "react";
import type { Block, SessionState } from "../api/types.gen";
import { useCarriedPick } from "../store";
import { t, useLang } from "../i18n";
import { cn, keep, kept, PHONE, PopSurface, useEver } from "../ui";
import { moveDown } from "./stick";
import { useAway, Where } from "./away";
import type { Did, Work } from "./channel/lib";
import { cmdOfCall, isShell, type Cmd } from "./cmd";
import { bgLaunch, diffSum, foldLine, kindOf, runCmds, type Step } from "./fold";
import { StopKey } from "./live";
import { Pictures } from "./pictures";
import { DiffStat, KindIcon, lasted, ROW, StepCard, StepLine, tok, took, tookOf, wholeAhead } from "./said";
import { allStepsOf, readSteps, spanBlocks, spanSub, stepsOf } from "./spans";

export { allStepsOf, readSteps, stepsOf };

/** A compaction as one of the line's steps (kumo, 2026-11-26: a tool's row with its icon, each
 *  agent's own, not a rule across the channel): going on, the seconds so far, counted from when it
 *  began; done, the context before and after it. */
function Compact({ since, done }: { since?: number; done?: Block }) {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (done) return;
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(timer);
  }, [!!done]);
  const c = done?.compact;
  const says = done ? (c?.before ? (c.after ? `${tok(c.before)} → ${tok(c.after)}` : tok(c.before)) : "") : since && now - since >= 3000 ? took(now - since) : "";
  const label = t(done ? (c?.auto ? "sys.compactAuto" : "sys.compact") : "sys.compacting");
  return (
    <span role={done ? undefined : "status"} title={done ? [label, c?.ms ? took(c.ms) : ""].filter(Boolean).join(" · ") : undefined} className="g-fade flex shrink-0 items-center gap-1.5 whitespace-nowrap">
      <FoldVertical size={14} aria-hidden className={cn("shrink-0", done ? "text-ink-3" : "animate-pulse text-(--compact)")} />
      {!done && <span className="text-ink-2">{label}</span>}
      {says && <span className="text-xs tabular-nums">{says}</span>}
    </span>
  );
}

/** What a line's steps are narrowed to: the calls by one name ("": thinking), of one sort of
 *  shell command where its icons are apart, the failed ones apart too. */
export type Only = { name: string; cmd?: Cmd; failed?: boolean; sorted?: boolean };
const sameOnly = (a: Only | null, b: Only | null) => !!a && !!b && a.name === b.name && a.cmd === b.cmd && a.failed === b.failed && a.sorted === b.sorted;
const isOnly = (s: Step, o: Only | null) => !o || (o.name === "" ? s.kind === "thinking" : s.kind === "tool" && s.name === o.name && (!o.sorted || (cmdOfCall(s) === o.cmd && !!s.result?.error === !!o.failed)));

const useSpan = (session: string, from: number | undefined): Block[] | undefined => useSyncExternalStore(spanSub, () => (from === undefined ? undefined : spanBlocks(session, from)));

// --- which lines are open, kept in this browser ---

const OPEN_KEY = "work-open";
const OPEN_KEEP = 300;
let opens: string[] | undefined;
const openSubs = new Set<() => void>();
const openList = () => {
  if (!opens) {
    try {
      const v = JSON.parse(kept(OPEN_KEY) ?? "[]");
      opens = Array.isArray(v) ? v.filter((x): x is string => typeof x === "string") : [];
    } catch {
      opens = [];
    }
  }
  return opens;
};
/** Whether the line of message n of channel is open. */
export const isOpen = (channel: string, n: number) => openList().includes(`${channel} ${n}`);
function setOpen(channel: string, n: number, open: boolean) {
  const k = `${channel} ${n}`;
  const rest = openList().filter((x) => x !== k);
  opens = open ? [...rest, k].slice(-OPEN_KEEP) : rest;
  keep(OPEN_KEY, JSON.stringify(opens));
  for (const f of openSubs) f();
}
const openSub = (f: () => void) => (openSubs.add(f), () => void openSubs.delete(f));
const useOpen = (channel: string, n: number) => useSyncExternalStore(openSub, () => isOpen(channel, n));

// --- the line ---

/** How long, in words: 12 秒, 3 分 5 秒. */
const tookWords = (ms: number) => {
  const s = Math.max(1, Math.round(ms / 1000));
  if (s < 60) return t("work.sec", { s });
  const m = Math.floor(s / 60);
  if (m < 60) return t("work.min", { m, s: s % 60 });
  return t("work.hour", { h: Math.floor(m / 60), m: m % 60 });
};

/** How long since since (ms), told again each second. */
function Elapsed({ since }: { since: number }) {
  const [now, setNow] = useState(Date.now);
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(id);
  }, []);
  return <span className="ml-auto shrink-0 pl-2 text-xs text-ink-4 tabular-nums">{tookWords(Math.max(0, now - since))}</span>;
}

/** What older messages say was done before them, in the line's words. */
function didParts(did: Did): string[] {
  const parts: string[] = [];
  if (did.bash) parts.push(`bash ${did.bash}${did.cmds ? `（${did.cmds}）` : ""}${did.failed ? t("channel.did.failed", { n: did.failed }) : ""}`);
  if (did.files) parts.push(`${t("channel.did.files", { n: did.files })} +${did.add} −${did.del}`);
  if (did.read) parts.push(t("channel.did.read", { n: did.read }));
  if (did.web) parts.push(t("channel.did.web", { n: did.web }));
  if (did.agents) parts.push(t("channel.did.agents", { n: did.agents }));
  if (did.other) parts.push(t("channel.did.other", { n: did.other }));
  return parts;
}

/** A step pressed: its card, out of its row (kept as it shuts, to fold back into it). */
/** How long an agent that has done nothing yet in its turn is said to be being reached. */
const REACH = 4000;

/** The card open out of a row: key the row, group its steps (their places), at the one chosen. */
type Card = { key: string; at: string; group: string[]; el: HTMLElement; open: boolean; over?: Over };
/** On a phone, where a step's card stands: right over the row it is of, as wide as the window less
 *  its margins, as tall as what it holds up to under the top bar (kumo, 2026-11-25). row: the
 *  row's box; room: px from under the top bar to it. Taken as it opens. */
type Over = { row: DOMRect; room: number };

/** A row nearer the top than ROOM: the conversation moved down first to make that much room over
 *  it, as far as it goes. Not on a phone: undefined. */
const ROOM = 280;
function overRow(el: HTMLElement): Over | undefined {
  if (!matchMedia(PHONE).matches) return undefined;
  const log = el.closest<HTMLElement>("[role=log]");
  const top = (log ? log.getBoundingClientRect().top + parseFloat(getComputedStyle(log).paddingTop || "0") : 0) + 4;
  const room = () => el.getBoundingClientRect().top - 4 - top;
  if (room() < ROOM) moveDown(log, ROOM - room());
  return { row: el.getBoundingClientRect(), room: Math.max(120, room()) };
}

/** An agent's work on a message of channel (n, its number): work, as the message carries it; or
 *  did, what an older message says was done before it, and span: where that is in the record, if
 *  it says. session: the agent's, whose record it is. may: the one looking may stop it. esc: Esc
 *  in the composer stops it too (the channel's one agent). */
export function WorkLine({ channel, n, session, work, did, span, may, esc }: { channel: string; n: number; session: string; work?: Work; did?: Did; span?: { from: number; to: number }; may: boolean; esc?: boolean }) {
  useLang();
  const away = useAway(channel, session);
  const from = work ? work.from : span?.from;
  const to = work ? work.to : span?.to;
  const running = !!work?.running;
  const steps = work?.steps ?? 0;
  // Nothing to open till it has done a step: before that it only says it is being reached (kumo, 2026-11-24).
  const opens = from !== undefined && (steps > 0 || !!span);
  const open = useOpen(channel, n) && opens;
  const blocks = useSpan(session, from);
  const [card, setCard] = useState<Card | null>(null);
  const [only, setOnly] = useState<Only | null>(null);
  // Before its first step: being reached for its first REACH ms, thinking after (kumo, 2026-11-24).
  const began = work?.at ?? 0;
  const [reached, setReached] = useState(() => !began || Date.now() - began >= REACH);
  useEffect(() => {
    if (reached || !work?.running || work.steps) return;
    const id = setTimeout(() => setReached(true), Math.max(0, REACH - (Date.now() - began)));
    return () => clearTimeout(id);
  }, [reached, began, work?.running, work?.steps]);
  const reveal = useRef<HTMLDivElement>(null);
  // All its steps, a row each (kumo, 2026-11-24: 直接全展开): as many rows as the work says it
  // has, so that it is whole as it opens, before they are read.
  const rows = work ? Math.max(steps, 1) : undefined;
  const read = () => from !== undefined && void (work ? readSteps(session, from, undefined, steps) : readSteps(session, from, to)).catch(() => {});
  // When brief summary blocks are already seeded (from /first or device cache) and the line is
  // collapsed and done, avoid firing block.list on mount; full blocks load on hover/focus/press/open.
  const hasSpan = !!blocks?.length;
  useEffect(() => {
    if (open || running || !hasSpan) void read();
  }, [open, steps, running, to, hasSpan]);
  const limit = work ? Math.max(steps, 1) : undefined;
  const all = useMemo(() => (blocks ? allStepsOf(blocks, running, limit) : undefined), [blocks, running, limit]);
  const runs = useMemo(() => (all ? foldLine(all) : []), [all]);
  const diff = useMemo(() => diffSum(runs), [runs]);
  // Its shell commands sent to the background: begun, not done (the work bar has them).
  const bgShells = useMemo(() => (all ?? []).filter((s) => s.kind === "tool" && bgLaunch(s) === "shell").length, [all]);
  // One step done: the step itself on the line, pressed its card; nothing to fold (kumo, 2026-11-25).
  // Running too: its first step is taken for its only one till a second comes, then its sorts' icons (kumo, 2026-11-25).
  const one = all?.length === 1 && (!work || steps <= 1) ? all[0] : undefined;
  // A press anywhere outside the open card puts it away; when that press lands on the button that
  // owns the open card (single-step or list row), remember its key so the click that follows (on
  // mouse or touch) does not open it again. Handled in document capture phase so it runs before
  // Base UI's Popover outside-press closes the card.
  const wasOn = useRef("");
  const wasOnTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  useEffect(() => () => clearTimeout(wasOnTimer.current), []);
  useEffect(() => {
    if (!card?.open) return;
    const down = (e: PointerEvent) => {
      const on = e.target as Element | null;
      if (on?.closest?.("[role=dialog]")) return;
      const hit = !!on && (card.el.contains(on) || on.closest?.<HTMLElement>("[data-step-row]")?.dataset.stepRow === card.key);
      wasOn.current = hit ? card.key : "";
      clearTimeout(wasOnTimer.current);
      if (hit) {
        wasOnTimer.current = setTimeout(() => {
          if (wasOn.current === card.key) wasOn.current = "";
        }, 600);
      }
      setCard((c) => (c?.open ? { ...c, open: false } : c));
    };
    const cancel = () => {
      wasOn.current = "";
      clearTimeout(wasOnTimer.current);
    };
    document.addEventListener("pointerdown", down, true);
    document.addEventListener("pointercancel", cancel, true);
    return () => {
      document.removeEventListener("pointerdown", down, true);
      document.removeEventListener("pointercancel", cancel, true);
    };
  }, [card?.open, card?.key, card?.el]);
  // Its compactions: the one going on now (the agent's own state), and those in what it did.
  // Only whether it compacts, and since when; only while it runs: a line of work done is not
  // drawn again at each thing its agent's state says (its tokens, as it writes).
  const since = useCarriedPick<SessionState, number>(running ? `session:${session}` : null, (st) => (st?.status === "compacting" ? (st.compactingSince ?? 0) : -1));
  const compacting = running && since >= 0;
  const compacted = useMemo(() => (blocks ?? []).filter((b) => b.kind === "system" && b.event === "compact"), [blocks]);
  const parts = did && !work ? didParts(did) : [];
  // An older message's span read and nothing in it: no line.
  if (!work && !parts.length && !runs.length) return null;
  /** Opened or put away; to: narrowed to that sort (pressed again while it is: put away; another
   *  while open: narrowed to that one instead, still open). */
  const toggle = (to: Only | null = null) => {
    if (!opens) return;
    setCard((c) => c && { ...c, open: false });
    const stays = open && !!to && !sameOnly(to, only);
    if (!open) read();
    // Put away, it keeps what it showed: its rows go as they were, not all of them put in first
    // and then shut (kumo, 2026-11-25: 收起时把所有的排了一遍再收起).
    if (!open || stays) setOnly(to);
    if (!stays) setOpen(channel, n, !open);
  };
  // Before any tool step (connecting / thinking): match single-line text-md height (23px) so
  // switching from "思考中" to the first line of streamed text causes zero downward sink (#107).
  const idle = !!work && running && !steps;
  return (
    <div className="min-w-0">
      {/* The room right of its keys opens all of it too (kumo, 2026-11-25); each key keeps its own. */}
      <div onClick={(e) => opens && !one && !(e.target as Element).closest("button") && toggle()} className={cn("flex min-w-0 items-center gap-1.5 text-sm text-ink-3 select-none", idle ? "h-[23px]" : "h-7 pointer-coarse:h-9", opens && !one && "cursor-pointer")}>
        {/* The key that opens all of it, before the rest; where it worked, pressed, the same. */}
        <button
          type="button"
          aria-haspopup={one ? "dialog" : undefined}
          aria-expanded={one ? !!card?.open && card.key === one.at : opens ? open : undefined}
          data-step-row={one ? one.at : undefined}
          disabled={!opens}
          onPointerEnter={() => (read(), void (one && wholeAhead(session, one)))}
          onFocus={() => (read(), void (one && wholeAhead(session, one)))}
          onPointerDown={() => (read(), void (one && wholeAhead(session, one)))}
          onClick={(e) => {
            if (one) {
              const shut = (!!card?.open && card.key === one.at) || wasOn.current === one.at;
              wasOn.current = "";
              clearTimeout(wasOnTimer.current);
              if (shut) return setCard(card ? { ...card, open: false } : null);
              const el = e.currentTarget;
              void Promise.race([wholeAhead(session, one), new Promise((r) => setTimeout(r, 400))]).then(() => setCard({ key: one.at, at: one.at, group: [one.at], el, open: true, over: overRow(el) }));
              return;
            }
            toggle();
          }}
          className={cn("flex h-full min-w-0 items-center gap-1.5 rounded-sm text-left outline-none", !runs.length || one ? "flex-1" : "shrink-0", opens && "cursor-pointer hover:text-ink-2 focus-visible:text-ink-2", (open && !only) || (one && card?.open && card.key === one.at) ? "text-ink-2" : undefined)}
        >
          {opens && !one && <ChevronRight size={13} aria-hidden className={cn("shrink-0 text-ink-4 transition-transform duration-(--dur-2)", open && "rotate-90")} />}
          {away && <Where place={away} short />}
          {one && <StepLine s={one} />}
          {work ? (
            running && (
              <>
                {/* No lamp of its own (its face has one): before its first step a plug being
                    reached, the thinking chip once it thinks; then its sorts as when it is done,
                    no count of steps (kumo, 2026-11-25). */}
                {!steps && (
                  <span key={reached ? "r" : "c"} className="g-fade flex shrink-0 items-center gap-1.5">
                    {reached ? <KindIcon kind="thinking" /> : <Plug size={14} aria-hidden className="shrink-0 text-ink-3" />}
                    {reached ? t("work.thinking") : t("work.reaching")}
                  </span>
                )}
                {!!steps && !runs.length && work.tool && (
                  <span key={`t${steps}`} className="g-fade flex min-w-0 items-center gap-1.5">
                    <KindIcon kind={kindOf(work.tool)} shell={isShell(work.tool)} />
                    <span className="min-w-0 truncate text-ink-2">{work.tool}</span>
                  </span>
                )}
                {!!work.agents && (
                  <span key={`a${work.agents}`} className="g-fade shrink-0 tabular-nums">
                    {t("work.agents", { n: work.agents })}
                  </span>
                )}
              </>
            )
          ) : (
            !runs.length && (
              <span className="flex min-w-0 items-center gap-2 overflow-hidden whitespace-nowrap tabular-nums">
                {parts.map((p, i) => (
                  <span key={i} className={cn(i === parts.length - 1 ? "min-w-0 truncate" : "shrink-0")}>
                    {p}
                  </span>
                ))}
              </span>
            )
          )}
        </button>
        {compacted.map((b) => (
          <Compact key={b.at} done={b} />
        ))}
        {compacting && <Compact since={since || undefined} />}
        {/* What was done, by its sorts, as before (kumo, 2026-11-24): each pressed opens its own
            steps alone. */}
        {!one && runs.length > 0 && all && (
          <span className="g-fade flex min-w-0 flex-1 items-center gap-3 overflow-hidden whitespace-nowrap">
            {runs.map((r) => {
              const groups = r.name ? runCmds(all, r.name) : [{ cmd: undefined, n: r.n, failed: false }];
              const apart = groups.length > 1 || isShell(r.name);
              const key = (o: Only) => cn("flex h-7 shrink-0 cursor-pointer items-center gap-0.5 rounded-sm outline-none hover:text-ink-2 focus-visible:text-ink-2 pointer-coarse:h-9", open && sameOnly(o, only) && "text-ink");
              if (apart)
                return (
                  <span key={r.name} className="flex shrink-0 items-center gap-1.5">
                    {groups.map((g, j) => {
                      const o: Only = { name: r.name, sorted: true, ...(g.cmd ? { cmd: g.cmd } : {}), ...(g.failed ? { failed: true } : {}) };
                      return (
                        <button key={j} type="button" onClick={() => toggle(o)} aria-pressed={open && sameOnly(o, only)} className={key(o)}>
                          <KindIcon kind={kindOf(r.name)} cmd={g.cmd} shell={isShell(r.name)} failed={g.failed} />
                          {g.n > 1 && <span className={cn("text-xs tabular-nums", g.failed && "text-err")}>×{g.n}</span>}
                        </button>
                      );
                    })}
                  </span>
                );
              const o: Only = { name: r.name };
              return (
                <button key={r.name} type="button" onClick={() => toggle(o)} aria-pressed={open && sameOnly(o, only)} className={cn(key(o), "gap-1.5")}>
                  <KindIcon kind={r.name ? kindOf(r.name) : "thinking"} cmd={groups[0]?.cmd} failed={groups[0]?.failed} />
                  <span className="text-ink-2">{r.name || t("msg.thinking")}</span>
                  {r.n > 1 && <span className="text-xs tabular-nums">×{r.n}</span>}
                </button>
              );
            })}
            {diff && <DiffStat d={diff} />}
          </span>
        )}
        {!running && !!work?.agents && <span className="shrink-0 text-xs tabular-nums">{t("work.agents", { n: work.agents })}</span>}
        {!running && !one && bgShells > 0 && <span className="shrink-0 text-xs tabular-nums">{t("work.bgShell")}{bgShells > 1 && ` ×${bgShells}`}</span>}
        {!running && !!work?.ms && <span className="ml-auto shrink-0 pl-2 text-xs text-ink-4 tabular-nums">{tookWords(work.ms)}</span>}
        {/* How long it has been at it, a second at a time (kumo, 2026-11-25). */}
        {running && !!steps && !!work?.at && <Elapsed since={work.at} />}
        {running && may && <StopKey session={session} esc={esc} />}
      </div>
      {opens && <StepList reveal={reveal} session={session} steps={all} only={only} rows={only || !rows ? undefined : rows} open={open} card={card} setCard={setCard} wasOn={wasOn} clearWasOn={() => clearTimeout(wasOnTimer.current)} shut={() => toggle()} />}
    </div>
  );
}

/** Where a step's card opens: under its row, from a fixed way in, so the rows' keys and names to
 *  its left stay seen and pressed one after another (kumo, 2026-11-25); where the pane is too
 *  narrow, as far left as it must. A finger's screen: from the row's start. */
const ASIDE = 176;
const overIt = (o: Over) => ({ getBoundingClientRect: () => new DOMRect(8, o.row.top, window.innerWidth - 16, o.row.height) });

const aside = (el: Element) => ({
  getBoundingClientRect: () => {
    const r = el.getBoundingClientRect();
    const by = matchMedia("(pointer: coarse)").matches ? 0 : Math.min(ASIDE, r.width / 2);
    return new DOMRect(r.left + by, r.top, r.width - by, r.height);
  },
});

/** The steps under a line, opened in place: a row each, all of them, none scrolled in the list
 *  itself. A row pressed: its card out of the row; pressed again, or a press elsewhere, puts it away. */
function StepList({ reveal, session, steps, only, rows, open, card, setCard, wasOn, clearWasOn, shut }: { reveal: RefObject<HTMLDivElement | null>; session: string; steps?: Step[]; only: Only | null; rows?: number; open: boolean; card: Card | null; setCard: (c: Card | null) => void; wasOn: RefObject<string>; clearWasOn: () => void; shut: () => void }) {
  const list = useMemo(() => (steps ?? []).filter((s) => isOnly(s, only)), [steps, only]);
  // Made as it is first opened.
  const ever = useEver(open);
  // Opened already as it is first drawn (kept open): there at once, not opening under the eyes.
  const [snap, setSnap] = useState(open);
  useEffect(() => {
    if (!snap) return;
    const f = requestAnimationFrame(() => setSnap(false));
    return () => cancelAnimationFrame(f);
  }, [snap]);
  const among = card ? list.filter((s) => card.group.includes(s.at)) : [];
  const chosen = card ? (among.find((s) => s.at === card.at) ?? among[0]) : undefined;
  const row = (key: string, group: Step[], body: ReactNode) => {
    const on = !!card?.open && card.key === key;
    return (
      <button
        key={key}
        type="button"
        aria-haspopup="dialog"
        aria-expanded={on}
        data-step-row={key}
        onPointerEnter={() => void wholeAhead(session, group[0]!)}
        onFocus={() => void wholeAhead(session, group[0]!)}
        onPointerDown={() => void wholeAhead(session, group[0]!)}
        onClick={(e) => {
          // Pressed while its card was open: the card is put away (the press outside it did that
          // already), not opened again.
          const shut = on || wasOn.current === key;
          wasOn.current = "";
          clearWasOn();
          if (shut) return setCard(card ? { ...card, open: false } : null);
          // Whole as it opens: what it shows cut short is waited for, a moment at most (a finger
          // points at nothing before it presses). Its place on a phone taken then (overRow).
          const el = e.currentTarget;
          const open = () => setCard({ key, at: group[0]!.at, group: group.map((s) => s.at), el, open: true, over: overRow(el) });
          void Promise.race([wholeAhead(session, group[0]!), new Promise((r) => setTimeout(r, 400))]).then(open);
        }}
        className={cn(ROW, "w-full shrink-0 text-left outline-none select-none focus-visible:text-ink-2", on && "text-ink")}
      >
        {body}
      </button>
    );
  };
  const tookAll = (ss: Step[]) => {
    const ts = ss.map(tookOf);
    return ts.some((x) => x === undefined) ? undefined : ts.reduce<number>((a, b) => a + (b ?? 0), 0);
  };
  const took = (ss: Step[]) => {
    const ms = tookAll(ss);
    return ms !== undefined && <span className="ml-auto shrink-0 pl-2 text-xs text-ink-4 tabular-nums">{lasted(ms)}</span>;
  };
  const sized = !!steps && !!rows;
  return (
    <>
      <div ref={reveal} inert={!open || undefined} aria-hidden={!open || undefined} className={cn("g-reveal", snap && "g-snap")} data-open={open ? "" : undefined}>
        <div className="min-h-0 min-w-0 overflow-hidden">
          {ever && (
            <div style={sized ? ({ "--rows": Math.max(rows, list.length) } as CSSProperties) : undefined} className={cn("ml-[3px] flex min-w-0 flex-col overflow-hidden pl-3 shadow-[inset_1px_0_0_var(--edge)]", sized && "min-h-[calc(var(--rows)*28px)] pointer-coarse:min-h-[calc(var(--rows)*36px)]")}>
              {list.map((s) =>
                row(
                  s.at,
                  [s],
                  <>
                    <StepLine s={s} />
                    {took([s])}
                  </>,
                ),
              )}
            </div>
          )}
          {/* Put away from its end too, where a long one leaves its line far above (kumo,
              2026-11-24): the whole row from the left edge, under the line's own key, the line
              over it ending before it. */}
          {ever && (
            <button type="button" onClick={shut} className={cn(ROW, "w-full shrink-0 cursor-pointer gap-1.5 text-left text-xs text-ink-4 outline-none select-none hover:text-ink-2 focus-visible:text-ink-2")}>
              <ChevronRight size={13} aria-hidden className="shrink-0 -rotate-90" />
              {t("nav.less")}
            </button>
          )}
        </div>
      </div>
      {/* One for each row: a still card's place is taken as it opens, and another row pressed
          while the first one's card was still going away opened in that one, at its place
          (kumo, 2026-11-25). */}
      {(ever || !!card) && (
        <Popover.Root key={card?.key} open={!!card?.open && !!chosen} onOpenChange={(o) => !o && card && setCard({ ...card, open: false })}>
          <PopSurface anchor={card?.over ? overIt(card.over) : card?.el && aside(card.el)} side={card?.over ? "top" : "bottom"} still sideOffset={4} style={card?.over ? { maxHeight: card.over.room } : undefined} className={cn("flex w-[min(620px,calc(100vw-16px))] flex-col overflow-hidden", card?.over ? "bg-(--glass-thick-solid)!" : "max-h-[min(72dvh,620px,var(--available-height))]")}>
            {chosen && card && (
              <>
                {among.length > 1 && (
                  <div className="flex max-h-[168px] shrink-0 flex-col overflow-y-auto overscroll-contain border-b border-edge px-3 py-1 pointer-coarse:max-h-[216px]">
                    {among.map((s) => (
                      <button key={s.at} type="button" aria-pressed={s.at === chosen.at} onClick={() => setCard({ ...card, at: s.at })} className={cn(ROW, "w-full shrink-0 text-left outline-none focus-visible:text-ink-2", s.at === chosen.at && "text-ink")}>
                        <StepLine s={s} />
                        {took([s])}
                      </button>
                    ))}
                  </div>
                )}
                <StepCard key={chosen.at} s={chosen} session={session} />
                {chosen.kind === "tool" && !!chosen.result?.images?.length && (
                  <div className="px-3 pb-3">
                    <Pictures session={session} s={chosen} />
                  </div>
                )}
              </>
            )}
          </PopSurface>
        </Popover.Root>
      )}
    </>
  );
}
