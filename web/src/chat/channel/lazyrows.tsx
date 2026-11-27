// A conversation come back to (docs/动画.md "回访"): its rows near what is seen are drawn in the
// first frame, the rest stand as boxes of the heights they had when last drawn, and are drawn
// after, a few at a time while the page is idle, the nearest first. The column is as tall as it
// was, row for row, so where it scrolls to, what follows its end and what keeps its place work as
// with every row drawn; a row whose height turns out another is under what Stick already keeps
// for (stick.tsx under, the browser's anchoring). Opened for the first time in this page, or at
// another width, every row is drawn at once, as before.
//
// Why: a long channel is thousands of elements, and going back to one drew all of them in the
// frame of the swap (measured 2026-11-27: 270 messages, 14 000 elements, 0.65 s of one task on a
// fast machine, with nothing fetched).
import { startTransition, useEffect, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { flushSync } from "react-dom";

/** A row as last drawn: what it was of (same says whether it still is), its height, the column's
 *  width then. */
type Seen<G> = { g: G; h: number; w: number };
const seenOf = new WeakMap<object, Map<string, Seen<unknown>>>();
/** The width the rows of a conversation were last drawn at, any conversation's: one kept at
 *  another is not trusted. */
let lastW = 0;

/** How much is drawn in the first frame, in panes: what is seen, and this much over and under. */
const OVER = 1.5;
const UNDER = 1;
/** How much is drawn at a time after, in px of rows. */
const CHUNK = 800;
/** Fewer rows than this left as boxes are not worth it. */
const MIN = 4;
/** How far from what is seen a row is far, in panes; how many rows at the end never are. */
const FAR = 2;
const LIVE = 3;
/** How long a far row that changed stays near, from its last change: what changes its height
 *  over a while (a fold's own move, a picture after its words) is over by then. */
const STILL = 1200;

const idle = (f: () => void): (() => void) => {
  if (typeof requestIdleCallback === "function") {
    const h = requestIdleCallback(f, { timeout: 250 });
    return () => cancelIdleCallback(h);
  }
  const h = setTimeout(f, 30);
  return () => clearTimeout(h);
};

/** The scrollers whose rows are not all drawn yet, each with what draws them now. */
const waiting = new WeakMap<Element, () => void>();
/** Every row under root (a chat's scroller, or anything in it) drawn now: before a row is looked
 *  for by what it holds (a message gone to, channel/jump.ts). Says whether any was still a box. */
export function drawAllRows(root: ParentNode | null | undefined): boolean {
  const el = root instanceof Element ? root.closest('[role="log"]') : null;
  const all = el && waiting.get(el);
  if (!all) return false;
  all();
  return true;
}

/** The rows left as boxes as the conversation opens, each with its height: none (null) unless it
 *  was opened in this page before, at this width, and enough of its rows are as they were.
 *  top: where it was left (null: at its end). pane: the height of what is seen, at most. */
export function boxes<G extends { key: string }>(rows: readonly G[], seen: ReadonlyMap<string, Seen<G>>, same: (a: G, b: G) => boolean, top: number | null, pane: number, width = lastW, lead = 0, gap = 0): Map<string, number> | null {
  if (!width || pane <= 0) return null;
  const hs = rows.map((g) => {
    const s = seen.get(g.key);
    return s && s.w === width && s.h > 0 && same(s.g, g) ? s.h : undefined;
  });
  const out = new Map<string, number>();
  if (top === null) {
    // At its end: from the last row up, a row not known counted as nothing (so more is drawn).
    let under = 0;
    for (let i = rows.length - 1; i >= 0; i--) {
      if (hs[i] !== undefined && under > pane * (1 + OVER) && i < rows.length - 2) out.set(rows[i]!.key, hs[i]!);
      under += (hs[i] ?? 0) + gap;
    }
  } else {
    // Left up from its end: where each row is, from the first one down, known as far as the
    // first row that is not. One not known over or in what is drawn at first: every row at once
    // (where the rows are is not known); under it, the rest are under it too, wherever.
    let at = lead, sure = true;
    for (let i = 0; i < rows.length; i++) {
      const h = hs[i];
      const under = at > top + pane * (1 + UNDER);
      if (h === undefined) {
        if (!under) return null;
        sure = false;
        continue;
      }
      if (under || (sure && at + h < top - pane * OVER)) out.set(rows[i]!.key, h);
      at += h + gap;
    }
  }
  return out.size >= MIN ? out : null;
}

/** The boxes drawn next: those nearest the rows drawn already, about CHUNK px of them. */
export function nextRows<G extends { key: string }>(rows: readonly G[], left: ReadonlyMap<string, number>): string[] {
  const out: string[] = [];
  let px = 0;
  const at = rows.map((g) => left.has(g.key));
  // From each edge of what is drawn, outward: the row before it, the row after it, in turn.
  const first = at.indexOf(false), last = at.lastIndexOf(false);
  let up = first < 0 ? rows.length - 1 : first - 1, down = last < 0 ? rows.length : last + 1;
  const inner = at.flatMap((b, i) => (b && i > first && i < last ? [i] : []));
  for (const i of inner) {
    out.push(rows[i]!.key);
    px += left.get(rows[i]!.key)!;
  }
  while (px < CHUNK && (up >= 0 || down < rows.length)) {
    for (const i of [up--, down++]) {
      if (i < 0 || i >= rows.length || !at[i]) continue;
      out.push(rows[i]!.key);
      px += left.get(rows[i]!.key)!;
    }
  }
  return out;
}

/** The rows of a conversation, each in its own box (className), drawn by children. owner: what
 *  the conversation is kept by (its log): the heights are kept with it and go with it. same:
 *  whether a row is what it was when its height was taken. place: where it was left (stick.tsx). */
export function LazyRows<G extends { key: string }>({ owner, rows, same, place, className, children }: { owner: object; rows: readonly G[]; same: (a: G, b: G) => boolean; place: { top: number | null; opened?: boolean }; className?: string; children: (g: G) => ReactNode }) {
  let seen = seenOf.get(owner) as Map<string, Seen<G>> | undefined;
  if (!seen) seenOf.set(owner, (seen = new Map<string, Seen<G>>()) as Map<string, Seen<unknown>>);
  const heights = seen;
  const [left, setLeft] = useState<Map<string, number> | null>(() => {
    if (!place.opened || typeof window === "undefined" || typeof ResizeObserver === "undefined") return null;
    // The column's own: 64px over its first row, 20px between rows (page.tsx). A little off is a
    // row more or less drawn at first, nothing else.
    return boxes(rows, heights, same, place.top, window.innerHeight, lastW, 64, 20);
  });
  const els = useRef(new Map<string, HTMLDivElement>()).current;
  const of = useRef(new WeakMap<Element, string>()).current;
  // What is drawn now, for what is told later (a size, a scroll): as drawn, not as a draw still
  // being made (the rest drawn in a transition) would have it.
  const leftNow = useRef(left);
  const now = useRef(new Map<string, G>()).current;
  const tail = useRef(new Set<string>()).current;
  useLayoutEffect(() => {
    leftNow.current = left;
    now.clear();
    for (const g of rows) now.set(g.key, g);
    tail.clear();
    for (const g of rows.slice(-LIVE)) tail.add(g.key);
  });

  /** The rows far off now, and what tells which are. */
  const far = useRef(new WeakSet<Element>()).current;
  const reach = useRef<IntersectionObserver | null>(null);
  const ref = (key: string) => (el: HTMLDivElement | null) => {
    const was = els.get(key);
    if (el === was) return;
    if (was) (ro.current?.unobserve(was), reach.current?.unobserve(was), els.delete(key));
    if (!el) return;
    els.set(key, el);
    of.set(el, key);
    ro.current?.observe(el);
    reach.current?.observe(el);
  };
  const refs = useRef(new Map<string, (el: HTMLDivElement | null) => void>()).current;
  const refOf = (key: string) => {
    let f = refs.get(key);
    if (!f) refs.set(key, (f = ref(key)));
    return f;
  };

  // Each row's height as it is drawn, kept for the next time it is come back to.
  const ro = useRef<ResizeObserver | null>(null);
  useLayoutEffect(() => {
    const r = new ResizeObserver((es) => {
      for (const e of es) {
        const key = of.get(e.target);
        const g = key !== undefined ? now.get(key) : undefined;
        const box = e.borderBoxSize?.[0];
        // A box, or a row far off (its height the one given it): nothing of its own to learn; at
        // another width than it was measured at, it is measured again.
        if (box && far.has(e.target) && box.inlineSize > 0 && Math.abs(box.inlineSize - lastW) > 0.5) nearAgain.current(e.target as HTMLElement);
        if (!g || !box || leftNow.current?.has(key!) || far.has(e.target)) continue;
        // Taken out of the page (the chat left): no size, nothing learnt.
        if (!box.blockSize && !box.inlineSize) continue;
        lastW = box.inlineSize;
        heights.set(key!, { g, h: box.blockSize, w: box.inlineSize });
      }
    });
    ro.current = r;
    for (const el of els.values()) r.observe(el);
    return () => void (r.disconnect(), (ro.current = null));
  }, []);
  // The rows gone are forgotten.
  useEffect(() => {
    for (const k of heights.keys()) if (!now.has(k)) heights.delete(k);
    for (const k of refs.keys()) if (!now.has(k)) refs.delete(k);
  });

  // Far from what is seen (FAR panes over or under it), a row is not styled, laid out or painted
  // by the browser (content-visibility: auto), its box kept at the height it was measured at: a
  // swap's pictures (a view transition styles the whole page again, twice) and every change of
  // style are the cost of what is near alone. Only while far: near, the row is as it ever was
  // (the property contains paint: what a row draws outside its box would be cut). The last rows
  // never: they are the ones written on.
  /** Till when each row stays near though far off: it changed, and is measured as it is. */
  const held = useRef(new WeakMap<Element, number>()).current;
  const nearAgain = useRef<(el: HTMLElement) => void>(() => {});
  const setFar = (el: HTMLElement, on: boolean) => {
    const key = of.get(el);
    const g = key !== undefined ? now.get(key) : undefined;
    const box = key !== undefined ? leftNow.current?.get(key) : undefined;
    const s = key !== undefined ? heights.get(key) : undefined;
    const h = box ?? (s && g && same(s.g, g) ? s.h : undefined);
    if (on && h !== undefined && !tail.has(key!) && (held.get(el) ?? 0) <= performance.now()) {
      far.add(el);
      el.style.containIntrinsicBlockSize = `${h}px`;
      el.style.contentVisibility = "auto";
    } else if (far.has(el)) {
      far.delete(el);
      el.style.contentVisibility = "";
      el.style.containIntrinsicBlockSize = "";
    }
  };
  useLayoutEffect(() => {
    if (typeof IntersectionObserver === "undefined" || typeof CSS === "undefined" || !CSS.supports("content-visibility", "auto")) return;
    const scroller = els.values().next().value?.closest('[role="log"]');
    if (!scroller) return;
    const o = new IntersectionObserver((es) => es.forEach((e) => setFar(e.target as HTMLElement, !e.isIntersecting)), { root: scroller, rootMargin: `${FAR * 100}% 0px` });
    reach.current = o;
    for (const el of els.values()) o.observe(el);
    // A far row keeps the height it was measured at, whatever becomes of what is in it: so one
    // that changes (drawn in the place of its box, its steps read, words folded, a picture come)
    // is near again, laid out and measured as it is, and far once more when it has been still a
    // while. Else the column was a little other than its rows, and a place kept in it too.
    const col = els.values().next().value?.parentElement;
    const timers = new Map<HTMLElement, ReturnType<typeof setTimeout>>();
    const again = (el: HTMLElement) => {
      held.set(el, performance.now() + STILL);
      if (far.has(el)) setFar(el, false);
      clearTimeout(timers.get(el));
      timers.set(el, setTimeout(() => (timers.delete(el), el.isConnected && (o.unobserve(el), o.observe(el))), STILL + 50));
    };
    nearAgain.current = again;
    const rowOf = (n: Node | null) => {
      let e = n instanceof HTMLElement ? n : (n?.parentElement ?? null);
      while (e && e.parentElement !== col) e = e.parentElement;
      return e;
    };
    const mo =
      col && typeof MutationObserver !== "undefined"
        ? new MutationObserver((rs) => {
            const hit = new Set<HTMLElement>();
            for (const r of rs) {
              // The row's own style is this file's (far or not) and its box's height: not a change in it.
              if (r.type === "attributes" && r.target.parentNode === col) continue;
              const row = rowOf(r.target);
              if (row && (far.has(row) || timers.has(row))) hit.add(row);
            }
            hit.forEach(again);
          })
        : null;
    if (col) mo?.observe(col, { subtree: true, childList: true, characterData: true, attributes: true });
    const loaded = (e: Event) => {
      const row = rowOf(e.target as Node);
      if (row && far.has(row)) again(row);
    };
    col?.addEventListener("load", loaded, true);
    return () => {
      o.disconnect();
      reach.current = null;
      nearAgain.current = () => {};
      mo?.disconnect();
      col?.removeEventListener("load", loaded, true);
      for (const t of timers.values()) clearTimeout(t);
    };
  }, []);
  // A row that is another now (written on, changed) is near again till it is measured anew; the
  // last rows are never far.
  useLayoutEffect(() => {
    for (const [k, el] of els) {
      if (!far.has(el)) continue;
      const s = heights.get(k), g = now.get(k);
      if (leftNow.current?.has(k)) continue;
      if (!g || !s || !same(s.g, g) || tail.has(k)) (setFar(el, false), reach.current?.unobserve(el), reach.current?.observe(el));
    }
  });

  // Drawn at another width than the heights were taken at: every row at once, before it is seen.
  useLayoutEffect(() => {
    if (!left) return;
    const el = els.values().next().value;
    if (el && Math.abs(el.getBoundingClientRect().width - lastW) > 0.5) setLeft(null);
  }, []);

  // The rest, a few at a time while the page is idle; all of them at once when asked (a message
  // gone to), and those scrolled near before their turn.
  const some = left ? left.size : 0;
  useEffect(() => {
    if (!some) return;
    return idle(() =>
      startTransition(() =>
        setLeft((l) => {
          if (!l) return l;
          const next = new Map(l);
          for (const k of nextRows(rows, l)) next.delete(k);
          return next.size ? next : null;
        }),
      ),
    );
  }, [some]);
  useLayoutEffect(() => {
    if (!some) return;
    const scroller = els.values().next().value?.closest('[role="log"]');
    if (!scroller) return;
    waiting.set(scroller, () => flushSync(() => setLeft(null)));
    const near = new IntersectionObserver(
      (es) => {
        const keys = es.flatMap((e) => (e.isIntersecting ? [of.get(e.target)] : []));
        if (!keys.length) return;
        flushSync(() =>
          setLeft((l) => {
            if (!l) return l;
            const next = new Map(l);
            for (const k of keys) if (k !== undefined) next.delete(k);
            return next.size ? next : null;
          }),
        );
      },
      { root: scroller, rootMargin: "100% 0px" },
    );
    for (const [k, el] of els) if (leftNow.current?.has(k)) near.observe(el);
    // Scrolled far at once (the scrollbar, Home, a place gone to): the boxes come near are drawn in
    // the frame of the scroll itself, which is told before it is painted; the observer tells a
    // frame late, and they were seen empty for that frame.
    const scrolled = () => {
      const l = leftNow.current;
      if (!l) return;
      const pane = scroller.getBoundingClientRect();
      const keys: string[] = [];
      for (const k of l.keys()) {
        const q = els.get(k)?.getBoundingClientRect();
        if (q && q.bottom > pane.top - pane.height && q.top < pane.bottom + pane.height) keys.push(k);
      }
      if (!keys.length) return;
      flushSync(() =>
        setLeft((x) => {
          if (!x) return x;
          const next = new Map(x);
          for (const k of keys) next.delete(k);
          return next.size ? next : null;
        }),
      );
    };
    scroller.addEventListener("scroll", scrolled, { passive: true });
    return () => void (near.disconnect(), waiting.delete(scroller), scroller.removeEventListener("scroll", scrolled));
  }, [!some]);

  return (
    <>
      {rows.map((g) => {
        const h = left?.get(g.key);
        return h !== undefined ? <div key={g.key} ref={refOf(g.key)} aria-hidden className={className} style={{ height: h }} /> : <div key={g.key} ref={refOf(g.key)} className={className}>{children(g)}</div>;
      })}
    </>
  );
}
