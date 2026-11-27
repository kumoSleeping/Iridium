// Shared pieces (from the style sample, web/sample): tooltips that also open on long-press, the
// popover shell, icon keys, agent logos with their status lamp, terminal avatars, switches.
import { PatternFace } from "./pattern";
import { Dialog } from "@base-ui/react/dialog";
import { Menu } from "@base-ui/react/menu";
import { Popover } from "@base-ui/react/popover";
import { Tooltip } from "@base-ui/react/tooltip";
import { Bot, Camera, Check, Copy, Monitor, Puzzle, ScrollText, UsersRound, type LucideIcon } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore, type ComponentProps, type ComponentType, type CSSProperties, type ReactElement, type ReactNode } from "react";
import { InPopup } from "./api/query";
import type { Agent, Status } from "./api/types.gen";
import { t } from "./i18n";
import { useCarried } from "./store";
import { AGY_DATA_URI, CLAUDE, DSH, DSH_VIEWBOX, OPENAI, OPENAI_VIEWBOX, PI, PI_VIEWBOX } from "./logos";

export const cn = (...xs: (string | false | null | undefined)[]) => xs.filter(Boolean).join(" ");

/** The shell of what floats over a chat's foot and top: the composer, the work bar. SHELL_LINE: as
 *  tall as the composer with one line in it (its keys and their padding). */
export const SHELL = "glass float rounded-[20px]";
export const SHELL_LINE = "min-h-12 pointer-coarse:min-h-14";

export const params = new URLSearchParams(location.search);

export function useMedia(q: string) {
  return useSyncExternalStore(
    (f) => {
      const m = matchMedia(q);
      m.addEventListener("change", f);
      return () => m.removeEventListener("change", f);
    },
    () => matchMedia(q).matches,
    () => false,
  );
}
/** The phone's layout (the sidebar a sheet from the foot): by the window's shape, not the device
 *  (the user, 2026-11-06). Under 520px wide always; under 768px only held tall, taller than 5:4,
 *  so an unfolded foldable, near square, has the computer's. */
export const PHONE = "(max-width: 519px), (max-width: 767px) and (max-aspect-ratio: 4/5)";
/** A keyboard and a pointer, not a phone: Enter sends, the caret goes to the composer. Only a
 *  touch screen alone says no (a headless browser has no pointer at all). */
/** How long ago, in words: just now, minutes, hours, days. */
export function ago(ms: number | undefined, now = Date.now(), short = false): string {
  if (!ms) return t("term.never");
  const m = Math.floor((now - ms) / 60_000);
  if (m < 1) return t("ago.now");
  if (m < 60) return t(short ? "ago.minShort" : "ago.min", { n: m });
  const h = Math.floor(m / 60);
  if (h < 48) return t(short ? "ago.hourShort" : "ago.hour", { n: h });
  return t(short ? "ago.dayShort" : "ago.day", { n: Math.floor(h / 24) });
}

/** The time now, again each minute: for what says how long ago. */
let minute = Date.now();
const minuteSubs = new Set<() => void>();
export function useMinute(): number {
  return useSyncExternalStore(
    (f) => {
      minuteSubs.add(f);
      if (minuteSubs.size === 1) tick = window.setInterval(() => ((minute = Date.now()), minuteSubs.forEach((g) => g())), 60_000);
      return () => {
        minuteSubs.delete(f);
        if (!minuteSubs.size) clearInterval(tick);
      };
    },
    () => minute,
  );
}
let tick: number | undefined;

export const fine = () => !matchMedia("(pointer: coarse)").matches;

export const MAC = /Mac|iPhone|iPad/.test(navigator.platform);

/** Whether an event's target is in the element's own DOM, not in a portal of its children (a
 *  popover's, a menu's): React events bubble out of those too. */
export const own = (e: React.SyntheticEvent) => e.currentTarget.contains(e.target as Node);

/** A short tick of the phone's motor (Android; the web has none on iOS): the effort slider's steps. */
export const buzz = (ms = 8) => void navigator.vibrate?.(ms);
/** Android's browser buzzes for a long press itself, its contextmenu: only that one, not ours too
 *  (the user's Honor buzzed twice, 2026-11-07). */
const android = typeof navigator !== "undefined" && /Android/i.test(navigator.userAgent);
const buzzHeld = () => android || buzz();

/** Whether on has been true since this was first drawn: what floats out of a row (its menu, its
 *  card) is made as it first opens and kept from then on (it folds away drawn), not made shut
 *  with every row of a conversation, which no one may ever open (a chat of 388 rows spent most of
 *  its first draw making some thousand menus and popovers, shut; measured 2026-11-23). */
