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
import { Archive, ArchiveRestore, Bot, Check, ChevronRight, Copy, Eye, EyeOff, Folder, FolderOpen, Monitor, Pencil, Pin, PinOff, Plus, RefreshCw, Square, Workflow as WorkflowIcon } from "lucide-react";
import { ConfirmIcon, confirmName, confirmTitle } from "./confirmname";
import { Fragment, lazy, memo, Suspense, useLayoutEffect, useRef, useState, type CSSProperties, type ReactElement, type ReactNode } from "react";
import { api } from "./api/client";
import type { Color, Confirm, Machine, Session, Terminal } from "./api/types.gen";
import { colorVar, COLORS, useFolderColors } from "./colors";
import { ahead, behind } from "./chats";
import { t, useLang } from "./i18n";
import { useFlip } from "./motion";
import { folderLevels, sessionLevels, usePushMenus, wsLevels, type PushLevel } from "./push";
import { machineOf, remotePath, type Folder as FolderNode, type Tree, type WsNode } from "./tree";
import { LinkDot } from "./link";
import { errText, toast } from "./toast";
import { useMe, useTerminalList, useUpdate } from "./data";
import { agentName, ago, Avatar, cn, fine, IconKey, IrMark, keep, kept, Lamp, lampOf, Logo, own, sessionStatusText, statusText, tilde, Tip, titleOf, untitled, useLongPress, useMenuAt, useMinute } from "./ui";
import { SESSION_DRAG } from "./chat/refs";
import { useUnread } from "./unread";
import { LINK_COLOR, MachineBadge, MachineFace, setView, shownPath, useLate, useLink as useLinkOf, useMachine, useView } from "./machines";
import { useMachines } from "./store";

const STEP = 18;
const BUSY = new Set(["running", "compacting", "starting"]);
/** While its agent works, an approval awaited too: then it may be stopped (the user, 2026-11-06). */
const WORKING = new Set([...BUSY, "waiting"]);

