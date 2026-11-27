// The conversation at its end and the tab behind another (chat/stick.tsx): nothing is drawn there,
// so what came meanwhile was followed by nothing and eased through, top to bottom, as the tab came
// back (kumo, 2026-11-26: Zen). Back in front it is at its end at once; so is more than a pane's
// height come in one frame; a line written is still eased.
import { act, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import { Stick } from "../src/chat/stick";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const PANE = 600;
let high = 0; // the conversation's height
let hidden = false;
let resized: (() => void)[] = [];
let frames: FrameRequestCallback[] = [];
let root: Root | null = null;

beforeEach(() => {
  high = 2000;
  hidden = false;
  resized = [];
  frames = [];
  vi.stubGlobal("ResizeObserver", class { constructor(f: () => void) { resized.push(f); } observe() {} disconnect() {} });
  vi.stubGlobal("requestAnimationFrame", (f: FrameRequestCallback) => frames.push(f));
  vi.stubGlobal("cancelAnimationFrame", () => void (frames = []));
  Object.defineProperty(document, "hidden", { configurable: true, get: () => hidden });
  Object.defineProperty(document, "visibilityState", { configurable: true, get: () => (hidden ? "hidden" : "visible") });
});
afterEach(() => {
  act(() => root?.unmount());
  root = null;
  vi.unstubAllGlobals();
});

function open(rows: ReactNode, tail = 1) {
  const host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
  const draw = (rows: ReactNode, tail: number) =>
    act(() => root!.render(<Stick tail={tail} older={async () => false} draw={() => {}} place={{ top: null }} bottom={0}><div data-column>{rows}</div></Stick>));
  // The pane laid out before the first draw reads it: the log's own sizes, as a browser has them.
  const proto = HTMLElement.prototype;
  const sizes = { scrollHeight: () => high, clientHeight: () => PANE };
  for (const [k, f] of Object.entries(sizes)) Object.defineProperty(proto, k, { configurable: true, get(this: HTMLElement) { return this.getAttribute("role") === "log" ? f() : 0; } });
  draw(rows, tail);
  const el = host.querySelector<HTMLElement>('[role="log"]')!;
  return { el, draw, max: () => high - PANE };
}
const frame = () => act(() => void frames.splice(0).forEach((f) => f(performance.now())));
const grew = (by: number) => act(() => ((high += by), resized.forEach((f) => f())));

describe("a chat at its end", () => {
  test("opens at its end", () => {
    const c = open(<p>one</p>);
    expect(c.el.scrollTop).toBe(c.max());
  });

  test("a line written is eased to, not jumped to", () => {
    const c = open(<p>one</p>);
    grew(40);
    expect(c.el.scrollTop).toBe(c.max() - 40);
    frame();
    expect(c.el.scrollTop).toBeGreaterThan(c.max() - 40);
  });

  test("more than a pane come at once: at the end in that frame", () => {
    const c = open(<p>one</p>);
    grew(5000);
    expect(c.el.scrollTop).toBe(c.max());
    expect(frames).toHaveLength(0);
  });

  test("what grows past a pane while it eases is put at the end too", () => {
    const c = open(<p>one</p>);
    grew(400);
    high += 3000; // not told yet: seen by the frame
    frame();
    expect(c.el.scrollTop).toBe(c.max());
  });

  test("back in front after what was said behind another tab: at the end before any frame", () => {
    const c = open(<p>one</p>);
    hidden = true;
    document.dispatchEvent(new Event("visibilitychange"));
    high += 300; // no change of height is told, no frame runs, behind
    hidden = false;
    document.dispatchEvent(new Event("visibilitychange"));
    expect(c.el.scrollTop).toBe(c.max());
    expect(frames).toHaveLength(0);
  });

  test("scrolled up, coming back in front moves nothing", () => {
    const c = open(<p>one</p>);
    act(() => void c.el.dispatchEvent(new WheelEvent("wheel", { deltaY: -50 })));
    c.el.scrollTop = 300;
    high += 300;
    document.dispatchEvent(new Event("visibilitychange"));
    grew(5000);
    expect(c.el.scrollTop).toBe(300);
  });

  test("opened with nothing in it yet: its rows come at the end at once, however many", () => {
    high = PANE;
    const c = open(null);
    high = PANE + 400; // less than a pane more: eased, were it not the first draw
    c.draw(<p>one</p>, 2);
    expect(c.el.scrollTop).toBe(c.max());
  });

  test("scrolled up without new messages shows the scroll-to-bottom button, hides back at end", () => {
    const c = open(<p>one</p>);
    const btn = () => c.el.parentElement!.querySelector("button");
    expect(btn()).toBeNull();
    act(() => void c.el.dispatchEvent(new WheelEvent("wheel", { deltaY: -50 })));
    c.el.scrollTop = c.max() - 200;
    act(() => void c.el.dispatchEvent(new Event("scroll")));
    expect(btn()).not.toBeNull();
    expect(btn()!.textContent).toBe("");
    // Returning to the bottom hides it again.
    c.el.scrollTop = c.max();
    act(() => void c.el.dispatchEvent(new Event("scroll")));
    expect(btn()).toBeNull();
  });

  test("a new message while scrolled up shows the newer label on the button", () => {
    const c = open(<p>one</p>, 1);
    const btn = () => c.el.parentElement!.querySelector("button");
    act(() => void c.el.dispatchEvent(new WheelEvent("wheel", { deltaY: -50 })));
    c.el.scrollTop = c.max() - 200;
    act(() => void c.el.dispatchEvent(new Event("scroll")));
    c.draw(<p>two</p>, 2);
    expect(btn()?.textContent).toBeTruthy();
    act(() => btn()!.click());
    expect(btn()).toBeNull();
  });
});