export function useEver(on: boolean): boolean {
  const ever = useRef(false);
  if (on) ever.current = true;
  return ever.current;
}

/** A row's menu at a point, one open at a time: a long press on another row never clicks outside
 *  the one open (its click is swallowed), so opening one puts the other away (the user, 2026-11-07). */
let shut: (() => void) | null = null;
export function useMenuAt() {
  const [at, setAt] = useState<{ x: number; y: number } | null>(null);
  const self = useRef(() => setAt(null)).current;
  useEffect(() => () => void (shut === self && (shut = null)), []);
  const set = useCallback((p: { x: number; y: number } | null) => {
    if (p && shut !== self) shut?.();
    shut = p ? self : shut === self ? null : shut;
    setAt(p);
  }, []);
  return [at, set] as const;
}

/** Something else that floats at a point as a row's menu does (the usage panel, app.tsx): opening
 *  it puts away the menu open, and a menu opened later puts it away. Gives its letting go. */
export function holdMenu(close: () => void) {
  if (shut !== close) shut?.();
  shut = close;
  return () => void (shut === close && (shut = null));
}

/** Long-press on touch: fires after 450 ms held still, and swallows the click that follows.
 *  suppressTouchClick: also prevent touchend once it has fired, so a nonmodal menu is not shut by
 *  the synthetic mouse press that comes before click. Not for a press in a portal of its children
 *  (a row's "+" popover): React may hand one pointerdown to a row twice from there, from the
 *  portal's container and from <body>, and the first timer would be lost, never cleared, opening the
 *  row's menu after a plain tap (the user, 2026-11-06). onHeldMove: the finger, held, then moved
 *  (a row dragged, reorder.ts touchDrag); till then the page is not scrolled by it. */
export function useLongPress(onLong: (at: { x: number; y: number }) => void, { suppressTouchClick = false, onHeldMove }: { suppressTouchClick?: boolean; onHeldMove?: (row: HTMLElement, from: { x: number; y: number }, pointerId: number) => void } = {}) {
  const timer = useRef<number | undefined>(undefined);
  const fired = useRef(false);
  const start = useRef({ x: 0, y: 0 });
  const clear = () => window.clearTimeout(timer.current);
  return {
    onPointerDown: (e: React.PointerEvent) => {
      fired.current = false;
      if (e.pointerType !== "touch" || !own(e)) return;
      clear();
      start.current = { x: e.clientX, y: e.clientY };
      const row = e.currentTarget as HTMLElement;
      const id = e.pointerId;
      timer.current = window.setTimeout(() => {
        fired.current = true;
        buzzHeld();
        onLong(start.current);
        if (onHeldMove) held(row, start.current, id, onHeldMove);
      }, 450);
    },
    onPointerMove: (e: React.PointerEvent) => {
      if (Math.hypot(e.clientX - start.current.x, e.clientY - start.current.y) > 8) clear();
    },
    onPointerUp: clear,
    onPointerCancel: clear,
    // Once fired, stop the synthetic mouse press that follows a touch from shutting a nonmodal
    // menu before onClickCapture can swallow the click.
    onTouchEnd: suppressTouchClick ? (e: React.TouchEvent) => {
      if (!fired.current || !e.cancelable) return;
      e.preventDefault();
      fired.current = false;
    } : undefined,
    /** true: the finger held has opened the menu already (Android says contextmenu after it):
     *  the caller opens nothing again, at another place. */
    onContextMenu: (e: React.MouseEvent): boolean => {
      if (fired.current) e.preventDefault();
      return fired.current;
    },
    onClickCapture: (e: React.MouseEvent) => {
      if (fired.current) {
        fired.current = false;
        e.preventDefault();
        e.stopPropagation();
      }
    },
  };
}

/** Held (the long press fired), the finger still down: the page not scrolled by it; moved past 8px,
 *  onMove (a row dragged, reorder.ts touchDrag). Let go first: nothing. */
function held(row: HTMLElement, from: { x: number; y: number }, id: number, onMove: (row: HTMLElement, from: { x: number; y: number }, pointerId: number) => void) {
  const still = (e: TouchEvent) => void (e.cancelable && e.preventDefault());
  const off = () => {
    removeEventListener("touchmove", still);
    removeEventListener("pointermove", move);
    removeEventListener("pointerup", up);
    removeEventListener("pointercancel", up);
  };
  const move = (e: PointerEvent) => {
    if (e.pointerId !== id || Math.hypot(e.clientX - from.x, e.clientY - from.y) <= 8) return;
    off();
    onMove(row, from, id);
  };
  const up = (e: PointerEvent) => void (e.pointerId === id && off());
  addEventListener("touchmove", still, { passive: false });
  addEventListener("pointermove", move);
  addEventListener("pointerup", up);
  addEventListener("pointercancel", up);
}

