// A session's chat (plan "网页", web/sample): a frosted top bar, the conversation by source, newest at
// the bottom and paged back as you scroll up, the agent's words as it writes them, and the bottom
// stack: the queue, the confirmation cards, the composer, the mode and model keys. Its blocks come
// from the transcript (GET messages, after= what the open session's state says was written); its
// drafts from the stream. Loaded only once a chat opens or the page is idle. One page, one agent
// or several (kumo, 2026-11-15: 会话页不换页，多一个 agent 只是多一个成员): once its channel has
// another agent, the others' words go in their places among its own (channel/side.ts, merge.ts),
// their at-work frames under the words that called them; its subagents at work under the steps
// that started them.
import { Menu } from "@base-ui/react/menu";
import { Popover } from "@base-ui/react/popover";
import { BarChart3, Ellipsis, FolderOpen, Globe, ListTree, Plus, Settings } from "lucide-react";
import type { Block, Status } from "../api/types.gen";
import { lazy, Suspense, useEffect, useLayoutEffect, useMemo, useRef, useState, type ComponentProps, type DragEvent, type ReactNode } from "react";
import type { SessionState as LiveSession } from "../api/types.gen";
import { api } from "../api/client";
import { LinkDot } from "../link";
import type { Session } from "../api/types.gen";
import { WsPlus } from "../create";
import { born, chatOf, compactSent, firstScreen, fresh, isWhole, lineOf, stateAhead, type Draft as Drafted, type Sent } from "../chats";
import { LOCAL_ME, useMachines, useMe, useWhoList, useWorkspaces } from "../data";
import { t, useLang } from "../i18n";
import { pushIn } from "../motion";
import { openChat, openUsage } from "../route";
import { PULL } from "../sheet";
import { MenuItems, Rename, sessionActions, sessionTags } from "../sidebar";
import { useConfirms, useList, useSession } from "../store";
import { drafts, opened } from "../stream";
import { errText, toast } from "../toast";
import { groupPath, working } from "../tree";
import { agentName, cn, Lamp, lampOf, Leaving, LogoLamp, LogoStack, PopSurface, sessionStatusText, tilde, Tip, titleOf, untitled } from "../ui";
import { givenTurn, groupMessages, isMulti, type AgentRow, type GivenTurn, type Msg } from "./channel/lib";
import { betweenAgents, callerOf, foreignOf, givenHere, held, HOLD, linesOf, marksAt, named as namedIn, ownAnchors, slotsOf, subsAt, type Anchor, type RowTime } from "./channel/merge";
import { LongWords } from "./longwords";
import { dispatched } from "./dispatch";
import { Dispatching, SubAtWork } from "./subwork";
import { alignBack, loadParts, sideReady, useSide } from "./channel/side";
import { relays, type Relay } from "../satori";
import { AgentFace } from "./channel/members";
import { PersonCard, PersonMenu, useNamed } from "./channel/person";
import { Nicks } from "./channel/nick";
import { closeEdit, useEditing } from "./channel/editing";
import { EditBody } from "./channel/edit";
import { ChannelCard } from "./channel/channelcard";
import { CARD_POP } from "./channel/profilecard";
import { Face } from "./channel/face";
import { mayAct, useMembers } from "./channel/gate";
// A channel's parts, fetched apart: ⋯'s 编辑资料 on a phone (ChannelMenu), those looking and the
// members at the bar's right (TopRight), the line in the composer's place for one not joined.
const ChannelMenu = lazy(() => import("./channel/top"));
// 编辑资料, the page drawn over the chat while a card is being changed (channel/edit.tsx).
const TopRight = lazy(() => import("./channel/top").then((m) => ({ default: m.TopRight })));
const NotJoined = lazy(() => import("./channel/key").then((m) => ({ default: m.NotJoined })));
// A channel of several agents: its own code, fetched as one is first opened.
const ChannelPage = lazy(() => import("./channel/page").then((m) => ({ default: m.ChannelPage })));
import { MachineFace, shownPath } from "../machines";
import { openPlace } from "../places";
import { Asks, Denials } from "./asks";
import { renameOnly } from "./cmd";
import { Composer, type ComposerHandle } from "./composer";
import { folded, type Tool } from "./fold";
import { StopKey, ToolDraft, Waiting } from "./live";
import { Draft } from "./markdown";
import { ContextRing, ModeKey, ModelKey, modelView, QuotaBars, useMode } from "./meters";
import { PublicLink } from "./publiclink";
import { Queue } from "./queue";
import { ShellRuns } from "./shell";
import { Crash } from "./crash";
import { placesIn, PLACE_DRAG, SESSION_DRAG, sourceOf, yours } from "./refs";
import { placeDropped, type NamedPlace } from "./at";
import { PlaceIcon, placeOf } from "./mention";
import { spotsOf, type Spot } from "./where";
import { Compaction, Earlier, Pending, Relayed, Running, Said, useWhenRow, When } from "./said";
import { turnsOf, type Row } from "./turns";
import { Stick, toEnd } from "./stick";
import { VoiceDock } from "../voice/dock";

const BUSY = new Set(["running", "compacting", "starting", "waiting"]);
/** Another agent of the channel at work: its frame under the conversation. */
// At work: in a turn. Not "starting" (its harness coming up, no turn yet: an invited agent's face
// flashed up as if to speak and went; kumo, 2026-11-16).
const WORKING = new Set(["running", "compacting"]);
/** A turn is on: at work, or waiting on an ask. Waiting only on a step auto mode denied, the turn is
 *  over (session/release.go): no stop key, no activity row. */
const turning = (s: { status: string; asks: unknown[]; denials?: unknown[] }) => BUSY.has(s.status) && !(s.status === "waiting" && !s.asks.length && !!s.denials?.length);

/** The bottom stack's height as last measured: a chat opens padded by it, not by a guess and then
 *  moved (one frame drawn short or long, the conversation jumping after). */
let lastStackH = 140;

/** A session's state before the stream (or chats.ts stateAhead) brings it, from its row: the chat is
 *  drawn whole at once, its composer and conversation, its keys after (ghost). */
const ghostOf = (row: Session): LiveSession => ({ ...row, asks: [], tasks: [], denials: [], queue: [], end: 0, proc: -1 });

/** sheet: on a phone, the sidebar's sheet is out this tall (sheet.tsx): the bottom stack sinks out
 *  under it; the conversation stays where it is, not pushed up with it (the user, 2026-11-06). onSheet: on a phone, brings it out,
 *  from the bar's ⋯ menu; onMe, there too, this terminal's own sheet (个人). */
export function ChatPage({ id, sheet = 0, onSheet, onMe }: { id: string; sheet?: number; onSheet?: () => void; onMe?: () => void }) {
  const list = useList();
  const row = list?.find((x) => x.id === id);
  // A session's page is this chat, one agent or several (kumo, 2026-11-15: 会话页不换页，多一个
  // agent 只是多一个成员): another agent is one more member, its words in their place among the
  // own agent's. A channel that is no session's (no agent of its own): the channel's page.
  if (row?.bare)
    return (
      <>
        <Suspense
          fallback={
            <div className="relative h-full">
              <Bar s={row} onSheet={onSheet} onMe={onMe} />
            </div>
          }
        >
          <ChannelPage key="channel" id={id} sheet={sheet} record={openChat} bar={(s) => <Bar s={s} onSheet={onSheet} onMe={onMe} />} />
        </Suspense>
        </>
    );
  return (
    <>
      <OneChat key="one" id={id} sheet={sheet} onSheet={onSheet} onMe={onMe} />
    </>
  );
}



/** A session's chat, drawn once its first screen is in (chats.ts firstScreen: its state, its
 *  record, its members, its channel's side, in one answer): whole at once, not its record and
 *  then the others' words and their work as each comes; with another agent in its channel, the
 *  code that draws the others too. Until then its bar alone; one seen before draws at once. */
