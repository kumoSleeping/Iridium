// Creating things (plan "网页" 新建): the sidebar's "+" (a new session: a folder, then an agent), a
// group row's "+" (a new session: pick an agent; the folder defaults to the group's) and the folder-only
// picker: a breadcrumb you can tap or type into, folders only, an inline new folder, one key.
import { Menu } from "@base-ui/react/menu";
import { Popover } from "@base-ui/react/popover";
import { Tooltip } from "@base-ui/react/tooltip";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Bot, Check, ChevronDown, ChevronLeft, ChevronRight, Folder, FolderPlus, LoaderCircle, Play, UserMinus } from "lucide-react";
import { lazy, Suspense, useEffect, useLayoutEffect, useRef, useState, type CSSProperties, type ReactElement, type ReactNode } from "react";
import type { NewMember, NewSession as NewSessionReq, Trigger, Workspace } from "./api/types.gen";
import { api, missing } from "./api/client";
import { openChat } from "./route";
import type { Agent, Role } from "./api/types.gen";
import { t } from "./i18n";
import { agentsQ, isComputer, useApps, useHome, useMachines } from "./data";
import { choicesQ, draftDefault, DraftNamed } from "./chat/choices";
import { appAll, hasApp, hasLogin, pressApp, pressLogin } from "./chat/channel/setup";
import { agentsAt, atMachine, homeAt, MachineBadge, MachineFace, pageAt, type Machine } from "./machines";
import { onCreateAt } from "./places";
import { effortName, picked, prefetchRoles, RoleFace, RoleLine, RoleModels, roleStart, useRoles, type RolePick } from "./roles";
import { validNick } from "./chat/channel/lib";
import { ProfileCard } from "./chat/channel/profilecard";
import { errText, toast } from "./toast";
import { AGENTS, agentName, Avatar, cn, homeOf, IconKey, Logo, own, PHONE, PopSurface, tilde, Tip, useLongPress, useMedia, useMenuAt, usePointAnchor } from "./ui";

const join = (dir: string, name: string) => (dir.endsWith("/") ? dir + name : `${dir}/${name}`);
/** One folder's listing; prefetched before it is shown (the popover opening, a row hovered), so the
 *  picker opens and steps at its full height instead of a spinner springing open into the list. */
export const foldersQ = (path: string, machine?: string) => ({ queryKey: ["folders", machine ?? "", path], queryFn: () => api.folders(path, machine), retry: false, staleTime: 10_000 });

/** Its children's height, glided to as it changes (a folder of more or fewer folders): the popover
 *  grows and shrinks instead of jumping (the user, 2026-11-06). */
function Tall({ children, className }: { children: ReactNode; className?: string }) {
  const inner = useRef<HTMLDivElement>(null);
  const [h, setH] = useState<number | undefined>(undefined);
  useLayoutEffect(() => {
    const el = inner.current;
    if (!el) return;
    const ro = new ResizeObserver(() => setH(el.offsetHeight));
    ro.observe(el);
    setH(el.offsetHeight);
    return () => ro.disconnect();
  }, []);
  return (
    <div className={cn("overflow-hidden transition-[height] duration-(--dur-3) ease-(--ease-out) motion-reduce:transition-none", className)} style={{ height: h }}>
      <div ref={inner}>{children}</div>
    </div>
  );
}

/** The folder picker. initial: where it opens; onChoose: the folder chosen, absolute. machine:
 *  another machine's folders (docs/exec.md), its paths '/'-separated. */
