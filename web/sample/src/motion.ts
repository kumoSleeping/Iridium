// The script half of the motion system (motion.css has the tokens and the CSS gestures): FLIP for rows
// that move or appear, and the send flight. Both read their timings from the CSS tokens and move only
// transform and opacity; with reduced motion they fall back to a short fade.
import { useLayoutEffect, useRef, type RefObject } from "react";

const reduced = () => matchMedia("(prefers-reduced-motion: reduce)").matches;

function token(name: string, fallback: string) {
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim() || fallback;
}
const ms = (v: string) => (v.endsWith("ms") ? parseFloat(v) : parseFloat(v) * 1000);

export const motion = {
  dur: (n: 1 | 2 | 3 | 4) => ms(token(`--dur-${n}`, ["120ms", "200ms", "280ms", "420ms"][n - 1])),
  ease: (k: "out" | "in" | "spring") => token(`--ease-${k}`, "ease-out"),
};

/** Reorder / unfold / fold for a list: rows marked data-flip="<key>" inside ref glide from their old
 *  place to the new one; rows that are new rise in. Positions are offsets inside the container, so
 *  scrolling does not count as movement. */
export function useFlip(ref: RefObject<HTMLElement | null>, dep: unknown) {
  const last = useRef(new Map<string, number>());
  const first = useRef(true);
  useLayoutEffect(() => {
    const root = ref.current;
    if (!root) return;
    const now = new Map<string, number>();
    const rows = root.querySelectorAll<HTMLElement>("[data-flip]");
    const dur = motion.dur(3);
    const ease = motion.ease("out");
    const calm = reduced();
    rows.forEach((el) => {
      const key = el.dataset.flip!;
      const top = el.offsetTop;
      now.set(key, top);
      if (first.current) return;
      const was = last.current.get(key);
      if (was === undefined) {
        el.animate(calm ? [{ opacity: 0 }, { opacity: 1 }] : [{ opacity: 0, transform: "translateY(-4px)" }, { opacity: 1, transform: "none" }], {
          duration: calm ? motion.dur(1) : motion.dur(2),
          easing: ease,
          delay: calm ? 0 : 40,
          fill: "backwards",
        });
      } else if (was !== top && !calm) {
        el.animate([{ transform: `translateY(${was - top}px)` }, { transform: "none" }], { duration: dur, easing: ease });
      }
    });
    first.current = false;
    last.current = now;
  }, [dep, ref]);
}

/** Send: a copy of the new bubble starts where the text sat in the composer and flies to the bubble's
 *  place; the bubble shows when it lands. from: the composer text's top-left corner. */
export function flySend(bubble: HTMLElement, from: { x: number; y: number }) {
  const to = bubble.getBoundingClientRect();
  if (reduced() || to.width === 0) {
    bubble.animate([{ opacity: 0 }, { opacity: 1 }], { duration: motion.dur(1) });
    return;
  }
  const clone = bubble.cloneNode(true) as HTMLElement;
  const pad = parseFloat(getComputedStyle(bubble).paddingLeft) || 0;
  const padT = parseFloat(getComputedStyle(bubble).paddingTop) || 0;
  Object.assign(clone.style, {
    position: "fixed",
    left: `${to.left}px`,
    top: `${to.top}px`,
    width: `${to.width}px`,
    height: `${to.height}px`,
    margin: "0",
    zIndex: "60",
    pointerEvents: "none",
    transformOrigin: "0 0",
  });
  clone.removeAttribute("data-flip");
  document.body.appendChild(clone);
  bubble.style.opacity = "0";
  const dx = from.x - (to.left + pad);
  const dy = from.y - (to.top + padT);
  const dur = motion.dur(4);
  const bg = clone.querySelector<HTMLElement>("[data-bubble-bg]");
  bg?.animate([{ opacity: 0 }, { opacity: 1 }], { duration: dur * 0.6, easing: motion.ease("out"), fill: "both" });
  const a = clone.animate([{ transform: `translate(${dx}px, ${dy}px) scale(0.98)` }, { transform: "none" }], {
    duration: dur,
    easing: motion.ease("spring"),
  });
  a.onfinish = a.oncancel = () => {
    bubble.style.opacity = "";
    clone.remove();
  };
}
