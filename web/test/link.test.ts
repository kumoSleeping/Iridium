// The top bar's dot against a mocked daemon and a fake EventSource: the round trip measured again
// at each beat, a drop that clears it, a request that hangs cut off, a stream that looks open but
// is dead opened again. The tests follow on from each other, as the stream module is one per page.
import { describe, expect, test, vi } from "vitest";
import { getCarried, type Link } from "../src/store";

class FakeES {
  static CONNECTING = 0;
  static OPEN = 1;
  static CLOSED = 2;
  static all: FakeES[] = [];
  readyState = FakeES.CONNECTING;
  onopen: (() => void) | null = null;
  onerror: (() => void) | null = null;
  constructor(readonly url: string) {
    FakeES.all.push(this);
  }
  addEventListener() {}
  close() {
    this.readyState = FakeES.CLOSED;
  }
  open() {
    this.readyState = FakeES.OPEN;
    this.onopen?.();
  }
}

/** How /api/health answers: after ms, or never (until cut off). */
let health: { ms: number } | "hang" = { ms: 5 };
let asked = 0;

// Before the module loads, so its beat runs on these clocks.
vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout", "setInterval", "clearInterval", "performance", "Date"] });
vi.stubGlobal("EventSource", FakeES);
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
const last = () => FakeES.all.at(-1)!;
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
    es.readyState = FakeES.CONNECTING; // the browser tries again itself
    es.onerror?.();
    expect(link()).toEqual({ live: false, rtt: null });
    health = { ms: 12 };
    es.open();
    await wait(20);
    expect(link()).toEqual({ live: true, rtt: 12 });
    expect(last()).toBe(es);
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
    expect(es.readyState).toBe(FakeES.CLOSED);
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
    es.readyState = FakeES.CONNECTING;
    es.onerror?.(); // dropped, and the browser's own try never answers
    await toBeat(100);
    expect(last()).toBe(es); // just dropped: the browser is given its time
    await toBeat(100);
    expect(last()).not.toBe(es);
    expect(link()?.live).toBe(false);
    last().open();
    await wait(20);
    expect(link()).toEqual({ live: true, rtt: 9 });
  });
});
