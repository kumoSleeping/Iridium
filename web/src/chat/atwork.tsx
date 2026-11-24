// A turn at work as it shows at the conversation's end: the row of what is being written or run
// (Activity), and another agent of the channel at work (OtherTurn), drawn as its own page draws
// its turn (kumo, 2026-11-18: what an agent does looks the same wherever one looks at it), not
// dots alone.
import { useEffect, useRef, useState, type ReactNode } from "react";
import type { SessionState as LiveSession } from "../api/types.gen";
import type { Block } from "../api/types.gen";
import { chatOf, fresh, type Draft as Drafted } from "../chats";
import { t, useLang } from "../i18n";
import { openChat } from "../route";
import { carries, drafts, opened } from "../stream";
import { cn } from "../ui";
import { liveOf, type AgentRow, type Src } from "./channel/lib";
import { AgentFace } from "./channel/members";
import { useAway, Where } from "./away";
import { renameOnly } from "./cmd";
import { folded, foldKey, type Tool } from "./fold";
import { toldOf } from "./dispatch";
import { StopKey, ToolDraft, Waiting } from "./live";
import { Draft } from "./markdown";
import { Fold, Running, Said, StepsHead, When, useWhenRow, type LiveCall, type LiveFold } from "./said";

/** A call being written that only names the session (ir rename): left out, as its block is. */
const renamesDraft = (d: Drafted) => !!d.tool && renameOnly({ name: d.tool.name, input: d.text });

/** The turn's row at the end of the conversation: the call being written, else the one running
 *  (the newest, how many more beside it), else the dots once it has gone quiet; one line high, even
 *  empty, while the turn lasts. */
/** under: in a turn's frame of its own, under the face and name (the turn's first). stop: the
 *  agent's stop key, at the row's right. */
export function Activity({ calls, running, cwd, quiet, grew, under, stop }: { calls: Drafted[]; running: Tool[]; cwd: string; quiet: boolean; grew: string; under?: boolean; stop?: ReactNode }) {
  const writing = calls.at(-1);
  const run = running.at(-1);
  return (
    <div className={cn("flex h-7 min-w-0 items-center pointer-coarse:h-9", under ? "-mt-1" : "-mt-3 pl-[38px]")}>
      {/* One box for a call, written then running, under its id: it fades in once, not again
          as the call written begins to run. */}
      {writing?.tool ? (
        <div key={writing.tool.id} className="g-fade flex min-w-0 flex-1 items-center gap-2">
          <div className="min-w-0 flex-1">
            <ToolDraft name={writing.tool.name} input={writing.text} whole={writing.whole} plain />
          </div>
        </div>
      ) : run ? (
        <div key={run.id ?? run.at} className="g-fade flex min-w-0 flex-1 items-center gap-2">
          <div className="min-w-0 flex-1">
            <Running tool={run} cwd={cwd} />
          </div>
          {running.length > 1 && <span className="shrink-0 text-xs text-ink-4 tabular-nums">+{running.length - 1}</span>}
        </div>
      ) : (
        <Waiting on={quiet} quiet={grew} />
      )}
      {stop}
    </div>
  );
}

/** A turn's frame: the face at the left, the head's line over the rest. */
/** bare: no face nor head (under its own last words), their room kept: the same boxes either
 *  way, so that what is in it is not taken out and put in again as the face comes or goes. */
export function TurnFrame({ face, head, bare, children }: { face: ReactNode; head: ReactNode; bare?: boolean; children: ReactNode }) {
  const row = useWhenRow();
  return (
    <div {...row} className={cn(row.className, "flex min-w-0 items-start gap-2.5")}>
      {bare ? <span aria-hidden className="w-7 shrink-0" /> : face}
      <div className="flex min-w-0 flex-1 flex-col gap-5">
        {!bare && <div className="-mb-3.5 flex h-4 min-w-0 items-center gap-1.5">{head}</div>}
        {children}
      </div>
    </div>
  );
}

