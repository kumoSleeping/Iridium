// The layout (plan "网页"): one sidebar. On a computer the sidebar and the main pane side by side;
// on a phone the sidebar is the first screen and a chat, the topology or the settings slide over
// it, with a back key. The chat, the topology, the settings and the jump box (⌘K) come only as
// they are opened, or the page is idle: the first screen stays light.
import { Toast } from "@base-ui/react/toast";
import { Tooltip } from "@base-ui/react/tooltip";
import { lazy, Suspense, useEffect, useMemo, useRef, useState, type ComponentProps } from "react";
import type { Workspace } from "./api/types.gen";
import { watchTurns } from "./chats";
import { CreatePopover, WsPlus } from "./create";
import { useHome, useSettings, useUpdate, useWorkspaces } from "./data";
import { t, useLang } from "./i18n";
import { applyTheme, initialTheme, keepTheme, MeChip, type ThemePref } from "./me";
import { prefetch } from "./prefetch";
import { back, go, openChat, useRoute, type Route } from "./route";
import { Sidebar } from "./sidebar";
import { useList } from "./store";
import { follow } from "./stream";
import { ToastList, toasts } from "./toast";
import { buildTree, flat } from "./tree";
import { cn, kept, keep, params, PHONE, useMedia } from "./ui";
import { watchUnread } from "./unread";

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
const Topology = lazy(() => import("./topology/topology").then((m) => ({ default: m.TopologyView })));
const SettingsPage = lazy(() => import("./settings").then((m) => ({ default: m.SettingsPage })));
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
  const expanded = (k: string) => over[k] ?? k !== "archive";
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
  const systemDark = useMedia("(prefers-color-scheme: dark)");
  const [theme, setTheme] = useState<ThemePref>(initialTheme);
  useEffect(() => applyTheme(theme, systemDark), [theme, systemDark]);
  const r = useRoute();
  const open = r.name === "chat" ? r.id : null;
  const list = useList();
  const ws = useWorkspaces();
  const update = useUpdate();
  useSettings(); // asked early: the chat's quota bars read it
  useHome(); // paths show ~ for it
  const { expanded, toggle } = useExpanded();
  const tree = useMemo(() => buildTree(list ?? [], ws.data ?? [], t("nav.unsorted")), [list, ws.data, t("nav.unsorted")]);

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

  // The pane a phone slides out keeps its last page till it is gone.
  const [shown, setShown] = useState<Route>(r.name === "home" ? { name: "home" } : r);
  if (r.name !== "home" && r !== shown) setShown(r);

  const [jumping, setJumping] = useState(params.get("open") === "jump");
  useEffect(() => {
    const key = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && !e.shiftKey && !e.altKey && e.key.toLowerCase() === "k") {
        e.preventDefault();
        setJumping((j) => !j);
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

  const current = (list ?? []).find((s) => s.id === open);
  const curWs: Workspace | null = ws.data?.find((w) => w.id === current?.workspace) ?? ws.data?.[0] ?? null;
  const fallbackDir = current?.cwd ?? list?.[0]?.cwd ?? "~";

  const sidebar = (
    <Sidebar
      tree={tree}
      expanded={expanded}
      toggle={toggle}
      selected={phone && r.name === "home" ? "" : (open ?? "")}
      onSelect={openChat}
      phone={phone}
      topology={r.name === "topology"}
      onTopology={() => (r.name === "topology" ? back() : go({ name: "topology" }, r.name !== "home"))}
      onSearch={() => setJumping(true)}
      settings={r.name === "settings"}
      onSettings={() => (r.name === "settings" ? back() : go({ name: "settings" }, r.name !== "home"))}
      updateDot={!!update.data?.available}
      chatCode={() => void loadChat().catch(() => {})}
      createKey={(trigger) => <CreatePopover trigger={trigger} ws={curWs} fallbackDir={fallbackDir} opened={(id) => openChat(id)} />}
      wsPlus={(w, trigger, o, setO) => <WsPlus key={w.key} w={{ id: w.id, name: w.name, path: w.root }} trigger={trigger} open={o} setOpen={setO} opened={(id) => openChat(id)} />}
      me={<MeChip theme={theme} setTheme={(v) => (setTheme(v), keepTheme(v))} defaultOpen={params.get("open") === "me"} initialPage={params.get("open") === "perms" ? "perms" : "main"} />}
    />
  );

  const main = (at: Route) => (
    <main className="relative h-full min-w-0 flex-1">
      <Suspense fallback={<Waiting />}>
        {at.name === "chat" ? (
          <ChatPage key={at.id} id={at.id} phone={phone} onBack={back} />
        ) : at.name === "topology" ? (
          <Topology phone={phone} onBack={back} tree={tree} />
        ) : at.name === "settings" ? (
          <SettingsPage phone={phone} onBack={back} theme={theme} setTheme={(v: ThemePref) => (setTheme(v), keepTheme(v))} />
        ) : (
          <Empty />
        )}
      </Suspense>
    </main>
  );

  return (
    <Toast.Provider toastManager={toasts}>
      <Tooltip.Provider delay={450} closeDelay={60}>
        <div className="flex h-full overflow-hidden" data-still={params.get("still") === "1" || undefined}>
          {phone ? (
            <div className="relative h-full w-full overflow-hidden">
              <div className="g-push absolute inset-0 flex bg-paper" data-at={r.name === "home" ? "here" : "under"} inert={r.name !== "home"}>
                {sidebar}
              </div>
              <div className="g-push absolute inset-0 z-10 flex bg-paper shadow-[-1px_0_0_var(--edge)]" data-at={r.name === "home" ? "off" : "here"} inert={r.name === "home"}>
                {shown.name !== "home" && main(shown)}
              </div>
            </div>
          ) : (
            <>
              {sidebar}
              {main(r)}
            </>
          )}
        </div>
        {jumping && (
          <Suspense>
            <Jump close={() => setJumping(false)} tree={tree} open={open} />
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