type Props = {
  tree: Tree;
  expanded: (key: string) => boolean;
  toggle: (key: string) => void;
  selected: string;
  onSelect: (id: string) => void;
  phone: boolean;
  /** The jump box open: drawn here, under the head, in the tree's place (the user, 2026-11-06). */
  searching: boolean;
  search: ReactNode;
  onSearch: () => void;
  /** What agents' ir asked this panel for (invites, tokens): under 需要你 too. */
  confirms: Confirm[];
  onConfirm: (c: Confirm) => void;
  createKey: (trigger: ReactElement) => ReactNode;
  wsPlus: (ws: WsNode, trigger: ReactElement, open: boolean, setOpen: (o: boolean) => void) => ReactNode;
  me: ReactNode;
  /** The chat's code, fetched as a row is pressed. */
  chatCode: () => void;
  /** The settings' and the jump box's code and answers, fetched as a pointer comes on their keys. */
  panelsCode: () => void;
  /** Without its foot, me and the settings key: on a phone they are a sheet of their own. */
  noFoot?: boolean;
  /** The notes, shown in the tree's place as the jump box is (a computer's; notes.tsx), and the
   *  key in the head that shows them. */
  notes?: ReactNode;
  notesOut?: boolean;
  notesKey?: ReactNode;
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
  const list = useRef<HTMLDivElement>(null);
  const [width, setWidth] = useState(() => clampWidth(Number(kept(WIDTH_KEY)) || WIDTH.start));
  useLayoutEffect(() => void document.documentElement.style.setProperty("--sidebar-w", `${p.phone ? 0 : width}px`), [width, p.phone]);
  const [plusOpen, setPlusOpen] = useState<string | null>(null);
  // The machine the list shows (the head's switch, machines.tsx useView).
  const view = useView();
  // All groups, or one machine's (tree.ts lists this machine's first, then each other's).
  const shownWs = view === null ? p.tree.workspaces : p.tree.workspaces.filter((w) => (w.machine ?? "") === view);
  // A confirmation an agent's ir asked for lights its session's lamp; one of no session listed here
  // is a row of its own at the top (no 需要你 group: the user, 2026-11-08).
  const listed = new Set(p.tree.workspaces.flatMap((w) => w.rows.flatMap((r) => (r.kind === "session" ? [r.s.id] : r.f.sessions.map((s) => s.id)))));
  const asked = new Set(p.confirms.flatMap((c) => (c.session && listed.has(c.session) ? [c.session] : [])));
  const loose = p.confirms.filter((c) => !c.session || !listed.has(c.session));
  const last = shownWs.at(-1);
  const lastOpen = last ? p.expanded(last.key) && last.rows.length > 0 : loose.length > 0;
  const shape = [loose.map((c) => c.id).join(","), ...p.tree.workspaces.map((w) => `${w.key}:${p.expanded(w.key)}:${w.rows.map((r) => (r.kind === "session" ? r.s.id : `${r.f.key}${p.expanded(r.f.key)}${r.f.sessions.length}`)).join(",")}`), p.expanded("archive"), p.tree.archived.length, [...p.tree.kids.values()].flat().map((s) => s.id).join(","), [...p.tree.runs.values()].flat().map((r) => `${r.id}${p.expanded(`run:${r.id}`)}${r.working.map((s) => s.id)}`).join(",")].join("|");
  useFlip(list, shape);
  const scrolled = useRef(0);
  useLayoutEffect(() => void (!p.searching && !p.notesOut && list.current && (list.current.scrollTop = scrolled.current)), [p.searching, p.notesOut]);
  // A session's row, and under it the subagents it has at work.
  // shown: its machine is said by the group above it (no badge on its logo).
  const row = (s: Session, depth: number, shown = false, flip = `s:${s.id}`) => (
    <Fragment key={flip}>
      <SessionRow s={s} where={p.tree.where.get(s.id)} depth={depth} flip={flip} selected={p.selected === s.id} onSelect={p.onSelect} chatCode={p.chatCode} asked={asked.has(s.id)} machineShown={shown} />
      {p.tree.kids.get(s.id)?.map((k) => <SessionRow key={k.id} s={k} depth={depth + 1} flip={`${flip}>${k.id}`} selected={p.selected === k.id} onSelect={p.onSelect} chatCode={p.chatCode} working />)}
      {p.tree.runs.get(s.id)?.map((r) => {
        const open = p.expanded(`run:${r.id}`);
        return (
          <Fragment key={r.id}>
            <Row
              depth={depth + 1}
              flip={`${flip}>${r.id}`}
              // No chevron: the workflow's mark says it holds agents (the user, 2026-11-06).
              expanded={open}
              onClick={() => p.toggle(`run:${r.id}`)}
              icon={<WorkflowIcon size={15} className="text-ink-2" />}
              label={<span className="font-mono text-sm">{r.name}</span>}
              end={
                <span className="flex items-center gap-1.5">
                  <span className="text-xs whitespace-nowrap text-ink-3 tabular-nums">
                    {r.phase && `${r.phase} · `}
                    {r.done}/{r.total}
                  </span>
                  <Lamp status="running" />
                </span>
              }
              aria={`Workflow ${r.name}${r.phase ? `, ${r.phase}` : ""}, ${r.done}/${r.total} done`}
            />
            {open && r.working.map((k) => <SessionRow key={k.id} s={k} depth={depth + 2} flip={`${flip}>${r.id}>${k.id}`} selected={p.selected === k.id} onSelect={p.onSelect} chatCode={p.chatCode} working />)}
          </Fragment>
        );
      })}
    </Fragment>
  );

  // One not shown that needs you, so it is not missed.
  const unseen = p.tree.workspaces.filter((w) => !shownWs.includes(w)).flatMap((w) => w.rows.flatMap((r) => (r.kind === "session" ? [r.s] : r.f.sessions)));
  const waits = unseen.some((s) => asked.has(s.id) || lampOf(s) === "waiting");
  const group = (w: WsNode, i: number, ws: WsNode[]) => {
    const open = p.expanded(w.key);
    // A gap after what is open above; folded groups one under another stay close (the user,
    // 2026-11-08).
    const prev = ws[i - 1];
    const apart = prev ? p.expanded(prev.key) && prev.rows.length > 0 : loose.length > 0;
    // Listed at a machine its folder is not at (its sessions there): its folders by their paths
    // there, its "+" in the first of them, at that machine.
    const foreign = (w.machine ?? "") !== (machineOf(w.root) ?? "") && w.id !== 0;
    const plusAt = foreign ? (w.rows.find((r) => r.kind === "folder")?.f.path ?? w.root) : w.root;
    return (
      <div key={w.key} className={apart ? "mt-4" : "mt-1"} role="group">
        <WsRow
          flip={`w:${w.key}`}
          machine={machines.find((m) => m.id === w.machine)}
          remote={!!w.machine}
          id={w.id}
          name={w.name}
          sessions={w.rows.flatMap((r) => (r.kind === "session" ? [r.s] : r.f.sessions))}
          root={w.root}
          hidden={w.hidden}
          open={open}
          onToggle={() => p.toggle(w.key)}
          // Not its count: the "+" that opens a session here, always at the row's end, plain to
          // see, darker when the row is hovered (the user, 2026-11-06).
          end={
            w.id !== 0 &&
            p.wsPlus(
              { ...w, root: plusAt },
              <button type="button" onClick={(e) => e.stopPropagation()} aria-label={t("nav.newHere", { ws: w.name })} className="g-press -mr-1 grid size-6 place-items-center rounded-sm text-ink-3 group-hover:text-ink-2 hover:bg-fill-2 data-popup-open:bg-fill-2 data-popup-open:text-ink-2 pointer-coarse:size-8">
                <Plus size={15} />
              </button>,
              plusOpen === w.key,
              (o) => setPlusOpen(o ? w.key : null),
            )
          }
        />
        {open &&
          w.rows.map((r) =>
            r.kind === "session" ? (
              row(r.s, 0, !!w.machine)
            ) : (
              <div key={r.f.key} role="group">
                <FolderRow
                  f={r.f}
                  ws={w.id}
                  label={w.id === 0 || foreign ? <FolderLabel path={r.f.path} /> : r.f.name}
                  open={p.expanded(r.f.key)}
                  onToggle={() => p.toggle(r.f.key)}
                  newHere={() => setPlusOpen(r.f.key)}
                  // As a workspace's: not its count, the "+" that opens a session in the folder
                  // (the user, 2026-11-06).
                  plus={p.wsPlus(
                    { ...w, key: r.f.key, root: r.f.path },
                    <button type="button" onClick={(e) => e.stopPropagation()} aria-label={t("nav.newHere", { ws: r.f.name })} className="g-press -mr-1 grid size-6 place-items-center rounded-sm text-ink-3 group-hover:text-ink-2 hover:bg-fill-2 data-popup-open:bg-fill-2 data-popup-open:text-ink-2 pointer-coarse:size-8">
                      <Plus size={15} />
                    </button>,
                    plusOpen === r.f.key,
                    (o) => setPlusOpen(o ? r.f.key : null),
                  )}
                />
                {p.expanded(r.f.key) && r.f.sessions.map((s) => row(s, 1, !!w.machine))}
              </div>
            ),
          )}
      </div>
    );
  };

  // On a phone no head of its own: its keys stand by the sheet's tabs (app.tsx, pager.tsx).
  const head = !p.phone && (
    <div className="flex h-12 shrink-0 items-center gap-0.5 px-2">
      {/* The name at the left, the keys at the right (the user, 2026-11-06). */}
      {machines.length ? (
        <ViewSwitch view={view} machines={machines} waits={waits} />
      ) : (
        <span className="flex min-w-0 flex-1 items-center gap-2 pl-2 select-none">
          <IrMark size={18} className="text-ink" />
          <span className="truncate text-base font-semibold tracking-[-0.01em]">Iridium</span>
          {/* The link's dot and latency, by the name (the user, 2026-11-08). */}
          <LinkDot className="shrink-0" />
        </span>
      )}
      {p.notesKey}
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
      {p.notes && (
        <div hidden={p.searching || !p.notesOut} className="min-h-0 flex-1 flex-col [&:not([hidden])]:flex">
          {p.notes}
        </div>
      )}
      <div ref={list} hidden={p.searching || !!p.notesOut} onScroll={(e) => void (scrolled.current = e.currentTarget.scrollTop)} className="relative min-h-0 flex-1 no-bar overflow-y-auto px-2 pb-3" role="tree" aria-label={t("nav.sessions")}>
        {/* On a phone, with no head: the switch at the list's top. */}
        {p.phone && machines.length > 0 && (
          <div className="flex h-10 items-center">
            <ViewSwitch view={view} machines={machines} waits={waits} />
          </div>
        )}
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
        {shownWs.map(group)}
        {p.tree.archived.length > 0 && (
          <div className={lastOpen ? "mt-4" : "mt-1"} role="group">
            <Row depth={0} flip="archive" head expanded={p.expanded("archive")} onClick={() => p.toggle("archive")} icon={<TrashLid open={p.expanded("archive")} />} label={<span className="text-ink-2">{t("nav.archive")}</span>} end={<Count n={p.tree.archived.length} faint />} />
            {p.expanded("archive") && p.tree.archived.map((s) => row(s, 0))}
          </div>
        )}
        {!p.tree.workspaces.length && !loose.length && <div className="g-fade px-2 pt-10 text-center text-sm text-ink-3">{t("nav.empty")}</div>}
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
      <UpdateKey />
    </div>
  );
}

