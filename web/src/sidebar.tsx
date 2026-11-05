// The one list (plan "网页", "风格"; web/sample's design): workspace → folder → session. No panel, no
// cards, no headings; groups split by space, chevrons in their own column, one truncated line per
// row, colour only in logos and lamps, the selected row only bolder. 需要你 on top when anything
// waits on you, 归档 folded at the bottom. A row's menu (right click, long-press): pin, rename, stop,
// archive. The mouse resting on a row fetches its chat ahead.
import { Menu } from "@base-ui/react/menu";
import { Archive, ArchiveRestore, ChevronRight, Copy, Folder, Inbox, KeyRound, Link2, Network, Pencil, Pin, PinOff, Plus, Search, Settings, ShieldOff, Square } from "lucide-react";
import { useRef, useState, type CSSProperties, type ReactElement, type ReactNode } from "react";
import { api } from "./api/client";
import type { Confirm, Session } from "./api/types.gen";
import { ahead, behind } from "./chats";
import { t } from "./i18n";
import { useFlip } from "./motion";
import type { Tree, WsNode } from "./tree";
import { errText, toast } from "./toast";
import { agentName, cn, IconKey, LogoLamp, statusText, tilde, Tip, titleOf, useLongPress } from "./ui";
import { useUnread } from "./unread";

const STEP = 18;

