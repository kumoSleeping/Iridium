// The splash in the app's own page (添加服务器; otae-c, 霓虹, 2026-11-26): the Iridium pages' band
// of code drawing Iridium's letters (splash/engine.ts), over the field. Nothing asked of a server
// (the app's page has none): Iridium's letters and the agents' marks alone. It follows the
// pointer, a press rings out about it, day and night as the page.
import { useLayoutEffect, useRef } from "react";
import { splash } from "../splash/engine";
import type { Cast } from "../splash/slice";
import { cn, useMedia } from "../ui";

const MARKS: Cast[] = (["claude", "codex", "pi", "dsh", "agy"] as const).map((agent) => ({ agent }));

export function AppSplash({ className }: { className?: string }) {
  const cv = useRef<HTMLCanvasElement>(null);
  const still = useMedia("(prefers-reduced-motion: reduce)");
  useLayoutEffect(() => {
    const el = cv.current;
    if (!el) return;
    const e = splash(el, still, false);
    e.figure(null);
    e.cast(MARKS);
    // The canvas is the band: the letters in its middle, as large as it has room for.
    const fit = () => {
      e.resize();
      const w = el.clientWidth, h = el.clientHeight;
      e.window(h / 2, h);
      e.place(-1, Math.min(h * 0.78, w * 0.7), h / 2);
      e.foot(-1);
      e.edge(true);
    };
    fit();
    e.enter();
    const ro = new ResizeObserver(fit);
    ro.observe(el);
    const tone = new MutationObserver(() => requestAnimationFrame(() => e.recolor()));
    tone.observe(document.documentElement, { attributes: true, attributeFilter: ["data-theme", "class", "style"] });
    const at = (ev: PointerEvent) => {
      const r = el.getBoundingClientRect();
      return [ev.clientX - r.left, ev.clientY - r.top] as const;
    };
    const move = (ev: PointerEvent) => e.near(...at(ev));
    const leave = () => e.near(-1, -1);
    // A press on the band itself (not the field, a key, a row): rings come out about it.
    const press = (ev: PointerEvent) => {
      const t = ev.target as Element | null;
      if (t?.closest?.("button, a, input, form, header")) return;
      const [x, y] = at(ev);
      if (x < 0 || y < 0 || x > el.clientWidth || y > el.clientHeight) return;
      if (ev.pointerType !== "touch") {
        if (document.activeElement instanceof HTMLElement && document.activeElement.matches("input, textarea")) {
          document.activeElement.blur();
        }
        ev.preventDefault();
        window.getSelection()?.removeAllRanges();
      }
      document.documentElement.dataset.slicing = "1";
      e.tap(x, y);
      e.hold(true);
    };
    const free = () => {
      delete document.documentElement.dataset.slicing;
      e.hold(false);
    };
    window.addEventListener("pointermove", move, { passive: true });
    document.addEventListener("pointerleave", leave);
    window.addEventListener("pointerdown", press, { passive: false });
    window.addEventListener("pointerup", free, { passive: true });
    window.addEventListener("pointercancel", free, { passive: true });
    window.addEventListener("blur", free);
    return () => {
      ro.disconnect();
      tone.disconnect();
      window.removeEventListener("pointermove", move);
      document.removeEventListener("pointerleave", leave);
      window.removeEventListener("pointerdown", press);
      window.removeEventListener("pointerup", free);
      window.removeEventListener("pointercancel", free);
      window.removeEventListener("blur", free);
      delete document.documentElement.dataset.slicing;
      e.stop();
    };
  }, [still]);
  return (
    <div aria-hidden className={cn("pointer-events-none relative", className)}>
      <canvas ref={cv} className="absolute inset-0 size-full text-ink" />
    </div>
  );
}
