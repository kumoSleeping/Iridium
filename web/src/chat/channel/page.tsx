// A channel as a chat (docs/channels.md): every chat in Iridium — 0, 1, or several agents — is
// drawn by this one page. Its members are a key at the bar's right (top.tsx). The conversation is
// the channel's messages and nothing else (docs/next/ui.md §1), as the log holds them (log.ts,
// rows.ts): your words a bubble on the right, anyone else's on the left under their face, one face
// for a run of one sender's; an agent's as Markdown under its face, nick and what it runs as, its
// usage (context ring, quota bars) after that, what it did after each one line under it (work.tsx);
// Iridium's lines small and centred.
// When the channel has a single agent, /commands, /model, /mode, /clear, Shift+Tab and Esc work
// directly on that agent.
import { flushSync } from "react-dom";
import { memo, useEffect, useLayoutEffect, useMemo, useRef, useState, type DragEvent, type ReactNode } from "react";
import { Popover } from "@base-ui/react/popover";
import { api, ApiError } from "../../api/client";
import type { Ask, Block, ChannelReads, Reader, SessionState as LiveSession, Session } from "../../api/types.gen";
import { firstScreen, fresh, freshState, isWhole, stateAhead, type Sent } from "../../chats";
import { LOCAL_ME, useApps, useMe, useWhoList, useWorkspaces } from "../../data";
import { t, useLang } from "../../i18n";
import { pushIn, pushing } from "../../motion";
import { openChat, openUsage } from "../../route";
import { PULL } from "../../sheet";
import { getCarried, onCarried, setCarried, useCarried, useConfirms, useList, useListRow, useSession } from "../../store";
import { also, carries, events, opened, type SatoriEvent } from "../../stream";
import { errText, toast } from "../../toast";
import { agentName, cn, lampOf, PopSurface, ServerFace } from "../../ui";
import { useSecret } from "../../secrets";
import { AskDone, AskInline, Asks } from "../asks";
import { placeDropped } from "../at";
import { Composer, type ComposerHandle } from "../composer";
import { Copyable } from "../copyable";
import { Crash } from "../crash";
import { ContextRing, ModeKey, ModelKey, modelView, QuotaBars, useMode } from "../meters";
import { fromSatori, PLACE_DRAG, SESSION_DRAG, sourceOf, withDevice, yours, type Source } from "../refs";
import { shownAs } from "../files";
import { Gallery } from "../gallery";
import { shotsOf, type Said } from "../gallery-lib";
import { Assistant, Attached, clock, Compaction, Gutter, Letter, Line, MsgRow, Pending, When, YouBubble } from "../said";
import { ShellRuns } from "../shell";
import { Stick, toEnd } from "../stick";
import { justMade } from "./adding";
import { ChannelSplash } from "../../splash/splash";
import { WorkLine } from "../work";
import { askQueue, quoteAt, watchExtra, type AgentRow, type Did, type Msg, type Work, eventParts, personName, type EventWords } from "./lib";
import { channelLog } from "./log";
import { doneBefore, openAsks, readMarks, type Group, type Person } from "./rows";
import { lendActs } from "./menu";
import { onOf } from "./scripts";
import { useRecvLabel } from "./recv";
import { betweenAgents, linesOf, longWords, oldRead } from "./folds";
import { Foldable } from "../longwords";
import { foldsOnce, useReadTo } from "../foldstate";
import { around, goTo, seqEl } from "./jump";
import { LazyRows } from "./lazyrows";
import { Forwards, quoteText } from "../forwardcard";
import { msgWords, withForwards } from "../forward";
import { mayAct, useMembers } from "./gate";
import { NotJoined } from "./key";
import { Face as ChannelFace, MemberFace } from "./face";
import { AgentFace } from "./members";
import { PersonCard, PersonMenu, useNamed } from "./person";
import { NickLink, Nicks, NickWords, SeqRefs, type Seqs } from "./nick";
import { useRecalled } from "../reactions";
import { lendWork, unlendWork } from "./workkey";

// At work: in a turn. Not "starting" (its harness coming up, no turn yet: an invited agent's face
// flashed up as if to speak and went; kumo, 2026-11-16).
const WORKING = new Set(["running", "compacting"]);
const BUSY = new Set(["running", "compacting", "starting", "waiting"]);
const turning = (s?: { status: string }) => !!s && BUSY.has(s.status);

let lastStackH = 120;

const ghostOf = (row: Session): LiveSession => ({ ...row, asks: [], tasks: [], queue: [], end: 0, proc: -1 });

/** The states of these sessions, as carried; drawn again as any of them changes. */
export function useStates(ids: string[]): (LiveSession | undefined)[] {
  const [, redraw] = useState(0);
  const key = ids.join(" ");
  useEffect(() => onCarried((k) => k.startsWith("session:") && ids.includes(k.slice(8)) && redraw((n) => n + 1)), [key]);
  return ids.map((id) => getCarried<LiveSession>(`session:${id}`));
}

