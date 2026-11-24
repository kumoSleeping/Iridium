// A channel as a chat (docs/channels.md): every chat in Iridium — 0, 1, or several agents — is
// drawn by this one page. Its members are a key at the bar's right (top.tsx). The conversation is
// the channel's log: your words a bubble on the right, anyone else's on the left under their face;
// an agent's as Markdown under its face, nick and what it runs as, its usage (context ring, quota
// bars) after that, over them its steps from its own record; Iridium's lines small and centred.
// When the channel has a single agent, /commands, /model, /mode, /clear, Shift+Tab and Esc work
// directly on that agent.
import { flushSync } from "react-dom";
import { useEffect, useLayoutEffect, useMemo, useRef, useState, type DragEvent, type ReactNode } from "react";
import { Popover } from "@base-ui/react/popover";
import { useQuery } from "@tanstack/react-query";
import { api } from "../../api/client";
import type { Block, ChannelReads, Reader, SessionState as LiveSession, Session } from "../../api/types.gen";
import { firstScreen, fresh, freshState, isWhole, peekChat, stateAhead, type Sent } from "../../chats";
import { LOCAL_ME, useApps, useMe, useWhoList, useWorkspaces } from "../../data";
import { t, useLang } from "../../i18n";
import { pushIn, pushing } from "../../motion";
import { openChat, openUsage } from "../../route";
import { PULL } from "../../sheet";
import { getCarried, onCarried, setCarried, useCarried, useConfirms, useList, useSession } from "../../store";
import { also, carries, events, opened, type SatoriEvent } from "../../stream";
import { errText } from "../../toast";
import { Arrive, agentName, cn, lampOf, PopSurface, ServerFace } from "../../ui";
import { Leaving } from "../../moves";
import { working as subWorking } from "../../tree";
import { useSecret } from "../../secrets";
import { Asks, Denials } from "../asks";
import { placeDropped } from "../at";
import { Composer, type ComposerHandle } from "../composer";
import { Copyable } from "../copyable";
import { Crash } from "../crash";
import { ContextRing, ModeKey, ModelKey, modelView, QuotaBars, useMode } from "../meters";
import { fromSatori, PLACE_DRAG, SESSION_DRAG, sourceOf, withDevice, yours, type Source } from "../refs";
import { Assistant, Attached, Compaction, Letter, Line, MsgRow, Pending, Said, StepsHead, When, YouBubble } from "../said";
import { ShellRuns } from "../shell";
import { folded, foldKey } from "../fold";
import { Stick, toEnd } from "../stick";
import { askQueue, groupMessages, quoteAt, readMarks, stillWaiting, watchExtra, type AgentRow, type Did, type Group, type Msg, type Src, lineOf, workOf, saidBefore, workKey, eventParts, personName, type EventWords } from "./lib";
import { channelLog } from "./log";
import { lendActs } from "./menu";
import { onOf } from "./scripts";
import { useRecvLabel } from "./recv";
import { betweenAgents, linesOf, longWords, oldRead } from "./folds";
import { Foldable } from "../longwords";
import { foldsOnce, useReadTo } from "../foldstate";
import { around, goTo, seqEl } from "./jump";
import { Forwards, quoteText } from "../forwardcard";
import { msgWords, withForwards } from "../forward";
import { mayAct, useMembers } from "./gate";
import { NotJoined } from "./key";
import { Face as ChannelFace, MemberFace } from "./face";
import { AgentFace } from "./members";
import { OtherTurn } from "../atwork";
import { useAway, Where } from "../away";
import { PersonCard, PersonMenu, useNamed } from "./person";
import { NickLink, Nicks, NickWords } from "./nick";
import { useRecalled } from "../reactions";

// At work: in a turn. Not "starting" (its harness coming up, no turn yet: an invited agent's face
// flashed up as if to speak and went; kumo, 2026-11-16).
const WORKING = new Set(["running", "compacting"]);
const BUSY = new Set(["running", "compacting", "starting", "waiting"]);
const turning = (s?: { status: string; asks: unknown[]; denials?: unknown[] }) => !!s && BUSY.has(s.status) && !(s.status === "waiting" && !s.asks.length && !!s.denials?.length);

let lastStackH = 120;

const ghostOf = (row: Session): LiveSession => ({ ...row, asks: [], tasks: [], denials: [], queue: [], end: 0, proc: -1 });

/** The states of these sessions, as carried; drawn again as any of them changes. */
export function useStates(ids: string[]): (LiveSession | undefined)[] {
  const [, redraw] = useState(0);
  const key = ids.join(" ");
  useEffect(() => onCarried((k) => k.startsWith("session:") && ids.includes(k.slice(8)) && redraw((n) => n + 1)), [key]);
  return ids.map((id) => getCarried<LiveSession>(`session:${id}`));
}

/** bar: the chat's top bar (chat.tsx Bar). record: opens a session's read-only TranscriptView.
 *  subTurn: draws a subagent at work under the conversation. */
