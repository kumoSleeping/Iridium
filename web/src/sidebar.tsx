// The one list (plan "网页", "风格"; web/sample's design): group → folder → session, a group the
// folder its sessions are in (no workspaces to make or delete: the user, 2026-11-08). No panel, no
// cards, no headings; groups split by space, chevrons in their own column, colour only in logos and
// lamps, the selected row only bolder. A session's row is one line (the user, 2026-11-08): its name
// (the title its agent gave it, plan "会话标题") and, where the tree does not say it, its folder; out
// of a turn, it ends with how long ago it was. 归档 folded at the bottom, its bin where a group's
// folder is. A group's row: its name a size up, a faint "+" at its end for a new session there. A
// row's menu (right click, long-press): pin, rename, stop, archive, 通知 while push is on (push.ts),
// and at its foot the colour tags (colors.ts); a group's: rename, its path, 通知. The mouse resting
// on a row fetches its chat ahead.
import { Menu } from "@base-ui/react/menu";
import { Archive, ArchiveRestore, ArrowDown, ArrowUp, Bot, Check, ChevronRight, Copy, Ellipsis, Ungroup, Folder, FolderOpen, ListCollapse, Moon, Pencil, Pin, PinOff, Plus, RefreshCw, Square } from "lucide-react";
import { ConfirmIcon, confirmName, confirmTitle } from "./confirmname";
import { Fragment, lazy, memo, Suspense, useCallback, useEffect, useLayoutEffect, useRef, useState, type CSSProperties, type ReactElement, type ReactNode, type RefObject } from "react";
import { api } from "./api/client";
import type { Agent, Color, Guilds, Need, Session } from "./api/types.gen";
import { NO_GUILDS, placesOf, withGroupOrder, withMark, type Places } from "./guilds";
import { colorVar, COLORS, useFolderColors } from "./colors";
import { ahead, behind } from "./chats";
import { t, useLang } from "./i18n";
import { useMoves } from "./motion";
import { folderLevels, sessionLevels, usePushMenus, wsLevels, type PushLevel } from "./push";
import { allFolders, folderSessions, foldersHolding, foldMore, groupHolding, machineOf, remotePath, type Folder as FolderNode, type Tree, type WsNode } from "./tree";
import { createAt, onOpenPlace, type Place } from "./places";
import { LinkDot } from "./link";
import { errText, toast } from "./toast";
import { manages, useMachines, useMe, useSaidLine, useUpdate } from "./data";
import { agentName, ago, cn, fine, IconKey, IrMark, keep, kept, Lamp, lampOf, Logo, own, sessionStatusText, statusText, tilde, Tip, titleOf, untitled, useLongPress, useMenuAt, useMinute, usePointAnchor } from "./ui";
import { PLACE_DRAG, SESSION_DRAG } from "./chat/refs";
import { badge, isQuiet, setQuiet, useAt, useQuiet, useUnread } from "./unread";
import { Face, FaceStack } from "./chat/channel/face";
import { CloudBadge, MachineFace, shownPath, useLate, useMachine, type Machine } from "./machines";
import { getCarried, setCarried, useCarried, useLink, useList } from "./store";
import { channelLamp, isMulti } from "./chat/channel/lib";
import { useVoiceItem, VoiceDock, VoiceMark } from "./voice/dock";
import { dropMark, dropTarget, endDrag, listDrop, pinFolder, placeFolder, placeGroup as placeGroupKey, placeSession, sortList, startDrag, useDragged, useOver, type Dragged, type Edge, type Over } from "./reorder";

const STEP = 18;
/** The deepest a folder is drawn in: those deeper are drawn as deep, the sidebar being narrow. */
const MAX_IN = 3;
const BUSY = new Set(["running", "compacting", "starting"]);
/** While its agent works, an approval awaited too: then it may be stopped (the user, 2026-11-06). */
const WORKING = new Set([...BUSY, "waiting"]);

/** How many of 归档's sessions are drawn at a time. */
const ARCHIVE_STEP = 50;

type Props = {
  tree: Tree;
  expanded: (key: string) => boolean;
  toggle: (key: string) => void;
  /** How many of a folder's folded sessions were opened beyond the first few (app.tsx useExpanded). */
  more: (key: string) => number;
  showMore: (key: string) => void;
  foldBack: (keys: string[]) => void;
  selected: string;
  onSelect: (id: string) => void;
  phone: boolean;
  /** The jump box open: drawn here, under the head, in the tree's place (the user, 2026-11-06). */
  searching: boolean;
  search: ReactNode;
  onSearch: () => void;
  /** What agents' ir asked this panel for (invites, tokens): under 需要你 too. */
  confirms: Need[];
  onConfirm: (c: Need) => void;
  createKey: (trigger: ReactElement) => ReactNode;
  wsPlus: (ws: WsNode, trigger: ReactElement, open: boolean, setOpen: (o: boolean) => void) => ReactNode;
  me: ReactNode;
  /** The chat's code, fetched as a row is pressed. */
  chatCode: () => void;
  /** The settings' and the jump box's code and answers, fetched as a pointer comes on their keys. */
  panelsCode: () => void;
  /** Without its foot, me and the settings key: on a phone they are a sheet of their own. */
  noFoot?: boolean;
  /** On a phone: the sheet brought out, a place shown in it (openPlace). */
  reveal?: () => void;
};

/** How wide the sidebar may be dragged, and at first: freely, to 640px or 60% of the window (the
 *  user, 2026-11-06). The width is --sidebar-w on the page too: the settings dialog sizes by it. */
const WIDTH = { min: 220, max: 640, start: 292 };
const WIDTH_KEY = "ir.sidebarWidth";
const clampWidth = (w: number) => Math.round(Math.min(WIDTH.max, innerWidth * 0.6, Math.max(WIDTH.min, w)));

/** The line to the main pane, fading out toward its top and its foot; dragged, it sets how wide
 *  the sidebar is, within WIDTH; a double click puts it back; ← → by a step on the keyboard. */
function Divider({ width, setWidth }: { width: number; setWidth: (w: number) => void }) {
  const [dragging, setDragging] = useState(false);
  const from = useRef({ x: 0, w: 0 });
  const set = (w: number, done: boolean) => {
    const v = clampWidth(w);
    setWidth(v);
    if (done) keep(WIDTH_KEY, String(v));
  };
  return (
    <div
      role="separator"
      aria-orientation="vertical"
      aria-label={t("nav.resize")}
      aria-valuemin={WIDTH.min}
      aria-valuemax={WIDTH.max}
      aria-valuenow={width}
      tabIndex={0}
      onPointerDown={(e) => {
        if (e.button !== 0) return;
        e.preventDefault();
        e.currentTarget.setPointerCapture(e.pointerId);
        from.current = { x: e.clientX, w: width };
        setDragging(true);
      }}
      onPointerMove={(e) => dragging && set(from.current.w + e.clientX - from.current.x, false)}
      onPointerUp={(e) => {
        if (!dragging) return;
        setDragging(false);
        set(from.current.w + e.clientX - from.current.x, true);
      }}
      onPointerCancel={() => setDragging(false)}
      onDoubleClick={() => set(WIDTH.start, true)}
      onKeyDown={(e) => {
        const by = { ArrowLeft: -16, ArrowRight: 16 }[e.key];
        if (by) (e.preventDefault(), set(width + by, true));
      }}
      className="group absolute inset-y-0 -right-1 z-30 w-2 cursor-col-resize touch-none outline-none"
    >
      <span
        aria-hidden
        className={cn(
          "pointer-events-none absolute inset-y-0 right-1 w-px bg-[linear-gradient(to_bottom,transparent,var(--edge)_18%,var(--edge)_82%,transparent)] transition-[background] duration-(--dur-1)",
          "group-hover:bg-[linear-gradient(to_bottom,transparent,var(--ink-4)_18%,var(--ink-4)_82%,transparent)] group-focus-visible:bg-[linear-gradient(to_bottom,transparent,var(--ink-4)_18%,var(--ink-4)_82%,transparent)]",
          dragging && "bg-[linear-gradient(to_bottom,transparent,var(--ink-3)_18%,var(--ink-3)_82%,transparent)]",
        )}
      />
    </div>
  );
}

