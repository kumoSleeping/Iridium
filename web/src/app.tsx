// The layout (plan "网页"): one main pane, the chat, on a computer and on a phone alike (the user,
// 2026-11-06). On a computer the sidebar always stands beside it, as wide as it is dragged; a window
// too narrow for both is a phone's, its sidebar a sheet from the foot in the composer's place
// (sheet.tsx), brought from the chat's ⋯ menu or by a swipe up; its foot, me and the settings key, a
// small sheet of its own, by a swipe up from the left quarter (the user, 2026-11-06). The settings are a
// dialog rising from their key, over the sidebar mostly, the chat staying under it. The jump box
// (⌘K) opens in the sidebar, not over the page. The chat, the settings and the jump box come only as
// they are opened, or the page is idle: the first screen stays light.
import { Dialog } from "@base-ui/react/dialog";
import { Toast } from "@base-ui/react/toast";
import { Tooltip } from "@base-ui/react/tooltip";
import { lazy, Suspense, useEffect, useLayoutEffect, useMemo, useRef, useState, type ComponentType, type CSSProperties } from "react";
import { queryClient } from "./api/client";
import { watchTurns } from "./chats";
import { CreatePopover, WsPlus } from "./create";
import { agentsQ, pushQ, useHome, useMe, useSettings, useTerminals, useUpdate, useWorkspaces } from "./data";
import { t, useLang } from "./i18n";
import { applyTheme, initialTheme, keepTheme, MeChip, MePanel, type ThemePref } from "./me";
import { motion, pageTurn } from "./motion";
import { prefetch } from "./prefetch";
import { close, go, onTurn, openChat, over, route, useRoute, type Route } from "./route";
import { atLeft, Sheet } from "./sheet";
import { Sidebar } from "./sidebar";
import { useConfirms, useList } from "./store";
import { follow } from "./stream";
import { ToastList, toasts } from "./toast";
import { buildTree, flat } from "./tree";
import { cn, kept, keep, params, PHONE, useMedia, useMinute } from "./ui";
import { watchUnread } from "./unread";
import { warmUp } from "./warm";

/** A part that comes only as it is opened, or the page is idle. Once its code came it is drawn
 *  straight, not through lazy, whose first draw shows the fallback even with the code at hand
 *  (Rhodium #386); which of the two is picked as it mounts, for good. load fetches and runs it. */
function part<P extends object>(get: () => Promise<ComponentType<P>>) {
  let code: ComponentType<P> | undefined;
  let coming: Promise<ComponentType<P>> | undefined;
  const load = () => (coming ??= get().then((c) => (code = c)).catch((e) => ((coming = undefined), Promise.reject(e))));
  const Lazy = lazy(() => load().then((c) => ({ default: c })));
  function Part(p: P) {
    const [C] = useState(() => code ?? Lazy);
    return <C {...p} />;
  }
  return Object.assign(Part, { load });
}
const ChatPage = part(() => import("./chat/chat").then((m) => m.ChatPage));
const SettingsPage = part(() => import("./settings").then((m) => m.SettingsPage));
const TerminalsPage = part(() => import("./terminals").then((m) => m.TerminalsPage));
const Jump = part(() => import("./jump").then((m) => m.Jump));
export const loadChat = ChatPage.load;
/** The settings and the jump box, their code run, the settings' answers asked: once idle, or a
 *  pointer on their key. */
const loadPanels = () => {
  for (const p of [SettingsPage, TerminalsPage, Jump]) void p.load().catch(() => {});
  void queryClient.prefetchQuery(agentsQ);
  void queryClient.prefetchQuery(pushQ);
};

// Once idle, the later parts' files into the cache (vite.config.ts LATER); then their code runs from
// it, so a turn that ends fetches its chat on the list too, and the settings and the jump box open at
// once. Not after one failed: a module that failed to load stays failed in the browser.
void prefetch().then((all) => {
  if (!all) return;
  void loadChat().catch(() => {});
  loadPanels();
});

/** Whether a sidebar group is open, as this device left it: all are, but 归档. */
function useExpanded() {
  const [over, setOver] = useState<Record<string, boolean>>(() => {
    try {
      return JSON.parse(kept("expanded") ?? "{}");
    } catch {
      return {};
    }
  });
  // Open unless folded, but the archive and a workflow run's agents: those wait to be opened.
  const expanded = (k: string) => over[k] ?? (k !== "archive" && !k.startsWith("run:"));
  const toggle = (k: string) =>
    setOver((o) => {
      const n = { ...o, [k]: !(o[k] ?? k !== "archive") };
      keep("expanded", JSON.stringify(n));
      return n;
    });
  return { expanded, toggle };
}