function OneChat(p: { id: string; sheet?: number; onSheet?: () => void; onMe?: () => void }) {
  const [whole, setWhole] = useState(() => isWhole(p.id) && sideReady(p.id));
  const row = useList()?.find((x) => x.id === p.id);
  useEffect(() => {
    if (whole) return;
    let live = true;
    // The others' code with it, when the list says already there are others.
    if (isMulti(row)) void loadParts().catch(() => {});
    void (isWhole(p.id) ? Promise.resolve() : firstScreen(p.id))
      .then(() => (sideReady(p.id) ? undefined : loadParts()))
      .then(() => live && setWhole(true), () => live && setWhole(true));
    return () => void (live = false);
  }, [p.id]);
  if (!whole)
    return (
      <div className="relative h-full">
        <Bar s={row} onSheet={p.onSheet} onMe={p.onMe} />
      </div>
    );
  return <WholeChat {...p} />;
}

/** A session's chat: its own agent's record, and, once its channel has another agent, what the
 *  others say in their places among it (chat/channel/side.ts, merge.ts). */
function WholeChat({ id, sheet = 0, onSheet, onMe }: { id: string; sheet?: number; onSheet?: () => void; onMe?: () => void }) {
  useLang();
  const real = useSession(id);
  const list = useList();
  const row = list?.find((x) => x.id === id);
  const ghost = !real && !!row;
  const ghostS = useMemo(() => row && ghostOf(row), [row]);
  const s = real ?? ghostS;
  // Gone only once asked and not there, or there and then not: a session just made is in no list
  // yet, and is not to show "no such session" as it opens.
  const [lost, setLost] = useState(false);
  useEffect(() => {
    if (!real) void stateAhead(id).then((there) => !there && setLost(true));
  }, [id]);
  const had = useRef(false);
  if (real) had.current = true;
  else if (had.current && !lost) setLost(true);
  if (real && lost) setLost(false);
  const [chat] = useState(() => chatOf(id));
  const log = chat.log;
  const [, redraw] = useState(0);
  const changed = () => redraw((n) => n + 1);
  const me = useMe().data;
  const terminals = useWhoList();
  const workspaces = useWorkspaces().data;
  const confirms = useConfirms();
  // Its channel's members (docs/channels.md; their key is at the bar's right): one not of its
  // people reads, and neither speaks nor answers.
  const members = useMembers(id);
  const may = mayAct(members);
  // Its channel, once it has another agent: the log, the others' states, the reads (side.ts).
  // Its agents as the list has them; before the list came, as its members do.
  const fromMembers = useMemo(() => (members?.members ?? []).flatMap((m) => (m.session ? [{ session: m.session, ...(m.agent ? { agent: m.agent } : {}), ...(m.nick ? { nick: m.nick } : {}) }] : [])), [members]);
  const chAgents = row ? row.agents : fromMembers;
  const side = useSide(id, chAgents ? { agents: chAgents } : undefined, may);

  // The newest page, or what was kept; then what came since.
  useEffect(() => {
    let live = true;
    const more = () => void (chat.coming ?? log.newer()).then(() => live && changed(), () => {});
    // Read just now (its first screen, or ahead of the turn to it): not again.
    if (chat.coming || !fresh(log.came)) more();
    const hear = (d: { session: string }) => d.session === id && (log.wrote(d as never), changed());
    const back = () => (log.dropDrafts(), more());
    drafts.add(hear);
    opened.add(back);
    return () => {
      live = false;
      drafts.delete(hear);
      opened.delete(back);
    };
  }, [id]);
  // The transcript grew past what is shown: the blocks since, from the state's tail when it has
  // them, else fetched. Also as the first page comes: it may have been read before what the state
  // says was written (a message sent as the chat opened), and nothing else asks again.
  const end = s?.end ?? 0;
  useEffect(() => {
    if (!log.loaded || end <= log.end) return;
    if (!log.fromTail(s?.tail)) return void log.newer().then(changed, () => {});
    changed();
  }, [end, log.loaded]);
  useEffect(() => void (real && log.procIs(real.proc), changed()), [real?.proc]);
  // The turn ended: what was drafted is in the transcript now. Only those drafted by then go: the
  // next turn (queued, sent at once) may begin to write while the page fetches.
  const busy = !!s && turning(s);
  const wasBusy = useRef(busy);
  useEffect(() => {
    if (wasBusy.current && !busy) {
      const upTo = log.mark();
      // A /compact that wrote no compaction (it had nothing to compact) is done too.
      void log.newer().then(() => (log.dropDrafts(upTo), (log.sent = log.sent.filter((x) => x.failed !== undefined || !compactSent(x))), changed()), () => {});
    }
    wasBusy.current = busy;
  }, [busy]);
  // Compacting, or a /compact on its way: the compaction's line, since when.
  const compacting = s?.status === "compacting" || log.sent.some((x) => x.failed === undefined && compactSent(x));
  // Counted from when the agent began (the daemon's time), not from when this page saw it.
  const liveTurn = useRef("");
  const compactSince = useRef(0);
  if (!compacting) compactSince.current = 0;
  else if (s?.compactingSince) compactSince.current = s.compactingSince;
  else if (!compactSince.current) compactSince.current = Date.now();
  // Its agent cleared itself (Claude's own /clear): the conversation goes on in another session,
  // and the page with it, as after Iridium's /clear. Only as it happens here, not on opening one
  // that went on before.
  const seenNext = useRef<{ id: string; next?: string } | null>(null);
  useEffect(() => {
    if (!s) return;
    const was = seenNext.current;
    seenNext.current = { id, next: s.next };
    if (was?.id === id && s.next && s.next !== was.next) openChat(s.next);
  }, [id, !!s, s?.next]);

  // Who sent what; this panel as /v1/internal/me has it (its name, avatar, device).
  const self = me ?? LOCAL_ME;
  const source = (b: { from?: string; fromTerminal?: string }) => sourceOf(b.from, b.fromTerminal, self, terminals, list ?? [], workspaces ?? [], s);

  // The bottom stack's height pads the conversation's end.
  const stack = useRef<HTMLDivElement>(null);
  const [stackH, setStackH] = useState(lastStackH);
  useLayoutEffect(() => {
    const el = stack.current;
    if (!el) return;
    // Measured now, before the first paint, then as it changes.
    const measure = () => setStackH((lastStackH = el.offsetHeight));
    measure();
    const ro = new ResizeObserver(measure);
    ro.observe(el);
    return () => ro.disconnect();
  }, [!!s]); // the stack is drawn only once the session or its row has come

  // A message sent: at the bottom, the conversation pushed up by it (the chat's glide) as it fades in.
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

  const composer = useRef<ComposerHandle>(null);
  // A note put into this chat (notes.tsx putIntoSession): its words in the composer, on a line of
  // their own; the event cancelled says it was taken.
  useEffect(() => {
    const put = (e: Event) => {
      if (!composer.current) return;
      composer.current.put((e as CustomEvent<string>).detail);
      e.preventDefault();
    };
    addEventListener("ir:note", put);
    return () => removeEventListener("ir:note", put);
  }, []);
  const [modelOpen, setModelOpen] = useState(false);
  const [modeOpen, setModeOpen] = useState(false);
  // The context's popover, at the ring after the model name of the agent's latest turn (/context
  // opens it).
  const [ctxOpen, setCtxOpen] = useState(false);
  const shown = folded(log.blocks);
  const cwd = s?.cwd ?? row?.cwd ?? "";
  // 在哪干活 under the title (plan, 2026-11-10): the places this one sent sessions to, and those its
  // messages name (as far as they are loaded), as the page has them.
  const machines = useMachines();
  const said = `${log.blocks.length}.${log.blocks[0]?.at}.${log.blocks.at(-1)?.at}.${log.sent.length}`;
  const named = useMemo(() => {
    const texts = [...log.blocks.filter((b) => b.kind === "user").map((b) => b.text ?? ""), ...log.sent.map((x) => x.text)];
    const out: NamedPlace[] = [];
    for (const p of texts.flatMap(placesIn)) {
      const at = placeOf(p, machines);
      if (at && !out.some((o) => o.machine === at.machine && o.path === at.path)) out.push({ ...at, name: p.name });
    }
    return out;
  }, [said, machines]);
  const spots = useMemo(() => (row ? spotsOf(row, list ?? [], named, workspaces ?? []) : []), [row, list, named, workspaces]);
  // The bar a row taller with them: the conversation padded as much, and kept where it was as the
  // row comes or goes (the bar's 48px and a gap; the row's 32px).
  const padTop = 64 + (spots.length ? 32 : 0);
  const padWas = useRef(padTop);
  useLayoutEffect(() => {
    const d = padTop - padWas.current;
    padWas.current = padTop;
    if (d && scroller.current) scroller.current.scrollTop += d;
  }, [padTop]);

  const onSent = (sent: Sent) => {
    sent.from = log.end;
    log.sent.push(sent);
    fly.current = sent.key;
    changed();
  };
  // Again under its id: one that went after all is not sent twice.
  const retry = async (sent: Sent) => {
    sent.failed = undefined;
    changed();
    try {
      const r = await api.send(id, sent.text, sent.files.map((f) => f.ref), false, sent.input);
      sent.input = r.input || sent.input;
      sent.queued = r.queued || r.command;
    } catch (e) {
      sent.failed = errText(e);
    }
    sentDone(sent);
  };
  // Answered: queued, it is the queue's; else its block may have come before the answer did.
  const sentDone = (x: Sent) => {
    log.sent = log.sent.filter((y) => y !== x || !x.queued);
    log.recheck();
    changed();
  };
  const files = (e: DragEvent) => e.dataTransfer.types.includes("Files") && (e.preventDefault(), true);
  // A session's row dropped here (sidebar.tsx): named in the composer, for this one to write to it
  // with ir (the user, 2026-11-06): a card among the attachments, one piece (the user, 2026-11-08).
  const session = (e: DragEvent) => e.dataTransfer.types.includes(SESSION_DRAG) && (e.preventDefault(), (e.dataTransfer.dropEffect = "copy"), true);
  // A group's or a folder's row (2026-11-10): that place named, as @ names it.
  const place = (e: DragEvent) => e.dataTransfer.types.includes(PLACE_DRAG) && (e.preventDefault(), (e.dataTransfer.dropEffect = "copy"), true);
  const drop = (e: DragEvent) => {
    if (files(e)) return composer.current?.attach([...e.dataTransfer.files]);
    if (place(e)) {
      const p = placeDropped(e.dataTransfer.getData(PLACE_DRAG));
      return p && composer.current?.name(p);
    }
    if (!session(e)) return;
    const to = list?.find((x) => x.id === e.dataTransfer.getData(SESSION_DRAG));
    if (!to || to.id === id) return;
    composer.current?.name(to.id);
  };

  // The agent as its face shows it (the user, 2026-11-13: both sides have a face from the start,
  // not only once a second agent is in): its nick and what it runs as from the members.
  const mine = members?.members.find((m) => m.session === id);
  const faceRow: AgentRow | undefined = s && { session: id, member: `agent:${id}`, agent: s.agent, nick: mine?.nick ?? "", name: mine?.name ?? agentName(s.agent), status: lampOf(s), recv: mine?.recv ?? "all" };
  // 发消息 on its own agent's card: the composer here.
  const talk = () => composer.current?.focus();
  // Its members as words name them and as the composer's @ lists them (agents, people and apps
  // are peers): the agent by its nick, the people of its channel.
  // The channel's agents, its own as its face has it, once it has another; else its own alone.
  const agents: AgentRow[] = !faceRow
    ? []
    : !side.on
      ? [faceRow]
      : [
          faceRow,
          ...(chAgents ?? []).flatMap((a): AgentRow[] => {
            if (a.session === id) return [];
            const m = members?.members.find((x) => x.session === a.session);
            const of = side.states[side.sessions.indexOf(a.session)] ?? list?.find((x) => x.id === a.session);
            const agent = of?.agent ?? m?.agent ?? a.agent ?? "claude";
            return [{ session: a.session, member: `agent:${a.session}`, agent, nick: m?.nick ?? a.nick ?? "", name: m?.name ?? agentName(agent), status: of ? lampOf(of) : (m?.status ?? "idle"), recv: m?.recv ?? "at" }];
          }),
        ];
  const stateOf = (session: string): LiveSession | undefined => (session === id ? s : side.states[side.sessions.indexOf(session)]);
  const callAt = (name: string) => composer.current?.insert(`@${name}`);
  const { named: nicks, others } = useNamed({ channel: id, agents: agents.filter((a) => a.nick), members, may, call: callAt, talk });
  const atRows = useMemo(() => [...nicks.agents, ...others], [nicks, others]);
  // The message being answered (<quote>), over the composer till it is sent.
  const [quote, setQuote] = useState<Msg | null>(null);
  // The others at work, and since when this page has seen each at work: one called is shown at
  // work only once what called it is on the page (merge.ts held), at most HOLD after.
  const othersAt = agents.filter((a) => a.session !== id && WORKING.has(stateOf(a.session)?.status ?? ""));
  const atSince = useRef(new Map<string, number>());
  for (const a of othersAt) if (!atSince.current.has(a.session)) atSince.current.set(a.session, Date.now());
  for (const k of [...atSince.current.keys()]) if (!othersAt.some((a) => a.session === k)) atSince.current.delete(k);
  const [, tick] = useState(0);
  const atKey = othersAt.map((a) => a.session).join(" ");
  useEffect(() => {
    if (!atKey) return;
    const timer = setTimeout(() => tick((n) => n + 1), HOLD);
    return () => clearTimeout(timer);
  }, [atKey]);
  // The log back as far as the record is loaded: no gap of the others' words at its top.
  const firstTime = log.blocks[0]?.time;
  useEffect(() => {
    const kept = side.ready?.kept;
    if (kept && firstTime !== undefined) void alignBack(kept.log, log.more ? firstTime : 0).then((any) => any && changed());
  }, [!!side.ready, firstTime]);

  if (!s || !faceRow) {
    const gone = lost && list !== undefined && !row;
    return (
      <div className="relative h-full">
        <Bar s={row} onSheet={onSheet} onMe={onMe} />
        {gone && <p className="g-fade m-0 pt-24 text-center text-sm text-ink-3">{t("chat.gone")}</p>}
      </div>
    );
  }
  const elsewhere = s.status === "elsewhere";
  const face = faceRow;
  // Its lamp only on the face of the turn that is on.
  const resting: AgentRow = { ...face, status: "idle" };
  // Another person's face opens their card (people and agents are peers, the user, 2026-11-13):
  // their Satori user id, a login's (an app's) or the panel login's.
  // Your own face the same card as others see (kumo, 2026-11-16: not the settings): no 私聊, no
  // 提及; 编辑资料 in it.
  const personCard = (b: Block) => {
    const user = b.fromLogin || b.fromTerminal;
    if (!user) return undefined;
    const src = source(b);
    const own = user === self.id || (self.host && user === "local");
    return <PersonCard user={user} name={b.from || user} avatar={src.kind === "you" ? src.avatar : undefined} dm={!own} call={may && !own ? (name) => composer.current?.insert(`@${name}`) : undefined} />;
  };
  // Another person's face's menu (kumo, 2026-11-16): 提及, 私聊, as the card has them.
  const personMenu = (b: Block) => {
    const user = b.fromLogin || b.fromTerminal;
    if (!user || user === self.id || (self.host && user === "local")) return undefined;
    const name = b.from || user;
    return (at: { x: number; y: number } | null, close: () => void) => <PersonMenu user={user} name={name} call={may ? (n) => composer.current?.insert(`@${n}`) : undefined} at={at} close={close} />;
  };
  // Another login's words on the left, with the agents (kumo, 2026-11-16: every login was on the
  // right). The machine's own (typed at its terminal, its ir, no sender) stay the owner's.
  const othersWords = (b: Block) => {
    const user = b.fromLogin || b.fromTerminal;
    if (!user || user === self.id || user === "local") return false;
    const src = source(b);
    return src.kind === "you" && !src.local;
  };
  // When each of the agent's rows was written: its turn's head says the first's.
  const times = new Map<string, number>();
  const began = (rs: { key: string }[]) => rs.map((r) => times.get(r.key)).find(Boolean);
  const ready = side.ready;
  // When each row was written, for the others' words to go in their places (merge.ts slotsOf):
  // steps folded have no time of their own (they go with the words after them), only since when
  // (a turn still at its steps has no words yet: what called it stays above them).
  let rowTimes: RowTime[] = [];
  const sent = dispatched(shown);
  // The steps each row holds: a subagent at work goes under the call that started it.
  const stepsOf: { key: string; steps: { id?: string; name?: string; input?: string }[] }[] = [];
  // The own agent's words by row, for the fold of words between agents.
  const wordsOf = new Map<string, string>();
  let rows: Row<ReactNode, Works>[] = (busy ? shown.filter((b) => b.kind !== "running") : shown).flatMap((b) => {
    // A call from another channel it is in (chat/channel/lib givenTurn): Iridium's message on
    // the left, what follows its work there (Relayed).
    // As the record says it, a Satori forward (satori.ts relays); else read from its words.
    const relay = b.kind === "user" ? relays.get(b) : undefined;
    // Its own channel's words forwarded to it: drawn from the log, by who said them.
    if (ready && b.kind === "user" && givenHere(id, relay, b.text)) return [];
    rowTimes.push(b.kind === "fold" ? { since: b.steps[0]?.time } : b.kind === "running" ? { since: b.tool.time } : b.time);
    stepsOf.push({ key: b.at, steps: b.kind === "fold" ? b.steps.map((x) => ({ id: x.id, name: x.name, input: x.input })) : [] });
    if (b.kind === "assistant") wordsOf.set(b.at, b.text ?? "");
    const given: GivenTurn | null = relay ? { seq: relay.seq, words: relay.msgs.at(-1)?.text ?? "", context: relay.context, ...(relay.channel !== id ? { channel: { id: relay.channel, name: relay.name } } : {}) } : b.kind === "user" ? givenTurn(b.text ?? "") : null;
    const time = b.kind === "user" ? b.time : undefined;
    if (b.kind !== "user" && "time" in b && b.time) times.set(b.at, b.time);
    return [
      {
        key: b.at,
        agent: b.kind === "assistant" || b.kind === "fold" || b.kind === "running",
        ...(given?.channel ? { works: { given, relay, time } } : {}),
        node: <Said key={b.at} b={b} session={id} cwd={cwd} source={source} onJump={openChat} card={personCard} menu={personMenu} left={othersWords} />,
      },
    ];
  });
  for (const d of log.drafts) {
    if (d.tool && (busy || renamesDraft(d))) continue;
    const key = `${d.proc}.${d.n}`;
    rowTimes.push(Infinity);
    rows.push({
      key,
      agent: true,
      node: d.tool ? (
        <ToolDraft key={key} name={d.tool.name} input={d.text} whole />
      ) : (
        <div key={key} className="g-fade min-w-0 pr-6">
          <Draft text={d.text} live={!d.whole && busy} />
        </div>
      ),
    });
  }
  // Its subagents at work (kumo, 2026-11-15: members that only read, no rows in the sidebar):
  // each under the step that started it, coming in as one called does; those whose step is not
  // shown, at the end.
  const subs = (list ?? []).filter((x) => x.subagent && x.parent === id && working(x, row, Date.now()));
  const placed = subsAt(stepsOf, subs);
  if (placed.at.size) {
    const r2: typeof rows = [];
    const t2: typeof rowTimes = [];
    rows.forEach((r, i) => {
      r2.push(r);
      t2.push(rowTimes[i]);
      for (const x of placed.at.get(r.key) ?? []) {
        r2.push({ key: `sub:${x.id}`, agent: true, node: <div key={`sub:${x.id}`} className="g-arrive"><div><SubAtWork s={x} /></div></div> });
        t2.push(undefined);
      }
    });
    rows = r2;
    rowTimes = t2;
  }
  // The others' words (once its channel has another agent): each where it was said among the
  // own agent's rows, a speaker's in a row under one head (merge.ts); the read marks under the
  // last row each member has read, the own rows known by the log's numbers.
  const msgs = ready?.kept.log.msgs ?? [];
  const P = ready?.parts;
  const who = P && P.personWho(self, terminals, list ?? [], workspaces ?? []);
  const nameOfMsg = (m: Msg) => (m.author.kind === "agent" ? m.author.nick || m.author.name : m.author.kind === "person" ? m.author.name : "Iridium");
  const quoted = (n: number) => {
    const m = msgs.find((x) => x.n === n);
    return m ? { who: nameOfMsg(m), text: m.text } : undefined;
  };
  const quoting = (m: Msg) => (setQuote(m), composer.current?.focus());
  const barsOf = (session: string) => {
    const a = agents.find((x) => x.session === session);
    return a && P ? <P.AgentBars a={a} st={stateOf(session)} /> : null;
  };
  let merged = rows;
  const groupAt = new Map<string, ReturnType<typeof groupMessages>[number]>();
  const nickSet = new Set(agents.map((a) => a.nick.toLowerCase()).filter(Boolean));
  const anchors: Anchor[] = [];
  const shownSeq = new Set<number>();
  let lastGroup: { session?: string } | undefined;
  if (ready && P && who) {
    const inputs = new Set(log.blocks.flatMap((b) => (b.kind === "user" && b.id ? [b.id] : [])));
    const foreign = foreignOf(msgs, id, inputs, log.more ? log.blocks[0]?.time : undefined);
    const slots = slotsOf(rowTimes, foreign);
    merged = [];
    for (let i = 0; i <= rows.length; i++) {
      for (const g of groupMessages(slots.get(i) ?? [])) {
        const key = `ch:${g.key}`;
        anchors.push({ key, seq: g.msgs.at(-1)!.n });
        for (const m of g.msgs) shownSeq.add(m.n);
        merged.push({ key, agent: false, node: <P.GroupView key={key} g={g} channel={id} agents={agents} who={who} record={openChat} call={callAt} may={may} quote={may ? quoting : undefined} quoted={quoted} bars={barsOf} fold={(m) => betweenAgents(m, msgs, nickSet)} /> });
        groupAt.set(key, g);
        if (i === rows.length) lastGroup = g.author.kind === "agent" ? { session: g.author.session } : {};
      }
      if (i < rows.length) {
        merged.push(rows[i]!);
        lastGroup = undefined;
      }
    }
    for (const a of ownAnchors(msgs, id, shown.map((b) => ({ key: b.at, line: lineOf(b), ...(b.kind === "user" && b.id ? { id: b.id } : {}) })))) {
      anchors.push(a);
      shownSeq.add(a.seq);
    }
  }
  // The own agent's words in a stretch another agent began (its words to it, naming it and no
  // person): words between agents, folded when long; a person's words end such a stretch.
  if (ready && face.nick) {
    let byAgent = false;
    const me = new Set([face.nick.toLowerCase()]);
    merged = merged.map((r) => {
      if (!r.agent) {
        const g = groupAt.get(r.key);
        const last = g?.msgs.at(-1);
        byAgent = !!g && g.author.kind === "agent" && !!last && namedIn(last.text, me).agents > 0 && namedIn(last.text, nickSet).people === 0;
        return r;
      }
      const w = wordsOf.get(r.key);
      return byAgent && w !== undefined ? { ...r, node: <LongWords key={r.key} fold lines={linesOf(w)}>{r.node}</LongWords> } : r;
    });
  }
  const marks = ready ? marksAt(anchors, side.reads?.readers ?? [], members?.me) : new Map<string, never[]>();
  const turns = turnsOf(merged);
  // Its subagents as people (subwork.tsx): which call, which notice is whose, who dispatched them.
  const dispatching = { parent: id, by: face.nick || face.name, d: sent };
  const marksOf = (keys: string[]) => keys.flatMap((k) => marks.get(k) ?? []);
  // What of yours the log has said already (sent to the channel, not given to its own agent):
  // drawn from the log, no bubble of its own.
  const inLog = (x: Sent) => !!ready && x.failed === undefined && msgs.some((m) => (!!x.seq && m.n === x.seq) || (!!x.input && m.input === x.input));
  const pending = log.sent.filter((x) => (x.failed !== undefined || !compactSent(x)) && !inLog(x));
  // The others at work, each once what called it is on the page (the hand-over, merge.ts held).
  const ownWriting = busy && log.drafts.some((d) => !d.tool && !d.whole);
  const now = Date.now();
  const atWork = othersAt.filter((a) => !held({ since: atSince.current.get(a.session) ?? now, called: callerOf(msgs, a.nick, a.session) }, (n) => shownSeq.has(n), ownWriting, now));
  // A turn begun with nothing of the agent's under its face yet (what you sent last, or nothing
  // since): its face and name come first, the first call and the dots under them (kumo,
  // 2026-11-15), not in a row of their own.
  const pendingShown = pending.length > 0;
  const headless = busy && !compacting && (turns.at(-1)?.kind !== "agent" || pendingShown);
  // Its usage after its model's name, a strip of its own as small as a phone's (the user,
  // 2026-11-13): the context's ring, the quota's bars; each pressed, its popover.
  // The agent's latest turn: the ring /context opens. None yet: the command is the agent's.
  const ctxTurn = turns.findLastIndex((x) => x.kind === "agent" || (x.kind === "works" && x.rows.length > 0));
  const openContext = () => ctxTurn >= 0 && !!s.context?.max && (setCtxOpen(true), true);
  const bars = (last: boolean) =>
    s && (
      <span className="flex shrink-0 items-center">
        <ContextRing session={s.id} summary={s.context} tokens={s.tokens} compact {...(last ? { open: ctxOpen, onOpenChange: setCtxOpen } : {})} />
        <QuotaBars agent={s.agent} machine={s.machine} live={s.live} using={[s.model, s.pick, modelView(s).name]} compact />
      </span>
    );
  // The channel as the list names it now: what the agent was given may be its id (not named
  // then) or an older name.
  const nameOf = (g: GivenTurn) => {
    const ch = list?.find((x) => x.id === g.channel?.id);
    return ch ? titleOf(ch) : (g.channel?.name ?? "");
  };
  // What changes as the conversation grows: it keeps to the bottom, the dots wait again.
  const grew = `${log.blocks.at(-1)?.at} ${log.drafts.length} ${log.drafts.at(-1)?.text.length} ${log.drafts.at(-1)?.whole} ${log.sent.length} ${ready?.kept.log.end ?? 0} ${atWork.map((a) => a.session).join(",")} ${subs.map((x) => x.id).join(",")}`;
  // The own agent's stop key, on its row at work; starting is no turn yet.
  const stop = may && s.status !== "starting" && !s.subagent && !elsewhere ? <StopKey session={id} esc /> : null;
  const activity = <Activity stop={stop} calls={log.drafts.filter((d) => d.tool && !renamesDraft(d))} running={shown.flatMap((b) => (b.kind === "running" ? [b.tool] : []))} cwd={cwd} quiet={s.status !== "waiting" && !log.drafts.some((d) => !d.whole && !d.tool)} grew={grew} under={headless} />;
  // A turn's rows: those before its last words under one line once it has ended (Earlier), when
  // they are more than one line already (words, or several rows).
  const stepsAt = new Map(stepsOf.map((x) => [x.key, x.steps.length]));
  // The turn at work as this page last saw it: once it ends, what it folds folds before you.
  // (none of its rows yet: the turn after the last row, keyed by it, turns.ts)
  const lastTurn = turns.at(-1);
  if (busy && lastTurn) liveTurn.current = lastTurn.kind === "one" ? `${lastTurn.key}:a` : lastTurn.key;
  const rowsOf = (rs: Row<ReactNode, Works>[], done: boolean, key: string) => {
    const last = rs.findLastIndex((r) => wordsOf.has(r.key));
    const before = last > 0 ? rs.slice(0, last) : [];
    const words = before.filter((r) => wordsOf.has(r.key)).length;
    if (!words && before.length < 2) return rs.map((r) => r.node);
    return [
      <Earlier key={`earlier:${rs[0]!.key}`} done={done} fresh={key === liveTurn.current} words={words} steps={before.reduce((n, r) => n + (stepsAt.get(r.key) ?? 0), 0)}>
        {before.map((r) => r.node)}
      </Earlier>,
      ...rs.slice(last).map((r) => r.node),
    ];
  };
  const turnNode = (x: (typeof turns)[number], i: number) =>
            x.kind === "one" ? (
              x.node
            ) : x.kind === "agent" ? (
              <AgentTurn key={x.key} channel={id} a={i === turns.length - 1 && !headless ? face : resting} may={may} bars={bars(i === ctxTurn)} talk={talk} time={began(x.rows)}>
                {rowsOf(x.rows, !(busy && i === turns.length - 1), x.key)}
              </AgentTurn>
            ) : (
              <Relayed key={x.key} given={x.works.given} relay={x.works.relay} name={nameOf(x.works.given)} time={x.works.time} onJump={openChat}>
                {x.rows.length > 0 && (
                  <AgentTurn channel={id} a={resting} may={may} bars={bars(i === ctxTurn)} talk={talk} time={began(x.rows)}>
                    {rowsOf(x.rows, !(busy && i === turns.length - 1), x.key)}
                  </AgentTurn>
                )}
              </Relayed>
            );
  return (
    <Nicks.Provider value={nicks}>
    <Dispatching.Provider value={dispatching}>
    <div className="relative h-full" data-chat-drop={id} onDragOver={(e) => files(e) || session(e) || place(e)} onDrop={drop}>
      <Stick scroller={scroller} tail={grew} older={() => log.older().then(async (any) => (ready && log.blocks[0]?.time !== undefined && (await alignBack(ready.kept.log, log.more ? log.blocks[0].time : 0)) && changed(), any))} place={chat.place} bottom={stackH} quiet={agents.some((a) => !!stateOf(a.session)?.asks.length)} style={{ paddingTop: padTop, paddingBottom: stackH + 20 }}>
        <div className="mx-auto flex max-w-(--chat-w) flex-col gap-5 px-4 sm:px-6">
          {/* In a turn, the calls being written or run and the dots share one row at the end, as
              high whatever it shows: the conversation does not jump as a call is drafted, runs and
              folds into the line above (the user, 2026-11-06). */}
          {turns.map((x, i) => {
            // Each turn in a box of its own, its read marks in the gap under it (over nothing:
            // coming late, they move nothing); the same box with none, so marks coming draw
            // nothing again.
            const rs = marksOf(x.kind === "one" ? [x.key] : [...(x.kind === "works" ? [x.key] : []), ...x.rows.map((r) => r.key)]);
            return (
              <div key={x.key} className="relative flex min-w-0 flex-col">
                {turnNode(x, i)}
                {rs.length > 0 && P && P.SHOW_READS && <P.ReadMarks readers={rs} agents={agents} />}
              </div>
            );
          })}
          {pending.map((x) => (
            <Pending
              key={x.key}
              s={x}
              who={yours(self)}
              bubbleRef={(el) => void (el ? bubbles.current.set(x.key, el) : bubbles.current.delete(x.key))}
              retry={() => retry(x)}
              drop={() => ((log.sent = log.sent.filter((y) => y !== x)), changed())}
            />
          ))}
          {/* The turn's row, after what you sent: kept, one line high, even out of a turn, so a
              message sent or the dots coming do not move the conversation (the user, 2026-11-06). */}
          {/* Close under what is above it (the user, 2026-11-06): a call's line by the calls before. */}
          {log.loaded && compacting ? (
            <div className="-mt-1 flex min-w-0 items-center">
              <div className="min-w-0 flex-1">
                <Compaction since={compactSince.current} />
              </div>
              {busy && stop}
            </div>
          ) : log.loaded && (busy ? (
            headless ? (
              <AgentTurn channel={id} a={face} may={may} bars={bars(false)} talk={talk}>
                {activity}
              </AgentTurn>
            ) : (
              activity
            )
          ) : s.denials?.length ? <Denials session={id} denials={s.denials} cwd={cwd} stuck={s.status === "waiting" && !s.asks.length} /> : <div aria-hidden className="-mt-3 h-7 pointer-coarse:h-9" />)}
          {/* The others at work (kumo, 2026-11-15): one called comes in under the words that
              called it, once they are whole (g-arrive: its height opens, then it rises in); under
              its own last words, the dots alone. */}
          {/* One that goes with nothing said (ir's interrupt of itself) fades and closes (Leaving). */}
          {P && (
            <Leaving
              items={atWork}
              keyOf={(a) => a.session}
              draw={(a, leaving) => {
                const alone = !pending.length && lastGroup?.session === a.session;
                return (
                  <div className={cn(!alone && !leaving && "g-arrive", "-mt-2")}>
                    <div>
                      <P.AtWork a={a} channel={id} alone={alone} quiet={alone ? `${a.session} ${ready?.kept.log.end}` : a.session} bars={barsOf(a.session)} call={may ? callAt : undefined} record={openChat} may={may} />
                    </div>
                  </div>
                );
              }}
            />
          )}
          {placed.rest.map((x) => (
            <div key={x.id} className="g-arrive -mt-2">
              <div>
                <SubAtWork s={x} />
              </div>
            </div>
          ))}
          {s.status === "error" && s.error && <Crash error={s.error} />}
        </div>
      </Stick>
      <Bar s={s} onSheet={onSheet} onMe={onMe} spots={spots} asked={confirms} />
      {/* Under the phone's sheet the stack stays where it is, covered, and is hidden once the sheet is
          up (its glass not drawn again as the chat moves under it); only one taller than the sheet,
          which would show over it or at its round corners, sinks (the user, 2026-11-06). */}
      <div ref={stack} {...{ [PULL]: "" }} data-covered={sheet > 0 || undefined} data-sunk={(sheet > 0 && stackH > sheet - 16) || undefined} inert={sheet > 0} className="g-turn-stay g-sink vt-stack absolute inset-x-0 bottom-0 z-10 px-2.5 pb-[max(14px,calc(env(safe-area-inset-bottom)+6px))] sm:px-5">
        {/* Paper from the composer's middle down: the conversation scrolled under it stops there. */}
        {!elsewhere && !s.subagent && <div aria-hidden className="pointer-events-none absolute inset-x-0 bottom-0 h-[calc(44px+max(14px,calc(env(safe-area-inset-bottom)+6px)))] bg-[linear-gradient(to_bottom,transparent,var(--paper)_14px)]" />}
        <div className="relative mx-auto max-w-(--chat-w)">
          {!s.subagent && !elsewhere && <ShellRuns s={s} />}
          <Queue session={id} queue={s.queue ?? []} steered={(q) => onSent({ key: crypto.randomUUID(), text: q.text, files: [], from: 0, input: q.id, steer: true })} />
          {P ? (
            // Several agents: the others' queues under their nicks, and all the asks as one queue,
            // the oldest open (docs/channels.md "提问").
            <>
              {agents.map((a) => {
                const st = stateOf(a.session);
                return a.session !== id && st?.queue?.length ? (
                  <P.Of key={a.session} a={a} off={!may}>
                    <Queue session={a.session} queue={st.queue} steered={() => {}} />
                  </P.Of>
                ) : null;
              })}
              {agents.map((a) => {
                const needs = confirms.filter((c) => c.session === a.session && c.kind !== "ask");
                return needs.length ? (
                  <P.Of key={a.session} a={a} off={!may}>
                    <Asks session={a.session} asks={[]} cwd={stateOf(a.session)?.cwd ?? cwd} confirms={needs} />
                  </P.Of>
                ) : null;
              })}
              <P.AskQueue agents={agents} states={agents.map((a) => stateOf(a.session))} cwd={cwd} off={!may} />
            </>
          ) : (
            <div inert={!may} className={cn(!may && "opacity-60")}>
              <Asks session={id} asks={s.asks} cwd={cwd} confirms={confirms.filter((c) => c.session === id && c.kind !== "ask")} />
            </div>
          )}
          {s.subagent ? (
            <div className="glass float rest flex h-12 items-center justify-center gap-1 rounded-[20px] text-sm text-ink-3">
              {t("chat.subagent")}
              {s.parent && (
                <button type="button" className="cursor-pointer text-ink-2 underline-offset-2 hover:underline" onClick={() => openChat(s.parent!)}>
                  {t("chat.subagentParent")}
                </button>
              )}
            </div>
          ) : elsewhere ? (
            <div className="glass float rest flex h-12 items-center justify-center rounded-[20px] text-sm text-ink-3">{t("chat.elsewhere")}</div>
          ) : !may ? (
            <Suspense fallback={<div className="h-12" />}>
              <NotJoined />
            </Suspense>
          ) : (
            <Under s={s} ghost={ghost} composer={composer} onSent={onSent} onSentDone={sentDone} modelOpen={modelOpen} setModelOpen={setModelOpen} modeOpen={modeOpen} setModeOpen={setModeOpen} openContext={openContext} people={atRows} quote={quote ? { n: quote.n, who: nameOfMsg(quote), text: quote.text } : undefined} unquote={() => setQuote(null)} />
          )}
        </div>
      </div>
    </div>
    </Dispatching.Provider>
    </Nicks.Provider>
  );
}