export function Sidebar(p: Props) {
  const machines = useMachines();
  saidNow = useSaidLine();
  const list = useRef<HTMLDivElement>(null);
  const [width, setWidth] = useState(() => clampWidth(Number(kept(WIDTH_KEY)) || WIDTH.start));
  useLayoutEffect(() => void document.documentElement.style.setProperty("--sidebar-w", `${p.phone ? 0 : width}px`), [width, p.phone]);
  const [plusOpen, setPlusOpen] = useState<string | null>(null);
  // A confirmation an agent's ir asked for lights its session's lamp; one of no session listed
  // here is a row of its own at the top (no 需要你 group: the user, 2026-11-08). The sessions a
  // session launched are not rows (kumo, 2026-11-15): theirs, and one of them waiting on you,
  // light the launcher's.
  // A member spawned for a channel (tree.ts memberOf) is not a row either: theirs light the channel's.
  const hungAt = new Map([...[...p.tree.hung].flatMap(([at, ss]) => ss.map((s) => [s.id, at] as const)), ...p.tree.member]);
  const listed = new Set([...p.tree.workspaces.flatMap((w) => w.rows.flatMap((r) => (r.kind === "session" ? [r.s.id] : folderSessions(r.f).map((s) => s.id)))), ...hungAt.keys()]);
  const asked = new Set(p.confirms.flatMap((c) => (c.session && listed.has(c.session) ? [hungAt.get(c.session) ?? c.session] : [])));
  for (const [at, ss] of p.tree.hung) if (ss.some((s) => lampOf(s) === "waiting")) asked.add(at);
  const loose = p.confirms.filter((c) => !c.session || !listed.has(c.session));
  const last = p.tree.workspaces.at(-1);
  const lastOpen = last ? p.expanded(last.key) && last.rows.length > 0 : loose.length > 0;
  // A folder, or a group's root, with many sessions: the first few and a row for the rest (tree.ts
  // foldRest), opened by hand; one open, pinned, at work or waiting on you shown wherever it is.
  const stays = (s: Session) => !!s.pinned || s.id === p.selected || asked.has(s.id) || BUSY.has(lampOf(s)) || lampOf(s) === "waiting";
  // Opened a few at a time (tree.ts foldMore; the user, 2026-11-22: not all at once).
  const fold = (ss: Session[], key: string) => {
    const f = foldMore(ss, stays, p.more(key));
    return { list: f.shown, rest: f.rest, less: f.less };
  };
  const roots = (w: WsNode) => w.rows.flatMap((r) => (r.kind === "session" ? [r.s] : []));
  const tops = (w: WsNode) => w.rows.flatMap((r) => (r.kind === "folder" ? [r.f] : []));
  const foldShape = (ss: Session[], key: string) => {
    const f = fold(ss, key);
    return `${f.list.map((s) => s.id)}${f.rest ? `+${f.rest}` : f.less ? "-" : ""}`;
  };
  // 折叠会话 in a group's or a folder's menu: what was opened of the folded sessions in it, and
  // in the folders in it, folded again; none when nothing is.
  const foldBackOf = (keys: string[]) => (keys.some((k) => p.more(k) > 0) ? () => p.foldBack(keys) : undefined);
  // 归档 drawn ARCHIVE_STEP at a time, more as asked: all of it at once (some hundreds) held the
  // page for most of a second as it opened (kumo, 2026-11-23). Folded, it starts again from the first.
  const [archShown, setArchShown] = useState(ARCHIVE_STEP);
  const archOpen = p.expanded("archive");
  useEffect(() => void (!archOpen && setArchShown(ARCHIVE_STEP)), [archOpen]);
  const shape = [loose.map((c) => c.id).join(","), ...p.tree.workspaces.map((w) => `${w.key}:${p.expanded(w.key)}:${foldShape(roots(w), w.key)}:${allFolders(tops(w)).map((f) => `${f.key}${p.expanded(f.key)}${foldShape(f.sessions, f.key)}`).join(",")}`), archOpen, p.tree.archived.length, archShown].join("|");
  useMoves(list, true)(shape);
  // Dragged to a new place (reorder.ts): the sessions beside each other, by the key of their
  // group's root or folder, and the groups, as last drawn. The page shows the new order at once.
  const lanes = useRef(new Map<string, Session[]>());
  lanes.current = new Map(p.tree.workspaces.flatMap((w) => [[w.key, roots(w)] as const, ...allFolders(tops(w)).map((f) => [f.key, f.sessions] as const)]));
  // A folder's siblings: those of its group's root, or of the folder it is in, by that one's key.
  const folderLanes = useRef(new Map<string, FolderNode[]>());
  folderLanes.current = new Map(p.tree.workspaces.flatMap((w) => [[w.key, tops(w)] as const, ...allFolders(tops(w)).map((f) => [f.key, f.children] as const)]));
  // The groups' rows, all machines', as listed (未归类 not among them).
  const groupKeys = useRef<string[]>([]);
  groupKeys.current = p.tree.workspaces.flatMap((w) => (w.id !== 0 ? [w.key] : []));
  const place = useCallback((id: string, target: Session, edge: Edge, lane: string) => {
    const sibs = lanes.current.get(lane) ?? [];
    const s = sibs.find((x) => x.id === id);
    if (!s) return;
    const at = placeSession(sibs, s, target, edge);
    const was = getCarried<Session[]>("list");
    const shown = was && sortList(was.map((x) => (x.id === id ? { ...x, ...at } : x)));
    if (shown) setCarried("list", shown);
    void api.patch(id, { order: at.order, ...(at.pinned !== !!s.pinned ? { pinned: at.pinned } : {}) }).catch((e: unknown) => {
      if (getCarried("list") === shown) setCarried("list", was);
      toast(t("err.action"), { error: true, description: errText(e) });
    });
  }, []);
  // A folder put (dragged, pinned): shown so at once, then told the server.
  const putFolder = useCallback((path: string, at: (was: Places) => { order?: number; pinned?: boolean }) => {
    const was = getCarried<Guilds>("guilds") ?? NO_GUILDS;
    const put = at(placesOf(was));
    const shown = withMark(was, path, put);
    setCarried("guilds", shown);
    void api.markFolder(path, put).catch((e: unknown) => {
      if (getCarried("guilds") === shown) setCarried("guilds", was);
      toast(t("err.action"), { error: true, description: errText(e) });
    });
  }, []);
  const placeFolderAt = useCallback((path: string, target: FolderNode, edge: Edge, lane: string) => {
    const sibs = folderLanes.current.get(lane) ?? [];
    const f = sibs.find((x) => x.path === path);
    if (f) putFolder(path, (was) => placeFolder(sibs, f, target, edge, was));
  }, [putFolder]);
  const pinFolderOn = useCallback((path: string, on: boolean) => putFolder(path, () => ({ pinned: on })), [putFolder]);
  // Dropped on a session at its group's root: pinned, last of the pinned, above them.
  const pinFolderAt = useCallback((path: string, lane: string) => {
    const sibs = folderLanes.current.get(lane) ?? [];
    const f = sibs.find((x) => x.path === path);
    if (f) putFolder(path, (was) => pinFolder(sibs, f, was));
  }, [putFolder]);
  // A group's row moved, by its key.
  const placeGroup = useCallback((key: string, target: string, edge: Edge) => {
    const was = getCarried<Guilds>("guilds") ?? NO_GUILDS;
    // Its place among the groups (未归类, w0, is always last).
    const keys = placeGroupKey(placesOf(was).groups, groupKeys.current, key, target, edge).filter((k) => k !== "w0");
    const shown = withGroupOrder(was, keys);
    setCarried("guilds", shown);
    void api.patchGuild(Number(key.slice(1)), { position: keys.indexOf(key) }).catch((e: unknown) => {
      if (getCarried("guilds") === shown) setCarried("guilds", was);
      toast(t("err.action"), { error: true, description: errText(e) });
    });
  }, []);
  const scrolled = useRef(0);
  useLayoutEffect(() => void (!p.searching && list.current && (list.current.scrollTop = scrolled.current)), [p.searching]);
  // A place pressed elsewhere (a card in a chat, places.ts): its group opened and scrolled to, its
  // folder too, nothing selected; one no group listed holds, the "+" opened there.
  const now = useRef({ tree: p.tree, expanded: p.expanded, toggle: p.toggle, reveal: p.reveal });
  now.current = { tree: p.tree, expanded: p.expanded, toggle: p.toggle, reveal: p.reveal };
  useEffect(
    () =>
      onOpenPlace((at: Place) => {
        const { tree, expanded, toggle, reveal } = now.current;
        const path = at.machine ? `${at.machine}:${at.path}` : at.path;
        const w = groupHolding(tree.workspaces, path);
        // On a phone the sheet comes out first: the "+" is on it.
        if (!w) return reveal?.(), createAt(at);
        // A folder in another one: those it is in opened too.
        const fs = foldersHolding(w, path);
        const f = fs.at(-1);
        if (!expanded(w.key)) toggle(w.key);
        for (const x of fs) if (!expanded(x.key)) toggle(x.key);
        reveal?.();
        requestAnimationFrame(() => list.current?.querySelector(`[data-flip="${CSS.escape(f ? `f:${f.key}` : `w:${w.key}`)}"]`)?.scrollIntoView({ block: "nearest", behavior: "smooth" }));
      }),
    [],
  );
  // The open session's row in sight as it is opened, or first listed (one just started, say): its
  // group and folders opened, the list scrolled to it. A session in a group far down was opened
  // and nowhere to be seen (kumo, 2026-11-23). A row already in sight stays where it is.
  const selListed = listed.has(p.selected) && !hungAt.has(p.selected);
  useEffect(() => {
    if (!selListed || p.searching) return;
    const { tree, expanded, toggle } = now.current;
    const w = tree.workspaces.find((x) => x.rows.some((r) => (r.kind === "session" ? r.s.id === p.selected : folderSessions(r.f).some((s) => s.id === p.selected))));
    if (!w) return;
    if (!expanded(w.key)) toggle(w.key);
    const fs = allFolders(w.rows.flatMap((r) => (r.kind === "folder" ? [r.f] : []))).filter((f) => folderSessions(f).some((s) => s.id === p.selected));
    for (const f of fs) if (!expanded(f.key)) toggle(f.key);
    requestAnimationFrame(() => list.current?.querySelector(`[data-flip="${CSS.escape(`s:${p.selected}`)}"]`)?.scrollIntoView({ block: "nearest", behavior: "smooth" }));
  }, [p.selected, selListed, p.searching]);
  // A session's row (its subagents have none: kumo, 2026-11-15). The sessions it started (ir new)
  // are not rows (kumo, 2026-11-15), nor the agents of its channel: those in its members key.
  // shown: its machine is said by the group above it (no cloud on its logo, no face after its name).
  // lane: the rows it may be dragged among (reorder.ts), none out of its place.
  // pinLine: at a group's root, a folder dropped on it is pinned, the line at the top of the first.
  const row = (s: Session, depth: number, shown = false, flip = `s:${s.id}`, lane?: string, pinLine?: string) => (
    <Fragment key={flip}>
      <SessionRow
        s={s}
        where={p.tree.where.get(s.id)}
        depth={depth}
        flip={flip}
        selected={p.selected === s.id}
        onSelect={p.onSelect}
        chatCode={p.chatCode}
        kidsAt={!!p.tree.kids.get(s.id)?.length || !!p.tree.runs.get(s.id)?.length}
        asked={asked.has(s.id)}
        machineShown={shown}
        lane={lane}
        place={place}
        pinLine={pinLine}
        pinFolder={pinFolderAt}
      />
    </Fragment>
  );
  // Those hung under sessions, all of them: what archiving those archives too (the server's
  // cascade, SessionPatch.archived).
  const hungOf = (ss: Session[]) => ss.flatMap((s) => p.tree.hung.get(s.id) ?? []);
  // The sessions of a folder or a group's root, those folded under one row: "还有 N 个" opens
  // FOLD_STEP more a press; all open, "收起" at their foot folds them again.
  const folded = (ss: Session[], key: string, depth: number, draw: (s: Session) => ReactNode) => {
    const f = fold(ss, key);
    return (
      <>
        {f.list.map(draw)}
        {(f.rest > 0 || f.less) && (
          <Row
            key={`more:${key}`}
            depth={depth}
            flip={`more:${key}`}
            expanded={!f.rest}
            onClick={() => (f.rest ? p.showMore(key) : p.foldBack([key]))}
            icon={<Ellipsis size={15} className="text-ink-3" />}
            label={<span className="text-ink-3">{f.rest ? t("nav.more", { n: f.rest }) : t("nav.less")}</span>}
          />
        )}
      </>
    );
  };

  const group = (w: WsNode, i: number, ws: WsNode[]) => {
    const open = p.expanded(w.key);
    // A gap after what is open above; folded groups one under another stay close (the user,
    // 2026-11-08).
    const prev = ws[i - 1];
    const apart = prev ? p.expanded(prev.key) && prev.rows.length > 0 : loose.length > 0;
    // The groups it moves among, at any machine (not 未归类): one up, one down, from its menu.
    const lane = ws.filter((x) => x.id !== 0);
    const k = lane.indexOf(w);
    const up = k > 0 ? lane[k - 1] : undefined;
    const down = k >= 0 ? lane[k + 1] : undefined;
    // A folder of the group and its sessions; pinned ones above the sessions at the root.
    const firstRoot = roots(w)[0];
    // At a machine no longer paired: no "+" (nowhere to open it).
    const gone = (path: string) => !!machineOf(path) && !machines.some((m) => m.id === machineOf(path));
    // A folder of the group and its sessions, the folders in it under them, a step in each (none
    // past MAX_IN); pinned ones above the sessions at the root, or at the folder they are in. Those
    // hung under a session (dispatched) are under it wherever it is.
    const folderEl = (f: FolderNode, lane: string, level: number): ReactNode => {
      const at = Math.min(level, MAX_IN);
      const first = f.sessions[0];
      return (
        <div key={f.key} role="group">
          <FolderRow
            f={f}
            ws={w.id}
            depth={at}
            label={level === 0 && w.id === 0 ? <FolderLabel path={f.path} /> : f.name}
            hung={hungOf(folderSessions(f))}
            open={p.expanded(f.key)}
            onToggle={() => p.toggle(f.key)}
            foldBack={foldBackOf(allFolders([f]).map((x) => x.key))}
            lane={lane}
            place={placeFolderAt}
            pin={pinFolderOn}
            newHere={gone(f.path) ? undefined : () => setPlusOpen(f.key)}
            // As a workspace's: not its count, the "+" that opens a session in the folder
            // (the user, 2026-11-06).
            plus={!gone(f.path) && p.wsPlus(
              { ...w, key: f.key, root: f.path },
              <button type="button" onClick={(e) => e.stopPropagation()} aria-label={t("nav.newHere", { ws: f.name })} className={plusKey(p.expanded(f.key))}>
                <Plus size={15} />
              </button>,
              plusOpen === f.key,
              (o) => setPlusOpen(o ? f.key : null),
            )}
          />
          {p.expanded(f.key) && (
            <>
              {f.children.flatMap((c) => (c.pinned ? [folderEl(c, f.key, level + 1)] : []))}
              {folded(f.sessions, f.key, at + 1, (s) => row(s, at + 1, !!w.machine, undefined, f.key, first && f.children.length > 0 ? dropMark({ kind: "s", id: first.id, lane: f.key }) : undefined))}
              {f.children.flatMap((c) => (c.pinned ? [] : [folderEl(c, f.key, level + 1)]))}
            </>
          )}
        </div>
      );
    };
    return (
      <div key={w.key} className={apart ? "mt-4" : "mt-1"} role="group">
        <WsRow
          flip={`w:${w.key}`}
          machine={machines.find((m) => m.id === w.machine)}
          remote={!!w.machine}
          id={w.id}
          name={w.name}
          sessions={w.rows.flatMap((r) => (r.kind === "session" ? [r.s] : folderSessions(r.f)))}
          hung={hungOf(w.rows.flatMap((r) => (r.kind === "session" ? [r.s] : folderSessions(r.f))))}
          root={w.root}
          folders={w.rows.some((r) => r.kind === "folder")}
          open={open}
          onToggle={() => p.toggle(w.key)}
          foldBack={foldBackOf([w.key, ...allFolders(tops(w)).map((x) => x.key)])}
          wkey={w.key}
          place={placeGroup}
          up={up && (() => placeGroup(w.key, up.key, "top"))}
          down={down && (() => placeGroup(w.key, down.key, "bottom"))}
          // Not its count: the "+" that opens a session here, at the row's end (the user,
          // 2026-11-06), seen while the group is open or the row hovered (plusKey).
          end={
            w.id !== 0 &&
            !gone(w.root) && (
              <>
                {/* Who is in the group's voice; the way in is its menu. */}
                <VoiceMark guild={w.id} />
                {p.wsPlus(
              w,
              <button type="button" onClick={(e) => e.stopPropagation()} aria-label={t("nav.newHere", { ws: w.name })} className={plusKey(open)}>
                <Plus size={15} />
              </button>,
              plusOpen === w.key,
              (o) => setPlusOpen(o ? w.key : null),
            )}
              </>
            )
          }
        />
        {open && w.rows.flatMap((r) => (r.kind === "folder" && r.f.pinned ? [folderEl(r.f, w.key, 0)] : []))}
        {open && folded(roots(w), w.key, 0, (s) => row(s, 0, !!w.machine, undefined, w.key, firstRoot && dropMark({ kind: "s", id: firstRoot.id, lane: w.key })))}
        {open && w.rows.flatMap((r) => (r.kind === "folder" && !r.f.pinned ? [folderEl(r.f, w.key, 0)] : []))}
      </div>
    );
  };


  // On a phone no head of its own: its keys stand by the sheet's tabs (app.tsx, pager.tsx).
  const head = !p.phone && (
    <div className="flex h-12 shrink-0 items-center gap-0.5 px-2">
      {/* The name at the left, the keys at the right (the user, 2026-11-06); no switch of machines
          (the user, 2026-11-10: a group at another is listed as a session is opened there). */}
      <div className="flex min-w-0 flex-1 items-center gap-2 pl-2">
        <IrMark size={18} className="text-ink" />
        <span className="truncate text-base font-semibold tracking-[-0.01em]">Iridium</span>
        {/* The link's dot and latency, by the name (the user, 2026-11-08). */}
        <LinkDot className="shrink-0" />
      </div>
      <HeadKeys {...p} />
    </div>
  );

  return (
    <aside className={cn("relative flex h-full flex-col select-none [-webkit-touch-callout:none]", p.phone ? "w-full" : "shrink-0")} style={p.phone ? undefined : { width }}>
      {!p.phone && <Divider width={width} setWidth={setWidth} />}
      {head}
      {/* While searching, the jump box in the tree's place; the tree stays mounted, hidden, and
          comes back scrolled as it was. */}
      {p.searching && <div className="flex min-h-0 flex-1 flex-col">{p.search}</div>}
      <div ref={list} {...listDrop()} hidden={p.searching} onScroll={(e) => void (scrolled.current = e.currentTarget.scrollTop)} className="relative min-h-0 flex-1 no-bar overflow-y-auto px-2 pb-3" role="tree" aria-label={t("nav.sessions")}>
        <DropLine list={list} />
        {/* 需要你's confirmations: lined up with the groups' folders, apart from the list as a group is. */}
        {loose.length > 0 && (
          <div className="mt-1" role="group">
            {loose.map((c) => (
              <Row
                key={`c:${c.id}`}
                depth={0}
                head
                flip={`c:${c.id}`}
                onClick={() => p.onConfirm(c)}
                icon={<ConfirmIcon c={c} size={15} className="text-need" />}
                label={
                  <>
                    {confirmTitle(c)}
                    {confirmName(c) && <span className="ml-1.5 text-ink-3">{confirmName(c)}</span>}
                  </>
                }
                end={<Lamp status="waiting" />}
                aria={`${confirmTitle(c)} ${confirmName(c) ?? ""}, ${t("status.needs")}`}
              />
            ))}
          </div>
        )}
        {p.tree.workspaces.map(group)}
        {p.tree.archived.length > 0 && (
          <div className={lastOpen ? "mt-4" : "mt-1"} role="group">
            <Row depth={0} flip="archive" head expanded={p.expanded("archive")} onClick={() => p.toggle("archive")} icon={<TrashLid open={p.expanded("archive")} />} label={<span className="text-ink-2">{t("nav.archive")}</span>} end={<Count n={p.tree.archived.length} faint />} />
            {archOpen && p.tree.archived.slice(0, archShown).map((s) => row(s, 0))}
            {archOpen && p.tree.archived.length > archShown && (
              <Row
                key="more:archive"
                depth={0}
                flip="more:archive"
                onClick={() => setArchShown((n) => n + ARCHIVE_STEP)}
                icon={<Ellipsis size={15} className="text-ink-3" />}
                label={<span className="text-ink-3">{t("nav.more", { n: p.tree.archived.length - archShown })}</span>}
              />
            )}
          </div>
        )}
        {!p.tree.workspaces.length && !loose.length && <Empty />}
      </div>
      {!p.noFoot && <Foot me={p.me} />}
    </aside>
  );
}

