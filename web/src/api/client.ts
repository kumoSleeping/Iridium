// The one module that talks to the daemon (docs/api.md, docs/gate.md). Every path the page uses is
// here, typed by internal/api's generated types.
import { QueryClient } from "@tanstack/react-query";
import type {
  AgentInfo,
  AllowRule,
  ApplyUpdate,
  CardAnswer,
  CardReply,
  CompleteUpload,
  ConfirmAnswer,
  ConfirmReply,
  Confirms,
  EditQueued,
  FolderColor,
  FolderReply,
  Folders,
  FullText,
  Invite,
  Me,
  MessagePage,
  NewFolder,
  NewInvite,
  NewSession,
  NewSessionReply,
  NewUpload,
  NewWorkspace,
  PushEvents,
  PushRule,
  PushState,
  PushSubscription,
  PushSwitch,
  Send,
  SendReply,
  Session,
  SessionPatch,
  SessionState,
  SetDevice,
  SetMode,
  SetModel,
  SetRules,
  SetUltracode,
  Settings,
  SettingsPatch,
  Terminal,
  Terminals,
  Token,
  UpdateState,
  Upload,
  Workspace,
  WorkspacePatch,
} from "./types.gen";
import type { SecretInfo, Secrets, SetSecret } from "./types.gen";

/** The gate's GET /_ir/api/session (docs/gate.md). */
export type GateSession = { authenticated: boolean; name?: string; local?: boolean; setup?: boolean };
/** What PUT /api/me/profile and /api/terminals/{id}/profile answer: the terminal's name and avatar. */
export type Profile = { id: string; name: string; kind: string; avatar?: string };

/** The answers shown. A failed one is not retried: the next state or the clock asks again. */
export const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: 5_000, refetchOnWindowFocus: false } } });

/** How many writes are out, as a send with files going up a slow link: what loading the page again
 *  would cut off (src/upgrade.ts). */
export const posting = { count: 0 };

/** An answer other than 2xx: its status and the server's {error}. */
export class ApiError extends Error {
  constructor(
    readonly status: number,
    message: string,
    readonly body?: unknown,
  ) {
    super(message);
  }
}

let toLogin = false;
/** Behind the Passkey gate a session that ran out answers 401: go to its login, back here after. */
function login() {
  if (toLogin) return;
  toLogin = true;
  location.assign(`/_ir/login?next=${encodeURIComponent(location.pathname + location.search + location.hash)}`);
}

/** fetch, saying the request is the page's own. Tests replace globalThis.fetch. */
export async function request(url: string, init: RequestInit = {}): Promise<Response> {
  const headers = new Headers(init.headers);
  headers.set("x-requested-with", "XMLHttpRequest");
  const r = await fetch(url, { ...init, headers });
  if (r.status === 401) login();
  return r;
}

async function fail(r: Response): Promise<never> {
  let body: unknown;
  let msg = `${r.status}`;
  try {
    body = await r.json();
    if (body && typeof body === "object" && "error" in body) msg = String((body as { error: unknown }).error);
  } catch {}
  throw new ApiError(r.status, msg, body);
}

/** GETs a path; an answer other than 2xx throws an ApiError. init: as for one fetched ahead, its low
 *  priority or its signal. */
export async function get<T>(url: string, init?: RequestInit): Promise<T> {
  const r = await request(url, { cache: "no-store", ...init });
  if (!r.ok) return fail(r);
  return (await r.json()) as T;
}

/** Sends a write. Its JSON body, if any; the answer's JSON. A network error throws a TypeError, a
 *  refusal an ApiError. */
export async function write<T>(method: string, url: string, body?: unknown, init: RequestInit = {}): Promise<T> {
  posting.count++;
  try {
    const json = body !== undefined && !(body instanceof FormData) && !(body instanceof Blob);
    const r = await request(url, {
      method,
      ...init,
      headers: { ...(json ? { "content-type": "application/json" } : {}), ...(init.headers as Record<string, string>) },
      body: body === undefined ? undefined : json ? JSON.stringify(body) : (body as BodyInit),
    });
    if (!r.ok) return fail(r);
    const text = await r.text();
    return (text ? JSON.parse(text) : {}) as T;
  } finally {
    posting.count--;
  }
}

/** A write that may go again as it was: on a network error or a 502/503 (the daemon restarting),
 *  up to three times a second apart. Only for writes that do the same twice (pin, mode, settings). */
export async function writeAgain<T>(method: string, url: string, body?: unknown): Promise<T> {
  for (let tries = 1; ; tries++) {
    try {
      return await write<T>(method, url, body);
    } catch (e) {
      const again = e instanceof TypeError || (e instanceof ApiError && (e.status === 502 || e.status === 503));
      if (!again || tries === 3) throw e;
      await new Promise((r) => setTimeout(r, 1000));
    }
  }
}

