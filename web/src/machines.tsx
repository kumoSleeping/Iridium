// Other machines' executors (docs/exec.md "网页"): a Bot each, its agents offered for new sessions,
// its sessions marked with its face on the agent's logo. Its face is its Bot's avatar, or by its
// system: a Windows window, a laptop for a Mac, a monitor for Linux.
import { Laptop, Monitor } from "lucide-react";
import { useEffect, useState, useSyncExternalStore } from "react";
import type { Machine } from "./api/types.gen";
import { useMachines } from "./store";
import { cn, keep, kept } from "./ui";

/** The machine a session or a folder group runs at: by ID, as the carried "machines" lists them. */
export const useMachine = (id?: string) => {
  const ms = useMachines();
  return id ? ms.find((m) => m.id === id) : undefined;
};

/** A path at a machine as the folder picker and groups write it: "<machine>:<path>", '/'-separated. */
export const atMachine = (m: Machine, path: string) => `${m.id}:${path}`;

/** A machine's home, '/'-separated as the page writes its paths. */
export const homeAt = (m: Machine) => (m.sep === "\\" ? m.home.replaceAll("\\", "/") : m.home);

/** "<machine>:<path>" split: the machine and the page's path there; undefined for this machine's. */
export function pageAt(path: string, ms: Machine[]): { m: Machine; path: string } | undefined {
  const m = ms.find((x) => path.startsWith(`${x.id}:`));
  return m && { m, path: path.slice(m.id.length + 1) };
}

/** A remote folder group's path ("<machine>:<path>", '/'-separated) as the machine and its own
 *  path there; undefined for this machine's. */
export function remoteOf(path: string, ms: Machine[]): { m: Machine; cwd: string } | undefined {
  const m = ms.find((x) => path.startsWith(`${x.id}:`));
  if (!m) return undefined;
  const p = path.slice(m.id.length + 1);
  return { m, cwd: m.sep === "\\" ? p.replaceAll("/", "\\") : p };
}

function WindowsMark({ size }: { size: number }) {
  return (
    <svg viewBox="0 0 16 16" width={size} height={size} aria-hidden>
      <path fill="currentColor" d="M1 2.6 6.7 1.8v5.6H1zm6.6-.9L15 .7v6.7H7.6zM1 8.3h5.7v5.6L1 13.1zm6.6 0H15V15l-7.4-1z" />
    </svg>
  );
}

/** A machine's face: its avatar, else its system's mark. */
export function MachineFace({ m, size, className }: { m: Machine; size: number; className?: string }) {
  if (m.avatar) return <img src={m.avatar} alt="" draggable={false} className={cn("shrink-0 rounded-full object-cover", className)} style={{ width: size, height: size }} />;
  const inner = Math.round(size * 0.66);
  return (
    <span className={cn("grid shrink-0 place-items-center rounded-full bg-fill-2 text-ink-2", className)} style={{ width: size, height: size }}>
      {m.os === "windows" ? <WindowsMark size={inner} /> : m.os === "darwin" ? <Laptop size={inner} strokeWidth={2.5} /> : <Monitor size={inner} strokeWidth={2.5} />}
    </span>
  );
}

/** The machine's face on an agent logo's bottom-right corner, ringed in the colour under it. */
export function MachineBadge({ m, size = 11 }: { m: Machine; size?: number }) {
  return (
    <span role="img" aria-label={m.name} className="absolute -right-1 -bottom-1 grid place-items-center rounded-full" style={{ boxShadow: "0 0 0 1.5px var(--row-bg, var(--paper))" }}>
      <MachineFace m={m} size={size} />
    </span>
  );
}

/** A path as the page shows it: at another machine ("<machine>:<path>"), with ~ for its home,
 *  its machine, and the path as that machine writes it; this machine's as given. */
export function shownPath(path: string, ms: Machine[]): { m?: Machine; label: string; real: string } {
  const r = remoteOf(path, ms);
  if (!r) return { label: path, real: path };
  const p = path.slice(r.m.id.length + 1);
  const home = homeAt(r.m);
  return { m: r.m, label: p === home || p.startsWith(home + "/") ? "~" + p.slice(home.length) : p, real: r.cwd };
}

/** on, once it has stayed so for ms: a connection gone for a moment (a proxy's cut, a Mac waking)
 *  is not shown as gone. */
export function useLate(on: boolean, ms = AWAY_AFTER): boolean {
  const [late, setLate] = useState(false);
  useEffect(() => {
    if (!on) return setLate(false);
    const t = setTimeout(() => setLate(true), ms);
    return () => clearTimeout(t);
  }, [on, ms]);
  return on && late;
}

/** How long a machine is gone before the page says so. */
export const AWAY_AFTER = 10_000;
/** A round trip above it is slow (the server's SlowRTT). */
const SLOW_MS = 300;

/** A machine's connection: on, slow (or gone a moment), away. */
export function useLink(m: Machine): "on" | "slow" | "away" {
  const away = useLate(!m.online);
  return away ? "away" : !m.online || (m.latency ?? 0) > SLOW_MS ? "slow" : "on";
}

export const LINK_COLOR = { on: "var(--plus)", slow: "var(--need)", away: "var(--err)" };

/** What the session list shows (sidebar.tsx ViewSwitch): null all, "" this machine, else another's
 *  ID; kept in this browser. The head's "+" opens at the machine shown. */
const VIEW_KEY = "ir.machineView";
const viewers = new Set<() => void>();
export function setView(v: string | null) {
  keep(VIEW_KEY, v);
  viewers.forEach((f) => f());
}
/** The view, null once its machine is no more listed. */
export function useView(): string | null {
  const v = useSyncExternalStore(
    (f) => (viewers.add(f), () => void viewers.delete(f)),
    () => kept(VIEW_KEY),
  );
  const ms = useMachines();
  return v === null || (v !== "" && !ms.some((m) => m.id === v)) ? null : v;
}
