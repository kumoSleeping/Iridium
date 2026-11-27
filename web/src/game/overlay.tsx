// The ⋯ menu's Game (kumo, 2026-11-26): the splash's one game, the one of slice.ts (faces thrown
// up, cut through; Iridium's mark not to be cut), over the whole pane, the page under it left as
// it is (a chat goes on being written). Opened: the pane's bar and composer go (styles.css,
// html[data-gaming]), a veil of the paper comes over the page, the code comes up over it and the
// game is on at once, no zigzag drawn first. Over: its count and nothing more (no band going round
// after), a press anywhere back to the chat. Its count gone, Esc or its ×: all of it goes
// the way it came, and nothing of it is left running (its frames, its listeners, its canvases).
//
// Not web/attic/splash-game (a cat-eared boss): that is old code, put away, not to be used.
import { X } from "lucide-react";
import { useLayoutEffect, useRef, useState } from "react";
import { t } from "../i18n";
import { useRoles } from "../roles";
import { splash } from "../splash/engine";
import type { Cast } from "../splash/slice";
import { cn, IconKey, PHONE, useMedia } from "../ui";
import { closeGame } from "./open";

// As the splash's (splash.tsx): the roles kept, by their faces, and each agent's mark.
const MARKS: Cast[] = (["claude", "codex", "pi", "dsh", "agy"] as const).map((agent) => ({ agent }));
/** How long it comes and goes, ms (styles.css game-* the same). */
const IN = 450, OUT = 420;
// How long the count stays before a press takes the game away (kumo, 2026-11-26).
const SEE = 1000;

let back: number | undefined;

