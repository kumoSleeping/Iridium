// The one list (plan "网页", "风格"; web/sample's design): workspace → folder → session. No panel, no
// cards, no headings; groups split by space, chevrons in their own column, colour only in logos and
// lamps, the selected row only bolder. A session's row has two truncated lines, as Rhodium's: its name
// and, where the tree does not say it, its folder; under them the last thing said, yours after 你：
// (the user's decision, 2026-11-05); out of a turn, the first line ends with how long ago it was. 需要你 on top when anything
// waits on you, 归档 folded at the bottom. A workspace's row: its name a size up, its chevron only
// under the mouse, a faint "+" at its end for a new session there. A row's menu (right click,
// long-press): pin, rename, stop, archive. The mouse resting on a row fetches its chat ahead.
import { Menu } from "@base-ui/react/menu";
import { Archive, ArchiveRestore, Bot, ChevronRight, Copy, Folder, FolderOpen, Inbox, KeyRound, Link2, Pencil, Pin, PinOff, Plus, Search, Settings, ShieldOff, Square, Workflow as WorkflowIcon } from "lucide-react";
import { Fragment, useLayoutEffect, useRef, useState, type CSSProperties, type ReactElement, type ReactNode } from "react";
import { api } from "./api/client";
import type { Confirm, Session } from "./api/types.gen";
import { ahead, behind } from "./chats";
import { t } from "./i18n";
import { useFlip } from "./motion";
import { useLink } from "./store";
import type { Folder as FolderNode, Tree, WsNode } from "./tree";
import { errText, toast } from "./toast";
import { useSaidLine } from "./data";
import { agentName, ago, cn, IconKey, keep, kept, Lamp, Logo, statusText, tilde, Tip, titleOf, useLongPress, useMinute } from "./ui";
import { useUnread } from "./unread";

const STEP = 18;
const BUSY = new Set(["running", "compacting", "starting"]);

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
  settings: boolean;
  onSettings: () => void;
  updateDot: boolean;
  /** What agents' ir asked this panel for (invites, tokens): under 需要你 too. */
  confirms: Confirm[];
  onConfirm: (c: Confirm) => void;
  createKey: (trigger: ReactElement) => ReactNode;
  wsPlus: (ws: WsNode, trigger: ReactElement, open: boolean, setOpen: (o: boolean) => void) => ReactNode;
  me: ReactNode;
  /** The chat's code, fetched as a row is pressed. */
  chatCode: () => void;
};