/** The composer, with in its foot the approval-mode key (back, its name alone, the user,
 *  2026-11-08) and the model key, then send. Its usage (the context's ring, the quota's bars) is
 *  after the agent's model name over its turns (the user, 2026-11-13), not here. */
/** people: its channel's members an @ lists (chat/channel/person.tsx useNamed). */
function Under({ s, ghost, composer, onSent, onSentDone, modelOpen, setModelOpen, modeOpen, setModeOpen, openContext, people, quote, unquote }: { s: LiveSession; ghost: boolean; composer: React.RefObject<ComposerHandle | null>; onSent: (x: Sent) => void; onSentDone: (x: Sent) => void; modelOpen: boolean; setModelOpen: (o: boolean) => void; modeOpen: boolean; setModeOpen: (o: boolean) => void; openContext: () => boolean; people: AgentRow[]; quote?: { n: number; who: string; text: string }; unquote?: () => void}) {
  const mode = useMode(s);
  // Made here just now (create.tsx useStart): its keys fade in, not the chat before's swapped for them.
  const [fresh] = useState(() => born.has(s.id));
  useEffect(() => void born.delete(s.id), [s.id]);
  // The model as the agent says it, once: while its process starts, or runs and has not said it
  // yet, the key waits, not the default's guess shown and then changed (默认模型, Opus, Haiku in
  // a second, the user, 2026-11-12). Not for ever: an agent that never says shows the guess.
  const modelKnown = useWaited(!!s.model || (!s.live && s.status !== "starting"), MODEL_WAIT);
  return (
    <>
      <Composer
        ref={composer}
        s={s}
        fresh={fresh}
        // Starting is no turn yet: what is sent goes once the agent is up, and Esc stops nothing.
        turn={turning(s) && s.status !== "starting"}
        onSent={onSent}
        onSentDone={onSentDone}
        cycleMode={mode.cycle}
        openModel={() => setModelOpen(true)}
        openMode={() => setModeOpen(true)}
        openUsage={() => openUsage(undefined, s.machine)}
        openContext={openContext}
        cleared={openChat}
        people={people}
        quote={quote}
        unquote={unquote}
        tools={
          // No mode, model or preset keys under the composer, one agent or several (kumo,
          // 2026-11-16: their being there at all was wrong): the agent's face's menu sets the mode
          // and the model, the lineup and the invite pick the preset. Still there, unseen, for
          // /mode and /model to open their menus where the keys were.
          <span aria-hidden className="invisible flex w-0 overflow-hidden">
            {!ghost && <ModeKey mode={mode} open={modeOpen} onOpenChange={setModeOpen} />}
            {!ghost && modelKnown && <ModelKey s={s} open={modelOpen} onOpenChange={setModelOpen} />}
          </span>
        }
      />
    </>
  );
}