/** The head's key: a new workspace, a "+" (the user, 2026-11-08: back from a folder). No search key
 *  (the user, 2026-11-08): the jump box is a keyboard's (nav.searchKey). On a phone it stands right
 *  of the sheet's tabs. */
export function HeadKeys(p: Pick<Props, "createKey">) {
  return (
    <span className="flex shrink-0 items-center gap-0.5">
      {p.createKey(
        <button type="button" aria-label={t("menu.new")} className="g-press grid size-8 place-items-center rounded-md text-ink-3 hover:bg-fill hover:text-ink-2 data-popup-open:bg-fill data-popup-open:text-ink-2 pointer-coarse:size-10">
          <Plus size={18} />
        </button>,
      )}
    </span>
  );
}

/** Me, the key to the 终端 panel (me.tsx MeKey), right on the sidebar's ground at its foot: no card
 *  around it (the user, 2026-11-06); 更新 beside it once a newer release is out. Not on a phone with a
 *  chat open: there the panel comes by a swipe up from the left quarter or the chat bar's ⋯ (app.tsx). */
function Foot(p: Pick<Props, "me">) {
  return (
    <div className="flex h-14 shrink-0 items-center justify-between gap-2 px-2 pointer-coarse:h-16">
      <div className="flex min-w-0 flex-1">{p.me}</div>
      {/* The voice this page is in, beside me; nothing while in none (the user, 2026-11-12). */}
      <VoiceDock />
      <UpdateKey />
    </div>
  );
}

