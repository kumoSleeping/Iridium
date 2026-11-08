// The thinking effort as a slider of stops (Codex's), in the model key's panel: dragged, tapped on
// the track or moved with the keys, it snaps to a level; only a level let go of is sent. A thick
// track, the knob inside it, the fill a flowing liquid of orange, pink and fire (chat/liquid.ts; the
// user, 2026-11-06). The knob pulls the liquid after it (chat/goo.ts): springs move the knob, the
// fill's front and the blobs between, the blobs drawn through the goo filter, so they run together
// under the liquid. The knob is frosted white balls run together through a goo of its own.
import { Slider } from "@base-ui/react/slider";
import { useEffect, useId, useRef, useState, type CSSProperties, type ReactNode } from "react";
import { t, useLang } from "../i18n";
import { zh, type Key } from "../i18n/zh";
import { buzz, cn } from "../ui";
import { pull } from "./goo";
import { flow } from "./liquid";

/** A level by its name in the page's language; one the page does not know (pi's own) as it is. */
export const levelName = (level: string) => (`effort.${level}` in zh ? t(`effort.${level}` as Key) : level);

/** Where the slider rests for level among levels: its stop, known; with none set or one this model
 *  does not take, medium's stop (else the middle one), not known: the name says "default", the
 *  liquid as ever (greyed, it read as broken; the user, 2026-11-08). */
export function restAt(levels: string[], level?: string): { i: number; known: boolean } {
  const i = level ? levels.indexOf(level) : -1;
  if (i >= 0) return { i, known: true };
  const m = levels.indexOf("medium");
  return { i: m >= 0 ? m : Math.floor((levels.length - 1) / 2), known: false };
}

/** The blobs between the front and the knob, by size (px). */
const BLOBS = [15, 12, 10];

/** The goo: blurred, then the alpha pulled hard, so the blobs near one another run together with
 *  round edges. Only the blobs go through it (a cap at the fill's front, those between, one under
 *  the knob); the fill itself is drawn sharp over them. */
function GooFilter({ id }: { id: string }) {
  return (
    <svg aria-hidden width="0" height="0" className="absolute">
      <filter id={id} x="-10%" y="-60%" width="120%" height="220%" colorInterpolationFilters="sRGB">
        <feGaussianBlur in="SourceGraphic" stdDeviation="3.5" result="blur" />
        <feColorMatrix in="blur" mode="matrix" values="1 0 0 0 0  0 1 0 0 0  0 0 1 0 0  0 0 0 16 -6" />
      </filter>
      {/* The knob's own goo (the user, 2026-11-06): its white balls run into one shape, half clear,
          a soft shadow only outside it. */}
      <filter id={`${id}k`} x="-10%" y="-100%" width="120%" height="300%" colorInterpolationFilters="sRGB">
        <feGaussianBlur in="SourceGraphic" stdDeviation="3" result="blur" />
        <feColorMatrix in="blur" mode="matrix" values="1 0 0 0 0  0 1 0 0 0  0 0 1 0 0  0 0 0 18 -7" result="goo" />
        <feGaussianBlur in="goo" stdDeviation="1.5" result="soft" />
        <feColorMatrix in="soft" mode="matrix" values="0 0 0 0 0  0 0 0 0 0  0 0 0 0 0  0 0 0 0.22 0" result="dark" />
        <feOffset in="dark" dy="1" result="drop" />
        <feComposite in="drop" in2="goo" operator="out" result="shadow" />
        <feColorMatrix in="goo" mode="matrix" values="1 0 0 0 0  0 1 0 0 0  0 0 1 0 0  0 0 0 0.6 0" result="frost" />
        <feMerge>
          <feMergeNode in="shadow" />
          <feMergeNode in="frost" />
        </feMerge>
      </filter>
    </svg>
  );
}

/** The knob's balls, by size (px), and the spring each follows: the knob itself, then two trailing
 *  it, looser (goo.ts --k1, --k2), as the code the user brought had them. */