export function ChannelPage({ id, sheet = 0, bar, record, subTurn }: { id: string; sheet?: number; bar: (s: Session | undefined) => ReactNode; record: (session: string) => void; subTurn?: (s: Session) => ReactNode }) {
  useLang();
  const [whole, setWhole] = useState(() => isWhole(id));
  useState(() => void (isWhole(id) || firstScreen(id)));
  useEffect(() => {
    if (whole) return;
    let live = true;
    void (isWhole(id) ? Promise.resolve() : firstScreen(id)).then(() => live && setWhole(true));
    return () => void (live = false);
  }, [id]);
  const hold = !whole;
  const list = useList();
  const row = list?.find((x) => x.id === id);
  const real = useSession(id);
  const ghost = useMemo(() => row && ghostOf(row), [row]);
  const s = real ?? ghost;
  const [lost, setLost] = useState(false);
  useEffect(() => {
    if (!real) void stateAhead(id).then((there) => !there && setLost(true));
  }, [id]);
  const had = useRef(false);
  if (real) had.current = true;
  else if (had.current && !lost) setLost(true);
  if (real && lost) setLost(false);
  const [entry] = useState(() => channelLog(id));
  const log = entry.log;
  const [, redraw] = useState(0);
  const changed = () => redraw((n) => n + 1);
  const me = useMe().data;
  const self = me ?? LOCAL_ME;
  const terminals = useWhoList();
  const workspaces = useWorkspaces().data;
  const confirms = useConfirms();

  // Its agents: from members once loaded, else from the list row (including a legacy 1-agent
  // session whose row.agents is empty).
  const members = useMembers(id);
  const seedAgents = useMemo(() => {
    const fromMembers = (members?.members ?? []).flatMap((m) => (m.session ? [{ session: m.session, agent: m.agent, nick: m.nick }] : []));
    if (fromMembers.length) return fromMembers;
    if (row?.agents?.length) return row.agents;
    if (row && !row.bare && row.agent) return [{ session: row.id, agent: row.agent, nick: "" }];
    return [];
  }, [members, row]);
  const sessions = seedAgents.map((a) => a.session);
  const states = useStates(sessions);
  const sig = states.map((st) => `${st?.model ?? ""}:${st?.effort ?? ""}:${st?.effortNow ?? ""}`).join(" ");
  useMembers(id, sig);
  // Not of its people: read, not spoken in (docs/channels.md).
  const may = mayAct(members);
  const agents: AgentRow[] = seedAgents.map((a, i) => {
    const m = members?.members.find((x) => x.session === a.session);
    const of = states[i] ?? (a.session === id ? s : undefined) ?? list?.find((x) => x.id === a.session);
    const agent = of?.agent ?? m?.agent ?? a.agent ?? "claude";
    return { session: a.session, member: `agent:${a.session}`, agent, nick: m?.nick ?? a.nick ?? "", name: m?.name ?? agentName(agent), status: of ? lampOf(of) : (m?.status ?? "idle"), on: onOf(m?.on) };
  });
  // When the channel has exactly one agent, keyboard shortcuts and /commands target it directly.
  const solo = agents.length === 1 ? agents[0] : undefined;
  const soloState = solo ? (states[0] ?? (solo.session === id ? s : undefined)) : undefined;

  // Its legacy session cleared itself (Claude's own /clear): go on in the replacement session.
  const seenNext = useRef<{ id: string; next?: string } | null>(null);
  useEffect(() => {
    if (!s || s.bare) return;
    const was = seenNext.current;
    seenNext.current = { id, next: s.next };
    if (was?.id === id && s.next && s.next !== was.next) openChat(s.next);
  }, [id, !!s, s?.bare, s?.next]);

  // Subagents at work for any agent in this channel.
  const sessionSet = useMemo(() => new Set(sessions), [sessions.join(" ")]);
  const subs = (list ?? []).filter((x) => x.subagent && !!x.parent && sessionSet.has(x.parent) && subWorking(x, list?.find((y) => y.id === x.parent), Date.now()));
  const subKey = subs.map((x) => x.id).join(" ");
  useEffect(() => {
    also(subKey ? subKey.split(" ").map((x) => `session:${x}`) : [], "subs");
    return () => also([], "subs");
  }, [subKey]);

  // The stream carries the agents' states too, as far as it has room; the rest are asked for as
  // the list says they changed.
  const key = sessions.join(" ");
  useEffect(() => {
    also(watchExtra(id, sessions));
    for (const x of sessions) if (!getCarried(`session:${x}`)) void stateAhead(x);
    return () => also([]);
  }, [id, key]);
  const moved = sessions.map((x) => {
    const r = list?.find((y) => y.id === x);
    return `${r?.status}:${r?.updatedAt}`;
  });
  const opening = useRef(true);
  useEffect(() => {
    const now = opening.current;
    opening.current = false;
    sessions.forEach((x) => {
      if (x === id || carries(`session:${x}`) || (now && freshState(x))) return;
      void api.session(x, { priority: "low" } as RequestInit).then(
        (st) => setCarried(`session:${x}`, st),
        () => {},
      );
    });
  }, [moved.join(" ")]);

  // The newest page, or what was kept; then each message as its event brings it. What the events
  // may have missed (the stream down a while) is read once it is back, and as an agent's turn moves.
  useEffect(() => {
    let live = true;
    const more = () => void log.newer().then(() => live && changed(), () => {});
    if (!fresh(log.came)) more();
    const hear = (e: SatoriEvent) => {
      if ((e.type !== "message-created" && e.type !== "message-deleted") || e.channel?.id !== id || !e.message) return;
      log.heard({ ...e.message, _data: { ...e._data, ...e.message._data } }, e.type === "message-deleted");
      changed();
    };
    events.add(hear);
    opened.add(more);
    return () => {
      live = false;
      events.delete(hear);
      opened.delete(more);
    };
  }, [id]);
  const turns = states.map((st) => `${st?.status}:${st?.end}`).join(" ");
  const turnsOpen = useRef(true);
  useEffect(() => {
    const now = turnsOpen.current;
    turnsOpen.current = false;
    if (log.loaded && !(now && fresh(log.came))) void log.newer().then(changed, () => {});
  }, [turns]);

  // What was sent from here, till the log has it.
  const sent = (entry.sent = entry.sent.filter((x) => stillWaiting(x, log.msgs)));
  const bubbles = useRef(new Map<string, HTMLDivElement>());
  const fly = useRef<string | null>(null);
  const scroller = useRef<HTMLDivElement>(null);
  // When each one sent began to come in: its message, come from the log in the middle of that,
  // goes on from where its bubble was (motion.ts pushIn), not drawn whole at once.
  const flew = useRef(new Map<Sent, number>());
  useLayoutEffect(() => {
    const el = fly.current && bubbles.current.get(fly.current);
    if (el) {
      const x = entry.sent.find((y) => y.key === fly.current);
      fly.current = null;
      toEnd(scroller.current);
      const t = pushIn(el);
      if (x) flew.current.set(x, t);
    }
    for (const [x, t] of flew.current) {
      if (!pushing(t)) {
        flew.current.delete(x);
        continue;
      }
      const row = x.seq ? scroller.current?.querySelector<HTMLElement>(`[data-seq="${x.seq}"]`) : null;
      if (!row) continue;
      flew.current.delete(x);
      pushIn(row, t);
    }
  });
  const onSent = (x: Sent) => {
    x.from = log.end;
    x.mid = agents.some((a) => WORKING.has(a.status));
    entry.sent.push(x);
    fly.current = x.key;
    changed();
  };
  // Failed, shown so even if it was taken for come.
  const sentDone = (x: Sent) => {
    x.at = Date.now();
    if (x.failed !== undefined && !entry.sent.includes(x)) entry.sent.push(x);
    changed();
  };
  const retry = async (x: Sent) => {
    x.failed = undefined;
    x.at = undefined;
    changed();
    try {
      const r = await api.send(id, x.text, x.files.map((f) => f.ref), x.input, x.quote);
      x.seq = r.seq;
    } catch (e) {
      x.failed = errText(e);
    }
    x.at = Date.now();
    changed();
  };

  // The bottom stack's height pads the conversation's end.
  const stack = useRef<HTMLDivElement>(null);
  const [stackH, setStackH] = useState(lastStackH);
  useLayoutEffect(() => {
    const el = stack.current;
    if (!el) return;
    const measure = () => setStackH((lastStackH = el.offsetHeight));
    measure();
    // Drawn at once, in the frame the stack changed in: drawn whenever React next got to it, the
    // conversation's padding was a frame behind the stack.
    const ro = new ResizeObserver(() => flushSync(measure));
    ro.observe(el);
    return () => ro.disconnect();
  }, [!!s]);

  const composer = useRef<ComposerHandle>(null);
  const call = (nick: string) => composer.current?.insert(`@${nick}`);
  // Lend @ and record actions to the top bar's Members menu (top.tsx).
  useEffect(() => lendActs(id, { call: may ? call : undefined, record }), [id, may, record]);
  // Its members as words name them and as @ lists them (agents, people and apps: peers).
  const { named: nicks, others: peopleRows } = useNamed({ channel: id, agents, members, may, call, record });

  const [modelOpen, setModelOpen] = useState(false);
  const [modeOpen, setModeOpen] = useState(false);

  const files = (e: DragEvent) => e.dataTransfer.types.includes("Files") && (e.preventDefault(), true);
  const session = (e: DragEvent) => e.dataTransfer.types.includes(SESSION_DRAG) && (e.preventDefault(), (e.dataTransfer.dropEffect = "copy"), true);
  const place = (e: DragEvent) => e.dataTransfer.types.includes(PLACE_DRAG) && (e.preventDefault(), (e.dataTransfer.dropEffect = "copy"), true);
  const drop = (e: DragEvent) => {
    if (files(e)) return composer.current?.attach([...e.dataTransfer.files]);
    if (place(e)) {
      const p = placeDropped(e.dataTransfer.getData(PLACE_DRAG));
      return p && composer.current?.name(p);
    }
    if (!session(e)) return;
    const to = list?.find((x) => x.id === e.dataTransfer.getData(SESSION_DRAG));
    if (to && to.id !== id) composer.current?.name(to.id);
  };

  // Who said a person's words, and whether it is the one looking: only those are on the right
  // (the user, 2026-11-13); another login, an app, a Bot or another session's ir on the left, under its own face.
  const who = personWho(self, terminals, list ?? [], workspaces ?? [], s);
  const groups = useMemo(() => groupMessages(log.msgs), [log.msgs]);
  // Words between agents fold when long (folds.ts betweenAgents).
  const nickSet = new Set(agents.map((a) => a.nick.toLowerCase()).filter(Boolean));
  const fold = (m: Msg) => betweenAgents(m, log.msgs, nickSet);
  // Each agent's usage after its model's name, a strip of its own as small as a phone's.
  const stateAt = (session: string) => {
    const i = sessions.indexOf(session);
    return (i >= 0 ? states[i] : undefined) ?? (session === id ? s : undefined);
  };
  const bars = (session: string) => {
    const a = agents.find((x) => x.session === session);
    return a ? <AgentBars a={a} st={stateAt(session)} /> : null;
  };
  // The message being answered (<quote>), over the composer till it is sent; its author named
  // at the composer's start, as QQ does (kumo, 2026-11-21): the @, not the quote, calls.
  const [quote, setQuote] = useState<Msg | null>(null);
  const nameOf = (m: Msg) => (m.author.kind === "agent" ? m.author.nick || m.author.name : m.author.kind === "person" ? who(m.author).who.name : m.author.kind === "script" ? m.author.name : "Iridium");
  const quoting = (m: Msg) => {
    setQuote(m);
    const p = m.author.kind === "person" ? who(m.author) : undefined;
    const at = quoteAt(m, !!p && (p.mine || (p.who.kind === "letter" && !p.who.channel)));
    if (at) composer.current?.atStart(at);
    else composer.current?.focus();
  };
  const quoted = (n: number) => {
    const m = log.msgs.find((x) => x.n === n);
    return m ? { who: nameOf(m), text: quoteText(m) } : undefined;
  };
  // A quote pressed: the message it answers, brought if the page has not got it (channel/jump.ts).
  const jump = (n: number) => void goTo(scroller.current, () => seqEl(scroller.current, n), () => around(log, n), changed);

  // How far each member has read (docs/channels.md "已读"): asked once, then as the stream
  // brings it. This page has read what it shows: said as the log's end moves while it is seen,
  // and as it is seen again.
  const reads = useCarried<ChannelReads>(`reads:${id}`);
  useEffect(() => {
    if (getCarried(`reads:${id}`) && fresh(log.came)) return;
    void api.reads(id).then(
      (r) => getCarried(`reads:${id}`) || setCarried(`reads:${id}`, r),
      () => {},
    );
  }, [id]);
  const end = log.loaded ? log.end : 0;
  // Seen: the tab in front. Back in front, what came meanwhile is read then.
  const [seen, setSeen] = useState(() => document.visibilityState === "visible");
  useEffect(() => {
    const look = () => setSeen(document.visibilityState === "visible");
    document.addEventListener("visibilitychange", look);
    return () => document.removeEventListener("visibilitychange", look);
  }, []);
  useEffect(() => {
    if (!end || !may || !seen) return;
    const timer = setTimeout(() => void api.read(id, end).then((r) => setCarried(`reads:${id}`, r), () => {}), 600);
    return () => clearTimeout(timer);
  }, [id, end, may, seen]);
  const marks = useMemo(() => readMarks(groups, reads?.readers ?? [], members?.me), [groups, reads, members?.me]);
  // Long words read before the latest rounds fold of themselves (channel/folds.ts), as each is
  // first drawn once the place read to is known.
  const readTo = useReadTo(id, reads?.readers, members?.me, log.loaded ? log.end : undefined);
  const olds = useMemo(() => (readTo === undefined ? undefined : oldRead(log.msgs, readTo)), [log.msgs, readTo]);
  const old = (m: Msg) => foldsOnce(id, `m${m.n}`, olds?.has(m.n));
  const working = agents.filter((a) => WORKING.has(a.status));
  // Hold back newly started agent turns while the triggering send HTTP is still in flight
  // (kumo, 2026-11-24: SSE running can arrive before api.send responds, flashing a reply under a
  // still-sending bubble).
  const sendingBefore = sent.some((x) => !x.mid && x.at === undefined && x.left === undefined && x.failed === undefined);
  const shownWorking = sendingBefore ? [] : working;
  const secretAt = useSecret()?.at;
  const secretHere = secretAt === id || (!!solo && secretAt === solo.session);
  const asking = agents.some((a) => !!stateAt(a.session)?.asks.length) || agents.some((a) => confirms.some((c) => c.session === a.session && c.kind !== "ask")) || secretHere;
  const compacting = soloState?.status === "compacting";
  const compactSince = useRef(0);
  if (!compacting) compactSince.current = 0;
  else if (soloState?.compactingSince) compactSince.current = soloState.compactingSince;
  else if (!compactSince.current) compactSince.current = Date.now();
  const grew = `${log.end} ${sent.length} ${working.map((a) => `${a.session}:${stateAt(a.session)?.end ?? 0}`).join(" ")} ${subs.map((x) => `${x.id}:${x.updatedAt}`).join(",")}`;
  const padTop = 64; // the bar's 48px, a gap

  if (!s) {
    const gone = lost && list !== undefined && !row;
    return (
      <div className="relative h-full">
        {bar(row)}
        {gone && <p className="g-fade m-0 pt-24 text-center text-sm text-ink-3">{t("chat.gone")}</p>}
      </div>
    );
  }
  return (
    <Nicks.Provider value={nicks}>
      <div className="relative h-full" onDragOver={(e) => files(e) || session(e) || place(e)} onDrop={drop}>
        <Stick key={hold ? "hold" : "whole"} scroller={scroller} tail={grew} older={() => log.older()} busy={working.length > 0 || subs.length > 0} draw={changed} place={entry.place} bottom={stackH} quiet={asking} style={{ paddingTop: padTop, paddingBottom: stackH + 20 }}>
          <div data-column className="mx-auto flex max-w-(--chat-w) flex-col gap-5 px-4 [--depart-gap:1.25rem] sm:px-6">
            {!hold && groups.map((g) => (
              <div key={g.key} className="relative flex min-w-0 flex-col">
                <GroupView g={g} channel={id} agents={agents} who={who} record={record} call={call} may={may} quote={may ? quoting : undefined} quoted={quoted} bars={bars} fold={fold} old={old} jump={jump} />
                {/* In the gap under it, over nothing: coming late, it moves nothing (docs/前端设计规范.md). */}
                {SHOW_READS && marks.has(g.key) && <ReadMarks readers={marks.get(g.key)!} agents={agents} />}
              </div>
            ))}
            {!hold && sent.map((x) => (
              <Pending
                key={x.key}
                s={x}
                who={yours(self)}
                taken={!x.mid && shownWorking.length > 0}
                words={<NickWords text={x.text} />}
                bubbleRef={(el) => void (el ? bubbles.current.set(x.key, el) : bubbles.current.delete(x.key))}
                retry={() => retry(x)}
                drop={() => ((entry.sent = entry.sent.filter((y) => y !== x)), changed())}
              />
            ))}
            {!hold && log.loaded && compacting && (
              <div className="-mt-1">
                <Compaction since={compactSince.current} />
              </div>
            )}
            {/* Each called agent at work: its face and name at once, as its words will be, the dots
                under them, and live streaming drafts + tool calls; collapsed cleanly when idle so
                there is no dead empty gap under the last message. */}
            {!hold && log.loaded && !compacting && (
              <div className="-mt-2 flex flex-col gap-5 empty:hidden">
                <Leaving
                  items={shownWorking}
                  keyOf={(a) => a.session}
                  draw={(a, leaving) => {
                    const last = groups.at(-1)?.author;
                    const alone = !sent.length && last?.kind === "agent" && last.session === a.session;
                    return (
                      <Arrive on={!alone} off={leaving} as="g-fade">
                        <OtherTurn spoken={new Set(log.msgs.slice(-12).flatMap((m) => (m.author.kind === "agent" && m.author.session === a.session && m.text.trim() ? [m.text.trim()] : [])))} a={a} channel={id} st={stateAt(a.session)} said={log.msgs.findLast((m) => m.author.kind === "agent" && m.author.session === a.session && !!m.src)?.src} alone={alone} bars={bars(a.session)} call={may ? call : undefined} record={record} may={may} esc={agents.length === 1} />
                      </Arrive>
                    );
                  }}
                />
              </div>
            )}
            {!hold && subTurn && subs.map((x) => (
              <div key={x.id} className="g-arrive">
                <div>{subTurn(x)}</div>
              </div>
            ))}
            {!hold && soloState?.denials?.length ? <Denials session={soloState.id} denials={soloState.denials} cwd={soloState.cwd} stuck={soloState.status === "waiting" && !soloState.asks.length} /> : null}
            {!hold && soloState?.status === "error" && soloState.error && <Crash error={soloState.error} />}
          </div>
        </Stick>
        {bar(s)}
        <div ref={stack} {...{ [PULL]: "" }} data-asking={asking || undefined} data-covered={sheet > 0 || undefined} data-sunk={(sheet > 0 && stackH > sheet - 16) || undefined} inert={sheet > 0} className="g-turn-stay g-sink vt-stack absolute inset-x-0 bottom-0 z-10 px-2.5 pb-[max(14px,calc(env(safe-area-inset-bottom)+6px))] sm:px-5">
          <div aria-hidden className="pointer-events-none absolute inset-x-0 bottom-0 h-[calc(44px+max(14px,calc(env(safe-area-inset-bottom)+6px)))] bg-[linear-gradient(to_bottom,transparent,var(--paper)_14px)]" />
          <div className="relative mx-auto max-w-(--chat-w)">
            <ShellRuns s={s} />
            {agents.map((a) => {
              const needs = confirms.filter((c) => c.session === a.session && c.kind !== "ask");
              return needs.length ? (
                <Of key={a.session} a={a} off={!may}>
                  <Asks session={a.session} asks={[]} cwd={stateAt(a.session)?.cwd ?? s.cwd} confirms={needs} />
                </Of>
              ) : null;
            })}
            <AskQueue agents={agents} states={agents.map((a) => stateAt(a.session))} cwd={s.cwd} off={!may} />
            {!may ? (
              <NotJoined />
            ) : (
              <ChannelComposer
                composer={composer}
                s={s}
                solo={soloState}
                agents={agents}
                people={peopleRows}
                quote={quote ? { n: quote.n, who: nameOf(quote), text: quoteText(quote) } : undefined}
                unquote={() => setQuote(null)}
                onSent={onSent}
                onSentDone={sentDone}
                modelOpen={modelOpen}
                setModelOpen={setModelOpen}
                modeOpen={modeOpen}
                setModeOpen={setModeOpen}
              />
            )}
          </div>
        </div>
      </div>
    </Nicks.Provider>
  );
}