/** How long the model key waits for the agent to say its model. */
const MODEL_WAIT = 3000;

/** ready, or ms after it was first not: what waits for something that may never come. */
function useWaited(ready: boolean, ms: number): boolean {
  const [over, setOver] = useState(false);
  useEffect(() => {
    if (ready || over) return;
    const t = setTimeout(() => setOver(true), ms);
    return () => clearTimeout(t);
  }, [ready, over, ms]);
  return ready || over;
}

/** The top bar: the logo with its lamp, the name (its folder is ⋯'s first row, which copies it);
 *  more. The link's dot is the bar's own, left of ⋯ (link.tsx). Under them, while
 *  there are any, the places it works in (spots, Where). */
function Bar({ s, onSheet, onMe, spots, asked }: { s?: Session; onSheet?: () => void; onMe?: () => void; spots?: Spot[]; asked?: { session?: string }[] }) {
  const list = useList();
  // A channel of several: its agents' logos, as its row in the list has them.
  const stack = s && isMulti(s) ? s.agents!.map((a) => list?.find((x) => x.id === a.session)?.agent ?? a.agent ?? s.agent) : undefined;
  const ws = useWorkspaces().data ?? [];
  const machines = useMachines();
  const [renaming, setRenaming] = useState(false);
  // ⋯'s "new session": the "+" of the session's workspace, its folder already this one's, under ⋯.
  const [making, setMaking] = useState(false);
  // ⋯'s 公开链接: its popover under ⋯.
  const [publicOpen, setPublicOpen] = useState(false);
  // Not there as the bar was first drawn: faded in as it comes.
  const late = useRef(!s).current ? "g-fade" : "";
  const more = useRef<HTMLButtonElement>(null);
  // The usage under ⋯, its right edge on ⋯'s, as the menu was (the user, 2026-11-08).
  const usageHere = () => {
    const b = more.current?.getBoundingClientRect();
    openUsage(b && { x: b.right, y: b.bottom + 6, end: true }, s?.machine);
  };
  // Its folder, ⋯'s first row (not the title's tooltip: a phone has no hover); pressed, copied.
  const folder = s ? (s.machine ? shownPath(groupPath(s), machines).real : tilde(s.cwd)) : "";
  const where = folder ? [{ icon: <FolderOpen size={15} />, label: folder, run: () => void navigator.clipboard.writeText(folder).then(() => toast(t("toast.copied")), () => {}) }] : [];
  // ⋯'s items: its folder; making, the sheet and 我 on a phone, usage; then the session's own.
  const lead = [...where, { icon: <Plus size={15} />, label: t("menu.new"), run: () => requestAnimationFrame(() => setMaking(true)) }, ...(onSheet ? [{ icon: <ListTree size={15} />, label: t("nav.sidebar"), run: onSheet }] : []), ...(onMe ? [{ icon: <Settings size={15} />, label: t("nav.me"), run: onMe }] : []), { icon: <BarChart3 size={15} />, label: t("usage.title"), run: usageHere }];
  const acts = s ? [...sessionActions(s, () => setRenaming(true)), { icon: <Globe size={15} />, label: t("public.menu"), run: () => requestAnimationFrame(() => setPublicOpen(true)) }] : [];
  return (
    <header className="g-turn-stay vt-bar glass absolute inset-x-0 top-0 z-20 shadow-[0_0.5px_0_var(--edge)]">
      <div className="flex h-12 items-center gap-1 pr-2 pl-2 pointer-coarse:h-13">
        <div className="flex min-w-0 flex-1 items-center gap-2.5 pl-2">
          {s && (
            <>
              <Tip label={stack ? s.agents!.map((a) => a.nick).join(" · ") : `${agentName(s.agent)} · ${sessionStatusText(s)}`} side="bottom">
                {/* On a phone the logo is a key too: the session list's sheet, as ⋯'s first item (the
                    user, 2026-11-06). Its tap area wider than it, the bar not moved. */}
                {onSheet ? (
                  <button type="button" aria-label={t("nav.sidebar")} onClick={onSheet} className={cn("g-press -m-2 grid place-items-center rounded-md p-2 hover:bg-fill", late)}>
                    {/* Iridium's mark, not the agent's (the user, 2026-11-06): it opens the list. */}
                    <LogoLamp agent={s.agent} status={lampOf(s)} size={18} ir />
                  </button>
                ) : (
                  <FaceKey s={s} stack={stack} className={late} />
                )}
              </Tip>
              {renaming ? (
                <span className="min-w-0 flex-1">
                  <Rename s={s} done={() => setRenaming(false)} className="h-7 font-semibold" />
                </span>
              ) : (
                <h1 className={cn("m-0 min-w-0 truncate text-base font-semibold", late, untitled(s) && "text-ink-3")} onDoubleClick={() => setRenaming(true)}>
                  {titleOf(s)}
                </h1>
              )}
            </>
          )}
        </div>
        {s && (
          <Suspense fallback={null}>
            <TopRight id={s.id} />
          </Suspense>
        )}
        {/* With the sidebar shown, the link's dot is at its top (sidebar.tsx). */}
        {onSheet && <LinkDot />}
        {/* ⋯'s place kept while the session comes: the dot beside it does not move as it does. */}
        {!s && <span aria-hidden className="size-8 shrink-0 pointer-coarse:size-10" />}
        {/* On a phone the voice this page is in stands here: there is no foot beside me. */}
        {onSheet && <VoiceDock side="bottom" />}
        {s && (
          <span className="relative grid">
            {/* Anchored on ⋯ itself, under it: clicks go through to ⋯. */}
            <WsPlus
              key={s.id}
              w={{ id: s.workspace ?? 0, name: ws.find((w) => w.id === s.workspace)?.name ?? "", path: groupPath(s) }}
              trigger={<span aria-hidden className="pointer-events-none absolute inset-0" />}
              open={making}
              setOpen={setMaking}
              opened={(id) => openChat(id)}
            />
            <Menu.Root>
              <Tip label={t("top.more")} side="bottom">
                <Menu.Trigger
                  render={
                    <button ref={more} type="button" aria-label={t("top.more")} className={cn("g-press grid size-8 place-items-center rounded-md text-ink-2 hover:bg-fill hover:text-ink data-popup-open:bg-fill pointer-coarse:size-10", late)}>
                      <Ellipsis size={17} />
                    </button>
                  }
                />
              </Tip>
              <Menu.Portal>
                <Menu.Positioner side="bottom" align="end" sideOffset={6} collisionPadding={8} className="z-50">
                  <MenuItems lead={lead} items={acts} tags={sessionTags(s)}>
                    <Suspense fallback={null}>
                      <ChannelMenu id={s.id} from={lead.length} face={!!onSheet} />
                    </Suspense>
                  </MenuItems>
                </Menu.Positioner>
              </Menu.Portal>
            </Menu.Root>
            <PublicLink key={`public:${s.id}`} session={s.id} anchor={more} open={publicOpen} onOpenChange={setPublicOpen} />
            {/* ⋯'s 编辑资料 (on a phone, where the title's face is Iridium's mark): out of ⋯. */}
            <EditAt id={s.id} anchor={more} />
          </span>
        )}
      </div>
      {!!spots?.length && <Where spots={spots} asked={asked ?? []} />}
    </header>
  );
}

