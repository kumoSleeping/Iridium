// Creating things (plan "网页" 新建): the sidebar's "+" (a new session: a folder, then an agent), a
// group row's "+" (a new session: pick an agent; the folder defaults to the group's) and the folder-only
// picker: a breadcrumb you can tap or type into, folders only, an inline new folder, one key.
import { Popover } from "@base-ui/react/popover";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronRight, Folder, FolderPlus, LoaderCircle } from "lucide-react";
import { useEffect, useLayoutEffect, useRef, useState, type CSSProperties, type ReactElement, type ReactNode } from "react";
import type { Workspace } from "./api/types.gen";
import { api, missing } from "./api/client";
import { openChat } from "./route";
import type { Agent } from "./api/types.gen";
import { t } from "./i18n";
import { agentsQ, useHome } from "./data";
import { errText, toast } from "./toast";
import { AGENTS, agentName, cn, homeOf, IconKey, Logo, PopSurface, tilde, Tip } from "./ui";

const join = (dir: string, name: string) => (dir.endsWith("/") ? dir + name : `${dir}/${name}`);
/** One folder's listing; prefetched before it is shown (the popover opening, a row hovered), so the
 *  picker opens and steps at its full height instead of a spinner springing open into the list. */
const foldersQ = (path: string) => ({ queryKey: ["folders", path], queryFn: () => api.folders(path), retry: false, staleTime: 10_000 });

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