/** The channel's composer, wired to the sole agent's /mode, /model, /usage, Shift+Tab and Esc when
 *  the channel has exactly one agent. */
function ChannelComposer({ composer, s, solo, agents, people, quote, unquote, onSent, onSentDone, modelOpen, setModelOpen, modeOpen, setModeOpen }: { composer: React.RefObject<ComposerHandle | null>; s: LiveSession; solo?: LiveSession; agents: AgentRow[]; people: AgentRow[]; quote?: { n: number; who: string; text: string }; unquote: () => void; onSent: (x: Sent) => void; onSentDone: (x: Sent) => void; modelOpen: boolean; setModelOpen: (o: boolean) => void; modeOpen: boolean; setModeOpen: (o: boolean) => void }) {
  const mode = useMode(solo ?? s);
  return (
    <Composer
      ref={composer}
      s={s}
      solo={solo}
      turn={!!solo && turning(solo) && solo.status !== "starting"}
      nicks={agents}
      people={people}
      quote={quote}
      unquote={unquote}
      onSent={onSent}
      onSentDone={onSentDone}
      cycleMode={solo ? mode.cycle : () => {}}
      openModel={() => solo && setModelOpen(true)}
      openMode={() => solo && setModeOpen(true)}
      openUsage={() => openUsage(undefined, (solo ?? s).machine)}
      openContext={() => false}
      cleared={openChat}
      tools={
        solo ? (
          <span aria-hidden className="invisible flex w-0 overflow-hidden">
            <ModeKey mode={mode} open={modeOpen} onOpenChange={setModeOpen} />
            <ModelKey s={solo} open={modelOpen} onOpenChange={setModelOpen} />
          </span>
        ) : undefined
      }
    />
  );
}

