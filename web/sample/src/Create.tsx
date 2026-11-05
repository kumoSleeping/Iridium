// Creating things: the sidebar's "+" popover (new session, new workspace), the new-session page (pick an
// agent; the folder defaults to the workspace's root) and the folder picker.
import { Popover } from "@base-ui/react/popover";
import { ChevronRight, Folder, FolderPlus, MessageSquarePlus, PencilLine, SquarePlus } from "lucide-react";
import { useState, type ReactElement } from "react";
import { listFolders, type Agent } from "./data";
import { t } from "./i18n";
import { agentName, cn, IconKey, Logo, PopSurface, Tip } from "./ui";

export type NewPage = "menu" | "session" | "sessionFolder" | "workspace";

const base = (p: string) => p.split("/").filter(Boolean).pop() ?? p;

/** The folder picker: a breadcrumb you can tap or type into, folders only, an inline new-folder field,
 *  and one key to choose. */
export function FolderPicker({ initial, onChoose, startNew = false }: { initial: string; onChoose: (path: string) => void; startNew?: boolean }) {
  const [path, setPath] = useState(initial);
  const [dir, setDir] = useState<"fwd" | "back">("fwd");
  const [typing, setTyping] = useState<string | null>(null);
  const [newName, setNewName] = useState<string | null>(startNew ? "" : null);
  const [made, setMade] = useState<Record<string, string[]>>({});
  const folders = [...listFolders(path), ...(made[path] ?? [])].sort((a, b) => a.localeCompare(b, undefined, { sensitivity: "base" }));
  const segs = path.split("/");
  const go = (p: string, d: "fwd" | "back") => {
    setDir(d);
    setPath(p);
    setNewName(null);
  };
  const commitNew = () => {
    const n = newName?.trim();
    if (n) setMade((m) => ({ ...m, [path]: [...(m[path] ?? []), n] }));
    setNewName(null);
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
                const p = typing.trim().replace(/\/+$/, "") || "~";
                go(p, "fwd");
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
          <nav aria-label="path" className="flex min-w-0 flex-1 items-center overflow-x-auto [scrollbar-width:none]" onClick={(e) => e.target === e.currentTarget && setTyping(path)}>
            {segs.map((s, i) => {
              const p = segs.slice(0, i + 1).join("/");
              const last = i === segs.length - 1;
              return (
                <span key={p} className="flex shrink-0 items-center">
                  {i > 0 && <ChevronRight size={12} className="mx-0.5 text-ink-4" />}
                  <button
                    type="button"
                    onClick={() => !last && go(p, "back")}
                    className={cn("g-press rounded-sm px-1.5 py-1 text-sm whitespace-nowrap", last ? "font-semibold text-ink" : "text-ink-2 hover:bg-fill hover:text-ink")}
                    aria-current={last ? "location" : undefined}
                  >
                    {s === "~" ? <span aria-label={t("pick.home")}>~</span> : s}
                  </button>
                </span>
              );
            })}
          </nav>
        )}
        <IconKey label={t("pick.typePath")} onClick={() => setTyping(path)} className="size-7 pointer-coarse:size-9">
          <PencilLine size={15} />
        </IconKey>
        <IconKey label={t("pick.newFolder")} onClick={() => setNewName("")} className="size-7 pointer-coarse:size-9">
          <FolderPlus size={15} />
        </IconKey>
      </div>
      <div key={path} data-dir={dir === "back" ? "back" : undefined} className="g-slide max-h-[min(264px,45vh)] overflow-y-auto px-1.5 pb-1">
        {newName !== null && (
          <div className="flex h-9 items-center gap-2.5 rounded-md bg-fill px-2.5">
            <Folder size={16} className="shrink-0 text-ink-3" />
            <input
              autoFocus
              aria-label={t("pick.folderName")}
              placeholder={t("pick.folderName")}
              value={newName}
              onChange={(e) => setNewName(e.target.value)}
              onBlur={commitNew}
              onKeyDown={(e) => {
                if (e.key === "Enter") commitNew();
                if (e.key === "Escape") {
                  e.stopPropagation();
                  setNewName(null);
                }
              }}
              className="no-ring h-full min-w-0 flex-1 bg-transparent text-base placeholder:text-ink-3"
            />
          </div>
        )}
        {folders.map((f) => (
          <button key={f} type="button" onClick={() => go(`${path}/${f}`, "fwd")} className="flex h-9 w-full items-center gap-2.5 rounded-md px-2.5 text-left text-base hover:bg-fill pointer-coarse:h-11">
            <Folder size={16} className="shrink-0 text-ink-3" />
            <span className="truncate">{f}</span>
          </button>
        ))}
      </div>
      <div className="flex justify-end px-2.5 pt-1.5 pb-2.5">
        <button type="button" onClick={() => onChoose(path)} className="g-press h-8 rounded-md bg-ink px-3.5 text-sm font-medium text-on-ink pointer-coarse:h-10">
          {t("pick.choose")}
        </button>
      </div>
    </div>
  );
}

