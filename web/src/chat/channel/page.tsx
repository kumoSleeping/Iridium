// A channel of no agent of its own (docs/channels.md), as a chat; a session's page, one agent or
// several, is chat.tsx's (kumo, 2026-11-15), drawn with the parts exported here. Its members are a key at
// the bar's right (top.tsx), not a row under the title. The conversation is the channel's log,
// only what was said: your words a bubble on the right, anyone else's on the left under their
// face; an agent's as Markdown under its face, nick and what it runs as, its usage (the
// context's ring, the quota's bars) after that, over them its thinking and tool calls from its
// own record; Iridium's lines small and centred. The newest at the bottom, paged back as you
// scroll up. Under it: each agent's queue and asks, under its nick; a composer whose @ names a
// member (agents, people, apps).
import { useEffect, useLayoutEffect, useMemo, useRef, useState, type DragEvent, type ReactNode } from "react";
import { Popover } from "@base-ui/react/popover";
import { useQuery } from "@tanstack/react-query";
import { api } from "../../api/client";
import type { Block, ChannelReads, Reader, SessionState as LiveSession, Session } from "../../api/types.gen";
import { stateAhead, type Sent } from "../../chats";
import { LOCAL_ME, useApps, useMe, useWhoList, useWorkspaces } from "../../data";
import { t, useLang } from "../../i18n";
import { pushIn } from "../../motion";
import { openChat } from "../../route";
import { PULL } from "../../sheet";
import { getCarried, onCarried, setCarried, useCarried, useConfirms, useList, useSession } from "../../store";
import { also, carries, events, opened, type SatoriEvent } from "../../stream";
import { errText } from "../../toast";
import { agentName, cn, lampOf, Leaving, Logo, PopSurface } from "../../ui";
import { Asks } from "../asks";
import { placeDropped } from "../at";
import { Composer, type ComposerHandle } from "../composer";
import { Copyable } from "../copyable";
import { StopKey, Waiting } from "../live";
import { ContextRing, modelView, QuotaBars } from "../meters";
import { Queue } from "../queue";
import { fromSatori, PLACE_DRAG, SESSION_DRAG, sourceOf, yours } from "../refs";
import { Assistant, Attached, Line, MsgRow, Pending, Said, When, YouBubble } from "../said";
import { folded } from "../fold";
import { Stick, toEnd } from "../stick";
import { askQueue, groupMessages, readMarks, stillWaiting, watchExtra, type AgentRow, type Did, type Group, type Msg, type Src, lineOf, workOf, workKey, eventParts, personName, type EventWords } from "./lib";
import { channelLog } from "./log";
import { betweenAgents, linesOf } from "./merge";
import { LongWords } from "../longwords";
import { mayAct, useMembers } from "./gate";
import { NotJoined } from "./key";
import { MemberFace } from "./face";
import { AgentFace } from "./members";
import { PersonCard, PersonMenu, useNamed } from "./person";
import { NickLink, Nicks, NickWords } from "./nick";

// At work: in a turn. Not "starting" (its harness coming up, no turn yet: an invited agent's face
// flashed up as if to speak and went; kumo, 2026-11-16).
const WORKING = new Set(["running", "compacting"]);

let lastStackH = 120;

const ghostOf = (row: Session): LiveSession => ({ ...row, asks: [], tasks: [], denials: [], queue: [], end: 0, proc: -1 });

/** The states of these sessions, as carried; drawn again as any of them changes. */
export function useStates(ids: string[]): (LiveSession | undefined)[] {
  const [, redraw] = useState(0);
  const key = ids.join(" ");
  useEffect(() => onCarried((k) => k.startsWith("session:") && ids.includes(k.slice(8)) && redraw((n) => n + 1)), [key]);
  return ids.map((id) => getCarried<LiveSession>(`session:${id}`));
}

/** bar: the chat's top bar (chat.tsx Bar). record: an agent's own record asked for (the
 *  channel's own session's: shown in its place). */