export function FolderPicker({ initial, onChoose, startNew = false, busy, machine }: { initial: string; onChoose: (path: string) => void; startNew?: boolean; busy?: boolean; machine?: Machine }) {
  const qc = useQueryClient();
  const [path, setPath] = useState(initial);
  const [dir, setDir] = useState<"fwd" | "back">("fwd");
  const [typing, setTyping] = useState<string | null>(null);
  const [newName, setNewName] = useState<string | null>(startNew ? "" : null);
  const mid = machine?.id;
  const q = useQuery(foldersQ(path, mid));
  // dsh's workspaces at the machine (DSH Desktop's), above the folders where the picker opens, each
  // with its name, path and how many sessions it holds: a press takes one, on to that machine's
  // agents (docs/exec.md "远端 DSH", "远端工作区").
  const dsh = !!mid && !!machine?.online && machine.agents.some((a) => a.agent === "dsh");
  const ws = useQuery({ queryKey: ["dshWorkspaces", mid ?? ""], queryFn: () => api.dshWorkspaces(mid!), enabled: dsh, retry: false, staleTime: 30_000 });
  const workspaces = dsh && path === initial ? (ws.data ?? []) : [];
  const nameBox = useRef<HTMLInputElement>(null);
  const root = useRef<HTMLDivElement>(null);
  const naming = newName !== null;
  useEffect(() => {
    if (naming) nameBox.current?.focus();
  }, [naming]);
  const here = q.data?.path ?? path;
  const localHome = useHome().data;
  const home = machine ? homeAt(machine) : (localHome ?? homeOf(here));
  const gone = q.error && missing(q.error); // the route is not served yet: only typing a path works
  const shown = tilde(here, home);
  const segs = shown.split("/").filter((s, i) => s || i === 0);
  // The folder shown, at once as it is gone to (a press may come before the page draws it again).
  const target = useRef(here);
  target.current = here;
  const go = (p: string, d: "fwd" | "back") => {
    target.current = p;
    setDir(d);
    setPath(p);
    setNewName(null);
  };
  const segPath = (i: number) => {
    let s = segs.slice(0, i + 1).join("/") || "/";
    if (/^[A-Za-z]:$/.test(s)) s += "/"; // a drive's root (another machine's, on Windows)
    return s.startsWith("~") && home ? home + s.slice(1) : s;
  };
  // What the row holds now, read by its blur too: Enter or Escape has closed it by then.
  const live = useRef<string | null>(newName);
  live.current = newName;
  const closeNew = () => {
    live.current = null;
    setNewName(null);
    // Focus back on the picker, not out of it (that would close the popover), nor left in the
    // row now shut.
    if (document.activeElement === nameBox.current) root.current?.focus();
  };
  // A folder being made: 选择 pressed meanwhile (on a phone, the press is what ends the typing)
  // waits for it and takes it, not the folder it is made in (the user, 2026-11-08).
  const making = useRef<Promise<string | null> | null>(null);
  const commitNew = () => {
    if (live.current === null) return;
    const n = live.current.trim();
    closeNew();
    if (!n || /[/\0]/.test(n)) return;
    const at = here;
    const made = api.mkdir({ path: at, name: n, ...(mid ? { machine: mid } : {}) }).then(
      () => {
        void qc.invalidateQueries({ queryKey: ["folders", mid ?? "", path] });
        // Into it, made for choosing: 选择 takes it (the user, 2026-11-06).
        go(join(at, n), "fwd");
        return join(at, n);
      },
      (e) => (toast(t("err.mkdir"), { error: true, description: errText(e) }), null),
    );
    making.current = made;
    void made.finally(() => making.current === made && (making.current = null));
  };
  // A path typed: where it leads, ~ for the home folder.
  const typedPath = (v: string) => {
    const p = v.trim().replace(/(.)\/+$/, "$1") || "~";
    return p.startsWith("~") && home ? home + p.slice(1) : p;
  };
  const typed = useRef<string | null>(null);
  typed.current = typing;
  const enterTyped = () => {
    if (typed.current === null) return;
    const p = typedPath(typed.current);
    typed.current = null;
    setTyping(null);
    if (p !== target.current) go(p, "fwd");
  };
  // 选择: a path being typed, else a folder being made, else the folder shown.
  const choose = async () => {
    let p = typed.current !== null ? typedPath(typed.current) : target.current;
    if (typed.current === null && making.current) p = (await making.current) ?? target.current;
    onChoose(p);
  };
  return (
    <div ref={root} tabIndex={-1} className="flex w-[min(352px,calc(100vw-16px))] flex-col outline-none">
      <div className="flex h-11 items-center gap-1 pr-1.5 pl-2">
        {typing !== null ? (
          <input
            autoFocus
            aria-label={t("pick.typePath")}
            value={typing}
            onChange={(e) => setTyping(e.target.value)}
            // Leaving it goes there, as Enter does: on a phone, 选择 pressed is what ends it.
            onBlur={enterTyped}
            onKeyDown={(e) => {
              if (e.nativeEvent.isComposing || e.keyCode === 229) return;
              if (e.key === "Enter") enterTyped();
              if (e.key === "Escape") {
                e.stopPropagation();
                typed.current = null;
                setTyping(null);
              }
            }}
            className="g-fade no-ring h-8 min-w-0 flex-1 rounded-md bg-fill px-2 font-mono text-sm"
          />
        ) : (
          <nav aria-label={t("pick.path")} className="g-fade flex min-w-0 flex-1 items-center overflow-x-auto [scrollbar-width:none]" onClick={(e) => e.target === e.currentTarget && setTyping(shown)}>
            {segs.map((s, i) => {
              const last = i === segs.length - 1;
              return (
                <span key={i} className="flex shrink-0 items-center">
                  {i > 0 && <ChevronRight size={12} className="mx-0.5 text-ink-4" />}
                  <button
                    type="button"
                    // The folder you are in: a press types the path (no pencil key: the user, 2026-11-08).
                    onClick={() => (last ? setTyping(shown) : go(segPath(i), "back"))}
                    className={cn("g-press rounded-sm px-1.5 py-1 text-sm whitespace-nowrap", last ? "font-semibold text-ink" : "text-ink-2 hover:bg-fill hover:text-ink")}
                    aria-current={last ? "location" : undefined}
                  >
                    {s === "~" ? <span aria-label={t("pick.home")}>~</span> : s || "/"}
                  </button>
                </span>
              );
            })}
          </nav>
        )}
        <IconKey label={t("pick.newFolder")} onClick={() => setNewName("")} disabled={!!gone} className="size-7 pointer-coarse:size-9">
          <FolderPlus size={15} />
        </IconKey>
      </div>
      {/* The new folder's row: opening and closing in place, above the list (the user, 2026-11-06). */}
      <div aria-hidden={newName === null || undefined} className="g-reveal px-1.5" data-open={newName !== null ? "" : undefined}>
        <div className="min-h-0 overflow-hidden">
          <div className="mb-px flex h-9 items-center gap-2.5 rounded-md bg-fill px-2.5">
            <FolderPlus size={16} className="shrink-0 text-ink-3" />
            <input
              ref={nameBox}
              tabIndex={newName === null ? -1 : undefined}
              aria-label={t("pick.folderName")}
              placeholder={t("pick.folderName")}
              value={newName ?? ""}
              onChange={(e) => setNewName(e.target.value)}
              onBlur={commitNew}
              onKeyDown={(e) => {
                if (e.nativeEvent.isComposing || e.keyCode === 229) return;
                if (e.key === "Enter") commitNew();
                if (e.key === "Escape") {
                  e.stopPropagation();
                  closeNew();
                }
              }}
              className="no-ring h-full min-w-0 flex-1 bg-transparent text-base placeholder:text-ink-3"
            />
          </div>
        </div>
      </div>
      <Tall>
        <div key={here} data-dir={dir === "back" ? "back" : undefined} className="g-slide max-h-[min(264px,45vh)] min-h-9 overflow-y-auto px-1.5 pb-1">
          {workspaces.length > 0 && (
            <div role="group" aria-label={t("pick.dshWorkspaces")} className="mb-1 border-b border-fill-2 pb-1">
              <p className="m-0 px-2.5 pt-1.5 pb-1 text-xs text-ink-3">{t("pick.dshWorkspaces")}</p>
              {workspaces.map((w) => (
                <button key={w.path} type="button" title={w.path} disabled={busy} onClick={() => onChoose(w.path)} className="flex h-9 w-full items-center gap-2.5 rounded-md px-2.5 text-left text-base hover:bg-fill disabled:opacity-50 pointer-coarse:h-11">
                  <Logo agent="dsh" size={16} className="shrink-0" />
                  <span className="max-w-[55%] shrink-0 truncate">{w.title}</span>
                  <span className="min-w-0 flex-1 truncate text-right font-mono text-xs text-ink-3">{tilde(w.path, home)}</span>
                  {/* How many of its sessions are listed (the server's count): none is said too. */}
                  <span aria-hidden className="min-w-4 shrink-0 text-right text-xs text-ink-3 tabular-nums">
                    {w.sessions}
                  </span>
                  <span className="sr-only">{t("pick.sessions", { n: w.sessions })}</span>
                </button>
              ))}
            </div>
          )}
          {q.isPending && <LoaderCircle size={15} className="mx-auto my-2.5 animate-spin text-ink-4" />}
          {q.data?.folders.map((f) => (
            <button key={f} type="button" onPointerEnter={() => void qc.prefetchQuery(foldersQ(join(here, f), mid))} onFocus={() => void qc.prefetchQuery(foldersQ(join(here, f), mid))} onClick={() => go(join(here, f), "fwd")} className="flex h-9 w-full items-center gap-2.5 rounded-md px-2.5 text-left text-base hover:bg-fill pointer-coarse:h-11">
              <Folder size={16} className="shrink-0 text-ink-3" />
              <span className="truncate">{f}</span>
            </button>
          ))}
          {q.error && !gone && <p className="m-0 px-2.5 py-2 text-sm text-ink-3">{errText(q.error)}</p>}
        </div>
      </Tall>
      <div className="flex justify-end px-2.5 pt-1.5 pb-2.5">
        <button type="button" disabled={busy || !!(q.error && !gone)} onClick={() => void choose()} className="g-press flex h-8 items-center gap-1.5 rounded-md bg-ink px-3.5 text-sm font-medium text-on-ink disabled:opacity-40 pointer-coarse:h-10">
          {busy && <LoaderCircle size={14} className="animate-spin" />}
          {t("pick.choose")}
        </button>
      </div>
    </div>
  );
}

/** Where a new session runs: another machine and the folder there. */
export type Target = { m: Machine; cwd: string };

/** A subfolder's name as typed: trimmed, or null when it is none (empty, or a path, not a name). */
export const subName = (v: string) => {
  const n = v.trim();
  return n && n !== "." && n !== ".." && !/[/\\\0]/.test(n) ? n : null;
};

/** A new session's lineup (kumo, 2026-11-16: never started by one press; several agents, roles
 *  more than once): the folder line (pressed: the picker; its key beside it: a subfolder, made
 *  at its machine too, the user, 2026-11-10); the Bots and people (PeopleRow); the lineup, a row
 *  each, added by pressing an agent (the folder's machine's only: docs/exec.md "远端工作区") or a
 *  role, the channel with no agent first among the agents; 开始. Each row: its face, its 称呼
 *  (typed; none: the server's, numbered), its model line (pressed: the model and effort menu),
 *  what it receives (点名 or 全部; the first agent, the session's own, 全部 till changed, the rest
 *  点名); its menu (right button, a finger held) to take it out. One row: a session as before;
 *  more: the first its own agent, the rest invited with it (NewSession members); no agent: a
 *  channel, every row invited. The last role added shows its card, as others will see it. */