/** 编辑资料 asked from a menu with no face to open out of (⋯ on a phone; editing.ts): the same
 *  as a card's, out of anchor. */
function EditAt({ id, anchor }: { id: string; anchor: React.RefObject<HTMLElement | null> }) {
  const editing = useEditing(id);
  // The last one asked stays drawn while it folds away.
  const last = useRef(editing);
  if (editing) last.current = editing;
  return (
    <Popover.Root open={!!editing} onOpenChange={(o) => !o && closeEdit()}>
      <PopSurface anchor={anchor.current} align="end" className={CARD_POP}>
        {last.current && <EditBody key={last.current.agent ?? ""} editing={last.current} done={closeEdit} />}
      </PopSurface>
    </Popover.Root>
  );
}

/** The channel's face before the title, pressed: its card in a popover out of the face (kumo,
 *  2026-11-16: a face pops its card, it never opens a page; docs/前端设计规范.md); 编辑资料 is a
 *  key of the card, for one of its people. */
function FaceKey({ s, stack, className, ...rest }: { s: Session; stack?: Session["agent"][] } & ComponentProps<"button">) {
  const [open, setOpen] = useState(false);
  const face = <LogoLamp agent={s.agent} status={lampOf(s)} size={18} mark={<Face key={s.id} id={s.id} agent={s.agent} size={18} none={stack && <LogoStack agents={stack} size={18} />} />} />;
  // rest: what the tip around it gives its trigger.
  return (
    <Popover.Root open={open} onOpenChange={setOpen}>
      <Popover.Trigger
        render={
          <button {...rest} type="button" aria-label={titleOf(s)} className={cn("g-press -m-1 grid place-items-center rounded-full p-1 hover:bg-fill data-popup-open:bg-fill", className)}>
            {face}
          </button>
        }
      />
      <PopSurface className={CARD_POP}>
        <ChannelCard id={s.id} agent={s.agent} done={() => setOpen(false)} />
      </PopSurface>
    </Popover.Root>
  );
}