/** 更新 by the me key (updatekey.tsx), loaded only once a newer release is found. */
const UpdateKeyPart = lazy(() => import("./updatekey").then((m) => ({ default: m.UpdateKey })));
function UpdateKey() {
  const u = useUpdate().data;
  const me = useMe().data;
  if (!u?.available || !manages(me)) return null;
  return (
    <Suspense>
      <UpdateKeyPart />
    </Suspense>
  );
}

function Count({ n, faint }: { n: number; faint?: boolean }) {
  return <span className={cn("text-xs tabular-nums", faint ? "text-ink-4" : "text-ink-3")}>{n}</span>;
}

/** The archive's mark: lucide's trash-2 with its lid a group of its own, lifting as the archive
 *  opens (the user, 2026-11-06). motion.css "lid". */
function TrashLid({ open }: { open: boolean }) {
  return (
    <svg width={15} height={15} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round" data-open={open || undefined} className="shrink-0 overflow-visible text-ink-3" aria-hidden>
      <g className="g-lid">
        <path d="M3 6h18" />
        <path d="M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2" />
      </g>
      <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6" />
      <path d="M10 11v6" />
      <path d="M14 11v6" />
    </svg>
  );
}

/** A folder shut or open: both drawn, cross-faded (motion.css "swap"); tagged, in its colour and
 *  faintly filled with it. */
function FolderMark({ open, color, remote, size = 15, className }: { open: boolean; color?: Color; remote?: boolean; size?: number; className?: string }) {
  const mark = (
    <span
      data-on={open || undefined}
      className={cn("g-swap transition-colors duration-(--dur-2)", color ? "[&_svg]:[fill:color-mix(in_oklch,currentColor_24%,transparent)]" : (className ?? "text-ink-3"))}
      style={color ? { color: colorVar(color) } : undefined}
      aria-hidden
    >
      <Folder size={size} />
      <FolderOpen size={size} />
    </span>
  );
  if (!remote) return mark;
  // At another machine: a cloud over the folder's lower corner (the user, 2026-11-08).
  return (
    <span className="relative grid">
      {mark}
      <CloudBadge label={t("nav.remoteFolder")} />
    </span>
  );
}

/** Its column is every row's, a chevron in it or not, so the names line up. onClick: a key of its
 *  own (a launcher's, folding those hung under it), the row's press still the row's. */
function Chevron({ state, onClick }: { state?: "open" | "closed"; onClick?: () => void }) {
  const mark = state && <ChevronRight size={13} strokeWidth={2.25} className="transition-transform duration-(--dur-2) ease-(--ease-out)" style={{ transform: state === "open" ? "rotate(90deg)" : undefined }} />;
  if (onClick)
    return (
      <button type="button" tabIndex={-1} aria-label={state === "open" ? t("menu.fold") : t("menu.unfold")} onClick={(e) => (e.stopPropagation(), onClick())} className="grid w-[18px] shrink-0 place-items-center self-stretch rounded-sm text-ink-4 hover:text-ink-2">
        {mark}
      </button>
    );
  return (
    <span className="grid w-[18px] shrink-0 place-items-center text-ink-4" aria-hidden>
      {mark}
    </span>
  );
}

type RowProps = {
  depth: number;
  flip: string;
  chevron?: "open" | "closed";
  /** Open or not, for a row whose icon says it (a folder, the archive) and has no chevron. */
  expanded?: boolean;
  icon?: ReactNode;
  label: ReactNode;
  end?: ReactNode;
  selected?: boolean;
  onClick?: () => void;
  aria?: string;
  extra?: Record<string, unknown>;
  faded?: boolean;
  /** A second line, under the label: the row is two lines high, its icon by the first. */
  sub?: ReactNode;
  /** A workspace's own row (the user, 2026-11-06): a larger name; no chevron nor its column (the user, 2026-11-08): its folder
   *  at the far left, the rows in it one step in under it, as a tree. */
  reveal?: boolean;
  /** A group's place without its larger name: the bin of 归档, under the groups' folders. */
  head?: boolean;
  /** The row dragged now (reorder.ts): faded while it is. */
  dim?: boolean;
  /** The chevron a key of its own (→ ← on the keyboard too). */
  onChevron?: () => void;
  /** A session's row (the user, 2026-11-13): its icon a face as tall as its two lines. */
  face?: boolean;
};

function Row({ depth, flip, chevron, expanded, icon, label, end, selected, onClick, aria, extra, faded, sub, reveal, head, dim, onChevron, face }: RowProps) {
  const two = sub !== undefined;
  const top = reveal || head;
  return (
    <div
      data-flip={flip}
      role="treeitem"
      aria-selected={selected}
      aria-expanded={chevron ? chevron === "open" : expanded}
      aria-label={aria}
      tabIndex={0}
      onClick={onClick}
      onKeyDown={(e) => {
        if (e.target !== e.currentTarget) return;
        if (e.key === "Enter" || e.key === " ") (e.preventDefault(), onClick?.());
        else if (onChevron && ((e.key === "ArrowRight" && chevron === "closed") || (e.key === "ArrowLeft" && chevron === "open"))) (e.preventDefault(), onChevron());
      }}
      className={cn(
        "group relative flex cursor-default rounded-md pr-2 transition-opacity duration-(--dur-2) select-none hover:bg-fill hover:[--row-bg:var(--fill)]",
        face ? "items-center py-1.5 pointer-coarse:py-2" : two ? "items-start py-[5px] pointer-coarse:py-2" : "h-[30px] items-center pointer-coarse:h-10",
        faded && "opacity-60",
        dim && "opacity-40",
      )}
      // A group's folder under the Ir mark at the top, its name under "Iridium" (the user, 2026-11-08).
      style={{ paddingLeft: top ? 8 : depth * STEP } as CSSProperties}
      {...extra}
    >
      {!top && <Chevron state={chevron} onClick={onChevron} />}
      {icon && <span className={cn("grid shrink-0 place-items-center", face ? "mr-2.5 size-9" : top ? "mr-2 w-[18px]" : "mr-2 w-4", !face && two && "h-5 pointer-coarse:h-[23px]")}>{icon}</span>}
      <span className="min-w-0 flex-1">
        <span className="flex min-w-0 items-center">
          <span
            className={cn(
              "min-w-0 flex-1",
              reveal ? "text-md" : "text-base pointer-coarse:text-md",
              two ? "flex items-baseline leading-5 pointer-coarse:leading-[23px]" : "truncate",
              selected ? "font-semibold text-ink" : "text-ink",
            )}
          >
            {label}
          </span>
          {end && <span className="ml-2 flex shrink-0 items-center">{end}</span>}
        </span>
        {two && <span className="block truncate text-sm text-ink-3">{sub}</span>}
      </span>
    </div>
  );
}

