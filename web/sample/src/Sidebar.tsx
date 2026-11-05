// The one list: workspace → folder → session. No panel, no cards, no headings; groups split by space,
// chevrons in their own column, one truncated line per row, colour only in logos and lamps, the
// selected row only bolder. A folder with one session is not shown; the session sits in its place.
import { ChevronRight, Folder, Inbox, Network, Plus, Search, Settings } from "lucide-react";
import { useRef, type CSSProperties, type ReactNode } from "react";
import { pick, type Session, type Workspace } from "./data";
import { t, useLang } from "./i18n";
import { useFlip } from "./motion";
import { agentName, cn, IconKey, LogoLamp, statusText, Tip, useLongPress } from "./ui";

const STEP = 18;

type Props = {
  workspaces: Workspace[];
  expanded: Set<string>;
  toggle: (id: string) => void;
  selected: string;
  onSelect: (id: string) => void;
  phone: boolean;
  createKey: (trigger: React.ReactElement) => ReactNode;
  wsPlus: (ws: Workspace, trigger: React.ReactElement) => ReactNode;
  plusShown: (id: string) => boolean;
  setHoverWs: (id: string | null) => void;
  me: ReactNode;
  needs: number;
};

export function Sidebar(p: Props) {
  const lang = useLang();
  const list = useRef<HTMLDivElement>(null);
  const shape = p.workspaces.map((w) => `${w.id}:${p.expanded.has(w.id)}:${w.sessions.map((s) => s.id).join(",")}:${w.folders.map((f) => `${f.id}${p.expanded.has(f.id)}`).join(",")}`).join("|");
  useFlip(list, shape);

  const head = (
    <div className={cn("flex shrink-0 items-center gap-0.5 px-2", p.phone ? "glass absolute inset-x-0 top-0 z-20 h-13 pt-[env(safe-area-inset-top)] shadow-[0_0.5px_0_var(--edge)]" : "h-12")}>
      {p.createKey(
        <button type="button" aria-label={t("nav.new")} className="g-press grid size-8 place-items-center rounded-md text-ink-2 hover:bg-fill hover:text-ink pointer-coarse:size-10">
          <Plus size={18} />
        </button>,
      )}
      <IconKey label={t("nav.search")} tipSide="bottom">
        <Search size={16} />
      </IconKey>
      <IconKey label={t("nav.topology")} tipSide="bottom">
        <Network size={16} />
      </IconKey>
    </div>
  );

  return (
    <aside className={cn("relative flex h-full flex-col", p.phone ? "w-full" : "w-[288px] shrink-0")}>
      {head}
      <div ref={list} className={cn("relative min-h-0 flex-1 overflow-y-auto px-2 pb-3", p.phone && "pt-15")} role="tree" aria-label="sessions">
        <Row depth={0} flip="needs" icon={<Inbox size={16} className="text-ink-2" />} label={t("nav.needsYou")} end={<Count n={p.needs} />} />
        {p.workspaces.map((w) => {
          const open = p.expanded.has(w.id);
          const n = w.sessions.length + w.folders.reduce((a, f) => a + f.sessions.length, 0);
          const hover = p.plusShown(w.id);
          return (
            <div key={w.id} className="mt-4" role="group">
              <WsRow
                w={w}
                name={pick(w.name, lang)}
                open={open}
                onToggle={() => p.toggle(w.id)}
                setHover={(h) => p.setHoverWs(h ? w.id : null)}
                end={hover ? p.wsPlus(w, <button type="button" onClick={(e) => e.stopPropagation()} aria-label={t("nav.newHere", { ws: pick(w.name, lang) })} className="g-pop g-press -mr-1 grid size-6 place-items-center rounded-sm text-ink-2 hover:bg-fill-2 hover:text-ink pointer-coarse:size-8"><Plus size={15} /></button>) : <Count n={n} faint />}
              />
              {open && (
                <>
                  {w.sessions.map((s) => (
                    <SessionRow key={s.id} s={s} depth={1} selected={p.selected === s.id} onSelect={p.onSelect} />
                  ))}
                  {w.folders.map((f) =>
                    f.sessions.length === 1 ? (
                      <SessionRow key={f.sessions[0].id} s={f.sessions[0]} depth={1} selected={p.selected === f.sessions[0].id} onSelect={p.onSelect} />
                    ) : (
                      <div key={f.id} role="group">
                        <Row
                          depth={1}
                          flip={`f:${f.id}`}
                          chevron={p.expanded.has(f.id) ? "open" : "closed"}
                          onClick={() => p.toggle(f.id)}
                          icon={<Folder size={15} className="text-ink-3" />}
                          label={f.name}
                          end={<Count n={f.sessions.length} faint />}
                        />
                        {p.expanded.has(f.id) && f.sessions.map((s) => <SessionRow key={s.id} s={s} depth={2} selected={p.selected === s.id} onSelect={p.onSelect} />)}
                      </div>
                    ),
                  )}
                </>
              )}
            </div>
          );
        })}
      </div>
      <div className="flex shrink-0 items-center justify-between gap-2 px-2 pt-1 pb-[max(8px,env(safe-area-inset-bottom))]">
        <IconKey label={t("nav.settings")}>
          <Settings size={16} />
        </IconKey>
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

function Row({
  depth,
  flip,
  chevron,
  icon,
  label,
  end,
  selected,
  onClick,
  aria,
  extra,
}: {
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
}) {
  return (
    <div
      data-flip={flip}
      role="treeitem"
      aria-selected={selected}
      aria-expanded={chevron ? chevron === "open" : undefined}
      aria-label={aria}
      tabIndex={0}
      onClick={onClick}
      onKeyDown={(e) => (e.key === "Enter" || e.key === " ") && (e.preventDefault(), onClick?.())}
      className="group flex h-[30px] cursor-default items-center rounded-md pr-2 select-none hover:bg-fill hover:[--row-bg:var(--fill)] pointer-coarse:h-10"
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

function WsRow({ w, name, open, onToggle, end, setHover }: { w: Workspace; name: string; open: boolean; onToggle: () => void; end: ReactNode; setHover: (h: boolean) => void }) {
  const lp = useLongPress(() => setHover(true));
  return (
    <div onMouseEnter={() => setHover(true)} onMouseLeave={() => setHover(false)} {...lp}>
      <Row depth={0} flip={`w:${w.id}`} chevron={open ? "open" : "closed"} onClick={onToggle} label={<span className="font-medium">{name}</span>} end={end} />
    </div>
  );
}

function SessionRow({ s, depth, selected, onSelect }: { s: Session; depth: number; selected: boolean; onSelect: (id: string) => void }) {
  const lang = useLang();
  const title = pick(s.title, lang);
  return (
    <Row
      depth={depth}
      flip={`s:${s.id}`}
      selected={selected}
      onClick={() => onSelect(s.id)}
      aria={`${title}, ${statusText(s.status)}${s.unread ? `, ${t("status.unread")}` : ""}`}
      icon={
        <Tip label={s.status === "idle" ? agentName(s.agent) : `${agentName(s.agent)} · ${statusText(s.status)}`} side="bottom">
          <span className="grid">
            <LogoLamp agent={s.agent} status={s.status} />
          </span>
        </Tip>
      }
      label={title}
      end={s.unread ? <span className="unread" aria-hidden /> : undefined}
    />
  );
}