const KNOB = [
  [28, "--knob"],
  [23, "--k1"],
  [19, "--k2"],
] as const;

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
  const name = (
    <span key={known ? levels[i] : ""} className={cn("g-fade text-sm font-medium whitespace-nowrap", !!aside && "justify-self-center", known ? "text-ink-2" : "text-ink-3")}>
      {known ? levelName(levels[i]!) : t("composer.effortDefault")}
    </span>
  );
  return (
    <div ref={root} className="effort-slider px-2.5 pt-1.5 pb-2 select-none [-webkit-touch-callout:none]" onKeyDown={(e) => SLIDER_KEY.test(e.key) && e.stopPropagation()} style={{ "--frac": frac } as CSSProperties}>
      {/* With a key beside it (UltraCode), two columns: the track and that key, the level's name over
          the key, centred on it (the user, 2026-11-06). Without one, the track the whole width, the
          name at the end of the line over it: nothing left empty at its right (the user, 2026-11-06). */}
      <div className={cn("grid items-center gap-x-3", aside ? "grid-cols-[minmax(0,1fr)_auto]" : "grid-cols-1")}>
        <div className="flex h-6 min-w-0 items-center justify-between gap-2 text-xs">
          <span className="text-ink-3">{t("composer.effort")}</span>
          <span className="flex min-w-0 items-center gap-2">
            {tag}
            {!aside && name}
          </span>
        </div>
        {aside && name}
        <Slider.Root value={i} min={0} max={n - 1} step={1} onValueChange={(v) => (v !== i && buzz(), setDrag(v as number))} onValueCommitted={(v) => commit(v as number)} aria-label={t("composer.effort")} className="group/effort min-w-0">
          <Slider.Control className="relative flex h-7 cursor-grab touch-none items-center data-dragging:cursor-grabbing pointer-coarse:h-10">
            <Slider.Track className="relative h-6 w-full rounded-full bg-fill-2 shadow-[inset_0_1px_2px_oklch(0_0_0/0.12)]">
              <span aria-hidden className="absolute inset-0 overflow-hidden rounded-full">
                {/* Under: the neck the knob draws out, the blobs run together; over: the liquid. */}
                <span className="absolute inset-0" style={{ filter: `url(#${goo})` }}>
                  <span className="effort-blob" style={{ "--b": "var(--front)", "--s": "24px" } as CSSProperties} />
                  {BLOBS.map((size, k) => (
                    <span key={k} className="effort-blob" style={{ "--b": `var(--b${k})`, "--s": `${size}px` } as CSSProperties} />
                  ))}
                  <span className="effort-blob" style={{ "--b": "var(--knob)", "--s": "18px" } as CSSProperties} />
                </span>
                <span className="effort-body absolute inset-0">
                  <canvas ref={canvas} className="absolute inset-0 size-full" />
                </span>
              </span>
              {/* The stops: notches on the line the knob's middle runs along (12px in from each end). */}
              {levels.map((l, k) => (
                <span key={l} aria-hidden className={cn("absolute top-1/2 size-1 -translate-1/2 rounded-full transition-colors duration-(--dur-2)", k === i ? "bg-white/0" : k < i ? "bg-white/70" : "bg-ink-4")} style={{ left: `calc(12px + (100% - 24px) * ${n > 1 ? k / (n - 1) : 0})` }} />
              ))}
              {/* The knob: the liquid frosted under it, and over that its white balls through the goo, so it
                  stretches and wobbles as it moves. */}
              <span aria-hidden className="effort-frost pointer-events-none group-has-[:focus-visible]/effort:outline-2 group-has-[:focus-visible]/effort:outline-offset-1 group-has-[:focus-visible]/effort:outline-ink-3" />
              <span aria-hidden className="pointer-events-none absolute inset-0" style={{ filter: `url(#${goo}k)` }}>
                {KNOB.map(([size, v]) => (
                  <span key={v} className="effort-blob effort-ball" style={{ "--b": `var(${v})`, "--s": `${size}px` } as CSSProperties} />
                ))}
              </span>
              {/* What the keys and a screen reader take; drawn by the knob above, which glides. */}
              <Slider.Thumb getAriaValueText={(_, v) => (known ? levelName(levels[v] ?? "") : t("composer.effortDefault"))} aria-label={t("composer.effort")} className="size-5 opacity-0 outline-none" />
            </Slider.Track>
          </Slider.Control>
        </Slider.Root>
        {aside && <span className="justify-self-center">{aside}</span>}
      </div>
      <GooFilter id={goo} />
    </div>
  );
}
