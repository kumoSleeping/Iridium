// Types of the routes and fields the page uses that internal/api does not have yet (docs/plan.md
// describes them; other work adds them). Each is written against the plan's shape and used only
// through src/api/client.ts; once the Go side has it, its generated type replaces the one here.
import type { Agent, AgentQuota, SessionState } from "./types.gen";

/** GET /api/workspaces: the workspaces, in the sidebar's order. Session.workspace points at one. */
export type Workspace = { id: number; name: string; root: string };

/** GET /api/folders?path=: the folders (no files, no hidden ones) in path, which the server resolves
 *  (~ and symlinks); home: the user's home, for the picker's ~. */
export type FolderList = { path: string; home: string; folders: string[] };

/** PATCH /api/sessions/{id}: what the sidebar and the chat's menu change. */
export type SessionPatch = { name?: string; pinned?: boolean; archived?: boolean; workspace?: number };

/** A message waiting to go (the queue above the composer): held, why the server holds it, when it does. */
export type QueuedInput = { id: string; text: string; files?: number; held?: string };

/** An approval mode of the agent's own, as it names it. */
export type Mode = { id: string; name?: string };
export type Model = { value: string; name: string; description?: string; efforts?: string[] };
export type Command = { name: string; description?: string; argumentHint?: string };

/** The fields of the carried "session:<id>" the page reads beyond internal/api's SessionState. */
export type SessionExtra = {
  queue?: QueuedInput[];
  queuePaused?: boolean;
  modes?: Mode[];
  models?: Model[];
  effort?: string;
  commands?: Command[];
};
export type LiveSession = SessionState & SessionExtra;

/** The carried "quota", per agent and the account it is logged in with. */
export type AccountQuota = AgentQuota & { account?: string; plan?: string };

/** Web Push per workspace (plan "推送"): what is pushed. Off by default as a whole. */
export type PushRules = { needsYou: boolean; error: boolean; turnEnd: boolean; compact: boolean };

/** GET/PUT /api/settings: the config file's fields the settings page edits. PUT takes a part. */
export type Settings = {
  idleReap: string; // "1h", "0" for never
  crashResume: boolean;
  quotaResume: boolean;
  defaultMode?: Partial<Record<Agent, string>>;
  push: { enabled: boolean; workspaces: Record<string, PushRules> };
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

/** POST /api/sessions/{id}/files {paths}: the paths a reply names that exist and may be fetched. */
export type FileInfo = { path: string; name: string; size: number; type: string; image?: boolean };

/** An upload (plan "文件传输"): POST /api/uploads, PUT ?offset= chunks, GET for what came, POST done. */
export type Upload = { id: string; size: number; received: number };
export type UploadDone = { ref: string; path: string; name: string; size: number };
