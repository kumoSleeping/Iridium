// The page's one event stream (docs/api.md "事件流"): it names what it carries (the list, the quota,
// and the open session with its context) and gets each whole at once, then whole again as it
// changes. Opening another session opens a new stream; one dropped comes back by itself, and its
// first states bring the page up to date. Drafts, the agent's words as it writes them, go to the
// open chat alone.
import { api } from "./api/client";
import type { DraftEvent, StateEvent } from "./api/types.gen";
import { getCarried, setCarried, type Link } from "./store";

/** Told each draft event. */
export const drafts = new Set<(d: DraftEvent) => void>();
/** Told each time the stream opens, which it may have been down: drafts go, new blocks are fetched. */
export const opened = new Set<() => void>();

let es: EventSource | null = null;
let session: string | null = null;
let retry: ReturnType<typeof setTimeout> | undefined;
let soon: ReturnType<typeof setTimeout> | undefined;
let started = false;
/** When the stream last began opening, or the browser trying it again; how many round trips in a row failed. */
let since = 0;
let missed = 0;

const link = (l: Partial<Link>) => setCarried("link", { ...(getCarried<Link>("link") ?? { live: null, rtt: null }), ...l });

// The states every page carries. A key the daemon does not carry for this terminal (confirms and
// colors, but for panels) is said to be ignored as the stream opens; the rest goes on.
const ALWAYS = ["list", "workspaces", "colors", "quota", "update", "confirms", "terminals", "machines", "notes"];
/** What the stream last brought of each state every page carries, as it came: a stream opened
 *  again (each chat opened opens one) brings them all whole again, and one unchanged is not set
 *  again, the sidebar and the rest drawn again for nothing. Not the open session's: chats.ts
 *  stateAhead sets those apart from the stream. */
const lastRaw = new Map<string, string>();
/** The keys the daemon said it does not carry, and why ("unknown", "forbidden"). */
export const ignored = new Map<string, string>();
export const keysFor = (s: string | null) => [...ALWAYS, ...(s ? [`session:${s}`, `context:${s}`] : [])];
export const streamURL = (s: string | null) => `/api/events?${keysFor(s).map((k) => `sub=${encodeURIComponent(k)}`).join("&")}`;

function connect() {
  clearTimeout(retry);
  es?.close();
  const src = new EventSource(streamURL(session));
  es = src;
  since = performance.now();
  src.onopen = () => {
    if (es !== src) return;
    missed = 0;
    link({ live: true });
    opened.forEach((f) => f());
    void measure();
  };
  src.addEventListener("state", (m) => {
    const raw = (m as MessageEvent<string>).data;
    const e = JSON.parse(raw) as StateEvent;
    if (ALWAYS.includes(e.key)) {
      if (lastRaw.get(e.key) === raw && getCarried(e.key) !== undefined) return;
      lastRaw.set(e.key, raw);
    }
    if (e.ignored) ignored.set(e.key, e.ignored);
    else ignored.delete(e.key);
    setCarried(e.key, e.ignored ? null : e.value);
  });
  src.addEventListener("draft", (m) => {
    const d = JSON.parse((m as MessageEvent<string>).data) as DraftEvent;
    drafts.forEach((f) => f(d));
  });
  src.onerror = () => {
    if (es !== src) return;
    link({ live: false, rtt: null }); // back, it is measured afresh
    since = performance.now(); // the browser's own try begins
    // The browser retries a dropped stream itself, but gives up for good on an answer other than
    // 200 (a proxy's 502 while the daemon restarts, a login that ran out): then open a new one.
    if (src.readyState !== EventSource.CLOSED) return;
    void api.health().catch(() => {}); // a 401 here takes the page to the login
    retry = setTimeout(connect, 3000);
  };
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
  soon = setTimeout(connect, 60);
}

/** How often the round trip is measured while the page is in front, and how long one may take. */
export const EVERY = 10_000;
export const WAIT = 8_000;

let measuring: Promise<boolean> | undefined;
/** The round trip of one small request, shown by the top bar's dot; whether it came back in time.
 *  One at a time: a request that hangs (a dead tunnel) is cut off at WAIT, not left to show the
 *  number from before. */
export function measure(): Promise<boolean> {
  return (measuring ??= (async () => {
    const stop = new AbortController();
    const cut = setTimeout(() => stop.abort(), WAIT);
    const t = performance.now();
    try {
      // Not low priority: a request held back behind others is not the round trip.
      await api.health({ signal: stop.signal });
      link({ rtt: Math.round(performance.now() - t) });
      return true;
    } catch {
      link({ rtt: null });
      return false;
    } finally {
      clearTimeout(cut);
      measuring = undefined;
    }
  })());
}

/** One beat of the dot, while in front: the round trip again. A stream that looks open while two
 *  round trips in a row fail (a tunnel or a laptop's sleep that dropped it without a word) is taken
 *  as down and opened again; one still opening long after requests get through again is too. */
export async function beat() {
  if (!started || document.visibilityState !== "visible") return;
  const live = getCarried<Link>("link")?.live;
  const ok = await measure();
  missed = ok ? 0 : missed + 1;
  if (live && missed >= 2) {
    missed = 0;
    link({ live: false, rtt: null });
    connect();
  } else if (!live && ok && es?.readyState !== EventSource.OPEN && performance.now() - since > WAIT) connect();
}

// While in front, a beat every EVERY; back in front, at once.
setInterval(() => void beat(), EVERY);
document.addEventListener("visibilitychange", () => void beat());
