// The top bar's dot against a mocked daemon and a fake WebSocket (Satori's /v1/events): the round trip measured again
// at each beat, a drop that clears it, a request that hangs cut off, a stream that looks open but
// is dead opened again. The tests follow on from each other, as the stream module is one per page.
import { describe, expect, test, vi } from "vitest";
import { getCarried, type Link } from "../src/store";

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
  }
  close() {
    this.readyState = FakeWS.CLOSED;
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

/** How /api/health answers: after ms, or never (until cut off). */
let health: { ms: number } | "hang" = { ms: 5 };
let asked = 0;

// Before the module loads, so its beat runs on these clocks.
vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout", "setInterval", "clearInterval", "performance", "Date"] });
vi.stubGlobal("WebSocket", FakeWS);
vi.stubGlobal("fetch", (_url: string, init: RequestInit = {}) => {
  asked++;
  return new Promise<Response>((ok, fail) => {
    init.signal?.addEventListener("abort", () => fail(new DOMException("aborted", "AbortError")));
    if (health !== "hang") setTimeout(() => ok(new Response('{"ok":true}', { status: 200 })), health.ms);
  });
});
const { EVERY, WAIT, follow, measure } = await import("../src/stream");

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

    health = { ms: 40 };
    await toBeat(100);
    expect(link()).toEqual({ live: true, rtt: 40 });
    health = { ms: 7 };
    await toBeat(100);
    expect(link()).toEqual({ live: true, rtt: 7 });

    const es = last();
    expect(JSON.parse(es.sent[0]!)).toMatchObject({ op: 3, body: { _watch: expect.arrayContaining(["list"]) } });
    es.drop();
    expect(link()).toEqual({ live: false, rtt: null });
    health = { ms: 12 };
    await wait(3000); // opened again
    expect(last()).not.toBe(es);
    last().open();
    await wait(20);
    expect(link()).toEqual({ live: true, rtt: 12 });
  });

  test("a request that hangs is cut off, one at a time; two in a row and the stream opens again", async () => {
    const es = last();
    health = "hang";
    const n = asked;
    await toBeat(10); // its request hangs
    void measure(); // the same request, not a second
    expect(asked).toBe(n + 1);
    await wait(WAIT);
    expect(link()).toEqual({ live: true, rtt: null });
    expect(last()).toBe(es);

    await toBeat(WAIT + 10); // the next hangs too: taken as down, opened again
    expect(asked).toBe(n + 2);
    expect(link()).toEqual({ live: false, rtt: null });
    expect(es.readyState).toBe(FakeWS.CLOSED);
    const again = last();
    expect(again).not.toBe(es);

    health = { ms: 9 };
    again.open();
    await wait(20);
    expect(link()).toEqual({ live: true, rtt: 9 });
  });

  test("a stream still opening long after requests get through again is opened again", async () => {
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