export function App() {
  useLang();
  const phone = useMedia(PHONE);
  // On a phone, whether the sidebar's sheet is out, and how tall it is: the chat keeps its newest
  // above it while it is out.
  const [sheetOut, setSheetOut] = useState(() => phone && route().name !== "chat");
  const [sheetH, setSheetH] = useState(0);
  // With a chat open, the sidebar's foot is a sheet of its own, one out at a time.
  const [footOut, setFootOut] = useState(false);
  const [footH, setFootH] = useState(0);
  const systemDark = useMedia("(prefers-color-scheme: dark)");
  const [theme, setTheme] = useState<ThemePref>(initialTheme);
  useEffect(() => applyTheme(theme, systemDark), [theme, systemDark]);
  const r = useRoute();
  // Under the settings' dialog, the page open before it.
  const [under, setUnder] = useState<Route>(over(r) ? { name: "home" } : r);
  if (!over(r) && r !== under) setUnder(r);
  const open = under.name === "chat" ? under.id : null;
  // On a phone the settings are a sheet too, as the me sheet is (the user, 2026-11-06): their page
  // drawn still as it goes down, once they have been open.
  const setsOut = phone && over(r);
  const [setsH, setSetsH] = useState(0);
  const [setsPage, setSetsPage] = useState<"settings" | "terminals" | null>(null);
  if (over(r) && setsPage !== (r.name === "terminals" ? "terminals" : "settings")) setSetsPage(r.name === "terminals" ? "terminals" : "settings");
  const below = !phone ? 0 : setsOut ? setsH : sheetOut ? sheetH : footOut && open ? footH : 0;
  const list = useList();
  const ws = useWorkspaces();
  const update = useUpdate();
  useSettings(); // asked early: the chat's quota bars read it
  useHome(); // paths show ~ for it
  useMe();
  useTerminals(); // a message's source shows its terminal's avatar
  const confirms = useConfirms();
  const { expanded, toggle } = useExpanded();
  // Each minute too: a subagent long quiet is let go.
  const minute = useMinute();
  const tree = useMemo(() => buildTree(list ?? [], ws.data ?? [], t("nav.unsorted"), Math.max(minute, Date.now())), [list, ws.data, t("nav.unsorted"), minute]);

  useEffect(() => follow(open), [open]);
  // Unread, the done reminder, and the chats of turns that end off screen.
  const unread = useRef<ReturnType<typeof watchUnread>>(undefined);
  useEffect(() => {
    unread.current = watchUnread();
    return () => unread.current?.stop();
  }, []);
  const turns = useRef(new Map<string, string>());
  useEffect(() => {
    if (!list) return;
    unread.current?.take(list);
    watchTurns(list, turns.current, open);
  }, [list]);
  // The chats you may open next, warmed (src/warm.ts).
  useEffect(() => warmUp(list, tree, open), [tree, open]);

  // On a phone a chat opened from the sheet puts it away, and with nothing open it is out, to stay:
  // there is nothing else to see.
  useEffect(() => {
    if (phone) setSheetOut(!open);
    setFootOut(false);
  }, [open, phone]);
  const setSheet = (o: boolean) => (setSheetOut(o || !open), o && setFootOut(false));
  const setFoot = (o: boolean) => (setFootOut(o), o && setSheetOut(false));
  // Put away, the me sheet is drawn afresh once it is down: its first page, not renaming.
  const [footRound, setFootRound] = useState(0);
  useEffect(() => {
    if (footOut) return;
    const t = setTimeout(() => setFootRound((n) => n + 1), motion.dur(3));
    return () => clearTimeout(t);
  }, [footOut]);

  // Chat to chat on a computer, the main pane swaps quickly, drifting the way the sidebar lists them.
  const now = useRef({ tree, phone });
  now.current = { tree, phone };
  useEffect(() => onTurn(pageTurn(() => flat(now.current.tree).map((s) => s.id), () => !now.current.phone)), []);

  // The jump box, in the sidebar (the user, 2026-11-06); on a phone ⌘K brings the sheet out too, and
  // ⌘\ brings it out or puts it away.
  const [jumping, setJumping] = useState(params.get("open") === "jump");
  const jumpingNow = useRef(jumping);
  jumpingNow.current = jumping;
  useEffect(() => {
    const key = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && !e.shiftKey && !e.altKey && e.key.toLowerCase() === "k") {
        e.preventDefault();
        const to = !jumpingNow.current;
        setJumping(to);
        if (to && now.current.phone) setSheet(true);
      } else if ((e.metaKey || e.ctrlKey) && !e.shiftKey && !e.altKey && e.key === "\\" && now.current.phone) {
        e.preventDefault();
        setSheet(!sheetNow.current);
      } else if (e.altKey && !e.metaKey && !e.ctrlKey && (e.key === "ArrowUp" || e.key === "ArrowDown") && open && !e.defaultPrevented) {
        const all = flat(tree);
        const i = all.findIndex((s) => s.id === open);
        const next = all[i + (e.key === "ArrowDown" ? 1 : -1)];
        if (next) (e.preventDefault(), go({ name: "chat", id: next.id }, true));
      }
    };
    addEventListener("keydown", key);
    return () => removeEventListener("keydown", key);
  }, [tree, open]);
  const sheetNow = useRef(sheetOut);
  sheetNow.current = sheetOut;
  const setThemeKept = (v: ThemePref) => (setTheme(v), keepTheme(v));

  const onSettings = () => (over(r) ? close(under) : go({ name: "settings" }));
  const me = <MeChip theme={theme} setTheme={setThemeKept} defaultOpen={params.get("open") === "me"} initialPage={params.get("open") === "perms" ? "perms" : "main"} />;
  const sidebar = (
    <Sidebar
      tree={tree}
      expanded={expanded}
      toggle={toggle}
      selected={open ?? ""}
      onSelect={(id) => (openChat(id), phone && setSheetOut(false))}
      noFoot={phone && !!open}
      phone={phone}
      searching={jumping}
      search={
        <Suspense>
          <Jump close={() => setJumping(false)} tree={tree} open={open} />
        </Suspense>
      }
      onSearch={() => setJumping((j) => !j)}
      settings={over(r)}
      onSettings={onSettings}
      updateDot={!!update.data?.available}
      confirms={confirms}
      onConfirm={(c) => (c.session && list?.some((s) => s.id === c.session) ? openChat(c.session) : go({ name: "terminals" }, over(r)))}
      chatCode={() => void loadChat().catch(() => {})}
      panelsCode={loadPanels}
      createKey={(trigger) => <CreatePopover trigger={trigger} />}
      wsPlus={(w, trigger, o, setO) => <WsPlus key={w.key} w={{ id: w.id, name: w.name, path: w.root }} trigger={trigger} open={o} setOpen={setO} opened={(id) => openChat(id)} />}
      me={me}
    />
  );

  const main = (
    // On a phone a tap on the chat puts the sheet away, the composer back (the user, 2026-11-06);
    // not one on its top bar: its ⋯ opens over the sheet (the user, 2026-11-06).
    <main className={cn("relative h-full min-w-0 flex-1 bg-paper", !phone && "g-turn")} onClickCapture={phone && (((sheetOut || footOut) && open) || setsOut) ? (e) => !(e.target as Element).closest?.("header") && (setsOut ? close(under) : (setSheetOut(false), setFootOut(false))) : undefined}>
      <Suspense fallback={<Waiting />}>
        {under.name === "chat" ? (
          <ChatPage key={under.id} id={under.id} phone={phone} sheet={below} onSheet={phone ? () => setSheet(true) : undefined} onMe={phone ? () => setFoot(true) : undefined} />
        ) : (
          <Empty below={below} />
        )}
      </Suspense>
    </main>
  );

  // The settings: a small dialog as wide as the me popover, right above their key at the sidebar's
  // foot, its right edge on the key's, rising out of it (the user, 2026-11-06); the terminals its page.
  // As the me popover is (the user, 2026-11-06): no shade over the page, which stays live; a press
  // outside puts it away, but one on its own key, which does that itself.
  const settingsAt = useSettingsAt(over(r) && !phone);
  const setsBody = (page: "settings" | "terminals") => (
    <Suspense fallback={<Waiting />}>
      {page === "terminals" ? (
        <div key="terminals" className="g-slide h-full">
          <TerminalsPage onBack={() => (history.state?.depth > 1 ? history.back() : go({ name: "settings" }, true))} />
        </div>
      ) : (
        <div key="settings" className="g-slide h-full" data-dir="back">
          <SettingsPage theme={theme} setTheme={setThemeKept} />
        </div>
      )}
    </Suspense>
  );
  const settings = phone ? (
    <Sheet open={setsOut} setOpen={(o) => !o && over(route()) && close(under)} size="tall" from={() => false} label={t("nav.settings")} onHeight={setSetsH}>
      <div className="absolute inset-0">{setsPage && setsBody(setsPage)}</div>
    </Sheet>
  ) : (
    <Dialog.Root
      open={over(r)}
      modal={false}
      onOpenChange={(o, d) => {
        if (o || !over(route())) return;
        const e = d.event as (Event & { relatedTarget?: EventTarget | null }) | undefined;
        const key = (x?: EventTarget | null) => !!(x as Element | null)?.closest?.("[data-settings-key]");
        if ((d.reason === "outside-press" || d.reason === "focus-out") && (key(e?.target) || key(e?.relatedTarget))) return d.cancel();
        close(under);
      }}
    >
      <Dialog.Portal>
        <Dialog.Popup
          aria-label={t("nav.settings")}
          style={settingsAt}
          className="g-dialog glass thick float fixed z-50 w-[min(288px,calc(100vw-16px))] overflow-hidden rounded-lg text-ink outline-none"
        >
          {setsBody(r.name === "terminals" ? "terminals" : "settings")}
        </Dialog.Popup>
      </Dialog.Portal>
    </Dialog.Root>
  );

  return (
    <Toast.Provider toastManager={toasts}>
      <Tooltip.Provider delay={450} closeDelay={60}>
        <div className="flex h-full overflow-hidden" data-still={params.get("still") === "1" || undefined}>
          {!phone && sidebar}
          {main}
          {phone && (
            <Sheet open={sheetOut} setOpen={setSheet} size={!open ? "full" : jumping ? "tall" : "half"} from={(x) => !atLeft(x)} label={t("nav.sessions")} onHeight={setSheetH}>
              {sidebar}
            </Sheet>
          )}
          {phone && open && (
            <Sheet open={footOut} setOpen={setFoot} size="fit" from={atLeft} label={t("me.title")} onHeight={setFootH}>
              {/* The me popover's body, not the sidebar's foot: the settings a row in it (the user, 2026-11-06). */}
              <div className="px-2 pb-2" onPointerEnter={loadPanels}>
                <MePanel key={footRound} wide theme={theme} setTheme={setThemeKept} onSettings={() => (setFootOut(false), onSettings())} updateDot={!!update.data?.available} />
              </div>
            </Sheet>
          )}
        </div>
        {settings}
        <Toast.Portal>
          <Toast.Viewport className={cn("fixed top-[60px] left-1/2 z-60 flex w-[min(400px,calc(100vw-24px))] -translate-x-1/2 flex-col items-center gap-2 outline-none")}>
            <ToastList onJump={(id) => openChat(id)} />
          </Toast.Viewport>
        </Toast.Portal>
      </Tooltip.Provider>
    </Toast.Provider>
  );
}