/** The line where a dragged row goes (reorder.ts): one for the list, gliding from row to row as
 *  the mouse moves, fading in where it first shows and out where it was dropped, while the rows
 *  glide to their new places (useMoves). */
function DropLine({ list }: { list: RefObject<HTMLDivElement | null> }) {
  const over = useOver();
  const [at, setAt] = useState({ x: 0, y: 0, w: 0, glide: false });
  const was = useRef<Over | null>(null);
  useLayoutEffect(() => {
    const el = over && list.current?.querySelector<HTMLElement>(`[data-place="${CSS.escape(over.line)}"]`);
    if (el) {
      const pad = parseFloat(getComputedStyle(el).paddingLeft) || 0;
      setAt({ x: el.offsetLeft + pad, y: el.offsetTop + (over.edge === "bottom" ? el.offsetHeight : 0) - 1, w: el.offsetWidth - pad, glide: was.current !== null });
    }
    was.current = over;
  }, [over, list]);
  return (
    <div
      aria-hidden
      className={cn("pointer-events-none absolute top-0 left-0 z-10 h-0.5 rounded-full bg-ink-2 duration-(--dur-2) ease-(--ease-out) motion-reduce:transition-none", at.glide ? "transition-[transform,width,opacity]" : "transition-opacity")}
      style={{ transform: `translate(${at.x}px, ${at.y}px)`, width: at.w, opacity: over ? 1 : 0 }}
    />
  );
}

/** A group's row; its menu (right click, long-press): its sessions archived, as a folder's (the
 *  user, 2026-11-08: a group is a folder now), rename, its path copied, open or folded, 通知 while
 *  push is on. 未归类 itself (id 0): its sessions archived and its 通知. */
function WsRow({ flip, id, name, root, sessions, hung, machine, remote, folders, open, onToggle, foldBack, end, wkey, place, up, down }: { flip: string; id: number; name: string; root: string; sessions: Session[]; hung: Session[]; machine?: Machine; remote?: boolean; folders?: boolean; open: boolean; onToggle: () => void; foldBack?: () => void; end: ReactNode; wkey: string; place: (key: string, target: string, edge: Edge) => void; up?: () => void; down?: () => void }) {
  const machines = useMachines();
  const push = usePushMenus();
  const me = useMe().data;
  const real = id !== 0 && manages(me);
  const [at, setAt] = useMenuAt();
  const [renaming, setRenaming] = useState(false);
  const lp = useLongPress((p) => setAt({ x: p.x, y: p.y }));
  const anchor = usePointAnchor(at);
  const failed = (e: unknown) => toast(t("err.action"), { error: true, description: errText(e) });
  // The group's voice, its way in (kumo, 2026-11-16).
  const voice = useVoiceItem(id);
  const items = [
    ...(id > 0 ? [voice] : []),
    // Those hung under its sessions go with them (the server's cascade): counted, not sent. The way
    // a group at another machine is no longer listed (the user, 2026-11-10).
    ...(sessions.length
      ? [{ icon: <Archive size={15} />, label: t("menu.archiveHere", { n: sessions.length + hung.length }), sure: t("menu.archiveSure"), run: () => void Promise.all(sessions.map((s) => api.patch(s.id, { archived: true }))).then(() => toast(t("toast.archived")), failed) }]
      : []),
    // What was opened of its folded sessions, folded again (the user, 2026-11-22).
    ...(foldBack ? [{ icon: <ListCollapse size={15} />, label: t("menu.foldSessions"), run: foldBack }] : []),
    ...(real
      ? [
          { icon: <Pencil size={15} />, label: t("menu.rename"), run: () => setRenaming(true) },
          { icon: <Copy size={15} />, label: t("menu.copyPath"), run: () => void navigator.clipboard.writeText(shownPath(root, machines).real).then(() => toast(t("toast.copied")), failed) },
          { icon: open ? <Folder size={15} /> : <FolderOpen size={15} />, label: open ? t("menu.fold") : t("menu.unfold"), run: onToggle },
          // Taken apart at its folder, its subfolders groups of their own (the user, 2026-11-09).
          ...(folders ? [{ icon: <Ungroup size={15} />, label: t("menu.split"), run: () => void api.patchGuild(id, { split: true }).then(() => toast(t("toast.split", { ws: name })), failed) }] : []),
          // Moved among all groups; with a mouse it is dragged too (reorder.ts).
          ...(up ? [{ icon: <ArrowUp size={15} />, label: t("menu.moveUp"), run: up }] : []),
          ...(down ? [{ icon: <ArrowDown size={15} />, label: t("menu.moveDown"), run: down }] : []),
        ]
      : []),
    // A group at a machine with dsh: what its dsh offers read again now, its workspaces and
    // sessions, models and presets with DSH Desktop's settings (the user, 2026-11-09: here, not in
    // the head's switch).
    ...(machine?.agents.some((a) => a.agent === "dsh")
      ? [{ icon: <RefreshCw size={15} />, label: t("menu.syncDsh"), run: () => {
          toast(t("toast.syncing"));
          void api.dshSync(machine.id).then(() => toast(t("toast.synced", { m: machine.name })), (e: unknown) => toast(t("err.sync"), { error: true, description: errText(e) }));
        } }]
      : []),
  ];
  const menu = items.length > 0 || push;
  // Dragged among all groups' rows, by its key (tree.ts WsNode.key).
  const dim = useDragged("ws", wkey);
  const dragging = real && fine() && !renaming
    ? {
        draggable: true,
        onDragStart: (e: React.DragEvent) => {
          e.dataTransfer.setData("text/plain", name);
          // Dropped on a chat: the group named there (chat.tsx), so a copy too.
          if (id) e.dataTransfer.setData(PLACE_DRAG, JSON.stringify({ path: root, name }));
          e.dataTransfer.effectAllowed = "copyMove";
          startDrag({ kind: "ws", id: wkey, lane: "" });
        },
        onDragEnd: endDrag,
        ...dropTarget("ws", wkey, "", (d, edge) => place(d.id, wkey, edge)),
      }
    : {};
  // Its "+" popover is a portal, but React events bubble through it: a press in it must not fold
  // the workspace (the user's, 2026-11-05: only you fold or unfold).
  return (
    <>
      <Row
        depth={0}
        flip={flip}
        // No chevron, on a phone either (the user, 2026-11-08): its sessions shown under it, or not.
        expanded={open}
        reveal
        icon={<WsMark open={open} remote={!!remote} />}
        onClick={() => !renaming && onToggle()}
        label={
          renaming ? (
            <WsRename id={id} name={name} done={() => setRenaming(false)} />
          ) : (
            <span className="flex min-w-0 items-center">
              <span className="truncate font-medium">{name}</span>
              {/* At another machine: its Bot's face after the name, its link said at the head. */}
              {machine && (
                <Tip label={machine.name} side="bottom">
                  <span className="ml-2 inline-flex shrink-0">
                    <MachineFace m={machine} size={16} className={cn(!machine.online && "opacity-50 grayscale")} />
                  </span>
                </Tip>
              )}
            </span>
          )
        }
        end={end && !renaming && <span className="flex items-center" onClick={(e) => e.stopPropagation()} onKeyDown={(e) => e.stopPropagation()}>{end}</span>}
        dim={dim}
        extra={{ ...(menu ? { ...lp, onContextMenu: (e: React.MouseEvent) => !lp.onContextMenu(e) && own(e) && (e.preventDefault(), setAt({ x: e.clientX, y: e.clientY })), onClickCapture: lp.onClickCapture } : {}), ...dragging }}
      />
      {menu && (
        <Menu.Root open={at !== null} onOpenChange={(o) => !o && setAt(null)}>
          <Menu.Portal>
            <Menu.Positioner anchor={anchor} side="bottom" align="start" sideOffset={4} collisionPadding={8} className="z-50">
              {items.length ? (
                <MenuItems
                  items={items}
                  // No 删除 (the user, 2026-11-08): a group is the folder its sessions are in; it
                  // goes once none is shown.
                  foot={<PushFoot levels={wsLevels(id)} i={items.length} />}
                />
              ) : (
                push && (
                  <Menu.Popup finalFocus={false} className="g-unfold glass thick float min-w-52 rounded-lg p-1.5 text-ink outline-none">
                    <div className="g-stagger flex flex-col">
                      <div className="flex h-7 items-center px-2.5 text-xs font-medium text-ink-3">{t("push.menu")}</div>
                      <push.m.PushItems st={push.st} levels={wsLevels(id)} i={1} />
                    </div>
                  </Menu.Popup>
                )
              )}
            </Menu.Positioner>
          </Menu.Portal>
        </Menu.Root>
      )}
    </>
  );
}

/** A group's mark: a folder as a folder's in it is (the user, 2026-11-08: a group is only a folder),
 *  shut or open as the group is. */
/** A folder by its path, in 未归类: ~ for home; one at another machine (a person opened a session
 *  there) by its path there. */
