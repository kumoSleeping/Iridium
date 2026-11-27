// What scrolls as a chat does (docs/动画.md): it opens at its newest, or where it was left. At its
// newest (following), whatever comes moves only the scroll place, eased to the end (a change over
// the last row, at once: the last row stays where it is); scrolled up,
// nothing seen moves (the browser's scroll anchoring, or the page's own where there is none), and
// what comes below shows a key to go down. Whether it follows is the reader's alone: going up by
// wheel, finger, key or scrollbar lets go, coming back to the end takes it again; nothing this
// page scrolls by itself changes it. No row is moved, no height animated, nothing waited for.
import { ArrowDown } from "lucide-react";
import { useEffect, useLayoutEffect, useRef, useState, type CSSProperties, type ReactNode, type RefObject } from "react";
import { flushSync } from "react-dom";
import { t } from "../i18n";
import { motion, reduced } from "../motion";
import { moveLog } from "../motionlog";
import { cn } from "../ui";

/** The follow: the px left to the end shrink by the same share each ms (an ease-out, at rest once,
 *  never past the end), and what comes meanwhile adds to them: it moves from the first frame,
 *  faster the more is left, so pushed again it never stands or slows. One pace for all of it, a
 *  third of a move (motion.css --move): a line written pushes the rest up by a line at once, not a
 *  few lines behind (kumo, 2026-11-25). */
export const approach = (left: number, ms: number, tau: number) => left * Math.exp(-ms / tau);
/** More than the pane's own height left to the end is not a line written: a page come at once (a
 *  tab back in front with all that was said while it was behind, a first screen filled late, a
 *  long message whole). Eased, it ran through all of it, top to bottom (kumo, 2026-11-26: Zen,
 *  back from another tab); it is put at the end at once. */
export const far = (left: number, pane: number) => pane > 0 && left > pane;
/** A person scrolling (a wheel, a finger, a key, the scrollbar) has the scroll to itself this
 *  long after each of its moves: the follow writes no place meanwhile, so a trackpad's glide and
 *  bounce at the end are not cut each frame (kumo, 2026-11-25). */
const HAND = 160;
/** The key to go down, from far up: put this short of the end, the rest eased. */
const NEAR = 96;
/** How far above the end before the scroll-to-bottom button appears without new messages. */
const AWAY = 48;
/** How near the top the page before is put in: two screens, at least 600px. */
const near = (el: HTMLElement) => Math.max(600, el.clientHeight * 2);
/** Whether the browser keeps what is seen in place as what is over it changes (CSS scroll
 *  anchoring: Chromium, Firefox, Safari 27 on). ?noanchor=1 tries the page's own way. */
const anchors = () => typeof CSS !== "undefined" && CSS.supports("overflow-anchor", "auto") && !/[?&]noanchor=1/.test(location.search);

/** Where a chat was left up from its end, kept in this browser by a key (a channel's id) for the
 *  page loaded again (kumo, 2026-11-26: reloaded, it was at its end, or glided there): the row at
 *  the top of what was seen, by its number, and how far under the pane's top its top was. At its
 *  end, nothing kept: it opens at its end (or at the last read). The newest LEFT_MAX kept. */
type Left = { seq: string; off: number; at: number };
const LEFT_KEY = "ir:left1";
const LEFT_MAX = 64;
const lefts = (): Record<string, Left> => {
  try {
    return JSON.parse(localStorage.getItem(LEFT_KEY) ?? "{}") as Record<string, Left>;
  } catch {
    return {};
  }
};
export const leftAt = (key: string): Left | undefined => lefts()[key];
export function keepLeft(key: string, left: Left | null) {
  const all = lefts();
  if (left) all[key] = left;
  else if (all[key]) delete all[key];
  else return;
  const keys = Object.keys(all).sort((a, b) => all[b]!.at - all[a]!.at);
  for (const k of keys.slice(LEFT_MAX)) delete all[k];
  try {
    localStorage.setItem(LEFT_KEY, JSON.stringify(all));
  } catch {
    // A full or closed store: not kept.
  }
}