/** Who said a person's words, and whether it is the one looking: only those are on the right
 *  (the user, 2026-11-13); another login, an app, a Bot or another session's ir on the left, under its own face. */
export type Who = (a: Extract<Msg["author"], { kind: "person" }>) => { who: Extract<Source, { kind: "you" | "letter" }>; mine: boolean };
export function personWho(self: Parameters<typeof sourceOf>[2], terminals: Parameters<typeof sourceOf>[3], list: Session[], workspaces: { id: number; name: string }[], of?: Parameters<typeof sourceOf>[6]): Who {
  return (m) => {
    const src = withDevice(sourceOf(m.name, m.id, self, terminals, list, workspaces, of), m.device);
    // A letter (from another session's ir, or a plugin/peer) is never yours.
    if (src.kind === "letter") return { who: src, mine: false };
    // The machine's own words (typed at its terminal, sent with its ir) are the host's.
    const mine = m.id === self.id || (!!self.host && (m.id === "local" || (src.kind === "you" && !!src.local)));
    if (src.kind === "you") return { who: src, mine };
    const known = terminals.find((x) => x.id === m.id);
    return { who: yours(known ? { ...known, name: m.name || known.name, ...(m.device ? { device: m.device } : {}) } : { id: m.id, name: m.name, device: m.device ?? "none" }), mine };
  };
}