function FolderLabel({ path }: { path: string }) {
  const sp = shownPath(path, useMachines());
  // At another machine: its path there; the group's mark says which.
  return <>{sp.m ? sp.label : tilde(path)}</>;
}

function WsMark({ open, remote }: { open: boolean; remote: boolean }) {
  return <FolderMark open={open} remote={remote} className="text-ink-2" />;
}

/** A workspace's new name, in place of its row's: Enter or leaving it renames, Esc leaves it. */
function WsRename({ id, name, done }: { id: number; name: string; done: () => void }) {
  const over = useRef(false);
  const end = async (value?: string) => {
    if (over.current) return;
    over.current = true;
    const to = value?.trim();
    done();
    if (to && to !== name) await api.patchGuild(id, { name: to }).catch((e: unknown) => toast(t("err.rename"), { error: true, description: errText(e) }));
  };
  return (
    <input
      autoFocus
      aria-label={t("menu.rename")}
      defaultValue={name}
      maxLength={80}
      onFocus={(e) => e.currentTarget.select()}
      onClick={(e) => e.stopPropagation()}
      onKeyDown={(e) => {
        e.stopPropagation();
        if (e.nativeEvent.isComposing || e.keyCode === 229) return;
        if (e.key === "Enter") void end(e.currentTarget.value);
        else if (e.key === "Escape") void end();
      }}
      onBlur={(e) => void end(e.currentTarget.value)}
      className="no-ring h-6 w-full min-w-0 rounded-sm bg-fill px-1.5 text-md font-medium"
    />
  );
}

/** A session's row: its logo and lamp, its name, unread or pinned at the end. The mouse resting on
 *  it 0.1 s, counted from its last move, or a press, fetches its chat ahead. */
/** The last thing said in a session as a row writes it (data.ts useSaidLine), as the list last
 *  drew it: read by the rows as they draw, so that a name changing elsewhere draws none of them. */
let saidNow: ReturnType<typeof useSaidLine> = () => undefined;

/** A session's face in the list (the user, 2026-11-13): its channel's picture, round, else (one of
 *  several agents) their own faces (FaceStack; the user, 2026-11-16: not their logos), else its
 *  agent's; what is unread of it a badge on its top right corner, red, or grey
 *  while it is quiet (unread.ts) and nothing unread names me, 99+ past 99. */
function RowFace({ s, group, unread, quiet }: { s: Session; group?: { id: string; agent: Agent }[]; unread: number; quiet: boolean }) {
  const logo = group ? <FaceStack members={group} size={36} /> : <Logo agent={s.agent} size={20} />;
  return (
    <span className={cn("relative grid size-9 place-items-center rounded-full", !group && "bg-fill")}>
      <Face id={s.id} agent={s.agent} size={36} none={logo} className="place-items-center" low channel />
      {unread > 0 && (
        <span
          className={cn("g-pop absolute -top-1 -right-1.5 grid h-4 min-w-4 place-items-center rounded-full px-1 text-[10px] leading-none font-semibold tabular-nums", quiet ? "bg-ink-4" : "bg-err")}
          style={{ color: "rgb(255 255 255 / 0.92)", boxShadow: "0 0 0 1.5px var(--row-bg, var(--paper))" }}
        >
          {badge(unread)}
        </span>
      )}
    </span>
  );
}

/** Drawn again only as its props change, not with every draw of the list: a page of the phone's
 *  sheet turned drew every row, its menu and its tip, again (the user, 2026-11-08). */
const SessionRow = memo(function SessionRow({ s, where, depth, flip, selected, onSelect, chatCode, kidsAt, asked, machineShown, lane, place, pinLine, pinFolder }: { s: Session; where?: string; depth: number; flip: string; selected: boolean; onSelect: (id: string) => void; chatCode: () => void; kidsAt?: boolean; asked?: boolean; machineShown?: boolean; lane?: string; place?: (id: string, target: Session, edge: Edge, lane: string) => void; pinLine?: string; pinFolder?: (path: string, lane: string) => void }) {
  useLang();
  const machines = useMachines();
  const unread = useUnread(s.id);
  const quiet = useQuiet(s.id);
  // A message I have not read names me (docs/channels.md "有人 @ 我").
  const at = useAt(s.id);
  const now = useMinute();
  const title = titleOf(s);
  // A confirmation its agent asked for waits on you too, while it is not at work.
  // A channel of several agents (chat/channel): their logos one over the next, their nicks on a
  // second line, its lamp theirs: lit while any of them works.
  const all = useCarried<Session[]>(s.agents?.length ? "list" : null);
  const multi = isMulti(s) ? s.agents! : undefined;
  const members = multi?.map((a) => all?.find((x) => x.id === a.session));
  // Its channel's other agents at work, or its subagents (no rows of their own, kumo 2026-11-15):
  // its lamp says so.
  const base = channelLamp(lampOf(s), [...(members ?? []).flatMap((m) => (m && m.id !== s.id ? [lampOf(m)] : [])), ...(kidsAt ? (["running"] as const) : [])]);
  const status = asked && !BUSY.has(base) ? "waiting" : base;
  const told = status !== base ? statusText("waiting") : base !== lampOf(s) ? statusText(base) : sessionStatusText(s);
  // How long ago it was last active (its turn ended, it was last written to), but in a turn. Unread,
  // it is in ink: no grey dot for it (the user, 2026-11-06).
  const fresh = unread > 0 && (!quiet || !!at) && status === "idle";
  // At work, its lamp stands in the time's place: the two cross-fade there (g-swap), the time still
  // drawn under it so the row keeps its width (the user, 2026-11-08).
  const busy = BUSY.has(status);
  const lastBusy = useRef<typeof status | null>(null);
  if (busy) lastBusy.current = status;
  const when = s.updatedAt > 0 && !s.archived && (
    <span aria-hidden={busy || undefined} className={cn("text-xs whitespace-nowrap tabular-nums transition-colors duration-(--dur-2)", fresh ? "font-medium text-ink" : "text-ink-4")}>
      {ago(s.updatedAt, now, true)}
    </span>
  );
  // Under its name, who last spoke and what (the user, 2026-11-13): you, a person by name, its
  // agent by its nick, else its kind; nothing said yet, its agents.
  const nicks = multi?.map((a) => a.nick).filter(Boolean).join(" · ");
  // A channel of no agent of its own: the agent that spoke there, by its nick (s.last.nick).
  const speaker = s.last?.nick || s.agents?.find((a) => a.session === s.id)?.nick || agentName(s.agent);
  const line = s.last ? (s.last.role === "user" ? saidNow(s.last, s) : `${speaker}: ${s.last.text}`) : nicks || (s.bare ? "" : agentName(s.agent));
  const [menuAt, setMenuAt] = useMenuAt();
  const [renaming, setRenaming] = useState(false);
  const rest = useRef<number | undefined>(undefined);
  const lp = useLongPress((p) => setMenuAt({ x: p.x, y: p.y }));
  const hover = {
    onPointerMove: (e: React.PointerEvent) => {
      lp.onPointerMove(e);
      if (e.pointerType !== "mouse") return;
      clearTimeout(rest.current);
      rest.current = window.setTimeout(() => (chatCode(), ahead(s.id)), 100);
    },
    onPointerLeave: () => (clearTimeout(rest.current), behind()),
    onPointerDown: (e: React.PointerEvent) => {
      lp.onPointerDown(e);
      chatCode();
      // A finger may be scrolling the list: only the chat's code, not this row's messages.
      if (e.pointerType === "mouse") ahead(s.id, true);
    },
    onContextMenu: (e: React.MouseEvent) => {
      if (lp.onContextMenu(e) || !own(e)) return;
      e.preventDefault();
      setMenuAt({ x: e.clientX, y: e.clientY });
    },
  };
  // Dragged to a chat, it is named in that chat's composer (chat.tsx): with a mouse, as a finger's
  // long press is the menu. Nothing changes as it is dragged (the user, 2026-11-06), but the line
  // where it goes among those beside it (reorder.ts).
  const dim = useDragged("s", s.id);
  const drag = fine() && !renaming
    ? {
        draggable: true,
        onDragStart: (e: React.DragEvent) => {
          e.dataTransfer.setData(SESSION_DRAG, s.id);
          e.dataTransfer.setData("text/plain", s.id);
          e.dataTransfer.effectAllowed = "all";
          if (lane) startDrag({ kind: "s", id: s.id, lane });
        },
        onDragEnd: endDrag,
        ...(lane && place
          ? dropTarget("s", s.id, lane, (d: Dragged, edge) => place(d.id, s, edge, lane), pinLine && pinFolder ? { kind: "f", lane, line: pinLine, drop: (d) => pinFolder(d.id, lane) } : undefined)
          : {}),
      }
    : {};
  // An archived one (no shown marker): one press brings it back.
  const restore = (e: React.MouseEvent) => {
    e.stopPropagation();
    void api.patch(s.id, { archived: false }).then(
      () => toast(t("toast.restored")),
      (err) => toast(t("err.action"), { error: true, description: errText(err) }),
    );
  };
  const mark = s.archived ? (
    <IconKey label={t("menu.unarchive")} onClick={restore} className="-mr-1 size-6 text-ink-3 pointer-coarse:size-8">
      <ArchiveRestore size={14} />
    </IconKey>
  ) : s.pinned ? (
    <Pin size={12} className="text-ink-4" aria-label={t("menu.pinned")} />
  ) : undefined;
  const lamp = status !== "idle";
  const slot = (when || lastBusy.current) && (
    <span className="g-swap items-center" data-on={busy ? "" : undefined}>
      {when || <span />}
      <span className="grid justify-self-end">{lastBusy.current && <Lamp status={lastBusy.current} />}</span>
    </span>
  );
  // At another machine, out of its group (in 归档, in 未归类): a cloud on
  // its logo, as a folder there has, its machine's face after its name (the user, 2026-11-10); in
  // its group, the group's row says both. Not connected now, a grey ring for its lamp.
  const machine = useMachine(s.machine);
  const remote = !!s.machine && !s.subagent && !machineShown;
  // Gone a moment (a proxy's cut, a Mac waking) is not shown as gone (machines.tsx useLate).
  const away = useLate(!!s.offline) && !busy;
  const end = slot || mark || lamp || away ? (
    <span className="flex items-center gap-1.5">
      {mark}
      {slot}
      {!busy && (away ? <span className="row-lamp g-pop" data-s="offline" role="img" aria-label={t("status.offline")} /> : <Lamp status={status} />)}
    </span>
  ) : undefined;
  return (
    <>
      <Row
        depth={depth}
        flip={flip}
        selected={selected}
        onClick={() => !renaming && onSelect(s.id)}
        faded={s.archived}
        aria={`${title}, ${told}${s.color ? `, ${t("color.tagged", { color: t(`color.${s.color}`) })}` : ""}${unread ? `, ${t("status.unreadCount", { n: badge(unread) })}` : ""}${at ? `, ${t(at.everyone ? "row.atAll" : "row.atMe")}` : ""}${quiet ? `, ${t("menu.quiet")}` : ""}`}
        icon={
          <Tip label={[multi ? multi.map((a, i) => `${agentName(members?.[i]?.agent ?? a.agent ?? s.agent)} ${a.nick ?? ""}`.trim()).join(", ") : agentName(s.agent), machine?.name, s.subagent && t("row.subagent"), away ? t("status.offline") : status !== "idle" && told].filter(Boolean).join(" · ")} side="bottom">
            <span className="relative grid">
              <RowFace s={s} group={multi?.map((a, i) => ({ id: a.session, agent: members?.[i]?.agent ?? a.agent ?? s.agent }))} unread={unread} quiet={quiet && !at} />
              {remote && <CloudBadge label={machine?.name ?? t("nav.remoteFolder")} />}
              {/* A subagent: no word for it, a small agent badge on the logo's corner (the user, 2026-11-06). */}
              {s.subagent && (
                <span role="img" aria-label={t("row.subagent")} className="absolute -right-0.5 -bottom-0.5 grid size-[13px] place-items-center rounded-full text-ink-2" style={{ background: "var(--row-bg, var(--paper))" }}>
                  <Bot size={9} strokeWidth={2.5} />
                </span>
              )}
            </span>
          </Tip>
        }
        label={
          renaming ? (
            <Rename s={s} done={() => setRenaming(false)} />
          ) : (
            <span className="flex min-w-0 items-baseline">
              <Marked color={s.color}>{untitled(s) ? <span className="text-ink-3">{title}</span> : title}</Marked>
              {/* Lifted to the middle of the name's glyphs (the user, 2026-11-10). */}
              {remote && machine && <MachineFace m={machine} size={14} className={cn("ml-1.5 -translate-y-[1.5px] self-center", !machine.online && "opacity-50 grayscale")} />}
              {where && (
                <Tip label={tilde(s.cwd)} side="bottom">
                  <span className="ml-1.5 max-w-[35%] shrink-0 truncate text-xs font-normal text-ink-3">{where.startsWith("/") ? tilde(where) : shownPath(where, machines).label}</span>
                </Tip>
              )}
            </span>
          )
        }
        end={end}
        sub={at ? <><span className="text-err">{t(at.everyone ? "row.atAll" : "row.atMe")}</span>{line ? ` ${line}` : ""}</> : line}
        face
        dim={dim}
        extra={{ ...lp, ...hover, ...drag, onClickCapture: lp.onClickCapture }}
      />
      <SessionMenu s={s} at={menuAt} close={() => setMenuAt(null)} rename={() => setRenaming(true)} />
    </>
  );
});