/** A key that takes something away for good: pressed once it turns red and its name becomes
 *  sure, only a second press does it (docs/前端设计规范.md "确认在原处"); left alone a moment, or the
 *  focus gone, it is as it was. No × anywhere: its sign is the caller's (a bin). */
export function SureKey({ label, sure, run, className, children }: { label: string; sure: string; run: () => void; className?: string; children: ReactNode }) {
  const [armed, setArmed] = useState(false);
  useEffect(() => {
    if (!armed) return;
    const t = setTimeout(() => setArmed(false), 4000);
    return () => clearTimeout(t);
  }, [armed]);
  return (
    <IconKey label={armed ? sure : label} onClick={() => (armed ? (setArmed(false), run()) : setArmed(true))} onBlur={() => setArmed(false)} className={cn(className, armed && "bg-fill text-err hover:text-err")}>
      {children}
    </IconKey>
  );
}

/** A block opened under a row (its permissions, a folder to open, a confirmation) is brought into
 *  view: under the last row of a list it opened below the fold, as if nothing had (kumo, 2026-11-19). */
export const intoView = (el: HTMLElement | null) => void el?.scrollIntoView({ block: "nearest", behavior: "smooth" });

/** A menu's anchor at a point of the window. The same one while the point is: a new one each
 *  draw had the menu placed again each time its row was drawn (a stream's tick). */
export function usePointAnchor(at: { x: number; y: number } | null) {
  const x = at?.x;
  const y = at?.y;
  return useMemo(() => (x === undefined || y === undefined ? null : { getBoundingClientRect: () => DOMRect.fromRect({ x, y, width: 0, height: 0 }) }), [x, y]);
}

/** The last pointer pressed was a finger: a phone, or a tablet or an unfolded phone in the
 *  computer's layout. */
let touched = false;
/** The last thing pressed was a key: only then does the focus coming to a key show its tip. A
 *  card opened with the mouse had the focus put on its first key, and that key's tip came up with
 *  no pointer near it (kumo, 2026-11-22: 换头像 over the card, in the desktop app). */
let keyed = false;
if (typeof window !== "undefined") {
  addEventListener("pointerdown", (e) => ((touched = e.pointerType === "touch"), (keyed = false)), { capture: true, passive: true });
  addEventListener("keydown", () => (keyed = true), { capture: true, passive: true });
  // A finger's long press is ours (a row's menu) or nothing: never the browser's own download,
  // print, share, anywhere on the page (the user's unfolded phone in the computer's layout,
  // 2026-11-08). The browser keeps it only where it is of use: words being typed, words to choose
  // (a long press on them chooses them), a link, a picture.
  addEventListener(
    "contextmenu",
    (e) => {
      const el = e.target instanceof Element ? e.target : null;
      if (!touched || !el) return;
      if (el.closest("input, textarea, [contenteditable], a[href], img, video")) return;
      if (!getSelection()?.isCollapsed) return;
      if (!el.closest("aside, header, [role=treeitem], [role=menu]") && onWords(el, e.clientX, e.clientY)) return;
      e.preventDefault();
    },
    { capture: true },
  );
}

/** Whether a point is on words that may be chosen: a letter under it, not only near it, in what
 *  does not forbid choosing. */
function onWords(el: Element, x: number, y: number) {
  if (getComputedStyle(el).userSelect === "none") return false;
  const doc = document as Document & { caretPositionFromPoint?: (x: number, y: number) => { offsetNode: Node; offset: number } | null; caretRangeFromPoint?: (x: number, y: number) => Range | null };
  const p = doc.caretPositionFromPoint?.(x, y);
  const r = doc.caretRangeFromPoint?.(x, y);
  const node = p?.offsetNode ?? r?.startContainer;
  const at = p?.offset ?? r?.startOffset ?? 0;
  if (!node || node.nodeType !== Node.TEXT_NODE) return false;
  const text = node.textContent ?? "";
  // The letter before or after the caret, whichever the finger is on.
  for (const i of [at, at - 1]) {
    if (i < 0 || i >= text.length || !/\S/.test(text[i]!)) continue;
    const range = document.createRange();
    range.setStart(node, i);
    range.setEnd(node, i + 1);
    for (const b of range.getClientRects()) if (x >= b.left - 2 && x <= b.right + 2 && y >= b.top - 2 && y <= b.bottom + 2) return true;
  }
  return false;
}

/** The words behind an icon: on hover or keyboard focus; never for a finger, whose long press is a
 *  row's menu: the two came up together (the user, 2026-11-08). */
