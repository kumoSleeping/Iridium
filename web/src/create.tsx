// Creating things (plan "网页" 新建): the sidebar's "+" (a new session: a folder, then an agent), a
// group row's "+" (a new session: pick an agent; the folder defaults to the group's) and the folder-only
// picker: a breadcrumb you can tap or type into, folders only, an inline new folder, one key.
import { Popover } from "@base-ui/react/popover";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronRight, Folder, FolderPlus, LoaderCircle } from "lucide-react";
import { useEffect, useLayoutEffect, useRef, useState, type CSSProperties, type ReactElement, type ReactNode } from "react";
import type { Workspace } from "./api/types.gen";
import { api, missing } from "./api/client";
import { born } from "./chats";
import { openChat } from "./route";
import type { Agent } from "./api/types.gen";
import { t } from "./i18n";
import { agentsQ, useHome, useMachines } from "./data";
import { agentsAt, atMachine, homeAt, MachineBadge, MachineFace, pageAt, type Machine } from "./machines";
import { onCreateAt } from "./places";
import { errText, toast } from "./toast";
import { AGENTS, agentName, cn, homeOf, IconKey, Logo, PopSurface, tilde, Tip } from "./ui";

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
      <div aria-hidden={newName === null || undefined} className={cn("grid px-1.5 transition-[grid-template-rows,opacity] duration-(--dur-2) ease-(--ease-out)", newName !== null ? "grid-rows-[1fr]" : "grid-rows-[0fr] opacity-0")}>
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

/** Pick an agent; the folder line opens the picker for a subfolder. Picking an agent opens the chat.
 *  A folder offers its own machine's agents only (docs/exec.md "远端工作区", 2026-11-10): this
 *  machine's in a folder here; another's, its face on their logos and its name over them, in a folder
 *  there, faded and saying so while it is not connected. A group of a machine no more there (its Bot
 *  gone) offers none. A pick of this machine's comes with no Target. onMake: a subfolder of the
 *  folder made (at its machine too), the folder then; its key beside the folder's line (the user,
 *  2026-11-10). */
export function NewSession({ ws, folder, onFolder, onPick, busy, onMake }: { ws: string; folder: string; onFolder: () => void; onPick: (a: Agent, to?: Target) => void; busy: string | null; onMake?: (name: string) => Promise<boolean> }) {
  // The agents the daemon can host (GET /v1/internal/agents); all four before it says. One not installed
  // is shown faded: picking it says so, no session is made.
  const info = useQuery({ ...agentsQ, refetchOnMount: "always" }).data; // installed since: the next open knows
  const agents = info?.map((a) => a.agent) ?? AGENTS;
  const missing = (a: Agent) => !!info?.find((x) => x.agent === a)?.missing;
  const pick = (a: Agent) => (missing(a) ? toast(t("new.notInstalled", { name: agentName(a) }), { error: true }) : onPick(a));
  const machines = useMachines();
  const whose = agentsAt(folder, machines);
  const there = typeof whose === "object" ? whose : undefined;
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
  let i = 0;
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
  return (
    <div ref={box} tabIndex={-1} className="flex w-[min(288px,calc(100vw-16px))] flex-col gap-1 p-1.5 outline-none">
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
      {!there ? (
        <div role="group" aria-label={t("new.agent")} className="g-stagger grid grid-cols-4 gap-1">
          {agents.map((a) => (
            <Tip key={a} label={missing(a) ? t("new.notInstalled", { name: agentName(a) }) : agentName(a)}>
              <button type="button" aria-label={agentName(a)} disabled={busy !== null} onClick={() => pick(a)} style={{ "--i": i++ } as CSSProperties} className="g-press grid h-14 place-items-center rounded-md hover:bg-fill disabled:opacity-50">
                {busy === a ? <LoaderCircle size={20} className="animate-spin text-ink-3" /> : <Logo agent={a} size={24} className={cn(missing(a) && "opacity-30 grayscale")} />}
              </button>
            </Tip>
          ))}
        </div>
      ) : (
        <MachineAgents m={there.m} busy={busy} onPick={(a) => (!there.m.online ? toast(t("new.offline", { name: there.m.name }), { error: true }) : onPick(a, { m: there.m, cwd: there.cwd }))} />
      )}
    </div>
  );
}