export function NewSession({ ws, folder, onFolder, onPick, busy, onMake }: { ws: string; folder: string; onFolder: () => void; onPick: (a: Agent, to?: Target, people?: string[], as?: As) => void; busy: string | null; onMake?: (name: string) => Promise<boolean> }) {
  // Its people (the user, 2026-11-13): the panel's logins and the host's unless changed here.
  const [people, setPeople] = useState<string[] | null>(null);
  // The agents the daemon can host (GET /v1/internal/agents); all four before it says. One not installed
  // is shown faded: picking it says so, nothing is added.
  const info = useQuery({ ...agentsQ, refetchOnMount: "always" }).data; // installed since: the next open knows
  const agents = info?.map((a) => a.agent) ?? AGENTS;
  const missing = (a: Agent) => !!info?.find((x) => x.agent === a)?.missing;
  const machines = useMachines();
  const whose = agentsAt(folder, machines);
  const there = typeof whose === "object" ? whose : undefined;
  const roles = useRoles().filter((r) => (there ? there.m.agents.some((a) => a.agent === r.agent) : agents.includes(r.agent)));
  const at = there ? { machine: there.m.id } : NO_PLACE;
  // The lineup; bare: the channel with no agent of its own is in it (first), every row invited.
  const [seats, setSeats] = useState<Seat[]>([]);
  const [bare, setBare] = useState<"on" | "going" | "off">("off");
  const next = useRef(0);
  const [last, setLast] = useState<number | null>(null);
  const shown = seats.filter((x) => !x.going);
  const requiresBare = !!shown[0]?.m;
  const bareOn = bare === "on" || requiresBare;
  // The other computers (kumo, 2026-11-18): their agents are offered under the folder's own. One
  // pressed joins the lineup at its computer's home, invited: it works there, away from the
  // channel's folder. Pressed first, it would be the session's own agent, which runs in the
  // session's folder: the channel with no agent of its own is taken with it instead (kumo,
  // 2026-11-18: not a message asking for another first), and it receives all, the only one there.
  const others = useMachines(!!there).filter((m) => m.id !== there?.m.id && m.agents.length > 0);
  const add = (agent: Agent, role?: Role, m?: Machine) => {
    const where = m ?? there?.m;
    if (where && !where.online) return void toast(t("new.offline", { name: where.name }), { error: true });
    if (!where && missing(agent)) return void toast(t("new.notInstalled", { name: agentName(agent) }), { error: true });
    // The session's own agent receives all, as a session's always has; one invited, what names it.
    const own = bare === "off" && !shown.length;
    if (m && own) setBare("on");
    const key = next.current++;
    setSeats((ss) => [...ss, { key, agent, role, pick: {}, nick: "", recv: own ? ["*"] : [], ...(m ? { m } : {}) }]);
    setLast(role ? key : null);
  };
  const change = (key: number, p: Partial<Seat>) => setSeats((ss) => ss.map((x) => (x.key === key ? { ...x, ...p } : x)));
  // Taken out: folded away first, then gone.
  const remove = (key: number) => {
    // Match the receive default when a cross-computer agent is picked into an empty lineup.
    const nextFirst = bare !== "on" && shown[0]?.key === key ? shown[1] : undefined;
    if (nextFirst?.m && !nextFirst.recvChosen) change(nextFirst.key, { recv: RECV.all });
    change(key, { going: true });
    if (last === key) setLast(null);
    window.setTimeout(() => setSeats((ss) => ss.filter((x) => x.key !== key)), 320);
  };
  const toggleBare = () => {
    if (requiresBare) return;
    if (bare === "on") {
      setBare("going");
      window.setTimeout(() => setBare("off"), 320);
    } else setBare("on");
  };
  const typed = (x: Seat) => x.nick.trim();
  const dup = (x: Seat) => !!typed(x) && shown.some((y) => y !== x && typed(y).toLowerCase() === typed(x).toLowerCase());
  const bad = (x: Seat) => (!!typed(x) && !validNick(typed(x))) || dup(x);
  const lastRole = shown.find((x) => x.key === last && x.role);
  // Its card beside the panel, by its row; where there is no room beside it (a phone), none.
  const [roleRow, setRoleRow] = useState<HTMLDivElement | null>(null);
  const wide = !useMedia(PHONE);
  const ready = bareOn || shown.length > 0;
  const start = () => {
    if (!ready || busy !== null) return;
    if (shown.some(bad)) return void toast(t(shown.some(dup) ? "channel.nickTaken" : "channel.nickBad"), { error: true });
    if (there && !there.m.online) return void toast(t("new.offline", { name: there.m.name }), { error: true });
    const off = shown.find((x) => x.m && !x.m.online);
    if (off) return void toast(t("new.offline", { name: off.m!.name }), { error: true });
    const to = there ? { m: there.m, cwd: there.cwd } : undefined;
    const ps = people ?? undefined;
    // Every new chat is a channel (bare: true with 0, 1, or several agents): no single-chat
    // session-bound channel, so /clear replaces the agent in place while keeping the channel.
    onPick("" as Agent, to, ps, shown.length ? { agents: shown.map(member) } : undefined);
  };
  // A subfolder being named, in the folder's line (the user, 2026-11-10). What it holds now is read
  // by its blur too: Enter or Escape has closed it by then.
  const box = useRef<HTMLDivElement>(null);
  const [naming, setNaming] = useState<string | null>(null);
  const live = useRef<string | null>(null);
  live.current = naming;
  const closeNaming = () => {
    live.current = null;
    setNaming(null);
    // Focus kept in the popover, not left in the line now shut (that would close it).
    box.current?.focus();
  };
  const commitNaming = () => {
    if (live.current === null || !onMake) return;
    const n = subName(live.current);
    closeNaming();
    // Made: on to the agents, the folder now its.
    if (n) void onMake(n).then((ok) => ok && box.current?.querySelector<HTMLButtonElement>("[role=group] button:not(:disabled)")?.focus());
  };
  const path = there ? tilde(pageAt(folder, machines)?.path ?? there.cwd, homeAt(there.m)) : tilde(folder);
  if (whose === "gone")
    return (
      <div className="flex w-[min(288px,calc(100vw-16px))] p-1.5">
        <p className="m-0 flex h-9 min-w-0 flex-1 items-center gap-2 px-2 text-sm text-ink-3">
          <Folder size={16} className="shrink-0" />
          {ws && <span className="shrink-0 font-semibold text-ink-2">{ws}</span>}
          <span className="truncate">{t("new.gone")}</span>
        </p>
      </div>
    );
  // The folder's agent grid starts with the option to create a channel without its own agent.
  const noAgent = (
    <Tip label={t("new.noAgent")}>
      <button type="button" aria-label={t("new.noAgent")} aria-pressed={bareOn} aria-disabled={requiresBare || undefined} disabled={busy !== null} onClick={toggleBare} className="g-press grid h-12 place-items-center rounded-md hover:bg-fill disabled:opacity-50 aria-disabled:opacity-50 aria-pressed:bg-fill pointer-coarse:h-14">
        <Logo agent={"" as Agent} size={22} />
      </button>
    </Tip>
  );
  return (
    <div ref={box} tabIndex={-1} className="flex w-[min(320px,calc(100vw-16px))] flex-col gap-1 p-1.5 outline-none">
      {naming !== null ? (
        <div className="g-fade flex h-9 items-center gap-2 rounded-md bg-fill px-2 pointer-coarse:h-11">
          <FolderPlus size={16} className="shrink-0 text-ink-3" />
          {/* The folder it is made in, cut at its start when long: the end is where it goes. */}
          <span dir="rtl" className="max-w-[60%] min-w-0 shrink truncate font-mono text-xs text-ink-3">
            <bdi dir="ltr">{path.endsWith("/") ? path : `${path}/`}</bdi>
          </span>
          <input
            autoFocus
            aria-label={t("pick.folderName")}
            placeholder={t("pick.folderName")}
            value={naming}
            onChange={(e) => setNaming(e.target.value)}
            onBlur={commitNaming}
            onKeyDown={(e) => {
              if (e.nativeEvent.isComposing || e.keyCode === 229) return;
              if (e.key === "Enter") commitNaming();
              if (e.key === "Escape") {
                e.stopPropagation();
                closeNaming();
              }
            }}
            className="no-ring h-full min-w-0 flex-1 bg-transparent font-mono text-xs"
          />
        </div>
      ) : (
        <div className="flex items-center gap-1">
          <button type="button" onClick={onFolder} className="flex h-9 min-w-0 flex-1 items-center gap-2 rounded-md px-2 text-left hover:bg-fill pointer-coarse:h-11" aria-label={t("new.folder")}>
            {there ? <MachineFace m={there.m} size={16} /> : <Folder size={16} className="shrink-0 text-ink-3" />}
            {ws && <span className="shrink-0 text-sm font-semibold">{ws}</span>}
            <span className="min-w-0 flex-1 truncate font-mono text-xs text-ink-3">{path}</span>
            <ChevronRight size={14} className="shrink-0 text-ink-4" />
          </button>
          {onMake && (
            <IconKey label={t("new.subFolder")} onClick={() => setNaming("")} className="size-7 text-ink-3 pointer-coarse:size-9">
              <FolderPlus size={15} />
            </IconKey>
          )}
        </div>
      )}
      <PeopleRow people={people ?? DEFAULT_PEOPLE} set={setPeople} />
      {roles.length > 0 && <RolesRow roles={roles} at={at} add={(r) => add(r.agent, r)} />}
      {!there ? (
        <div role="group" aria-label={t("new.agent")} className="g-stagger grid grid-cols-5 gap-1">
          {noAgent}
          {agents.map((a, i) => {
            // Each by its name and the model a new one will run (never "default", the user, 2026-11-16).
            return (
              <DraftNamed key={a} agent={a} at={NO_PLACE}>
                {(name) => (
                  <Tip label={missing(a) ? t("new.notInstalled", { name: agentName(a) }) : name}>
                    <button type="button" aria-label={name} disabled={busy !== null} onClick={() => add(a)} style={{ "--i": i + 1 } as CSSProperties} className="g-press grid h-12 place-items-center rounded-md hover:bg-fill disabled:opacity-50 pointer-coarse:h-14">
                      <Logo agent={a} size={24} className={cn(missing(a) && "opacity-30 grayscale")} />
                    </button>
                  </Tip>
                )}
              </DraftNamed>
            );
          })}
        </div>
      ) : (
        <MachineAgents m={there.m} noAgent={noAgent} onPick={(a) => add(a)} />
      )}
      {others.map((m) => (
        <MachineAgents key={m.id} m={m} onPick={(a) => add(a, undefined, m)} />
      ))}
      {/* The lineup: each row glides open as it is added and shut as it is taken out. */}
      <div role="list" aria-label={t("new.lineup")} className="flex flex-col">
        <Grow open={bareOn}>
          <BareRow remove={bareOn && !requiresBare ? toggleBare : undefined} />
        </Grow>
        {seats.map((x) => (
          <Grow key={x.key} open={!x.going}>
            <SeatRow rowRef={x.key === lastRole?.key ? setRoleRow : undefined} seat={x} at={x.m ? { machine: x.m.id } : at} recv={bareOn || bare === "going" || shown.length > 1 || !everyone(x.recv)} own={!bareOn && shown[0]?.key === x.key} bad={bad(x)} set={(p) => change(x.key, p)} remove={() => remove(x.key)} />
          </Grow>
        ))}
      </div>
      {/* The last role added: its card, as others will see it (kumo, 2026-11-16), beside the
          panel, not in it: in it, the panel grew and flipped above its key (kumo, 2026-11-17). */}
      <RoleBeside open={wide && !!lastRole?.role && !!roleRow} anchor={roleRow}>
        {lastRole?.role && <RolePreview role={picked(lastRole.role, lastRole.pick)} nick={typed(lastRole) && !bad(lastRole) ? typed(lastRole) : ""} at={at} />}
      </RoleBeside>
      <Fold open={ready}>
        <button type="button" disabled={busy !== null} onClick={start} className="g-press mt-1 flex h-8 w-full items-center justify-center gap-1.5 rounded-md bg-fill text-sm hover:bg-fill-2 disabled:opacity-50 pointer-coarse:h-10">
          {busy !== null ? <LoaderCircle size={14} className="animate-spin" /> : <Play size={14} className="text-ink-2" />}
          {t("new.start")}
        </button>
      </Fold>
    </div>
  );
}

