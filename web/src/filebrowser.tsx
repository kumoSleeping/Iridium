// Read-only file browser: a computer switcher, editable breadcrumb bar, single-level folder list
// and inline preview (reusing chat/preview.tsx). Desktop shows both panes side by side; a narrow
// container (<620px) shows one pane at a time.
import { Dialog } from "@base-ui/react/dialog";
import { Menu } from "@base-ui/react/menu";
import { type UseQueryResult } from "@tanstack/react-query";
import { useQuery } from "./api/query";
import { ArrowLeft, ChevronRight, Copy, Download, ExternalLink, Eye, FileText, Folder, FolderOpen, House, Link2, FolderPlus, LoaderCircle, Plus, RefreshCw } from "lucide-react";
import { lazy, memo, Suspense, useCallback, useLayoutEffect, useMemo, useRef, useState, type ReactNode, type RefObject } from "react";
import { api, ApiError, queryClient } from "./api/client";
import type { DirEntry, DirListing } from "./api/types.gen";
import { nativeAt, placeMention } from "./chat/at";
import { newChannelAt } from "./create";
import { openChat } from "./route";
import { size } from "./chat/files";
import { previewKind } from "./chat/preview-lib";
import { Choose } from "./choose";
import { useMachines } from "./data";
import { t } from "./i18n";
import { MenuItems, type MenuItem } from "./sidebar";
import { errText, toast } from "./toast";
import { cn, IconKey, Tip, useLongPress, useMenuAt, usePointAnchor, MenuPortal } from "./ui";

const preview = () => import("./chat/preview");
const PreviewBody = lazy(preview);
const filesQ = (machine: string, path: string) => ({
  queryKey: ["browse-files", machine, path],
  queryFn: ({ signal }: { signal: AbortSignal }) => api.browseFiles(path, machine, signal),
  staleTime: 10_000,
  retry: false,
});

/** Prefetch the listing the browser will open at; an empty path asks for the starting folder. */
export const prefetchDirectory = (machine: string, path: string) => queryClient.prefetchQuery(filesQ(machine, path));

const join = (dir: string, name: string) => dir.endsWith("/") ? dir + name : `${dir}/${name}`;

const fileDownload = (machine: string, path: string, name: string) => ({
  href: api.browseFileURL(path, machine, true),
  download: name,
});

async function copyText(text: string) {
  try {
    await navigator.clipboard.writeText(text);
    toast(t("toast.copied"));
  } catch (e) {
    toast(t("err.action"), { error: true, description: errText(e) });
  }
}

