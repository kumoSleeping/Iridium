// The splash (kumo, 2026-11-25): what a channel with nothing said in it yet, and the pane with
// no channel open, show in their middle: a band of small code drawing Iridium's letters, or the
// agent pointed at in the + panel, or the channel's own once it has one (engine.ts). It is
// behind everything and takes no press; it goes as the first message comes.
import { useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore } from "react";
import type { Agent } from "../api/types.gen";
import { useFaceSrc } from "../chat/channel/face";
import { useRoles } from "../roles";
import { cn, PHONE, useMedia } from "../ui";
import { splash, type Splash as Engine } from "./engine";
import type { Cast } from "./slice";
import { justMade } from "../chat/channel/adding";
import { onPoint, pointedAt, powerNow, stage, stagedNow, STANDARD, type Fig } from "./figure";

// What the little game throws up (slice.ts): the roles kept, by their faces, and each agent's mark.
const MARKS: Cast[] = (["claude", "codex", "pi", "dsh", "agy"] as const).map((agent) => ({ agent }));

/** show: false, it fades and is gone. fig: what it draws when no row of the + panel is pointed
 *  at; none: Iridium's letters. */
export function Splash({ show = true, fig = null, scene = "", className }: { show?: boolean; fig?: Fig | null; scene?: string; className?: string }) {
  const [there, setThere] = useState(show);
  if (show && !there) setThere(true);
  const pointed = useSyncExternalStore(onPoint, pointedAt);
  const still = useMedia("(prefers-reduced-motion: reduce)");
  const cv = useRef<HTMLCanvasElement>(null);
  const showRef = useRef(show);
  showRef.current = show;
  const eng = useRef<Engine | null>(null);
  // With no agent pointed at or in the channel, Iridium's letters, always (kumo, 2026-11-25: the
  // setting with its other kinds gone). An agent pointed at or added: that one's face.
  const now = show ? (pointed ?? fig) : fig;
  const power = useSyncExternalStore(onPoint, powerNow);
  // The portrait layout with the + panel there: the band under the panel, over the composer.
  const phone = useMedia(PHONE);
  const fitRef = useRef(() => {});
  const phoneRef = useRef(phone);
  phoneRef.current = phone;
  const nowRef = useRef(now);
  nowRef.current = now;
  // A face is drawn (a picture, not letters or a mark): the pane is laid out for it (fit).
  const face = !!now?.src;
  const faceRef = useRef(face);
  faceRef.current = face;
  useEffect(() => fitRef.current(), [face]);
  const roles = useRoles();
  useEffect(() => eng.current?.cast([...roles.filter((r) => r.avatar).slice(0, 6).map((r) => ({ src: r.avatar!, agent: r.agent })), ...MARKS]), [roles, there, still, phone]);
  // Before the frame it is first in: one band going as another comes (the empty pane's, a new
  // channel's) is the same characters with no frame between them.
  useLayoutEffect(() => {
    const el = cv.current;
    if (!el) return;
    const e = splash(el, still, phone);
    eng.current = e;
    e.figure(nowRef.current);
    e.resize();
    // The canvas is the whole pane; the band is the window of it that is seen (engine.ts), over the
    // composer, and stays there whatever opens over it (kumo, 2026-11-25: it is not to move about). The figure in its middle, whole: in the portrait
    // layout as large as the band has room for.
    // The composer's top (a chat's, or the empty pane's box), as far down the canvas as it is.
    const composer = () => el.parentElement?.parentElement?.querySelector<HTMLElement>("[data-sheet-pull]") ?? document.querySelector<HTMLElement>("main [data-sheet-pull]");
    const fit = () => {
      e.resize();
      const w = el.clientWidth, pane = el.clientHeight, ph = phoneRef.current;
      const h = ph ? Math.min(460, Math.max(260, pane * 0.56)) : Math.min(420, Math.max(220, pane * 0.46));
      const box = composer()?.firstElementChild ?? composer();
      const top = box ? box.getBoundingClientRect().top - el.getBoundingClientRect().top : pane - 76;
      // In the portrait layout low in the pane, the figure standing on the composer: the lowest
      // edge of what it draws (Ir's letters' foot) just over the composer's top, as on a line. On a
      // computer in the middle of what is over the composer (kumo, 2026-11-25).
      // On a computer (kumo, 2026-11-25): Iridium's letters (or a mark) in a band across the middle;
      // a face over the whole pane, the code about it going to none (engine.ts), a little smaller.
      const face = !!faceRef.current;
      // The band a little taller than the figure, above and below it (kumo, 2026-11-25: room for
      // the stars and rings the pointer colours in it); on the phone still ending on the composer.
      const bh = Math.min(ph ? top + 28 : top - 48, h * 1.4);
      e.window(ph ? top + 28 - bh / 2 : face ? pane / 2 : (48 + top) / 2, ph ? bh : face ? pane * 2 : bh);
      e.place(ph ? w / 2 : -1, ph ? Math.min(w * 0.94, h * 0.98) : Math.min(h * 0.92, w * 0.7) * (face ? 0.82 : 1), ph ? -1 : (48 + top) / 2);
      e.foot(ph ? top - 6 : -1);
      e.edge(ph || !face);
    };
    fitRef.current = fit;
    fit();
    const ro = new ResizeObserver(fit);
    ro.observe(el);
    const stack = composer();
    if (stack) ro.observe(stack);
    // Day and night: drawn again in the page's ink as it changes.
    const tone = new MutationObserver(() => requestAnimationFrame(() => e.recolor()));
    tone.observe(document.documentElement, { attributes: true, attributeFilter: ["data-theme", "class", "style"] });
    const move = (ev: PointerEvent) => {
      if (touching) return; // a finger's way is touchmove's
      const r = el.getBoundingClientRect();
      e.near(ev.clientX - r.left, ev.clientY - r.top);
    };
    window.addEventListener("pointermove", move, { passive: true });
    // A press on the pane itself (not a key, a box, a menu): rings come out about it.
    // The game on (kumo, 2026-11-26): the pane's bars go and what the page does with a finger of
    // its own is off (pulled down to load again; the root's rule, styles.css), till it is over.
    let playing = false;
    e.onPlay((on) => {
      playing = on;
      document.documentElement.dataset.playing = on ? "1" : "0";
      if (!on) setTimeout(() => !playing && delete document.documentElement.dataset.playing, 1500);
    });
    const press = (ev: PointerEvent) => {
      const t = ev.target as Element | null;
      if (t?.closest?.("button, a, input, textarea, [role=menu], [role=dialog], [role=listbox], [data-sheet-pull], nav, aside")) return;
      // Over the pane there may be a page that scrolls (the settings, a list): a press in one is
      // its own, never the splash's (kumo, 2026-11-26: a finger's drag taken here, the settings
      // would not scroll). Nor one while the splash is not shown.
      if (!showRef.current) return;
      for (let n: Element | null = t; n && n !== document.body; n = n.parentElement) {
        if (n.scrollHeight > n.clientHeight + 1 && /auto|scroll/.test(getComputedStyle(n).overflowY)) return;
      }
      const r = el.getBoundingClientRect();
      if (ev.clientX < r.left || ev.clientX > r.right || ev.clientY < r.top || ev.clientY > r.bottom) return;
      // A finger at the pane's top is the page's own (pulled down, it is loaded again): no stars
      // there, the drag not taken (kumo, 2026-11-26).
      if (!playing && ev.pointerType === "touch" && ev.clientY - r.top < Math.max(120, r.height * 0.2)) return;
      touching = ev.pointerType === "touch";
      if (!touching) {
        // Mouse/pen drag on the splash (kumo, 2026-11-26): blur inputs, prevent native drag
        // selection and I-beam cursor over text underneath (WebKit/WKWebView).
        if (document.activeElement instanceof HTMLElement && document.activeElement.matches("input, textarea")) {
          document.activeElement.blur();
        }
        ev.preventDefault();
        window.getSelection()?.removeAllRanges();
      }
      document.documentElement.dataset.slicing = "1";
      e.tap(ev.clientX - r.left, ev.clientY - r.top);
      e.hold(true);
    };
    // A finger (kumo, 2026-11-25): held and drawn along the pane as a mouse is, the page not
    // scrolled by it; let go, all it did ends (the lens with it).
    let touching = false;
    const drag = (ev: TouchEvent) => {
      const p = touching ? ev.touches[0] : undefined;
      if (!p) return;
      ev.preventDefault();
      const r = el.getBoundingClientRect();
      e.near(p.clientX - r.left, p.clientY - r.top);
    };
    const free = (ev: Event) => {
      // A finger's pointer is cancelled as the page would scroll: it is let go by touchend alone.
      if (touching && ev.type === "pointercancel") return;
      delete document.documentElement.dataset.slicing;
      e.hold(false);
      if (touching || ev.type.startsWith("touch")) (touching = false), e.near(-1, -1);
    };
    window.addEventListener("pointerdown", press, { passive: false });
    window.addEventListener("pointerup", free, { passive: true });
    window.addEventListener("pointercancel", free, { passive: true });
    window.addEventListener("blur", free);
    window.addEventListener("touchmove", drag, { passive: false });
    window.addEventListener("touchend", free, { passive: true });
    window.addEventListener("touchcancel", free, { passive: true });
    return () => {
      ro.disconnect();
      tone.disconnect();
      window.removeEventListener("pointermove", move);
      window.removeEventListener("pointerdown", press);
      window.removeEventListener("pointerup", free);
      window.removeEventListener("pointercancel", free);
      window.removeEventListener("blur", free);
      window.removeEventListener("touchmove", drag);
      window.removeEventListener("touchend", free);
      window.removeEventListener("touchcancel", free);
      delete document.documentElement.dataset.playing;
      delete document.documentElement.dataset.slicing;
      e.stop();
      eng.current = null;
    };
  }, [there, still, phone]);
  useEffect(() => eng.current?.figure(now), [now?.key]);
  // Not to be shown any more: where it was shown (the same channel, its first agent in or its
  // first words said), it goes by its own going, played whole; the page under it another one,
  // it is gone with the page it was of.
  const shownAt = useRef(scene);
  if (show) shownAt.current = scene;
  useLayoutEffect(() => {
    if (!there) return;
    if (show) eng.current?.enter();
    else if (shownAt.current !== scene || !eng.current) setThere(false);
    else eng.current.leave(() => setThere(false));
  }, [show, scene, there]);
  useEffect(() => eng.current?.power(show ? power : STANDARD), [power, show]);
  if (!there) return null;
  return (
    <div aria-hidden={!now} data-splash className={cn("pointer-events-none absolute inset-0 z-1", className)}>
      <canvas ref={cv} aria-hidden className="absolute inset-0 size-full text-ink" />
    </div>
  );
}