type Chat = { toEnd(): void; letGo(): void; rests(): boolean; down(by: number): void };
const chats = new WeakMap<HTMLElement, Chat>();
/** A message sent from here: the chat follows again, to its end. */
export const toEnd = (el: HTMLElement | null) => el && chats.get(el)?.toEnd();
/** The chat is taken up by the reader's doing elsewhere (a quote pressed, gone to). */
export const letGo = (el: HTMLElement | null) => el && chats.get(el)?.letGo();
/** The conversation moved down by px, as far as it goes, and let go: room over a row for what opens
 *  over it (a step's card on a phone, work.tsx). */
export const moveDown = (el: HTMLElement | null, by: number) => el && chats.get(el)?.down(by);
/** At its very newest and still: the phone's sheet may be swiped on from it (sheet.tsx). */
export const restsAtEnd = (el: HTMLElement) => chats.get(el)?.rests() ?? false;

/** Whether something of the list (or the list itself) is changing its own height now, by a
 *  transition or an animation of its own. */
const SIZES = ["grid-template-rows", "height", "max-height", "padding-bottom"];
const sizing = (el: HTMLElement) =>
  el.getAnimations({ subtree: true }).some((a) => {
    if (a.playState !== "running") return false;
    const prop = (a as CSSTransition).transitionProperty;
    if (prop) return SIZES.includes(prop);
    const fx = a.effect as KeyframeEffect | null;
    return !!fx?.getKeyframes?.().some((k) => "height" in k || "maxHeight" in k);
  });

/** Whether a box between target and the list scrolls up itself (a long output): the list does not. */
const inner = (target: EventTarget | null, list: HTMLElement) => {
  for (let n = target instanceof Element ? target : null; n && n !== list; n = n.parentElement)
    if (n.scrollTop > 0 && n.scrollHeight > n.clientHeight && /auto|scroll/.test(getComputedStyle(n).overflowY)) return true;
  return false;
};
/** The row at the top of what is seen: the first message row (the column's own rows where there
 *  are none) that begins in view, else the one across its top (not that one first: its own head
 *  goes as the page before joins its group). Found again by its number when drawn anew. */
type Mark = { el: Element; seq?: string; off: number };
const topRow = (el: HTMLElement, col: Element) => {
  const { top, bottom } = el.getBoundingClientRect();
  // Only in the column's rows that reach under the pane's top: every message over it was measured
  // for nothing, and one in a row far off (lazyrows.tsx: not laid out) was laid out to be.
  const under = function* (): Generator<HTMLElement> {
    if (!col.querySelector("[data-seq]")) return yield* col.children as HTMLCollectionOf<HTMLElement>;
    for (const c of col.children) if (c.getBoundingClientRect().bottom > top) yield* c.matches("[data-seq]") ? [c as HTMLElement] : c.querySelectorAll<HTMLElement>("[data-seq]");
  };
  let pick: HTMLElement | null = null;
  for (const r of under()) {
    const b = r.getBoundingClientRect();
    if (!b.height || b.bottom <= top) continue;
    if (b.top < top - 0.5) pick = r;
    else {
      if (b.top < bottom || !pick) pick = r;
      break;
    }
  }
  return pick && { el: pick, seq: pick.dataset.seq, off: pick.getBoundingClientRect().top - col.getBoundingClientRect().top };
};
/** Where m's row is in the column now (null: gone). */
const offNow = (m: Mark, col: Element) => {
  const el = m.el.isConnected ? m.el : m.seq ? col.querySelector(`[data-seq="${m.seq}"]`) : null;
  return el && el.getBoundingClientRect().top - col.getBoundingClientRect().top;
};

/** The conversation's last row (the column's last child: a run, a bubble) and where it begins in
 *  the column: what changes height over it is not at the end. */
type Foot = { el: Element; at: number; top?: number };
const offIn = (el: Element, col: Element) => el.getBoundingClientRect().top - col.getBoundingClientRect().top;
const footOf = (col: Element | null): Foot | null => {
  const el = col?.lastElementChild;
  return el ? { el, at: offIn(el, col) } : null;
};
/** Following, how far a change over the row that was last moved it (at: where it begins now, null
 *  gone): that much the scroll goes at once, not eased, so the rows at the end stay where they are;
 *  eased, they went down with each line written over them and came back up, again and again (an
 *  agent streaming over others working under it, kumo, 2026-11-26). What came after it is left to
 *  the follow, eased as before. */
export const overFoot = (was: Foot | null, at: number | null) => (was && at !== null && Math.abs(at - was.at) > 0.5 ? at - was.at : 0);