/** A chip's lamp for the sessions sent there: amber while one needs you, else green while one
 *  works, else none (as a launcher folded in the list shows them). */
function lampFor(ss: Session[], asked: { session?: string }[]): Status {
  if (ss.some((s) => lampOf(s) === "waiting" || asked.some((c) => c.session === s.id))) return "waiting";
  return ss.some((s) => ["running", "compacting", "starting"].includes(lampOf(s))) ? "running" : "idle";
}

/** 在哪干活 (plan, 2026-11-10): a row of chips under the title, in line with it, scrolled sideways
 *  when they do not fit (on a phone, from the bar's edge). Its own folder; the places it sent
 *  sessions to (another machine's with its face and name), how many, their lamp, pressed the first
 *  of them; those it named and sent none to, faint, dashed. Its own and those named, pressed, show
 *  in the list (places.ts openPlace). */
function Where({ spots, asked }: { spots: Spot[]; asked: { session?: string }[] }) {
  const ms = useMachines();
  return (
    <div role="list" aria-label={t("chat.where")} className="g-fade flex h-8 min-w-0 items-start gap-1.5 overflow-x-auto pr-3 pl-9 [scrollbar-width:none] [&::-webkit-scrollbar]:hidden">
      {spots.map((x) => {
        const shown = shownPath(x.place.machine ? `${x.place.machine}:${x.place.path}` : x.place.path, ms);
        const m = shown.m;
        const where = m ? `${m.name} · ${shown.real}` : tilde(shown.real);
        const n = x.sessions.length;
        const lamp = n ? lampFor(x.sessions, asked) : "idle";
        const label = x.kind === "named" ? `${where} · ${t("chat.whereNamed")}` : n ? `${where} · ${t("nav.hungCount", { n })}` : where;
        return (
          <Tip key={`${x.kind}:${x.key}`} label={label} side="bottom">
            <button
              type="button"
              role="listitem"
              onClick={() => (x.kind === "sent" ? openChat(x.sessions[0]!.id) : openPlace(x.place))}
              className={cn("g-press flex h-6 shrink-0 items-center gap-1.5 rounded-full px-2 text-xs transition-colors duration-(--dur-2)", x.kind === "named" ? "border border-dashed border-edge text-ink-3 hover:text-ink-2" : "bg-fill text-ink-2 hover:bg-fill-2 hover:text-ink")}
            >
              {m && x.kind !== "named" ? <MachineFace m={m} size={14} className={cn(!m.online && "opacity-50 grayscale")} /> : <PlaceIcon remote={!!x.place.machine} label={m?.name} size={13} />}
              <span className="max-w-48 truncate">{m ? `${m.name} · ${x.name}` : x.name}</span>
              {n > 0 && <span className="text-ink-3 tabular-nums">{n}</span>}
              {lamp !== "idle" && <Lamp status={lamp} />}
            </button>
          </Tip>
        );
      })}
    </div>
  );
}


