// The jump box (⌘K, Ctrl+K, or the sidebar's search key; Rhodium spec web-maximize "跳转框"): type to
// go to a session by its name, its folder or what was said last. Nothing typed: those opened lately
// in this tab, then the rest as the sidebar lists them. The archive (sessions with no shown marker)
// is found too, last and faint. In the sidebar only, under its head, its list in the tree's place;
// the page beside it stays as it is (the user, 2026-11-06). A press outside the sidebar or Esc
// closes it. Loaded only once it first opens.
import { Archive, Search } from "lucide-react";
import { useEffect, useRef, useState, type CSSProperties } from "react";
import type { Session } from "./api/types.gen";
import { t } from "./i18n";
import { openChat, recent } from "./route";
import { useSaidLine } from "./data";
import { useList } from "./store";
import { flat, type Tree } from "./tree";
import { cn, lampOf, LogoLamp, tilde, titleOf } from "./ui";

/** The sessions for what was typed: names that start with it, then names or folders that have it,
 *  then those whose last words do; archived last. */
export function jumpList(all: Session[], order: Session[], typed: string, open: string | null, lately: string[]): Session[] {
  const q = typed.trim().toLowerCase();
  const live = [...order, ...all.filter((s) => !s.archived && !order.includes(s))];
  if (!q) {
    const rest = live.filter((s) => s.id !== open);
    const seen = lately.flatMap((id) => rest.filter((s) => s.id === id));
    return [...seen, ...rest.filter((s) => !seen.includes(s))];
  }
  const rank = (s: Session) => {
    const name = titleOf(s).toLowerCase();
    if (name.startsWith(q)) return 0;
    if (name.includes(q) || s.cwd.toLowerCase().includes(q)) return 1;
    if ((s.last?.text ?? "").toLowerCase().includes(q)) return 2;
    return 3;
  };
  const ranked = (xs: Session[]) => [0, 1, 2].flatMap((k) => xs.filter((s) => rank(s) === k));
  return [...ranked(live), ...ranked(all.filter((s) => s.archived))];
}

export function Jump({ close, tree, open }: { close: () => void; tree: Tree; open: string | null }) {
  const all = useList() ?? [];
  const said = useSaidLine();
  const [typed, setTyped] = useState("");
  const [pick, setPick] = useState(0);
  const list = jumpList(all.filter((s) => !tree.hidden.has(s.id)), flat(tree), typed, open, recent());
  const sel = Math.min(pick, list.length - 1);
  const go = (id: string) => (close(), id !== open && openChat(id));
  const root = useRef<HTMLDivElement>(null);
  const shut = useRef(close);
  shut.current = close;
  // A press anywhere outside the sidebar it sits in closes it.
  useEffect(() => {
    const box = root.current?.closest("aside") ?? root.current;
    const down = (e: PointerEvent) => box && !box.contains(e.target as Node) && shut.current();
    document.addEventListener("pointerdown", down, true);
    return () => document.removeEventListener("pointerdown", down, true);
  }, []);
  return (
    <div ref={root} role="search" onMouseDown={(e) => e.target instanceof HTMLInputElement || e.preventDefault()} className="g-stagger flex min-h-0 flex-1 flex-col text-ink">
      <label className="mx-2 flex h-8 shrink-0 items-center gap-2 rounded-md bg-fill px-2.5 pointer-coarse:h-10" style={{ "--i": 0 } as CSSProperties}>
        <Search size={15} className="shrink-0 text-ink-3" />
        <input
          autoFocus
          role="combobox"
          aria-expanded
          aria-controls="jump-list"
          aria-activedescendant={list[sel] && `jump-${list[sel].id}`}
          aria-label={t("nav.search")}
          placeholder={t("jump.placeholder")}
          value={typed}
          onChange={(e) => (setTyped(e.target.value), setPick(0))}
          onKeyDown={(e) => {
            if (e.nativeEvent.isComposing || e.keyCode === 229) return;
            const by = { ArrowDown: 1, ArrowUp: -1 }[e.key];
            if (by && list.length) setPick((sel + by + list.length) % list.length);
            else if (e.key === "Enter" && list[sel]) go(list[sel].id);
            else if (e.key === "Escape") (e.stopPropagation(), close());
            else if (e.key !== "Tab") return;
            e.preventDefault();
          }}
          className="no-ring h-full min-w-0 flex-1 bg-transparent text-base placeholder:text-ink-3 pointer-coarse:text-md"
        />
      </label>
      <div id="jump-list" role="listbox" aria-label={t("nav.sessions")} className="no-bar mt-1.5 min-h-0 flex-1 overflow-y-auto px-2 pb-3" style={{ "--i": 1 } as CSSProperties}>
        {list.map((s, i) => (
          <button
            key={s.id}
            id={`jump-${s.id}`}
            type="button"
            role="option"
            aria-selected={i === sel}
            tabIndex={-1}
            ref={(el) => void (i === sel && el?.scrollIntoView({ block: "nearest" }))}
            onPointerMove={() => i === sel || setPick(i)}
            onClick={() => go(s.id)}
            className={cn("flex h-[30px] w-full cursor-default items-center gap-2 rounded-md px-2 text-left pointer-coarse:h-10", i === sel && "bg-fill", s.archived && "opacity-55")}
          >
            <LogoLamp agent={s.agent} status={lampOf(s)} />
            <span className="min-w-0 shrink truncate text-base">{titleOf(s)}</span>
            <span className="min-w-0 flex-1 truncate text-xs text-ink-3">{said(s.last, s) ?? tilde(s.cwd)}</span>
            {s.archived && <Archive size={13} className="shrink-0 text-ink-4" aria-label={t("nav.archive")} />}
          </button>
        ))}
        {!list.length && <p className="m-0 px-2 py-2 text-sm text-ink-3">{t("jump.none")}</p>}
      </div>
    </div>
  );
}
