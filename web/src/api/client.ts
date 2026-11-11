// The one module that talks to the daemon (docs/api.md, docs/gate.md). Every path the page uses is
// here, typed by internal/api's generated types.
import { QueryClient } from "@tanstack/react-query";
import { blockOf, type SatoriMessage } from "../satori";
import type {
  DshWorkspace,
  AgentInfo,
  AllowRule,
  ApplyUpdate,
  CardAnswer,
  CardReply,
  CompleteUpload,
  NeedAnswer,
  ShareReply,
  ShareRequest,
  ShareTargets,
  NeedReply,
  Needs,
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
  NewNote,
  Note,
  NoteFolder,
  NoteFolderPatch,
  NotePatch,
  Notes,
  NewSession,
  NewSessionReply,
  NewUpload,
  Release,
  PushEvents,
  PushRule,
  PushState,
  PushSubscription,
  PushSwitch,
  SendReply,
  Session,
  SessionPatch,
  SessionState,
  SetDevice,
  SetMode,
  SetModel,
  SetPreset,
  Presets,
  SetFeature,
  SetUltracode,
  Settings,
  SettingsPatch,
  AppAgents,
  Apps,
  CatalogueEntry,
  PairWindow,
  Perms,
  UpdateState,
  Upload,
  Workspace,
  FolderPlace,
  GroupOrder,
  WorkspacePatch,
} from "./types.gen";
import type { ShellOutput, ShellRun, ShellStarted } from "./types.gen";
import type { SecretInfo, Secrets, SetSecret, Usage } from "./types.gen";

/** The gate's GET /_ir/api/session (docs/gate.md). */
export type GateSession = { authenticated: boolean; name?: string; local?: boolean; setup?: boolean };
/** What PUT /v1/internal/me/profile and /v1/internal/apps/{id}/profile answer: the app's or login's name and avatar. */
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

/** Who the page speaks as in Satori (docs/satori.md): ir_web and one of its logins, or the host's
 *  page as local/local. Set once /me answers; until then Iridium's own routes go without them. */
const who = { platform: "", user: "" };
export function speakAs(platform: string, user: string) {
  who.platform = platform;
  who.user = user;
}
export const speaker = () => ({ ...who });

/** fetch, saying the request is the page's own (and, to /v1, which login speaks). Tests replace
 *  globalThis.fetch. */
export async function request(url: string, init: RequestInit = {}): Promise<Response> {
  const headers = new Headers(init.headers);
  headers.set("x-requested-with", "XMLHttpRequest");
  if (url.startsWith("/v1/") && who.platform) {
    headers.set("satori-platform", who.platform);
    headers.set("satori-user-id", who.user);
  }
  const r = await fetch(url, { ...init, headers });
  if (r.status === 401) login();
  return r;
}