export function Tip({ label, children, side = "top", align = "center", tap }: { label: ReactNode; children: ReactElement; side?: "top" | "bottom" | "left" | "right"; align?: "start" | "center" | "end"; tap?: boolean }) {
  const [open, setOpen] = useState(false);
  useEffect(() => {
    if (!open) return;
    const close = () => setOpen(false);
    const id = window.setTimeout(() => addEventListener("pointerdown", close, { once: true }), 0);
    return () => (window.clearTimeout(id), removeEventListener("pointerdown", close));
  }, [open]);
  return (
    <Tooltip.Root
      open={open}
      onOpenChange={(o, d) => {
        const how = d as { reason?: string; event?: Event } | undefined;
        const byFocus = String(how?.reason ?? "").includes("focus") || !!how?.event?.type.startsWith("focus");
        setOpen(o && !touched && !(byFocus && !keyed));
      }}
    >
      {/* tap: pressed it shows too, a finger's way to it (the link's signal: no hover on a phone). */}
      <Tooltip.Trigger render={children} onClick={tap ? () => setOpen(true) : undefined} />
      <TooltipPortal>
        <Tooltip.Positioner side={side} align={align} sideOffset={6} collisionPadding={8} className="z-70">
          <Tooltip.Popup className="g-unfold glass thick float w-max max-w-80 rounded-md px-2 py-1 text-xs text-ink">{label}</Tooltip.Popup>
        </Tooltip.Positioner>
      </TooltipPortal>
    </Tooltip.Root>
  );
}

/** A square key with only an icon; its name is the tooltip and the aria-label. */
export function IconKey({ label, className, children, tipSide, ...rest }: ComponentProps<"button"> & { label: string; tipSide?: "top" | "bottom" | "left" | "right" }) {
  return (
    <Tip label={label} side={tipSide}>
      <button
        type="button"
        aria-label={label}
        className={cn("g-press grid size-8 shrink-0 place-items-center rounded-md text-ink-2 hover:bg-fill hover:text-ink disabled:opacity-40 pointer-coarse:size-10", className)}
        {...rest}
      >
        {children}
      </button>
    </Tip>
  );
}

/** Copies text; a check for a moment after (a command's, over it: docs/前端设计规范.md "命令"). */
export function CopyKey({ text }: { text: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <IconKey
      label={copied ? t("msg.copied") : t("msg.copy")}
      onClick={() => void navigator.clipboard.writeText(text).then(() => (setCopied(true), setTimeout(() => setCopied(false), 1500)))}
      className="-my-2 size-6 pointer-coarse:size-8"
    >
      {copied ? <Check size={13} /> : <Copy size={13} />}
    </IconKey>
  );
}

/** A Base UI portal that says what is in it is a popup's (api/query.ts InPopup: what it draws is
 *  fetched before it opens). Every popup goes through one of these, not Base UI's own
 *  (web/test/popdata.test.ts). */
export function popupPortal<P extends { children?: ReactNode }>(Portal: ComponentType<P>) {
  return function PopupPortal(p: P) {
    return (
      <Portal {...p}>
        <InPopup value>{p.children}</InPopup>
      </Portal>
    );
  };
}
export const MenuPortal = popupPortal(Menu.Portal);
export const PopoverPortal = popupPortal(Popover.Portal);
export const DialogPortal = popupPortal(Dialog.Portal);
export const TooltipPortal = popupPortal(Tooltip.Portal);

/** A popup of the page's own, not Base UI's (the composer's lists): kept drawn while it goes, with
 *  Base UI's data-ending-style, so the same CSS transitions (g-unfold: in from @starting-style)
 *  open and shut it, turned back midway by the next change. Gives whether to draw it (false once
 *  gone) and what to put on its surface. */
export function usePresence(open: boolean): { shown: boolean; attrs: Record<string, "" | undefined>; ref: (el: HTMLElement | null) => void } {
  const [shown, setShown] = useState(open);
  if (open && !shown) setShown(true);
  const ending = shown && !open;
  const el = useRef<HTMLElement | null>(null);
  // Ending: gone once its transitions are over; opened again first, it stays.
  useEffect(() => {
    if (!ending) return;
    const node = el.current;
    let off = false;
    const done = () => !off && setShown(false);
    const f = requestAnimationFrame(() => {
      const runs = node?.getAnimations?.() ?? [];
      if (!runs.length) return done();
      void Promise.all(runs.map((a) => a.finished)).then(done, () => {});
    });
    return () => ((off = true), cancelAnimationFrame(f));
  }, [ending]);
  const ref = useCallback((n: HTMLElement | null) => void (el.current = n), []);
  return { shown, attrs: { "data-ending-style": ending ? "" : undefined }, ref };
}