/** One of the lineup: its agent, or a role (as which it starts); its model, effort, preset for
 *  this once (pick); its 称呼 ("": the server's); what it receives; going: being taken out. m:
 *  the computer it works at when that is not the folder's (its home there). */
type Seat = { key: number; agent: Agent; role?: Role; pick: RolePick; nick: string; recv: Recv; recvChosen?: boolean; going?: boolean; m?: Machine };

/** Whether a seat's receive set is 全部 (every person's words), not 点名 (only what names it):
 *  the two the lineup offers (docs/channels.md "接收"). */
/** Whose words wake a seat's agent beside @: everyone's ("*"), or none (@ only). */
type Recv = string[];
const everyone = (r: Recv) => r.includes("*");
/** A seat's senders as its subscriptions (docs/channels.md "订阅事件"). */
const onOfRecv = (r: Recv): Trigger[] => (r.length ? [{ message: { from: r } }] : []);
const RECV: Record<"all" | "at", Recv> = { all: ["*"], at: [] };

/** What a seat starts with: as its role (what pick changes of it), else its agent's model,
 *  effort, preset as picked. */
const runs = (x: Seat): { role?: string; model?: string; effort?: string; preset?: string } =>
  x.role ? roleStart(x.role, x.pick) : { ...(x.pick.model ? { model: x.pick.model } : {}), ...(x.pick.effort ? { effort: x.pick.effort } : {}), ...(x.pick.preset ? { preset: x.pick.preset } : {}) };

/** A seat invited with the session (NewSession members.agents). */
const member = (x: Seat): NewMember => ({ agent: x.agent, ...runs(x), ...(x.nick.trim() ? { nick: x.nick.trim() } : {}), on: onOfRecv(x.recv), ...(x.m ? { machine: x.m.id, cwd: x.m.home } : {}) });

/** What a start adds: its 称呼 (nick); as a role, the role and what this one session changes of
 *  it (roles.tsx roleStart); its model, effort, preset; what its own agent receives (recv); the
 *  agents invited with it (agents). */
export type As = Partial<ReturnType<typeof roleStart>> & { nick?: string; on?: Trigger[]; agents?: NewMember[] };

/** What opens and shuts in the "+" (开始): its height glides, nothing jumps. */
function Fold({ open, children }: { open: boolean; children: ReactNode }) {
  return (
    <div aria-hidden={!open || undefined} inert={!open || undefined} className="g-reveal" data-open={open ? "" : undefined}>
      <div className="min-h-0 overflow-hidden">{children}</div>
    </div>
  );
}

/** A row of the lineup coming and going: shut as it is first drawn, opened the frame after;
 *  shut again before it goes (the caller removes it once that is done). */
function Grow({ open, children }: { open: boolean; children: ReactNode }) {
  const [on, setOn] = useState(false);
  useEffect(() => {
    const id = requestAnimationFrame(() => setOn(open));
    return () => cancelAnimationFrame(id);
  }, [open]);
  return <Fold open={on && open}>{children}</Fold>;
}

/** The roles, to add to the lineup: a row; pressed, each by its face, name and line, a press
 *  adding one (again and again: a role may be in it more than once). */
function RolesRow({ roles, at, add }: { roles: Role[]; at: object; add: (r: Role) => void }) {
  return (
    <Menu.Root>
      <Menu.Trigger
        render={
          <button type="button" className="flex h-8 min-w-0 items-center gap-2 rounded-md px-2 text-left hover:bg-fill data-popup-open:bg-fill pointer-coarse:h-10">
            <span className="flex shrink-0 -space-x-1.5">
              {roles.slice(0, 3).map((r) => (
                <span key={r.id} className="grid rounded-full ring-2 ring-(--paper)">
                  <RoleFace role={r} size={16} />
                </span>
              ))}
            </span>
            <span className="min-w-0 flex-1 truncate text-sm">{t("role.title")}</span>
            <ChevronRight size={14} className="shrink-0 text-ink-4" />
          </button>
        }
      />
      <Menu.Portal>
        <Menu.Positioner side="bottom" align="start" sideOffset={4} collisionPadding={8} className="z-50">
          <Menu.Popup finalFocus={false} className="g-unfold glass thick float no-bar max-h-[min(480px,var(--available-height))] w-[min(288px,calc(100vw-16px))] overflow-y-auto rounded-lg p-1.5 text-ink outline-none">
            <div className="g-stagger flex flex-col">
              {roles.map((r, i) => (
                <Menu.Item key={r.id} onClick={() => add(r)} style={{ "--i": i } as CSSProperties} className={cn(ROW, "h-auto min-h-10 py-1 pointer-coarse:min-h-12")}>
                  <RoleFace role={r} size={24} />
                  <span className="flex min-w-0 flex-1 flex-col">
                    <span className="truncate text-sm">{r.name}</span>
                    <RoleLine role={r} at={at} logo className="text-xs text-ink-3" />
                  </span>
                </Menu.Item>
              ))}
            </div>
          </Menu.Popup>
        </Menu.Positioner>
      </Menu.Portal>
    </Menu.Root>
  );
}