/** An agent's usage after its model's name, a strip of its own as small as a phone's (the user,
 *  2026-11-13): its context's ring, its quota's bars; each pressed, its popover. */
export function AgentBars({ a, st }: { a: AgentRow; st?: LiveSession }) {
  return (
    <span className="flex shrink-0 items-center">
      <ContextRing session={a.session} summary={st?.context} tokens={st?.tokens} compact />
      <QuotaBars agent={a.agent} machine={st?.machine} live={st?.live ?? false} using={st ? [st.model, st.pick, modelView(st).name] : []} compact />
    </span>
  );
}

/** Who has read up to here: small faces at the right, under what they last read. */
/** Whether the read marks are drawn under the messages (kumo, 2026-11-16: not shown). How far
 *  each member has read is still kept: what names a person is cleared by it. */
export const SHOW_READS: boolean = false;

export function ReadMarks({ readers, agents }: { readers: Reader[]; agents: AgentRow[] }) {
  // The host's login is Iridium on the page, never 主机 (docs/前端设计规范.md "称呼").
  const nameOf = (r: Reader) => r.nick || (r.member === "local" || r.member.startsWith("local/") ? "Iridium" : r.name) || r.member;
  const names = readers.map(nameOf).join(t("channel.event.sep"));
  // Each its own face (an agent's, a login's avatar), as beside what it says.
  const face = (r: Reader, size: number) => <MemberFace member={r.member} name={nameOf(r)} agent={r.agent ?? agents.find((x) => x.member === r.member)?.agent} size={size} />;
  return (
    <Popover.Root>
      <Popover.Trigger
        render={
          <button type="button" aria-label={t("channel.readBy", { names })} className="g-fade absolute top-full right-0 flex h-5 items-center gap-1 rounded-sm text-xs text-ink-4 hover:text-ink-3">
            <span>{t("channel.read")}</span>
            {readers.slice(0, 8).map((r) => (
              <span key={r.member} className="grid">
                {face(r, 18)}
              </span>
            ))}
          </button>
        }
      />
      <PopSurface align="end" className="no-bar max-h-[min(360px,var(--available-height))] min-w-44 overflow-y-auto p-1.5">
        <div className="truncate px-2.5 pt-1 pb-0.5 text-xs text-ink-3">{t("channel.read")}</div>
        {readers.map((r) => (
          <div key={r.member} className="flex h-8 items-center gap-2.5 px-2.5 text-base pointer-coarse:h-11">
            {face(r, 20)}
            <span className="min-w-0 flex-1 truncate">{nameOf(r)}</span>
          </div>
        ))}
      </PopSurface>
    </Popover.Root>
  );
}

