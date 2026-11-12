// Shared pieces (from the style sample, web/sample): tooltips that also open on long-press, the
// popover shell, icon keys, agent logos with their status lamp, terminal avatars, switches.
import { Popover } from "@base-ui/react/popover";
import { Tooltip } from "@base-ui/react/tooltip";
import { Bot, Camera, Monitor, Puzzle, ScrollText, type LucideIcon } from "lucide-react";
import { useCallback, useEffect, useRef, useState, useSyncExternalStore, type ComponentProps, type CSSProperties, type ReactElement, type ReactNode } from "react";
import type { Agent, Status } from "./api/types.gen";
import { t } from "./i18n";
import { CLAUDE, DSH, DSH_VIEWBOX, OPENAI, OPENAI_VIEWBOX, PI, PI_VIEWBOX } from "./logos";

export const cn = (...xs: (string | false | null | undefined)[]) => xs.filter(Boolean).join(" ");

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

const mac = () => /Mac|iPhone|iPad/.test(navigator.platform);
/** The other key for words in a turn, as Claude Code's ctrl+enter (send now); Enter itself. */
export const steerKey = () => (mac() ? "⌘↩" : "Ctrl+Enter");
export const enterKey = () => (mac() ? "↩" : "Enter");
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

/** Long-press on touch: fires after 450 ms held still, and swallows the click that follows. Not for
 *  a press in a portal of its children (a row's "+" popover): React may hand one pointerdown to a
 *  row twice from there, from the portal's container and from <body>, and the first timer would be
 *  lost, never cleared, opening the row's menu after a plain tap (the user, 2026-11-06). */