/** The new name, in place of the row's: Enter or leaving it renames, Esc leaves it as it was. */
export function Rename({ s, done, className }: { s: Session; done: () => void; className?: string }) {
  const over = useRef(false);
  const end = async (value?: string) => {
    if (over.current) return;
    over.current = true;
    const to = value?.trim();
    done();
    if (to !== undefined && to !== (s.name ?? "") && to !== titleOf(s)) await api.patch(s.id, { name: to }).catch((e) => toast(t("err.rename"), { error: true, description: errText(e) }));
  };
  return (
    <input
      autoFocus
      aria-label={t("menu.rename")}
      defaultValue={untitled(s) ? "" : titleOf(s)}
      maxLength={80}
      onFocus={(e) => e.currentTarget.select()}
      onClick={(e) => e.stopPropagation()}
      onKeyDown={(e) => {
        e.stopPropagation();
        if (e.nativeEvent.isComposing || e.keyCode === 229) return;
        if (e.key === "Enter") void end(e.currentTarget.value);
        else if (e.key === "Escape") void end();
      }}
      onBlur={(e) => void end(e.currentTarget.value)}
      className={cn("no-ring h-6 w-full min-w-0 rounded-sm bg-fill px-1.5 text-base", className)}
    />
  );
}

/** A session's actions: pin, rename, stop its agent while it works, archive; restore an archived
 *  one. */
export function sessionActions(s: Session, rename: () => void): { icon: ReactNode; label: string; run: () => void }[] {
  const run = (f: () => Promise<unknown>, done?: string) => () =>
    void f().then(
      () => done && toast(done),
      (e) => toast(t("err.action"), { error: true, description: errText(e) }),
    );
  // Archive first, the one most used (the user, 2026-11-06).
  return [
    s.archived
      ? { icon: <ArchiveRestore size={15} />, label: t("menu.unarchive"), run: run(() => api.patch(s.id, { archived: false })) }
      : { icon: <Archive size={15} />, label: t("menu.archive"), run: run(() => api.patch(s.id, { archived: true }), t("toast.archived")) },
    s.pinned ? { icon: <PinOff size={15} />, label: t("menu.unpin"), run: run(() => api.patch(s.id, { pinned: false })) } : { icon: <Pin size={15} />, label: t("menu.pin"), run: run(() => api.patch(s.id, { pinned: true })) },
    { icon: <Moon size={15} />, label: t(isQuiet(s.id) ? "menu.unquiet" : "menu.quiet"), run: () => setQuiet(s.id, !isQuiet(s.id)) },
    { icon: <Pencil size={15} />, label: t("menu.rename"), run: rename },
    { icon: <Copy size={15} />, label: t("menu.copyId"), run: run(() => navigator.clipboard.writeText(s.id), t("toast.copied")) },
    ...(s.live && WORKING.has(s.status) ? [{ icon: <Square size={14} />, label: t("menu.stop"), run: run(() => api.stop(s.id), t("toast.stopped")) }] : []),
  ];
}

/** A session's colour tag, set from its menu. */
export function sessionTags(s: Session): Tags {
  return { value: s.color, set: (c) => void api.patch(s.id, { color: c as Color }).catch((e) => toast(t("err.action"), { error: true, description: errText(e) })) };
}

/** A folder's row in a workspace: its name, the "+" for a session in it; its menu (right click,
 *  long-press): a session here, its path copied, open or folded, its sessions archived, its colour
 *  tag. */