/** The main pane with nothing open: the faint mark alone, in the middle of what the phone's sheet
 *  leaves free. */
function Empty({ below }: { below: number }) {
  return (
    <div className="grid h-full place-items-center transition-[padding] duration-(--dur-3) ease-(--ease-out)" style={{ paddingBottom: below }} aria-hidden>
      <svg viewBox="0 0 64 64" width="44" height="44" className="text-fill-2">
        <rect width="64" height="64" rx="14" fill="currentColor" />
        <path d="M20 18h6v28h-6zM32 28h5.6v3.2c1.4-2.4 3.6-3.6 6.4-3.6v5.8c-3.8 0-6.2 1.6-6.2 6V46H32z" fill="var(--paper)" />
      </svg>
    </div>
  );
}

/** While a part's code comes: the top bar's place, frosted and empty. */
function Waiting() {
  return <div className="glass absolute inset-x-0 top-0 h-12 shadow-[0_0.5px_0_var(--edge)] pointer-coarse:h-13" />;
}

/** Where the settings stand, open: above their key (sidebar.tsx data-settings-key), its right edge
 *  on the key's, as tall as fits there up to 600px; with too little room or no key in sight, at the
 *  window's bottom left. */
function useSettingsAt(open: boolean): CSSProperties | undefined {
  const [at, setAt] = useState<CSSProperties>();
  useLayoutEffect(() => {
    if (!open) return;
    const w = Math.min(288, innerWidth - 16);
    const b = document.querySelector("[data-settings-key]")?.getBoundingClientRect();
    const seen = b && b.width > 0 && b.right > 0 && b.left < innerWidth && b.bottom <= innerHeight;
    const room = seen ? b.top - 16 : 0;
    if (!seen || room < 360) {
      setAt({ left: 8, bottom: "max(8px, env(safe-area-inset-bottom))", height: "min(600px, calc(100dvh - 16px))" });
      return;
    }
    setAt({ left: Math.max(8, Math.min(innerWidth - w - 8, b.right - w)), bottom: innerHeight - b.top + 8, height: Math.min(600, room) });
  }, [open]);
  return at;
}
