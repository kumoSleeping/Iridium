// The jump box (⌘K, Ctrl+K, or the sidebar's search key; Rhodium spec web-maximize "跳转框"): type to
// go to a session by its name, its folder or what was said last. Nothing typed: those opened lately
// in this tab, then the rest as the sidebar lists them. The archive (sessions with no shown marker)
// is found too, last and faint. Loaded only once it first opens.
import { Archive, Search } from "lucide-react";
import { useState } from "react";
import type { Session } from "./api/types.gen";
import { t } from "./i18n";
import { openChat, recent } from "./route";
import { useList } from "./store";
import { flat, type Tree } from "./tree";
import { cn, LogoLamp, tilde, titleOf } from "./ui";

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
  const [typed, setTyped] = useState("");
  const [pick, setPick] = useState(0);
  const list = jumpList(all, flat(tree), typed, open, recent());
  const sel = Math.min(pick, list.length - 1);
  const go = (id: string) => (close(), id !== open && openChat(id));
  return (
    <div className="g-fade fixed inset-0 z-80 bg-ink/10" onMouseDown={(e) => e.target === e.currentTarget && (e.preventDefault(), close())}>
      <div role="dialog" aria-label={t("nav.search")} onMouseDown={(e) => e.target instanceof HTMLInputElement || e.preventDefault()} className="g-unfold glass thick float mx-auto mt-[12vh] w-[min(36rem,calc(100%-1.5rem))] overflow-hidden rounded-xl text-ink" style={{ transformOrigin: "top center" }}>
        <label className="flex items-center gap-2.5 px-3.5 shadow-[0_0.5px_0_var(--edge)]">
          <Search size={16} className="shrink-0 text-ink-3" />
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
            className="no-ring h-12 min-w-0 flex-1 bg-transparent text-md placeholder:text-ink-3"
          />
        </label>
        <div id="jump-list" role="listbox" aria-label={t("nav.sessions")} className="max-h-[22rem] overflow-y-auto p-1.5">
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
              className={cn("flex h-10 w-full items-center gap-2.5 rounded-md px-2.5 text-left", i === sel && "bg-fill", s.archived && "opacity-55")}
            >
              <LogoLamp agent={s.agent} status={s.status} />
              <span className="min-w-0 shrink truncate text-base">{titleOf(s)}</span>
              <span className="min-w-0 flex-1 truncate text-xs text-ink-3">{s.last ? `${s.last.role === "user" ? (s.last.from && s.last.from !== "网页" ? `${s.last.from}: ` : t("jump.you")) : ""}${s.last.text}` : tilde(s.cwd)}</span>
              {s.archived && <Archive size={13} className="shrink-0 text-ink-4" aria-label={t("nav.archive")} />}
            </button>
          ))}
          {!list.length && <p className="m-0 px-3 py-2.5 text-sm text-ink-3">{t("jump.none")}</p>}
        </div>
      </div>
    </div>
  );
}
