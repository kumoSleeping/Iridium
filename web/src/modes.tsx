// The approval modes' names in the page's language (the mode key, the settings' defaults); a mode
// not known here shows as the agent calls it. Each also has a mark, for where its name does not fit.
import { t } from "./i18n";

const MODE_KEY: Record<string, Parameters<typeof t>[0]> = {
  default: "mode.default",
  manual: "mode.default",
  ask: "mode.default",
  acceptEdits: "mode.acceptEdits",
  plan: "mode.plan",
  auto: "mode.auto",
  dontAsk: "mode.dontAsk",
  bypassPermissions: "mode.bypass",
  untrusted: "mode.untrusted",
  "on-request": "mode.onRequest",
  granular: "mode.granular",
  never: "mode.never",
  "read-only": "mode.readOnly",
  "workspace-write": "mode.workspaceWrite",
  "danger-full-access": "mode.fullAccess",
};

export const modeName = (m: { id: string }) => (MODE_KEY[m.id] ? t(MODE_KEY[m.id]!) : m.id);

// The marks, drawn as lucide's (24 grid, stroke 2): a speech bubble for whether it asks, a shield
// for who approves, dsh's sandboxes as what they let in.
const BUBBLE = "M7.9 20A9 9 0 1 0 4 16.1L2 22Z";
const SHIELD = "M20 13c0 5-3.5 7.5-7.66 8.95a1 1 0 0 1-.67-.01C7.5 20.5 4 18 4 13V6a1 1 0 0 1 1-1c2 0 4.5-1.2 6.24-2.72a1.17 1.17 0 0 1 1.52 0C14.51 3.81 17 5 19 5a1 1 0 0 1 1 1z";
const ASK = [BUBBLE, "M9.1 9a3 3 0 0 1 5.8 1c0 2-2.9 2.5-2.9 2.5", "M12 16.5h.01"];
const MARKS: Record<string, string[]> = {
  default: ASK,
  manual: ASK,
  ask: ASK,
  acceptEdits: ["M15 3.5a2.12 2.12 0 0 1 3 3L8.5 16 4 17.5 5.5 13Z", "m14 19 2 2 5-5"],
  plan: ["M9 2h6a1 1 0 0 1 1 1v2a1 1 0 0 1-1 1H9a1 1 0 0 1-1-1V3a1 1 0 0 1 1-1Z", "M16 4h2a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2V6a2 2 0 0 1 2-2h2", "M12 11h4", "M12 16h4", "M8 11h.01", "M8 16h.01"],
  auto: [SHIELD, "m9 12 2 2 4-4"],
  dontAsk: [SHIELD, "m9.5 9.5 5 5", "m14.5 9.5-5 5"],
  bypassPermissions: ["m2 2 20 20", "M5 5a1 1 0 0 0-1 1v7c0 5 3.5 7.5 7.67 8.94a1 1 0 0 0 .67.01c2.35-.82 4.48-1.97 5.9-3.71", "M9.31 3.65a12.25 12.25 0 0 0 1.93-1.37 1.17 1.17 0 0 1 1.52 0C14.51 3.81 17 5 19 5a1 1 0 0 1 1 1v7a9.78 9.78 0 0 1-.08 1.26"],
  untrusted: [BUBBLE, "M12 7.5v4.5", "M12 16h.01"],
  "on-request": [BUBBLE, "M8 12h.01", "M12 12h.01", "M16 12h.01"],
  granular: ["M21 4h-7", "M10 4H3", "M21 12h-9", "M8 12H3", "M21 20h-5", "M12 20H3", "M14 2v4", "M8 10v4", "M16 18v4"],
  never: [BUBBLE, "m8.5 8.5 7 7"],
  "read-only": ["M2.06 12.35a1 1 0 0 1 0-.7 10.75 10.75 0 0 1 19.88 0 1 1 0 0 1 0 .7 10.75 10.75 0 0 1-19.88 0", "M9 12a3 3 0 1 0 6 0 3 3 0 1 0-6 0"],
  "workspace-write": ["M2 11.5V5a2 2 0 0 1 2-2h3.9a2 2 0 0 1 1.69.9l.81 1.2a2 2 0 0 0 1.67.9H20a2 2 0 0 1 2 2v10a2 2 0 0 1-2 2h-9.5", "M12.4 12.6a2.12 2.12 0 0 0-3-3L3 16v4h4Z"],
  "danger-full-access": ["M5 11h14a2 2 0 0 1 2 2v7a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-7a2 2 0 0 1 2-2Z", "M7 11V7a5 5 0 0 1 9.9-1"],
};

export const hasMark = (id: string) => id in MARKS;

/** A mode's mark; null for a mode not known here. */
export function ModeMark({ id, size = 16, className }: { id: string; size?: number; className?: string }) {
  const d = MARKS[id];
  if (!d) return null;
  return (
    <svg viewBox="0 0 24 24" width={size} height={size} fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round" aria-hidden className={className}>
      {d.map((p) => (
        <path key={p} d={p} />
      ))}
    </svg>
  );
}