/** A channel says what the pane's splash is to show for it: whether nothing is said in it yet,
 *  and its figure: its own face, or its last agent's, or that one's mark; no agent yet:
 *  Iridium's letters. Draws nothing itself. */
export function ChannelSplash({ id, show: blank, last, agents }: { id: string; show: boolean; last?: { session: string; agent: Agent }; agents: number }) {
  const own = useFaceSrc(id, true);
  const src = useFaceSrc(last?.session ?? id);
  const key = last ? `c:${id}:${own}:${src}` : "";
  // Its first agent come while it is looked at: the splash turns to that one's face, stays a
  // moment, and goes (kumo, 2026-11-25: an agent takes its time to start; the going is played
  // whole meanwhile). One come to with agents and nothing said keeps its splash till words come.
  const none = useRef(agents === 0);
  const [over, setOver] = useState(false);
  useEffect(() => {
    if (!none.current || agents === 0) return;
    const timer = setTimeout(() => setOver(true), 2400);
    return () => clearTimeout(timer);
  }, [agents > 0]);
  const show = blank && !over;
  useLayoutEffect(() => stage({ id, show, fig: last ? { key, src: own || src || undefined, alt: own && src ? src : undefined, agent: last.agent } : null }), [id, show, key]);
  return null;
}

/** The pane's one splash: there with no channel open (open: none) and in a channel that says
 *  nothing is said in it yet; one just made is taken so before it says. off: a page that has
 *  none (a session's record). */
export function PaneSplash({ open, off }: { open?: string; off?: boolean }) {
  const st = useSyncExternalStore(onPoint, stagedNow);
  const mine = open && st?.id === open ? st : null;
  return <Splash show={!off && (!open || (mine ? mine.show : justMade(open)))} fig={mine?.fig ?? null} scene={open ?? ""} />;
}