/** A row's menu, at the right button or a finger held: what it receives, and 移出. */
function RowMenu({ at, close, recv, setRecv, remove }: { at: { x: number; y: number } | null; close: () => void; recv?: Recv; setRecv?: (r: Recv) => void; remove: () => void }) {
  const anchor = usePointAnchor(at);
  return (
    <Menu.Root open={at !== null} onOpenChange={(o) => !o && close()}>
      <Menu.Portal>
        <Menu.Positioner anchor={anchor} side="bottom" align="start" sideOffset={4} collisionPadding={8} className="z-50">
          <Menu.Popup finalFocus={false} className="g-unfold glass thick float min-w-44 rounded-lg p-1.5 text-ink outline-none">
            {recv !== undefined && setRecv && (
              <Menu.RadioGroup value={everyone(recv) ? "all" : "at"} onValueChange={(v) => setRecv(RECV[v as "all" | "at"])} className="flex flex-col">
                {(["at", "all"] as const).map((r) => (
                  <Menu.RadioItem key={r} value={r} closeOnClick className={ROW}>
                    <span className="min-w-0 flex-1 truncate">{t(r === "all" ? "channel.recvAll" : "channel.recvAt")}</span>
                    <Menu.RadioItemIndicator className="text-ink">
                      <Check size={14} />
                    </Menu.RadioItemIndicator>
                  </Menu.RadioItem>
                ))}
                <div role="separator" className="mx-2.5 my-1 border-t border-(--edge)" />
              </Menu.RadioGroup>
            )}
            <Menu.Item onClick={remove} className={cn(ROW, "text-err")}>
              <UserMinus size={15} />
              {t("channel.remove")}
            </Menu.Item>
          </Menu.Popup>
        </Menu.Positioner>
      </Menu.Portal>
    </Menu.Root>
  );
}

/** The menu a row opens at the right button, or a finger held on it (where it was held); not
 *  over its 称呼 being typed, which keeps the text's own menu. */
function useRowMenu() {
  const [menuAt, setMenuAt] = useMenuAt();
  const held = useRef({ x: 0, y: 0 });
  const lp = useLongPress(() => setMenuAt(held.current));
  const props = {
    ...lp,
    onPointerDown: (e: React.PointerEvent) => {
      held.current = { x: e.clientX, y: e.clientY };
      lp.onPointerDown(e);
    },
    onContextMenu: (e: React.MouseEvent) => {
      // Opened by the finger held already.
      if (lp.onContextMenu(e)) return;
      if (!own(e) || (e.target as HTMLElement).closest("input")) return;
      e.preventDefault();
      setMenuAt({ x: e.clientX, y: e.clientY });
    },
  };
  return { menuAt, setMenuAt, props };
}

/** The channel with no agent of its own, first in the lineup: its mark and name; its menu takes
 *  it out (as pressing it among the agents again). */
function BareRow({ remove }: { remove?: () => void }) {
  const m = useRowMenu();
  return (
    <div role="listitem" {...(remove && m.props)} className="flex h-10 items-center gap-2 rounded-md px-1.5 select-none pointer-coarse:h-12">
      <span className="grid size-6 shrink-0 place-items-center rounded-full bg-fill">
        <Logo agent={"" as Agent} size={14} />
      </span>
      <span className="min-w-0 flex-1 truncate text-sm">{t("new.noAgent")}</span>
      {remove && <RowMenu at={m.menuAt} close={() => m.setMenuAt(null)} remove={() => (m.setMenuAt(null), remove())} />}
    </div>
  );
}

const DraftModels = lazy(() => import("./chat/channel/pick").then((x) => ({ default: x.DraftModels })));

/** One row of the lineup: its face, its 称呼 typed (its role's name or its agent's in grey: the
 *  server's then), its model line (pressed: the model and effort menu, this once), what it
 *  receives (a key: 点名 or 全部; shown once there is more than one, or changed); its menu (right
 *  button, a finger held) to take it out. own: the session's own agent. */
function SeatRow({ rowRef, seat, at, recv, own: mine, bad, set, remove }: { rowRef?: (el: HTMLDivElement | null) => void; seat: Seat; at: object; recv: boolean; own: boolean; bad: boolean; set: (p: Partial<Seat>) => void; remove: () => void }) {
  const m = useRowMenu();
  const asked = useQuery(choicesQ(seat.agent, at)).data;
  const role = seat.role && picked(seat.role, seat.pick);
  // A kind's line: the model it runs and its effort (its face says which agent).
  const model = seat.pick.model ? (asked?.models.find((x) => x.value === seat.pick.model)?.name ?? seat.pick.model) : draftDefault(asked).def?.name;
  const kindLine = [model ?? agentName(seat.agent), seat.pick.effort ? effortName(seat.pick.effort) : "", seat.m?.name ?? ""].filter(Boolean).join(" · ");
  const all = everyone(seat.recv);
  const recvLabel = t(all ? "new.recvAll" : "new.recvAt");
  const setRecv = (recv: Recv) => set({ recv, recvChosen: true });
  return (
    <div ref={rowRef} role="listitem" {...m.props} className="flex min-h-11 items-center gap-2 rounded-md px-1.5 py-1 pointer-coarse:min-h-13">
      {seat.role ? (
        <RoleFace role={seat.role} size={28} />
      ) : (
        <span className="relative grid size-7 shrink-0 place-items-center rounded-full bg-fill">
          <Logo agent={seat.agent} size={16} />
          {seat.m && <MachineBadge m={seat.m} size={12} />}
        </span>
      )}
      <span className="flex min-w-0 flex-1 flex-col">
        <input
          aria-label={t("channel.nick")}
          aria-invalid={bad || undefined}
          value={seat.nick}
          maxLength={32}
          placeholder={seat.role?.name ?? agentName(seat.agent)}
          onChange={(e) => set({ nick: e.target.value })}
          className={cn("no-ring -mx-1 h-6 min-w-0 rounded-sm bg-transparent px-1 text-sm placeholder:text-ink-3 focus:bg-fill", bad && "text-err")}
        />
        <Menu.Root>
          <Menu.Trigger aria-label={t("role.options")} className="g-press -mx-1 flex max-w-full min-w-0 items-center gap-1 rounded-sm px-1 text-xs text-ink-3 hover:bg-fill hover:text-ink-2 data-popup-open:bg-fill">
            {role ? <RoleLine role={role} at={at} logo className="min-w-0" /> : <span className="min-w-0 truncate">{kindLine}</span>}
            <ChevronDown size={12} className="shrink-0 text-ink-4" />
          </Menu.Trigger>
          <Menu.Portal>
            <Menu.Positioner side="bottom" align="start" sideOffset={4} collisionPadding={8} className="z-50">
              <Menu.Popup finalFocus={false} className="g-unfold glass thick float flex max-h-[min(560px,var(--available-height))] w-[min(300px,calc(100vw-16px))] flex-col rounded-lg p-1.5 text-ink outline-none">
                {seat.role ? (
                  <RoleModels role={seat.role} at={at} pick={seat.pick} set={(p) => set({ pick: p })} />
                ) : (
                  <Suspense fallback={<LoaderCircle size={15} className="mx-auto my-2.5 animate-spin text-ink-4" />}>
                    <DraftModels agent={seat.agent} at={at} model={seat.pick.model ?? ""} effort={seat.pick.effort ?? ""} preset={seat.pick.preset} set={(x) => set({ pick: { ...seat.pick, ...x } })} />
                  </Suspense>
                )}
              </Menu.Popup>
            </Menu.Positioner>
          </Menu.Portal>
        </Menu.Root>
      </span>
      {/* What it receives: pressed, the other (kumo, 2026-11-16: a choice, never assumed). */}
      {recv && (
        <Tip label={`${t("channel.recv")} · ${t(all ? "channel.recvAll" : "channel.recvAt")}`}>
          <button type="button" aria-label={`${t("channel.recv")}: ${recvLabel}`} onClick={() => setRecv(all ? RECV.at : RECV.all)} className="g-press h-6 shrink-0 rounded-full bg-fill px-2 text-xs text-ink-2 hover:bg-fill-2 pointer-coarse:h-8">
            <span key={String(all)} className="g-fade">
              {recvLabel}
            </span>
          </button>
        </Tip>
      )}
      {mine && !recv && <span className="shrink-0 px-1 text-xs text-ink-4">{t("new.own")}</span>}
      <RowMenu at={m.menuAt} close={() => m.setMenuAt(null)} recv={seat.recv} setRecv={setRecv} remove={() => (m.setMenuAt(null), remove())} />
    </div>
  );
}