/** The folder picker. initial: where it opens; onChoose: the folder chosen, absolute. */
export function FolderPicker({ initial, onChoose, startNew = false, busy }: { initial: string; onChoose: (path: string) => void; startNew?: boolean; busy?: boolean }) {
  const qc = useQueryClient();
  const [path, setPath] = useState(initial);
  const [dir, setDir] = useState<"fwd" | "back">("fwd");
  const [typing, setTyping] = useState<string | null>(null);
  const [newName, setNewName] = useState<string | null>(startNew ? "" : null);
  const q = useQuery(foldersQ(path));
  const nameBox = useRef<HTMLInputElement>(null);
  const root = useRef<HTMLDivElement>(null);
  const naming = newName !== null;
  useEffect(() => {
    if (naming) nameBox.current?.focus();
  }, [naming]);
  const here = q.data?.path ?? path;
  const home = useHome().data ?? homeOf(here);
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
    const s = segs.slice(0, i + 1).join("/") || "/";
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
    const made = api.mkdir({ path: at, name: n }).then(
      () => {
        void qc.invalidateQueries({ queryKey: ["folders", path] });
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
          {q.isPending && <LoaderCircle size={15} className="mx-auto my-2.5 animate-spin text-ink-4" />}
          {q.data?.folders.map((f) => (
            <button key={f} type="button" onPointerEnter={() => void qc.prefetchQuery(foldersQ(join(here, f)))} onFocus={() => void qc.prefetchQuery(foldersQ(join(here, f)))} onClick={() => go(join(here, f), "fwd")} className="flex h-9 w-full items-center gap-2.5 rounded-md px-2.5 text-left text-base hover:bg-fill pointer-coarse:h-11">
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

/** Pick an agent; the folder line opens the picker for a subfolder. Picking an agent opens the chat. */
export function NewSession({ ws, folder, onFolder, onPick, busy }: { ws: string; folder: string; onFolder: () => void; onPick: (a: Agent) => void; busy: Agent | null }) {
  // The agents the daemon can host (GET /api/agents); all four before it says. One not installed
  // is shown faded: picking it says so, no session is made.
  const info = useQuery({ ...agentsQ, refetchOnMount: "always" }).data; // installed since: the next open knows
  const agents = info?.map((a) => a.agent) ?? AGENTS;
  const missing = (a: Agent) => !!info?.find((x) => x.agent === a)?.missing;
  const pick = (a: Agent) => (missing(a) ? toast(t("new.notInstalled", { name: agentName(a) }), { error: true }) : onPick(a));
  return (
    <div className="flex w-[min(288px,calc(100vw-16px))] flex-col gap-1 p-1.5">
      <button type="button" onClick={onFolder} className="flex h-9 items-center gap-2 rounded-md px-2 text-left hover:bg-fill pointer-coarse:h-11" aria-label={t("new.folder")}>
        <Folder size={16} className="shrink-0 text-ink-3" />
        {ws && <span className="shrink-0 text-sm font-semibold">{ws}</span>}
        <span className="min-w-0 flex-1 truncate font-mono text-xs text-ink-3">{tilde(folder)}</span>
        <ChevronRight size={14} className="shrink-0 text-ink-4" />
      </button>
      <div role="group" aria-label={t("new.agent")} className="g-stagger grid grid-cols-4 gap-1">
        {agents.map((a, i) => (
          <Tip key={a} label={missing(a) ? t("new.notInstalled", { name: agentName(a) }) : agentName(a)}>
            <button type="button" aria-label={agentName(a)} disabled={busy !== null} onClick={() => pick(a)} style={{ "--i": i } as CSSProperties} className="g-press grid h-14 place-items-center rounded-md hover:bg-fill disabled:opacity-50">
              {busy === a ? <LoaderCircle size={20} className="animate-spin text-ink-3" /> : <Logo agent={a} size={24} className={cn(missing(a) && "opacity-30 grayscale")} />}
            </button>
          </Tip>
        ))}
      </div>
    </div>
  );
}

/** Starts a session: the chat opens on it, the caret in the composer. */
export function useStart(opened: (id: string) => void) {
  const [busy, setBusy] = useState<Agent | null>(null);
  const start = async (agent: Agent, cwd: string, workspace?: number) => {
    setBusy(agent);
    try {
      const { id } = await api.create({ agent, cwd, ...(workspace ? { workspace } : {}) });
      opened(id);
    } catch (e) {
      toast(t("err.newSession"), { error: true, description: errText(e) });
    } finally {
      setBusy(null);
    }
  };
  return { busy, start };
}

/** The "+" at the sidebar's top: a new session, its folder picked first, then its agent. The folder
 *  is its group, or in one (the user, 2026-11-08: no workspaces to make first; the server groups it,
 *  discover GroupFor). */
export function CreatePopover({ trigger }: { trigger: ReactElement }) {
  const [open, setOpen] = useState(false);
  const [page, setPage] = useState<"folder" | "session">("folder");
  const [folder, setFolder] = useState("~");
  const { busy, start } = useStart((id) => (setOpen(false), openChat(id)));
  return (
    <Popover.Root
      open={open}
      onOpenChange={(o) => {
        setOpen(o);
        if (o) setPage("folder");
      }}
    >
      <Popover.Trigger render={trigger} />
      <PopSurface align="start" finalFocus={false}>
        {/* Page to page, sliding the way you went: on to the agents, back to the folder. */}
        <div key={page} data-dir={page === "folder" ? "back" : undefined} className="g-slide">
          {page === "folder" ? (
            <FolderPicker
              initial={folder}
              onChoose={(p) => {
                setFolder(p);
                setPage("session");
              }}
            />
          ) : (
            <NewSession ws="" folder={folder} busy={busy} onFolder={() => setPage("folder")} onPick={(a) => void start(a, folder)} />
          )}
        </div>
      </PopSurface>
    </Popover.Root>
  );
}

/** The "+" on a workspace row: a new session there. */
export function WsPlus({ w, trigger, open, setOpen, opened }: { w: Workspace; trigger: ReactElement; open: boolean; setOpen: (o: boolean) => void; opened: (id: string) => void }) {
  const qc = useQueryClient();
  const [page, setPage] = useState<"session" | "folder">("session");
  const [stepped, setStepped] = useState(false); // gone to the folder page since it opened
  const [folder, setFolder] = useState(w.path);
  const { busy, start } = useStart((id) => (setOpen(false), opened(id)));
  // the folder page, ready before its line is tapped (opened by the "+" or by the row's menu)
  useEffect(() => {
    if (open) void qc.prefetchQuery(foldersQ(folder));
  }, [open, folder, qc]);
  return (
    <Popover.Root
      open={open}
      onOpenChange={(o) => {
        setOpen(o);
        if (o) setPage("session"), setStepped(false);
      }}
    >
      <Popover.Trigger render={trigger} />
      <PopSurface side="bottom" align="end" finalFocus={false}>
        {/* Page to page, sliding the way you went: into the folder, back to the agents (the user, 2026-11-06). */}
        <div key={page} data-dir={page === "session" ? "back" : undefined} className={page === "folder" || stepped ? "g-slide" : ""}>
          {page === "session" ? (
            <NewSession ws={w.name} folder={folder} busy={busy} onFolder={() => (setStepped(true), setPage("folder"))} onPick={(a) => void start(a, folder, w.id)} />
          ) : (
            <FolderPicker
              initial={folder}
              onChoose={(p) => {
                setFolder(p);
                setPage("session");
              }}
            />
          )}
        </div>
      </PopSurface>
    </Popover.Root>
  );
}