/** Parent of a slash-separated page path, "" at a filesystem, drive (C:/) or UNC share root. */
function parent(dir: string) {
  if (!dir.startsWith("/") && !/^[A-Za-z]:\//.test(dir)) return "";
  const path = dir.replace(/\/+$/, "");
  if (!path || /^[A-Za-z]:$/.test(path) || /^\/\/[^/]+\/[^/]+$/.test(path)) return "";
  const up = path.slice(0, path.lastIndexOf("/")) || "/";
  return /^[A-Za-z]:$/.test(up) ? up + "/" : up;
}

/** Ancestor paths from root to dir for the breadcrumb buttons, stopping at parent()'s root. */
function pathTrail(dir: string) {
  const parts: string[] = [];
  for (let folder = dir; folder; folder = parent(folder)) parts.push(folder);
  return parts.reverse();
}

/** Split-pane folder list and file preview; below 620px only one pane shows at a time. */
/** within: the folder it does not leave (no computer to change, nothing above it in the trail,
 *  no ..), opened to make a subfolder: its 新建文件夹 row open; workspace: the group a channel
 *  made by 在此新建 goes in. */
export default function FileBrowser({ machine: initialMachine, path: initialPath, within, workspace }: { machine: string; path: string; within?: string; workspace?: number }) {
  const machines = useMachines(true);
  const [machine, setMachine] = useState(initialMachine);
  const [path, setPath] = useState(initialPath);
  const [typed, setTyped] = useState<string | null>(null);
  const [selected, setSelected] = useState<string | null>(null);
  const folders = useQuery(filesQ(machine, path));
  // Derive the open file from the listing so a refresh picks up a new version or drops a deleted
  // file; if a refresh fails, the last listing stays cached so the open preview is kept.
  const file = useMemo(() => selected ? folders.data?.entries.find((entry) => entry.name === selected && entry.type === "file") : undefined, [folders.data, selected]);
  const previewOpen = !!file;
  if (selected && folders.data && !file) setSelected(null);
  const computer = machines.find((m) => m.id === machine);
  const computers = [
    { value: "", label: t("term.local") },
    ...machines.filter((m) => m.id).map((m) => ({
      value: m.id,
      label: m.online ? m.name : t("new.offline", { name: m.name }),
      disabled: !m.online,
    })),
  ];
  // Keep the opening computer in the switcher even if it is absent from the machine list.
  if (machine && !computer) computers.splice(1, 0, { value: machine, label: machine });
  const here = folders.data?.path ?? path;
  // The folder kept in, as the computer itself writes it (its first listing's path).
  const rootAs = useRef<string | undefined>(undefined);
  if (within && rootAs.current === undefined && folders.data && path === initialPath) rootAs.current = folders.data.path.replaceAll("\\", "/");
  const root = within ? (rootAs.current ?? within) : undefined;
  const inside = useCallback((p: string) => !root || p === root || p.startsWith(root.replace(/\/+$/, "") + "/"), [root]);
  // A folder being named (新建文件夹), its row at the list's head.
  const [naming, setNaming] = useState<string | null>(within ? "" : null);
  const [making, setMaking] = useState(false);
  // Normalize Windows separators to '/' so parent() and join() work uniformly.
  const dir = computer?.sep === "\\" ? here.replaceAll("\\", "/") : here;
  const list = useRef<HTMLDivElement>(null);
  const title = useRef<HTMLHeadingElement>(null);
  const returnTo = useRef<HTMLButtonElement | null>(null);

  const visit = useCallback((next: string, at = machine) => {
    returnTo.current = null;
    setTyped(null);
    setSelected(null);
    setNaming(null);
    if (root) return setPath(next && inside(next.replaceAll("\\", "/")) ? next : root);
    setMachine(at);
    setPath(next);
  }, [inside, machine, root]);
  const make = () => {
    const name = naming?.trim();
    if (!name || name === "." || name === ".." || /[/\\\0]/.test(name) || making) return;
    setMaking(true);
    void api
      .mkdir({ path: dir, name, ...(machine ? { machine } : {}) })
      .then(
        () => (void queryClient.invalidateQueries({ queryKey: ["browse-files", machine] }), visit(join(dir, name))),
        (e) => toast(t("err.mkdir"), { error: true, description: errText(e) }),
      )
      .finally(() => setMaking(false));
  };
  const openFolder = useCallback((entry: BrowserEntry, fromKeyboard: boolean) => {
    // Keep focus on .. across climbs; hand it to the pane if that button is about to disappear.
    if (fromKeyboard && (entry.name !== ".." || entry.path === root || !parent(entry.path))) {
      list.current?.focus({ preventScroll: true });
    }
    visit(entry.path);
  }, [root, visit]);
  const openFile = useCallback((name: string, from: HTMLButtonElement) => {
    returnTo.current = from;
    setSelected(name);
  }, []);
  const copyChatLink = (path: string) => void copyText(placeMention({ machine, path }, machines));
  // A folder's chat link names its machine too; an absolute path is plain, as that machine writes it.
  const copyActions = (path: string, directory: boolean): MenuItem[] => [
    ...(directory ? [{ icon: <Link2 size={15} />, label: t("browse.copyChatLink"), run: () => copyChatLink(path) }] : []),
    { icon: <Copy size={15} />, label: t("browse.copyAbsolutePath"), run: () => void copyText(nativeAt(computer, path)) },
  ];
  // On a narrow pane the preview hides the list: focus the heading, and return to the file's row
  // when going back.
  useLayoutEffect(() => {
    if (previewOpen && list.current?.getClientRects().length === 0) title.current?.focus();
    if (!previewOpen && returnTo.current) {
      const key = returnTo.current;
      returnTo.current = null;
      if (key.isConnected) key.focus();
    }
  }, [previewOpen]);

  return (
    <>
      <header className="flex h-12 shrink-0 items-center gap-2 border-b border-edge px-3 pointer-coarse:h-13">
        {file && <IconKey label={t("browse.toFolder")} className="@min-[620px]:hidden" onClick={() => setSelected(null)}><ArrowLeft size={17} /></IconKey>}
        {/* Its name an icon, the one of the key that opens it (kumo, 2026-11-25); said to a reader. */}
        <Dialog.Title className="flex min-w-0 flex-1 items-center text-sm font-semibold">
          {within ? t("new.subFolder") : <span className="sr-only">{t("browse.title")}</span>}
        </Dialog.Title>
        {!within && <Choose
          label={t("device.computer")}
          options={computers}
          value={machine}
          onChange={(id) => visit("", id)}
          className="pointer-coarse:h-10"
        />}
      </header>
      <div className={cn("shrink-0 items-center gap-1 border-b border-edge px-2 py-1", file ? "hidden @min-[620px]:flex" : "flex")}>
        <IconKey label={t("browse.start")} onClick={() => visit(root ?? "")}><House size={15} /></IconKey>
        <PathBar dir={dir} machine={machine} typed={typed} onType={setTyped} onVisit={visit} inside={inside} />
        <IconKey label={t("pick.newFolder")} disabled={!folders.data?.path} onClick={() => setNaming((n) => (n === null ? "" : null))}><FolderPlus size={15} /></IconKey>
        <IconKey label={t("top.reload")} onClick={() => void folders.refetch()}><RefreshCw size={15} /></IconKey>
        <IconKey label={t("browse.copyChatLink")} disabled={!folders.data?.path} onClick={() => copyChatLink(dir)}><Link2 size={15} /></IconKey>
      </div>
      <div className="relative flex min-h-0 flex-1">
        {/* Pull the focus ring inside the pane so the dialog's overflow-hidden rounded edge does not clip it. */}
        <div ref={list} tabIndex={-1} className={cn("min-w-0 flex-1 flex-col focus-visible:-outline-offset-6 @min-[620px]:flex @min-[620px]:w-72 @min-[620px]:flex-none @min-[620px]:border-r @min-[620px]:border-edge", file ? "hidden" : "flex")}>
          <FolderContents machine={machine} dir={dir} top={dir === root} folders={folders} selected={file?.name} onFolder={openFolder} onFile={openFile} copyActions={copyActions}>
            {naming !== null && (
              <form className={cn(folderRowClass, "bg-fill")} onSubmit={(e) => (e.preventDefault(), make())}>
                {making ? <LoaderCircle size={17} className="shrink-0 animate-spin text-ink-3" /> : <FolderPlus size={17} className="shrink-0 text-ink-2" />}
                <input
                  autoFocus
                  aria-label={t("pick.newFolder")}
                  placeholder={t("pick.newFolder")}
                  value={naming}
                  onChange={(e) => setNaming(e.currentTarget.value)}
                  onKeyDown={(e) => {
                    if (e.key !== "Escape") return;
                    // Escape shuts the row, not the dialog.
                    e.preventDefault();
                    e.stopPropagation();
                    setNaming(null);
                  }}
                  spellCheck={false}
                  autoComplete="off"
                  enterKeyHint="done"
                  className="min-w-0 flex-1 bg-transparent text-sm text-ink outline-none placeholder:text-ink-4"
                />
              </form>
            )}
          </FolderContents>
        </div>
        <div className={cn("min-w-0 flex-1 flex-col @min-[620px]:flex", file ? "flex" : "hidden")}>
          <FilePreview file={file} dir={dir} machine={machine} title={title} />
        </div>
        {/* 在此新建 (kumo, 2026-11-25), at the window's lower right while no file is looked at: a
            channel in the folder shown, at the computer shown; the folder is its group. */}
        {!file && (
          <Dialog.Close
            disabled={!folders.data?.path || (!!machine && !computer?.online)}
            onClick={() => void newChannelAt(nativeAt(computer, dir), machine || undefined, workspace).then((id) => id && openChat(id))}
            className="g-press absolute right-3 bottom-3 flex h-9 items-center gap-1.5 rounded-full bg-ink pr-3.5 pl-2.5 text-sm font-medium text-on-ink shadow-lg disabled:opacity-40 pointer-coarse:h-11"
          >
            <Plus size={16} />
            {t("browse.newHere")}
          </Dialog.Close>
        )}
      </div>
    </>
  );
}

/** Editable breadcrumb bar: clickable ancestor segments, with trailing space opening a path input
 *  (Enter visits, Escape cancels without closing the dialog). */
function PathBar({ dir, machine, typed, onType, onVisit, inside }: {
  inside: (path: string) => boolean;
  dir: string;
  machine: string;
  typed: string | null;
  onType: (path: string | null) => void;
  onVisit: (path: string) => void;
}) {
  const trail = useRef<HTMLElement>(null);
  const editKey = useRef<HTMLButtonElement>(null);
  const returnToEdit = useRef(false);
  const editing = typed !== null;
  const bar = "h-8 min-w-0 flex-1 rounded-md bg-fill pointer-coarse:h-10";

  useLayoutEffect(() => {
    if (editing) return;
    // Keep the current segment scrolled into view when the trail overflows the bar.
    if (trail.current) trail.current.scrollLeft = trail.current.scrollWidth;
    if (returnToEdit.current) {
      returnToEdit.current = false;
      editKey.current?.focus({ preventScroll: true });
    }
  }, [dir, machine, editing]);

  if (editing) return (
    <input
      autoFocus
      aria-label={t("pick.typePath")}
      value={typed}
      onFocus={(e) => e.currentTarget.select()}
      onChange={(e) => onType(e.currentTarget.value)}
      onBlur={() => onType(null)}
      onKeyDown={(e) => {
        if (e.nativeEvent.isComposing || e.keyCode === 229) return;
        if (e.key !== "Enter" && e.key !== "Escape") return;
        e.preventDefault();
        // Stop propagation so Escape cancels editing rather than closing the dialog.
        e.stopPropagation();
        returnToEdit.current = true;
        if (e.key === "Enter") onVisit(typed || dir);
        else onType(null);
      }}
      spellCheck={false}
      autoComplete="off"
      enterKeyHint="go"
      className={cn(bar, "g-fade px-2 font-mono text-xs text-ink")}
    />
  );

  return (
    <div className={cn(bar, "flex items-center px-1")}>
      <nav ref={trail} aria-label={t("pick.path")} className="no-bar min-w-0 overflow-x-auto overscroll-x-contain">
        <ol role="list" className="m-0 flex w-max list-none items-center p-0">
          {pathTrail(dir).filter(inside).map((folder, i) => {
            const current = folder === dir;
            const label = folder.replace(/(.)\/+$/, "$1");
            const warm = () => !current && void prefetchDirectory(machine, folder);
            return (
              <li key={folder} className="flex shrink-0 items-center">
                {i > 0 && <ChevronRight aria-hidden size={12} className="mx-0.5 text-ink-4" />}
                <button
                  type="button"
                  aria-current={current ? "location" : undefined}
                  aria-disabled={current || undefined}
                  onPointerEnter={warm}
                  onPointerDown={warm}
                  onFocus={warm}
                  onClick={() => !current && onVisit(folder)}
                  className={cn("g-press flex h-7 items-center rounded-sm px-1.5 text-sm whitespace-nowrap focus-visible:-outline-offset-2 pointer-coarse:h-9", current ? "font-semibold text-ink" : "text-ink-2 hover:bg-fill-2")}
                >
                  {i === 0 && !parent(folder) ? label : label.slice(label.lastIndexOf("/") + 1)}
                </button>
              </li>
            );
          })}
        </ol>
      </nav>
      <button
        ref={editKey}
        type="button"
        aria-label={t("pick.typePath")}
        onClick={() => onType(dir)}
        className="h-full min-w-8 flex-1 cursor-text rounded-md focus-visible:-outline-offset-2 pointer-coarse:min-w-10"
      />
    </div>
  );
}

const folderRowClass = "flex h-9 w-full items-center gap-2.5 rounded-md px-2.5 text-left text-sm pointer-coarse:h-11";

type FolderContentsProps = {
  machine: string;
  dir: string;
  /** dir is the folder the browser is kept in: no .. above it. */
  top?: boolean;
  folders: UseQueryResult<DirListing>;
  selected?: string;
  onFolder: (entry: BrowserEntry, fromKeyboard: boolean) => void;
  onFile: (name: string, from: HTMLButtonElement) => void;
  copyActions: (path: string, directory: boolean) => MenuItem[];
  children: ReactNode;
};

/** What stands in for a folder's entries while it loads, fails or is empty. */
function FolderStatus({ folders }: { folders: UseQueryResult<DirListing> }) {
  if (folders.isPending) return <Loading />;
  if (folders.isError) {
    let error = t("msg.unreadable");
    if (folders.error instanceof ApiError) {
      const code = (folders.error.body as { code?: string } | undefined)?.code;
      if (folders.error.status === 403) error = t("channel.forbidden");
      else if (code === "scoped_files_unsupported") error = t("browse.updateComputer");
    }
    return <p role="status" className="mx-2.5 my-3 text-sm text-ink-2">{error}</p>;
  }
  if (!folders.data?.entries.length) return <p className="mx-2.5 my-3 text-sm text-ink-3">{t("browse.empty")}</p>;
  return null;
}

type Point = { x: number; y: number };
/** One row in the folder: path is the real path it acts on (the parent itself for ..). */
type BrowserEntry = Pick<DirEntry, "name" | "type"> & { path: string };

const byName = new Intl.Collator(undefined, { numeric: true });
const sortedListings = new WeakMap<DirListing, DirEntry[]>();
const noEntries: DirEntry[] = [];

/** Directories first, then natural name order; cached per DirListing reference so reopening a
 *  cached folder does not re-sort large listings. */
function sortedEntries(listing: DirListing) {
  let entries = sortedListings.get(listing);
  if (!entries) {
    entries = listing.entries.slice().sort((a, b) => Number(b.type === "dir") - Number(a.type === "dir") || byName.compare(a.name, b.name));
    sortedListings.set(listing, entries);
  }
  return entries;
}

/** Extra rows rendered above and below the viewport to cover fast scrolls. */
const overscan = 8;
/** Fallback viewport height before ResizeObserver fires, so the opening frame draws full. */
const initialHeight = 640;

/** Estimated row height matching h-9 (2.25rem) and pointer-coarse:h-11 (2.75rem) until measured. */
function estimatedRowHeight() {
  const rem = parseFloat(getComputedStyle(document.documentElement).fontSize) || 16;
  return rem * (window.matchMedia("(pointer: coarse)").matches ? 2.75 : 2.25);
}

/** Virtualized fixed-height folder list and its shared row menu: .. stands first with the parent's
 *  real path so opening, copying and the menu act on it, staying usable while the rest loads or
 *  fails. */
function FolderContents({ machine, dir, top, folders, selected, onFolder, onFile, copyActions, children }: FolderContentsProps) {
  const { data, isSuccess } = folders;
  const entries = isSuccess ? sortedEntries(data) : noEntries;
  const up = top ? "" : parent(dir);
  const parentCount = up ? 1 : 0;
  const count = entries.length + parentCount;
  const scroll = useRef<HTMLDivElement>(null);
  const naming = useRef<HTMLDivElement>(null);
  const rows = useRef<HTMLUListElement>(null);
  const firstRow = useRef<HTMLLIElement | null>(null);
  const observer = useRef<ResizeObserver | null>(null);
  const frame = useRef<number | null>(null);
  const [focused, setFocused] = useState<string | null>(null);
  const [view, setView] = useState(() => {
    const rowHeight = estimatedRowHeight();
    return { machine, dir, rowHeight, start: 0, end: Math.ceil(initialHeight / rowHeight) + overscan };
  });
  const measured = useRef(view.rowHeight);
  const viewport = useRef(initialHeight);
  // Reset the rendered range as well as the DOM scroll, before React can remove the focused .. row.
  if (view.machine !== machine || view.dir !== dir) {
    setView({ ...view, machine, dir, start: 0, end: Math.ceil(viewport.current / view.rowHeight) + overscan });
    if (focused && (focused !== ".." || !up)) setFocused(null);
  }
  const updateView = useCallback(() => {
    const pane = scroll.current;
    const list = rows.current;
    if (!pane || !list || !pane.clientHeight) return; // A narrow preview hides the list until Back.
    viewport.current = pane.clientHeight;
    const rowHeight = measured.current;
    // Subtract the list's offset so the expanding new-folder row above it does not shift indices.
    const top = pane.scrollTop - list.offsetTop;
    const start = Math.max(0, Math.floor(top / rowHeight) - overscan);
    const end = Math.ceil((top + viewport.current) / rowHeight) + overscan;
    setView((v) => v.start === start && v.end === end && v.rowHeight === rowHeight ? v : { ...v, start, end, rowHeight });
  }, []);
  // Observe whichever row currently renders first so zoom or pointer changes update rowHeight.
  const observeRow = useCallback((row: HTMLLIElement | null) => {
    if (firstRow.current) observer.current?.unobserve(firstRow.current);
    firstRow.current = row;
    if (row) observer.current?.observe(row);
  }, []);
  useLayoutEffect(() => {
    const pane = scroll.current!;
    const resize = new ResizeObserver(() => {
      if (!pane.clientHeight) return;
      // The CSS height preserves fractional rem sizes and ignores the dialog's opening scale.
      const height = firstRow.current ? parseFloat(getComputedStyle(firstRow.current).height) : 0;
      measured.current = height > 0 ? height : estimatedRowHeight();
      updateView();
    });
    observer.current = resize;
    resize.observe(pane);
    // Track the new-folder row's height transition because it shifts list.offsetTop.
    resize.observe(naming.current!);
    if (firstRow.current) resize.observe(firstRow.current);
    return () => {
      observer.current = null;
      resize.disconnect();
      if (frame.current !== null) cancelAnimationFrame(frame.current);
      frame.current = null;
    };
  }, [updateView]);

  const [at, setAt] = useMenuAt();
  const [target, setTarget] = useState<{ entry: BrowserEntry; key: HTMLButtonElement } | null>(null);
  const anchor = usePointAnchor(at);
  // Return to the top and shut any open row menu when changing folder or computer.
  useLayoutEffect(() => {
    scroll.current!.scrollTop = 0;
    setAt(null);
    updateView();
  }, [dir, machine, setAt, updateView]);
  // Coalesce scroll events to one range update per frame and close any menu whose anchor scrolled.
  const onScroll = () => {
    if (at) setAt(null);
    if (frame.current !== null) return;
    frame.current = requestAnimationFrame(() => {
      frame.current = null;
      updateView();
    });
  };
  // Shut an open menu if a refresh drops its row.
  useLayoutEffect(() => {
    if (at && target && !target.key.isConnected) setAt(null);
  });
  const openMenu = useCallback((entry: BrowserEntry, key: HTMLButtonElement, point: Point) => {
    setTarget({ entry, key });
    setAt(point);
  }, [setAt]);
  // Pin the focused row (so scrolling away does not drop focus to body) and the preview's return
  // target (so Back on a narrow pane finds its button), kept in original DOM order for Tab flow.
  const kept = useMemo(() => [focused, selected].flatMap((name) => {
    if (!name) return [];
    if (name === ".." && up) return [0];
    const index = entries.findIndex((entry) => entry.name === name);
    return index < 0 ? [] : [index + parentCount];
  }), [entries, focused, selected, up, parentCount]);
  const indices = new Set(kept);
  for (let i = view.start; i < Math.min(view.end, count); i++) indices.add(i);
  const visible = [...indices].sort((a, b) => a - b);

  const items: MenuItem[] = [];
  if (target) {
    const { entry, key } = target;
    const { path } = entry;
    const directory = entry.type === "dir";
    if (directory) {
      items.push({ icon: <FolderOpen size={15} />, label: t("toast.open"), run: () => onFolder(entry, true) });
    } else {
      items.push(
        { icon: <Eye size={15} />, label: t("msg.preview"), run: () => onFile(entry.name, key) },
        { icon: <Download size={15} />, label: t("msg.download"), ...fileDownload(machine, path, entry.name) },
      );
    }
    items.push(...copyActions(path, directory));
  }
  return (
    <>
      {/* overflow-anchor:none stops scroll anchoring from jumping as virtual rows mount/unmount. */}
      <div ref={scroll} onScroll={onScroll} className="relative min-h-0 flex-1 overflow-auto overscroll-contain p-1.5 [overflow-anchor:none]">
        {/* New folder takes up room before the list, outside the virtual rows. */}
        <div ref={naming} className={cn("grid transition-[grid-template-rows] duration-(--dur-3) ease-(--ease-out) motion-reduce:transition-none", children ? "grid-rows-[1fr]" : "grid-rows-[0fr]")}>
          <div className="min-h-0 overflow-hidden">{children}</div>
        </div>
        <ul ref={rows} role="list" hidden={!count} className="relative m-0 list-none p-0" style={{ height: count * view.rowHeight }}>
          {visible.map((index, i) => {
            const isParent = index < parentCount;
            const { name, type } = isParent ? { name: "..", type: "dir" } : entries[index - parentCount]!;
            const path = isParent ? up : join(dir, name);
            return (
              <FolderRow
                key={isParent ? ".." : `${machine}:${path}`}
                rowRef={i === 0 ? observeRow : undefined}
                name={name}
                type={type}
                path={path}
                index={index}
                count={count}
                offset={index * view.rowHeight}
                machine={machine}
                selected={selected === name}
                onFolder={onFolder}
                onFile={onFile}
                onMenu={openMenu}
                onFocusName={setFocused}
              />
            );
          })}
        </ul>
        <FolderStatus folders={folders} />
      </div>
      <Menu.Root modal={false} open={at !== null} onOpenChange={(open) => !open && setAt(null)}>
        <MenuPortal>
          <Menu.Positioner anchor={anchor} side="bottom" align="start" sideOffset={4} collisionPadding={8} className="z-50">
            <MenuItems
              items={items}
              label={target?.entry.name}
              // When Esc shuts the menu, put the focus back on the row if it is still on screen.
              finalFocus={(type) => type === "keyboard" && target?.key.getClientRects().length ? target.key : false}
            />
          </Menu.Positioner>
        </MenuPortal>
      </Menu.Root>
    </>
  );
}

/** One virtualized row: memoized with primitive props so typing in the path or new-folder input,
 *  or switching previews, skips untouched rows. Warmed on hover or focus; its menu opens on right
 *  click, long press or menu key. */
const FolderRow = memo(function FolderRow({ name, type, path, index, count, offset, rowRef, machine, selected, onFolder, onFile, onMenu, onFocusName }: BrowserEntry & Pick<FolderContentsProps, "machine" | "onFolder" | "onFile"> & {
  index: number;
  count: number;
  offset: number;
  rowRef?: (row: HTMLLIElement | null) => void;
  selected: boolean;
  onMenu: (entry: BrowserEntry, key: HTMLButtonElement, point: Point) => void;
  onFocusName: (name: string | null) => void;
}) {
  const entry = { name, type, path };
  const key = useRef<HTMLButtonElement>(null);
  const lp = useLongPress((point) => key.current && onMenu(entry, key.current, point), { suppressTouchClick: true });
  const directory = entry.type === "dir";
  const Icon = directory ? Folder : entry.type === "link" ? Link2 : FileText;
  const label = (
    <>
      <Icon size={16} aria-hidden className="shrink-0 text-ink-3" />
      <span className="no-bar min-w-0 flex-1 overflow-x-auto whitespace-nowrap">{entry.name}</span>
    </>
  );
  const warm = () => directory ? void prefetchDirectory(machine, entry.path) : void preview().catch(() => {});
  return (
    <li ref={rowRef} aria-setsize={count} aria-posinset={index + 1} className="absolute inset-x-0 top-0" style={{ transform: `translateY(${offset}px)` }}>
      {!directory && entry.type !== "file" ? (
        <span className={cn(folderRowClass, "text-ink-3")}>
          {label}{entry.type === "link" && <span className="text-xs">{t("browse.link")}</span>}
        </span>
      ) : (
        <button
          ref={key}
          type="button"
          aria-label={name === ".." ? t("browse.up") : undefined}
          aria-current={selected || undefined}
          aria-keyshortcuts="Shift+F10"
          className={cn(folderRowClass, "hover:bg-fill select-none aria-[current=true]:bg-fill aria-[current=true]:font-medium [-webkit-touch-callout:none]")}
          {...lp}
          onPointerEnter={warm}
          onPointerDown={(e) => {
            lp.onPointerDown(e);
            warm();
          }}
          onFocus={() => {
            onFocusName(name);
            warm();
          }}
          onBlur={() => onFocusName(null)}
          onClick={(e) => directory ? onFolder(entry, e.detail === 0) : onFile(name, e.currentTarget)}
          onContextMenu={(e) => {
            if (lp.onContextMenu(e)) return;
            e.preventDefault();
            onMenu(entry, e.currentTarget, { x: e.clientX, y: e.clientY });
          }}
          onKeyDown={(e) => {
            if (e.key !== "ContextMenu" && !(e.shiftKey && e.key === "F10")) return;
            e.preventDefault();
            const { left, bottom } = e.currentTarget.getBoundingClientRect();
            onMenu(entry, e.currentTarget, { x: left, y: bottom });
          }}
        >
          {label}
        </button>
      )}
    </li>
  );
});

/** Selected file's header, download link and preview body (image, PDF tab link, or PreviewBody). */
function FilePreview({ file, dir, machine, title }: {
  file?: DirEntry;
  dir: string;
  machine: string;
  title: RefObject<HTMLHeadingElement | null>;
}) {
  if (!file) return <div className="grid flex-1 place-items-center text-sm text-ink-3">{t("browse.noSelection")}</div>;

  const filePath = join(dir, file.name);
  // Version the preview URL by mtime-size so a rewritten file fetches fresh bytes and remounts.
  const url = api.browseFileURL(filePath, machine, false, `${file.mtime}-${file.size}`);
  const kind = previewKind(file.name) ?? "text";
  const image = /\.(?:avif|bmp|gif|jpe?g|png|webp)$/i.test(file.name);
  return (
    <>
      <div className="flex shrink-0 items-center gap-2 border-b border-edge px-3 py-1">
        <h2 ref={title} tabIndex={-1} className="no-bar m-0 min-w-0 flex-1 overflow-x-auto text-sm font-medium whitespace-nowrap outline-none">{file.name}</h2>
        <span className="shrink-0 text-xs text-ink-3 tabular-nums">{size(file.size)}</span>
        <Tip label={t("msg.download")}>
          <a
            className="g-press grid size-8 shrink-0 place-items-center rounded-md text-ink-2 hover:bg-fill hover:text-ink pointer-coarse:size-10"
            aria-label={t("msg.download")}
            {...fileDownload(machine, filePath, file.name)}
          >
            <Download size={16} />
          </a>
        </Tip>
      </div>
      <div className="min-h-0 flex-1 overflow-auto overscroll-contain" key={url}>
        {image ? <ImagePreview url={url} name={file.name} /> : kind === "pdf" ? (
          <div className="grid h-full place-items-center">
            <a href={url} target="_blank" rel="noreferrer" className="g-press inline-flex items-center gap-2 rounded-md bg-fill px-3 py-2 text-sm hover:bg-fill-2">
              <ExternalLink size={15} />{t("msg.openTab")}
            </a>
          </div>
        ) : <Suspense fallback={<Loading />}><PreviewBody url={url} name={file.name} kind={kind} /></Suspense>}
      </div>
    </>
  );
}

function Loading() {
  return <div className="grid min-h-32 place-items-center text-ink-3" role="status" aria-label={t("msg.loading")}><LoaderCircle size={18} className="animate-spin" /></div>;
}

function ImagePreview({ url, name }: { url: string; name: string }) {
  const [failed, setFailed] = useState(false);
  if (failed) return <p role="status" className="px-4 text-sm text-ink-3">{t("msg.noPreview")}</p>;
  return <img src={url} alt={name} onError={() => setFailed(true)} className="mx-auto block max-h-full max-w-full object-contain p-3" />;
}