export function ChannelPage({ id, sheet = 0, bar, record }: { id: string; sheet?: number; bar: (s: Session | undefined) => ReactNode; record: (session: string) => void }) {
  useLang();
  const list = useList();
  const row = list?.find((x) => x.id === id);
  const real = useSession(id);
  const ghost = useMemo(() => row && ghostOf(row), [row]);
  const s = real ?? ghost;
  const [entry] = useState(() => channelLog(id));
  const log = entry.log;
  const [, redraw] = useState(0);
  const changed = () => redraw((n) => n + 1);
  const me = useMe().data;
  const self = me ?? LOCAL_ME;
  const terminals = useWhoList();
  const workspaces = useWorkspaces().data;
  const confirms = useConfirms();

  // Its agents: who they are from the list (at once), what each runs as and receives from the
  // members, their lamps from their sessions' states.
  const sessions = (row?.agents ?? []).map((a) => a.session);
  const states = useStates(sessions);
  const sig = states.map((st) => `${st?.model ?? ""}:${st?.effort ?? ""}:${st?.effortNow ?? ""}`).join(" ");
  const members = useMembers(id, sig);
  // Not of its people: read, not spoken in (docs/channels.md).
  const may = mayAct(members);
  const agents: AgentRow[] = (row?.agents ?? []).map((a, i) => {
    const m = members?.members.find((x) => x.session === a.session);
    const of = states[i] ?? list?.find((x) => x.id === a.session);
    const agent = of?.agent ?? m?.agent ?? a.agent ?? "claude";
    return { session: a.session, member: `agent:${a.session}`, agent, nick: m?.nick ?? a.nick ?? "", name: m?.name ?? agentName(agent), status: of ? lampOf(of) : (m?.status ?? "idle"), recv: m?.recv ?? "at" };
  });

  // The stream carries the other agents' states too, as far as it has room; the rest are asked
  // for as the list says they changed.
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
  useEffect(() => {
    sessions.forEach((x) => {
      if (carries(`session:${x}`)) return;
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
    more();
    const hear = (e: SatoriEvent) => {
      if (e.type !== "message-created" || e.channel?.id !== id || !e.message) return;
      log.heard({ ...e.message, _data: { ...e._data, ...e.message._data } });
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
  useEffect(() => {
    if (log.loaded) void log.newer().then(changed, () => {});
  }, [turns]);

  // What was sent from here, till the log has it.
  const sent = (entry.sent = entry.sent.filter((x) => stillWaiting(x, log.msgs)));
  const bubbles = useRef(new Map<string, HTMLDivElement>());
  const fly = useRef<string | null>(null);
  const scroller = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => {
    const el = fly.current && bubbles.current.get(fly.current);
    if (!el) return;
    fly.current = null;
    toEnd(scroller.current);
    pushIn(el);
  });
  const onSent = (x: Sent) => {
    x.from = log.end;
    entry.sent.push(x);
    fly.current = x.key;
    changed();
  };
  const retry = async (x: Sent) => {
    x.failed = undefined;
    changed();
    try {
      const r = await api.send(id, x.text, x.files.map((f) => f.ref), false, x.input, x.quote);
      x.seq = r.seq;
    } catch (e) {
      x.failed = errText(e);
    }
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
    const ro = new ResizeObserver(measure);
    ro.observe(el);
    return () => ro.disconnect();
  }, [!!s]);

  const composer = useRef<ComposerHandle>(null);
  const call = (nick: string) => composer.current?.insert(`@${nick}`);
  // Its members as words name them and as @ lists them (agents, people and apps: peers).
  const { named: nicks, others: peopleRows } = useNamed({ channel: id, agents, members, may, call, record });

  const files = (e: DragEvent) => e.dataTransfer.types.includes("Files") && (e.preventDefault(), true);
  const session = (e: DragEvent) => e.dataTransfer.types.includes(SESSION_DRAG) && (e.preventDefault(), (e.dataTransfer.dropEffect = "copy"), true);
  const place = (e: DragEvent) => e.dataTransfer.types.includes(PLACE_DRAG) && (e.preventDefault(), (e.dataTransfer.dropEffect = "copy"), true);
  const drop = (e: DragEvent) => {
    // Files dropped go to the composer, a channel with no agent too.
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
  // (the user, 2026-11-13); another login, an app, a Bot on the left, under its own face.
  const who = personWho(self, terminals, list ?? [], workspaces ?? []);
  const groups = useMemo(() => groupMessages(log.msgs), [log.msgs]);
  // Words between agents fold when long (merge.ts betweenAgents).
  const nickSet = new Set(agents.map((a) => a.nick.toLowerCase()).filter(Boolean));
  const fold = (m: Msg) => betweenAgents(m, log.msgs, nickSet);
  // Each agent's usage after its model's name, a strip of its own as small as a phone's (the
  // user, 2026-11-13): its context's ring, its quota's bars; each pressed, its popover.
  const bars = (session: string) => {
    const i = agents.findIndex((a) => a.session === session);
    return agents[i] ? <AgentBars a={agents[i]} st={states[i]} /> : null;
  };
  // The message being answered (<quote>), over the composer till it is sent.
  const [quote, setQuote] = useState<Msg | null>(null);
  const nameOf = (m: Msg) => (m.author.kind === "agent" ? m.author.nick || m.author.name : m.author.kind === "person" ? m.author.name : "Iridium");
  const quoting = (m: Msg) => (setQuote(m), composer.current?.focus());
  const quoted = (n: number) => {
    const m = log.msgs.find((x) => x.n === n);
    return m ? { who: nameOf(m), text: m.text } : undefined;
  };

  // How far each member has read (docs/channels.md "已读"): asked once, then as the stream
  // brings it. This page has read what it shows: said as the log's end moves while it is seen,
  // and as it is seen again.
  const reads = useCarried<ChannelReads>(`reads:${id}`);
  useEffect(() => {
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
  const working = agents.filter((a) => WORKING.has(a.status));
  const asking = agents.some((_, i) => states[i]?.asks.length);
  const grew = `${log.end} ${sent.length} ${working.map((a) => a.session).join(" ")}`;
  const padTop = 64; // the bar's 48px, a gap

  if (!s) return <div className="relative h-full">{bar(row)}</div>;
  return (
    <Nicks.Provider value={nicks}>
      <div className="relative h-full" onDragOver={(e) => files(e) || session(e) || place(e)} onDrop={drop}>
        <Stick scroller={scroller} tail={grew} older={() => log.older()} draw={changed} place={entry.place} bottom={stackH} quiet={asking} style={{ paddingTop: padTop, paddingBottom: stackH + 20 }}>
          <div className="mx-auto flex max-w-(--chat-w) flex-col gap-5 px-4 sm:px-6">
            {groups.map((g) => (
              <div key={g.key} className="relative flex min-w-0 flex-col">
                <GroupView g={g} channel={id} agents={agents} who={who} record={record} call={call} may={may} quote={may ? quoting : undefined} quoted={quoted} bars={bars} fold={fold} />
                {/* In the gap under it, over nothing: coming late, it moves nothing (docs/前端设计规范.md). */}
                {SHOW_READS && marks.has(g.key) && <ReadMarks readers={marks.get(g.key)!} agents={agents} />}
              </div>
            ))}
            {sent.map((x) => (
              <Pending
                key={x.key}
                s={x}
                who={yours(self)}
                words={<NickWords text={x.text} />}
                bubbleRef={(el) => void (el ? bubbles.current.set(x.key, el) : bubbles.current.delete(x.key))}
                retry={() => retry(x)}
                drop={() => ((entry.sent = entry.sent.filter((y) => y !== x)), changed())}
              />
            ))}
            {/* Each called agent at work: its face and name at once, as its words will be, the dots
                under them (kumo, 2026-11-15: not a line of its own without the face); under its
                own last words, the dots alone. */}
            {log.loaded && (
              <div className="-mt-2 flex min-h-7 flex-col gap-5 pointer-coarse:min-h-9">
                <Leaving
                  items={working}
                  keyOf={(a) => a.session}
                  draw={(a, leaving) => {
                    const last = groups.at(-1)?.author;
                    const alone = !sent.length && last?.kind === "agent" && last.session === a.session;
                    return (
                      <div className={cn(!alone && !leaving && "g-fade")}>
                        <AtWork a={a} channel={id} alone={alone} quiet={alone ? `${a.session} ${log.end}` : a.session} bars={bars(a.session)} call={may ? call : undefined} record={record} may={may} />
                      </div>
                    );
                  }}
                />
              </div>
            )}
          </div>
        </Stick>
        {bar(s)}
        <div ref={stack} {...{ [PULL]: "" }} data-covered={sheet > 0 || undefined} data-sunk={(sheet > 0 && stackH > sheet - 16) || undefined} inert={sheet > 0} className="g-turn-stay g-sink vt-stack absolute inset-x-0 bottom-0 z-10 px-2.5 pb-[max(10px,calc(env(safe-area-inset-bottom)+6px))] sm:px-5">
          <div aria-hidden className="pointer-events-none absolute inset-x-0 bottom-0 h-[calc(64px+max(10px,calc(env(safe-area-inset-bottom)+6px)))] bg-[linear-gradient(to_bottom,transparent,var(--paper)_14px)]" />
          <div className="relative mx-auto max-w-(--chat-w)">
            {agents.map((a, i) => {
              const st = states[i];
              return st?.queue?.length ? (
                <Of key={a.session} a={a} off={!may}>
                  <Queue session={a.session} queue={st.queue} steered={() => {}} />
                </Of>
              ) : null;
            })}
            {agents.map((a) => {
              const needs = confirms.filter((c) => c.session === a.session && c.kind !== "ask");
              return needs.length ? (
                <Of key={a.session} a={a} off={!may}>
                  <Asks session={a.session} asks={[]} cwd={s.cwd} confirms={needs} />
                </Of>
              ) : null;
            })}
            <AskQueue agents={agents} states={states} cwd={s.cwd} off={!may} />
            {!may ? <NotJoined /> : null}
            {may && <Composer ref={composer} s={s} turn={false} nicks={agents} people={peopleRows} quote={quote ? { n: quote.n, who: nameOf(quote), text: quote.text } : undefined} unquote={() => setQuote(null)} onSent={onSent} onSentDone={changed} cycleMode={() => {}} openModel={() => {}} openMode={() => {}} openUsage={() => {}} openContext={() => false} cleared={openChat} />}
          </div>
        </div>
      </div>
    </Nicks.Provider>
  );
}

/** Who said a person's words, and whether it is the one looking: only those are on the right
 *  (the user, 2026-11-13); another login, an app, a Bot on the left, under its own face. */
export type Who = (a: Extract<Msg["author"], { kind: "person" }>) => { who: ReturnType<typeof yours>; mine: boolean };
export function personWho(self: Parameters<typeof sourceOf>[2], terminals: Parameters<typeof sourceOf>[3], list: Session[], workspaces: { id: number; name: string }[]): Who {
  return (m) => {
    const src = sourceOf(m.name, m.id, self, terminals, list, workspaces);
    // The machine's own words (typed at its terminal, sent with its ir) are the host's.
    const mine = m.id === self.id || (!!self.host && (m.id === "local" || (src.kind === "you" && !!src.local)));
    if (src.kind === "you") return { who: src, mine };
    const known = terminals.find((x) => x.id === m.id);
    return { who: yours(known ? { ...known, name: m.name || known.name } : { id: m.id, name: m.name, device: "none" }), mine };
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

/** A called agent at work (kumo, 2026-11-15): its face and name at once, as its words will be,
 *  the dots under them; under its own last words (alone: no face again), the dots only. Its
 *  stop key at the row's right, for one of the channel's people (live.tsx StopKey). */
export function AtWork({ a, channel, alone, quiet, bars, call, record, may }: { a: AgentRow; channel: string; alone: boolean; quiet: string; bars: ReactNode; call?: (nick: string) => void; record: (session: string) => void; may: boolean }) {
  useLang();
  if (alone)
    return (
      <div className="flex h-7 items-center pl-[38px] pointer-coarse:h-9" role="status" aria-label={`${a.nick} · ${t("chat.working")}`}>
        <Waiting on quiet={quiet} />
        {may && <StopKey session={a.session} />}
      </div>
    );
  return (
    <div className="flex min-w-0 items-start gap-2.5" role="status" aria-label={`${a.nick} · ${t("chat.working")}`}>
      <AgentFace channel={channel} a={a} call={call} record={record} may={may} />
      <div className="flex min-w-0 flex-1 flex-col gap-1.5">
        <div className="flex h-4 min-w-0 items-center gap-1.5">
          <span className="shrink-0 text-xs font-medium text-ink-2">{a.nick}</span>
          <span className="min-w-0 truncate text-xs text-ink-4">{a.name}</span>
          {bars}
        </div>
        <div className="flex h-7 items-center pointer-coarse:h-9">
          <Waiting on quiet={quiet} />
          {may && <StopKey session={a.session} />}
        </div>
      </div>
    </div>
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
        <Logo agent={a.agent} size={12} />
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
  const words = fromSatori(m.text, (session) => agents.find((a) => a.session === session)?.nick);
  const b: Block = { at: String(m.n), kind: "assistant", text: words, ...(m.files.length ? { files: m.files } : {}), ...(m.time ? { time: m.time } : {}) };
  blocks.set(m, { sig, b });
  return b;
}

/** One speaker's messages: a person's bubble; an agent's words under its head; Iridium's line. */
export function GroupView({ g, channel, agents, who, record, call, may, quote, quoted, bars, fold }: { fold?: (m: Msg) => boolean; g: Group; channel: string; agents: AgentRow[]; who: Who; record: (session: string) => void; call: (nick: string) => void; may: boolean; quote?: (m: Msg) => void; quoted: (n: number) => { who: string; text: string } | undefined; bars: (session: string) => ReactNode }) {
  const list = useList();
  const a = g.author;
  if (a.kind === "iridium")
    return (
      <div className="flex flex-col">
        {g.msgs.map((m) => (
          <EventLine key={m.n} m={m} />
        ))}
      </div>
    );
  if (a.kind === "person") {
    const m = g.msgs[0]!;
    const by = who(a);
    return (
      <Copyable copy={m.text} quote={quote && (() => quote(m))}>
        <YouBubble
          text={m.text}
          who={by.who}
          left={!by.mine}
          time={m.time}
          words={<NickWords text={m.text} />}
          under={m.quote !== undefined && <QuoteLine n={m.quote} q={quoted(m.quote)} />}
          menu={by.mine ? undefined : (at, close) => <PersonMenu user={a.id} name={a.name} call={may ? call : undefined} at={at} close={close} />}
          card={<PersonCard user={a.id} name={a.name} avatar={"avatar" in by.who ? by.who.avatar : undefined} dm={!by.mine} call={may && !by.mine ? call : undefined} />}>
          {m.files.map((f) => (
            <Attached key={f.ref} session={channel} f={f} />
          ))}
        </YouBubble>
      </Copyable>
    );
  }
  const now = agents.find((x) => x.session === a.session);
  // One no longer in the channel: its harness as the list still has its session.
  const agent = now?.agent ?? list?.find((x) => x.id === a.session)?.agent;
  return (
    <MsgRow className="flex min-w-0 items-start gap-2.5">
      {now ? (
        <AgentFace channel={channel} a={now} call={may ? call : undefined} record={record} may={may} />
      ) : (
        <span className="grid size-7 shrink-0 place-items-center rounded-full bg-fill">{agent && <Logo agent={agent} size={16} />}</span>
      )}
      <div className="flex min-w-0 flex-1 flex-col gap-1.5">
        <div className="flex h-4 min-w-0 items-center gap-1.5">
          <span className="shrink-0 text-xs font-medium text-ink-2">{now?.nick || a.nick}</span>
          <span className="min-w-0 truncate text-xs text-ink-4">{now?.name || a.name}</span>
          {now && bars(now.session)}
          <When time={g.msgs[0]!.time} />
        </div>
        {g.msgs.map((m) => (
          <div key={m.n} className={cn(m.kind === "error" && "text-err")}>
            {m.src ? <Work src={m.src} did={m.did} said={m.text} /> : m.did && <DidLine did={m.did} />}
            <Copyable copy={m.text} quote={quote && (() => quote(m))}>
              <LongWords fold={fold?.(m) ?? false} lines={linesOf(m.text)}>
                <Assistant b={blockFor(m, agents)} session={a.session} />
              </LongWords>
            </Copyable>
            {m.quote !== undefined && <QuoteLine n={m.quote} q={quoted(m.quote)} />}
          </div>
        ))}
      </div>
    </MsgRow>
  );
}

/** The message one answers, under the words that answer it, as chat apps put it (kumo,
 *  2026-11-16: a line over the face read oddly): a small grey block, who said it and its words,
 *  two lines at most; the number alone when the page has not got it. */
function QuoteLine({ n, q }: { n: number; q?: { who: string; text: string } }) {
  return (
    <div className="mt-1 max-w-[min(560px,100%)] rounded-md bg-fill px-2.5 py-1.5 text-xs leading-[18px] text-ink-3">
      <span className="line-clamp-2 [overflow-wrap:anywhere]">{q ? t("channel.quoteOf", { who: q.who, text: q.text }) : `#${n}`}</span>
    </div>
  );
}

const EVENT_KEYS = ["join", "leave", "rename", "recv", "everyone", "added", "removed", "undelivered", "stopped", "silent", "silentWhy", "by", "sep", "profile", "profileChannel", "profileNoBy", "profileChannelNoBy"] as const;
const WHAT_KEYS = ["name", "nick", "intro", "avatar", "background"] as const;

/** A line of Iridium's own: what happened to the channel (who joined, left, was renamed, what one
 *  receives, its people), in the page's words, the agent in it as its nick is drawn elsewhere;
 *  one with no event (an older line), its own text. */
function EventLine({ m }: { m: Msg }) {
  useLang();
  const d = useApps().data;
  const words = { ...Object.fromEntries(EVENT_KEYS.map((k) => [k, t(`channel.event.${k}`)])), all: t("channel.recvAll"), at: t("channel.recvAt"), what: Object.fromEntries(WHAT_KEYS.map((k) => [k, t(`channel.what.${k}`)])), whatSep: t("channel.what.sep"), reasons: { 余额不足: t("channel.reason.balance"), 认证失败: t("channel.reason.auth"), 请求太频繁: t("channel.reason.rate") } } as unknown as EventWords;
  const parts = m.event && eventParts(m.event, words);
  if (!parts) return <Line>{m.text}</Line>;
  const apps = (d?.apps ?? []).map((a) => (a.web ? { ...a, name: t("term.panel") } : a));
  return (
    <Line>
      {parts.map((p, i) => (typeof p === "string" ? p : "nick" in p ? <NickLink key={i} nick={p.nick} /> : p.people.map((x) => personName(x, apps, d?.logins ?? [])).join(t("channel.event.sep"))))}
    </Line>
  );
}

/** What the agent did before the words under it (the user, 2026-11-13: all of it, not a count):
 *  its thinking and tool calls from its own record, folded as its own chat folds them. The
 *  count's line until they come, or when the record cannot be read. did: none for a stretch
 *  with no tool call (thinking alone), which has no count to show meanwhile. */
function Work({ src, did, said }: { src: Src; did?: Did; said?: string }) {
  const row = useList()?.find((x) => x.id === src.session);
  const q = useQuery({
    queryKey: workKey(src),
    queryFn: async () => {
      const page = src.from !== undefined ? await api.messages(src.session, { after: Math.max(0, src.from - 1) }) : await api.messages(src.session, { before: lineOf(src.block!) + 1 });
      return workOf(page.blocks, src, said);
    },
    staleTime: Infinity,
    retry: false,
  });
  const work = q.data;
  if (!work?.length) return did ? <DidLine did={did} /> : null;
  return (
    <div className="mb-1.5 flex min-w-0 flex-col gap-3">
      {folded(work).map((b) => (
        <Said key={b.at} b={b} session={src.session} cwd={row?.cwd ?? ""} source={() => ({ kind: "you" }) as ReturnType<Parameters<typeof Said>[0]["source"]>} onJump={openChat} />
      ))}
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