/** A popup of the page's own (the composer's lists over its box), as a menu is: frosted, unfolding
 *  out of from (its corner, the @ or / it grew from), folding away with what it showed last;
 *  nothing in it to press while it goes. */
export function OwnPopup({ open, from = "left bottom", className, style, children, ...rest }: { open: boolean; from?: string } & ComponentProps<"div">) {
  const p = usePresence(open);
  const last = useRef(children);
  if (open) last.current = children;
  if (!p.shown) return null;
  return (
    <div ref={p.ref} {...p.attrs} {...rest} inert={!open || undefined} style={{ "--transform-origin": from, ...style } as CSSProperties} className={cn("g-unfold glass thick float", className)}>
      <InPopup value>{last.current}</InPopup>
    </div>
  );
}

/** A popover's surface: frosted, unfolding out of its anchor. */
export function PopSurface({
  children,
  className,
  side = "bottom",
  align = "start",
  sideOffset = 8,
  initialFocus,
  finalFocus,
  anchor,
  still,
  style,
}: {
  children: ReactNode;
  className?: string;
  side?: "top" | "bottom" | "left" | "right";
  align?: "start" | "center" | "end";
  sideOffset?: number;
  initialFocus?: ComponentProps<typeof Popover.Popup>["initialFocus"];
  finalFocus?: ComponentProps<typeof Popover.Popup>["finalFocus"];
  /** What it unfolds out of, when not its trigger (a row it was opened from). */
  anchor?: Element | { getBoundingClientRect: () => DOMRect } | null;
  /** Out of something in a conversation (a face, a name): it stays where it opened while the
   *  conversation moves under it (a turn being written, the wheel). Following its anchor it came
   *  a frame behind each push, and flipped over the face as that neared an edge (kumo,
   *  2026-11-17). Needs anchor. */
  still?: boolean;
  style?: CSSProperties;
}) {
  return (
    <PopoverPortal>
      <Placed side={side} align={align} sideOffset={sideOffset} anchor={anchor} still={still}>
        <Popover.Popup initialFocus={initialFocus ?? ((type) => type === "keyboard")} finalFocus={finalFocus} style={style} className={cn("g-unfold glass thick float rounded-lg text-ink outline-none", className)}>
          {children}
        </Popover.Popup>
      </Placed>
    </PopoverPortal>
  );
}

/** PopSurface's place; drawn only while it is open, so a still one's anchor is where it was as
 *  it opened: its box then, not the element, which moves. */
function Placed({ children, side, align, sideOffset, anchor, still }: { children: ReactNode; side: "top" | "bottom" | "left" | "right"; align: "start" | "center" | "end"; sideOffset: number; anchor?: Element | { getBoundingClientRect: () => DOMRect } | null; still?: boolean }) {
  const [box] = useState(() => (still && anchor ? anchor.getBoundingClientRect() : null));
  const at = useMemo(() => (box ? { getBoundingClientRect: () => box } : undefined), [box]);
  // A still one's side is chosen once, as it opens: over its anchor where under it there is little
  // room and more over it. Not turned over after: what it holds grows and shrinks as it is read
  // (a step's card), and it went under, over, under (kumo, 2026-11-25).
  const [sure] = useState(() => {
    if (!box || side !== "bottom" || typeof window === "undefined") return side;
    const under = window.innerHeight - box.bottom;
    return under < 320 && box.top > under ? "top" : side;
  });
  return (
    <Popover.Positioner side={box ? sure : side} align={align} sideOffset={sideOffset} collisionPadding={8} collisionAvoidance={box ? { side: "none", align: "shift" } : undefined} anchor={at ?? anchor ?? undefined} className="z-50">
      {children}
    </Popover.Positioner>
  );
}

/** A switch: role switch, ink when on. */
export function Switch({ on, onChange, label, disabled, className }: { on: boolean; onChange: (on: boolean) => void; label: string; disabled?: boolean; className?: string }) {
  return <button type="button" role="switch" aria-checked={on} aria-label={label} disabled={disabled} onClick={() => onChange(!on)} className={cn("switch g-press", className)} />;
}

const AGENT_NAME: Record<Agent, string> = { claude: "Claude", codex: "Codex", pi: "pi", dsh: "DeepSeek Harness", agy: "Antigravity" };
/** A token count, short: 980, 12.3k, 4.5M. */
export const tokens = (n: number) => (n >= 1e9 ? `${(n / 1e9).toFixed(2)}B` : n >= 1_000_000 ? `${(n / 1_000_000).toFixed(1)}M` : n >= 1000 ? `${Math.round(n / 100) / 10}k` : `${n}`);
export const agentName = (a: Agent) => AGENT_NAME[a] ?? a;
export const AGENTS: Agent[] = ["claude", "codex", "pi", "dsh", "agy"];

