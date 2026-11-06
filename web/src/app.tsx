// The layout (plan "网页"): one main pane, the chat, on a computer and on a phone alike (the user,
// 2026-11-06). On a computer the sidebar stands beside it, as wide as it is dragged, or is put away
// (⌘\) for the chat the whole window; on a portrait touch screen it is a drawer from the left
// (drawer.tsx), and on a phone a sheet from the foot in the composer's place (sheet.tsx), brought by
// the key at the top left. The settings are a
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
import { Drawer, SideKey } from "./drawer";
import { agentsQ, pushQ, useHome, useMe, useSettings, useTerminals, useUpdate, useWorkspaces } from "./data";
import { t, useLang } from "./i18n";
import { applyTheme, initialTheme, keepTheme, MeChip, type ThemePref } from "./me";
import { pageTurn } from "./motion";
import { prefetch } from "./prefetch";
import { close, go, onTurn, openChat, over, route, useRoute, type Route } from "./route";
import { Sheet } from "./sheet";
import { Sidebar } from "./sidebar";
import { useSecret } from "./secrets";
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
const SecretDialog = part(() => import("./secret").then((m) => m.SecretDialog));
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
  void SecretDialog.load().catch(() => {});
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

/** Where the sidebar is always a drawer: a phone, or a touch screen held upright. */
const NARROW = `${PHONE}, (orientation: portrait) and (pointer: coarse)`;