export function useLongPress(onLong: () => void) {
  const timer = useRef<number | undefined>(undefined);
  const fired = useRef(false);
  const start = useRef({ x: 0, y: 0 });
  const clear = () => window.clearTimeout(timer.current);
  return {
    onPointerDown: (e: React.PointerEvent) => {
      if (e.pointerType !== "touch" || !own(e)) return;
      clear();
      fired.current = false;
      start.current = { x: e.clientX, y: e.clientY };
      timer.current = window.setTimeout(() => {
        fired.current = true;
        buzzHeld();
        onLong();
      }, 450);
    },
    onPointerMove: (e: React.PointerEvent) => {
      if (Math.hypot(e.clientX - start.current.x, e.clientY - start.current.y) > 8) clear();
    },
    onPointerUp: clear,
    onPointerCancel: clear,
    onContextMenu: (e: React.MouseEvent) => {
      if (fired.current) e.preventDefault();
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

/** The last pointer pressed was a finger: a phone, or a tablet or an unfolded phone in the
 *  computer's layout. */
let touched = false;
if (typeof window !== "undefined") {
  addEventListener("pointerdown", (e) => (touched = e.pointerType === "touch"), { capture: true, passive: true });
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
export function Tip({ label, children, side = "top", align = "center" }: { label: ReactNode; children: ReactElement; side?: "top" | "bottom" | "left" | "right"; align?: "start" | "center" | "end" }) {
  const [open, setOpen] = useState(false);
  useEffect(() => {
    if (!open) return;
    const close = () => setOpen(false);
    const id = window.setTimeout(() => addEventListener("pointerdown", close, { once: true }), 0);
    return () => (window.clearTimeout(id), removeEventListener("pointerdown", close));
  }, [open]);
  return (
    <Tooltip.Root open={open} onOpenChange={(o) => setOpen(o && !touched)}>
      <Tooltip.Trigger render={children} />
      <Tooltip.Portal>
        <Tooltip.Positioner side={side} align={align} sideOffset={6} collisionPadding={8} className="z-70">
          <Tooltip.Popup className="g-unfold glass thick float w-max max-w-80 rounded-md px-2 py-1 text-xs text-ink">{label}</Tooltip.Popup>
        </Tooltip.Positioner>
      </Tooltip.Portal>
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

/** A popover's surface: frosted, unfolding out of its anchor. */
export function PopSurface({
  children,
  className,
  side = "bottom",
  align = "start",
  sideOffset = 8,
  initialFocus,
  finalFocus,
}: {
  children: ReactNode;
  className?: string;
  side?: "top" | "bottom" | "left" | "right";
  align?: "start" | "center" | "end";
  sideOffset?: number;
  initialFocus?: ComponentProps<typeof Popover.Popup>["initialFocus"];
  finalFocus?: ComponentProps<typeof Popover.Popup>["finalFocus"];
}) {
  return (
    <Popover.Portal>
      <Popover.Positioner side={side} align={align} sideOffset={sideOffset} collisionPadding={8} className="z-50">
        <Popover.Popup initialFocus={initialFocus ?? ((type) => type === "keyboard")} finalFocus={finalFocus} className={cn("g-unfold glass thick float rounded-lg text-ink outline-none", className)}>
          {children}
        </Popover.Popup>
      </Popover.Positioner>
    </Popover.Portal>
  );
}

/** A switch: role switch, ink when on. */
export function Switch({ on, onChange, label, disabled, className }: { on: boolean; onChange: (on: boolean) => void; label: string; disabled?: boolean; className?: string }) {
  return <button type="button" role="switch" aria-checked={on} aria-label={label} disabled={disabled} onClick={() => onChange(!on)} className={cn("switch g-press", className)} />;
}

const AGENT_NAME: Record<Agent, string> = { claude: "Claude", codex: "Codex", pi: "pi", dsh: "DeepSeek Harness" };
/** A token count, short: 980, 12.3k, 4.5M. */
export const tokens = (n: number) => (n >= 1e9 ? `${(n / 1e9).toFixed(2)}B` : n >= 1_000_000 ? `${(n / 1_000_000).toFixed(1)}M` : n >= 1000 ? `${Math.round(n / 100) / 10}k` : `${n}`);
export const agentName = (a: Agent) => AGENT_NAME[a] ?? a;
export const AGENTS: Agent[] = ["claude", "codex", "pi", "dsh"];

/** An agent's mark, the only coloured thing in a row. */
export function Logo({ agent, size = 16, className }: { agent: Agent; size?: number; className?: string }) {
  const style: CSSProperties = { width: size, height: size };
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
  // dsh: its own mark, the black whale of its web app's favicon (dsh-web-frontend), in the text colour
  // like Codex's: black on light, white on dark, as dsh draws it.
  return (
    <svg role="img" aria-label="DeepSeek Harness" viewBox={DSH_VIEWBOX} className={cn("shrink-0 text-ink", className)} style={style}>
      <path fill="currentColor" d={DSH} />
    </svg>
  );
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
export function IrMark({ size = 16, className, ground = "currentColor" }: { size?: number; className?: string; ground?: string }) {
  return (
    <svg viewBox="0 0 64 64" width={size} height={size} className={cn("shrink-0", className)} aria-hidden>
      <rect width="64" height="64" rx="14" fill={ground} />
      <path d="M20 18h6v28h-6zM32 28h5.6v3.2c1.4-2.4 3.6-3.6 6.4-3.6v5.8c-3.8 0-6.2 1.6-6.2 6V46H32z" fill="var(--paper)" />
    </svg>
  );
}

/** A logo with its status lamp on the bottom-right corner; idle has none. ir: Iridium's mark in
 *  the agent's place (the phone's key to the session list). */
export function LogoLamp({ agent, status, size = 16, ir }: { agent: Agent; status: Status; size?: number; ir?: boolean }) {
  return (
    <span className="relative inline-grid shrink-0 place-items-center" style={{ width: size, height: size }}>
      {ir ? <IrMark size={size} className="text-ink" /> : <Logo agent={agent} size={size} />}
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

/** An app's or login's avatar, or its initial on a disc (tinted, when given a colour); an app
 *  carries its type's mark on the corner (type), or another mark (badge: a device, Monitor, Smartphone). */
export function Avatar({ src, name, size = 20, type, badge, badgeSize, badgeLabel, tint, over, className }: { src?: string; name?: string; size?: number; type?: string; badge?: LucideIcon; badgeSize?: number; badgeLabel?: string; tint?: string; over?: ReactNode; className?: string }) {
  const [broken, setBroken] = useState<string | null>(null);
  const typed = badge ? undefined : typeMark(type);
  const mark = badge ?? typed?.[0];
  const label = badgeLabel ?? (typed && t(typed[1]));
  return (
    <span className={cn("relative inline-block shrink-0", className)} style={{ width: size, height: size }}>
      {src && broken !== src ? (
        <img src={src} alt="" draggable={false} onError={() => setBroken(src)} className="size-full rounded-full object-cover" style={{ boxShadow: "inset 0 0 0 0.5px var(--edge)" }} />
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
        <Avatar
          {...face}
          over={
            <span className="absolute inset-0 grid place-items-center rounded-full bg-[oklch(0_0_0/0.45)] text-[oklch(1_0_0)] opacity-0 transition-opacity duration-(--dur-2) group-hover:opacity-100 group-focus-visible:opacity-100" aria-hidden>
              <Camera size={Math.round(size * 0.4)} strokeWidth={2} />
            </span>
          }
        />
      </button>
    </Tip>
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