/** What shows beside the panel, out of its row (anchor): to look at only, nothing on it to press
 *  nor to focus, over nothing of the panel; the panel stays as it is. Its last words kept while
 *  it shuts. */
function RoleBeside({ open, anchor, children }: { open: boolean; anchor: Element | null; children: ReactNode }) {
  const kept = useRef<ReactNode>(null);
  if (open && children) kept.current = children;
  return (
    // A tooltip's, not a popover's: one nested in the panel took its Escape.
    <Tooltip.Root open={open}>
      <Tooltip.Portal>
        {/* Out from the panel's edge (its row is 6px in from it), the panel's own gap away. */}
        <Tooltip.Positioner side="right" align="start" sideOffset={14} collisionPadding={8} anchor={anchor} className="pointer-events-none z-50">
          <Tooltip.Popup aria-hidden className="g-unfold float w-[min(288px,calc(100vw-16px))] overflow-hidden rounded-lg bg-paper outline-none">
            {open ? children : kept.current}
          </Tooltip.Popup>
        </Tooltip.Positioner>
      </Tooltip.Portal>
    </Tooltip.Root>
  );
}

/** The card a session started as role will have (the one card, chat/channel/profilecard.tsx):
 *  its background, its face, its 称呼 (the one typed, else the role's name), its line, its
 *  intro. Nothing on it to press: the row changes it. */
function RolePreview({ role, nick, at }: { role: Role; nick: string; at: object }) {
  return (
    <div key={role.id} className="g-fade">
      <ProfileCard face={<RoleFace role={role} size={56} />} name={nick || role.name} sub={<RoleLine role={role} at={at} logo className="min-w-0" />} background={role.background} keys={[]} />
    </div>
  );
}

/** What starting a session sends (channel.create's _data): the agent in the folder, in its
 *  group, at its machine, with its people; as a role, the role and what was changed of it. */
export const startRequest = (agent: Agent, cwd: string, workspace?: number, machine?: string, people?: string[], as?: As): NewSessionReq => {
  const { agents, on, ...card } = as ?? {};
  const members = people || agents?.length || on ? { ...(people ? { people } : {}), ...(agents?.length ? { agents } : {}), ...(on ? { on } : {}) } : undefined;
  return { agent, cwd, ...(workspace ? { workspace } : {}), ...(machine ? { machine } : {}), ...(members ? { members } : {}), ...card };
};

const DEFAULT_PEOPLE = [appAll("ir_web"), appAll("local")];
/** This machine, no channel: where a new session of the page's own runs (chat/choices.ts At). */
const NO_PLACE = {};
const ROW = "flex h-8 cursor-default items-center gap-2.5 rounded-md px-2.5 text-base outline-none select-none data-highlighted:bg-fill data-popup-open:bg-fill pointer-coarse:h-11";

/** A new session's people (internal/server DefaultPeople unless changed): the panel's logins
 *  and the host's at first, no Bot's. A row of its own for the Bots (kumo, 2026-11-16: 选 bot):
 *  it says those brought in; pressed, the connected apps that are no computer and not the
 *  panel, each app (all its logins) and its logins to bring in or leave out; under them the
 *  panel's, as before. */
function PeopleRow({ people, set }: { people: string[]; set: (p: string[]) => void }) {
  const d = useApps().data;
  const apps = (d?.apps ?? []).filter((a) => !isComputer(a));
  const bots = apps.filter((a) => !a.web);
  const panel = apps.filter((a) => a.web);
  const ids = apps.map((a) => a.id);
  const loginsOf = (app: string) => (d?.logins ?? []).filter((l) => l.app === app);
  const idsOf = (app: string) => loginsOf(app).map((l) => l.id);
  const inBots = bots.flatMap((a) => (hasApp(people, a.id) ? [a.name] : loginsOf(a.id).filter((l) => hasLogin(people, a.id, l.id)).map((l) => l.name)));
  const shown = inBots.join("、") || t("new.botNone");
  const check = (on: boolean) => <span className="grid size-3.5 place-items-center text-ink">{on && <Check size={14} />}</span>;
  // An app of one login is one row, pressed once (kumo, 2026-11-16); one of several, its row
  // (all of it) and each login's.
  const rows = (list: typeof apps) =>
    list.map((a) => {
      const logins = loginsOf(a.id);
      const one = logins.length <= 1;
      return (
        <div key={a.id} className="flex flex-col">
          <Menu.Item closeOnClick={false} onClick={() => set(pressApp(people, ids, idsOf, a.id))} className={ROW}>
            {check(hasApp(people, a.id) || (one && !!logins[0] && hasLogin(people, a.id, logins[0].id)))}
            <Avatar src={a.avatar} name={a.name} size={16} type={a.type} />
            <span className="min-w-0 flex-1 truncate font-medium">{a.web ? t("term.panel") : a.name}</span>
          </Menu.Item>
          {!one &&
            logins.map((l) => (
              <Menu.Item key={l.id} closeOnClick={false} onClick={() => set(pressLogin(people, ids, idsOf, a.id, l.id))} className={cn(ROW, "pl-8")}>
                {check(hasLogin(people, a.id, l.id))}
                <Avatar src={l.avatar} name={l.name} size={16} />
                <span className="min-w-0 flex-1 truncate">{l.name}</span>
              </Menu.Item>
            ))}
        </div>
      );
    });
  return (
    <Menu.Root>
      <Menu.Trigger
        render={
          <button type="button" className="flex h-8 min-w-0 items-center gap-2 rounded-md px-2 text-left hover:bg-fill data-popup-open:bg-fill pointer-coarse:h-10">
            <Bot size={15} className="shrink-0 text-ink-3" />
            <span className="shrink-0 text-sm">{t("new.bot")}</span>
            <span key={shown} className="g-fade min-w-0 flex-1 truncate text-right text-xs text-ink-3">{shown}</span>
            <ChevronRight size={14} className="shrink-0 text-ink-4" />
          </button>
        }
      />
      <Menu.Portal>
        <Menu.Positioner side="bottom" align="start" sideOffset={4} collisionPadding={8} className="z-50">
          <Menu.Popup finalFocus={false} className="g-unfold glass thick float no-bar max-h-[min(480px,var(--available-height))] min-w-56 max-w-[calc(100vw-16px)] overflow-y-auto rounded-lg p-1.5 text-ink outline-none">
            {/* No headings (kumo, 2026-11-16: no 人 to sort them under): the apps, the bots first. */}
            {rows(bots)}
            {rows(panel)}
          </Menu.Popup>
        </Menu.Positioner>
      </Menu.Portal>
    </Menu.Root>
  );
}