/** Something of one agent's above the composer (its queue, its asks), under its logo and nick,
 *  pressed its session. off: shown, not to be answered (the one looking is not of the channel's
 *  people). */
export function Of({ a, children, off }: { a: AgentRow; children: ReactNode; off?: boolean }) {
  return (
    <div inert={off} className={cn(off && "opacity-60")}>
      <button type="button" onClick={() => openChat(a.session)} className="g-press relative z-10 flex h-6 max-w-full items-center gap-1.5 rounded-md px-2 text-xs text-ink-2 hover:text-ink">
        <ChannelFace id={a.session} agent={a.agent} size={14} logo={12} />
        <span className="max-w-40 truncate">{a.nick}</span>
      </button>
      {children}
    </div>
  );
}

/** The channel's agents' asks as one queue (docs/channels.md "提问"): the oldest open, the rest one
 *  line, 还有 N 个, pressed open too. Each under its agent. */
const SEEN = new Map<string, Map<string, number>>();
export function AskQueue({ agents, states, cwd, off }: { agents: AgentRow[]; states: (LiveSession | undefined)[]; cwd: string; off: boolean }) {
  const [all, setAll] = useState(false);
  const key = agents.map((a) => a.session).join(",");
  let seen = SEEN.get(key);
  if (!seen) SEEN.set(key, (seen = new Map()));
  const queue = askQueue(agents.map((a, i) => ({ session: a.session, asks: states[i]?.asks ?? [] })), seen);
  if (!queue.length) return null;
  const shown = all ? queue : queue.slice(0, 1);
  return (
    <>
      {shown.map((q) => {
        const a = agents.find((x) => x.session === q.session)!;
        const i = agents.indexOf(a);
        return (
          <Of key={`${q.session}:${q.ask.requestId}`} a={a} off={off}>
            <Asks session={q.session} asks={[q.ask]} cwd={states[i]?.cwd ?? cwd} />
          </Of>
        );
      })}
      {queue.length > 1 && !all && (
        <button type="button" onClick={() => setAll(true)} className="g-press relative z-10 mb-1 flex h-7 items-center rounded-md px-2 text-xs text-ink-3 hover:text-ink-2 pointer-coarse:h-9">
          {t("ask.more", { n: queue.length - 1 })}
        </button>
      )}
    </>
  );
}

/** A message as the block an agent's words are drawn from (said.tsx Assistant, which makes the
 *  members they name links): kept per message, so one drawn is not drawn again. */
const blocks = new WeakMap<Msg, { sig: string; b: Block }>();
function blockFor(m: Msg, agents: AgentRow[]): Block {
  const nicks = agents.map((a) => a.nick);
  const sig = nicks.join(" ");
  const had = blocks.get(m);
  if (had?.sig === sig) return had.b;
  // Satori's elements in the words (an <at>) as Markdown writes them.
  const words = fromSatori(msgWords(m).words, (session) => agents.find((a) => a.session === session)?.nick);
  const b: Block = { at: String(m.n), kind: "assistant", text: words, ...(m.files.length ? { files: m.files } : {}), ...(m.time ? { time: m.time } : {}) };
  blocks.set(m, { sig, b });
  return b;
}

/** One speaker's messages: a person's bubble; an agent's words under its head; Iridium's line.
 *  Each message by its number (data-seq, where a quote goes: jump), folding by hand or of itself
 *  (old; fold: words between agents). */