/** How wide the sidebar may be dragged, and at first (the user, 2026-11-06). */
const WIDTH = { min: 232, max: 440, start: 292 };
const WIDTH_KEY = "ir.sidebarWidth";
const clampWidth = (w: number) => Math.round(Math.min(WIDTH.max, Math.max(WIDTH.min, w)));

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
  const list = useRef<HTMLDivElement>(null);
  const [width, setWidth] = useState(() => clampWidth(Number(kept(WIDTH_KEY)) || WIDTH.start));
  const [plusOpen, setPlusOpen] = useState<string | null>(null);
  const needs = p.tree.needs.length + p.confirms.length;
  const shape = [needs, ...p.tree.workspaces.map((w) => `${w.key}:${p.expanded(w.key)}:${w.rows.map((r) => (r.kind === "session" ? r.s.id : `${r.f.key}${p.expanded(r.f.key)}${r.f.sessions.length}`)).join(",")}`), p.expanded("archive"), p.tree.archived.length, [...p.tree.kids.values()].flat().map((s) => s.id).join(","), [...p.tree.runs.values()].flat().map((r) => `${r.id}${p.expanded(`run:${r.id}`)}${r.working.map((s) => s.id)}`).join(",")].join("|");
  useFlip(list, shape);
  const scrolled = useRef(0);
  useLayoutEffect(() => void (!p.searching && list.current && (list.current.scrollTop = scrolled.current)), [p.searching]);
  // A session's row, and under it the subagents it has at work.
  const row = (s: Session, depth: number, flip = `s:${s.id}`) => (
    <Fragment key={flip}>
      <SessionRow s={s} where={p.tree.where.get(s.id)} depth={depth} flip={flip} selected={p.selected === s.id} onSelect={p.onSelect} chatCode={p.chatCode} />
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

  const head = (
    <div className={cn("flex shrink-0 items-center gap-0.5 px-2", p.phone ? "glass absolute inset-x-0 top-0 z-20 h-13 pt-[env(safe-area-inset-top)] shadow-[0_0.5px_0_var(--edge)]" : "h-12")}>
      {p.createKey(
        <button type="button" aria-label={t("nav.newWorkspace")} className="g-press grid size-8 place-items-center rounded-md text-ink-2 hover:bg-fill hover:text-ink data-popup-open:bg-fill pointer-coarse:size-10">
          <Plus size={18} />
        </button>,
      )}
      <IconKey label={`${t("nav.search")} · ${t("nav.searchKey")}`} tipSide="bottom" onClick={p.onSearch} aria-pressed={p.searching} className={p.searching ? "bg-fill text-ink" : undefined}>
        <Search size={16} />
      </IconKey>
      <LinkDot />
    </div>
  );

  return (
    <aside className={cn("relative flex h-full flex-col", p.phone ? "w-full" : "shrink-0")} style={p.phone ? undefined : { width }}>
      {!p.phone && <Divider width={width} setWidth={setWidth} />}
      {head}
      {/* While searching, the jump box in the tree's place; the tree stays mounted, hidden, and
          comes back scrolled as it was. */}
      {p.searching && <div className={cn("flex min-h-0 flex-1 flex-col", p.phone && "pt-15")}>{p.search}</div>}
      <div ref={list} hidden={p.searching} onScroll={(e) => void (scrolled.current = e.currentTarget.scrollTop)} className={cn("relative min-h-0 flex-1 overflow-y-auto px-2 pb-3 [scrollbar-gutter:stable]", p.phone && "pt-15")} role="tree" aria-label={t("nav.sessions")}>
        {needs > 0 && (
          <div role="group">
            <Row depth={0} flip="needs" chevron={p.expanded("needs") ? "open" : "closed"} onClick={() => p.toggle("needs")} icon={<Inbox size={16} className="text-ink-2" />} label={t("nav.needsYou")} end={<Count n={needs} />} />
            {p.expanded("needs") && (
              <>
                {p.confirms.map((c) => (
                  <Row
                    key={`c:${c.id}`}
                    depth={1}
                    flip={`c:${c.id}`}
                    onClick={() => p.onConfirm(c)}
                    icon={c.kind === "invite" ? <Link2 size={15} className="text-need" /> : <KeyRound size={15} className="text-need" />}
                    label={
                      <>
                        {c.kind === "invite" ? t("confirm.invite") : t("confirm.token")}
                        {c.name && <span className="ml-1.5 text-ink-3">{c.name}</span>}
                      </>
                    }
                    aria={`${c.kind === "invite" ? t("confirm.invite") : t("confirm.token")} ${c.name ?? ""}, ${t("status.needs")}`}
                  />
                ))}
                {p.tree.needs.map((s) => row(s, 1, `n:${s.id}`))}
              </>
            )}
          </div>
        )}
        {p.tree.workspaces.map((w, i) => {
          const open = p.expanded(w.key);
          return (
            <div key={w.key} className={i > 0 || needs ? "mt-4" : "mt-1"} role="group">
              <WsRow
                flip={`w:${w.key}`}
                name={w.name}
                open={open}
                onToggle={() => p.toggle(w.key)}
                // Not its count: the "+" that opens a session here, always at the row's end, faint
                // till the row is hovered (the user, 2026-11-06).
                end={
                  w.id !== 0 &&
                  p.wsPlus(
                    w,
                    <button type="button" onClick={(e) => e.stopPropagation()} aria-label={t("nav.newHere", { ws: w.name })} className="g-press -mr-1 grid size-6 place-items-center rounded-sm text-ink-4 group-hover:text-ink hover:bg-fill-2 data-popup-open:bg-fill-2 data-popup-open:text-ink pointer-coarse:size-8 pointer-coarse:text-ink-3">
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
                    row(r.s, 1)
                  ) : (
                    <div key={r.f.key} role="group">
                      <FolderRow
                        f={r.f}
                        label={w.id === 0 ? tilde(r.f.path) : r.f.name}
                        open={p.expanded(r.f.key)}
                        onToggle={() => p.toggle(r.f.key)}
                        newHere={() => setPlusOpen(r.f.key)}
                        // As a workspace's: not its count, the "+" that opens a session in the folder
                        // (the user, 2026-11-06).
                        plus={p.wsPlus(
                          { ...w, key: r.f.key, root: r.f.path },
                          <button type="button" onClick={(e) => e.stopPropagation()} aria-label={t("nav.newHere", { ws: r.f.name })} className="g-press -mr-1 grid size-6 place-items-center rounded-sm text-ink-4 group-hover:text-ink hover:bg-fill-2 data-popup-open:bg-fill-2 data-popup-open:text-ink pointer-coarse:size-8 pointer-coarse:text-ink-3">
                            <Plus size={15} />
                          </button>,
                          plusOpen === r.f.key,
                          (o) => setPlusOpen(o ? r.f.key : null),
                        )}
                      />
                      {p.expanded(r.f.key) && r.f.sessions.map((s) => row(s, 2))}
                    </div>
                  ),
                )}
            </div>
          );
        })}
        {p.tree.archived.length > 0 && (
          <div className="mt-4" role="group">
            <Row depth={0} flip="archive" expanded={p.expanded("archive")} onClick={() => p.toggle("archive")} icon={<TrashLid open={p.expanded("archive")} />} label={<span className="text-ink-2">{t("nav.archive")}</span>} end={<Count n={p.tree.archived.length} faint />} />
            {p.expanded("archive") && p.tree.archived.map((s) => row(s, 1))}
          </div>
        )}
        {!p.tree.workspaces.length && !needs && <div className="g-fade px-2 pt-10 text-center text-sm text-ink-3">{t("nav.empty")}</div>}
      </div>
      {/* Me and the settings key, right on the sidebar's ground at its foot: no card around them (the
          user, 2026-11-06). */}
      <div className={cn("flex h-12 shrink-0 items-center justify-between gap-2 px-2 pointer-coarse:h-14", p.phone && "mb-[env(safe-area-inset-bottom)]")}>
        <div className="min-w-0">{p.me}</div>
        <span className="relative shrink-0">
          <IconKey label={p.updateDot ? `${t("nav.settings")} · ${t("settings.updateReady")}` : t("nav.settings")} onClick={p.onSettings} aria-pressed={p.settings} className={p.settings ? "bg-fill text-ink" : undefined}>
            <Settings size={16} />
          </IconKey>
          {p.updateDot && <span className="g-pop pointer-events-none absolute top-1 right-1 size-2 rounded-full bg-err shadow-[0_0_0_1.5px_var(--paper)]" aria-hidden />}
        </span>
      </div>
    </aside>
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

/** A folder shut or open: both drawn, cross-faded (motion.css "swap"). */
function FolderMark({ open }: { open: boolean }) {
  return (
    <span data-on={open || undefined} className="g-swap text-ink-3" aria-hidden>
      <Folder size={15} />
      <FolderOpen size={15} />
    </span>
  );
}

/** reveal: hidden till the row is hovered or focused, its column kept so nothing moves; always
 *  there for a finger, which has no hover. */
function Chevron({ state, reveal }: { state?: "open" | "closed"; reveal?: boolean }) {
  return (
    <span className={cn("grid w-[18px] shrink-0 place-items-center text-ink-4", reveal && "opacity-0 transition-opacity duration-(--dur-2) ease-(--ease-out) group-hover:opacity-100 group-focus-visible:opacity-100 pointer-coarse:opacity-100")} aria-hidden>
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
  /** A workspace's own row (the user, 2026-11-06): a larger name and no chevron till the mouse is on
   *  it; then the chevron fades in and the name springs a little to the right. */
  reveal?: boolean;
};

function Row({ depth, flip, chevron, expanded, icon, label, end, selected, onClick, aria, extra, faded, sub, reveal }: RowProps) {
  const two = sub !== undefined;
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
      style={{ paddingLeft: depth * STEP } as CSSProperties}
      {...extra}
    >
      <Chevron state={chevron} reveal={reveal} />
      {icon && <span className={cn("mr-2 grid w-4 shrink-0 place-items-center", two && "h-5 pointer-coarse:h-[23px]")}>{icon}</span>}
      <span className="min-w-0 flex-1">
        <span className="flex min-w-0 items-center">
          <span
            className={cn(
              "min-w-0 flex-1",
              reveal ? "text-md transition-[translate] duration-(--dur-2) ease-(--ease-spring) motion-safe:group-hover:translate-x-[3px]" : "text-base pointer-coarse:text-md",
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

/** The link to the daemon, at the top right of the sidebar (the user, 2026-11-06): a dot green under
 *  0.5 s round trip, amber to 2 s, red beyond or cut off; the time beside it in ink. */
function LinkDot() {
  const link = useLink();
  const label = link.live === false ? t("top.offline") : link.rtt !== null ? t("top.online", { ms: link.rtt }) : t("top.connecting");
  const tone = link.live === false ? "bg-err" : link.rtt === null ? "bg-ink-4" : link.rtt < 500 ? "bg-run" : link.rtt <= 2000 ? "bg-need" : "bg-err";
  return (
    <Tip label={label} side="bottom">
      <span className="mr-1.5 ml-auto flex items-center gap-1.5 text-xs text-ink tabular-nums" role="status" aria-label={label}>
        <span className={cn("size-1.5 rounded-full transition-colors duration-(--dur-2)", tone)} />
        {link.live === false ? t("top.offlineShort") : link.rtt !== null ? `${link.rtt} ms` : ""}
      </span>
    </Tip>
  );
}

function WsRow({ flip, name, open, onToggle, end }: { flip: string; name: string; open: boolean; onToggle: () => void; end: ReactNode }) {
  // Its "+" popover is a portal, but React events bubble through it: a press in it must not fold
  // the workspace (the user's, 2026-11-05: only you fold or unfold).
  return <Row depth={0} flip={flip} chevron={open ? "open" : "closed"} reveal onClick={onToggle} label={<span className="font-medium">{name}</span>} end={end && <span className="flex items-center" onClick={(e) => e.stopPropagation()} onKeyDown={(e) => e.stopPropagation()}>{end}</span>} />;
}

/** A session's row: its logo and lamp, its name, unread or pinned at the end. The mouse resting on
 *  it 0.1 s, counted from its last move, or a press, fetches its chat ahead. */
function SessionRow({ s, where, depth, flip, selected, onSelect, chatCode, working }: { s: Session; where?: string; depth: number; flip: string; selected: boolean; onSelect: (id: string) => void; chatCode: () => void; working?: boolean }) {
  const unread = useUnread(s.id);
  const said = useSaidLine();
  const now = useMinute();
  const title = titleOf(s);
  // A subagent shown is at work, whatever discovery says of its process.
  const status = working ? "running" : s.status;
  // How long ago it was last active (its turn ended, it was last written to), but in a turn.
  const when = !BUSY.has(status) && s.updatedAt > 0 && !s.archived && <span className="text-xs whitespace-nowrap text-ink-4 tabular-nums">{ago(s.updatedAt, now, true)}</span>;
  const [menuAt, setMenuAt] = useState<{ x: number; y: number } | null>(null);
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
      e.preventDefault();
      setMenuAt({ x: e.clientX, y: e.clientY });
    },
  };
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
  ) : unread ? (
    <span className="unread" aria-hidden />
  ) : s.pinned ? (
    <Pin size={12} className="text-ink-4" aria-label={t("menu.pinned")} />
  ) : undefined;
  const lamp = status !== "idle";
  const end = when || mark || lamp ? (
    <span className="flex items-center gap-1.5">
      {when}
      {mark}
      <Lamp status={status} />
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
        aria={`${title}, ${statusText(status)}${unread ? `, ${t("status.unread")}` : ""}`}
        icon={
          <Tip label={[agentName(s.agent), s.subagent && t("row.subagent"), status !== "idle" && statusText(status)].filter(Boolean).join(" · ")} side="bottom">
            <span className="relative grid">
              <Logo agent={s.agent} />
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
            <>
              <span className="min-w-0 truncate">{title}</span>
              {where && (
                <Tip label={tilde(s.cwd)} side="bottom">
                  <span className="ml-1.5 max-w-[35%] shrink-0 truncate text-xs font-normal text-ink-3">{where.startsWith("/") ? tilde(where) : where}</span>
                </Tip>
              )}
            </>
          )
        }
        sub={status === "error" ? <span className="text-err">{statusText(s.status)}</span> : (said(s.last) ?? tilde(s.cwd))}
        end={end}
        extra={{ ...lp, ...hover, onClickCapture: lp.onClickCapture }}
      />
      <SessionMenu s={s} at={menuAt} close={() => setMenuAt(null)} rename={() => setRenaming(true)} />
    </>
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
      defaultValue={titleOf(s)}
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

/** A session's actions: pin, rename, stop its agent, archive; restore an archived one. */
export function sessionActions(s: Session, rename: () => void): { icon: ReactNode; label: string; run: () => void }[] {
  const run = (f: () => Promise<unknown>, done?: string) => () =>
    void f().then(
      () => done && toast(done),
      (e) => toast(t("err.action"), { error: true, description: errText(e) }),
    );
  return [
    s.pinned ? { icon: <PinOff size={15} />, label: t("menu.unpin"), run: run(() => api.patch(s.id, { pinned: false })) } : { icon: <Pin size={15} />, label: t("menu.pin"), run: run(() => api.patch(s.id, { pinned: true })) },
    { icon: <Pencil size={15} />, label: t("menu.rename"), run: rename },
    { icon: <Copy size={15} />, label: t("menu.copyId"), run: run(() => navigator.clipboard.writeText(s.id), t("toast.copied")) },
    ...(s.live ? [{ icon: <Square size={14} />, label: t("menu.stop"), run: run(() => api.stop(s.id), t("toast.stopped")) }] : []),
    ...(s.hosted ? [{ icon: <ShieldOff size={15} />, label: t("menu.clearRules"), run: run(() => api.clearRules(s.id), t("toast.cleared")) }] : []),
    s.archived
      ? { icon: <ArchiveRestore size={15} />, label: t("menu.unarchive"), run: run(() => api.patch(s.id, { archived: false })) }
      : { icon: <Archive size={15} />, label: t("menu.archive"), run: run(() => api.patch(s.id, { archived: true }), t("toast.archived")) },
  ];
}

/** A folder's row in a workspace: its name, the "+" for a session in it; its menu (right click,
 *  long-press): a session here, its path copied, open or folded, its sessions archived. */
function FolderRow({ f, label, open, onToggle, newHere, plus }: { f: FolderNode; label: ReactNode; open: boolean; onToggle: () => void; newHere: () => void; plus: ReactNode }) {
  const [at, setAt] = useState<{ x: number; y: number } | null>(null);
  const lp = useLongPress(() => {
    const r = document.activeElement?.closest?.("[role=treeitem]")?.getBoundingClientRect();
    setAt(r ? { x: r.left + 40, y: r.bottom } : { x: 40, y: 40 });
  });
  const done = (what: string) => () => toast(what);
  const failed = (e: unknown) => toast(t("err.action"), { error: true, description: errText(e) });
  const items = [
    { icon: <Plus size={15} />, label: t("menu.newHere"), run: newHere },
    { icon: <Copy size={15} />, label: t("menu.copyPath"), run: () => void navigator.clipboard.writeText(f.path).then(done(t("toast.copied")), failed) },
    { icon: open ? <Folder size={15} /> : <FolderOpen size={15} />, label: open ? t("menu.fold") : t("menu.unfold"), run: onToggle },
    ...(f.sessions.length
      ? [{ icon: <Archive size={15} />, label: t("menu.archiveHere", { n: f.sessions.length }), run: () => void Promise.all(f.sessions.map((s) => api.patch(s.id, { archived: true }))).then(done(t("toast.archived")), failed) }]
      : []),
  ];
  const anchor = at && { getBoundingClientRect: () => DOMRect.fromRect({ x: at.x, y: at.y, width: 0, height: 0 }) };
  return (
    <>
      <Row
        depth={1}
        flip={`f:${f.key}`}
        expanded={open}
        onClick={onToggle}
        // No chevron: the folder itself is open or shut (the user, 2026-11-06).
        icon={<FolderMark open={open} />}
        label={label}
        end={plus && <span className="flex items-center" onClick={(e) => e.stopPropagation()} onKeyDown={(e) => e.stopPropagation()}>{plus}</span>}
        extra={{ ...lp, onContextMenu: (e: React.MouseEvent) => (e.preventDefault(), setAt({ x: e.clientX, y: e.clientY })), onClickCapture: lp.onClickCapture }}
      />
      <Menu.Root open={at !== null} onOpenChange={(o) => !o && setAt(null)}>
        <Menu.Portal>
          <Menu.Positioner anchor={anchor} side="bottom" align="start" sideOffset={4} collisionPadding={8} className="z-50">
            <MenuItems items={items} />
          </Menu.Positioner>
        </Menu.Portal>
      </Menu.Root>
    </>
  );
}

function SessionMenu({ s, at, close, rename }: { s: Session; at: { x: number; y: number } | null; close: () => void; rename: () => void }) {
  const anchor = at && { getBoundingClientRect: () => DOMRect.fromRect({ x: at.x, y: at.y, width: 0, height: 0 }) };
  return (
    <Menu.Root open={at !== null} onOpenChange={(o) => !o && close()}>
      <Menu.Portal>
        <Menu.Positioner anchor={anchor} side="bottom" align="start" sideOffset={4} collisionPadding={8} className="z-50">
          <MenuItems items={sessionActions(s, rename)} />
        </Menu.Positioner>
      </Menu.Portal>
    </Menu.Root>
  );
}

export function MenuItems({ items, children }: { items: { icon: ReactNode; label: string; run: () => void }[]; children?: ReactNode }) {
  return (
    <Menu.Popup finalFocus={false} className="g-unfold glass thick float min-w-48 rounded-lg p-1.5 text-ink outline-none">
      <div className="g-stagger flex flex-col">
        {children}
        {items.map((it, i) => (
          <Menu.Item key={it.label} onClick={it.run} style={{ "--i": i } as CSSProperties} className="flex h-8 cursor-default items-center gap-2.5 rounded-md px-2.5 text-base outline-none select-none data-highlighted:bg-fill pointer-coarse:h-11">
            <span className="text-ink-2">{it.icon}</span>
            {it.label}
          </Menu.Item>
        ))}
      </div>
    </Menu.Popup>
  );
}