function FolderRow({ f, ws, depth, label, hung, open, onToggle, foldBack, newHere, plus, lane, place, pin }: { f: FolderNode; ws: number; depth: number; label: ReactNode; hung: Session[]; open: boolean; onToggle: () => void; foldBack?: () => void; newHere?: () => void; plus: ReactNode; lane: string; place: (path: string, target: FolderNode, edge: Edge, lane: string) => void; pin: (path: string, on: boolean) => void }) {
  const machines = useMachines();
  const me = useMe().data;
  // Dragged among the folders of its group (reorder.ts): the panels' and 本机's, as a group.
  const dim = useDragged("f", f.path);
  const real = manages(me);
  const dragging = real && fine()
    ? {
        draggable: true,
        onDragStart: (e: React.DragEvent) => {
          e.dataTransfer.setData("text/plain", f.path);
          e.dataTransfer.setData(PLACE_DRAG, JSON.stringify({ path: f.path, name: f.path.split("/").filter(Boolean).pop() ?? f.path }));
          e.dataTransfer.effectAllowed = "copyMove";
          startDrag({ kind: "f", id: f.path, lane });
        },
        onDragEnd: endDrag,
        ...dropTarget("f", f.path, lane, (d, edge) => place(d.id, f, edge, lane)),
      }
    : {};
  const [at, setAt] = useMenuAt();
  const lp = useLongPress((p) => setAt({ x: p.x, y: p.y }));
  const done = (what: string) => () => toast(what);
  const failed = (e: unknown) => toast(t("err.action"), { error: true, description: errText(e) });
  const color = useFolderColors()[f.path];
  const tags: Tags = { value: color, set: (c) => void api.markFolder(f.path, { color: c as Color }).catch(failed) };
  // Archive first, as in a session's menu (the user, 2026-11-06); those of the folders in it too
  // (the user, 2026-11-10).
  const all = folderSessions(f);
  const items = [
    ...(all.length
      ? [{ icon: <Archive size={15} />, label: t("menu.archiveHere", { n: all.length + hung.length }), sure: t("menu.archiveSure"), run: () => void Promise.all(all.map((s) => api.patch(s.id, { archived: true }))).then(done(t("toast.archived")), failed) }]
      : []),
    ...(foldBack ? [{ icon: <ListCollapse size={15} />, label: t("menu.foldSessions"), run: foldBack }] : []),
    ...(newHere ? [{ icon: <Plus size={15} />, label: t("menu.newHere"), run: newHere }] : []),
    { icon: <Copy size={15} />, label: t("menu.copyPath"), run: () => void navigator.clipboard.writeText(shownPath(f.path, machines).real).then(done(t("toast.copied")), failed) },
    { icon: open ? <Folder size={15} /> : <FolderOpen size={15} />, label: open ? t("menu.fold") : t("menu.unfold"), run: onToggle },
    // Pinned: above the sessions at its group's root (reorder.ts).
    ...(real ? [f.pinned ? { icon: <PinOff size={15} />, label: t("menu.unpin"), run: () => pin(f.path, false) } : { icon: <Pin size={15} />, label: t("menu.pin"), run: () => pin(f.path, true) }] : []),
  ];
  const anchor = usePointAnchor(at);
  return (
    <>
      <Row
        depth={depth}
        flip={`f:${f.key}`}
        expanded={open}
        onClick={onToggle}
        // No chevron: the folder itself is open or shut (the user, 2026-11-06).
        icon={<FolderMark open={open} color={color} remote={remotePath(f.path)} />}
        label={label}
        // Pinned: the pin a session's row has, where it has it (the user, 2026-11-09).
        end={
          (f.pinned || plus) && (
            <span className="flex items-center gap-1.5">
              {f.pinned && <Pin size={12} className="text-ink-4" aria-label={t("menu.pinned")} />}
              {plus && <span className="flex items-center" onClick={(e) => e.stopPropagation()} onKeyDown={(e) => e.stopPropagation()}>{plus}</span>}
            </span>
          )
        }
        dim={dim}
        extra={{ ...lp, onContextMenu: (e: React.MouseEvent) => !lp.onContextMenu(e) && own(e) && (e.preventDefault(), setAt({ x: e.clientX, y: e.clientY })), onClickCapture: lp.onClickCapture, ...dragging }}
      />
      <Menu.Root open={at !== null} onOpenChange={(o) => !o && setAt(null)}>
        <Menu.Portal>
          <Menu.Positioner anchor={anchor} side="bottom" align="start" sideOffset={4} collisionPadding={8} className="z-50">
            <MenuItems items={items} tags={tags} foot={<PushFoot levels={folderLevels(f.path, ws)} i={items.length} />} />
          </Menu.Positioner>
        </Menu.Portal>
      </Menu.Root>
    </>
  );
}

function SessionMenu({ s, at, close, rename }: { s: Session; at: { x: number; y: number } | null; close: () => void; rename: () => void }) {
  const items = sessionActions(s, rename);
  const anchor = usePointAnchor(at);
  return (
    <Menu.Root open={at !== null} onOpenChange={(o) => !o && close()}>
      <Menu.Portal>
        <Menu.Positioner anchor={anchor} side="bottom" align="start" sideOffset={4} collisionPadding={8} className="z-50">
          <MenuItems
            items={items}
            tags={sessionTags(s)}
            foot={<PushFoot levels={sessionLevels(s)} i={items.length} />}
          />
        </Menu.Positioner>
      </Menu.Portal>
    </Menu.Root>
  );
}

/** 通知 at a menu's foot, while push is on (push.ts). */
function PushFoot({ levels, i }: { levels: PushLevel[]; i: number }) {
  const push = usePushMenus();
  return push && <push.m.PushSubmenu st={push.st} levels={levels} i={i} />;
}

/** A colour tag and how to set it ("" takes it off). */
export type Tags = { value?: Color; set: (c: Color | "") => void };

/** sure: what its words become once pressed, in red, the menu staying open: only a second press
 *  runs it (docs/前端设计规范.md "确认在原处"). No card is opened for it elsewhere: one under its row
 *  was out of sight below the fold for the last row of a list (kumo, 2026-11-19). */
export type MenuItem = { icon: ReactNode; label: string; run: () => void; sure?: string };

/** A menu's items; lead: before the children, at its top; foot: after the items, over the tags. */
export function MenuItems({ lead = [], items, tags, children, foot }: { lead?: MenuItem[]; items: MenuItem[]; tags?: Tags; children?: ReactNode; foot?: ReactNode }) {
  // The item pressed once that asks to be pressed again; the popup gone, so is this.
  const [armed, setArmed] = useState<string>();
  const item = (it: MenuItem, i: number) => {
    const asks = !!it.sure && armed !== it.label;
    const red = !!it.sure && armed === it.label;
    return (
      <Menu.Item key={it.label} closeOnClick={!asks} onClick={asks ? () => setArmed(it.label) : it.run} style={{ "--i": i } as CSSProperties} className={cn("flex h-8 cursor-default items-center gap-2.5 rounded-md px-2.5 text-base outline-none select-none data-highlighted:bg-fill pointer-coarse:h-11", red && "text-err")}>
        <span className={red ? "text-err" : "text-ink-2"}>{it.icon}</span>
        <span className="min-w-0 truncate">{red ? it.sure : it.label}</span>
      </Menu.Item>
    );
  };
  return (
    <Menu.Popup finalFocus={false} className="g-unfold glass thick float no-bar max-h-(--available-height) max-w-[calc(100vw-16px)] min-w-48 overflow-y-auto rounded-lg p-1.5 text-ink outline-none">
      <div className="g-stagger flex flex-col">
        {lead.map(item)}
        {children}
        {items.map((it, i) => item(it, lead.length + i))}
        {foot}
        {tags && <TagDots tags={tags} i={lead.length + items.length + (foot ? 1 : 0)} />}
      </div>
    </Menu.Popup>
  );
}

/** The menu's foot: a hairline, then the seven colour dots in a row. One press tags it and shuts
 *  the menu; the dot of its own colour is ringed and checked, and a press on it takes the tag off. */
function TagDots({ tags, i }: { tags: Tags; i: number }) {
  return (
    <div role="group" aria-label={t("color.tags")} style={{ "--i": i } as CSSProperties} className="mt-1.5 flex items-center justify-between gap-0.5 border-t border-edge px-1 pt-1.5">
      {COLORS.map((c) => {
        const on = tags.value === c;
        const name = t(`color.${c}`);
        return (
          <Menu.Item
            key={c}
            onClick={() => tags.set(on ? "" : c)}
            aria-label={on ? t("color.off", { color: name }) : t("color.tagged", { color: name })}
            className="group/tag grid size-7 cursor-default place-items-center rounded-full outline-none select-none data-highlighted:bg-fill pointer-coarse:size-9"
          >
            <span
              className={cn(
                "grid size-3.5 place-items-center rounded-full transition-[scale,box-shadow] duration-(--dur-2) ease-(--ease-spring) group-active/tag:scale-90 motion-safe:group-data-highlighted/tag:scale-115 pointer-coarse:size-4.5",
                on && "shadow-[0_0_0_1.5px_var(--paper),0_0_0_3px_currentColor]",
              )}
              style={{ background: colorVar(c), color: colorVar(c) }}
            >
              {on && <Check size={10} strokeWidth={3.5} className="g-pop text-on-ink" />}
            </span>
          </Menu.Item>
        );
      })}
    </div>
  );
}

/** A session's name, its colour tag a highlighter's stroke over it, the whole height of the
 *  letters, light (the user, 2026-11-06): drawn left to right as it is given, wiped back off in its
 *  colour as it is taken off (styles.css .tag-mark). */
export function Marked({ color, children }: { color?: Color; children: ReactNode }) {
  const last = useRef(color);
  if (color) last.current = color;
  const shown = color ?? last.current;
  return (
    <span data-on={color ? "" : undefined} className="tag-mark -mx-1 min-w-0 truncate px-1" style={shown ? ({ "--mark": `color-mix(in oklch,${colorVar(shown)} 32%,#0000)` } as CSSProperties) : undefined}>
      {children}
    </span>
  );
}

/** The list empty: no sessions, or none come yet because the stream is down (not that there are none). */
function Empty() {
  const listed = useList() !== undefined;
  const down = useLink().live === false;
  if (!listed && !down) return null;
  return <div className="g-fade px-2 pt-10 text-center text-sm text-ink-3">{t(listed ? "nav.empty" : "nav.offline")}</div>;
}

/** A group's or a folder's "+": seen while it is open, or while its row is hovered (a mouse only;
 *  kumo, 2026-11-16); kept while its popover is. Unseen, it takes no press, its place kept. */
const plusKey = (open: boolean) =>
  cn("g-press -mr-1 grid size-6 place-items-center rounded-sm text-ink-3 group-hover:text-ink-2 hover:bg-fill-2 data-popup-open:bg-fill-2 data-popup-open:text-ink-2 pointer-coarse:size-8", !open && "invisible group-hover:visible data-popup-open:visible");