export function GroupView({ g, channel, agents, who, record, call, may, quote, quoted, bars, fold, old, jump }: { fold?: (m: Msg) => boolean; old?: (m: Msg) => boolean; jump?: (n: number) => void; g: Group; channel: string; agents: AgentRow[]; who: Who; record: (session: string) => void; call: (nick: string) => void; may: boolean; quote?: (m: Msg) => void; quoted: (n: number) => { who: string; text: string } | undefined; bars: (session: string) => ReactNode }) {
  const list = useList();
  const a = g.author;
  if (a.kind === "iridium")
    return (
      <div className="flex flex-col">
        {g.msgs.map((m) => (
          <EventLine key={m.n} m={m} channel={channel} />
        ))}
      </div>
    );
  if (a.kind === "person") {
    const m = g.msgs[0]!;
    const by = who(a);
    const w = msgWords(m);
    const body = [...m.files.map((f) => <Attached key={f.ref} session={channel} f={f} />), ...(w.forwards.length ? [<Forwards key="forwards" fs={w.forwards} />] : [])];
    const under = m.quote !== undefined ? <QuoteLine n={m.quote} q={quoted(m.quote)} channel={channel} jump={jump} /> : undefined;
    return (
      <div data-seq={m.n}>
        <Foldable scope={channel} id={`m${m.n}`} auto={old?.(m) ?? false} lines={linesOf(w.words)}>
          {(wrap) => (
            <Copyable copy={withForwards(w.words, w.forwards)} quote={quote && (() => quote(m))} act={{ channel, seq: m.n, may, side: by.mine ? "right" : "left" }}>
              {by.who.kind === "letter" ? (
                <Letter
                  src={by.who}
                  text={w.words}
                  time={m.time}
                  onJump={openChat}
                  words={wrap(<NickWords text={w.words} />)}
                  under={under}
                  menu={by.who.channel ? (at, close) => <PersonMenu user={a.id} name={a.name} call={may ? call : undefined} at={at} close={close} /> : undefined}
                  card={by.who.channel ? <PersonCard user={a.id} name={a.name} avatar={by.who.avatar} dm={false} call={may ? call : undefined} /> : undefined}
                >
                  {body}
                </Letter>
              ) : (
                <YouBubble
                  text={w.words}
                  who={by.who}
                  left={!by.mine}
                  time={m.time}
                  words={wrap(<NickWords text={w.words} />)}
                  under={under}
                  menu={by.mine ? undefined : (at, close) => <PersonMenu user={a.id} name={a.name} call={may ? call : undefined} at={at} close={close} />}
                  card={<PersonCard user={a.id} name={a.name} avatar={by.who.avatar} dm={!by.mine} call={may && !by.mine ? call : undefined} />}
                >
                  {body}
                </YouBubble>
              )}
            </Copyable>
          )}
        </Foldable>
      </div>
    );
  }
  if (a.kind === "script") return <ScriptGroup g={g} channel={channel} agents={agents} quote={quote} quoted={quoted} old={old} jump={jump} />;
  const now = agents.find((x) => x.session === a.session);
  // One no longer in the channel, or an outsider writing via ir: its harness as the list has it.
  const agent = now?.agent ?? list?.find((x) => x.id === a.session)?.agent;
  return (
    <MsgRow className="flex min-w-0 items-start gap-2.5">
      {now ? (
        <AgentFace channel={channel} a={now} call={may ? call : undefined} record={record} may={may} />
      ) : (
        <span className="grid size-7 shrink-0 place-items-center rounded-full bg-fill">
          {agent ? <ChannelFace id={a.session} agent={agent} size={28} logo={16} /> : <ChannelFace id={a.session} agent={"claude"} size={28} none={<span />} />}
        </span>
      )}
      <div className="flex min-w-0 flex-1 flex-col gap-1.5">
        <div className="flex h-4 min-w-0 items-center gap-1.5">
          <span className="shrink-0 text-xs font-medium text-ink-2">{now?.nick || a.nick}</span>
          <span className="min-w-0 truncate text-xs text-ink-4">{now?.name || a.name}</span>
          {now && bars(now.session)}
          <When time={g.msgs[0]!.time} />
        </div>
        {g.msgs.map((m) => {
          const w = msgWords(m);
          return (
            <div key={m.n} data-seq={m.n} className={cn(m.kind === "error" && "text-err")}>
              {m.src ? <Work src={m.src} did={m.did} said={m.text} channel={channel} /> : m.did && <DidLine did={m.did} />}
              <Foldable scope={channel} id={`m${m.n}`} auto={((fold?.(m) ?? false) && longWords(w.words)) || (old?.(m) ?? false)} lines={linesOf(w.words)}>
                {(wrap) => (
                  <Copyable copy={withForwards(w.words, w.forwards)} quote={quote && (() => quote(m))} act={m.kind === "error" || m.kind === "ask" ? undefined : { channel, seq: m.n, may }}>
                    {wrap(
                      <>
                        <Assistant b={blockFor(m, agents)} session={m.input ? channel : a.session} />
                        <Forwards fs={w.forwards} />
                      </>,
                    )}
                  </Copyable>
                )}
              </Foldable>
              {m.quote !== undefined && <QuoteLine n={m.quote} q={quoted(m.quote)} channel={channel} jump={jump} />}
            </div>
          );
        })}
      </div>
    </MsgRow>
  );
}

/** A command's messages (docs/channels.md "命令"): said as Iridium, its face and name over what
 *  it printed, as an agent's words are drawn (kumo, 2026-11-24: no face or name of its own). */
function ScriptGroup({ g, channel, agents, quote, quoted, old, jump }: { g: Group; channel: string; agents: AgentRow[]; quote?: (m: Msg) => void; quoted: (n: number) => { who: string; text: string } | undefined; old?: (m: Msg) => boolean; jump?: (n: number) => void }) {
  return (
    <MsgRow className="flex min-w-0 items-start gap-2.5">
      <ServerFace size={28} />
      <div className="flex min-w-0 flex-1 flex-col gap-1.5">
        <div className="flex h-4 min-w-0 items-center gap-1.5">
          <span className="min-w-0 truncate text-xs font-medium text-ink-2">Iridium</span>
          <When time={g.msgs[0]!.time} />
        </div>
        {g.msgs.map((m) => {
          const w = msgWords(m);
          return (
            <div key={m.n} data-seq={m.n}>
              <Foldable scope={channel} id={`m${m.n}`} auto={old?.(m) ?? false} lines={linesOf(w.words)}>
                {(wrap) => (
                  <Copyable copy={withForwards(w.words, w.forwards)} quote={quote && (() => quote(m))} act={{ channel, seq: m.n, may: !!quote }}>
                    {wrap(
                      <>
                        <Assistant b={blockFor(m, agents)} session={channel} />
                        <Forwards fs={w.forwards} />
                      </>,
                    )}
                  </Copyable>
                )}
              </Foldable>
              {m.quote !== undefined && <QuoteLine n={m.quote} q={quoted(m.quote)} channel={channel} jump={jump} />}
            </div>
          );
        })}
      </div>
    </MsgRow>
  );
}

/** The message one answers, under the words that answer it, as chat apps put it (kumo,
 *  2026-11-16: a line over the face read oddly): a small grey block, who said it and its words,
 * two lines at most; the number alone when the page has not got it; taken back, the number and
 *  so (docs/channels.md "撤回"). Pressed, the message itself (jump; kumo, 2026-11-21). */
function QuoteLine({ n, q, channel, jump }: { n: number; q?: { who: string; text: string }; channel: string; jump?: (n: number) => void }) {
  const gone = useRecalled(channel, n);
  const words = <span className="line-clamp-2 [overflow-wrap:anywhere]">{gone ? t("channel.quoteGone", { n }) : q ? t("channel.quoteOf", { who: q.who, text: q.text }) : `#${n}`}</span>;
  const cls = "mt-1 block max-w-[min(560px,100%)] rounded-md bg-fill px-2.5 py-1.5 text-left text-xs leading-[18px] text-ink-3";
  if (!jump || gone) return <div className={cls}>{words}</div>;
  return (
    <button type="button" onClick={() => jump(n)} className={cn(cls, "g-press cursor-pointer hover:text-ink-2")}>
      {words}
    </button>
  );
}