/** An agent's mark, the only coloured thing in a row. */
export function Logo({ agent, size = 16, className }: { agent: Agent; size?: number; className?: string }) {
  const style: CSSProperties = { width: size, height: size };
  // No agent: a channel that is no session's (Session.bare), people talking (kumo, 2026-11-16: not #).
  if (!agent) return <UsersRound aria-hidden className={cn("shrink-0 text-ink-2", className)} style={style} strokeWidth={2} />;
  if (agent === "codex")
    return (
      <svg role="img" aria-label="Codex" viewBox={OPENAI_VIEWBOX} className={cn("shrink-0 text-ink", className)} style={style}>
        <path fill="currentColor" d={OPENAI} />
      </svg>
    );
  if (agent === "claude")
    return (
      <svg role="img" aria-label="Claude" viewBox="0 0 24 24" className={cn("shrink-0", className)} style={style}>
        <path fill="#D97757" d={CLAUDE} />
      </svg>
    );
  if (agent === "pi")
    return (
      <svg role="img" aria-label="pi" viewBox={PI_VIEWBOX} className={cn("shrink-0", className)} style={style}>
        {PI.map(([f, d]) => (
          <path key={f} fill={f} d={d} />
        ))}
      </svg>
    );
  if (agent === "agy") return <AgyMark className={className} style={style} />;
  // dsh: its own mark, the black whale of its web app's favicon (dsh-web-frontend), in the text colour
  // like Codex's: black on light, white on dark, as dsh draws it.
  return (
    <svg role="img" aria-label="DeepSeek Harness" viewBox={DSH_VIEWBOX} className={cn("shrink-0 text-ink", className)} style={style}>
      <path fill="currentColor" d={DSH} />
    </svg>
  );
}

/** Antigravity's mark as one cached image: shared across all rows instead of ten inline SVG blur
 *  filters per instance (which leaked graphics memory in WebKit). */
function AgyMark({ className, style }: { className?: string; style: CSSProperties }) {
  return <img src={AGY_DATA_URI} alt="Antigravity" draggable={false} className={cn("shrink-0 select-none", className)} style={style} />;
}

const STATUS_KEY = {
  running: "status.running",
  compacting: "status.compacting",
  waiting: "status.needs",
  error: "status.error",
  starting: "status.starting",
  elsewhere: "status.elsewhere",
  idle: "status.idle",
} as const;
export const statusText = (s: Status) => t(STATUS_KEY[s] ?? "status.idle");
/** The lamp a session shows: one running elsewhere that waits on you there (Claude says so in its
 *  registry) is amber as one here would be, though still read-only here. */
export const lampOf = (s: { status: Status; needs?: string }): Status => (s.status === "elsewhere" && s.needs ? "waiting" : s.status);
/** What a session's lamp says: elsewhere and waiting, that it waits on you there. */
export const sessionStatusText = (s: { status: Status; needs?: string }) => (s.status === "elsewhere" && s.needs ? t("status.needsElsewhere") : statusText(s.status));
/** The lamp's look: waiting shows as needs (amber). */
const LAMP: Record<Status, string> = { running: "running", compacting: "compacting", waiting: "needs", error: "error", starting: "starting", elsewhere: "elsewhere", idle: "idle" };

/** A session row's status lamp, at the row's top right (the user, 2026-11-06): green, breathing, as
 *  it works; idle has none. */
export function Lamp({ status }: { status: Status }) {
  if (status === "idle") return null;
  return <span key={status} className="row-lamp g-pop" data-s={LAMP[status]} role="img" aria-label={statusText(status)} />;
}

/** Iridium's own mark: "Ir" on a rounded square, in ink (or the colour given). */
export function IrMark({ size = 16, className, ground = "currentColor", round }: { size?: number; className?: string; ground?: string; round?: boolean }) {
  return (
    <svg viewBox="0 0 64 64" width={size} height={size} className={cn("shrink-0", className)} aria-hidden>
      <rect width="64" height="64" rx={round ? 32 : 14} fill={ground} />
      <path d="M20 18h6v28h-6zM32 28h5.6v3.2c1.4-2.4 3.6-3.6 6.4-3.6v5.8c-3.8 0-6.2 1.6-6.2 6V46H32z" fill="var(--paper)" />
    </svg>
  );
}

/** This Iridium's own face (GET /v1/internal/face; docs/app.md "服务器头像"), as it stands among
 *  others (the desktop app's list, the login page): round. None set: Iridium's mark in the circle.
 *  One set: the face, Iridium's mark small at its bottom right, a rounded square (kumo,
 *  2026-11-23). */
