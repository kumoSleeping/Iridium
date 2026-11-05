// Shared pieces: tooltips that also open on long-press, popover shell, icon buttons, agent logos with
// their status lamp, and terminal avatars.
import { Popover } from "@base-ui/react/popover";
import { Tooltip } from "@base-ui/react/tooltip";
import { Plug } from "lucide-react";
import { useEffect, useRef, useState, type ComponentProps, type CSSProperties, type ReactElement, type ReactNode } from "react";
import type { Agent, Status } from "./data";
import { t } from "./i18n";
import { CLAUDE, PI, PI_VIEWBOX } from "./logos";

export const cn = (...xs: (string | false | null | undefined)[]) => xs.filter(Boolean).join(" ");

export const params = new URLSearchParams(location.search);

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
        className={cn("g-press grid size-8 shrink-0 place-items-center rounded-md text-ink-2 hover:bg-fill hover:text-ink pointer-coarse:size-10", className)}
        {...rest}
      >
        {children}
      </button>
    </Tip>
  );
}

/** A popover's surface: frosted, unfolding out of its anchor. */
export function PopSurface({ children, className, side = "bottom", align = "start", sideOffset = 8, initialFocus, finalFocus }: { children: ReactNode; className?: string; side?: "top" | "bottom" | "left" | "right"; align?: "start" | "center" | "end"; sideOffset?: number; initialFocus?: ComponentProps<typeof Popover.Popup>["initialFocus"]; finalFocus?: ComponentProps<typeof Popover.Popup>["finalFocus"] }) {
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

const AGENT_NAME: Record<Agent, string> = { claude: "Claude", codex: "Codex", pi: "pi", dsh: "DeepSeek Harness" };
export const agentName = (a: Agent) => AGENT_NAME[a];

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
  needs: "status.needs",
  error: "status.error",
  starting: "status.starting",
  elsewhere: "status.elsewhere",
  idle: "status.idle",
} as const;
export const statusText = (s: Status) => t(STATUS_KEY[s]);

/** A logo with its status lamp on the bottom-right corner; idle has none. */
export function LogoLamp({ agent, status, size = 16 }: { agent: Agent; status: Status; size?: number }) {
  return (
    <span className="relative inline-grid shrink-0 place-items-center" style={{ width: size, height: size }}>
      <Logo agent={agent} size={size} />
      {status !== "idle" && (
        <span key={status} className="lamp g-pop" data-s={status} role="img" aria-label={statusText(status)}>
          <i />
        </span>
      )}
    </span>
  );
}

/** A terminal's avatar; channel-type terminals (plugins, peers) carry a small plug on the corner. */
export function Avatar({ src, size = 20, channel, className }: { src: string; size?: number; channel?: boolean; className?: string }) {
  const badge = Math.max(10, Math.round(size * 0.5));
  return (
    <span className={cn("relative inline-block shrink-0", className)} style={{ width: size, height: size }}>
      <img src={src} alt="" draggable={false} className="size-full rounded-full object-cover" style={{ boxShadow: "inset 0 0 0 0.5px var(--edge)" }} />
      {channel && (
        <span className="absolute grid place-items-center rounded-full bg-fill-2 text-ink-2" style={{ width: badge, height: badge, right: -3, bottom: -3, boxShadow: "0 0 0 1.5px var(--row-bg, var(--paper))" }}>
          <Plug strokeWidth={2.5} style={{ width: badge * 0.66, height: badge * 0.66 }} />
        </span>
      )}
    </span>
  );
}