export function App() {
  useLang();
  const phone = useMedia(PHONE);
  const narrow = useMedia(NARROW);
  // On a computer, the sidebar beside the chat or put away, as this device left it.
  const [docked, setDocked] = useState(() => kept("sidebar") !== "away");
  const drawer = narrow || !docked;
  const [drawerOpen, setDrawerOpen] = useState(() => narrow && route().name !== "chat");
  // On a phone, how tall the sheet is: the chat keeps its newest above it while it is out.
  const [sheetH, setSheetH] = useState(0);
  const dock = (on: boolean) => (setDocked(on), keep("sidebar", on ? "" : "away"), setDrawerOpen(false));
  const systemDark = useMedia("(prefers-color-scheme: dark)");
  const [theme, setTheme] = useState<ThemePref>(initialTheme);
  useEffect(() => applyTheme(theme, systemDark), [theme, systemDark]);
  const r = useRoute();
  // Under the settings' dialog, the page open before it.
  const [under, setUnder] = useState<Route>(over(r) ? { name: "home" } : r);
  if (!over(r) && r !== under) setUnder(r);
  const open = under.name === "chat" ? under.id : null;
  const list = useList();
  const ws = useWorkspaces();
  const update = useUpdate();
  useSettings(); // asked early: the chat's quota bars read it
  useHome(); // paths show ~ for it
  useMe();
  useTerminals(); // a message's source shows its terminal's avatar
  const confirms = useConfirms();
  const secret = useSecret();
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

  // On a phone a chat opened from the drawer puts it away, and back at nothing open it comes out;
  // on a computer it stays beside the chat till put away.
  useEffect(() => {
    if (narrow) setDrawerOpen(!open);
  }, [open]);

  // Chat to chat on a computer, the main pane swaps quickly, drifting the way the sidebar lists them.
  const now = useRef({ tree, phone, drawer });
  now.current = { tree, phone, drawer };
  useEffect(() => onTurn(pageTurn(() => flat(now.current.tree).map((s) => s.id), () => !now.current.phone)), []);

  // The jump box, in the sidebar (the user, 2026-11-06); with the sidebar a drawer, ⌘K brings it
  // out too. ⌘\ puts the sidebar away on a computer, or docks it again.
  const [jumping, setJumping] = useState(params.get("open") === "jump");
  const jumpingNow = useRef(jumping);
  jumpingNow.current = jumping;
  useEffect(() => {
    const key = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && !e.shiftKey && !e.altKey && e.key.toLowerCase() === "k") {
        e.preventDefault();
        const to = !jumpingNow.current;
        setJumping(to);
        if (to && now.current.drawer) setDrawerOpen(true);
      } else if ((e.metaKey || e.ctrlKey) && !e.shiftKey && !e.altKey && e.key === "\\") {
        e.preventDefault();
        if (!narrowNow.current) dock(!dockedNow.current);
        else setDrawerOpen((o) => !o);
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
  const narrowNow = useRef(narrow);
  narrowNow.current = narrow;
  const dockedNow = useRef(docked);
  dockedNow.current = docked;
  const setThemeKept = (v: ThemePref) => (setTheme(v), keepTheme(v));

  const sidebar = (
    <Sidebar
      tree={tree}
      expanded={expanded}
      toggle={toggle}
      selected={open ?? ""}
      onSelect={(id) => (openChat(id), narrow && setDrawerOpen(false))}
      phone={phone}
      searching={jumping}
      search={
        <Suspense>
          <Jump close={() => setJumping(false)} tree={tree} open={open} />
        </Suspense>
      }
      onSearch={() => setJumping((j) => !j)}
      settings={over(r)}
      onSettings={() => (over(r) ? close(under) : go({ name: "settings" }))}
      updateDot={!!update.data?.available}
      confirms={confirms}
      onConfirm={(c) => (c.session && list?.some((s) => s.id === c.session) ? openChat(c.session) : go({ name: "terminals" }, over(r)))}
      chatCode={() => void loadChat().catch(() => {})}
      panelsCode={loadPanels}
      createKey={(trigger) => <CreatePopover trigger={trigger} />}
      wsPlus={(w, trigger, o, setO) => <WsPlus key={w.key} w={{ id: w.id, name: w.name, path: w.root }} trigger={trigger} open={o} setOpen={setO} opened={(id) => openChat(id)} />}
      me={<MeChip theme={theme} setTheme={setThemeKept} defaultOpen={params.get("open") === "me"} initialPage={params.get("open") === "perms" ? "perms" : "main"} />}
      drawer={drawer}
      onDock={narrow ? undefined : () => dock(true)}
    />
  );

  const main = (
    // On a phone a tap on the chat puts the sheet away, the composer back (the user, 2026-11-06).
    <main className={cn("relative h-full min-w-0 flex-1 bg-paper", !phone && "g-turn")} onClickCapture={phone && drawerOpen && open ? () => setDrawerOpen(false) : undefined}>
      <Suspense fallback={<Waiting />}>
        {under.name === "chat" ? (
          <ChatPage key={under.id} id={under.id} phone={phone} slot={drawer} sheet={phone && drawerOpen ? sheetH : 0} />
        ) : (
          <Empty below={phone && drawerOpen ? sheetH : 0} />
        )}
      </Suspense>
    </main>
  );

  // The settings: a small dialog as wide as the me popover, right above their key at the sidebar's
  // foot, its right edge on the key's, rising out of it (the user, 2026-11-06); the terminals its page.
  // As the me popover is (the user, 2026-11-06): no shade over the page, which stays live; a press
  // outside puts it away, but one on its own key, which does that itself.
  const settingsAt = useSettingsAt(over(r));
  const settings = (
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
          <Suspense fallback={<Waiting />}>
            {r.name === "terminals" ? (
              <div key="terminals" className="g-slide h-full">
                <TerminalsPage onBack={() => (history.state?.depth > 1 ? history.back() : go({ name: "settings" }, true))} />
              </div>
            ) : (
              <div key="settings" className="g-slide h-full" data-dir="back">
                <SettingsPage onClose={() => close(under)} theme={theme} setTheme={setThemeKept} />
              </div>
            )}
          </Suspense>
        </Dialog.Popup>
      </Dialog.Portal>
    </Dialog.Root>
  );

  return (
    <Toast.Provider toastManager={toasts}>
      <Tooltip.Provider delay={450} closeDelay={60}>
        <div className="flex h-full overflow-hidden" data-still={params.get("still") === "1" || undefined}>
          {!drawer && sidebar}
          {main}
          {drawer &&
            (phone ? (
              <Sheet open={drawerOpen} setOpen={setDrawerOpen} tall={jumping} onHeight={setSheetH}>
                {sidebar}
              </Sheet>
            ) : (
              <Drawer open={drawerOpen} setOpen={setDrawerOpen}>
                {sidebar}
              </Drawer>
            ))}
          <SideKey state={!drawer ? "docked" : drawerOpen ? "out" : "in"} onClick={() => (!drawer ? dock(false) : setDrawerOpen((o) => !o))} />
        </div>
        {settings}
        {secret && (
          <Suspense>
            <SecretDialog secret={secret} />
          </Suspense>
        )}
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
    const seen = b && b.width > 0 && b.right > 0 && b.left < innerWidth;
    const room = seen ? b.top - 16 : 0;
    if (!seen || room < 360) {
      setAt({ left: 8, bottom: "max(8px, env(safe-area-inset-bottom))", height: "min(600px, calc(100dvh - 16px))" });
      return;
    }
    setAt({ left: Math.max(8, Math.min(innerWidth - w - 8, b.right - w)), bottom: innerHeight - b.top + 8, height: Math.min(600, room) });
  }, [open]);
  return at;
}
