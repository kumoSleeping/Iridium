// A picture over the page, to look closer (otae-c, 2026-11-22, as the Codex app's viewer): it
// opens fit to the window, the file itself; Ctrl + wheel or a trackpad's pinch zooms at the
// pointer, two fingers on a phone too; zoomed, the wheel and a drag move it; a double press goes
// to its own pixels (or twice its fit) and back. A press beside it shuts it; on it, only at fit
// and not after a drag. The scale, while zoomed, at the foot: pressed, back to fit.
import { useCallback, useEffect, useRef, useState, type PointerEvent as ReactPointerEvent, type ReactNode } from "react";
import { cn } from "../ui";
import { bound, maxScale, ORIGIN, zoomAt, type Pt } from "./zoom-lib";

type Pointer = { x: number; y: number };

/** corner: keys at the top right of the picture as it shows, kept inside the window. */
export function Viewer({ src, name, close, corner }: { src: string; name: string; close: () => void; corner?: ReactNode }) {
  const stage = useRef<HTMLDivElement>(null);
  const img = useRef<HTMLImageElement>(null);
  const [scale, setScale] = useState(1);
  const [offset, setOffset] = useState<Pt>(ORIGIN);
  // Moved by hand now (a drag, two fingers, the wheel): no easing, it follows.
  const [live, setLive] = useState(false);
  const [fit, setFit] = useState(0); // the fit's share of the picture's own pixels
  // The picture fit (w×h) and the window (vw×vh), as last laid out.
  const [box, setBox] = useState({ w: 0, h: 0, vw: 0, vh: 0 });
  const now = useRef({ scale: 1, offset: ORIGIN });
  now.current = { scale, offset };
  const pointers = useRef(new Map<number, Pointer>());
  // on: it began on the picture (once captured, the stage is every event's target).
  const gesture = useRef<{ from: Pointer; offset: Pt; dist?: number; scale?: number; moved: boolean; on?: boolean } | null>(null);
  const lastTap = useRef<{ t: number; x: number; y: number } | null>(null);
  const wheelEnd = useRef<ReturnType<typeof setTimeout>>(undefined);

  /** A point on screen as its distance from the window's middle. */
  const fromMiddle = (x: number, y: number): Pt => {
    const r = stage.current!.getBoundingClientRect();
    return { x: x - r.left - r.width / 2, y: y - r.top - r.height / 2 };
  };
  const set = useCallback((s: number, o: Pt) => {
    const el = img.current;
    const st = stage.current;
    if (!el || !st) return;
    const ratio = el.naturalWidth ? el.offsetWidth / el.naturalWidth : 1;
    const next = Math.min(Math.max(s, 1), maxScale(ratio));
    const off = next <= 1 ? ORIGIN : bound(o, next, el.offsetWidth, el.offsetHeight, st.clientWidth, st.clientHeight);
    now.current = { scale: next, offset: off };
    setScale(next);
    setOffset(off);
  }, []);
  /** Zoom to s, the point at (from the window's middle) staying where it is. */
  const zoomTo = useCallback(
    (s: number, at: Pt) => {
      const { scale: from, offset: o } = now.current;
      const el = img.current;
      const top = el?.naturalWidth ? maxScale(el.offsetWidth / el.naturalWidth) : 5;
      const to = Math.min(Math.max(s, 1), top);
      set(to, zoomAt(o, from, to, at));
    },
    [set],
  );
  const measure = () => {
    const el = img.current;
    const st = stage.current;
    if (el?.naturalWidth) setFit(el.offsetWidth / el.naturalWidth);
    if (el && st) setBox({ w: el.offsetWidth, h: el.offsetHeight, vw: st.clientWidth, vh: st.clientHeight });
  };

  // The wheel is taken from the page (not passive): Ctrl (a pinch) zooms, otherwise, zoomed, moves.
  // Safari's trackpad pinch comes as gesture events.
  useEffect(() => {
    const st = stage.current;
    if (!st) return;
    const busy = () => {
      setLive(true);
      clearTimeout(wheelEnd.current);
      wheelEnd.current = setTimeout(() => setLive(false), 150);
    };
    const wheel = (e: WheelEvent) => {
      const unit = e.deltaMode === WheelEvent.DOM_DELTA_LINE ? 16 : e.deltaMode === WheelEvent.DOM_DELTA_PAGE ? st.clientHeight : 1;
      if (e.ctrlKey) {
        e.preventDefault();
        busy();
        // A trackpad's pinch comes in small steps; a wheel's notch (about 100) at most a third more.
        const d = Math.min(Math.max(e.deltaY * unit, -30), 30);
        zoomTo(now.current.scale * Math.exp(-0.01 * d), fromMiddle(e.clientX, e.clientY));
        return;
      }
      if (now.current.scale <= 1) return;
      e.preventDefault();
      busy();
      const o = now.current.offset;
      set(now.current.scale, { x: o.x - e.deltaX * unit, y: o.y - e.deltaY * unit });
    };
    let base = 1;
    const gStart = (e: Event) => (e.preventDefault(), (base = now.current.scale));
    const gChange = (e: Event) => {
      const g = e as Event & { scale: number; clientX: number; clientY: number };
      e.preventDefault();
      busy();
      zoomTo(base * g.scale, fromMiddle(g.clientX, g.clientY));
    };
    st.addEventListener("wheel", wheel, { passive: false });
    st.addEventListener("gesturestart", gStart);
    st.addEventListener("gesturechange", gChange);
    const re = new ResizeObserver(() => (measure(), set(now.current.scale, now.current.offset)));
    re.observe(st);
    return () => {
      st.removeEventListener("wheel", wheel);
      st.removeEventListener("gesturestart", gStart);
      st.removeEventListener("gesturechange", gChange);
      re.disconnect();
      clearTimeout(wheelEnd.current);
    };
  }, [set, zoomTo]);

  const down = (e: ReactPointerEvent<HTMLDivElement>) => {
    if (e.button !== 0 && e.button !== 1) return;
    if ((e.target as Element).closest("button, a")) return;
    pointers.current.set(e.pointerId, { x: e.clientX, y: e.clientY });
    e.currentTarget.setPointerCapture(e.pointerId);
    const ps = [...pointers.current.values()];
    if (ps.length === 2) {
      const [a, b] = ps as [Pointer, Pointer];
      gesture.current = { from: { x: (a.x + b.x) / 2, y: (a.y + b.y) / 2 }, offset: now.current.offset, dist: Math.hypot(a.x - b.x, a.y - b.y), scale: now.current.scale, moved: true };
      setLive(true);
    } else if (ps.length === 1) gesture.current = { from: { x: e.clientX, y: e.clientY }, offset: now.current.offset, moved: false, on: e.target === img.current };
  };
  const move = (e: ReactPointerEvent<HTMLDivElement>) => {
    if (!pointers.current.has(e.pointerId)) return;
    pointers.current.set(e.pointerId, { x: e.clientX, y: e.clientY });
    const g = gesture.current;
    if (!g) return;
    const ps = [...pointers.current.values()];
    if (ps.length >= 2 && g.dist) {
      const [a, b] = ps as [Pointer, Pointer];
      const mid = { x: (a.x + b.x) / 2, y: (a.y + b.y) / 2 };
      const s = g.scale! * (Math.hypot(a.x - b.x, a.y - b.y) / g.dist);
      // Zoomed at where the fingers began, then carried as their middle moves.
      const start = fromMiddle(g.from.x, g.from.y);
      const o = zoomAt(g.offset, g.scale!, Math.max(1, s), start);
      set(s, { x: o.x + mid.x - g.from.x, y: o.y + mid.y - g.from.y });
      return;
    }
    const dx = e.clientX - g.from.x;
    const dy = e.clientY - g.from.y;
    if (!g.moved && Math.hypot(dx, dy) < 4) return;
    g.moved = true;
    if (now.current.scale <= 1) return;
    setLive(true);
    set(now.current.scale, { x: g.offset.x + dx, y: g.offset.y + dy });
  };
  const up = (e: ReactPointerEvent<HTMLDivElement>) => {
    if (!pointers.current.delete(e.pointerId)) return;
    const g = gesture.current;
    if (pointers.current.size > 0) {
      // One finger of two lifted: the other goes on moving it, from here.
      const [p] = [...pointers.current.values()] as [Pointer];
      gesture.current = { from: p, offset: now.current.offset, moved: true };
      return;
    }
    gesture.current = null;
    setLive(false);
    if (!g || g.moved || e.type === "pointercancel") return;
    // A press: two close together zoom; one beside the picture, or on it at fit, shuts it.
    const t = e.timeStamp;
    const last = lastTap.current;
    if (last && t - last.t < 300 && Math.hypot(e.clientX - last.x, e.clientY - last.y) < 30) {
      lastTap.current = null;
      clearTimeout(shutLater.current);
      const el = img.current!;
      const own = el.naturalWidth ? el.naturalWidth / el.offsetWidth : 2;
      if (now.current.scale > 1) set(1, ORIGIN);
      else zoomTo(own > 1.1 ? own : 2, fromMiddle(e.clientX, e.clientY));
      return;
    }
    lastTap.current = { t, x: e.clientX, y: e.clientY };
    const onPicture = !!g.on;
    if (!onPicture || now.current.scale <= 1) {
      // Shut after a moment, unless the press is the first of two.
      clearTimeout(shutLater.current);
      shutLater.current = setTimeout(close, onPicture ? 300 : 0);
    }
  };
  const shutLater = useRef<ReturnType<typeof setTimeout>>(undefined);
  useEffect(() => () => clearTimeout(shutLater.current), []);

  const zoomed = scale > 1.001;
  return (
    <div
      ref={stage}
      onPointerDown={down}
      onPointerMove={move}
      onPointerUp={up}
      onPointerCancel={up}
      className={cn("absolute inset-0 grid touch-none place-items-center overflow-hidden select-none", zoomed && (live ? "cursor-grabbing" : "cursor-grab"))}
    >
      <img
        ref={img}
        src={src}
        alt={name}
        draggable={false}
        onLoad={measure}
        style={{ transform: zoomed ? `translate(${offset.x}px, ${offset.y}px) scale(${scale})` : undefined }}
        className={cn("block max-h-[calc(100dvh-32px)] max-w-[calc(100vw-32px)] rounded-xl object-contain shadow-(--shadow-float) [will-change:transform]", !live && "transition-transform duration-150 ease-out")}
      />
      {corner && box.w > 0 && (
        <div
          className={cn("absolute z-10 flex -translate-x-full gap-1.5", !live && "transition-[left,top] duration-150 ease-out")}
          style={{ left: Math.min(box.vw / 2 + offset.x + (box.w * scale) / 2, box.vw) - 8, top: Math.max(box.vh / 2 + offset.y - (box.h * scale) / 2, 0) + 8 }}
        >
          {corner}
        </div>
      )}
      {zoomed && fit > 0 && (
        <button type="button" onClick={() => set(1, ORIGIN)} className="glass g-pop absolute bottom-4 left-1/2 h-8 -translate-x-1/2 rounded-full px-3 text-sm text-ink-2 tabular-nums hover:text-ink">
          {Math.round(fit * scale * 100)}%
        </button>
      )}
    </div>
  );
}