/** Another machine's agents, under its face and name: a press adds one to the lineup. */
function MachineAgents({ m, onPick, noAgent }: { m: Machine; onPick: (a: Agent) => void; noAgent?: ReactElement }) {
  return (
    <div role="group" aria-label={m.name} className="flex flex-col gap-1">
      <div className={cn("flex items-center gap-1.5 px-2 pt-1 text-xs", m.online ? "text-ink-2" : "text-ink-4")}>
        <MachineFace m={m} size={14} className={cn(!m.online && "opacity-50")} />
        <span className="truncate font-medium">{m.name}</span>
        {!m.online && <span className="shrink-0">{t("status.offline")}</span>}
      </div>
      {!m.agents.length && <p className="m-0 flex h-9 items-center px-2 text-sm text-ink-3">{t("new.noAgents")}</p>}
      <div className="g-stagger grid grid-cols-4 gap-1">
        {noAgent}
        {m.agents.map(({ agent }, i) => {
          const a = agent as Agent;
          const key = `${m.id}/${a}`;
          const says = (name: string) => (m.online ? `${name} · ${m.name}` : t("new.offline", { name: m.name }));
          return (
            <DraftNamed key={key} agent={a} at={{ machine: m.id }}>
              {(name) => (
                <Tip label={says(name)}>
                  <button type="button" aria-label={says(name)} onClick={() => onPick(a)} style={{ "--i": noAgent ? i + 1 : i } as CSSProperties} className="g-press grid h-12 place-items-center rounded-md hover:bg-fill disabled:opacity-50 pointer-coarse:h-14">
                    <span className={cn("relative grid", !m.online && "opacity-30 grayscale")}>
                      <Logo agent={a} size={24} />
                      <MachineBadge m={m} size={13} />
                    </span>
                  </button>
                </Tip>
              )}
            </DraftNamed>
          );
        })}
      </div>
    </div>
  );
}

/** Starts a session: the chat opens on it, the caret in the composer. busy: the agent starting
 *  ("<machine>/<agent>" at another machine), or the role ("role:<id>"). */
export function useStart(opened: (id: string) => void) {
  const [busy, setBusy] = useState<string | null>(null);
  const start = async (agent: Agent, cwd: string, workspace?: number, machine?: string, people?: string[], as?: As) => {
    setBusy(as?.agents?.length ? "lineup" : as?.role ? `role:${as.role}` : machine ? `${machine}/${agent}` : agent);
    try {
      const { id } = await api.create(startRequest(agent, cwd, workspace, machine, people, as));
      opened(id);
    } catch (e) {
      toast(t("err.newSession"), { error: true, description: errText(e) });
    } finally {
      setBusy(null);
    }
  };
  return { busy, start };
}

/** A subfolder made from the "+"'s folder line (NewSession onMake), at the folder's machine too:
 *  the folder then. An agent picked while it is being made waits for it and opens in it, or not at
 *  all when it failed (the folder picker's 选择 does the same). folder: as NewSession has it
 *  ("<machine>:<path>" at another). */
export function useSubfolder(folder: string, setFolder: (f: string) => void, open: boolean) {
  const qc = useQueryClient();
  const machines = useMachines();
  const making = useRef<Promise<string | null> | null>(null);
  // Which opening of the popover it is: one made for an earlier one, done late, is not the folder of
  // this one (a pick that waited for it still opens in it).
  const opening = useRef(0);
  useEffect(
    () => () => {
      opening.current++;
      making.current = null;
    },
    [open],
  );
  const make = (name: string) => {
    const there = pageAt(folder, machines);
    const at = there ? there.path : folder;
    const mine = opening.current;
    const made = api.mkdir({ path: at, name, ...(there ? { machine: there.m.id } : {}) }).then(
      () => {
        void qc.invalidateQueries({ queryKey: ["folders", there?.m.id ?? "", at] });
        const f = there ? atMachine(there.m, join(at, name)) : join(at, name);
        if (opening.current === mine) setFolder(f);
        return f;
      },
      (e) => (toast(t("err.mkdir"), { error: true, description: errText(e) }), null),
    );
    making.current = made;
    void made.finally(() => making.current === made && (making.current = null));
    return made.then(Boolean);
  };
  /** Where a pick opens: the folder (the one being made, once it is), and at another machine its
   *  path there; null: it was not made. */
  const settled = async (to?: Target): Promise<{ folder: string; to?: Target } | null> => {
    if (!making.current) return { folder, to };
    const made = await making.current;
    if (!made) return null;
    return { folder: made, to: retarget(made, to, machines) };
  };
  return { make, settled };
}

/** A pick's Target once the folder it was made for is made: at the folder's machine, its path there
 *  the new one. */
export function retarget(made: string, to: Target | undefined, ms: Machine[]): Target | undefined {
  const r = agentsAt(made, ms);
  return to && typeof r === "object" && r.m.id === to.m.id ? { m: to.m, cwd: r.cwd } : to;
}

/** The "+" at the sidebar's top: a new session, its folder picked first, then its agent. The folder
 *  is its group, or in one (the user, 2026-11-08: no workspaces to make first; the server groups it,
 *  discover GroupFor). It opens at this machine each time; a place pressed elsewhere that no listed
 *  group holds opens it at that place's machine, on its agents (places.ts createAt, 2026-11-10). */
export function CreatePopover({ trigger }: { trigger: ReactElement }) {
  const [open, setOpen] = useState(false);
  const [page, setPage] = useState<"folder" | "session">("folder");
  const [folder, setFolder] = useState("~");
  // A computer can hold the folder even if it cannot run an agent.
  const machines = useMachines();
  const [at, setAt] = useState<Machine | undefined>(undefined);
  const { busy, start } = useStart((id) => (setOpen(false), openChat(id)));
  // The folder as NewSession has it; a subfolder made there, the folder then.
  const here = at ? atMachine(at, folder) : folder;
  const sub = useSubfolder(here, (f) => setFolder(at ? f.slice(at.id.length + 1) : f), open);
  // Any paired machine, its agents or none (NewSession says so): only one not paired is gone.
  const ms = useRef(machines);
  ms.current = machines;
  useEffect(
    () =>
      onCreateAt((p) => {
        const whose = p.machine ? agentsAt(`${p.machine}:${p.path}`, ms.current) : "here";
        if (whose === "gone") return void toast(t("new.gone"), { error: true });
        setAt(whose === "here" ? undefined : whose.m);
        setFolder(p.path);
        setPage("session");
        setOpen(true);
      }),
    [],
  );
  // Its page slides only once it was turned to another: as it opens, the popover unfolds and
  // what is in it stays still in it, not sliding in under the unfolding.
  const shownAs = useRef({ open, page, stepped: false });
  if (shownAs.current.open !== open) shownAs.current = { open, page, stepped: false };
  else if (shownAs.current.page !== page) shownAs.current = { open, page, stepped: true };
  return (
    <Popover.Root
      open={open}
      onOpenChange={(o) => {
        setOpen(o);
        // At this machine each time (the user, 2026-11-10: no machine the list shows).
        if (o) {
          setPage("folder");
          if (at) setAt(undefined), setFolder("~");
        }
      }}
    >
      <Popover.Trigger render={trigger} />
      <PopSurface align="start" finalFocus={false}>
        {/* Page to page, sliding the way you went: on to the agents, back to the folder. */}
        <div key={page} data-dir={page === "folder" ? "back" : undefined} className={shownAs.current.stepped ? "g-slide" : undefined}>
          {page === "folder" ? (
            <div className="w-[min(352px,calc(100vw-16px))]">
              {machines.length > 0 && <MachineSwitch machines={machines} at={at} onAt={(m) => (setAt(m), setFolder("~"))} />}
              <FolderPicker
                key={at?.id ?? ""}
                machine={at}
                initial={folder}
                onChoose={(p) => {
                  setFolder(p);
                  setPage("session");
                }}
              />
            </div>
          ) : (
            <NewSession ws="" folder={here} busy={busy} onFolder={() => setPage("folder")} onMake={sub.make} onPick={(a, to, people, as) => void sub.settled(to).then((p) => p && start(a, p.to?.cwd ?? p.folder, undefined, p.to?.m.id, people, as))} />
          )}
        </div>
      </PopSurface>
    </Popover.Root>
  );
}