const enc = encodeURIComponent;
const S = (id: string) => `/api/sessions/${enc(id)}`;

export const api = {
  // --- served (docs/api.md) ---
  sessions: () => get<Session[]>("/api/sessions"),
  session: (id: string, init?: RequestInit) => get<SessionState>(S(id), init),
  messages: (id: string, q: { before?: number; after?: number } = {}, init?: RequestInit) =>
    get<MessagePage>(`${S(id)}/messages${q.before !== undefined ? `?before=${q.before}` : q.after !== undefined ? `?after=${q.after}` : ""}`, init),
  block: (id: string, at: string) => get<FullText>(`${S(id)}/block?at=${enc(at)}`),
  create: (s: NewSession) => write<NewSessionReply>("POST", "/api/sessions", s),
  /** A message: queued while a turn runs (queued in the reply), or joining it with steer.
   *  attachments: upload refs. */
  send: (id: string, text: string, attachments?: string[], steer = false) =>
    write<SendReply>("POST", `${S(id)}/send`, { text, ...(attachments?.length ? { attachments } : {}), ...(steer ? { steer } : {}) } satisfies Send),
  interrupt: (id: string) => write<{ ok: boolean }>("POST", `${S(id)}/interrupt`),
  stop: (id: string) => write<{ ok: boolean }>("POST", `${S(id)}/stop`),
  /** An ask's card answered (docs/api.md "卡片"): an option, a choice per question, own words. */
  answer: (a: CardAnswer) => write<CardReply>("POST", "/api/answer", a),
  health: (init?: RequestInit) => get<{ ok: boolean }>("/api/health", init),
  agents: () => get<AgentInfo[]>("/api/agents"),

  // --- workspaces, folders, the session overlay ---
  workspaces: () => get<Workspace[]>("/api/workspaces"),
  createWorkspace: (w: NewWorkspace) => write<Workspace>("POST", "/api/workspaces", w),
  patchWorkspace: (id: number, p: WorkspacePatch) => writeAgain<{ ok: boolean }>("PATCH", `/api/workspaces/${id}`, p),
  deleteWorkspace: (id: number) => write<{ ok: boolean }>("DELETE", `/api/workspaces/${id}`),
  folders: (path: string) => get<Folders>(`/api/folders?path=${enc(path)}`),
  mkdir: (f: NewFolder) => write<FolderReply>("POST", "/api/folders", f),
  patch: (id: string, p: SessionPatch) => writeAgain<{ ok: boolean }>("PATCH", S(id), p),
  /** A folder's colour tag; "" takes it off. The carried state "colors" has them all. */
  folderColor: (f: FolderColor) => writeAgain<{ ok: boolean }>("PUT", "/api/colors", f),

  // --- files in messages, uploads ---
  fileURL: (id: string, ref: string, download = false) => `${S(id)}/files/${enc(ref)}${download ? "?download" : ""}`,
  thumbURL: (id: string, ref: string, size = 1024) => `${S(id)}/files/${enc(ref)}/thumb?size=${size}`,
  upload: (id: string, u: NewUpload) => write<Upload>("POST", `${S(id)}/uploads`, u),
  uploadAt: (up: string) => get<Upload>(`/api/uploads/${enc(up)}`),
  uploadChunk: (up: string, offset: number, chunk: Blob, signal?: AbortSignal) =>
    write<Upload>("PUT", `/api/uploads/${enc(up)}?offset=${offset}`, chunk, { headers: { "content-type": "application/octet-stream" }, signal }),
  uploadDone: (up: string, sha256: string) => write<Upload>("POST", `/api/uploads/${enc(up)}/complete`, { sha256 } satisfies CompleteUpload),
  uploadCancel: (up: string) => write<{ ok: boolean }>("DELETE", `/api/uploads/${enc(up)}`),

  // --- the queue, the session's settings ---
  steer: (id: string, input: string) => write<{ ok: boolean }>("POST", `${S(id)}/queue/${enc(input)}/steer`),
  editQueued: (id: string, input: string, text: string) => write<{ ok: boolean }>("PUT", `${S(id)}/queue/${enc(input)}`, { text } satisfies EditQueued),
  unqueue: (id: string, input: string) => write<{ ok: boolean }>("DELETE", `${S(id)}/queue/${enc(input)}`),
  continueQueue: (id: string) => write<{ ok: boolean }>("POST", `${S(id)}/continue`),
  mode: (id: string, mode: string) => writeAgain<{ ok: boolean }>("PUT", `${S(id)}/mode`, { mode } satisfies SetMode),
  model: (id: string, model: string) => writeAgain<{ ok: boolean }>("PUT", `${S(id)}/model`, { model } satisfies SetModel),
  effort: (id: string, effort: string) => writeAgain<{ ok: boolean }>("PUT", `${S(id)}/effort`, { effort }),
  ultracode: (id: string, on: boolean) => writeAgain<{ ok: boolean }>("PUT", `${S(id)}/ultracode`, { ultracode: on } satisfies SetUltracode),
  rules: (id: string) => get<AllowRule[]>(`${S(id)}/rules`),
  clearRules: (id: string) => write<{ ok: boolean }>("DELETE", `${S(id)}/rules`),

  // --- push ---
  push: () => get<PushState>("/api/push"),
  pushSwitch: (enabled: boolean) => writeAgain<{ ok: boolean }>("PUT", "/api/push", { enabled } satisfies PushSwitch),
  pushDefaults: (e: PushEvents) => writeAgain<PushState>("PUT", "/api/push/defaults", e),
  pushRule: (r: PushRule) => writeAgain<PushState>("PUT", "/api/push/rules", r),
  pushSubscribe: (sub: PushSubscription) => write<{ ok: boolean }>("POST", "/api/push/subscriptions", sub),
  pushUnsubscribe: (endpoint: string) => write<{ ok: boolean }>("DELETE", "/api/push/subscriptions", { endpoint }),

  // --- settings, update ---
  settings: () => get<Settings>("/api/settings"),
  putSettings: (p: SettingsPatch) => writeAgain<Settings>("PUT", "/api/settings", p),
  update: () => get<UpdateState>("/api/update"),
  checkUpdate: () => write<UpdateState>("POST", "/api/update/check"),
  applyUpdate: (force = false) => write<UpdateState>("POST", "/api/update", force ? ({ force } satisfies ApplyUpdate) : undefined),

  // --- this terminal (GET /api/me), the gate's session, profile and logout ---
  me: () => get<Me>("/api/me"),
  gateSession: () => get<GateSession>("/_ir/api/session"),
  /** PUT /api/me/profile, or another terminal's (internal/gate): multipart name, avatar, removeAvatar. */
  profile: (p: { name?: string; avatar?: Blob; removeAvatar?: boolean }, of?: string) => {
    const f = new FormData();
    if (p.name !== undefined) f.set("name", p.name);
    if (p.avatar) f.set("avatar", p.avatar, "avatar.png");
    if (p.removeAvatar) f.set("removeAvatar", "1");
    return write<Profile>("PUT", of ? `/api/terminals/${enc(of)}/profile` : "/api/me/profile", f);
  },
  /** PUT /api/me/device, or another terminal's: the device it runs on. */
  device: (device: SetDevice["device"], of?: string) => write<Terminal>("PUT", of ? `/api/terminals/${enc(of)}/device` : "/api/me/device", { device } satisfies SetDevice),
  logout: () => write<{ ok: boolean }>("POST", "/_ir/api/logout"),

  // --- the interactive terminals (docs/api.md "交互终端"), the confirmations agents ask for ---
  terminals: () => get<Terminals>("/api/terminals"),
  setRules: (id: string, r: SetRules) => write<Terminal>("PUT", `/api/terminals/${enc(id)}/rules`, r),
  invite: (name: string, owner?: boolean) => write<Invite>("POST", "/api/terminals/invites", { ...(name ? { name } : {}), ...(owner ? { owner } : {}) } satisfies NewInvite),
  token: (name: string, owner?: boolean) => write<Token>("POST", "/api/terminals/tokens", { name, ...(owner ? { owner } : {}) } satisfies NewInvite),
  revoke: (id: string) => write<{ ok: boolean }>("DELETE", `/api/terminals/${enc(id)}`),
  confirms: () => get<Confirms>("/api/confirms"),
  /** extra: a secret-set's value, a secret-use's "this session". */
  answerConfirm: (id: string, allow: boolean, extra?: Omit<ConfirmAnswer, "allow">) => write<ConfirmReply>("POST", `/api/confirms/${enc(id)}`, { allow, ...extra } satisfies ConfirmAnswer),

  // --- the credentials (docs/api.md "凭据"): never a value back ---
  secrets: () => get<Secrets>("/api/secrets"),
  putSecret: (name: string, s: SetSecret) => write<SecretInfo>("PUT", `/api/secrets/${enc(name)}`, s),
  deleteSecret: (name: string) => write<{ ok: boolean }>("DELETE", `/api/secrets/${enc(name)}`),
};

/** Whether an error is the route not being there (an older daemon, a part turned off): the page
 *  then shows what it can without it. */
export const missing = (e: unknown) => e instanceof ApiError && (e.status === 404 || e.status === 405 || e.status === 501);