/** Whether the host's face at an address loaded: known for the page's life, so a face drawn again
 *  (a window resized, a pane mounted anew) starts as it was, not as an empty circle till the
 *  answer comes back (kumo, 2026-11-27). */
const serverFaces = new Map<string, boolean>();

export function ServerFace({ size = 32, over }: { size?: number; over?: ReactNode }) {
  const v = useCarried<number>("avatars") ?? 0;
  const src = `/v1/internal/face?v=${v}`;
  const [, seen] = useState(0);
  const known = serverFaces.get(src);
  const mark = Math.max(12, Math.round(size * 0.4));
  // Not known yet: Iridium's mark, the picture loading unseen over it, shown once it has come.
  const tell = (ok: boolean) => (serverFaces.set(src, ok), seen((n) => n + 1));
  return (
    <span className="relative inline-block shrink-0" style={{ width: size, height: size }}>
      {known !== true && <IrMark size={size} round className="text-ink" />}
      {known !== false && (
        <img src={src} alt="" draggable={false} onLoad={() => known || tell(true)} onError={() => tell(false)} className={cn("size-full rounded-full object-cover", known ? "" : "absolute inset-0 opacity-0")} style={{ boxShadow: "inset 0 0 0 0.5px var(--edge)" }} />
      )}
      {over}
      {known === true && (
        <span className="absolute -right-0.5 -bottom-0.5 grid rounded-[30%] bg-paper p-px">
          <IrMark size={mark} className="text-ink" />
        </span>
      )}
    </span>
  );
}

/** A logo with its status lamp on the bottom-right corner; idle has none. ir: Iridium's mark in
 *  the agent's place (the phone's key to the session list). */
export function LogoLamp({ agent, status, size = 16, ir, mark }: { agent: Agent; status: Status; size?: number; ir?: boolean; mark?: ReactNode }) {
  return (
    <span className="relative inline-grid shrink-0 place-items-center" style={mark ? { minWidth: size, height: size } : { width: size, height: size }}>
      {mark ?? (ir ? <IrMark size={size} className="text-ink" /> : <Logo agent={agent} size={size} />)}
      {status !== "idle" && (
        <span key={status} className="lamp g-pop" data-s={LAMP[status]} role="img" aria-label={statusText(status)}>
          <i />
        </span>
      )}
    </span>
  );
}

/** The mark an app's type puts on its avatar's corner, and the type's name; none for a type not
 *  known (types are display only). */
const TYPE_MARKS: Record<string, [LucideIcon, Parameters<typeof t>[0]]> = {
  computer: [Monitor, "apps.type.computer"],
  bot: [Bot, "apps.type.bot"],
  plugin: [Puzzle, "apps.type.plugin"],
  script: [ScrollText, "apps.type.script"],
};
export const typeMark = (type?: string) => (type ? TYPE_MARKS[type] : undefined);

/** Whether a channel's member ("<app>/<login>", "<app>/*") or a Satori user id is the host's. */
export const isHost = (member: string) => member === "local" || member.startsWith("local/");

/** An app's or login's avatar (host: Iridium's mark), or its initial on a disc (tinted, when given a colour); an app
 *  carries its type's mark on the corner (type), or another mark (badge: a device, Monitor, Smartphone). */
export function Avatar({ src, name, size = 20, type, badge, badgeSize, badgeLabel, tint, over, className, host, pattern }: { src?: string; name?: string; size?: number; type?: string; badge?: LucideIcon; badgeSize?: number; badgeLabel?: string; tint?: string; over?: ReactNode; className?: string; host?: boolean; pattern?: string }) {
  const [broken, setBroken] = useState<string | null>(null);
  const typed = badge ? undefined : typeMark(type);
  const mark = badge ?? typed?.[0];
  const label = badgeLabel ?? (typed && t(typed[1]));
  return (
    <span className={cn("relative inline-block shrink-0", className)} style={{ width: size, height: size }}>
      {host ? (
        // The host is Iridium itself: its mark, never a letter (the owner, 2026-11-15).
        <IrMark size={size} className="text-ink" />
      ) : src && broken !== src ? (
        <img src={src} alt="" draggable={false} onError={() => setBroken(src)} className="size-full rounded-full object-cover" style={{ boxShadow: "inset 0 0 0 0.5px var(--edge)" }} />
      ) : pattern ? (
        // None of its own: a face made from the seed, not a letter (kumo, 2026-11-27).
        <PatternFace seed={pattern} size={size} />
      ) : (
        <span
          className="grid size-full place-items-center rounded-full font-medium select-none"
          style={{ fontSize: Math.round(size * 0.45), background: tint ? `color-mix(in srgb, ${tint} 14%, var(--paper))` : "var(--fill-2)", color: tint ?? "var(--ink-2)" }}
        >
          {[...(name ?? "?")][0]?.toUpperCase()}
        </span>
      )}
      {over}
      {mark && <Badge icon={mark} size={badgeSize ?? Math.max(10, Math.round(size * 0.5))} label={label} />}
    </span>
  );
}