/** The "+" on a workspace row: a new session there, by an agent of the group's machine. */
export function WsPlus({ w, trigger, open, setOpen, opened }: { w: Workspace; trigger: ReactElement; open: boolean; setOpen: (o: boolean) => void; opened: (id: string) => void }) {
  const qc = useQueryClient();
  const [page, setPage] = useState<"session" | "folder">("session");
  const [stepped, setStepped] = useState(false); // gone to the folder page since it opened
  const [folder, setFolder] = useState(w.path);
  const { busy, start } = useStart((id) => (setOpen(false), opened(id)));
  // Each opening on the row's own folder and its agents, by the "+" or by the row's menu (which opens
  // it without onOpenChange): not the folder picked or made last time (the subfolder is listed under
  // the row by then).
  const [shown, setShown] = useState(open);
  if (open !== shown) {
    setShown(open);
    if (open) setPage("session"), setStepped(false), setFolder(w.path);
  }
  const machines = useMachines();
  // the folder page, ready before its line is tapped (opened by the "+" or by the row's menu); not
  // for a group at another machine, whose folders are not listed here
  const there = pageAt(folder, machines);
  const away = agentsAt(folder, machines) !== "here";
  useEffect(() => {
    if (open && !away) void qc.prefetchQuery(foldersQ(folder));
  }, [open, folder, qc, away]);
  const sub = useSubfolder(folder, setFolder, open);
  return (
    <Popover.Root open={open} onOpenChange={setOpen}>
      {/* Its roles asked for as the "+" is pointed at or pressed (and once idle, app.tsx), not as
          it opens: the popover is drawn whole. */}
      <Popover.Trigger render={trigger} onPointerEnter={prefetchRoles} onPointerDown={prefetchRoles} onFocus={prefetchRoles} />
      <PopSurface side="bottom" align="end" finalFocus={false}>
        {/* Page to page, sliding the way you went: into the folder, back to the agents (the user, 2026-11-06). */}
        <div key={page} data-dir={page === "session" ? "back" : undefined} className={page !== "session" || stepped ? "g-slide" : ""}>
          {page === "session" ? (
            // What still comes late (a role made elsewhere since) glides the popover's height.
            <Tall>
              <NewSession ws={w.name} folder={folder} busy={busy} onFolder={() => (setStepped(true), setPage("folder"))} onMake={sub.make} onPick={(a, to, people, as) => void sub.settled(to).then((at) => at && start(a, at.to?.cwd ?? at.folder, w.id, at.to?.m.id, people, as))} />
            </Tall>
          ) : (
            <FolderPicker
              machine={there?.m}
              initial={there ? there.path : folder}
              onChoose={(p) => {
                setFolder(there ? atMachine(there.m, p) : p);
                setPage("session");
              }}
            />
          )}
        </div>
      </PopSurface>
    </Popover.Root>
  );
}

/** Which machine's folders the picker shows: this one, or another connected one. When the strip
 *  overflows the popover, left and right arrows appear at its ends to step through it; at either
 *  end its arrow dims in place so the buttons never jump. */
function MachineSwitch({ machines, at, onAt }: { machines: Machine[]; at?: Machine; onAt: (m?: Machine) => void }) {
  const viewport = useRef<HTMLDivElement>(null);
  const [edges, setEdges] = useState({ overflow: false, left: false, right: false });
  useLayoutEffect(() => {
    const el = viewport.current;
    const row = el?.parentElement;
    const content = el?.firstElementChild;
    if (!el || !row || !content) return;
    const measure = () => {
      // Compare against the full row width so showing the arrows cannot keep them mounted by
      // shrinking the viewport; 1px tolerance absorbs subpixel scroll offsets.
      const overflow = el.scrollWidth > row.clientWidth;
      const left = el.scrollLeft > 1;
      const right = el.scrollWidth - el.clientWidth - el.scrollLeft > 1;
      setEdges((e) => (e.overflow === overflow && e.left === left && e.right === right ? e : { overflow, left, right }));
    };
    // Watch both the viewport (popover resize) and inner w-max strip (machines added or renamed).
    const resize = new ResizeObserver(measure);
    resize.observe(el);
    resize.observe(content);
    el.addEventListener("scroll", measure, { passive: true });
    measure();
    return () => {
      resize.disconnect();
      el.removeEventListener("scroll", measure);
    };
  }, []);
  // Step almost one view so the edge machine stays visible as an anchor on the opposite side; fall
  // back to 80% of the viewport when a wide item leaves less than a third of a view to advance.
  const scroll = (direction: -1 | 1) => {
    const el = viewport.current;
    if (!el || !(direction === -1 ? edges.left : edges.right)) return;
    const start = el.scrollLeft;
    const end = start + el.clientWidth;
    const visible = [...el.querySelectorAll("button")].filter((key) => key.offsetLeft >= start && key.offsetLeft + key.offsetWidth <= end);
    const keep = direction === -1 ? visible[0] : visible.at(-1);
    const distance = keep ? (direction === -1 ? end - keep.offsetLeft - keep.offsetWidth : keep.offsetLeft - start) : 0;
    el.scrollBy({ left: direction * (distance >= el.clientWidth / 3 ? distance : el.clientWidth * 0.8) });
  };
  // Use aria-disabled instead of disabled so hitting either end never drops keyboard focus.
  const arrow = "h-7 w-7 aria-disabled:opacity-35 aria-disabled:hover:bg-transparent pointer-coarse:h-9 pointer-coarse:w-10";
  // Let the popover's glass show through only at edges with more machines offscreen.
  const mask = `linear-gradient(to right, ${edges.left ? "transparent" : "#000"}, #000 12px, #000 calc(100% - 12px), ${edges.right ? "transparent" : "#000"})`;
  return (
    <div className="flex items-center pt-2">
      {edges.overflow && (
        <IconKey label={t("new.scrollMachinesLeft")} className={cn(arrow, "ms-1.5")} aria-disabled={!edges.left} onClick={() => scroll(-1)}>
          <ChevronLeft size={16} />
        </IconKey>
      )}
      <div
        ref={viewport}
        role="radiogroup"
        aria-label={t("new.machine")}
        className="no-bar min-w-0 flex-1 overflow-x-auto overscroll-x-contain scroll-px-3 motion-safe:scroll-smooth"
        style={edges.overflow ? { maskImage: mask, WebkitMaskImage: mask } : undefined}
      >
        {/* relative anchors each button's offsetLeft to the strip; w-max lets ResizeObserver track its full width. */}
        <div className="relative flex w-max gap-1 px-2">
          {[undefined, ...machines].map((m) => {
            const on = m?.id === at?.id;
            const off = !!m && !m.online;
            return (
              <button
                key={m?.id ?? ""}
                type="button"
                role="radio"
                aria-checked={on}
                disabled={off}
                onClick={() => !on && onAt(m)}
                className={cn("g-press flex h-7 shrink-0 items-center gap-1.5 rounded-full px-2.5 text-xs transition-colors duration-(--dur-2) focus-visible:-outline-offset-2 pointer-coarse:h-9", on ? "bg-ink text-on-ink" : "bg-fill text-ink-2 hover:bg-fill-2", off && "opacity-40")}
              >
                {m && <MachineFace m={m} size={14} />}
                {m ? m.name : t("term.local")}
                {off && <span>{t("status.offline")}</span>}
              </button>
            );
          })}
        </div>
      </div>
      {edges.overflow && (
        <IconKey label={t("new.scrollMachinesRight")} className={cn(arrow, "me-1.5")} aria-disabled={!edges.right} onClick={() => scroll(1)}>
          <ChevronRight size={16} />
        </IconKey>
      )}
    </div>
  );
}
