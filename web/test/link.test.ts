// The top bar's dot against a fake WebSocket (Satori's /v1/events): the round trip a PING's PONG,
// measured as the stream opens and again at each beat; a drop that clears it and asks /v1/meta (a
// login that ran out); a PONG that never comes, the stream opened again; a stream that hangs while
// opening, opened again. The tests follow on from each other, as the stream module is one per page.
import { describe, expect, test, vi } from "vitest";
import { getCarried, type Link } from "../src/store";

/** How the daemon answers a PING: after ms, or never. */
let pong: { ms: number } | "hang" = { ms: 5 };

class FakeWS {
  static CONNECTING = 0;
  static OPEN = 1;
  static CLOSING = 2;
  static CLOSED = 3;
  static all: FakeWS[] = [];
  readyState = FakeWS.CONNECTING;
  sent: string[] = [];
  onopen: (() => void) | null = null;
  onmessage: ((m: { data: string }) => void) | null = null;
  onclose: (() => void) | null = null;
  constructor(readonly url: string) {
    FakeWS.all.push(this);
  }
  send(d: string) {
    this.sent.push(d);
    if ((JSON.parse(d) as { op: number }).op === 1 && pong !== "hang") {
      setTimeout(() => this.readyState === FakeWS.OPEN && this.onmessage?.({ data: '{"op":2,"body":{}}' }), pong.ms);
    }
  }
  close() {
    this.readyState = FakeWS.CLOSED;
  }
  pings() {
    return this.sent.filter((d) => (JSON.parse(d) as { op: number }).op === 1).length;
  }
  /** Opens, the page identifies, Iridium answers READY. */
  open() {
    this.readyState = FakeWS.OPEN;
    this.onopen?.();
    this.onmessage?.({ data: JSON.stringify({ op: 4, body: { logins: [] } }) });
  }
  /** Dropped by the network. */
  drop() {
    this.readyState = FakeWS.CLOSED;
    this.onclose?.();
  }
}

/** Every request the page made: method and path. */
const asked: string[] = [];

// Before the module loads, so its beat runs on these clocks.
vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout", "setInterval", "clearInterval", "performance", "Date"] });
vi.stubGlobal("WebSocket", FakeWS);
vi.stubGlobal("fetch", (url: string, init: RequestInit = {}) => {
  asked.push(`${init.method ?? "GET"} ${url}`);
  return Promise.resolve(new Response('{"logins":[]}', { status: 200 }));
});
const { EVERY, WAIT, follow } = await import("../src/stream");

const t0 = performance.now();

const link = () => getCarried<Link>("link");
const last = () => FakeWS.all.at(-1)!;
const wait = (ms: number) => vi.advanceTimersByTimeAsync(ms);
/** On to ms after the next beat. */
const toBeat = (ms: number) => {
  const now = performance.now();
  return wait(t0 + (Math.floor((now - t0) / EVERY) + 1) * EVERY + ms - now);
};

describe("link", () => {
  test("measured as the stream opens, then again each beat; a drop clears it till it is back", async () => {
    follow(null);
    last().open();
    await wait(10);
    expect(link()).toEqual({ live: true, rtt: 5 });

    pong = { ms: 40 };
    await toBeat(100);
    expect(link()).toEqual({ live: true, rtt: 40 });
    pong = { ms: 7 };
    await toBeat(100);
    expect(link()).toEqual({ live: true, rtt: 7 });
    expect(asked).toEqual([]); // no HTTP for any of it

    const es = last();
    expect(JSON.parse(es.sent[0]!)).toMatchObject({ op: 3, body: { _watch: expect.arrayContaining(["list"]) } });
    es.drop();
    expect(link()).toEqual({ live: false, rtt: null });
    expect(asked).toEqual(["POST /v1/meta"]); // a 401 would take the page to the login
    pong = { ms: 12 };
    await wait(3000); // opened again
    expect(last()).not.toBe(es);
    last().open();
    await wait(20);
    expect(link()).toEqual({ live: true, rtt: 12 });
  });

  test("a PONG that never comes: one PING at a time, and after WAIT the stream opens again", async () => {
    const es = last();
    pong = "hang";
    const n = es.pings();
    await toBeat(10); // its PING goes unanswered
    expect(es.pings()).toBe(n + 1);
    expect(link()).toEqual({ live: true, rtt: 12 });

    await wait(WAIT);
    expect(link()).toEqual({ live: false, rtt: null });
    expect(es.readyState).toBe(FakeWS.CLOSED);
    expect(es.pings()).toBe(n + 1);
    const again = last();
    expect(again).not.toBe(es);

    pong = { ms: 9 };
    again.open();
    await wait(20);
    expect(link()).toEqual({ live: true, rtt: 9 });
  });

  test("a stream still opening long after it began is opened again", async () => {
    const es = last();
    await toBeat(EVERY - 5000);
    es.drop(); // dropped; the stream opened again never answers
    await wait(3000);
    const trying = last();
    expect(trying).not.toBe(es);
    await toBeat(100);
    expect(last()).toBe(trying); // just tried: given its time
    await toBeat(100);
    expect(last()).not.toBe(trying);
    expect(link()?.live).toBe(false);
    last().open();
    await wait(20);
    expect(link()).toEqual({ live: true, rtt: 9 });
  });
});