/** 更新 by the me key (updatekey.tsx), loaded only once a newer release is found. */
const UpdateKeyPart = lazy(() => import("./updatekey").then((m) => ({ default: m.UpdateKey })));
function UpdateKey() {
  const u = useUpdate().data;
  const me = useMe().data;
  if (!u?.available || !me || !(me.local || me.kind === "panel")) return null;
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
  // At another machine: a solid cloud over the folder's lower corner, tucked in toward its middle,
  // ringed in the row's colour so the folder's line stops short of it (the user, 2026-11-08).
  return (
    <span className="relative grid">
      {mark}
      <svg role="img" aria-label={t("nav.remoteFolder")} width={10} height={8} viewBox="0 0 20 15" className="absolute -right-px -bottom-px overflow-visible text-ink-2">
        <path d="M15 13.5H5.5a4 4 0 0 1-.6-7.95A5.2 5.2 0 0 1 14.8 4.6 4.45 4.45 0 0 1 15 13.5Z" fill="currentColor" stroke="var(--row-bg, var(--paper))" strokeWidth={3} strokeLinejoin="round" paintOrder="stroke" />
      </svg>
    </span>
  );
}

/** Its column is every row's, a chevron in it or not, so the names line up. */
function Chevron({ state }: { state?: "open" | "closed" }) {
  return (
    <span className="grid w-[18px] shrink-0 place-items-center text-ink-4" aria-hidden>
      {state && <ChevronRight size={13} strokeWidth={2.25} className="transition-transform duration-(--dur-2) ease-(--ease-out)" style={{ transform: state === "open" ? "rotate(90deg)" : undefined }} />}
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
};

function Row({ depth, flip, chevron, expanded, icon, label, end, selected, onClick, aria, extra, faded, sub, reveal, head }: RowProps) {
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
      onKeyDown={(e) => e.target === e.currentTarget && (e.key === "Enter" || e.key === " ") && (e.preventDefault(), onClick?.())}
      className={cn(
        "group relative flex cursor-default rounded-md pr-2 select-none hover:bg-fill hover:[--row-bg:var(--fill)]",
        two ? "items-start py-[5px] pointer-coarse:py-2" : "h-[30px] items-center pointer-coarse:h-10",
        faded && "opacity-60",
      )}
      // A group's folder under the Ir mark at the top, its name under "Iridium" (the user, 2026-11-08).
      style={{ paddingLeft: top ? 8 : depth * STEP } as CSSProperties}
      {...extra}
    >
      {!top && <Chevron state={chevron} />}
      {icon && <span className={cn("grid shrink-0 place-items-center", top ? "mr-2 w-[18px]" : "mr-2 w-4", two && "h-5 pointer-coarse:h-[23px]")}>{icon}</span>}
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

/** A group's row; its menu (right click, long-press): its sessions archived, as a folder's (the
 *  user, 2026-11-08: a group is a folder now), rename, its path copied, open or folded, 通知 while
 *  push is on. 未归类 itself (id 0): its sessions archived and its 通知. */
function WsRow({ flip, id, name, root, sessions, machine, remote, hidden, open, onToggle, end }: { flip: string; id: number; name: string; root: string; sessions: Session[]; machine?: Machine; remote?: boolean; hidden?: boolean; open: boolean; onToggle: () => void; end: ReactNode }) {
  const machines = useMachines();
  const push = usePushMenus();
  const me = useMe().data;
  const real = id !== 0 && !!me && (me.local || me.kind === "panel");
  const [at, setAt] = useMenuAt();
  const [renaming, setRenaming] = useState(false);
  const lp = useLongPress(() => {
    const r = document.activeElement?.closest?.("[role=treeitem]")?.getBoundingClientRect();
    setAt(r ? { x: r.left + 40, y: r.bottom } : { x: 40, y: 40 });
  });
  const anchor = at && { getBoundingClientRect: () => DOMRect.fromRect({ x: at.x, y: at.y, width: 0, height: 0 }) };
  const failed = (e: unknown) => toast(t("err.action"), { error: true, description: errText(e) });
  const items = [
    ...(sessions.length
      ? [{ icon: <Archive size={15} />, label: t("menu.archiveHere", { n: sessions.length }), run: () => void Promise.all(sessions.map((s) => api.patch(s.id, { archived: true }))).then(() => toast(t("toast.archived")), failed) }]
      : []),
    ...(real
      ? [
          { icon: <Pencil size={15} />, label: t("menu.rename"), run: () => setRenaming(true) },
          { icon: <Copy size={15} />, label: t("menu.copyPath"), run: () => void navigator.clipboard.writeText(shownPath(root, machines).real).then(() => toast(t("toast.copied")), failed) },
          { icon: open ? <Folder size={15} /> : <FolderOpen size={15} />, label: open ? t("menu.fold") : t("menu.unfold"), run: onToggle },
          // Hidden: listed no more, but while one of its sessions runs elsewhere (the user, 2026-11-08).
          hidden
            ? { icon: <Eye size={15} />, label: t("menu.unhide"), run: () => void api.patchWorkspace(id, { hidden: false }).catch(failed) }
            : { icon: <EyeOff size={15} />, label: t("menu.hide"), run: () => void api.patchWorkspace(id, { hidden: true }).then(() => toast(t("toast.hidden", { ws: name })), failed) },
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
        extra={menu ? { ...lp, onContextMenu: (e: React.MouseEvent) => own(e) && (e.preventDefault(), setAt({ x: e.clientX, y: e.clientY })), onClickCapture: lp.onClickCapture } : undefined}
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

/** The head's name, while another machine's Bot is linked (the user, 2026-11-09): a press opens a
 *  menu of what the list shows, 全部, 本机 or one other machine (its face, name, link lamp and round
 *  trip). Showing one, the head says that machine: Iridium's mark, then its name, face and link
 *  where Iridium's are, its "+" opening there. An amber lamp after it while a session not shown
 *  needs you. */
function ViewSwitch({ view, machines, waits }: { view: string | null; machines: Machine[]; waits: boolean }) {
  const m = view ? machines.find((x) => x.id === view) : undefined;
  const pick = (v: string | null, icon: ReactNode, label: ReactNode, i: number) => (
    <Menu.Item key={v ?? "all"} onClick={() => setView(v)} style={{ "--i": i } as CSSProperties} className="flex h-8 cursor-default items-center gap-2.5 rounded-md px-2.5 text-base outline-none select-none data-highlighted:bg-fill pointer-coarse:h-11">
      <span className="grid w-4 place-items-center text-ink-2">{icon}</span>
      <span className="flex min-w-0 flex-1 items-center gap-2">{label}</span>
      {view === v && <Check size={14} className="text-ink-2" />}
    </Menu.Item>
  );
  return (
    <Menu.Root>
      <Menu.Trigger className="g-press -my-1 flex min-w-0 flex-1 items-center gap-2 rounded-md py-1 pl-2 text-left outline-none select-none hover:bg-fill data-popup-open:bg-fill">
        {/* Iridium's mark stays; showing a machine, its face after its name, as a remote group's. */}
        <IrMark size={18} className="text-ink" />
        <span className="truncate text-base font-semibold tracking-[-0.01em]">{m ? m.name : view === "" ? t("new.local") : "Iridium"}</span>
        {m && <MachineFace m={m} size={16} />}
        {m ? <MachineLink m={m} /> : <LinkDot className="shrink-0" />}
        {waits && <Lamp status="waiting" />}
      </Menu.Trigger>
      <Menu.Portal>
        <Menu.Positioner side="bottom" align="start" sideOffset={4} collisionPadding={8} className="z-50">
          <Menu.Popup finalFocus={false} className="g-unfold glass thick float min-w-56 rounded-lg p-1.5 text-ink outline-none">
            <div className="g-stagger flex flex-col">
              {pick(null, <IrMark size={14} className="text-ink-2" />, t("nav.all"), 0)}
              {pick("", <Monitor size={14} />, t("new.local"), 1)}
              {machines.map((x, i) =>
                pick(
                  x.id,
                  <MachineFace m={x} size={16} />,
                  <>
                    <span className="truncate">{x.name}</span>
                    <MachineLink m={x} />
                  </>,
                  i + 2,
                ),
              )}
            </div>
          </Menu.Popup>
        </Menu.Positioner>
      </Menu.Portal>
    </Menu.Root>
  );
}

/** A machine's link as the head shows the daemon's: a lamp, the round trip or 断开. */
function MachineLink({ m }: { m: Machine }) {
  const link = useLinkOf(m);
  const said = link === "away" ? t("status.offline") : m.online && m.latency ? t("machine.latency", { ms: m.latency }) : t("machine.reconnecting");
  return (
    <span className="flex shrink-0 items-center gap-1.5 text-xs font-normal text-ink-3 tabular-nums">
      <span role="img" aria-label={said} className="size-1.5 rounded-full" style={{ background: LINK_COLOR[link] }} />
      {link === "away" ? t("top.offlineShort") : m.online && m.latency ? `${m.latency} ms` : ""}
    </span>
  );
}

/** A group's mark: a folder as a folder's in it is (the user, 2026-11-08: a group is only a folder),
 *  shut or open as the group is. */
/** A folder by its path, in 未归类 or a group listed at another machine: ~ for home. */
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
    if (to && to !== name) await api.patchWorkspace(id, { name: to }).catch((e) => toast(t("err.rename"), { error: true, description: errText(e) }));
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
/** Drawn again only as its props change, not with every draw of the list: a page of the phone's
 *  sheet turned drew every row, its menu and its tip, again (the user, 2026-11-08). */
const SessionRow = memo(function SessionRow({ s, where, depth, flip, selected, onSelect, chatCode, working, asked, machineShown }: { s: Session; where?: string; depth: number; flip: string; selected: boolean; onSelect: (id: string) => void; chatCode: () => void; working?: boolean; asked?: boolean; machineShown?: boolean }) {
  useLang();
  const machines = useMachines();
  const unread = useUnread(s.id);
  const now = useMinute();
  const title = titleOf(s);
  // A subagent shown is at work, whatever discovery says of its process.
  // A confirmation its agent asked for waits on you too, while it is not at work.
  const base = working ? "running" : lampOf(s);
  const status = asked && !BUSY.has(base) ? "waiting" : base;
  const told = working ? statusText("running") : status !== base ? statusText("waiting") : sessionStatusText(s);
  // How long ago it was last active (its turn ended, it was last written to), but in a turn. Unread,
  // it is in ink: no grey dot for it (the user, 2026-11-06).
  const fresh = unread && status === "idle";
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
  const [menuAt, setMenuAt] = useMenuAt();
  const [renaming, setRenaming] = useState(false);
  const rest = useRef<number | undefined>(undefined);
  const lp = useLongPress(() => {
    const r = document.activeElement?.closest?.("[role=treeitem]")?.getBoundingClientRect();
    setMenuAt(r ? { x: r.left + 40, y: r.bottom } : { x: 40, y: 40 });
  });
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
      if (!own(e)) return;
      e.preventDefault();
      setMenuAt({ x: e.clientX, y: e.clientY });
    },
  };
  // Dragged to a chat, it is named in that chat's composer (chat.tsx): with a mouse, as a finger's
  // long press is the menu. Nothing changes as it is dragged (the user, 2026-11-06).
  const drag = fine() && !renaming
    ? {
        draggable: true,
        onDragStart: (e: React.DragEvent) => {
          e.dataTransfer.setData(SESSION_DRAG, s.id);
          e.dataTransfer.setData("text/plain", s.id);
          e.dataTransfer.effectAllowed = "copyLink";
        },
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
  const bots = s.bots?.length ? <Here bots={s.bots} /> : null;
  // At another machine: its face on the logo; not connected now, a grey ring for its lamp.
  const machine = useMachine(s.machine);
  // In a group at another machine (machineShown), one of this machine's.
  const here = !!machineShown && !s.machine && !s.subagent;
  // Gone a moment (a proxy's cut, a Mac waking) is not shown as gone (machines.tsx useLate).
  const away = useLate(!!s.offline) && !busy;
  const end = slot || mark || lamp || bots || away ? (
    <span className="flex items-center gap-1.5">
      {bots}
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
        aria={`${title}, ${told}${s.color ? `, ${t("color.tagged", { color: t(`color.${s.color}`) })}` : ""}${unread ? `, ${t("status.unread")}` : ""}`}
        icon={
          <Tip label={[agentName(s.agent), machine?.name, here && t("new.local"), s.subagent && t("row.subagent"), away ? t("status.offline") : status !== "idle" && told].filter(Boolean).join(" · ")} side="bottom">
            <span className="relative grid">
              <Logo agent={s.agent} />
              {machine && !s.subagent && !machineShown && <MachineBadge m={machine} />}
              {/* This machine's, in a group at another (a coordinator there): Iridium's mark on its
                  logo, as that machine's would have its own (the user, 2026-11-08). */}
              {here && (
                <span role="img" aria-label={t("new.local")} className="absolute -right-1 -bottom-1 grid place-items-center rounded-[3px]" style={{ boxShadow: "0 0 0 1.5px var(--row-bg, var(--paper))" }}>
                  <IrMark size={10} ground="var(--ink-2)" />
                </span>
              )}
              {/* A subagent: no word for it, a small agent badge on the logo's corner (the user, 2026-11-06). */}
              {s.subagent && (
                <span role="img" aria-label={t("row.subagent")} className="absolute -right-1 -bottom-1 grid size-[11px] place-items-center rounded-full text-ink-2" style={{ background: "var(--row-bg, var(--paper))" }}>
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
              {where && (
                <Tip label={tilde(s.cwd)} side="bottom">
                  <span className="ml-1.5 max-w-[35%] shrink-0 truncate text-xs font-normal text-ink-3">{where.startsWith("/") ? tilde(where) : shownPath(where, machines).label}</span>
                </Tip>
              )}
            </span>
          )
        }
        end={end}
        // data-session: a note may be dropped on it (notes.tsx).
        extra={{ ...lp, ...hover, ...drag, onClickCapture: lp.onClickCapture, "data-session": s.id }}
      />
      <SessionMenu s={s} at={menuAt} close={() => setMenuAt(null)} rename={() => setRenaming(true)} />
    </>
  );
});

/** The bots whose chat is in this session now, between the name and the time (the user,
 *  2026-11-08): each one's avatar as big as the agent's logo, a bot on its corner; three at most and
 *  +N for the rest. Panels are not marked. */
function Here({ bots }: { bots: string[] }) {
  const terms = useTerminalList();
  const who = bots.map((id) => terms.find((x) => x.id === id) ?? { id, name: "", avatar: undefined });
  return (
    <Tip label={who.map((b) => b.name || "Bot").join("、")} side="bottom">
      <span className="flex shrink-0 items-center gap-1.5">
        {who.slice(0, 3).map((b) => (
          <Avatar key={b.id} src={b.avatar} name={b.name || "Bot"} size={16} badge={Bot} badgeSize={10} />
        ))}
        {who.length > 3 && <span className="text-xs text-ink-3 tabular-nums">+{who.length - 3}</span>}
      </span>
    </Tip>
  );
}

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
function FolderRow({ f, ws, label, open, onToggle, newHere, plus }: { f: FolderNode; ws: number; label: ReactNode; open: boolean; onToggle: () => void; newHere: () => void; plus: ReactNode }) {
  const machines = useMachines();
  const [at, setAt] = useMenuAt();
  const lp = useLongPress(() => {
    const r = document.activeElement?.closest?.("[role=treeitem]")?.getBoundingClientRect();
    setAt(r ? { x: r.left + 40, y: r.bottom } : { x: 40, y: 40 });
  });
  const done = (what: string) => () => toast(what);
  const failed = (e: unknown) => toast(t("err.action"), { error: true, description: errText(e) });
  const color = useFolderColors()[f.path];
  const tags: Tags = { value: color, set: (c) => void api.folderColor({ path: f.path, color: c as Color }).catch(failed) };
  // Archive first, as in a session's menu (the user, 2026-11-06).
  const items = [
    ...(f.sessions.length
      ? [{ icon: <Archive size={15} />, label: t("menu.archiveHere", { n: f.sessions.length }), run: () => void Promise.all(f.sessions.map((s) => api.patch(s.id, { archived: true }))).then(done(t("toast.archived")), failed) }]
      : []),
    { icon: <Plus size={15} />, label: t("menu.newHere"), run: newHere },
    { icon: <Copy size={15} />, label: t("menu.copyPath"), run: () => void navigator.clipboard.writeText(shownPath(f.path, machines).real).then(done(t("toast.copied")), failed) },
    { icon: open ? <Folder size={15} /> : <FolderOpen size={15} />, label: open ? t("menu.fold") : t("menu.unfold"), run: onToggle },
  ];
  const anchor = at && { getBoundingClientRect: () => DOMRect.fromRect({ x: at.x, y: at.y, width: 0, height: 0 }) };
  return (
    <>
      <Row
        depth={0}
        flip={`f:${f.key}`}
        expanded={open}
        onClick={onToggle}
        // No chevron: the folder itself is open or shut (the user, 2026-11-06).
        icon={<FolderMark open={open} color={color} remote={remotePath(f.path)} />}
        label={label}
        end={plus && <span className="flex items-center" onClick={(e) => e.stopPropagation()} onKeyDown={(e) => e.stopPropagation()}>{plus}</span>}
        extra={{ ...lp, onContextMenu: (e: React.MouseEvent) => own(e) && (e.preventDefault(), setAt({ x: e.clientX, y: e.clientY })), onClickCapture: lp.onClickCapture }}
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
  const anchor = at && { getBoundingClientRect: () => DOMRect.fromRect({ x: at.x, y: at.y, width: 0, height: 0 }) };
  return (
    <Menu.Root open={at !== null} onOpenChange={(o) => !o && close()}>
      <Menu.Portal>
        <Menu.Positioner anchor={anchor} side="bottom" align="start" sideOffset={4} collisionPadding={8} className="z-50">
          <MenuItems
            items={items}
            tags={sessionTags(s)}
            foot={<PushFoot levels={sessionLevels(s)} i={items.length} />}
            tail={<BotItems s={s} i={items.length + 2} />}
          />
        </Menu.Positioner>
      </Menu.Portal>
    </Menu.Root>
  );
}

/** At a session's menu's foot, one item per bot whose chat is elsewhere: that chat moved to this
 *  session, as its /use would (the user, 2026-11-08); in the list's menu and the chat's ⋯. A Bot no one has written to yet has no chat
 *  to move: its item is there, greyed, saying so. */
export function BotItems({ s, i }: { s: Session; i: number }) {
  // Claude's sessions only for a Bot that may use Claude (access.Agent).
  const claude = !s.agent || s.agent === "claude";
  const bots = useTerminalList().filter((x) => x.bot && !s.bots?.includes(x.id) && (!claude || x.rules.claudeOwner));
  if (s.archived || !bots.length) return null;
  const bring = (b: Terminal) =>
    void api.bringBot(s.id, b.id).then(
      () => toast(t("toast.botHere", { name: b.name || "Bot" })),
      (e) => toast(t("err.action"), { error: true, description: errText(e) }),
    );
  return (
    <div style={{ "--i": i } as CSSProperties} className="mt-1.5 flex flex-col border-t border-edge pt-1.5">
      {bots.map((b) => (
        <Menu.Item key={b.id} disabled={b.chatless} onClick={() => bring(b)} className="flex h-8 cursor-default items-center gap-2.5 rounded-md px-2.5 text-base outline-none select-none data-highlighted:bg-fill data-disabled:opacity-40 pointer-coarse:h-11">
          <Avatar src={b.avatar} name={b.name || "Bot"} size={15} badge={Bot} badgeSize={9} />
          <span className="truncate">{t(b.chatless ? "menu.botChatless" : "menu.bringBot", { name: b.name || "Bot" })}</span>
        </Menu.Item>
      ))}
    </div>
  );
}

/** 通知 at a menu's foot, while push is on (push.ts). */
function PushFoot({ levels, i }: { levels: PushLevel[]; i: number }) {
  const push = usePushMenus();
  return push && <push.m.PushSubmenu st={push.st} levels={levels} i={i} />;
}

/** A colour tag and how to set it ("" takes it off). */
export type Tags = { value?: Color; set: (c: Color | "") => void };

type MenuItem = { icon: ReactNode; label: string; run: () => void };

/** A menu's items; lead: before the children, at its top; foot: after the items, over the tags;
 *  tail: under the tags, the very end. */
export function MenuItems({ lead = [], items, tags, children, foot, tail }: { lead?: MenuItem[]; items: MenuItem[]; tags?: Tags; children?: ReactNode; foot?: ReactNode; tail?: ReactNode }) {
  const item = (it: MenuItem, i: number) => (
    <Menu.Item key={it.label} onClick={it.run} style={{ "--i": i } as CSSProperties} className="flex h-8 cursor-default items-center gap-2.5 rounded-md px-2.5 text-base outline-none select-none data-highlighted:bg-fill pointer-coarse:h-11">
      <span className="text-ink-2">{it.icon}</span>
      {it.label}
    </Menu.Item>
  );
  return (
    <Menu.Popup finalFocus={false} className="g-unfold glass thick float min-w-48 rounded-lg p-1.5 text-ink outline-none">
      <div className="g-stagger flex flex-col">
        {lead.map(item)}
        {children}
        {items.map((it, i) => item(it, lead.length + i))}
        {foot}
        {tags && <TagDots tags={tags} i={lead.length + items.length + (foot ? 1 : 0)} />}
        {tail}
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