async function fail(r: Response): Promise<never> {
  let body: unknown;
  let msg = `${r.status}`;
  try {
    body = await r.json();
    const e = body as { message?: unknown; code?: unknown } | null;
    if (e && typeof e === "object") msg = String(e.message || e.code || msg);
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

/** Text as a Satori message's content: its markup characters escaped (protocol/elements.md). */
export const escapeSatori = (t: string) => t.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;");

let whoAsked: Promise<void> | undefined;
/** Satori's standard methods need the login: from /me, once. */
function whoKnown(): Promise<void> {
  if (who.platform) return Promise.resolve();
  return (whoAsked ??= get<Me>("/v1/internal/me").then((m) => speakAs(m.app, m.login || m.app)));
}
const S = (id: string) => `/v1/internal/sessions/${enc(id)}`;

export const api = {
  // --- served (docs/api.md) ---
  sessions: () => get<Session[]>("/v1/internal/sessions"),
  session: (id: string, init?: RequestInit) => get<SessionState>(S(id), init),
  /** A page of session id's transcript: the newest, the one before line before, or what came after
   *  line after. Satori's message.list (docs/satori.md "记录"), a message per block; _data says
   *  where the page is. */
  messages: async (id: string, q: { before?: number; after?: number } = {}, init?: RequestInit): Promise<MessagePage> => {
    await whoKnown();
    const next = q.before ?? q.after;
    const r = await request("/v1/message.list", {
      method: "POST",
      cache: "no-store",
      ...init,
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ channel_id: id, ...(next !== undefined && { next: String(next), direction: q.after !== undefined ? "after" : "before" }) }),
    });
    if (!r.ok) return fail(r);
    const p = (await r.json()) as { data: SatoriMessage[]; _data?: { start: number; end: number; more: boolean } };
    return { blocks: p.data.map(blockOf), start: p._data?.start ?? 0, end: p._data?.end ?? 0, more: p._data?.more ?? false };
  },
  block: (id: string, at: string) => get<FullText>(`${S(id)}/block?at=${enc(at)}`),
  create: (s: NewSession) => write<NewSessionReply>("POST", "/v1/internal/sessions", s),
  /** A message: queued while a turn runs (queued in the reply), or joining it with steer.
   *  attachments: upload refs. */
  send: async (id: string, text: string, attachments?: string[], steer = false): Promise<SendReply> => {
    // Satori's message.create (docs/satori.md), as every app sends; Iridium's _data says whether it
    // joins the turn and which uploads go with it, and answers what the send did.
    await whoKnown();
    const sent = await write<{ _data?: SendReply }[]>("POST", "/v1/message.create", {
      channel_id: id,
      content: escapeSatori(text),
      _data: { ...(steer ? { steer } : {}), ...(attachments?.length ? { attachments } : {}) },
    });
    return sent[0]?._data ?? { input: "" };
  },
  interrupt: (id: string) => write<{ ok: boolean }>("POST", `${S(id)}/interrupt`),
  stop: (id: string) => write<{ ok: boolean }>("POST", `${S(id)}/stop`),
  /** A command line at the session's machine, in cwd ("": its folder) (docs/api.md "命令"). */
  shell: (id: string, line: string, cwd?: string) => write<ShellStarted>("POST", `${S(id)}/shell`, { line, ...(cwd ? { cwd } : {}) } satisfies ShellRun),
  shellRead: (run: string, from: number) => get<ShellOutput>(`/v1/internal/shell/${enc(run)}?from=${from}`),
  shellStop: (run: string) => write<{ ok: boolean }>("DELETE", `/v1/internal/shell/${enc(run)}`),
  /** One of session id's asks answered by its card (docs/api.md "卡片"): an option, a choice per question, own words. */
  answer: (id: string, a: CardAnswer) => write<CardReply>("POST", `${S(id)}/answer`, a),
  /** A step Claude's auto mode denied, run again (session/release.go). */
  release: (id: string, toolUseId: string, dismiss?: boolean) => write<{ ok: boolean }>("POST", `/v1/internal/sessions/${enc(id)}/release`, { toolUseId, dismiss } satisfies Release),
  /** Satori's meta (advanced/meta.md): the page asks it only for its status, a 401 taking it to the login. */
  meta: () => request("/v1/meta", { method: "POST" }),
  agents: () => get<AgentInfo[]>("/v1/internal/agents"),

  // --- workspaces, folders, the session overlay ---
  /** The folder groups: Satori's guild.list, each guild's own in its _data (Loose has none). */
  workspaces: async (): Promise<Workspace[]> => {
    await whoKnown();
    const out: Workspace[] = [];
    let next: string | undefined;
    do {
      const r = await request("/v1/guild.list", { method: "POST", cache: "no-store", headers: { "content-type": "application/json" }, body: JSON.stringify(next ? { next } : {}) });
      if (!r.ok) return fail(r);
      const l = (await r.json()) as { data: { _data?: Workspace }[]; next?: string };
      for (const g of l.data) if (g._data) out.push(g._data);
      next = l.next;
    } while (next);
    return out;
  },
  patchWorkspace: (id: number, p: WorkspacePatch) => writeAgain<{ ok: boolean }>("PATCH", `/v1/internal/workspaces/${id}`, p),
  /** The groups in the order they were dragged to: IDs first to last (reorder.ts). */
  /** Where a folder is put among those in its group, dragged or pinned (reorder.ts). */
  placeFolder: (p: FolderPlace) => writeAgain<{ ok: boolean }>("PATCH", "/v1/internal/folders/place", p),
  /** The group taken apart at its folder: its subfolders groups of their own. */
  splitWorkspace: (id: number) => writeAgain<{ ok: boolean }>("POST", `/v1/internal/workspaces/${id}/split`),
  orderGroups: (keys: string[]) => writeAgain<{ ok: boolean }>("PUT", "/v1/internal/groups/order", { keys } satisfies GroupOrder),
  /** machine: another machine's executor; its paths '/'-separated. */
  folders: (path: string, machine?: string) => get<Folders>(`/v1/internal/folders?path=${enc(path)}${machine ? `&machine=${enc(machine)}` : ""}`),
  mkdir: (f: NewFolder) => write<FolderReply>("POST", "/v1/internal/folders", f),
  /** dsh's workspaces at another machine (DSH Desktop's), else its sessions' folders: '/'-separated. */
  dshWorkspaces: (machine: string) => get<DshWorkspace[]>(`/v1/internal/machines/${enc(machine)}/dsh/workspaces`),
  dshSync: (machine: string) => write<{ ok: boolean }>("POST", `/v1/internal/machines/${enc(machine)}/dsh/sync`),
  patch: (id: string, p: SessionPatch) => writeAgain<{ ok: boolean }>("PATCH", S(id), p),
  /** A folder's colour tag; "" takes it off. The carried state "colors" has them all. */
  folderColor: (f: FolderColor) => writeAgain<{ ok: boolean }>("PUT", "/v1/internal/colors", f),

  // --- notes (the carried state "notes" has them all) ---
  notes: () => get<Notes>("/v1/internal/notes"),
  /** id: the page's own, so one sent again is the same note. */
  newNote: (n: NewNote) => writeAgain<Note>("POST", "/v1/internal/notes", n),
  patchNote: (id: string, p: NotePatch) => writeAgain<Note>("PATCH", `/v1/internal/notes/${enc(id)}`, p),
  deleteNote: (id: string) => writeAgain<{ ok: boolean }>("DELETE", `/v1/internal/notes/${enc(id)}`),
  newNoteFolder: (p: NoteFolderPatch) => write<NoteFolder>("POST", "/v1/internal/note-folders", p),
  patchNoteFolder: (id: number, p: NoteFolderPatch) => writeAgain<{ ok: boolean }>("PATCH", `/v1/internal/note-folders/${id}`, p),
  deleteNoteFolder: (id: number) => writeAgain<{ ok: boolean }>("DELETE", `/v1/internal/note-folders/${id}`),

  // --- files in messages, uploads ---
  // v: the file's version (its mtime): a file written again at its path is another address, not
  // the picture the browser has of it (the user, 2026-11-09: a shot redone showed the old one).
  fileURL: (id: string, ref: string, download = false, v?: number) => {
    const q = [download ? "download" : "", v ? `v=${v}` : ""].filter(Boolean).join("&");
    return `${S(id)}/files/${enc(ref)}${q ? `?${q}` : ""}`;
  },
  thumbURL: (id: string, ref: string, size = 1024, v?: number) => `${S(id)}/files/${enc(ref)}/thumb?size=${size}${v ? `&v=${v}` : ""}`,
  /** Picture n of the tool result at (Result.images), as the model was given it. */
  imageURL: (id: string, at: string, n: number) => `${S(id)}/image?at=${enc(at)}&n=${n}`,
  upload: (id: string, u: NewUpload) => write<Upload>("POST", `${S(id)}/uploads`, u),
  uploadAt: (up: string) => get<Upload>(`/v1/internal/uploads/${enc(up)}`),
  uploadChunk: (up: string, offset: number, chunk: Blob, signal?: AbortSignal) =>
    write<Upload>("PUT", `/v1/internal/uploads/${enc(up)}?offset=${offset}`, chunk, { headers: { "content-type": "application/octet-stream" }, signal }),
  uploadDone: (up: string, sha256: string) => write<Upload>("POST", `/v1/internal/uploads/${enc(up)}/complete`, { sha256 } satisfies CompleteUpload),
  uploadCancel: (up: string) => write<{ ok: boolean }>("DELETE", `/v1/internal/uploads/${enc(up)}`),

  // --- the queue, the session's settings ---
  steer: (id: string, input: string) => write<{ ok: boolean }>("POST", `${S(id)}/queue/${enc(input)}/steer`),
  editQueued: (id: string, input: string, text: string) => write<{ ok: boolean }>("PUT", `${S(id)}/queue/${enc(input)}`, { text } satisfies EditQueued),
  unqueue: (id: string, input: string) => write<{ ok: boolean }>("DELETE", `${S(id)}/queue/${enc(input)}`),
  continueQueue: (id: string) => write<{ ok: boolean }>("POST", `${S(id)}/continue`),
  mode: (id: string, mode: string) => writeAgain<{ ok: boolean }>("PUT", `${S(id)}/mode`, { mode } satisfies SetMode),
  model: (id: string, model: string) => writeAgain<{ ok: boolean }>("PUT", `${S(id)}/model`, { model } satisfies SetModel),
  effort: (id: string, effort: string) => writeAgain<{ ok: boolean }>("PUT", `${S(id)}/effort`, { effort }),
  preset: (id: string, preset: string) => writeAgain<{ ok: boolean }>("PUT", `${S(id)}/preset`, { preset } satisfies SetPreset),
  presets: (id: string) => get<Presets>(`${S(id)}/presets`),
  setFeature: (id: string, feature: string, f: SetFeature) => writeAgain<{ ok: boolean }>("PUT", `${S(id)}/features/${enc(feature)}`, f),
  ultracode: (id: string, on: boolean) => writeAgain<{ ok: boolean }>("PUT", `${S(id)}/ultracode`, { ultracode: on } satisfies SetUltracode),
  rules: (id: string) => get<AllowRule[]>(`${S(id)}/rules`),
  clearRules: (id: string) => write<{ ok: boolean }>("DELETE", `${S(id)}/rules`),

  // --- push ---
  push: () => get<PushState>("/v1/internal/web/push"),
  pushSwitch: (enabled: boolean) => writeAgain<{ ok: boolean }>("PUT", "/v1/internal/web/push", { enabled } satisfies PushSwitch),
  pushDefaults: (e: PushEvents) => writeAgain<PushState>("PUT", "/v1/internal/web/push/defaults", e),
  pushRule: (r: PushRule) => writeAgain<PushState>("PUT", "/v1/internal/web/push/rules", r),
  pushSubscribe: (sub: PushSubscription) => write<{ ok: boolean }>("POST", "/v1/internal/web/push/subscriptions", sub),
  pushUnsubscribe: (endpoint: string) => write<{ ok: boolean }>("DELETE", "/v1/internal/web/push/subscriptions", { endpoint }),

  // --- settings, update ---
  settings: () => get<Settings>("/v1/internal/settings"),
  putSettings: (p: SettingsPatch) => writeAgain<Settings>("PUT", "/v1/internal/settings", p),
  update: () => get<UpdateState>("/v1/internal/update"),
  checkUpdate: () => write<UpdateState>("POST", "/v1/internal/update/check"),
  applyUpdate: (force = false) => write<UpdateState>("POST", "/v1/internal/update", force ? ({ force } satisfies ApplyUpdate) : undefined),

  // --- this terminal (GET /v1/internal/me), the gate's session, profile and logout ---
  me: () => get<Me>("/v1/internal/me"),
  gateSession: () => get<GateSession>("/_ir/api/session"),
  /** PUT /v1/internal/me/profile, or another app's or login's (internal/gate): multipart name, avatar, removeAvatar. */
  profile: (p: { name?: string; avatar?: Blob; removeAvatar?: boolean }, of?: string) => {
    const f = new FormData();
    if (p.name !== undefined) f.set("name", p.name);
    if (p.avatar) f.set("avatar", p.avatar, "avatar.png");
    if (p.removeAvatar) f.set("removeAvatar", "1");
    return write<Profile>("PUT", of ? `/v1/internal/apps/${enc(of)}/profile` : "/v1/internal/me/profile", f);
  },
  /** PUT /v1/internal/me/device, or another app's or login's: the device it runs on. */
  device: (device: SetDevice["device"], of?: string) => write<SetDevice>("PUT", of ? `/v1/internal/apps/${enc(of)}/device` : "/v1/internal/me/device", { device } satisfies SetDevice),
  logout: () => write<{ ok: boolean }>("POST", "/_ir/api/logout"),

  // --- the apps and their logins (docs/api.md "应用"), the confirmations agents ask for ---
  apps: () => get<Apps>("/v1/internal/apps"),
  invite: (name: string, claude?: boolean) => write<Invite>("POST", "/v1/internal/invites", { ...(name ? { name } : {}), ...(claude ? { claude } : {}) } satisfies NewInvite),
  revoke: (id: string) => write<{ ok: boolean }>("DELETE", `/v1/internal/apps/${enc(id)}`),
  /** DELETE /v1/internal/apps/{app}/logins/{login}: another app's login asks again as it next speaks. */
  removeLogin: (app: string, login: string) => write<{ ok: boolean }>("DELETE", `/v1/internal/apps/${enc(app)}/logins/${enc(login)}`),
  /** PUT /v1/internal/apps/{id}/perms: what an app may use (agents, scope). */
  appPerms: (id: string, p: Perms) => write<Perms>("PUT", `/v1/internal/apps/${enc(id)}/perms`, p),
  /** PUT /v1/internal/apps/{id}/agents: the agents an app carries turned off there. */
  appAgents: (id: string, off: string[]) => write<AppAgents>("PUT", `/v1/internal/apps/${enc(id)}/agents`, { off } satisfies AppAgents),
  /** PUT /v1/internal/apps/{app}/logins/{login}/perms: what a login may use, within its app's. */
  loginPerms: (app: string, login: string, p: Perms) => write<Perms>("PUT", `/v1/internal/apps/${enc(app)}/logins/${enc(login)}/perms`, p),
  /** PUT /v1/internal/pairing: the pairing window opened for minutes (10 by default) or closed. */
  pairing: (open: boolean, minutes?: number) => write<PairWindow>("PUT", "/v1/internal/pairing", { open, ...(minutes ? { minutes } : {}) } satisfies PairWindow),
  /** GET /v1/internal/catalogue: the official apps and the commands that install them. */
  catalogue: () => get<CatalogueEntry[]>("/v1/internal/catalogue"),

  /** GET /v1/internal/usage: the last days' tokens by day, app, agent and model; terminal: one app only; by hour: the last 24 hours'; by weekhour: by weekday and hour. */
  usage: (days: number, terminal?: string, by?: "hour" | "weekhour") => get<Usage>(`/v1/internal/usage?days=${days}${terminal ? `&app=${enc(terminal)}` : ""}${by ? `&by=${by}` : ""}`),
  confirms: () => get<Needs>("/v1/internal/needs"),
  /** extra: a secret-set's value, a secret-use's "this session". */
  /** A share put away by the login it was for. */
  dropNeed: (id: string) => write<{ ok: boolean }>("DELETE", `/v1/internal/needs/${enc(id)}`),
  /** Something shared with a login (docs/api.md "分享"); targets: who may use it. */
  share: (r: ShareRequest) => write<ShareReply>("POST", "/v1/internal/share", r),
  shareTargets: (r: Omit<ShareRequest, "to" | "note">) => get<ShareTargets>(`/v1/internal/share/targets?${new URLSearchParams(Object.entries(r).filter(([, v]) => v) as [string, string][])}`),
  answerConfirm: (id: string, allow: boolean, extra?: Omit<NeedAnswer, "allow">) => write<NeedReply>("POST", `/v1/internal/needs/${enc(id)}`, { allow, ...extra } satisfies NeedAnswer),

  // --- the credentials (docs/api.md "凭据"): never a value back ---
  secrets: () => get<Secrets>("/v1/internal/secrets"),
  putSecret: (name: string, s: SetSecret) => write<SecretInfo>("PUT", `/v1/internal/secrets/${enc(name)}`, s),
  deleteSecret: (name: string) => write<{ ok: boolean }>("DELETE", `/v1/internal/secrets/${enc(name)}`),
};

/** Whether an error is the route not being there (an older daemon, a part turned off): the page
 *  then shows what it can without it. */
export const missing = (e: unknown) => e instanceof ApiError && (e.status === 404 || e.status === 405 || e.status === 501);
/** The thing asked about is not there (422 with a no_such_ code; 404 from an Iridium before 0.2). */
export const notThere = (e: unknown) => e instanceof ApiError && (e.status === 422 || e.status === 404);
