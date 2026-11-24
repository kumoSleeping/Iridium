// Guards for the macOS WebKit memory and storage regressions (AgyMark SVG filters,
// startViewTransition graphics leak, and frequent ir-first localStorage writes).
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import { AGY, AGY_BLOBS, AGY_DATA_URI } from "../src/logos";
import { isWebKit } from "../src/motion";
import { setCarried } from "../src/store";

describe("AGY_DATA_URI", () => {
  test("encodes one shared SVG image with all blur filters and the arch path", () => {
    expect(AGY_DATA_URI.startsWith("data:image/svg+xml,")).toBe(true);
    const svg = decodeURIComponent(AGY_DATA_URI.slice("data:image/svg+xml,".length));
    expect(svg).toContain(AGY);
    for (let i = 0; i < AGY_BLOBS.length; i++) {
      expect(svg).toContain(`id="f${i}"`);
      expect(svg).toContain(`filter="url(#f${i})"`);
    }
  });
});

describe("isWebKit", () => {
  test("matches macOS WKWebView, Safari, and iOS browsers", () => {
    expect(isWebKit("Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko)")).toBe(true);
    expect(isWebKit("Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.5 Safari/605.1.15")).toBe(true);
    expect(isWebKit("Mozilla/5.0 (iPhone; CPU iPhone OS 19_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) CriOS/136.0.0.0 Mobile/15E148 Safari/604.1")).toBe(true);
  });
  test("excludes Blink and Gecko browsers", () => {
    expect(isWebKit("Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/136.0.0.0 Safari/537.36")).toBe(false);
    expect(isWebKit("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/136.0.0.0 Safari/537.36 Edg/136.0.0.0")).toBe(false);
    expect(isWebKit("Mozilla/5.0 (Linux; Android 15) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/136.0.0.0 Mobile Safari/537.36")).toBe(false);
    expect(isWebKit("Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:136.0) Gecko/20100101 Firefox/136.0")).toBe(false);
  });
});

describe("device/first persistence throttling", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    localStorage.clear();
  });
  afterEach(() => {
    vi.useRealTimers();
    localStorage.clear();
    setCarried("faces", null);
  });

  test("throttles writes while open, skips identical JSON, and flushes on pagehide", async () => {
    const first = await import("../src/device/first");
    const setItem = vi.spyOn(Storage.prototype, "setItem");
    first.watch();
    setCarried("faces", { a: 1 });
    // Not written after 2s (was the old debounce that hammered WebKit SQLite).
    vi.advanceTimersByTime(5_000);
    expect(setItem).not.toHaveBeenCalled();
    // Written once the 30s delay elapses.
    vi.advanceTimersByTime(26_000);
    expect(setItem).toHaveBeenCalledTimes(1);
    // Another event with identical serialized value skips setItem.
    setCarried("faces", { a: 1 });
    vi.advanceTimersByTime(35_000);
    expect(setItem).toHaveBeenCalledTimes(1);
    // A real change flushes immediately on pagehide without waiting 30s.
    setCarried("faces", { a: 2 });
    window.dispatchEvent(new Event("pagehide"));
    expect(setItem).toHaveBeenCalledTimes(2);
    setItem.mockRestore();
  });
});