/** Its blocks are drawn as its own page draws them: who sent words is not asked (what it was
 *  given is the channel's, drawn there). */
const noSource = () => ({ kind: "you" }) as ReturnType<Parameters<typeof Said>[0]["source"]>;

/** Another agent of the channel at work: its face and name, as its words will be, then what it
 *  has done in this turn since it last spoke in the channel, read from its record as it grows
 *  (its state's end on the stream), folded as its own page folds a turn at work; the call it
 *  runs, or the dots, and its stop key at the end. alone: under its own last words, no face
 *  again. said: where its last words in the channel are from in its record (their work is
 *  under them). */
/** past: a part of its turn done before it was given something more (lib.ts liveParts), drawn
 *  as it was, at rest: no row of what runs, nothing asked for. grown: told as its record brings
 *  something it was given (the page then cuts its turn there). */
export function OtherTurn({ a, channel, st, said, spoken, alone, bars, call, record, may, past, grown, esc }: { past?: Block[]; grown?: () => void; spoken?: ReadonlySet<string>; a: AgentRow; channel: string; st?: LiveSession; said?: Src; alone: boolean; bars: ReactNode; call?: (nick: string) => void; record: (session: string) => void; may: boolean; esc?: boolean }) {
  useLang();
  const [chat] = useState(() => chatOf(a.session));
  const log = chat.log;
  const [, redraw] = useState(0);
  const changed = () => redraw((n) => n + 1);
  useEffect(() => {
    let live = true;
    const more = () => void log.newer().then(() => live && changed(), () => {});
    if (past) return;
    if (!log.loaded || !fresh(log.came)) more();
    const hear = (d: { session: string }) => d.session === a.session && (log.wrote(d as never), changed());
    const back = () => (log.dropDrafts(), more());
    drafts.add(hear);
    opened.add(back);
    return () => {
      live = false;
      drafts.delete(hear);
      opened.delete(back);
    };
  }, [a.session, !past]);
  useEffect(() => void (st && !past && (log.procIs(st.proc), changed())), [st?.proc, !past]);
  // Its record grew: what came since.
  const carried = carries(`session:${a.session}`);
  const end = st?.end ?? 0;
  // Not come (a weak link to its computer): asked again, a little later each time; left at that,
  // its steps stood still till its record next grew.
  useEffect(() => {
    if (past || !log.loaded || (carried && end <= log.end)) return;
    if (st?.tail && log.fromTail(st.tail)) return void changed();
    let live = true;
    let tries = 0;
    let again = 0;
    const ask = () =>
      void log.newer().then(
        () => live && changed(),
        () => {
          if (live && tries < 5) again = window.setTimeout(ask, Math.min(8000, 600 * 2 ** tries++));
        },
      );
    ask();
    return () => {
      live = false;
      clearTimeout(again);
    };
  }, [end, log.loaded, !past]);
  // Given something more: the page cuts the turn there.
  const users = log.blocks.reduce((n, b) => n + (b.kind === "user" ? 1 : 0), 0);
  const usersWas = useRef(users);
  useEffect(() => {
    if (usersWas.current === users) return;
    usersWas.current = users;
    if (!past) grown?.();
  }, [users]);
  const away = useAway(channel, a.session);
  // past: a part of the turn already done, nothing of it still running.
  const shown = folded(past ?? liveOf(log.blocks, said, spoken ? (x) => spoken.has(x) : undefined), !past);
  const running = shown.flatMap((b) => (b.kind === "running" ? [b.tool] : []));
  const rows = shown.filter((b) => b.kind !== "running" && b.kind !== "user");
  const textDrafts = past ? [] : log.drafts.filter((d) => !d.tool && d.text.trim() && !(spoken && spoken.has(d.text.trim())));
  const toolDrafts = past ? [] : log.drafts.filter((d) => d.tool && !renamesDraft(d));
  const cwd = st?.cwd ?? "";
  const stop = may && !past ? <StopKey session={a.session} esc={esc} /> : null;
  const runningIds = new Set(running.map((t) => t.id).filter(Boolean));
  const active: LiveCall[] = [
    ...running.map((tool) => ({ kind: "running" as const, tool })),
    ...toolDrafts.filter((d) => d.tool && !runningIds.has(d.tool.id)).map((d) => ({ kind: "draft" as const, id: d.tool!.id, name: d.tool!.name, text: d.text, whole: d.whole })),
  ];
  const textDrafting = textDrafts.length > 0;
  const grew = `${a.session} ${log.end} ${running.length} ${log.drafts.length} ${log.drafts.at(-1)?.text.length ?? 0}`;
  const lastRow = rows.at(-1);
  const lastFoldable = lastRow?.kind === "fold" && !(lastRow.steps.length === 1 && lastRow.steps[0]?.kind === "tool" && toldOf(lastRow.steps[0]));
  const liveFold: LiveFold | undefined = !past ? { active, stop, quiet: !textDrafts.some((d) => !d.whole), grew } : undefined;
  const mergeLast = !past && !textDrafting && lastFoldable;
  const synthLive = !past && !textDrafting && !lastFoldable && active.length > 0;
  const mergedWork = mergeLast || synthLive;
  const node = (b: (typeof rows)[number], i: number) => {
    const key = b.kind === "fold" ? (foldKey(b.steps) ?? b.at) : b.at;
    return <Said key={key} b={b} session={a.session} cwd={cwd} source={noSource} onJump={openChat} live={mergeLast && i === rows.length - 1 ? liveFold : undefined} />;
  };
  const time = rows.map((b) => (b.kind === "fold" ? b.steps[0]?.time : "time" in b ? b.time : undefined)).find(Boolean) ?? running[0]?.time;
  const activity = (under: boolean) => (past || mergedWork ? null : <Activity calls={toolDrafts} running={running} cwd={cwd} quiet={!textDrafts.some((d) => !d.whole)} grew={grew} under={under} stop={stop} />);
  const headOf = () => (
    <>
      <span className="shrink-0 text-xs font-medium text-ink-2">{a.nick}</span>
      <span className="min-w-0 truncate text-xs text-ink-4">{a.name}</span>
      {bars}
      <When time={time} />
    </>
  );
  const label = `${a.nick} · ${t("chat.working")}`;
  // Its steps a line each lot, where they were done said at the line's head (away.tsx); its
  // words between, as they will be once said to the channel; active calls live inside the
  // trailing tool fold instead of a separate row below unless text is streaming after them.
  const hasContent = rows.length > 0 || textDrafts.length > 0 || synthLive;
  return (
    <div role="group" aria-label={label} className="flex min-w-0 flex-col gap-5">
      {hasContent || alone ? (
        <>
          {hasContent && (
            <TurnFrame bare={alone} face={<AgentFace channel={channel} a={a} call={call} record={record} may={may} />} head={headOf()}>
              <StepsHead.Provider value={away ? <Where place={away} /> : null}>
                {rows.map(node)}
                {synthLive && <Fold key={foldKey([], running, toolDrafts) ?? `${a.session}:live`} steps={[]} session={a.session} cwd={cwd} live={liveFold} />}
              </StepsHead.Provider>
              {textDrafts.map((d) => (
                <div key={`${d.proc}.${d.n}`} className="g-fade min-w-0 pr-6">
                  <Draft text={d.text} live={!d.whole} />
                </div>
              ))}
            </TurnFrame>
          )}
          {activity(false)}
        </>
      ) : (
        <TurnFrame face={<AgentFace channel={channel} a={a} call={call} record={record} may={may} />} head={headOf()}>
          {activity(true)}
        </TurnFrame>
      )}
    </div>
  );
}