type Props = {
  tree: Tree;
  expanded: (key: string) => boolean;
  toggle: (key: string) => void;
  selected: string;
  onSelect: (id: string) => void;
  phone: boolean;
  topology: boolean;
  onTopology: () => void;
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

export function Sidebar(p: Props) {
  const list = useRef<HTMLDivElement>(null);
  const [hoverWs, setHoverWs] = useState<string | null>(null);
  const [plusOpen, setPlusOpen] = useState<string | null>(null);
  const needs = p.tree.needs.length + p.confirms.length;
  const shape = [needs, ...p.tree.workspaces.map((w) => `${w.key}:${p.expanded(w.key)}:${w.rows.map((r) => (r.kind === "session" ? r.s.id : `${r.f.key}${p.expanded(r.f.key)}${r.f.sessions.length}`)).join(",")}`), p.expanded("archive"), p.tree.archived.length].join("|");
  useFlip(list, shape);
  const row = (s: Session, depth: number, flip = `s:${s.id}`) => <SessionRow key={flip} s={s} depth={depth} flip={flip} selected={p.selected === s.id} onSelect={p.onSelect} chatCode={p.chatCode} />;

  const head = (
    <div className={cn("flex shrink-0 items-center gap-0.5 px-2", p.phone ? "glass absolute inset-x-0 top-0 z-20 h-13 pt-[env(safe-area-inset-top)] shadow-[0_0.5px_0_var(--edge)]" : "h-12")}>
      {p.createKey(
        <button type="button" aria-label={t("nav.new")} className="g-press grid size-8 place-items-center rounded-md text-ink-2 hover:bg-fill hover:text-ink data-popup-open:bg-fill pointer-coarse:size-10">
          <Plus size={18} />
        </button>,
      )}
      <IconKey label={`${t("nav.search")} · ${t("nav.searchKey")}`} tipSide="bottom" onClick={p.onSearch}>
        <Search size={16} />
      </IconKey>
      <IconKey label={t("nav.topology")} tipSide="bottom" onClick={p.onTopology} aria-pressed={p.topology} className={p.topology ? "bg-fill text-ink" : undefined}>
        <Network size={16} />
      </IconKey>
    </div>
  );

  return (
    <aside className={cn("relative flex h-full flex-col", p.phone ? "w-full" : "w-[288px] shrink-0")}>
      {head}
      <div ref={list} className={cn("relative min-h-0 flex-1 overflow-y-auto px-2 pb-3", p.phone && "pt-15")} role="tree" aria-label={t("nav.sessions")}>
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
          const shown = hoverWs === w.key || plusOpen === w.key;
          return (
            <div key={w.key} className={i > 0 || needs ? "mt-4" : "mt-1"} role="group">
              <WsRow
                flip={`w:${w.key}`}
                name={w.name}
                open={open}
                onToggle={() => p.toggle(w.key)}
                setHover={(h) => setHoverWs((c) => (h ? w.key : c === w.key ? null : c))}
                end={
                  w.id !== 0 && shown ? (
                    p.wsPlus(
                      w,
                      <button type="button" onClick={(e) => e.stopPropagation()} aria-label={t("nav.newHere", { ws: w.name })} className="g-pop g-press -mr-1 grid size-6 place-items-center rounded-sm text-ink-2 hover:bg-fill-2 hover:text-ink pointer-coarse:size-8">
                        <Plus size={15} />
                      </button>,
                      plusOpen === w.key,
                      (o) => setPlusOpen(o ? w.key : null),
                    )
                  ) : (
                    <Count n={w.count} faint />
                  )
                }
              />
              {open &&
                w.rows.map((r) =>
                  r.kind === "session" ? (
                    row(r.s, 1)
                  ) : (
                    <div key={r.f.key} role="group">
                      <Row
                        depth={1}
                        flip={`f:${r.f.key}`}
                        chevron={p.expanded(r.f.key) ? "open" : "closed"}
                        onClick={() => p.toggle(r.f.key)}
                        icon={<Folder size={15} className="text-ink-3" />}
                        label={w.id === 0 ? tilde(r.f.name) : r.f.name}
                        end={<Count n={r.f.sessions.length} faint />}
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
            <Row depth={0} flip="archive" chevron={p.expanded("archive") ? "open" : "closed"} onClick={() => p.toggle("archive")} icon={<Archive size={15} className="text-ink-3" />} label={<span className="text-ink-2">{t("nav.archive")}</span>} end={<Count n={p.tree.archived.length} faint />} />
            {p.expanded("archive") && p.tree.archived.map((s) => row(s, 1))}
          </div>
        )}
        {!p.tree.workspaces.length && !needs && <div className="g-fade px-2 pt-10 text-center text-sm text-ink-3">{t("nav.empty")}</div>}
      </div>
      <div className="flex shrink-0 items-center justify-between gap-2 px-2 pt-1 pb-[max(8px,env(safe-area-inset-bottom))]">
        <span className="relative">
          <IconKey label={p.updateDot ? `${t("nav.settings")} · ${t("settings.updateReady")}` : t("nav.settings")} onClick={p.onSettings} aria-pressed={p.settings} className={p.settings ? "bg-fill text-ink" : undefined}>
            <Settings size={16} />
          </IconKey>
          {p.updateDot && <span className="g-pop pointer-events-none absolute top-1 right-1 size-2 rounded-full bg-err shadow-[0_0_0_1.5px_var(--paper)]" aria-hidden />}
        </span>
        <div className="min-w-0">{p.me}</div>
      </div>
    </aside>
  );
}

function Count({ n, faint }: { n: number; faint?: boolean }) {
  return <span className={cn("text-xs tabular-nums", faint ? "text-ink-4" : "text-ink-3")}>{n}</span>;
}

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
  icon?: ReactNode;
  label: ReactNode;
  end?: ReactNode;
  selected?: boolean;
  onClick?: () => void;
  aria?: string;
  extra?: Record<string, unknown>;
  faded?: boolean;
};

function Row({ depth, flip, chevron, icon, label, end, selected, onClick, aria, extra, faded }: RowProps) {
  return (
    <div
      data-flip={flip}
      role="treeitem"
      aria-selected={selected}
      aria-expanded={chevron ? chevron === "open" : undefined}
      aria-label={aria}
      tabIndex={0}
      onClick={onClick}
      onKeyDown={(e) => e.target === e.currentTarget && (e.key === "Enter" || e.key === " ") && (e.preventDefault(), onClick?.())}
      className={cn("group relative flex h-[30px] cursor-default items-center rounded-md pr-2 select-none hover:bg-fill hover:[--row-bg:var(--fill)] pointer-coarse:h-10", faded && "opacity-60")}
      style={{ paddingLeft: depth * STEP } as CSSProperties}
      {...extra}
    >
      <Chevron state={chevron} />
      {icon && <span className="mr-2 grid w-4 shrink-0 place-items-center">{icon}</span>}
      <span className={cn("min-w-0 flex-1 truncate text-base pointer-coarse:text-md", selected ? "font-semibold text-ink" : "text-ink")}>{label}</span>
      {end && <span className="ml-2 flex shrink-0 items-center">{end}</span>}
    </div>
  );
}

function WsRow({ flip, name, open, onToggle, end, setHover }: { flip: string; name: string; open: boolean; onToggle: () => void; end: ReactNode; setHover: (h: boolean) => void }) {
  const lp = useLongPress(() => setHover(true));
  return (
    <div onMouseEnter={() => setHover(true)} onMouseLeave={() => setHover(false)} {...lp}>
      <Row depth={0} flip={flip} chevron={open ? "open" : "closed"} onClick={onToggle} label={<span className="font-medium">{name}</span>} end={end} />
    </div>
  );
}

/** A session's row: its logo and lamp, its name, unread or pinned at the end. The mouse resting on
 *  it 0.1 s, counted from its last move, or a press, fetches its chat ahead. */
function SessionRow({ s, depth, flip, selected, onSelect, chatCode }: { s: Session; depth: number; flip: string; selected: boolean; onSelect: (id: string) => void; chatCode: () => void }) {
  const unread = useUnread(s.id);
  const title = titleOf(s);
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
  const end = s.archived ? (
    <IconKey label={t("menu.unarchive")} onClick={restore} className="-mr-1 size-6 text-ink-3 pointer-coarse:size-8">
      <ArchiveRestore size={14} />
    </IconKey>
  ) : unread ? (
    <span className="unread" aria-hidden />
  ) : s.pinned ? (
    <Pin size={12} className="text-ink-4" aria-label={t("menu.pinned")} />
  ) : undefined;
  return (
    <>
      <Row
        depth={depth}
        flip={flip}
        selected={selected}
        onClick={() => !renaming && onSelect(s.id)}
        faded={s.archived}
        aria={`${title}, ${statusText(s.status)}${unread ? `, ${t("status.unread")}` : ""}`}
        icon={
          <Tip label={s.status === "idle" ? agentName(s.agent) : `${agentName(s.agent)} · ${statusText(s.status)}`} side="bottom">
            <span className="grid">
              <LogoLamp agent={s.agent} status={s.status} />
            </span>
          </Tip>
        }
        label={
          renaming ? (
            <Rename s={s} done={() => setRenaming(false)} />
          ) : s.subagent ? (
            <>
              <span className="mr-1.5 text-sm text-ink-4">{t("row.subagent")}</span>
              {title}
            </>
          ) : (
            title
          )
        }
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