export default function GameLayer() {
  const cv = useRef<HTMLCanvasElement>(null);
  const phone = useMedia(PHONE);
  const roles = useRoles();
  const castRef = useRef<Cast[]>([]);
  castRef.current = [...roles.filter((r) => r.avatar).slice(0, 6).map((r) => ({ src: r.avatar!, agent: r.agent })), ...MARKS];
  // shown: the veil and the code there (false the first frame, so they come; false again going).
  const [shown, setShown] = useState(false);
  const quit = useRef(() => {});

  useLayoutEffect(() => {
    const el = cv.current!;
    const root = document.documentElement;
    clearTimeout(back);
    root.dataset.gaming = "1";
    const e = splash(el, false, phone, true);
    e.figure(null);
    e.cast(castRef.current);
    const fit = () => {
      e.resize();
      const w = el.clientWidth, h = el.clientHeight;
      e.window(h / 2, h * 2);
      e.place(w / 2, Math.min(w * 0.7, h * 0.45), h / 2);
      e.foot(-1);
      e.edge(false);
    };
    fit();
    const ro = new ResizeObserver(fit);
    ro.observe(el);
    const tone = new MutationObserver(() => requestAnimationFrame(() => e.recolor()));
    tone.observe(root, { attributes: true, attributeFilter: ["data-theme", "class", "style"] });

    // over: the game ended, its count shown: a press anywhere goes back to the chat (kumo,
    // 2026-11-26), not waited out. ending: just ended, a press then is the one still cutting
    // (kumo, 2026-11-26: a tap right after the bomb left before the count was seen): let pass.
    let on = false, gone = false, over = false, ending = false, raf = 0, tries = 0;
    const timers: number[] = [];
    const leave = () => {
      if (gone) return;
      gone = true;
      cancelAnimationFrame(raf);
      setShown(false);
      root.dataset.gaming = "0";
      timers.push(window.setTimeout(closeGame, OUT));
    };
    quit.current = leave;
    // On at once: as soon as the letters are drawn (the engine says no till then).
    const go = () => {
      if (gone || on) return;
      on = e.play(leave, () => {
        ending = true;
        timers.push(window.setTimeout(() => ((ending = false), (over = true)), SEE));
      });
      if (!on && tries++ < 180) raf = requestAnimationFrame(go);
    };
    raf = requestAnimationFrame(() => {
      setShown(true);
      timers.push(window.setTimeout(go, IN * 0.6));
    });

    // A pointer or a finger over the layer: moved, pressed, held, as on the splash; not let on to
    // the page under it (the pane's own splash listens on the window).
    let touching = false;
    const at = (x: number, y: number) => {
      const r = el.getBoundingClientRect();
      return [x - r.left, y - r.top] as const;
    };
    const move = (ev: PointerEvent) => {
      ev.stopPropagation();
      if (!touching) e.near(...at(ev.clientX, ev.clientY));
    };
    const press = (ev: PointerEvent) => {
      ev.stopPropagation();
      if ((ev.target as Element | null)?.closest?.("button")) return;
      if (ending) return;
      if (over) return leave();
      touching = ev.pointerType === "touch";
      e.tap(...at(ev.clientX, ev.clientY));
      e.hold(true);
    };
    const drag = (ev: TouchEvent) => {
      ev.stopPropagation();
      const p = touching ? ev.touches[0] : undefined;
      if (!p) return;
      if (ev.cancelable) ev.preventDefault();
      e.near(...at(p.clientX, p.clientY));
    };
    const free = (ev: Event) => {
      if (touching && ev.type === "pointercancel") return;
      e.hold(false);
      if (touching || ev.type.startsWith("touch")) (touching = false), e.near(-1, -1);
    };
    const esc = (ev: KeyboardEvent) => {
      if (ev.key !== "Escape") return;
      ev.preventDefault();
      ev.stopPropagation();
      leave();
    };
    const box = el.parentElement!;
    box.addEventListener("pointermove", move, { passive: true });
    box.addEventListener("pointerdown", press, { passive: true });
    box.addEventListener("touchmove", drag, { passive: false });
    addEventListener("pointerup", free, { passive: true });
    addEventListener("pointercancel", free, { passive: true });
    addEventListener("touchend", free, { passive: true });
    addEventListener("touchcancel", free, { passive: true });
    addEventListener("keydown", esc, { capture: true });
    return () => {
      cancelAnimationFrame(raf);
      timers.forEach(clearTimeout);
      ro.disconnect();
      tone.disconnect();
      box.removeEventListener("pointermove", move);
      box.removeEventListener("pointerdown", press);
      box.removeEventListener("touchmove", drag);
      removeEventListener("pointerup", free);
      removeEventListener("pointercancel", free);
      removeEventListener("touchend", free);
      removeEventListener("touchcancel", free);
      removeEventListener("keydown", esc, { capture: true });
      e.stop();
      el.width = el.height = 0;
      // The bars' coming back played out, then nothing of it on the page.
      if (!gone) root.dataset.gaming = "0";
      back = window.setTimeout(() => root.dataset.gaming === "0" && delete root.dataset.gaming, 900);
    };
  }, [phone]);

  return (
    <div data-game className="absolute inset-0 z-40 touch-none overscroll-none select-none">
      {/* The veil: the page under it still seen, faint, going on. */}
      <div aria-hidden className={cn("absolute inset-0 bg-paper/75 backdrop-blur-[1.5px] transition-opacity ease-(--ease-out)", shown ? "opacity-100" : "opacity-0")} style={{ transitionDuration: `${shown ? IN : OUT}ms` }} />
      <canvas ref={cv} aria-hidden className={cn("absolute inset-0 size-full text-ink transition-opacity ease-(--ease-out)", shown ? "opacity-100" : "opacity-0")} style={{ transitionDuration: `${shown ? IN * 1.4 : OUT}ms` }} />
      <IconKey label={t("game.quit")} onClick={() => quit.current()} className={cn("absolute top-2 right-2 text-ink-3 transition-opacity duration-(--dur-2) pointer-coarse:top-[max(8px,env(safe-area-inset-top))]", shown ? "opacity-100" : "opacity-0")}>
        <X size={16} />
      </IconKey>
    </div>
  );
}
