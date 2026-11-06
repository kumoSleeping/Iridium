// The thinking effort as a slider of stops (Codex's), in the model key's panel: dragged, tapped on
// the track or moved with the keys, it snaps to a level; only a level let go of is sent. A thick
// track, the knob inside it, the fill a flowing liquid of orange, pink and fire (chat/liquid.ts; the
// user, 2026-11-06). The knob pulls the liquid after it (chat/goo.ts): springs move the knob, the
// fill's front and the blobs between, all drawn through the goo filter, so they run together.
import { Slider } from "@base-ui/react/slider";
import { useEffect, useId, useRef, useState, type CSSProperties, type ReactNode } from "react";
import { t, useLang } from "../i18n";
import { zh, type Key } from "../i18n/zh";
import { cn } from "../ui";
import { pull } from "./goo";
import { flow } from "./liquid";

/** A level by its name in the page's language; one the page does not know (pi's own) as it is. */
export const levelName = (level: string) => (`effort.${level}` in zh ? t(`effort.${level}` as Key) : level);

/** Where the slider rests for level among levels: its stop, known; with none set or one this model
 *  does not take, medium's stop (else the middle one), not known, shown muted. */
export function restAt(levels: string[], level?: string): { i: number; known: boolean } {
  const i = level ? levels.indexOf(level) : -1;
  if (i >= 0) return { i, known: true };
  const m = levels.indexOf("medium");
  return { i: m >= 0 ? m : Math.floor((levels.length - 1) / 2), known: false };
}

/** The blobs between the front and the knob, by size (px); the last sits under the knob itself. */
const BLOBS = [15, 12, 10];

/** The goo: blurred, then the alpha pulled hard, so shapes near one another run together with round
 *  edges; drawn only where the fill itself is not (out), the fill sharp over it. */
function GooFilter({ id }: { id: string }) {
  return (
    <svg aria-hidden width="0" height="0" className="absolute">
      <filter id={id} x="-10%" y="-60%" width="120%" height="220%" colorInterpolationFilters="sRGB">
        <feGaussianBlur in="SourceGraphic" stdDeviation="3.5" result="blur" />
        <feColorMatrix in="blur" mode="matrix" values="1 0 0 0 0  0 1 0 0 0  0 0 1 0 0  0 0 0 16 -6" result="goo" />
        <feComposite in="goo" in2="SourceAlpha" operator="out" result="neck" />
        <feMerge>
          <feMergeNode in="neck" />
          <feMergeNode in="SourceGraphic" />
        </feMerge>
      </filter>
    </svg>
  );
}

/** The keys the slider moves by: kept from the menu around it, whose list would move on them too. */
const SLIDER_KEY = /^(Arrow|Page|Home$|End$)/;

/** The effort slider. value: the level in use (set, or the agent's own); set sends one, and says
 *  whether it was taken: till the session's state says so, the slider stays where it was let go.
 *  aside: what sits at the track's right (UltraCode's switch); tag: words before the level's name
 *  (UltraCode, when on). */
