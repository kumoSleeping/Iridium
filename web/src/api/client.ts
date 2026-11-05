// The one module that talks to the daemon (docs/api.md). Every path the page uses is here; the ones
// marked "assumed" are not served yet and follow docs/plan.md (their types are in ./assumed.ts).
import { QueryClient } from "@tanstack/react-query";
import type { FolderList, Endpoint, FileInfo, Me, SessionPatch, Settings, Upload, UploadDone, UpdateState, Workspace } from "./assumed";
import type { Answer, FullText, MessagePage, NewSession, NewSessionReply, Session, SendReply, SessionState } from "./types.gen";

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
  /** files: upload refs (assumed: Send.files); sent only when there are some, as the server takes no unknown field. */
  send: (id: string, text: string, files?: string[]) => write<SendReply>("POST", `${S(id)}/send`, files?.length ? { text, files } : { text }),
  interrupt: (id: string) => write<{ ok: boolean }>("POST", `${S(id)}/interrupt`),
  stop: (id: string) => write<{ ok: boolean }>("POST", `${S(id)}/stop`),
  /** always: allow this tool for the rest of the session (assumed: Answer.always). */
  answer: (id: string, a: Answer & { always?: boolean }) => write<{ ok: boolean }>("POST", `${S(id)}/answer`, a.always ? a : stripAlways(a)),
  health: (init?: RequestInit) => get<{ ok: boolean }>("/api/health", init),

  // --- assumed: workspaces, folders, the session overlay ---
  workspaces: () => get<Workspace[]>("/api/workspaces"),
  createWorkspace: (root: string, name: string) => write<Workspace>("POST", "/api/workspaces", { root, name }),
  patchWorkspace: (id: number, p: { name?: string }) => writeAgain<Workspace>("PATCH", `/api/workspaces/${id}`, p),
  folders: (path: string) => get<FolderList>(`/api/folders?path=${enc(path)}`),
  mkdir: (path: string) => write<{ path: string }>("POST", "/api/folders", { path }),
  patch: (id: string, p: SessionPatch) => writeAgain<Session>("PATCH", S(id), p),

  // --- assumed: queue, mode, model ---
  queue: (id: string, text: string, files?: string[]) => write<{ id: string }>("POST", `${S(id)}/queue`, files?.length ? { text, files } : { text }),
  steer: (id: string, input: string) => write<{ ok: boolean }>("POST", `${S(id)}/queue/${enc(input)}/steer`),
  unqueue: (id: string, input: string) => write<{ ok: boolean }>("DELETE", `${S(id)}/queue/${enc(input)}`),
  pauseQueue: (id: string, paused: boolean) => writeAgain<{ ok: boolean }>("POST", `${S(id)}/queue/pause`, { paused }),
  mode: (id: string, mode: string) => writeAgain<{ ok: boolean }>("POST", `${S(id)}/mode`, { mode }),
  model: (id: string, p: { model?: string; effort?: string }) => writeAgain<{ ok: boolean }>("POST", `${S(id)}/model`, p),

  // --- assumed: files in replies, uploads ---
  files: (id: string, paths: string[]) => write<FileInfo[]>("POST", `${S(id)}/files`, { paths }),
  fileURL: (id: string, path: string, download = false) => `${S(id)}/file?path=${enc(path)}${download ? "&download=1" : ""}`,
  thumbURL: (id: string, path: string) => `${S(id)}/file?path=${enc(path)}&thumb=1024`,
  upload: (session: string, name: string, size: number, type: string) => write<Upload>("POST", "/api/uploads", { session, name, size, type }),
  uploadAt: (up: string) => get<Upload>(`/api/uploads/${enc(up)}`),
  uploadChunk: (up: string, offset: number, chunk: Blob, signal?: AbortSignal) =>
    write<Upload>("PUT", `/api/uploads/${enc(up)}?offset=${offset}`, chunk, { headers: { "content-type": "application/octet-stream" }, signal }),
  uploadDone: (up: string, sha256: string) => write<UploadDone>("POST", `/api/uploads/${enc(up)}/done`, { sha256 }),

  // --- assumed: settings, update, push ---
  settings: () => get<Settings>("/api/settings"),
  putSettings: (s: Partial<Settings>) => writeAgain<Settings>("PUT", "/api/settings", s),
  update: () => get<UpdateState>("/api/update"),
  applyUpdate: () => write<{ ok: boolean }>("POST", "/api/update"),
  pushKey: () => get<{ key: string }>("/api/push/key"),
  pushSubscribe: (sub: PushSubscriptionJSON) => write<{ ok: boolean }>("POST", "/api/push/subscribe", sub),

  // --- this terminal: /api/me is assumed; the gate's own routes are served once it is mounted ---
  me: () => get<Me>("/api/me"),
  gateSession: () => get<{ authenticated: boolean; name?: string; local?: boolean; setup?: boolean }>("/_ir/api/session"),
  /** PUT /api/me/profile (internal/gate): multipart name, avatar, removeAvatar. */
  profile: (p: { name?: string; avatar?: Blob; removeAvatar?: boolean }) => {
    const f = new FormData();
    if (p.name !== undefined) f.set("name", p.name);
    if (p.avatar) f.set("avatar", p.avatar, "avatar.png");
    if (p.removeAvatar) f.set("removeAvatar", "1");
    return write<Me>("PUT", "/api/me/profile", f);
  },
  logout: () => write<{ ok: boolean }>("POST", "/_ir/api/logout"),
  endpoints: () => get<Endpoint[]>("/api/endpoints"),
};

function stripAlways(a: Answer & { always?: boolean }): Answer {
  const { always: _, ...rest } = a;
  return rest;
}

/** Whether an error is the route not being served yet: the page then shows what it can without it. */
export const missing = (e: unknown) => e instanceof ApiError && (e.status === 404 || e.status === 405 || e.status === 501);
