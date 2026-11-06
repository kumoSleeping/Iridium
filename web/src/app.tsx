// The layout (plan "网页"): one main pane, the chat, on a computer and on a phone alike (the user,
// 2026-11-06). On a computer the sidebar stands beside it, as wide as it is dragged, or is put away
// (⌘\) for the chat the whole window; on a phone or a portrait screen it is a drawer from the left
// (drawer.tsx), brought by the key at the bottom left or a swipe from the edge. The settings are a
// dialog rising from their key, over the sidebar mostly, the chat staying under it. The jump box
// (⌘K) opens in the sidebar, not over the page. The chat, the settings and the jump box come only as
// they are opened, or the page is idle: the first screen stays light.
import { Dialog } from "@base-ui/react/dialog";
import { Toast } from "@base-ui/react/toast";
import { Tooltip } from "@base-ui/react/tooltip";
import { lazy, Suspense, useEffect, useMemo, useRef, useState, type ComponentProps } from "react";
import { watchTurns } from "./chats";
import { CreatePopover, WsPlus } from "./create";
import { Drawer, DrawerKey } from "./drawer";
import { useHome, useMe, useSettings, useTerminals, useUpdate, useWorkspaces } from "./data";
import { t, useLang } from "./i18n";
import { applyTheme, initialTheme, keepTheme, MeChip, type ThemePref } from "./me";
import { pageTurn } from "./motion";
import { prefetch } from "./prefetch";
import { close, go, onTurn, openChat, over, route, useRoute, type Route } from "./route";
import { Sidebar } from "./sidebar";
import { useSecret } from "./secrets";
import { useConfirms, useList } from "./store";
import { follow } from "./stream";
import { ToastList, toasts } from "./toast";
import { buildTree, flat } from "./tree";
import { cn, kept, keep, params, PHONE, useMedia, useMinute } from "./ui";
import { watchUnread } from "./unread";
import { warmUp } from "./warm";

// The chat's code, once it came, is drawn straight, not through lazy, whose first draw shows the
// fallback (Rhodium #386). Which of the two is picked as it mounts, for good.
type ChatCode = typeof import("./chat/chat");
let chatCode: ChatCode | undefined;
let chatComing: Promise<ChatCode> | undefined;
export const loadChat = () => (chatComing ??= import("./chat/chat").then((m) => (chatCode = m)).catch((e) => ((chatComing = undefined), Promise.reject(e))));
const LazyChat = lazy(() => loadChat().then((m) => ({ default: m.ChatPage })));
function ChatPage(p: ComponentProps<ChatCode["ChatPage"]>) {
  const [Page] = useState(() => chatCode?.ChatPage ?? LazyChat);
  return <Page {...p} />;
}
const SettingsPage = lazy(() => import("./settings").then((m) => ({ default: m.SettingsPage })));
const TerminalsPage = lazy(() => import("./terminals").then((m) => ({ default: m.TerminalsPage })));
const SecretDialog = lazy(() => import("./secret").then((m) => ({ default: m.SecretDialog })));
const Jump = lazy(() => import("./jump").then((m) => ({ default: m.Jump })));

// Once idle, the later parts' files into the cache; then the chat's code runs from it, so a turn
// that ends fetches its chat on the list too. Not after one failed: a module that failed to load
// stays failed in the browser.
void prefetch().then((all) => void (all && loadChat().catch(() => {})));

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
      onSelect={openChat}
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
      createKey={(trigger) => <CreatePopover trigger={trigger} />}
      wsPlus={(w, trigger, o, setO) => <WsPlus key={w.key} w={{ id: w.id, name: w.name, path: w.root }} trigger={trigger} open={o} setOpen={setO} opened={(id) => openChat(id)} />}
      me={<MeChip theme={theme} setTheme={setThemeKept} defaultOpen={params.get("open") === "me"} initialPage={params.get("open") === "perms" ? "perms" : "main"} />}
      drawer={drawer}
      onDock={narrow ? undefined : () => dock(drawer)}
    />
  );
  // On a computer the small sidebar, out, lives beside the chat: the main pane makes room for it,
  // nothing covered, and its key is not needed meanwhile.
  const beside = drawer && !narrow && drawerOpen;
  const drawerKey = drawer && !beside ? (above: boolean) => <DrawerKey above={above} onClick={() => setDrawerOpen(true)} /> : undefined;

  const main = (
    <main className={cn("relative h-full min-w-0 flex-1 bg-paper", !phone && "g-turn", drawer && !narrow && "g-make-room")} style={beside ? { marginLeft: "calc(var(--sidebar-w) + 16px)" } : undefined}>
      <Suspense fallback={<Waiting />}>
        {under.name === "chat" ? (
          <ChatPage key={under.id} id={under.id} phone={phone} drawerKey={drawerKey} />
        ) : (
          <>
            <Empty />
            {drawerKey?.(false)}
          </>
        )}
      </Suspense>
    </main>
  );

  // The settings: a dialog at the window's left, over the sidebar mostly and a little past it,
  // rising from their key at the sidebar's foot (the user, 2026-11-06); the terminals its page.
  const settings = (
    <Dialog.Root open={over(r)} onOpenChange={(o) => !o && over(route()) && close(under)}>
      <Dialog.Portal>
        <Dialog.Backdrop className="g-backdrop fixed inset-0 z-50 bg-[oklch(0_0_0/0.12)] dark:bg-[oklch(0_0_0/0.35)]" />
        <Dialog.Popup
          aria-label={t("nav.settings")}
          className="g-dialog glass thick float fixed top-[max(8px,env(safe-area-inset-top))] bottom-[max(8px,env(safe-area-inset-bottom))] left-2 z-50 w-[min(calc(100vw-16px),640px,max(460px,calc(var(--sidebar-w,292px)+220px)))] overflow-hidden rounded-xl text-ink outline-none"
        >
          <Suspense fallback={<Waiting />}>
            {r.name === "terminals" ? (
              <div key="terminals" className="g-slide h-full">
                <TerminalsPage phone={phone} onBack={() => (history.state?.depth > 1 ? history.back() : go({ name: "settings" }, true))} />
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
          {drawer && (
            <Drawer open={drawerOpen} setOpen={setDrawerOpen} phone={phone} modal={narrow}>
              {sidebar}
            </Drawer>
          )}
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

/** The main pane with nothing open: the faint mark alone. */
function Empty() {
  return (
    <div className="grid h-full place-items-center" aria-hidden>
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