/** bar: the chat's top bar (chat.tsx Bar). record: opens a session's read-only TranscriptView. */
export function ChannelPage({ id, sheet = 0, bar, record }: { id: string; sheet?: number; bar: (s: Session | undefined) => ReactNode; record: (session: string) => void }) {
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

  // The newest page, or what was kept; then each message as its events bring it: made, changed,
  // taken back (log.ts heard). What the events may have missed (the stream down a while) is read
  // once it is back, the messages still being written then with it.
  useEffect(() => {
    let live = true;
    const back = () => void log.newer(true).then(() => live && changed(), () => {});
    // Held from before and not on screen since: what was at work then may have ended unheard
    // (its lines kept their stop keys: kumo, 2026-11-25), so those are read again.
    if (!fresh(log.came)) back();
    const hear = (e: SatoriEvent) => {
      if ((e.type !== "message-created" && e.type !== "message-updated" && e.type !== "message-deleted") || e.channel?.id !== id || !e.message) return;
      log.heard({ ...e.message, _data: { ...e._data, ...e.message._data } }, e.type);
      changed();
    };
    events.add(hear);
    opened.add(back);
    return () => {
      live = false;
      events.delete(hear);
      opened.delete(back);
    };
  }, [id]);

  // What was sent from here, till the log has it (log.ts: by its input id or its number).
  const sent = log.sent;
  const bubbles = useRef(new Map<string, HTMLDivElement>());
  const fly = useRef<string | null>(null);
  const scroller = useRef<HTMLDivElement>(null);
  // When each one sent began to come in: its message, come from the log in the middle of that,
  // goes on from where its bubble was (motion.ts pushIn), not drawn whole at once.
  const flew = useRef(new Map<Sent, number>());
  useLayoutEffect(() => {
    const el = fly.current && bubbles.current.get(fly.current);
    if (el) {
      const x = log.sent.find((y) => y.key === fly.current);
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
      const n = log.became(x);
      const row = n !== undefined ? scroller.current?.querySelector<HTMLElement>(`[data-seq="${n}"]`) : null;
      if (!row) continue;
      flew.current.delete(x);
      pushIn(row, t);
    }
  });
  const onSent = (x: Sent) => {
    x.mid = agents.some((a) => WORKING.has(a.status));
    log.send(x);
    fly.current = x.key;
    changed();
  };
  const sentDone = (x: Sent) => {
    x.at = Date.now();
    log.answered(x);
    changed();
  };
  const retry = async (x: Sent) => {
    x.failed = undefined;
    x.at = undefined;
    log.resend(x);
    changed();
    try {
      const r = await api.send(id, x.text, x.files.map((f) => f.ref), x.input, x.quote);
      x.seq = r.seq;
    } catch (e) {
      x.failed = errText(e);
    }
    sentDone(x);
  };

  // The bottom stack's height pads the conversation's end.
  const stack = useRef<HTMLDivElement>(null);
  const [stackH, setStackH] = useState(lastStackH);
  // The composer's stack sits on the window's foot as the window is, in the frame it is: the
  // keyboard gone, it is down with it, not held up and let down after (kumo, 2026-11-25).
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
  const whoNow = useRef(who);
  whoNow.current = who;
  const mine = useMemo(() => (a: Person) => whoNow.current(a).mine, [self.id, self.host, terminals]);
  // The conversation, as the log gives it (log.ts, rows.ts): its rows in order, under their heads.
  const groups = log.groups(mine);
  const hidden = useMemo(() => doneBefore(log.msgs), [log.msgs]);
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
  // "#n" in the words, a key to message n (nick.tsx SeqLink): one object, its fields kept current.
  const seqs = useRef<Seqs>({ end: 0, jump }).current;
  seqs.jump = jump;
  seqs.end = log.loaded || log.msgs.length ? log.end : 0;
  // Who works, lent to the top bar's work key (workkey.tsx) at each draw, once the conversation is.
  useEffect(() => void (!hold && lendWork(id, { agents, stateAt, msgs: log.msgs, jump, may, solo: !!solo })));
  useEffect(() => () => unlendWork(id), [id]);
  // A bubble of what was sent from here, not yet its message.
  const pending = (x: Sent, bare: boolean) => (
    <Pending
      s={x}
      who={yours(self)}
      bare={bare}
      words={<NickWords text={x.text} />}
      quote={x.quote !== undefined ? <Quoted n={x.quote} q={quoted(x.quote)} channel={id} jump={jump} /> : undefined}
      bubbleRef={(el) => void (el ? bubbles.current.set(x.key, el) : bubbles.current.delete(x.key))}
      retry={() => retry(x)}
      drop={() => (log.unsend(x), changed())}
    />
  );

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
  // Read as far as it has been gone down to: what came under a view not at its end (scrolled up,
  // or come all at once as the page opened) is not read by being there (kumo, 2026-11-26).
  const [atEnd, setAtEnd] = useState(entry.place.top === null);
  useEffect(() => {
    if (!end || !may || !seen || !atEnd) return;
    const timer = setTimeout(() => void api.read(id, end).then((r) => setCarried(`reads:${id}`, r), () => {}), 600);
    return () => clearTimeout(timer);
  }, [id, end, may, seen, atEnd]);
  // Where this reader had read to, as the page opens with more than that: opened there (stick.tsx).
  const readAt = members?.me ? reads?.readers.find((r) => r.member === members.me)?.seq : undefined;
  const openAt = readAt !== undefined && log.loaded && readAt < log.end ? readAt : undefined;
  // This device's copy goes on with the channel as messages come, not only as a turn ends: loaded
  // again in the middle of one, the page is drawn as it was, not as it was a turn ago (kumo,
  // 2026-11-26).
  useEffect(() => {
    if (end) void import("../../device/chats").then((m) => m.saveSoon(id)).catch(() => {});
  }, [id, end]);
  const marks = useMemo(() => readMarks(groups, reads?.readers ?? [], members?.me), [groups, reads, members?.me]);
  // Long words read before the latest rounds fold of themselves (channel/folds.ts), as each is
  // first drawn once the place read to is known.
  const readTo = useReadTo(id, reads?.readers, members?.me, log.loaded ? log.end : undefined);
  const olds = useMemo(() => (readTo === undefined ? undefined : oldRead(log.msgs, readTo)), [log.msgs, readTo]);
  const old = (m: Msg) => foldsOnce(id, `m${m.n}`, olds?.has(m.n));
  const secretAt = useSecret()?.at;
  const secretHere = secretAt === id || (!!solo && secretAt === solo.session);
  // The asks open, as the channel's messages say (an ask's message carries it while it is open).
  // Those with a message of theirs are drawn in it (GroupView); only one with none (its message
  // not come yet) over the composer still.
  const asks = useMemo(() => {
    const open = openAsks(log.msgs, agents.map((a) => ({ session: a.session, asks: stateAt(a.session)?.asks ?? [] })));
    const said = new Set(log.msgs.flatMap((m) => (m.kind === "ask" && m.card ? [m.card] : [])));
    const out = new Map<string, Ask[]>();
    for (const [k, v] of open) {
      const left = v.filter((a) => !said.has(a.card?.id ?? `ask:${k}:${a.requestId}`));
      if (left.length) out.set(k, left);
    }
    return out;
  }, [log.msgs, agents.map((a) => `${a.session}:${(stateAt(a.session)?.asks ?? []).map((x) => x.requestId).join(",")}`).join(" ")]);
  const asking = agents.some((a) => !!asks.get(a.session)?.length) || agents.some((a) => confirms.some((c) => c.session === a.session && c.kind !== "ask")) || secretHere;
  // A compaction is said on the agent's own line of work, with its steps (work.tsx; kumo,
  // 2026-11-26); the rule across the channel only where there is no such line going (a /compact
  // of the channel's one agent with no turn of its own).
  const compacting = soloState?.status === "compacting" && !log.msgs.some((m) => m.work?.running);
  const compactSince = useRef(0);
  if (!compacting) compactSince.current = 0;
  else if (soloState?.compactingSince) compactSince.current = soloState.compactingSince;
  else if (!compactSince.current) compactSince.current = Date.now();
  // The conversation grew at its end: a message come, a bubble, the last message written on.
  const last = log.msgs.at(-1);
  const grew = `${log.end} ${sent.length} ${last?.text.length ?? 0}`;
  const padTop = 64; // the bar's 48px, a gap
  // What the rows call, the latest of it behind functions that stay the same, and the agents and
  // their strips as what they say: a group is drawn again for its own change alone, not for each
  // piece of another's draft (GroupView; a long channel streamed at a few frames a second).
  const latest = useRef({ who, call, quoting, quoted, bars, fold, old, jump, pending });
  latest.current = { who, call, quoting, quoted, bars, fold, old, jump, pending };
  const fx = useMemo(() => {
    const l = () => latest.current;
    return {
      who: ((a) => l().who(a)) as Who,
      call: (nick: string) => l().call(nick),
      quoting: (m: Msg) => l().quoting(m),
      quoted: (n: number) => l().quoted(n),
      bars: (session: string) => l().bars(session),
      fold: (m: Msg) => l().fold(m),
      old: (m: Msg) => l().old(m),
      jump: (n: number) => l().jump(n),
      pending: (x: Sent, bare: boolean) => l().pending(x, bare),
    };
  }, []);
  const agentsSig = JSON.stringify(agents);
  const rowAgents = useMemo(() => agents, [agentsSig]);
  const barsSig = JSON.stringify(
    agents.map((a) => {
      const st = stateAt(a.session);
      return st && [st.context, st.tokens, st.machine, st.live, st.model, st.pick, st.effort];
    }),
  );

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
     <SeqRefs.Provider value={seqs}>
     <Gallery shots={() => shotsOf(log.msgs.flatMap((m) => saidOf(m, id)), (session, f) => api.fileURL(session, f.ref, false, f.mtime), shownAs)}>
      <div className="relative h-full" onDragOver={(e) => files(e) || session(e) || place(e)} onDrop={drop}>
        {/* Nothing said yet: the pane's splash is told so (splash/splash.tsx). */}
        <ChannelSplash id={id} show={!hold && !sent.length && (log.loaded ? !log.msgs.length : justMade(id))} last={agents.at(-1)} agents={agents.length} />
        <Stick key={hold ? "hold" : "whole"} scroller={scroller} tail={grew} older={() => log.older()} draw={changed} place={entry.place} keep={id} openAt={openAt} late={log.late} onFollow={setAtEnd} bottom={stackH} quiet={asking} style={{ paddingTop: padTop, paddingBottom: stackH + 20 }}>
          <div data-column className="mx-auto flex max-w-(--chat-w) flex-col gap-5 px-4 [--depart-gap:1.25rem] sm:px-6">
            {!hold && (
              <LazyRows owner={log} rows={groups} same={sameRow} place={entry.place} className="relative flex min-w-0 flex-col">
                {(g) => (
                 <>
                  {g.mark !== undefined && <TimeMark at={g.mark} />}
                  <GroupView g={g} channel={id} agents={rowAgents} who={fx.who} record={record} call={fx.call} may={may} quote={may ? fx.quoting : undefined} quoted={fx.quoted} bars={fx.bars} barsSig={barsSig} fold={fx.fold} old={fx.old} olds={olds} jump={fx.jump} wrote={log.wrote} born={log.born} hidden={hidden} pending={fx.pending} />
                  {/* In the gap under it, over nothing: coming late, it moves nothing (docs/前端设计规范.md). */}
                  {SHOW_READS && marks.has(g.key) && <ReadMarks readers={marks.get(g.key)!} agents={agents} />}
                 </>
                )}
              </LazyRows>
            )}
            {!hold && log.loaded && compacting && (
              <div className="-mt-1">
                <Compaction since={compactSince.current} />
              </div>
            )}
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
            <AskQueue agents={agents} states={agents.map((a) => stateAt(a.session))} asks={asks} cwd={s.cwd} off={!may} />
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
     </Gallery>
     </SeqRefs.Provider>
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
      openUsage={() => openUsage((solo ?? s).machine)}
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
          <div key={r.member} className="flex h-8 items-center gap-2.5 px-2.5 text-base pointer-coarse:h-9">
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
export function AskQueue({ agents, states, asks, cwd, off }: { agents: AgentRow[]; states: (LiveSession | undefined)[]; asks: Map<string, Ask[]>; cwd: string; off: boolean }) {
  const [all, setAll] = useState(false);
  const key = agents.map((a) => a.session).join(",");
  let seen = SEEN.get(key);
  if (!seen) SEEN.set(key, (seen = new Map()));
  const queue = askQueue(agents.map((a) => ({ session: a.session, asks: asks.get(a.session) ?? [] })), seen);
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
/** A message as its pictures are drawn (gallery-lib.ts), its files served as GroupView serves them:
 *  an agent's by its session, unless it answers an input of the channel's; any other's by the
 *  channel. None for Iridium's lines and a crash. */
function saidOf(m: Msg, channel: string): Said[] {
  if (!m.files.length || m.kind === "error" || m.author.kind === "iridium") return [];
  if (m.author.kind === "person") return [{ words: "", files: m.files, session: channel, person: true }];
  const session = m.author.kind === "agent" && !m.input ? m.author.session : channel;
  return [{ words: fromSatori(msgWords(m).words), files: m.files, session, person: false }];
}

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

/** One speaker's rows (rows.ts Group): a person's bubbles, the first under its face and name, a
 *  bubble of what was sent from here not yet its message among them (pending); an agent's words
 *  under its head, each with its work's line; Iridium's lines. Each message by its number
 *  (data-seq, where a quote goes: jump), folding by hand or of itself (old; fold: words between
 *  agents). wrote: the messages this page saw written, drawn so to their end and never folded of
 *  themselves. hidden: those whose count of what was done before them is the line of the one
 *  before (rows.ts doneBefore). */
type GroupProps = { born?: Set<number>; fold?: (m: Msg) => boolean; old?: (m: Msg) => boolean; olds?: ReadonlySet<number>; jump?: (n: number) => void; g: Group<Sent>; channel: string; agents: AgentRow[]; who: Who; record: (session: string) => void; call: (nick: string) => void; may: boolean; quote?: (m: Msg) => void; quoted: (n: number) => { who: string; text: string } | undefined; bars: (session: string) => ReactNode; barsSig?: string; wrote?: ReadonlySet<number>; hidden?: ReadonlySet<number>; pending?: (x: Sent, bare: boolean) => ReactNode };

/** Whether a group draws the same: its rows the same messages (the log keeps a message not changed
 *  the same object), the sets it reads saying the same of its messages, the rest the same values.
 *  One with a bubble is drawn again each time: a bubble is changed where it is (failed, sent again). */
function sameGroup(a: GroupProps, b: GroupProps): boolean {
  if (b.g.lines.some((l) => l.sent)) return false;
  for (const k of Object.keys(b) as (keyof GroupProps)[]) {
    if (k === "g" || k === "hidden" || k === "olds" || a[k] === b[k]) continue;
    return false;
  }
  if (a.g !== b.g) {
    if (a.g.key !== b.g.key || a.g.author !== b.g.author || a.g.lines.length !== b.g.lines.length || a.g.bare !== b.g.bare) return false;
    for (let i = 0; i < b.g.lines.length; i++) {
      const x = a.g.lines[i]!, y = b.g.lines[i]!;
      if (x.key !== y.key || x.m !== y.m || x.sent !== y.sent) return false;
    }
  }
  if (!a.olds !== !b.olds) return false;
  for (const l of b.g.lines) if (l.m && (a.hidden?.has(l.m.n) !== b.hidden?.has(l.m.n) || a.olds?.has(l.m.n) !== b.olds?.has(l.m.n))) return false;
  return true;
}

/** Whether a group is what it was when its row's height was taken (lazyrows.tsx): its rows the
 *  same ones, under the same head and time. */
const sameRow = (a: Group<Sent>, b: Group<Sent>) => a.key === b.key && a.author === b.author && a.bare === b.bare && a.mark === b.mark && a.lines.length === b.lines.length && a.lines.every((x, i) => x.key === b.lines[i]!.key && x.m === b.lines[i]!.m && x.sent === b.lines[i]!.sent);

export const GroupView = memo(function GroupView({ g, channel, agents, who, record, call, may, quote, quoted, bars, fold, old, jump, wrote, born, hidden, pending }: GroupProps) {
  const a = g.author;
  // Its own row of the list alone: not drawn again as any other session changes.
  const listed = useListRow(a?.kind === "agent" ? a.session : undefined);
  const msgs = g.lines.flatMap((l) => (l.m ? [l.m] : []));
  if (a?.kind === "iridium") return <EventLines msgs={msgs} channel={channel} may={may} />;
  if (!a || a.kind === "person")
    return (
      <div className="flex flex-col gap-1.5">
        {g.lines.map((l, i) => (l.m && l.m.author.kind === "person" ? <PersonLine key={l.key} m={l.m} a={l.m.author} bare={i > 0 || !!g.bare} channel={channel} who={who} call={call} may={may} quote={quote} quoted={quoted} old={old} jump={jump} /> : l.sent ? <div key={l.key}>{pending?.(l.sent, i > 0 || !!g.bare)}</div> : null))}
      </div>
    );
  if (a.kind === "script") return <ScriptGroup msgs={msgs} bare={g.bare} channel={channel} agents={agents} quote={quote} quoted={quoted} old={old} jump={jump} />;
  const now = agents.find((x) => x.session === a.session);
  // One no longer in the channel, or an outsider writing via ir: its harness as the list has it.
  const agent = now?.agent ?? listed?.agent;
  return (
    <MsgRow className="flex min-w-0 items-start gap-2.5">
      {g.bare ? (
        // Its run going on past an event's line: under no head again, where its words were.
        <span className="w-7 shrink-0" />
      ) : now ? (
        <AgentFace channel={channel} a={now} call={may ? call : undefined} record={record} may={may} />
      ) : (
        <span className="grid size-7 shrink-0 place-items-center rounded-full bg-fill">
          {agent ? <ChannelFace id={a.session} agent={agent} size={28} logo={16} /> : <ChannelFace id={a.session} agent={"claude"} size={28} none={<span />} />}
        </span>
      )}
      <div className="flex min-w-0 flex-1 flex-col gap-1.5">
        <div data-lead="" className={cn("flex h-4 min-w-0 items-center gap-1.5", g.bare && "hidden")}>
          <span className="shrink-0 text-xs font-medium text-ink-2">{now?.nick || a.nick}</span>
          <span className="min-w-0 truncate text-xs text-ink-4">{/* What it runs as now; that not known just now (its kind's name alone: its state between
              two turns, a limit hit), what its message says it ran as, not the bare kind: the
              line shortened and the row jumped (kumo, 2026-11-25). */}
            {now?.name && !(a.name && a.name.length > now.name.length && now.name === agentName(now.agent)) ? now.name : a.name}</span>
          {now && bars(now.session)}
          <When time={msgs[0]?.time} n={msgs[0]?.n} lead />
        </div>
        {msgs.map((m) => {
          const w = msgWords(m);
          const said = !!w.words.trim() || m.files.length > 0 || w.forwards.length > 0;
          const seen = !!wrote?.has(m.n);
          const session = m.src?.session ?? a.session;
          const line = (props: { work?: Work; did?: Did; span?: { from: number; to: number } }) => <WorkLine channel={channel} n={m.n} session={session} may={may} esc={agents.length === 1} {...props} />;
          return (
            <div
              key={m.n}
              data-seq={m.n}
              // Its own time and number beside it, over it; the first's are on the head (When lead).
              data-lead={m === msgs[0] && !g.bare ? "" : undefined}
              className="group/line relative"
              // Come by an event: in as a bubble sent from here comes (kumo, 2026-11-24), its
              // head with it where it is the first of its sender's run.
              ref={(el) => void (el && born?.delete(m.n) && pushIn(m === msgs[0] ? (el.closest<HTMLElement>("[data-row]") ?? el) : el))}
            >
              {(m !== msgs[0] || g.bare) && <Gutter time={m.time} n={m.n} />}
              {/* An ask: its card while open, a line once over, who answered it there. */}
              {m.kind === "ask" && (m.ask ? <AskInline session={a.session} ask={m.ask} cwd={listed?.cwd ?? ""} off={!may} /> : <AskDone asked={m.asked} answer={m.answer} text={m.text} />)}
              {/* What an older message says was done before it, where the one before has no line of its own for it. */}
              {/* No work of its own (said before messages carried it, or a twin where it was called): its steps from its record, by the span it says, as before. */}
              {!m.work && !hidden?.has(m.n) && (m.did || (m.src?.from !== undefined && !!m.src.to)) && line({ ...(m.did ? { did: m.did } : {}), ...(m.src?.from !== undefined && m.src.to ? { span: { from: m.src.from, to: m.src.to } } : {}) })}
              {/* Its process ended badly: the channel's message of it, said as such (the line under
                  the conversation it was is gone: kumo, 2026-11-25). */}
              {said && m.kind === "error" && <Crash error={w.words} at="said" />}
              {m.quote !== undefined && (
                <div className="mb-1">
                  <Quoted n={m.quote} q={quoted(m.quote)} channel={channel} jump={jump} />
                </div>
              )}
              {said && m.kind !== "error" && m.kind !== "ask" && (
                <Foldable scope={channel} id={`m${m.n}`} auto={!seen && (((fold?.(m) ?? false) && longWords(w.words)) || (old?.(m) ?? false))} lines={linesOf(w.words)}>
                  {(wrap) => (
                    <Copyable copy={withForwards(w.words, w.forwards)} quote={quote && (() => quote(m))} act={m.kind === "error" || m.kind === "ask" ? undefined : { channel, seq: m.n, may }}>
                      {wrap(
                        <>
                          <Assistant b={blockFor(m, agents)} session={m.input ? channel : a.session} draft={seen ? { live: !!m.writing } : undefined} />
                          <Forwards fs={w.forwards} />
                        </>,
                      )}
                    </Copyable>
                  )}
                </Foldable>
              )}
              {m.work && line({ work: m.work })}
            </div>
          );
        })}
      </div>
    </MsgRow>
  );
}, sameGroup);

/** A person's message: a bubble, or a letter (another session's ir, a plugin, a peer); bare: under
 *  the head of the one before it from the same sender. */
function PersonLine({ m, a, bare, channel, who, call, may, quote, quoted, old, jump }: { m: Msg; a: Extract<Msg["author"], { kind: "person" }>; bare: boolean; channel: string; who: Who; call: (nick: string) => void; may: boolean; quote?: (m: Msg) => void; quoted: (n: number) => { who: string; text: string } | undefined; old?: (m: Msg) => boolean; jump?: (n: number) => void }) {
  const by = who(a);
  const w = msgWords(m);
  const body = [...m.files.map((f) => <Attached key={f.ref} session={channel} f={f} />), ...(w.forwards.length ? [<Forwards key="forwards" fs={w.forwards} />] : [])];
  const over = m.quote !== undefined ? <Quoted n={m.quote} q={quoted(m.quote)} channel={channel} jump={jump} /> : undefined;
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
                n={m.n}
                onJump={openChat}
                bare={bare}
                words={wrap(<NickWords text={w.words} n={m.n} />)}
                quote={over}
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
                n={m.n}
                bare={bare}
                words={wrap(<NickWords text={w.words} n={m.n} />)}
                quote={over}
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

/** A command's messages (docs/channels.md "命令"): said as Iridium, its face and name over what
 *  it printed, as an agent's words are drawn (kumo, 2026-11-24: no face or name of its own). */
function ScriptGroup({ msgs, bare, channel, agents, quote, quoted, old, jump }: { msgs: Msg[]; bare?: boolean; channel: string; agents: AgentRow[]; quote?: (m: Msg) => void; quoted: (n: number) => { who: string; text: string } | undefined; old?: (m: Msg) => boolean; jump?: (n: number) => void }) {
  return (
    <MsgRow className="flex min-w-0 items-start gap-2.5">
      {bare ? <span className="w-7 shrink-0" /> : <ServerFace size={28} />}
      <div className="flex min-w-0 flex-1 flex-col gap-1.5">
        <div data-lead="" className={cn("flex h-4 min-w-0 items-center gap-1.5", bare && "hidden")}>
          <span className="min-w-0 truncate text-xs font-medium text-ink-2">Iridium</span>
          <When time={msgs[0]?.time} n={msgs[0]?.n} lead />
        </div>
        {msgs.map((m) => {
          const w = msgWords(m);
          return (
            <div key={m.n} data-seq={m.n} data-lead={m === msgs[0] && !bare ? "" : undefined} className="group/line relative">
              {(m !== msgs[0] || bare) && <Gutter time={m.time} n={m.n} />}
              {m.quote !== undefined && (
                <div className="mb-1">
                  <Quoted n={m.quote} q={quoted(m.quote)} channel={channel} jump={jump} />
                </div>
              )}
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

            </div>
          );
        })}
      </div>
    </MsgRow>
  );
}

/** The message one answers, over the words that answer it and touching them, as chat apps put
 *  it: in a person's bubble at its top, over an agent's words (kumo, 2026-11-26: a grey block
 *  under them, in a run of several, was not told from the next one's). One line, a bar on its
 *  left, who said it and its words; the number alone when the page has not got it; taken back,
 *  the number and so (docs/channels.md "撤回"). Pressed, the message itself (jump). */
/** Words for a line of their own: Markdown's marks (bold, code, heads, lists, quotes) dropped, one line. */
const plain = (text: string) => text.replace(/^\s{0,3}(#{1,6}|>|[-*+]|\d+\.)\s+/gm, "").replace(/\*\*|__|`/g, "").replace(/\s+/g, " ").trim();

function Quoted({ n, q, channel, jump }: { n: number; q?: { who: string; text: string }; channel: string; jump?: (n: number) => void }) {
  const gone = useRecalled(channel, n);
  const words = gone ? (
    t("channel.quoteGone", { n })
  ) : q ? (
    <>
      <span className="font-medium text-ink-2">{q.who}</span> {plain(q.text)}
    </>
  ) : (
    `#${n}`
  );
  const body = (
    <>
      <span aria-hidden className="w-0.5 shrink-0 rounded-full bg-ink-4" />
      <span className="min-w-0 truncate">{words}</span>
    </>
  );
  const cls = "flex max-w-[min(560px,100%)] min-w-0 items-stretch gap-1.5 text-left text-xs leading-[18px] text-ink-3";
  if (!jump || gone) return <div className={cls}>{body}</div>;
  return (
    <button type="button" onClick={() => jump(n)} className={cn(cls, "g-press cursor-pointer hover:text-ink-2")}>
      {body}
    </button>
  );
}

const EVENT_KEYS = ["join", "leave", "rename", "recv", "on", "wake", "everyone", "added", "removed", "undelivered", "stopped", "silent", "silentWhy", "by", "sep", "profile", "profileChannel", "profileNoBy", "profileChannelNoBy"] as const;
const SCRIPT_KEYS = { add: "scriptAdd", remove: "scriptRemove", enabled: "scriptEnabled", disabled: "scriptDisabled", changed: "scriptChanged", renamed: "scriptRenamed" } as const;
const WHAT_KEYS = ["name", "nick", "persona", "avatar"] as const;

/** The time said over a run (rows.ts grouped: a while since the row before, a day changed, the
 *  channel's first): today's by its clock, yesterday's so, this week's by its day, older by its
 *  date (kumo, 2026-11-26). One line, its height the same whatever it says. */
function TimeMark({ at }: { at: number }) {
  useLang();
  const d = new Date(at), now = new Date();
  const days = Math.round((new Date(now.toDateString()).getTime() - new Date(d.toDateString()).getTime()) / 86_400_000);
  const l = document.documentElement.lang === "en" ? "en" : "zh-CN";
  const day = days === 0 ? t("channel.mark.today") : days === 1 ? t("channel.mark.yesterday") : days > 1 && days < 7 ? new Intl.DateTimeFormat(l, { weekday: "short" }).format(d) : new Intl.DateTimeFormat(l, { ...(d.getFullYear() !== now.getFullYear() ? { year: "numeric" } : {}), month: l === "en" ? "short" : "long", day: "numeric" }).format(d);
  return <div className="mb-3 flex h-4 items-center justify-center text-xs leading-4 text-ink-4 tabular-nums">{`${day} ${clock(at)}`}</div>;
}

/** The kinds of Iridium's lines that, one after another, are said as one: "A, B joined". */
const MERGED = new Set(["join", "leave"]);
/** How many of Iridium's lines in a row are shown before the rest are folded under one. */
const EVENTS_SHOWN = 3;

/** Iridium's lines one after another: those of one kind (joined, removed) as one line; more than
 *  EVENTS_SHOWN, the first under one line that says how many, opened as it is pressed (kumo,
 *  2026-11-26). */
function EventLines({ msgs, channel, may }: { msgs: Msg[]; channel: string; may?: boolean }) {
  const [open, setOpen] = useState(false);
  const runs: Msg[][] = [];
  for (const m of msgs) {
    const r = runs.at(-1), k = m.event?.type;
    if (r && k && MERGED.has(k) && r[0]!.event?.type === k && m.event?.nick) r.push(m);
    else runs.push([m]);
  }
  const line = (r: Msg[]) => (r[0]!.event?.type === "stopped" && r[0]!.event.count ? <PausedLine key={r[0]!.n} m={r[0]!} channel={channel} may={may} /> : r[0]!.event?.type === "resumed" ? <Line key={r[0]!.n}>{t("channel.event.resumed", { by: r[0]!.event.by ?? "", seq: r[0]!.event.seq ?? "" })}</Line> : <EventLine key={r[0]!.n} m={r[0]!} also={r.slice(1)} channel={channel} />);
  if (runs.length <= EVENTS_SHOWN) return <div className="flex flex-col">{runs.map(line)}</div>;
  const hid = runs.slice(0, runs.length - (EVENTS_SHOWN - 1));
  // Opened: its height not glided (docs/动画.md 4: the conversation's scroll keeps what is seen).
  return (
    <div className="flex flex-col">
      {open ? (
        hid.map(line)
      ) : (
        <button type="button" onClick={() => setOpen(true)} className="g-press py-1 text-xs leading-4 text-ink-4 hover:text-ink-3">
          {t("channel.eventsMore", { n: hid.length })}
        </button>
      )}
      {runs.slice(hid.length).map(line)}
    </div>
  );
}

/** Agents passing a message on paused (internal/server stopped: MaxHops in a row, or MaxIdle
 *  turns of words alone; kumo, 2026-11-26), and 继续 for one of its people: the count begins
 *  anew and the message is passed on, once (one gone on with already: the key goes). */
function PausedLine({ m, channel, may }: { m: Msg; channel: string; may?: boolean }) {
  useLang();
  const [state, setState] = useState<"" | "going" | "done">("");
  const e = m.event!;
  const words = t(e.reason === "idle" ? "channel.event.pausedIdle" : "channel.event.paused", { n: e.count ?? 0, seq: e.seq ?? "" });
  const go = () => {
    setState("going");
    api.goOn(channel, e.seq ?? 0).then(
      () => setState("done"),
      (err) => (setState("done"), err instanceof ApiError && err.status === 409 ? undefined : toast(errText(err), { error: true })),
    );
  };
  const key = may && state !== "done" && (
    <button type="button" disabled={state === "going"} onClick={go} className="g-press shrink-0 rounded-sm px-1 font-medium text-ink-2 hover:text-ink disabled:opacity-50">
      {t("channel.goOn")}
    </button>
  );
  return <Line tail={key || undefined}>{words}</Line>;
}

/** A line of Iridium's own: what happened to the channel (who joined, left, was renamed, what one
 *  receives, its people), in the page's words, the agent in it as its nick is drawn elsewhere;
 *  one with no event (an older line), its own text. also: more of the same kind after it, their
 *  agents named with its own. */
function EventLine({ m, also = [], channel }: { m: Msg; also?: Msg[]; channel: string }) {
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
      {parts.map((p, i) => (typeof p === "string" ? p : "nick" in p ? [p.nick, ...also.map((x) => x.event?.nick ?? "")].map((nick, j) => (j ? [t("channel.event.sep"), <NickLink key={`${i}.${j}`} nick={nick} />] : <NickLink key={i} nick={nick} />)) : "recv" in p ? whole(p.recv) : "on" in p ? onWhole(p.on) : p.people.map((x) => personName(x, apps, d?.logins ?? [])).join(t("channel.event.sep"))))}
    </Line>
  );
}
