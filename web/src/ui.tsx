// Shared pieces (from the style sample, web/sample): tooltips that also open on long-press, the
// popover shell, icon keys, agent logos with their status lamp, terminal avatars, switches.
import { Popover } from "@base-ui/react/popover";
import { Tooltip } from "@base-ui/react/tooltip";
import { Plug, type LucideIcon } from "lucide-react";
import { useEffect, useRef, useState, useSyncExternalStore, type ComponentProps, type CSSProperties, type ReactElement, type ReactNode } from "react";
import type { Agent, Status } from "./api/types.gen";
import { t } from "./i18n";
import { CLAUDE, PI, PI_VIEWBOX } from "./logos";

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

/** Long-press on touch: fires after 450 ms held still, and swallows the click that follows. */
export function useLongPress(onLong: () => void) {
  const timer = useRef<number | undefined>(undefined);
  const fired = useRef(false);
  const start = useRef({ x: 0, y: 0 });
  const clear = () => window.clearTimeout(timer.current);
  return {
    onPointerDown: (e: React.PointerEvent) => {
      if (e.pointerType !== "touch") return;
      fired.current = false;
      start.current = { x: e.clientX, y: e.clientY };
      timer.current = window.setTimeout(() => {
        fired.current = true;
        navigator.vibrate?.(8);
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

/** The words behind an icon: on hover or keyboard focus, or on long-press on touch. */
export function Tip({ label, children, side = "top", align = "center" }: { label: ReactNode; children: ReactElement; side?: "top" | "bottom" | "left" | "right"; align?: "start" | "center" | "end" }) {
  const [open, setOpen] = useState(false);
  const lp = useLongPress(() => setOpen(true));
  useEffect(() => {
    if (!open) return;
    const close = () => setOpen(false);
    const id = window.setTimeout(() => addEventListener("pointerdown", close, { once: true }), 0);
    return () => (window.clearTimeout(id), removeEventListener("pointerdown", close));
  }, [open]);
  return (
    <Tooltip.Root open={open} onOpenChange={setOpen}>
      <Tooltip.Trigger render={children} {...lp} />
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
export const agentName = (a: Agent) => AGENT_NAME[a] ?? a;
export const AGENTS: Agent[] = ["claude", "codex", "pi", "dsh"];

/** An agent's mark, the only coloured thing in a row. */
export function Logo({ agent, size = 16, className }: { agent: Agent; size?: number; className?: string }) {
  const style: CSSProperties = { width: size, height: size };
  if (agent === "codex")
    return <span role="img" aria-label="Codex" className={cn("inline-block shrink-0 bg-ink", className)} style={{ ...style, mask: "url(/logos/openai.svg) center / contain no-repeat", WebkitMask: "url(/logos/openai.svg) center / contain no-repeat" }} />;
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
  // dsh: placeholder whale until DeepSeek's mark is checked.
  return (
    <svg role="img" aria-label="DeepSeek Harness" viewBox="0 0 24 24" className={cn("shrink-0", className)} style={style}>
      <path fill="#4D6BFE" d="M2.5 12.6c0-4.2 3.7-7.3 8.6-7.3 4.3 0 7.4 2.3 8.4 5.5l1.8-1.9c.4-.4 1.1-.1 1 .5l-.6 3.3.9 2.7c.2.6-.4 1-.9.7l-2-1.3c-1.4 3.2-4.6 5.2-8.6 5.2-5 0-8.6-3.1-8.6-7.4z" />
      <circle cx="7.6" cy="11.2" r="1.15" fill="var(--paper)" />
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

/** A terminal's avatar, or its initial on a neutral disc; channel-type terminals (plugins, peers)
 *  carry a small plug on the corner, or badge another mark (a device: Monitor, Smartphone). */
export function Avatar({ src, name, size = 20, channel, badge, badgeSize, badgeLabel, className }: { src?: string; name?: string; size?: number; channel?: boolean; badge?: LucideIcon; badgeSize?: number; badgeLabel?: string; className?: string }) {
  const [broken, setBroken] = useState<string | null>(null);
  const mark = badge ?? (channel ? Plug : undefined);
  return (
    <span className={cn("relative inline-block shrink-0", className)} style={{ width: size, height: size }}>
      {src && broken !== src ? (
        <img src={src} alt="" draggable={false} onError={() => setBroken(src)} className="size-full rounded-full object-cover" style={{ boxShadow: "inset 0 0 0 0.5px var(--edge)" }} />
      ) : (
        <span className="grid size-full place-items-center rounded-full bg-fill-2 font-medium text-ink-2 select-none" style={{ fontSize: Math.round(size * 0.45) }}>
          {[...(name ?? "?")][0]?.toUpperCase()}
        </span>
      )}
      {mark && <Badge icon={mark} size={badgeSize ?? Math.max(10, Math.round(size * 0.5))} label={badgeLabel} />}
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

/** The name a session shows: its new name, else its first message's opening, else its folder's. */
export const titleOf = (s: { name?: string; title?: string; cwd: string }) => s.name || s.title || s.cwd.split("/").filter(Boolean).pop() || s.cwd;

/** The home directory as a path shows it: /home/<user>, /Users/<user> or /root. */
export const homeOf = (p: string) => /^(\/home\/[^/]+|\/Users\/[^/]+|\/root)(?=\/|$)/.exec(p)?.[1];

/** The home folder as the daemon resolves ~ (src/data.ts useHome), once known. */
let knownHome: string | undefined;
export const setHome = (h: string) => (knownHome = h);

/** ~ for the home directory at a path's start. */
export function tilde(p: string, home = knownHome ?? homeOf(p)) {
  return home && (p === home || p.startsWith(home + "/")) ? "~" + p.slice(home.length) : p;
}