/** An avatar that a press changes: under a pointer a shade over the face, the circle exactly, and a
 *  camera on it; the type's mark stays on top (the user, 2026-11-12). */
export function AvatarKey({ label, onClick, ...face }: { label: string; onClick: () => void } & Omit<ComponentProps<typeof Avatar>, "over" | "className">) {
  const size = face.size ?? 20;
  return (
    <Tip label={label}>
      <button type="button" aria-label={label} onClick={onClick} className="group g-press grid shrink-0 rounded-full">
        <Avatar {...face} over={<FaceChangeOver size={size} />} />
      </button>
    </Tip>
  );
}

/** What a face that changes the face shows over itself under a pointer: a camera on a dark veil
 *  (AvatarKey's; a channel's or an agent's face in 编辑资料 the same, docs/前端设计规范.md "点头像
 *  就是换头像"). Its parent is a .group, positioned and round. */
export function FaceChangeOver({ size }: { size: number }) {
  return (
    <span className="absolute inset-0 grid place-items-center rounded-full bg-[oklch(0_0_0/0.45)] text-[oklch(1_0_0)] opacity-0 transition-opacity duration-(--dur-2) group-hover:opacity-100 group-focus-visible:opacity-100" aria-hidden>
      <Camera size={Math.round(size * 0.4)} strokeWidth={2} />
    </span>
  );
}

/** A small mark on a face's bottom-right corner (a plug, a device, an agent), ringed in the colour
 *  under it so it reads apart from the face. Its parent is positioned. */
export function Badge({ icon: Icon, size, label }: { icon: LucideIcon; size: number; label?: string }) {
  return (
    <span
      role={label ? "img" : undefined}
      aria-label={label}
      aria-hidden={label ? undefined : true}
      className="absolute grid place-items-center rounded-full bg-fill-2 text-ink-2"
      style={{ width: size, height: size, right: -3, bottom: -3, boxShadow: "0 0 0 1.5px var(--row-bg, var(--paper))" }}
    >
      <Icon strokeWidth={2.5} style={{ width: size * 0.66, height: size * 0.66 }} />
    </span>
  );
}

/** What storage keeps under key, or null; storage that cannot be used keeps none. */
export function kept(key: string, storage: () => Storage = () => localStorage): string | null {
  try {
    return storage().getItem(key);
  } catch {
    return null;
  }
}
/** Keeps value under key; null, none. */
export function keep(key: string, value: string | null, storage: () => Storage = () => localStorage) {
  try {
    if (value === null) storage().removeItem(key);
    else storage().setItem(key, value);
  } catch {}
}

/** The name a session shows: the one the user or its agent gave, else its first message's opening,
 *  else 新会话 (drawn faint: untitled). */
export const titleOf = (s: { name?: string; title?: string }) => s.name || s.title || t("session.new");
/** Nothing said in it yet, nor a name given: titleOf is only 新会话. */
export const untitled = (s: { name?: string; title?: string }) => !s.name && !s.title;

/** The home directory as a path shows it: /home/<user>, /Users/<user> or /root. */
export const homeOf = (p: string) => /^(\/home\/[^/]+|\/Users\/[^/]+|\/root)(?=\/|$)/.exec(p)?.[1];

/** The home folder as the daemon resolves ~ (src/data.ts useHome), once known. */
let knownHome: string | undefined;
export const setHome = (h: string) => (knownHome = h);

/** ~ for the home directory at a path's start. */
export function tilde(p: string, home = knownHome ?? homeOf(p)) {
  return home && (p === home || p.startsWith(home + "/")) ? "~" + p.slice(home.length) : p;
}

/** A row that rises in as it comes (.g-arrive), decided once, as it is first drawn: a class that
 *  came and went with what else is on the page (the row alone or not) played its coming again on
 *  a row long there. on: whether it rises in at all; off: no longer (it is leaving). */
export function Arrive({ on, off, as = "g-arrive", className, children }: { on: boolean; off?: boolean; as?: "g-arrive" | "g-fade"; className?: string; children: ReactNode }) {
  const first = useRef(on);
  return <div className={cn(first.current && !off && as, className)}>{children}</div>;
}