/** Another machine's agents, under its face and name. */
function MachineAgents({ m, busy, onPick }: { m: Machine; busy: string | null; onPick: (a: Agent) => void }) {
  return (
    <div role="group" aria-label={m.name} className="flex flex-col gap-1">
      <div className={cn("flex items-center gap-1.5 px-2 pt-1 text-xs", m.online ? "text-ink-2" : "text-ink-4")}>
        <MachineFace m={m} size={14} className={cn(!m.online && "opacity-50")} />
        <span className="truncate font-medium">{m.name}</span>
        {!m.online && <span className="shrink-0">{t("status.offline")}</span>}
      </div>
      {!m.agents.length && <p className="m-0 flex h-9 items-center px-2 text-sm text-ink-3">{t("new.noAgents")}</p>}
      <div className="g-stagger grid grid-cols-4 gap-1">
        {m.agents.map(({ agent }, i) => {
          const a = agent as Agent;
          const key = `${m.id}/${a}`;
          const label = m.online ? `${agentName(a)} · ${m.name}` : t("new.offline", { name: m.name });
          return (
            <Tip key={key} label={label}>
              <button type="button" aria-label={label} disabled={busy !== null} onClick={() => onPick(a)} style={{ "--i": i } as CSSProperties} className="g-press grid h-14 place-items-center rounded-md hover:bg-fill disabled:opacity-50">
                {busy === key ? (
                  <LoaderCircle size={20} className="animate-spin text-ink-3" />
                ) : (
                  <span className={cn("relative grid", !m.online && "opacity-30 grayscale")}>
                    <Logo agent={a} size={24} />
                    <MachineBadge m={m} size={13} />
                  </span>
                )}
              </button>
            </Tip>
          );
        })}
      </div>
    </div>
  );
}

/** Starts a session: the chat opens on it, the caret in the composer. busy: the agent starting
 *  ("<machine>/<agent>" at another machine). */
export function useStart(opened: (id: string) => void) {
  const [busy, setBusy] = useState<string | null>(null);
  const start = async (agent: Agent, cwd: string, workspace?: number, machine?: string) => {
    setBusy(machine ? `${machine}/${agent}` : agent);
    try {
      const { id } = await api.create({ agent, cwd, ...(workspace ? { workspace } : {}), ...(machine ? { machine } : {}) });
      born.add(id);
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
  // Another machine's folders (docs/exec.md): picked above the folders; its agents alone then.
  const all = useMachines();
  const machines = all.filter((m) => m.agents.length > 0);
  const [at, setAt] = useState<Machine | undefined>(undefined);
  const { busy, start } = useStart((id) => (setOpen(false), openChat(id)));
  // The folder as NewSession has it; a subfolder made there, the folder then.
  const here = at ? atMachine(at, folder) : folder;
  const sub = useSubfolder(here, (f) => setFolder(at ? f.slice(at.id.length + 1) : f), open);
  // Any paired machine, its agents or none (NewSession says so): only one not paired is gone.
  const ms = useRef(all);
  ms.current = all;
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
        <div key={page} data-dir={page === "folder" ? "back" : undefined} className="g-slide">
          {page === "folder" ? (
            <>
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
            </>
          ) : (
            <NewSession ws="" folder={here} busy={busy} onFolder={() => setPage("folder")} onMake={sub.make} onPick={(a, to) => void sub.settled(to).then((p) => p && start(a, p.to?.cwd ?? p.folder, undefined, p.to?.m.id))} />
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
      <Popover.Trigger render={trigger} />
      <PopSurface side="bottom" align="end" finalFocus={false}>
        {/* Page to page, sliding the way you went: into the folder, back to the agents (the user, 2026-11-06). */}
        <div key={page} data-dir={page === "session" ? "back" : undefined} className={page !== "session" || stepped ? "g-slide" : ""}>
          {page === "session" ? (
            <NewSession ws={w.name} folder={folder} busy={busy} onFolder={() => (setStepped(true), setPage("folder"))} onMake={sub.make} onPick={(a, to) => void sub.settled(to).then((at) => at && start(a, at.to?.cwd ?? at.folder, w.id, at.to?.m.id))} />
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

/** Which machine's folders the picker shows: this one, or another connected one. */
function MachineSwitch({ machines, at, onAt }: { machines: Machine[]; at?: Machine; onAt: (m?: Machine) => void }) {
  return (
    <div role="radiogroup" aria-label={t("new.machine")} className="flex gap-1 overflow-x-auto px-2 pt-2 [scrollbar-width:none]">
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
            className={cn("g-press flex h-7 shrink-0 items-center gap-1.5 rounded-full px-2.5 text-xs transition-colors duration-(--dur-2) pointer-coarse:h-9", on ? "bg-ink text-on-ink" : "bg-fill text-ink-2 hover:bg-fill-2", off && "opacity-40")}
          >
            {m && <MachineFace m={m} size={14} />}
            {m ? m.name : t("term.local")}
            {off && <span>{t("status.offline")}</span>}
          </button>
        );
      })}
    </div>
  );
}