/** tail: changes as the conversation grows at its end. quiet: no key to go down (an ask under
 *  it). draw: draws the page now (the page before, once older brought it). keep: where it is
 *  left is kept in this browser under it, and opened at again as the page is loaded anew. */
export function Stick({ tail, older, draw, place, style, children, scroller, bottom, quiet, openAt, late, onFollow, keep: keepKey }: { keep?: string; tail: unknown; older: () => Promise<boolean>; draw: () => void; place: { top: number | null; opened?: boolean }; openAt?: number; late?: number; onFollow?: (on: boolean) => void; style?: CSSProperties; children: ReactNode; scroller?: RefObject<HTMLDivElement | null>; bottom: number; quiet?: boolean }) {
  const own = useRef<HTMLDivElement>(null);
  const list = scroller ?? own;
  const [fresh, setFresh] = useState(false);
  const [away, setAway] = useState(false);
  const [, redraw] = useState(0);
  // following: the reader's; left: px to the end as this last put it; ours: where this put the
  // scroll (to the part of a px); mark: without the browser's anchoring, the top row and its place.
  // Loaded anew (nothing held for it in this page yet): where this browser had it left.
  const [stored] = useState(() => (keepKey && place.top === null && !place.opened ? leftAt(keepKey) : undefined));
  const s = useRef({ following: place.top === null && !stored, frame: 0, last: 0, ours: -1, left: 0, pane: 0, drawn: false, tail: undefined as unknown, paging: false, drag: false, native: true, mark: null as Mark | null, foot: null as Foot | null, hand: -Infinity, sentAt: -Infinity }).current;

  const maxOf = (el: HTMLElement) => el.scrollHeight - el.clientHeight;
  const hands = () => s.drag || performance.now() - s.hand < HAND;
  const atFoot = (el: HTMLElement) => maxOf(el) - el.scrollTop < 2;
  const put = (el: HTMLElement, top: number) => ((el.scrollTop = top), (s.ours = top), s.foot && (s.foot.top = el.scrollTop));
  /** Px still to go to the end: from where this put it, unless something else moved it since. */
  const leftOf = (el: HTMLElement) => maxOf(el) - (Math.abs(el.scrollTop - s.ours) <= 1 ? s.ours : el.scrollTop);
  /** Following: what changed height over the last row moved it by as much as the scroll, at once. */
  const under = (el: HTMLElement) => {
    const col = el.firstElementChild, was = s.foot;
    const d = s.following && col && was?.el.parentElement === col ? overFoot(was, offIn(was.el, col)) : 0;
    s.foot = footOf(col);
    if (s.foot) s.foot.top = el.scrollTop;
    if (!d) return;
    // Shorter over it at the very end, the browser has put the scroll back as far already.
    const from = was!.top !== undefined && was!.top > el.scrollTop && maxOf(el) - el.scrollTop < 1 ? was!.top : el.scrollTop;
    moveLog("stick.under", "over the last row", { dy: -Math.round(d) });
    put(el, from + d);
  };
  const mark = (el: HTMLElement) => void (s.mark = s.native || !el.firstElementChild ? null : topRow(el, el.firstElementChild));
  /** Without the browser's anchoring: what changed over the row at the top moves it nowhere. */
  const keep = (el: HTMLElement) => {
    const m = s.mark;
    const now = m && offNow(m, el.firstElementChild!);
    if (!m || now == null || Math.abs(now - m.off) < 0.5) return;
    moveLog("stick.keep", "over", { dy: 0, note: `${Math.round(now - m.off)}px over the top row` });
    put(el, el.scrollTop + now - m.off);
    m.off = now;
  };
  const stop = () => (cancelAnimationFrame(s.frame), (s.frame = s.left = 0));
  const pin = (el: HTMLElement, why: string) => (Math.abs(leftOf(el)) > 0.5 && moveLog("stick.pin", why, { dy: -Math.round(leftOf(el)) }), stop(), put(el, maxOf(el)));
  // The follow, a frame at a time while there are px left to the end.
  const step = () => {
    s.frame = 0;
    const el = list.current;
    if (!el || !s.following) return;
    keep(el);
    under(el);
    // By the time it is drawn at, at most two frames' worth after the page was busy: the way left
    // is gone on with, not jumped.
    const now = performance.now();
    const dt = Math.min(32, Math.max(1, now - s.last));
    s.last = now;
    const y = leftOf(el);
    if (y > s.left + 0.5) moveLog("stick.follow", "grew", { dy: -Math.round(y - s.left) });
    if (y <= 0.5 || reduced() || document.hidden) return void (y > 0.5 ? pin(el, "reduced") : stop());
    if (far(y, el.clientHeight) && !hands()) return void pin(el, "far");
    // In a person's hands: the way left kept as it is, gone on with once they let go.
    if (hands()) return void ((s.left = y), (s.frame = requestAnimationFrame(step)));
    s.left = approach(y, dt, motion.move() / 3);
    if (s.left < 0.5) s.left = 0;
    put(el, maxOf(el) - s.left);
    mark(el);
    s.frame = requestAnimationFrame(step);
  };
  const run = () => void (!s.frame && s.following && ((s.last = performance.now()), (s.frame = requestAnimationFrame(step))));
  const told = useRef(onFollow);
  told.current = onFollow;
  const syncAway = (el: HTMLElement) => setAway(!s.following && maxOf(el) - el.scrollTop > AWAY);
  const leave = () => {
    if (s.following) moveLog("stick.leave", "reader");
    s.following = false;
    stop();
    told.current?.(false);
    if (list.current) syncAway(list.current);
  };
  const follow = () => ((s.following = true), setFresh(false), setAway(false), told.current?.(true));
  /** What came is not gone down to (kumo, 2026-11-26: said while the reader was away, it is theirs
   *  to go to): the view stays, the key to go down shown. */
  const stay = (why: string) => (moveLog("stick.stay", why), (s.following = false), stop(), setFresh(true), told.current?.(false));
  const lateWas = useRef(late);
  /** To the end, eased from no farther than NEAR (the key pressed, a message sent). */
  const goDown = (why: string) => {
    const el = list.current;
    if (!el) return;
    if (why === "send") s.sentAt = performance.now();
    if (!s.following && maxOf(el) - el.scrollTop > NEAR) (moveLog("stick.jump", why, { dy: -Math.round(maxOf(el) - NEAR - el.scrollTop) }), put(el, maxOf(el) - NEAR));
    follow();
    run();
  };

  /** Where it is left, kept for the page loaded anew (keep): nothing at its end; nothing while
   *  none of its rows is drawn (the first screen held back), which would forget it. */
  const saveLeft = () => {
    const el = list.current;
    if (!keepKey || !el || !el.querySelector("[data-seq]")) return;
    if (s.following || atFoot(el)) return keepLeft(keepKey, null);
    const m = el.firstElementChild && topRow(el, el.firstElementChild);
    if (m?.seq) keepLeft(keepKey, { seq: m.seq, off: m.el.getBoundingClientRect().top - el.getBoundingClientRect().top, at: Date.now() });
  };
  const saving = useRef(0);

  const back = async () => {
    const el = list.current;
    if (!el || s.paging) return;
    s.paging = true;
    try {
      while (el.isConnected && el.scrollTop < near(el) && (await older())) {
        // What is seen stays where it is, in both ways: the row at the top read before and after
        // it is drawn, the scroll moved by as much (the browser's anchoring off meanwhile).
        const col = el.firstElementChild!;
        const m = topRow(el, col);
        const way = el.style.overflowAnchor;
        el.style.overflowAnchor = "none";
        flushSync(() => (draw(), redraw((n) => n + 1)));
        const now = m && offNow(m, col);
        const d = m && now != null ? now - m.off : 0;
        if (d) put(el, el.scrollTop + d);
        el.style.overflowAnchor = way;
        moveLog("stick.back", "page", { dy: 0, note: `${Math.round(d)}px over` });
        mark(el);
        // The page before is over the last row too, and kept for already.
        s.foot = footOf(col);
      }
    } finally {
      s.paging = false;
    }
  };

  useLayoutEffect(() => {
    const el = list.current!;
    const was = s.tail;
    s.tail = tail;
    if (!s.drawn) {
      // Opened: drawn at its newest, or where it was left, in the first frame. With nothing in it
      // yet (its rows read late), the frame they come in is that first one: not followed down to
      // from the top.
      s.drawn = !!el.firstElementChild?.firstElementChild;
      // First opened with messages not read yet (kumo, 2026-11-26): at the last one read, its
      // foot at the pane's, as it was left; not at the newest.
      const first = !place.opened && !!el.querySelector("[data-seq]");
      const kept = first && stored ? el.querySelector<HTMLElement>(`[data-seq="${stored.seq}"]`) : null;
      let row: HTMLElement | null = null;
      if (first && s.following && openAt !== undefined) for (let n = openAt; !row && n > openAt - 4 && n > 0; n--) row = el.querySelector<HTMLElement>(`[data-seq="${n}"]`);
      if (first) place.opened = true;
      const to = row ? row.getBoundingClientRect().bottom - el.getBoundingClientRect().top + el.scrollTop - (el.clientHeight - parseFloat(getComputedStyle(el).paddingBottom || "0")) + 16 : Infinity;
      if (kept) {
        // Where it was left, its row as far under the pane's top as it was; the reader's to scroll.
        put(el, Math.min(maxOf(el), Math.max(0, el.scrollTop + kept.getBoundingClientRect().top - el.getBoundingClientRect().top - stored!.off)));
        if (atFoot(el)) follow();
        else ((s.following = false), told.current?.(false), syncAway(el));
      } else if (first && stored) {
        // Its row not here (gone, or older than the page has): at the end, as any.
        if (!s.following) follow();
        put(el, maxOf(el));
      } else if (row && to < maxOf(el) - 48) (put(el, Math.max(0, to)), stay("unread"));
      else if (s.following) put(el, maxOf(el));
      else ((el.scrollTop = place.top!), syncAway(el));
    } else if (s.following && late !== lateWas.current) stay("late");
    // Late is looked at first: a message written on in this device's copy brings its steps alone,
    // the tail the same, and it was followed down to (kumo, 2026-11-26).
    else if (was === tail) return;
    else if (!s.following) atFoot(el) ? follow() : setFresh(true);
    lateWas.current = late;
    // The page before is asked for near the top: opened, or read up there. Not at each draw while
    // it follows its end, where asking reads the layout the draw has just changed, once more a
    // frame (a stream drawn many times a second; scrolled up, the scroll asks).
    if (!s.following || !was) void back().catch(() => {});
  });

  useLayoutEffect(() => {
    const el = list.current!;
    s.native = anchors();
    if (!s.native) el.style.overflowAnchor = "none";
    s.pane = el.clientHeight;
    mark(el);
    // What changed height, after the layout, before it is drawn: kept in place, or followed; the
    // pane itself another height (the keyboard, the window): at the end at once.
    const ro = new ResizeObserver(() => {
      keep(el);
      under(el);
      const pane = el.clientHeight !== s.pane;
      s.pane = el.clientHeight;
      // Something in it draws its own change of height (a list of steps opening, words folding,
      // the room under it for the composer): the end stays where it is each frame, so what is under
      // the change does not move and what is over it does, both ways alike (kumo, 2026-11-24).
      // Not held off by a person's scrolling: a trackpad's glide still sends wheels after it, and
      // the end went down and came back (kumo, 2026-11-25: 贴底时直接往上).
      if (s.following) pane ? pin(el, "pane") : sizing(el) ? pin(el, "own") : far(leftOf(el), el.clientHeight) && !hands() ? pin(el, "far") : run();
      else syncAway(el);
      mark(el);
    });
    ro.observe(el);
    if (el.firstElementChild) ro.observe(el.firstElementChild);
    chats.set(el, { toEnd: () => goDown("send"), letGo: leave, rests: () => s.following && !s.frame && atFoot(el), down: (by) => (leave(), put(el, Math.max(0, el.scrollTop - by)), syncAway(el)) });
    // The reader's way up, seen before the scroll comes; down at the very end, where none comes.
    const wheel = (e: WheelEvent) => {
      s.hand = performance.now();
      if (Math.abs(e.deltaY) <= Math.abs(e.deltaX)) return;
      if (e.deltaY < 0 && el.scrollTop > 0 && !inner(e.target, el)) leave();
      else if (e.deltaY > 0 && !s.following && atFoot(el)) follow();
    };
    let y = 0;
    const touch = (e: TouchEvent) => {
      const p = e.touches[0];
      if (!p) return;
      s.hand = performance.now();
      if (e.type === "touchmove" && p.clientY - y > 2 && el.scrollTop > 0 && !inner(e.target, el)) leave();
      else if (e.type === "touchmove" && y - p.clientY > 2 && !s.following && atFoot(el)) follow();
      y = p.clientY;
    };
    const key = (e: KeyboardEvent) => !(e.target as Element).closest?.("textarea, input") && (["PageUp", "ArrowUp", "Home"].includes(e.key) || (e.key === " " && e.shiftKey)) && leave();
    // The scrollbar: pressed on the list itself, not on what is in it.
    const press = (e: Event) => void (s.drag = e.target === el);
    const lift = () => void (s.drag = false);
    const on: [EventTarget, string, (e: never) => void][] = [[el, "wheel", wheel], [el, "touchstart", touch], [el, "touchmove", touch], [el, "keydown", key], [el, "pointerdown", press], [window, "pointerup", lift], [window, "pointercancel", lift]];
    for (const [at, k, f] of on) at.addEventListener(k, f as EventListener, { passive: true, capture: at === window });
    // Behind another tab nothing is drawn: no frame, no change of height told, so what was said
    // meanwhile was followed by nothing, and all of it eased through as the tab came back. In front
    // again (or taken out of the browser's keep of pages left), following: at the end before the
    // first frame.
    const shown = () => void (s.following && !document.hidden && ((s.last = performance.now()), pin(el, "shown")));
    const hide = () => (document.visibilityState === "hidden" ? saveLeft() : shown());
    window.addEventListener("pagehide", saveLeft);
    window.addEventListener("pageshow", shown);
    document.addEventListener("visibilitychange", hide);
    return () => {
      clearTimeout(saving.current);
      saveLeft();
      window.removeEventListener("pagehide", saveLeft);
      window.removeEventListener("pageshow", shown);
      document.removeEventListener("visibilitychange", hide);
      ro.disconnect();
      stop();
      for (const [at, k, f] of on) at.removeEventListener(k, f as EventListener, { capture: at === window });
      chats.delete(el);
      place.top = s.following || atFoot(el) ? null : el.scrollTop;
    };
  }, []);

  // The room under it (the composer's height) glides to its new size, taller or shorter alike,
  // once it has been drawn: not as it opens.
  const [eased, setEased] = useState(false);
  useEffect(() => {
    const f = requestAnimationFrame(() => setEased(true));
    return () => cancelAnimationFrame(f);
  }, []);


  // A message just sent: the composer is shorter at once (its words gone, the quote over it gone),
  // and the room under the conversation with it, at once too, not glided: gliding, the end was
  // kept each frame (pin), so the bubble came all the way up in one frame and then sank as the
  // room closed, higher and then lower, and not at a message's own pace (kumo, 2026-11-26). At
  // once, what is left to go is the bubble's height less the room given back, eased as any is.
  const sending = performance.now() - s.sentAt < 500;

  const scrolled = () => {
    const el = list.current!;
    keep(el);
    // The scrollbar dragged up lets go; the end reached again, by any way, follows.
    if (s.following && s.drag && el.scrollTop < s.ours - 1) leave();
    else if (!s.following && atFoot(el)) follow();
    else syncAway(el);
    mark(el);
    // Following, it is at its end (gone up, it lets go first): nothing read for the page before.
    if (!s.following) void back().catch(() => {});
    if (keepKey && !saving.current) saving.current = window.setTimeout(() => ((saving.current = 0), saveLeft()), 250);
  };

  return (
    <>
      {/* A scrollbar that takes room has its width kept on the left too: the conversation stays
          centred, in line with the composer and the top bar (the user, 2026-11-06). */}
      <div ref={list} role="log" aria-label={t("chat.log")} onScroll={scrolled} className={cn("gapless absolute inset-0 overflow-y-auto [scrollbar-gutter:stable_both-edges]", eased && !sending && "g-room")} style={style}>
        {children}
      </div>
      {(fresh || away) && !quiet && (
        <button
          type="button"
          onClick={() => goDown("key")}
          aria-label={fresh ? t("chat.newer") : t("chat.bottom")}
          className={cn(
            "g-pop g-press glass thick float absolute left-1/2 z-10 flex h-8 -translate-x-1/2 items-center justify-center rounded-full text-xs font-medium text-ink",
            fresh ? "gap-1.5 px-3" : "w-8 pointer-coarse:size-9",
          )}
          style={{ bottom: bottom + 10 }}
        >
          {fresh && t("chat.newer")}
          <ArrowDown size={fresh ? 13 : 15} />
        </button>
      )}
    </>
  );
}
