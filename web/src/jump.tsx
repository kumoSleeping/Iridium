// The jump box (⌘K, Ctrl+K, or the sidebar's search key; Rhodium spec web-maximize "跳转框"): type to
// go to a session by its name, its folder or what was said last. Nothing typed: those opened lately
// in this tab, then the rest as the sidebar lists them. The archive (sessions with no shown marker)
// is found too, last and faint. Typing also queries unsynced sessions held by connected external
// host apps (DSH Desktop) on demand; picking one adopts and opens it. In the sidebar only, under
// its head, its list in the tree's place; the page beside it stays as it is (the user, 2026-11-06).
// A press outside the sidebar or Esc closes it. Loaded only once it first opens.
import { Archive, Search } from "lucide-react";
import { useEffect, useMemo, useRef, useState, type CSSProperties } from "react";
import { api } from "./api/client";
import { useQuery } from "./api/query";
import type { ExternalSession, Session } from "./api/types.gen";
import { useMachines, useSaidLine } from "./data";
import { t } from "./i18n";
import { openChat, recent } from "./route";
import { useList } from "./store";
import { errText, toast } from "./toast";
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
  const machines = useMachines();
  const said = useSaidLine();
  const [typed, setTyped] = useState("");
  const [debounced, setDebounced] = useState("");
  const [pick, setPick] = useState(0);
  const [adopting, setAdopting] = useState("");

  useEffect(() => {
    const q = typed.trim();
    if (!q) {
      setDebounced("");
      return;
    }
    const id = setTimeout(() => setDebounced(q), 200);
    return () => clearTimeout(id);
  }, [typed]);

  // Driven by what is typed inside the search box (popdata.test.ts meta.inside).
  const extQ = useQuery({
    queryKey: ["external-sessions", debounced],
    queryFn: ({ signal }) => api.searchExternal(debounced, 30, signal),
    enabled: debounced.length > 0,
    staleTime: 15_000,
    meta: { inside: true },
  });

  const known = useMemo(() => new Set(all.map((s) => s.id)), [all]);
  const list = jumpList(all.filter((s) => !tree.unlisted.has(s.id)), flat(tree), typed, open, recent());
  const ext = useMemo(
    () => (debounced ? (extQ.data ?? []).filter((s) => !known.has(s.id)) : []),
    [debounced, extQ.data, known],
  );
  const total = list.length + ext.length;
  const sel = total ? Math.min(pick, total - 1) : -1;

  const go = (id: string) => (close(), id !== open && openChat(id));
  const adopt = async (s: ExternalSession) => {
    if (adopting) return;
    setAdopting(s.id);
    try {
      await api.adoptExternal(s);
      close();
      if (s.id !== open) openChat(s.id);
    } catch (e) {
      toast(t("err.adopt"), { error: true, description: errText(e) });
    } finally {
      setAdopting("");
    }
  };

  const machineName = (id?: string) => {
    if (!id) return "Iridium";
    return machines.find((m) => m.id === id)?.name ?? id;
  };

  const root = useRef<HTMLDivElement>(null);
  const shut = useRef(close);
  shut.current = close;
  // A press anywhere outside the sidebar it sits in closes it (except the search trigger key,
  // which sits outside <aside> in the mobile sheet header and toggles Jump itself).
  useEffect(() => {
    const box = root.current?.closest("aside") ?? root.current;
    const down = (e: PointerEvent) => {
      const el = e.target as Element | null;
      if (el?.closest?.("[data-search-key]")) return;
      if (box && !box.contains(e.target as Node)) shut.current();
    };
    document.addEventListener("pointerdown", down, true);
    return () => document.removeEventListener("pointerdown", down, true);
  }, []);

  // The row picked is kept in sight as the pick changes, not at each draw: the list drawn again
  // (any session's news) pulled a list scrolled by hand back to it.
  const pickedId =
    sel < 0 ? undefined : sel < list.length ? `jump-${list[sel]!.id}` : `jump-ext-${ext[sel - list.length]!.id}`;
  useEffect(() => void (pickedId && document.getElementById(pickedId)?.scrollIntoView({ block: "nearest" })), [pickedId]);

  return (
    <div ref={root} role="search" onMouseDown={(e) => e.target instanceof HTMLInputElement || e.preventDefault()} className="g-stagger flex min-h-0 flex-1 flex-col text-ink">
      <label className="mx-2 flex h-8 shrink-0 items-center gap-2 rounded-md bg-fill px-2.5 pointer-coarse:h-10" style={{ "--i": 0 } as CSSProperties}>
        <Search size={15} className="shrink-0 text-ink-3" />
        <input
          autoFocus
          role="combobox"
          aria-expanded
          aria-controls="jump-list"
          aria-activedescendant={pickedId}
          aria-label={t("nav.search")}
          placeholder={t("jump.placeholder")}
          value={typed}
          onChange={(e) => (setTyped(e.target.value), setPick(0))}
          onKeyDown={(e) => {
            if (e.nativeEvent.isComposing || e.keyCode === 229) return;
            const by = { ArrowDown: 1, ArrowUp: -1 }[e.key];
            if (by && total) setPick((sel + by + total) % total);
            else if (e.key === "Enter" && sel >= 0) {
              if (sel < list.length) go(list[sel]!.id);
              else void adopt(ext[sel - list.length]!);
            } else if (e.key === "Escape") (e.stopPropagation(), close());
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
        {ext.length > 0 && (
          <>
            <div className="flex items-center justify-between px-2 pt-2.5 pb-1 text-[11px] font-medium text-ink-3">
              <span>{t("jump.unsynced")}</span>
              <span className="text-ink-4">DSH Desktop</span>
            </div>
            {ext.map((s, j) => {
              const i = list.length + j;
              const place = [machineName(s.machine), s.cwd ? tilde(s.cwd) : ""].filter(Boolean).join(" ");
              return (
                <button
                  key={`${s.machine ?? ""}:${s.id}`}
                  id={`jump-ext-${s.id}`}
                  type="button"
                  role="option"
                  aria-selected={i === sel}
                  tabIndex={-1}
                  disabled={adopting === s.id}
                  onPointerMove={() => i === sel || setPick(i)}
                  onClick={() => void adopt(s)}
                  className={cn("flex h-[30px] w-full cursor-default items-center gap-2 rounded-md px-2 text-left pointer-coarse:h-10", i === sel && "bg-fill")}
                >
                  <LogoLamp agent={s.agent} status="idle" />
                  <span className="min-w-0 shrink truncate text-base text-ink-2">{s.title || s.id}</span>
                  <span className="min-w-0 flex-1 truncate text-xs text-ink-3">{place}</span>
                  <span className="shrink-0 text-[11px] text-ink-3">{adopting === s.id ? t("jump.adopting") : t("jump.adopt")}</span>
                </button>
              );
            })}
          </>
        )}
        {!total && !extQ.isFetching && <p className="m-0 px-2 py-2 text-sm text-ink-3">{t("jump.none")}</p>}
      </div>
    </div>
  );
}
