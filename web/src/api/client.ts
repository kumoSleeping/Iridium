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
  CompleteUpload,
  NeedAnswer,
  NeedReply,
  PublicLink,
  Needs,
  FolderReply,
  Folders,
  Invite,
  Me,
  MessagePage,
  NewFolder,
  NewInvite,
  NewSession,
  NewUpload,
  Release,
  PushEvents,
  PushRule,
  PushState,
  Role,
  RolePatch,
  PushSubscription,
  PushSwitch,
  SendReply,
  SessionPatch,
  SessionState,
  SetDevice,
  SetMode,
  SetModel,
  SetPreset,
  Presets,
  SetUltracode,
  Settings,
  SettingsPatch,
  AppAgents,
  Apps,
  PairWindow,
  Perms,
  UpdateState,
  Upload,
  Workspace,
  FolderPatch,
  GuildPatch,
} from "./types.gen";
import type { ShellOutput, ShellRun, ShellStarted } from "./types.gen";
import type { SecretInfo, Secrets, SetSecret, Usage } from "./types.gen";
import type { AgentChoices, Block, ChannelMembers, ChannelReads, Injection, MemberPatch, NewMember } from "./types.gen";
import type { MemberCard, Memory, Profile as Card, ProfilePatch, Self } from "./types.gen";

/** A message of a channel's log (Satori's message.list): id its number there. */
export type ChannelMessage = { id: string; content: string; created_at?: number; user?: { id: string; name?: string; nick?: string; is_bot?: boolean }; _data?: { kind?: string; event?: unknown; session?: string; block?: string; from?: number; to?: number; input?: string } };
/** A page of a channel's log: start and end, the numbers of its first and last; more, earlier ones. */
export type ChannelPage = { data: ChannelMessage[]; start: number; end: number; more: boolean };

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

/** How long a send waits for its answer: past the daemon's own minute for one (an agent started at
 *  another machine), so only a link gone dead runs it out. */
const SEND_WAIT = 65_000;
/** A request that ran out of time (AbortSignal.timeout). */
export const noAnswer = (e: unknown) => e instanceof DOMException && e.name === "TimeoutError";

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

/** A Satori method (docs/satori.md), as the page's login: POST /v1/<method>. */
async function sat<T>(method: string, body: unknown = {}, again = false): Promise<T> {
  await whoKnown();
  return again ? writeAgain<T>("POST", `/v1/${method}`, body) : write<T>("POST", `/v1/${method}`, body);
}

/** A folder's channel (docs/satori.md): folder:<its path as grouped>. */
export const folderChannel = (path: string) => `folder:${path}`;

/** p as channel.update takes it for session id: the name and the place out from under its launcher
 *  in data, the rest Iridium's own in _data. */
function channelPatch(id: string, p: SessionPatch) {
  const { name, detach, byAgent: _, ...rest } = p;
  const data = { ...(name !== undefined ? { name } : {}), ...(detach ? { parent_id: null } : {}) };
  return { channel_id: id, ...(Object.keys(data).length ? { data } : {}), ...(Object.keys(rest).length ? { _data: rest } : {}) };
}

/** A session's first screen (GET …/first, docs/api.md): its state, its record's newest page and
 *  its channel's members; with another agent in the channel, how far each has read, the log as
 *  far back as the record's page, the others' states, and the work under their words (where in
 *  whose record, the blocks there). A part the server could not give is absent. */
export type First = { session: SessionState; page?: MessagePage; members?: ChannelMembers; reads?: ChannelReads; log?: ChannelPage; sessions?: Record<string, SessionState>; work?: { session: string; block?: string; from?: number; to?: number; blocks: Block[] }[] };
type RawPage<T> = { data: T[]; _data?: { start: number; end: number; more: boolean } };

