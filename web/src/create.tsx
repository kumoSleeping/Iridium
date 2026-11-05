// Creating things (plan "网页" 新建): the sidebar's "+" popover (new session, new workspace), the
// new-session page (pick an agent; the folder defaults to the workspace's root) and the folder-only
// picker: a breadcrumb you can tap or type into, folders only, an inline new folder, one key.
import { Popover } from "@base-ui/react/popover";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronRight, Folder, FolderPlus, LoaderCircle, MessageSquarePlus, PencilLine, SquarePlus } from "lucide-react";
import { useState, type CSSProperties, type ReactElement } from "react";
import type { Workspace } from "./api/types.gen";
import { api, missing } from "./api/client";
import type { Agent } from "./api/types.gen";
import { t } from "./i18n";
import { useAgents, useHome } from "./data";
import { errText, toast } from "./toast";
import { AGENTS, agentName, cn, homeOf, IconKey, Logo, PopSurface, tilde, Tip } from "./ui";

export type NewPage = "menu" | "session" | "sessionFolder" | "workspace";

const base = (p: string) => p.split("/").filter(Boolean).pop() ?? p;
const join = (dir: string, name: string) => (dir.endsWith("/") ? dir + name : `${dir}/${name}`);

/** The folder picker. initial: where it opens; onChoose: the folder chosen, absolute. */
export function FolderPicker({ initial, onChoose, startNew = false, busy }: { initial: string; onChoose: (path: string) => void; startNew?: boolean; busy?: boolean }) {
  const qc = useQueryClient();
  const [path, setPath] = useState(initial);
  const [dir, setDir] = useState<"fwd" | "back">("fwd");
  const [typing, setTyping] = useState<string | null>(null);
  const [newName, setNewName] = useState<string | null>(startNew ? "" : null);
  const q = useQuery({ queryKey: ["folders", path], queryFn: () => api.folders(path), retry: false, staleTime: 10_000 });
  const here = q.data?.path ?? path;
  const home = useHome().data ?? homeOf(here);
  const gone = q.error && missing(q.error); // the route is not served yet: only typing a path works
  const shown = tilde(here, home);
  const segs = shown.split("/").filter((s, i) => s || i === 0);
  const go = (p: string, d: "fwd" | "back") => {
    setDir(d);
    setPath(p);
    setNewName(null);
  };
  const segPath = (i: number) => {
    const s = segs.slice(0, i + 1).join("/") || "/";
    return s.startsWith("~") && home ? home + s.slice(1) : s;
  };
  const commitNew = async () => {
    const n = newName?.trim();
    setNewName(null);
    if (!n || /[/\0]/.test(n)) return;
    try {
      await api.mkdir({ path: here, name: n });
      await qc.invalidateQueries({ queryKey: ["folders", path] });
    } catch (e) {
      toast(t("err.mkdir"), { error: true, description: errText(e) });
    }
  };
  return (
    <div className="flex w-[min(352px,calc(100vw-16px))] flex-col">
      <div className="flex h-11 items-center gap-1 pr-1.5 pl-2">
        {typing !== null ? (
          <input
            autoFocus
            aria-label={t("pick.typePath")}
            value={typing}
            onChange={(e) => setTyping(e.target.value)}
            onBlur={() => setTyping(null)}
            onKeyDown={(e) => {
              if (e.key === "Enter") {
                const v = typing.trim().replace(/(.)\/+$/, "$1") || "~";
                go(v.startsWith("~") && home ? home + v.slice(1) : v, "fwd");
                setTyping(null);
              }
              if (e.key === "Escape") {
                e.stopPropagation();
                setTyping(null);
              }
            }}
            className="no-ring h-8 min-w-0 flex-1 rounded-md bg-fill px-2 font-mono text-sm"
          />
        ) : (
          <nav aria-label={t("pick.path")} className="flex min-w-0 flex-1 items-center overflow-x-auto [scrollbar-width:none]" onClick={(e) => e.target === e.currentTarget && setTyping(shown)}>
            {segs.map((s, i) => {
              const last = i === segs.length - 1;
              return (
                <span key={i} className="flex shrink-0 items-center">
                  {i > 0 && <ChevronRight size={12} className="mx-0.5 text-ink-4" />}
                  <button
                    type="button"
                    onClick={() => !last && go(segPath(i), "back")}
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
        <IconKey label={t("pick.typePath")} onClick={() => setTyping(shown)} className="size-7 pointer-coarse:size-9">
          <PencilLine size={15} />
        </IconKey>
        <IconKey label={t("pick.newFolder")} onClick={() => setNewName("")} disabled={!!gone} className="size-7 pointer-coarse:size-9">
          <FolderPlus size={15} />
        </IconKey>
      </div>
      <div key={here} data-dir={dir === "back" ? "back" : undefined} className="g-slide max-h-[min(264px,45vh)] min-h-9 overflow-y-auto px-1.5 pb-1">
        {newName !== null && (
          <div className="flex h-9 items-center gap-2.5 rounded-md bg-fill px-2.5">
            <Folder size={16} className="shrink-0 text-ink-3" />
            <input
              autoFocus
              aria-label={t("pick.folderName")}
              placeholder={t("pick.folderName")}
              value={newName}
              onChange={(e) => setNewName(e.target.value)}
              onBlur={() => void commitNew()}
              onKeyDown={(e) => {
                if (e.key === "Enter") void commitNew();
                if (e.key === "Escape") {
                  e.stopPropagation();
                  setNewName(null);
                }
              }}
              className="no-ring h-full min-w-0 flex-1 bg-transparent text-base placeholder:text-ink-3"
            />
          </div>
        )}
        {q.isPending && <LoaderCircle size={15} className="mx-auto my-2.5 animate-spin text-ink-4" />}
        {q.data?.folders.map((f) => (
          <button key={f} type="button" onClick={() => go(join(here, f), "fwd")} className="flex h-9 w-full items-center gap-2.5 rounded-md px-2.5 text-left text-base hover:bg-fill pointer-coarse:h-11">
            <Folder size={16} className="shrink-0 text-ink-3" />
            <span className="truncate">{f}</span>
          </button>
        ))}
        {q.error && !gone && <p className="m-0 px-2.5 py-2 text-sm text-ink-3">{errText(q.error)}</p>}
      </div>
      <div className="flex justify-end px-2.5 pt-1.5 pb-2.5">
        <button type="button" disabled={busy || !!(q.error && !gone)} onClick={() => onChoose(here)} className="g-press flex h-8 items-center gap-1.5 rounded-md bg-ink px-3.5 text-sm font-medium text-on-ink disabled:opacity-40 pointer-coarse:h-10">
          {busy && <LoaderCircle size={14} className="animate-spin" />}
          {t("pick.choose")}
        </button>
      </div>
    </div>
  );
}

/** Pick an agent; the folder line opens the picker for a subfolder. Picking an agent opens the chat. */
export function NewSession({ ws, folder, onFolder, onPick, busy }: { ws: string; folder: string; onFolder: () => void; onPick: (a: Agent) => void; busy: Agent | null }) {
  // The agents the daemon can host (GET /api/agents); all four before it says.
  const agents = useAgents().data?.map((a) => a.agent) ?? AGENTS;
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
          <Tip key={a} label={agentName(a)}>
            <button type="button" aria-label={agentName(a)} disabled={busy !== null} onClick={() => onPick(a)} style={{ "--i": i } as CSSProperties} className="g-press grid h-14 place-items-center rounded-md hover:bg-fill disabled:opacity-50">
              {busy === a ? <LoaderCircle size={20} className="animate-spin text-ink-3" /> : <Logo agent={a} size={24} />}
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

/** The "+" at the sidebar's top: two items, whose pages replace it in place. ws: the workspace a new
 *  session goes in, the open one's or the first; home: where a new workspace's picker opens. */
export function CreatePopover({ trigger, ws, fallbackDir, opened }: { trigger: ReactElement; ws: Workspace | null; fallbackDir: string; opened: (id: string) => void }) {
  const qc = useQueryClient();
  const [open, setOpen] = useState(false);
  const [page, setPage] = useState<NewPage>("menu");
  const root = ws?.path ?? fallbackDir;
  const [folder, setFolder] = useState(root);
  const [lastRoot, setLastRoot] = useState(root);
  if (lastRoot !== root) {
    setLastRoot(root);
    setFolder(root);
  }
  const { busy, start } = useStart((id) => (setOpen(false), opened(id)));
  const [making, setMaking] = useState(false);
  const newWorkspace = async (path: string) => {
    setMaking(true);
    try {
      const w = await api.createWorkspace({ path, name: base(path) });
      await qc.invalidateQueries({ queryKey: ["workspaces"] });
      setOpen(false);
      toast(t("toast.created"), { description: w.name });
    } catch (e) {
      toast(t("err.newWorkspace"), { error: true, description: missing(e) ? t("err.notYet") : errText(e) });
    } finally {
      setMaking(false);
    }
  };
  return (
    <Popover.Root
      open={open}
      onOpenChange={(o) => {
        setOpen(o);
        if (o) setPage("menu");
      }}
    >
      <Popover.Trigger render={trigger} />
      <PopSurface align="start" finalFocus={false}>
        <div key={page} className={page === "menu" ? "" : "g-fade"}>
          {page === "menu" && (
            <div className="g-stagger flex w-52 flex-col p-1.5">
              <MenuRow i={0} icon={<MessageSquarePlus size={16} />} label={t("nav.newSession")} onClick={() => setPage("session")} />
              <MenuRow i={1} icon={<SquarePlus size={16} />} label={t("nav.newWorkspace")} onClick={() => setPage("workspace")} />
            </div>
          )}
          {page === "session" && <NewSession ws={ws?.name ?? ""} folder={folder} busy={busy} onFolder={() => setPage("sessionFolder")} onPick={(a) => void start(a, folder, ws?.id)} />}
          {page === "sessionFolder" && (
            <FolderPicker
              initial={folder}
              onChoose={(p) => {
                setFolder(p);
                setPage("session");
              }}
            />
          )}
          {page === "workspace" && <FolderPicker initial="~" busy={making} onChoose={(p) => void newWorkspace(p)} />}
        </div>
      </PopSurface>
    </Popover.Root>
  );
}

/** The "+" on a workspace row: a new session there. */
export function WsPlus({ w, trigger, open, setOpen, opened }: { w: Workspace; trigger: ReactElement; open: boolean; setOpen: (o: boolean) => void; opened: (id: string) => void }) {
  const [page, setPage] = useState<"session" | "folder">("session");
  const [folder, setFolder] = useState(w.path);
  const { busy, start } = useStart((id) => (setOpen(false), opened(id)));
  return (
    <Popover.Root
      open={open}
      onOpenChange={(o) => {
        setOpen(o);
        if (o) setPage("session");
      }}
    >
      <Popover.Trigger render={trigger} />
      <PopSurface side="bottom" align="end" finalFocus={false}>
        <div key={page} className={page === "folder" ? "g-fade" : ""}>
          {page === "session" ? (
            <NewSession ws={w.name} folder={folder} busy={busy} onFolder={() => setPage("folder")} onPick={(a) => void start(a, folder, w.id)} />
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

function MenuRow({ i, icon, label, onClick }: { i: number; icon: ReactElement; label: string; onClick: () => void }) {
  return (
    <button type="button" onClick={onClick} style={{ "--i": i } as CSSProperties} className="flex h-9 items-center gap-2.5 rounded-md px-2.5 text-left text-base hover:bg-fill pointer-coarse:h-11">
      <span className="text-ink-2">{icon}</span>
      {label}
    </button>
  );
}