/** Pick an agent; the folder line opens the picker for a subfolder. Picking an agent opens the chat. */
export function NewSession({ ws, folder, onFolder, onPick }: { ws: string; folder: string; onFolder: () => void; onPick: (a: Agent) => void }) {
  const agents: Agent[] = ["claude", "codex", "pi", "dsh"];
  return (
    <div className="flex w-[min(288px,calc(100vw-16px))] flex-col gap-1 p-1.5">
      <button type="button" onClick={onFolder} className="flex h-9 items-center gap-2 rounded-md px-2 text-left hover:bg-fill pointer-coarse:h-11" aria-label={t("new.folder")}>
        <Folder size={16} className="shrink-0 text-ink-3" />
        <span className="shrink-0 text-sm font-semibold">{ws}</span>
        <span className="min-w-0 flex-1 truncate font-mono text-xs text-ink-3">{folder}</span>
        <ChevronRight size={14} className="shrink-0 text-ink-4" />
      </button>
      <div role="group" aria-label={t("new.agent")} className="g-stagger grid grid-cols-4 gap-1">
        {agents.map((a, i) => (
          <Tip key={a} label={agentName(a)}>
            <button type="button" aria-label={agentName(a)} onClick={() => onPick(a)} style={{ "--i": i } as React.CSSProperties} className="g-press grid h-14 place-items-center rounded-md hover:bg-fill">
              <Logo agent={a} size={24} />
            </button>
          </Tip>
        ))}
      </div>
    </div>
  );
}

/** The "+" at the sidebar's top: a small popover with two items, whose pages replace it in place. */
export function CreatePopover({
  open,
  onOpenChange,
  page,
  setPage,
  trigger,
  ws,
  root,
  onSession,
  onWorkspace,
  startNewFolder,
}: {
  open: boolean;
  onOpenChange: (o: boolean) => void;
  page: NewPage;
  setPage: (p: NewPage) => void;
  trigger: ReactElement;
  ws: string;
  root: string;
  onSession: (a: Agent, folder: string) => void;
  onWorkspace: (path: string, name: string) => void;
  startNewFolder?: boolean;
}) {
  const [folder, setFolder] = useState(root);
  const [lastRoot, setLastRoot] = useState(root);
  if (lastRoot !== root) {
    setLastRoot(root);
    setFolder(root);
  }
  return (
    <Popover.Root open={open} onOpenChange={onOpenChange}>
      <Popover.Trigger render={trigger} />
      <PopSurface align="start" finalFocus={false}>
        <div key={page} className={page === "menu" ? "" : "g-fade"}>
          {page === "menu" && (
            <div className="g-stagger flex w-52 flex-col p-1.5">
              <MenuRow i={0} icon={<MessageSquarePlus size={16} />} label={t("nav.newSession")} onClick={() => setPage("session")} />
              <MenuRow i={1} icon={<SquarePlus size={16} />} label={t("nav.newWorkspace")} onClick={() => setPage("workspace")} />
            </div>
          )}
          {page === "session" && <NewSession ws={ws} folder={folder} onFolder={() => setPage("sessionFolder")} onPick={(a) => onSession(a, folder)} />}
          {page === "sessionFolder" && (
            <FolderPicker
              initial={folder}
              onChoose={(p) => {
                setFolder(p);
                setPage("session");
              }}
            />
          )}
          {page === "workspace" && <FolderPicker initial="~/kumo" startNew={startNewFolder} onChoose={(p) => onWorkspace(p, base(p))} />}
        </div>
      </PopSurface>
    </Popover.Root>
  );
}

function MenuRow({ i, icon, label, onClick }: { i: number; icon: ReactElement; label: string; onClick: () => void }) {
  return (
    <button type="button" onClick={onClick} style={{ "--i": i } as React.CSSProperties} className="flex h-9 items-center gap-2.5 rounded-md px-2.5 text-left text-base hover:bg-fill pointer-coarse:h-11">
      <span className="text-ink-2">{icon}</span>
      {label}
    </button>
  );
}