type Works = { given: GivenTurn; relay?: Relay; time?: number };

/** What the agent did and said on end, under its face: the face at the left, its nick and what
 *  it runs as over the rest, as in a channel of several (chat/channel/page GroupView). */
/** bars: its usage, after its model's name (the user, 2026-11-13: as the composer's foot had it,
 *  as small as a phone's). */
function AgentTurn({ channel, a, may, bars, talk, time, children }: { channel: string; a: AgentRow; may: boolean; bars?: ReactNode; talk?: () => void; time?: number; children: ReactNode }) {
  const row = useWhenRow();
  return (
    <div {...row} className={cn(row.className, "flex min-w-0 items-start gap-2.5")}>
      <AgentFace channel={channel} a={a} may={may} talk={talk} />
      <div className="flex min-w-0 flex-1 flex-col gap-5">
        <div className="-mb-3.5 flex h-4 min-w-0 items-center gap-1.5">
          {a.nick && <span className="shrink-0 text-xs font-medium text-ink-2">{a.nick}</span>}
          <span className={cn("min-w-0 truncate text-xs", a.nick ? "text-ink-4" : "font-medium text-ink-2")}>{a.name}</span>
          {bars}
          <When time={time} />
        </div>
        {children}
      </div>
    </div>
  );
}

/** A call being written that only names the session (ir rename): left out, as its block is. */
const renamesDraft = (d: Drafted) => !!d.tool && renameOnly({ name: d.tool.name, input: d.text });

/** The turn's row at the end of the conversation: the call being written, else the one running
 *  (the newest, how many more beside it), else the dots once it has gone quiet; one line high, even
 *  empty, while the turn lasts. */
/** under: in a turn's frame of its own, under the face and name (the turn's first). stop: the
 *  agent's stop key, at the row's right. */
function Activity({ calls, running, cwd, quiet, grew, under, stop }: { calls: Drafted[]; running: Tool[]; cwd: string; quiet: boolean; grew: string; under?: boolean; stop?: ReactNode }) {
  const writing = calls.at(-1);
  const run = running.at(-1);
  return (
    <div className={cn("flex h-7 min-w-0 items-center pointer-coarse:h-9", under ? "-mt-1" : "-mt-3 pl-[38px]")}>
      {writing?.tool ? (
        <div key={writing.tool.id} className="min-w-0 flex-1">
          <ToolDraft name={writing.tool.name} input={writing.text} whole={writing.whole} />
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
