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

const link = (l: Partial<Link>) => setCarried("link", { ...(getCarried<Link>("link") ?? { live: null, rtt: null }), ...l });

// The states every page carries. A key the daemon does not carry for this terminal (confirms, but
// for panels) is said to be ignored as the stream opens; the rest goes on.
const ALWAYS = ["list", "workspaces", "quota", "update", "confirms"];
/** The keys the daemon said it does not carry, and why ("unknown", "forbidden"). */
export const ignored = new Map<string, string>();
export const keysFor = (s: string | null) => [...ALWAYS, ...(s ? [`session:${s}`, `context:${s}`] : [])];
export const streamURL = (s: string | null) => `/api/events?${keysFor(s).map((k) => `sub=${encodeURIComponent(k)}`).join("&")}`;

function connect() {
  clearTimeout(retry);
  es?.close();
  const src = new EventSource(streamURL(session));
  es = src;
  src.onopen = () => {
    if (es !== src) return;
    link({ live: true });
    opened.forEach((f) => f());
    void measure();
  };
  src.addEventListener("state", (m) => {
    const e = JSON.parse((m as MessageEvent<string>).data) as StateEvent;
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
    link({ live: false });
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

/** The round trip of one small request, shown by the top bar's dot. */
export async function measure() {
  const t = performance.now();
  try {
    await api.health({ priority: "low" } as RequestInit);
    link({ rtt: Math.round(performance.now() - t) });
  } catch {
    link({ rtt: null });
  }
}

// While in front and connected, the round trip every half minute; back in front, at once.
setInterval(() => void (document.visibilityState === "visible" && getCarried<Link>("link")?.live && measure()), 30_000);
document.addEventListener("visibilitychange", () => void (document.visibilityState === "visible" && getCarried<Link>("link")?.live && measure()));