export function EffortSlider({ levels, value, set, aside, tag }: { levels: string[]; value?: string; set: (level: string) => Promise<boolean>; aside?: ReactNode; tag?: ReactNode }) {
  useLang();
  const [want, setWant] = useState<string | null>(null);
  const [drag, setDrag] = useState<number | null>(null);
  if (want !== null && value === want) setWant(null);
  const rest = restAt(levels, want ?? value);
  const i = drag ?? rest.i;
  const known = rest.known || drag !== null;
  const n = levels.length;
  const frac = n > 1 ? i / (n - 1) : 0;
  const fracNow = useRef(frac);
  fracNow.current = frac;
  const canvas = useRef<HTMLCanvasElement>(null);
  const root = useRef<HTMLDivElement>(null);
  const goo = `goo${useId().replace(/\W/g, "")}`;
  useEffect(() => {
    const el = canvas.current;
    const box = root.current;
    if (!el || !box) return;
    const still = matchMedia("(prefers-reduced-motion: reduce)").matches || !!document.querySelector("[data-still]");
    const stopFlow = flow(el, () => fracNow.current, still);
    const stopPull = pull(box, () => fracNow.current, still);
    return () => (stopFlow(), stopPull());
  }, []);
  const commit = (k: number) => {
    setDrag(null);
    const level = levels[k];
    if (!level || level === (want ?? value)) return;
    setWant(level);
    void set(level).then((ok) => ok || setWant(null));
  };
  return (
    <div ref={root} className="effort-slider px-2.5 pt-1.5 pb-2" onKeyDown={(e) => SLIDER_KEY.test(e.key) && e.stopPropagation()} style={{ "--frac": frac } as CSSProperties}>
      <div className="flex h-6 items-center justify-between text-xs">
        <span className="text-ink-3">{t("composer.effort")}</span>
        <span className="flex min-w-0 items-baseline gap-1.5">
          {tag}
          <span key={known ? levels[i] : ""} className={cn("g-fade text-sm font-medium", known ? "liquid-text" : "text-ink-3")}>
            {known ? levelName(levels[i]!) : t("composer.effortDefault")}
          </span>
        </span>
      </div>
      <div className="flex items-center gap-3">
        <Slider.Root value={i} min={0} max={n - 1} step={1} onValueChange={(v) => setDrag(v as number)} onValueCommitted={(v) => commit(v as number)} aria-label={t("composer.effort")} data-unknown={known ? undefined : ""} className="group/effort min-w-0 flex-1">
          <Slider.Control className="relative flex h-7 cursor-grab touch-none items-center data-dragging:cursor-grabbing pointer-coarse:h-10">
            <Slider.Track className="relative h-6 w-full overflow-hidden rounded-full bg-fill-2 shadow-[inset_0_1px_2px_oklch(0_0_0/0.12)]">
              <span aria-hidden className="effort-tone absolute inset-0">
                <span className="effort-fill absolute inset-0" style={{ filter: `url(#${goo})` }}>
                  <span className="effort-body absolute inset-0">
                    <canvas ref={canvas} className="absolute inset-0 size-full" />
                  </span>
                  {BLOBS.map((size, k) => (
                    <span key={k} className="effort-blob" style={{ "--b": `var(--b${k})`, "--s": `${size}px` } as CSSProperties} />
                  ))}
                  <span className="effort-blob" style={{ "--b": "var(--knob)", "--s": "18px" } as CSSProperties} />
                </span>
              </span>
              {/* The stops: notches on the line the knob's middle runs along (12px in from each end). */}
              {levels.map((l, k) => (
                <span key={l} aria-hidden className={cn("absolute top-1/2 size-1 -translate-1/2 rounded-full transition-colors duration-(--dur-2)", k <= i ? "bg-white/70" : "bg-ink-4")} style={{ left: `calc(12px + (100% - 24px) * ${n > 1 ? k / (n - 1) : 0})` }} />
              ))}
              <span aria-hidden className="effort-knob pointer-events-none absolute inset-y-0 right-[22px] left-0.5">
                <span className="absolute top-0.5 left-0 size-5 rounded-full bg-white effort-knob-face transition-transform duration-(--dur-1) group-has-[:focus-visible]/effort:outline-2 group-has-[:focus-visible]/effort:outline-offset-1 group-has-[:focus-visible]/effort:outline-ink-3 group-data-dragging/effort:scale-90" />
              </span>
              {/* What the keys and a screen reader take; drawn by the knob above, which glides. */}
              <Slider.Thumb getAriaValueText={(_, v) => (known ? levelName(levels[v] ?? "") : t("composer.effortDefault"))} aria-label={t("composer.effort")} className="size-5 opacity-0 outline-none" />
            </Slider.Track>
          </Slider.Control>
        </Slider.Root>
        {aside}
      </div>
      <GooFilter id={goo} />
    </div>
  );
}
