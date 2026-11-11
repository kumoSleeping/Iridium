// The page's one event stream: Satori's /v1/events (docs/satori.md), as every app's. It names what it
// carries (WATCH: the list, the quota, and the open session with its context) and gets each whole
// at once (STATE), then whole again as it changes. Opening another session says so on the same
// stream; one dropped comes back, and its first states bring the page up to date. Drafts, the
// agent's words as it writes them, go to the open chat alone.
import { api } from "./api/client";
import type { DraftEvent, StateEvent } from "./api/types.gen";
import { getCarried, setCarried, type Link } from "./store";

/** Told each draft event. */
export const drafts = new Set<(d: DraftEvent) => void>();
/** Told each time the stream opens, which it may have been down: drafts go, new blocks are fetched. */
export const opened = new Set<() => void>();

let ws: WebSocket | null = null;
let session: string | null = null;
let retry: ReturnType<typeof setTimeout> | undefined;
let soon: ReturnType<typeof setTimeout> | undefined;
let started = false;
/** When the stream last began opening, or the browser trying it again. */
let since = 0;
/** When the PING waiting for its PONG went (null: none), and what gives up on it. */
let pingAt: number | null = null;
let late: ReturnType<typeof setTimeout> | undefined;

const link = (l: Partial<Link>) => setCarried("link", { ...(getCarried<Link>("link") ?? { live: null, rtt: null }), ...l });

// The states every page carries. A key the daemon does not carry for this terminal (confirms and
// colors, but for panels) is said to be ignored as the stream opens; the rest goes on.
const ALWAYS = ["list", "workspaces", "places", "colors", "quota", "update", "needs", "pairing", "terminals", "notes"];
/** What the stream last brought of each state every page carries, as it came: a stream opened
 *  again (each chat opened opens one) brings them all whole again, and one unchanged is not set
 *  again, the sidebar and the rest drawn again for nothing. Not the open session's: chats.ts
 *  stateAhead sets those apart from the stream. */
const lastRaw = new Map<string, string>();
/** The keys the daemon said it does not carry, and why ("unknown", "forbidden"). */
export const ignored = new Map<string, string>();
export const keysFor = (s: string | null) => [...ALWAYS, ...(s ? [`session:${s}`, `context:${s}`] : [])];
/** Satori's event stream, at this page's own address. */
export const streamURL = () => `${location.protocol === "https:" ? "wss:" : "ws:"}//${location.host}/v1/events`;
/** Satori's signals (protocol/events.md), and Iridium's own WATCH and STATE. */
const OP = { event: 0, ping: 1, pong: 2, identify: 3, ready: 4, meta: 5, watch: 6, state: 7 } as const;
/** How often the stream says it is there and the round trip is measured (Satori: a PING every 10
 *  seconds); how long its PONG may take. */
export const EVERY = 10_000;
export const WAIT = 8_000;

function onState(raw: string) {
  const e = JSON.parse(raw) as StateEvent;
  if (ALWAYS.includes(e.key)) {
    if (lastRaw.get(e.key) === raw && getCarried(e.key) !== undefined) return;
    lastRaw.set(e.key, raw);
  }
  if (e.ignored) ignored.set(e.key, e.ignored);
  else ignored.delete(e.key);
  setCarried(e.key, e.ignored ? null : e.value);
}

/** A PING on the open stream, one at a time; its PONG is the round trip, the top bar's dot. None
 *  back within WAIT (a tunnel or a laptop's sleep that dropped it without a word): the stream is
 *  taken as down and opened again. */
function ping() {
  const sock = ws;
  if (!sock || sock.readyState !== WebSocket.OPEN || pingAt !== null) return;
  pingAt = performance.now();
  sock.send(JSON.stringify({ op: OP.ping }));
  late = setTimeout(() => {
    if (ws !== sock) return;
    link({ live: false, rtt: null });
    connect();
  }, WAIT);
}

function connect() {
  clearTimeout(retry);
  clearTimeout(late);
  pingAt = null;
  if (ws) {
    ws.onclose = null;
    ws.close();
  }
  const sock = new WebSocket(streamURL());
  ws = sock;
  since = performance.now();
  sock.onopen = () => {
    if (ws !== sock) return;
    sock.send(JSON.stringify({ op: OP.identify, body: { _watch: keysFor(session) } }));
  };
  sock.onmessage = (m) => {
    if (ws !== sock) return;
    const sig = JSON.parse(m.data as string) as { op: number; body?: { type?: string; data?: unknown } };
    if (sig.op === OP.pong && pingAt !== null) {
      link({ rtt: Math.round(performance.now() - pingAt) });
      pingAt = null;
      clearTimeout(late);
    } else if (sig.op === OP.ready) {
      link({ live: true });
      opened.forEach((f) => f());
      ping();
    } else if (sig.op === OP.state && sig.body) {
      const raw = JSON.stringify(sig.body.data);
      if (sig.body.type === "state") onState(raw);
      else if (sig.body.type === "draft") drafts.forEach((f) => f(sig.body!.data as DraftEvent));
    }
  };
  sock.onclose = () => {
    if (ws !== sock) return;
    clearTimeout(late);
    pingAt = null;
    link({ live: false, rtt: null }); // back, it is measured afresh
    since = performance.now();
    // A browser does not say why an upgrade failed: one request finds a login that ran out (401).
    void api.meta().catch(() => {});
    retry = setTimeout(connect, 3000);
  };
}

/** Says on the open stream what it carries now; opens one if there is none. */
function watch() {
  if (ws?.readyState === WebSocket.OPEN) ws.send(JSON.stringify({ op: OP.watch, body: { keys: keysFor(session) } }));
  else if (!ws || ws.readyState === WebSocket.CLOSED) connect();
}

/** Follows session (null: none open) on the stream, opening it the first time. Switching quickly,
 *  as Alt+↓ held, opens one stream for the last. */
export function follow(s: string | null) {
  if (started && s === session) return;
  session = s;
  clearTimeout(soon);
  if (!started) {
    started = true;
    connect();
    return;
  }
  soon = setTimeout(watch, 60);
}

/** One beat: a PING on the open stream (hidden too: it keeps the stream); a stream still opening
 *  long after it began is opened again. */
export function beat() {
  if (!started) return;
  if (ws?.readyState === WebSocket.OPEN) ping();
  else if (ws?.readyState === WebSocket.CONNECTING && performance.now() - since > WAIT) connect();
}

// A beat every EVERY; back in front, at once.
setInterval(beat, EVERY);
document.addEventListener("visibilitychange", () => document.visibilityState === "visible" && beat());
