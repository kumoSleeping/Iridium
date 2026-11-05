// Types of the routes and fields the page uses that internal/api does not have yet (docs/plan.md
// describes them; other work adds them). Each is written against the plan's shape and used only
// through src/api/client.ts; once the Go side has it, its generated type replaces the one here.
import type { Agent, SessionState } from "./types.gen";

export type Command = { name: string; description?: string; argumentHint?: string };

/** The fields of the carried "session:<id>" the page reads beyond internal/api's SessionState: the
 *  agent's own commands, for the palette. */
export type LiveSession = SessionState & { commands?: Command[] };

/** GET/PUT /api/settings: the config file's fields the settings page edits. PUT takes a part. */
export type Settings = {
  idleReap: string; // "1h", "0" for never
  crashResume: boolean;
  quotaResume: boolean;
  /** Quota bars hidden, by quotaKey(). */
  quotaHidden: string[];
};

/** GET /api/update: whether a new version is out; POST /api/update installs it. */
export type UpdateState = { current: string; latest?: string; notes?: string; available: boolean; checkedAt?: number };

/** What this terminal may do (plan "交互终端"), read-only here. */
export type Permissions = {
  agents: Agent[];
  workspaces: "all" | number[];
  others: boolean;
  copyOut: boolean;
  copyIn: boolean;
  answer: boolean;
  mustApprove: boolean;
  daily?: number; // tokens a day; none: unlimited
};

/** GET /api/me: this terminal. local: the built-in 本机, with no Passkey gate. */
export type Me = { id: string; name: string; kind: "panel" | "channel"; local: boolean; avatar?: string; permissions?: Permissions };

/** GET /api/endpoints: the terminals, so a message's source shows its avatar and kind. */
export type Endpoint = { id: string; name: string; kind: "panel" | "channel"; avatar?: string; session?: string };