export const api = {
  // --- served (docs/api.md) ---
  session: (id: string, init?: RequestInit) => get<SessionState>(S(id), init),
  /** have: the page holds the record already (its page is left out); since: how far back the
   *  others' words are to go then (the record's first block's time; 0: all of them). */
  first: async (id: string, q: { have?: boolean; since?: number } = {}, init?: RequestInit): Promise<First> => {
    const args = q.have ? `?record=0${q.since !== undefined ? `&since=${q.since}` : ""}` : "";
    const f = await get<{ session: SessionState; page?: RawPage<SatoriMessage>; members?: ChannelMembers; reads?: ChannelReads; log?: RawPage<ChannelMessage>; sessions?: Record<string, SessionState>; work?: { session: string; block?: string; from?: number; to?: number; data: SatoriMessage[] }[] }>(`${S(id)}/first${args}`, init);
    const at = (p: RawPage<unknown>) => ({ start: p._data?.start ?? 0, end: p._data?.end ?? 0, more: p._data?.more ?? false });
    return {
      session: f.session,
      ...(f.page ? { page: { blocks: f.page.data.map(blockOf), ...at(f.page) } } : {}),
      ...(f.members ? { members: f.members } : {}),
      ...(f.reads ? { reads: f.reads } : {}),
      ...(f.log ? { log: { data: f.log.data, ...at(f.log) } } : {}),
      ...(f.sessions ? { sessions: f.sessions } : {}),
      ...(f.work ? { work: f.work.map(({ data, ...w }) => ({ ...w, blocks: data.map(blockOf) })) } : {}),
    };
  },
  /** A page of session id's transcript: the newest, the one before line before, or what came after
   *  line after. internal/block.list (docs/satori.md "记录"), a message per block; _data says
   *  where the page is. */
  messages: async (id: string, q: { before?: number; after?: number } = {}, init?: RequestInit): Promise<MessagePage> => {
    await whoKnown();
    const next = q.before ?? q.after;
    const r = await request("/v1/internal/block.list", {
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
  /** A page of channel id's log, what was said there (Satori's message.list): the newest, the one
   *  before number before, or what came after number after. */
  channelMessages: async (id: string, q: { before?: number; after?: number; limit?: number } = {}): Promise<ChannelPage> => {
    await whoKnown();
    const next = q.before ?? q.after;
    const r = await request("/v1/message.list", {
      method: "POST",
      cache: "no-store",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ channel_id: id, ...(q.limit ? { limit: q.limit } : {}), ...(next !== undefined && { next: String(next), direction: q.after !== undefined ? "after" : "before" }) }),
    });
    if (!r.ok) return fail(r);
    const p = (await r.json()) as { data: ChannelMessage[]; _data?: { start: number; end: number; more: boolean } };
    return { data: p.data, start: p._data?.start ?? 0, end: p._data?.end ?? 0, more: p._data?.more ?? false };
  },
  // --- a channel's members (docs/channels.md) ---
  members: (id: string) => get<ChannelMembers>(`/v1/internal/channels/${enc(id)}/members`),
  addMember: (id: string, m: NewMember) => write<ChannelMembers>("POST", `/v1/internal/channels/${enc(id)}/members`, m, { signal: AbortSignal.timeout(SEND_WAIT) }),
  patchMember: (id: string, member: string, p: MemberPatch) => write<ChannelMembers>("PATCH", `/v1/internal/channels/${enc(id)}/members/${enc(member)}`, p),
  removeMember: (id: string, member: string) => write<ChannelMembers>("DELETE", `/v1/internal/channels/${enc(id)}/members/${enc(member)}`),
  // --- roles (docs/channels.md "角色"): cards kept to start sessions as ---
  roles: () => get<Role[]>("/v1/internal/roles"),
  patchRole: (id: string, p: RolePatch) => write<Role>("PATCH", `/v1/internal/roles/${enc(id)}`, p),
  deleteRole: (id: string) => write<{ ok: boolean }>("DELETE", `/v1/internal/roles/${enc(id)}`),
  // --- cards (docs/next/group.md "资料卡"): a member's, a channel's, the one looking's own ---
  /** who: an agent's "agent:<session>", a person's login or app id. */
  memberCard: (who: string) => get<MemberCard>(`/v1/internal/members/${enc(who)}`),
  /** An agent's card changed; channel: where it is said. */
  patchMemberCard: (who: string, p: ProfilePatch, channel?: string) => write<Card>("PATCH", `/v1/internal/members/${enc(who)}${channel ? `?channel=${enc(channel)}` : ""}`, p),
  channelProfile: (id: string) => get<Card>(`/v1/internal/channels/${enc(id)}/profile`),
  patchChannel: (id: string, p: ProfilePatch) => write<Card>("PATCH", `/v1/internal/channels/${enc(id)}`, p),
  self: () => get<Self>("/v1/internal/self"),
  /** The one looking's own card: its intro, its background (name and avatar: profile below). */
  patchSelf: (p: Pick<ProfilePatch, "intro" | "background">) => write<Card>("PATCH", "/v1/internal/self", p),
  /** What a session's agent chose to remember; forget: one of them gone. */
  memory: (session: string) => get<Memory[]>(`/v1/internal/memory?session=${enc(session)}`),
  forget: (session: string, id: number) => write<unknown>("DELETE", `/v1/internal/memory/${id}?session=${enc(session)}`),
  /** 存为角色: the session's card and what it runs kept as a role, the session its from then on
   *  (更新角色: patchRole {session}); 移出角色: the session is no role's. */
  roleOf: (session: string) => write<Role>("POST", "/v1/internal/roles", { session }),
  roleLeave: (session: string) => write<unknown>("DELETE", `${S(session)}/role`),
  /** How far each member has read the channel; read: this login has read it up to message seq. */
  reads: (id: string) => get<ChannelReads>(`/v1/internal/channels/${enc(id)}/reads`),
  read: (id: string, seq: number) => write<ChannelReads>("POST", `/v1/internal/channels/${enc(id)}/read`, { seq }),
  /** A channel's people set ("*" everyone, "<app>/*", "<app>/<login>"); the caller stays one. */
  setPeople: (id: string, people: string[]) => write<ChannelMembers>("PUT", `/v1/internal/channels/${enc(id)}/people`, { people }),
  /** The channel of the caller and the login whose Satori user id user is ("local": the host's):
   *  the same two, the same one (docs/channels.md "私聊"). */
  dm: (user: string) => write<{ channel: string }>("POST", "/v1/internal/dm", { user }),
  /** What the channel gave session id's agent, the newest first. */
  injections: (id: string) => get<Injection[]>(`${S(id)}/injections`),
  /** What a new agent of a kind may start with, where a channel runs or at a machine. */
  agentChoices: (agent: string, at: { channel?: string; machine?: string }) =>
    get<AgentChoices>(`/v1/internal/agents/${encodeURIComponent(agent)}/choices?${new URLSearchParams(Object.entries(at).filter(([, v]) => v) as [string, string][])}`),
  /** A block's whole text: internal/block.get, untruncated. */
  block: async (id: string, at: string): Promise<{ text: string }> => ({ text: blockOf(await sat<SatoriMessage>("internal/block.get", { channel_id: id, message_id: at })).text ?? "" }),
  /** A session started: Satori's channel.create, its group guild_id, its launcher data.parent_id. */
  create: async (s: NewSession): Promise<{ id: string }> => {
    const { workspace, caller, ...start } = s;
    const ch = await sat<{ id: string }>("channel.create", { ...(workspace ? { guild_id: String(workspace) } : {}), ...(caller ? { data: { parent_id: caller } } : {}), _data: start });
    return { id: ch.id };
  },
  /** A message: queued while a turn runs (queued in the reply), or joining it with steer.
   *  attachments: upload refs. input: its id, chosen here (a UUID): the transcript's block has it
   *  as it comes, before the answer; lost on the way, the send goes again under it, never twice. */
  send: async (id: string, text: string, attachments?: string[], steer = false, input?: string, quote?: number): Promise<SendReply> => {
    // Satori's message.create (docs/satori.md), as every app sends; Iridium's _data says whether it
    // joins the turn and which uploads go with it, and answers what the send did.
    await whoKnown();
    const body = {
      channel_id: id,
      content: (quote ? `<quote id="${quote}"/>` : "") + escapeSatori(text),
      _data: { ...(steer ? { steer } : {}), ...(attachments?.length ? { attachments } : {}), ...(input ? { id: input } : {}) },
    };
    for (let tries = 1; ; tries++) {
      try {
        const sent = await write<{ _data?: SendReply }[]>("POST", "/v1/message.create", body, { signal: AbortSignal.timeout(SEND_WAIT) });
        return sent[0]?._data ?? { input: input ?? "" };
      } catch (e) {
        const lost = e instanceof TypeError || noAnswer(e) || (e instanceof ApiError && (e.status === 502 || e.status === 503));
        if (!input || !lost || tries === 3) throw e;
        await new Promise((r) => setTimeout(r, 1000));
      }
    }
  },
  interrupt: (id: string) => write<{ ok: boolean }>("POST", `${S(id)}/interrupt`),
  stop: (id: string) => write<{ ok: boolean }>("POST", `${S(id)}/stop`),
  /** The session's public read-only link (docs/api.md "公开链接"): its state; made anew (its URL only
   *  in this answer); revoked. */
  publicLink: (id: string) => get<PublicLink>(`${S(id)}/public`),
  makePublicLink: (id: string) => write<PublicLink>("POST", `${S(id)}/public`),
  revokePublicLink: (id: string) => write<{ ok: boolean }>("DELETE", `${S(id)}/public`),
  /** A command line at the session's machine, in cwd ("": its folder) (docs/api.md "命令"). */
  shell: (id: string, line: string, cwd?: string) => write<ShellStarted>("POST", `${S(id)}/shell`, { line, ...(cwd ? { cwd } : {}) } satisfies ShellRun),
  shellRead: (run: string, from: number) => get<ShellOutput>(`/v1/internal/shell/${enc(run)}?from=${from}`),
  shellStop: (run: string) => write<{ ok: boolean }>("DELETE", `/v1/internal/shell/${enc(run)}`),
  /** One of session id's asks answered by its card (docs/api.md "卡片"): an option, a choice per question, own words. */
  answer: (_id: string, a: CardAnswer) => sat<object>("internal/button", { id: a.card, option: a.option, choices: a.choices, words: a.words, message: a.message }),
  /** A button attached to a session's page pressed (its id "att:<session>:<from>:<id>"). */
  button: (id: string) => sat<object>("internal/button", { id }),
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
  /** A folder group renamed, moved among the groups (position, 0 first), or taken apart at its
   *  folder (split: its subfolders groups of their own). */
  patchGuild: (id: number, p: GuildPatch) => writeAgain<{ ok: boolean }>("PATCH", `/v1/internal/guilds/${id}`, p),
  /** A folder marked: its colour tag ("" off), pinned, where it was dragged (reorder.ts): channel.update of its channel. */
  markFolder: (path: string, p: FolderPatch) => sat<object>("channel.update", { channel_id: folderChannel(path), _data: p }, true),
  /** machine: another machine's executor; its paths '/'-separated. */
  folders: (path: string, machine?: string) => get<Folders>(`/v1/internal/folders?path=${enc(path)}${machine ? `&machine=${enc(machine)}` : ""}`),
  mkdir: (f: NewFolder) => write<FolderReply>("POST", "/v1/internal/folders", f),
  /** dsh's workspaces at another machine (DSH Desktop's), else its sessions' folders: '/'-separated. */
  dshWorkspaces: (machine: string) => get<DshWorkspace[]>(`/v1/internal/machines/${enc(machine)}/dsh/workspaces`),
  dshSync: (machine: string) => write<{ ok: boolean }>("POST", `/v1/internal/machines/${enc(machine)}/dsh/sync`),
  /** A session renamed, archived, pinned, tagged, put, moved or taken out from under its launcher: Satori's channel.update. */
  patch: (id: string, p: SessionPatch) => sat<object>("channel.update", channelPatch(id, p), true),

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
  /** A message waiting in the queue changed or taken back: Satori's message.update, message.delete. */
  editQueued: (id: string, input: string, text: string) => sat<object>("message.update", { channel_id: id, message_id: input, _data: { text } }),
  unqueue: (id: string, input: string) => sat<object>("message.delete", { channel_id: id, message_id: input }),
  continueQueue: (id: string) => write<{ ok: boolean }>("POST", `${S(id)}/continue`),
  mode: (id: string, mode: string) => writeAgain<{ ok: boolean }>("PUT", `${S(id)}/mode`, { mode } satisfies SetMode),
  model: (id: string, model: string) => writeAgain<{ ok: boolean }>("PUT", `${S(id)}/model`, { model } satisfies SetModel),
  effort: (id: string, effort: string) => writeAgain<{ ok: boolean }>("PUT", `${S(id)}/effort`, { effort }),
  preset: (id: string, preset: string) => writeAgain<{ ok: boolean }>("PUT", `${S(id)}/preset`, { preset } satisfies SetPreset),
  presets: (id: string) => get<Presets>(`${S(id)}/presets`),
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
  /** PUT /v1/internal/pairing: taking pairing requests, on or off till turned again. */
  pairing: (open: boolean) => write<PairWindow>("PUT", "/v1/internal/pairing", { open } satisfies PairWindow),

  /** GET /v1/internal/usage: the last days' tokens by day, app, agent and model; terminal: one app only; by hour: the last 24 hours'; by weekhour: by weekday and hour; machine: only the sessions at that computer (an executor's app ID). */
  usage: (days: number, terminal?: string, by?: "hour" | "weekhour", machine?: string) => get<Usage>(`/v1/internal/usage?days=${days}${terminal ? `&app=${enc(terminal)}` : ""}${by ? `&by=${by}` : ""}${machine ? `&machine=${enc(machine)}` : ""}`),
  confirms: () => get<Needs>("/v1/internal/needs"),
  /** extra: a secret-set's value, a secret-use's "this session". */
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