const EVENT_KEYS = ["join", "leave", "rename", "recv", "on", "wake", "everyone", "added", "removed", "undelivered", "stopped", "silent", "silentWhy", "by", "sep", "profile", "profileChannel", "profileNoBy", "profileChannelNoBy"] as const;
const SCRIPT_KEYS = { add: "scriptAdd", remove: "scriptRemove", enabled: "scriptEnabled", disabled: "scriptDisabled", changed: "scriptChanged", renamed: "scriptRenamed" } as const;
const WHAT_KEYS = ["name", "nick", "intro", "persona", "avatar", "background"] as const;

/** A line of Iridium's own: what happened to the channel (who joined, left, was renamed, what one
 *  receives, its people), in the page's words, the agent in it as its nick is drawn elsewhere;
 *  one with no event (an older line), its own text. */
function EventLine({ m, channel }: { m: Msg; channel: string }) {
  useLang();
  const d = useApps().data;
  const { whole, onWhole } = useRecvLabel(channel);
  const words = { ...Object.fromEntries(EVENT_KEYS.map((k) => [k, t(`channel.event.${k}`)])), script: Object.fromEntries(Object.entries(SCRIPT_KEYS).map(([k, v]) => [k, t(`channel.event.${v}`)])), what: Object.fromEntries(WHAT_KEYS.map((k) => [k, t(`channel.what.${k}`)])), whatSep: t("channel.what.sep"), reasons: { 余额不足: t("channel.reason.balance"), 认证失败: t("channel.reason.auth"), 请求太频繁: t("channel.reason.rate") } } as unknown as EventWords;
  // A message taken back: who did it (docs/channels.md "撤回").
  if (m.event?.type === "recall") return <Line>{t("channel.event.recall", { by: m.event.by === "主机" ? "Iridium" : (m.event.by ?? "") })}</Line>;
  const parts = m.event && eventParts(m.event, words);
  if (!parts) return <Line>{m.text}</Line>;
  const apps = (d?.apps ?? []).map((a) => (a.web ? { ...a, name: t("term.panel") } : a));
  return (
    <Line>
      {parts.map((p, i) => (typeof p === "string" ? p : "nick" in p ? <NickLink key={i} nick={p.nick} /> : "recv" in p ? whole(p.recv) : "on" in p ? onWhole(p.on) : p.people.map((x) => personName(x, apps, d?.logins ?? [])).join(t("channel.event.sep"))))}
    </Line>
  );
}

/** What the agent did before the words under it (the user, 2026-11-13: all of it, not a count):
 *  its thinking and tool calls from its own record, folded as its own chat folds them. The
 *  count's line until they come, or when the record cannot be read. did: none for a stretch
 *  with no tool call (thinking alone), which has no count to show meanwhile. */
function Work({ src, did, said, channel }: { src: Src; did?: Did; said?: string; channel: string }) {
  const row = useList()?.find((x) => x.id === src.session);
  // Done away from the channel's folder: in the box that says where (away.tsx), one line until
  // pressed; the words under it are the channel's and stay outside.
  const away = useAway(channel, src.session);
  const q = useQuery({
    queryKey: workKey(src),
    queryFn: async () => {
      const page = src.from !== undefined ? await api.messages(src.session, { after: Math.max(0, src.from - 1) }) : await api.messages(src.session, { before: lineOf(src.block!) + 1 });
      return workOf(page.blocks, src, said, saidBefore(channelLog(channel).log.msgs, src));
    },
    // What its record kept here already has (it was just drawn at work from it): no asking, no
    // line of counts in between while the answer comes over a slow link.
    initialData: () => {
      const log = peekChat(src.session)?.log;
      if (!log?.loaded || src.from === undefined || src.to === undefined || log.start > src.from || log.end < src.to) return undefined;
      // With the words of the message before, as the answer asked for is read (above): without
      // them the stretch began with those words again, a second time under themselves, and stayed
      // so (kumo, 2026-11-24: each thing an agent said on its way through a long turn drawn twice).
      const have = workOf<Block>(log.blocks, src, said, saidBefore(channelLog(channel).log.msgs, src));
      return have.length ? have : undefined;
    },
    staleTime: Infinity,
    // A weak link: tried again, not left at the counts for good.
    retry: 3,
    retryDelay: (n) => Math.min(8000, 600 * 2 ** n),
  });
  const work = q.data;
  if (!work?.length) return did ? <DidLine did={did} /> : null;
  // A stretch that is over: a call of it with no result never got one, and is a step, not a call
  // still running (kumo, 2026-11-24: 底下这行理论上不该存在的吧, a bash line for 149m41s under its
  // words).
  const shown = folded(work, false);
  const rows = shown.map((b) => <Said key={b.kind === "fold" ? (foldKey(b.steps) ?? b.at) : b.at} b={b} session={src.session} cwd={row?.cwd ?? ""} source={() => ({ kind: "you" }) as ReturnType<Parameters<typeof Said>[0]["source"]>} onJump={openChat} />);
  return (
    <div className="mb-1.5 flex min-w-0 flex-col gap-3">
      <StepsHead.Provider value={away ? <Where place={away} /> : null}>{rows}</StepsHead.Provider>
    </div>
  );
}

/** What the agent did before the words under it, in one quiet line. */
function DidLine({ did }: { did: Did }) {
  useLang();
  const parts: string[] = [];
  if (did.bash) parts.push(`bash ${did.bash}${did.cmds ? `（${did.cmds}）` : ""}${did.failed ? t("channel.did.failed", { n: did.failed }) : ""}`);
  if (did.files) parts.push(`${t("channel.did.files", { n: did.files })} +${did.add} −${did.del}`);
  if (did.read) parts.push(t("channel.did.read", { n: did.read }));
  if (did.web) parts.push(t("channel.did.web", { n: did.web }));
  if (did.agents) parts.push(t("channel.did.agents", { n: did.agents }));
  if (did.other) parts.push(t("channel.did.other", { n: did.other }));
  if (!parts.length) return null;
  return (
    <div className="mb-1 truncate text-xs text-ink-3 tabular-nums">
      {parts.join(" · ")}
    </div>
  );
}
