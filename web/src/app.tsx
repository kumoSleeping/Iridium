// The layout (plan "网页"): one main pane, the chat, on a computer and on a phone alike (the user,
// 2026-11-06). On a computer the sidebar always stands beside it, as wide as it is dragged; a window
// too narrow for both is a phone's, its sidebar a sheet from the foot in the composer's place
// (sheet.tsx), brought from the chat's ⋯ menu or by a swipe up. The 终端 panel, this terminal and the
// settings in one (the user, 2026-11-07), is a dialog rising from the me key at the sidebar's foot,
// the chat staying under it; on a phone a sheet too, by a swipe up from the left quarter or ⋯. The jump box
// (⌘K) opens in the sidebar, not over the page. The chat, the settings and the jump box come only as
// they are opened, or the page is idle: the first screen stays light.
import { AppKey, AppName, AppTell, WinKeys } from "./appkey";
import { Dialog } from "@base-ui/react/dialog";
import { Menu } from "@base-ui/react/menu";
import { Toast } from "@base-ui/react/toast";
import { Tooltip } from "@base-ui/react/tooltip";
import { BarChart3, Ellipsis, FolderOpen, Gamepad2, ListTree, Settings } from "lucide-react";
import { lazy, memo, Suspense, useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type ComponentType, type CSSProperties, type ReactNode } from "react";
import { api, queryClient } from "./api/client";
import { firstScreen, watchTurns } from "./chats";
import { NewHere } from "./create";
import { onCreateAt, type BrowseAt } from "./places";
import { listFromDevice } from "./device/first";
import { agentsQ, LOCAL_ME, selfQ, useApps, useHome, useMe, useSettings, useUpdate, useWorkspaces } from "./data";
import { FileWindow } from "./file-window";
import { FileBrowserHandle, FileBrowserTrigger, loadFileBrowser } from "./filebrowser-trigger";
import { t, useLang } from "./i18n";
import { applyTheme, initialTheme, keepTheme, MeKey, useTellLang, type ThemePref } from "./me";
import { motion, movesAhead, pageTurn, usePaced } from "./motion";
import { prefetch, settled } from "./prefetch";
import { prefetchRoles } from "./roles";
import { quotaQ } from "./quota";
import { back as stepBack, close, go, onTurn, needTab, openChat, openManage, openUsage, over, quietDialog, route, useRoute, type ManageTab, type Route } from "./route";
import { Pager, TABS_H } from "./pager";
import { atLeft, GRIP, PULL, Sheet } from "./sheet";
import { LinkDot } from "./link";
import { HeadKeys, MenuItems, SideKey, Sidebar } from "./sidebar";
import { keepShut, shutKept } from "./sideshut";
import { useConfirms, useList } from "./store";
import { follow, opened } from "./stream";
import { ToastList, toasts } from "./toast";
import { buildTree, flat, FOLD_STEP } from "./tree";
import { usePlaces } from "./reorder";
import { reloadAgain } from "./upgrade";
import { cn, IrMark, kept, keep, params, PHONE, useMedia, useMinute, DialogPortal, MenuPortal } from "./ui";
import { watchUnread } from "./unread";
import { tellRail } from "./railstatus";
import { usePart } from "./crossfav";
import { warmUp } from "./warm";
import { openGame, useGameOpen } from "./game/open";

/** A part that comes only as it is opened, or the page is idle. Once its code came it is drawn
 *  straight, not through lazy, whose first draw shows the fallback even with the code at hand
 *  (Rhodium #386); which of the two is picked as it mounts, for good. load fetches and runs it.
 *  Drawn again only as its props change, not with every draw of the page: a page of the phone's
 *  sheet turned drew the chat, the usage and the settings all again, and stuttered. */
function part<P extends object>(get: () => Promise<ComponentType<P>>) {
  let code: ComponentType<P> | undefined;
  let coming: Promise<ComponentType<P>> | undefined;
  // Its code not come (the server a moment away: restarting for a new build, the network gone)
  // is asked for again, a little later each time; left at the first failure the pane stayed empty
  // for good, the sidebar drawn beside it (kumo, 2026-11-24). Still not there: the page loads
  // itself again (upgrade.ts: the build may have gone, its files with it), once in 10 s.
  const again = async (): Promise<ComponentType<P>> => {
    for (let i = 0; ; i++) {
      try {
        return await get();
      } catch (e) {
        if (i >= 4) throw (reloadAgain(), e);
        await new Promise((r) => setTimeout(r, 400 * 2 ** i));
      }
    }
  };
  const load = () => (coming ??= again().then((c) => (code = memo(c) as unknown as ComponentType<P>)).catch((e) => ((coming = undefined), Promise.reject(e))));
  const Lazy = lazy(() => load().then((c) => ({ default: c })));
  function Part(p: P) {
    const [C] = useState(() => code ?? Lazy);
    return <C {...p} />;
  }
  return Object.assign(Part, { load });
}
const ChatPage = part(() => import("./chat/chat").then((m) => m.ChatPage));
const SettingsPage = part(() => import("./settings").then((m) => m.SettingsPage));
const ManagePage = part(() => import("./manage").then((m) => m.ManagePage));
const CachePage = part(() => import("./device/page").then((m) => m.CachePage));
const UsagePage = part(() => import("./usage").then((m) => m.UsagePage));
const PaneSplash = lazy(() => import("./splash/splash").then((m) => ({ default: m.PaneSplash })));
// The ⋯ menu's Game, over the pane (game/overlay.tsx): loaded as it is first opened.
const GameLayer = lazy(() => import("./game/overlay"));
function GameMount() {
  return useGameOpen() && (
    <Suspense fallback={null}>
      <GameLayer />
    </Suspense>
  );
}
const FileBrowserKey = part(() => import("./filebrowser-key").then((m) => m.FileBrowserKey));
const FileBrowser = part(() => loadFileBrowser().then((m) => m.default));
/** The page of the settings' dialog a route is: the settings, or one of their own pages. */
type SetsPage = "settings" | "cache";
const setsPageOf = (r: Route): SetsPage => (r.name === "cache" ? r.name : "settings");
/** The settings' dialog (the phone's 设置 page) is open: a route over the chat, but the usage and
 *  管理, windows of their own. */
const inSets = (r: Route) => over(r) && r.name !== "usage" && r.name !== "manage";
const Jump = part(() => import("./jump").then((m) => m.Jump));
export const loadChat = ChatPage.load;
// The address names a chat: its code and its first screen are asked for now, ahead of the first
// draw, the list and the sidebar's faces (chats.ts firstScreen).
// Not one: its code all the same, the first press not waiting for it (kumo, 2026-11-23).
{
  const at = route();
  void loadChat().catch(() => {});
  if (at.name === "chat") void firstScreen(at.id);
}
/** The settings and the jump box, their code run, the settings' answers asked: once idle, or a
 *  pointer on their key. */
const loadPanels = () => {
  for (const p of [SettingsPage, ManagePage, CachePage, UsagePage, Jump]) void p.load().catch(() => {});
  void queryClient.prefetchQuery(agentsQ);
  void queryClient.prefetchQuery({ ...selfQ, staleTime: 30_000 });
  // The credentials' rows too: their page slid in empty and filled after.
  void queryClient.prefetchQuery({ queryKey: ["secrets"], queryFn: api.secrets, staleTime: 30_000 });
};

// Once idle, the later parts' files into the cache (vite.config.ts LATER); then their code runs from
// it, so a turn that ends fetches its chat on the list too, and the settings and the jump box open at
// once. Not after one failed: a module that failed to load stays failed in the browser.
// What the "+" popover draws (its roles), asked for once idle, apart from the files: drawn
// whole as it opens, nothing pushed in after (docs/前端设计规范.md "后到的不撑开").
// The settings' own page and what it draws too, not after every later file has come (a slow link
// took seconds for those): the me key's dialog opens at its height, not the room's and then its own.
// The quota asked again as the page is come back to and as its stream opens again (kumo,
// 2026-11-25: back after a while it showed the old windows, full ones still red, for up to a
// minute; asked each minute only while the page is in front).
if (typeof document !== "undefined") document.addEventListener("visibilitychange", () => !document.hidden && void queryClient.invalidateQueries({ queryKey: quotaQ.queryKey }));
opened.add(() => void queryClient.invalidateQueries({ queryKey: quotaQ.queryKey }));
void settled().then(() => (prefetchRoles(), movesAhead(), void queryClient.prefetchQuery(agentsQ), void queryClient.prefetchQuery(quotaQ), void SettingsPage.load().catch(() => {}), void queryClient.prefetchQuery({ ...selfQ, staleTime: 30_000 })));
// The device's own cache: its worker, its sweeps (device/boot.ts), after the first screen.
void settled().then(() => import("./device/boot"));
void prefetch().then((all) => {
  if (!all) return;
  void loadChat().catch(() => {});
  loadPanels();
});

/** A record kept on this device by name; {} when there is none or it does not read. */
const keptRecord = <T,>(name: string): Record<string, T> => {
  try {
    return JSON.parse(kept(name) ?? "{}");
  } catch {
    return {};
  }
};

/** Whether a sidebar group is open, as this device left it: all are, but 归档. more: how many of
 *  a folder's folded sessions (tree.ts foldMore) were opened beyond the first few, by its key;
 *  showMore opens FOLD_STEP more, foldBack folds those of the keys given again. */
function useExpanded() {
  // "more:<key>" was once here too, all of a folder's rest opened at once: no longer read.
  const [over, setOver] = useState(() => Object.fromEntries(Object.entries(keptRecord<boolean>("expanded")).filter(([k]) => !k.startsWith("more:"))));
  const [opened, setOpened] = useState(() => keptRecord<number>("more"));
  // Open unless folded, but the archive: it waits to be opened.
  const shut = (k: string) => k === "archive";
  const expanded = (k: string) => over[k] ?? !shut(k);
  const toggle = (k: string) =>
    setOver((o) => {
      const n = { ...o, [k]: !(o[k] ?? !shut(k)) };
      keep("expanded", JSON.stringify(n));
      return n;
    });
  const setMore = (f: (o: Record<string, number>) => Record<string, number>) =>
    setOpened((o) => {
      const n = f(o);
      if (n === o) return o;
      keep("more", JSON.stringify(n));
      return n;
    });
  const more = (k: string) => opened[k] ?? 0;
  const showMore = (k: string) => setMore((o) => ({ ...o, [k]: (o[k] ?? 0) + FOLD_STEP }));
  const foldBack = (keys: string[]) => setMore((o) => (keys.some((k) => o[k]) ? Object.fromEntries(Object.entries(o).filter(([k]) => !keys.includes(k))) : o));
  return { expanded, toggle, more, showMore, foldBack };
}

export function App() {
  useLang();
  // Shown beside another page in the desktop app (crossfav.ts): never a phone's, however narrow.
  const part = usePart().part;
  const phone = useMedia(PHONE) && !part;
  // The settings' steps out of the address with the sidebar shown (route.ts quietDialog).
  quietDialog(!phone);
  // On a phone, whether the sidebar's sheet is out, and how tall it is: the chat keeps its newest
  // above it while it is out.
  // Come to with nothing open, a phone shows the splash first, the sheet down (kumo, 2026-11-25).
  const [sheetOut, setSheetOut] = useState(false);
  const [sheetH, setSheetH] = useState(0);
  const systemDark = useMedia("(prefers-color-scheme: dark)");
  const [theme, setTheme] = useState<ThemePref>(initialTheme);
  useEffect(() => applyTheme(theme, systemDark), [theme, systemDark]);
  const r = useRoute();
  // Under the settings' dialog, the page open before it.
  const [under, setUnder] = useState<Route>(over(r) ? { name: "home" } : r);
  if (!over(r) && r !== under) setUnder(r);
  const open = under.name === "chat" ? under.id : null;
  // On a phone the 终端 panel is a sheet too (the user, 2026-11-06): its page drawn still as it goes
  // down, once it has been open.
  // The usage is a window over the whole page (kumo, 2026-11-25): under it the sheet stays as it
  // was, its 设置 page too when the usage was asked for over it (the chat's ⋯ above the sheet).
  const usageOpen = r.name === "usage" || r.name === "manage";
  const setsWas = useRef(false);
  if (!usageOpen) setsWas.current = phone && inSets(r);
  const setsOut = usageOpen ? setsWas.current : phone && inSets(r);
  const [setsPage, setSetsPage] = useState<SetsPage | null>(null);
  // 管理 keeps the part it showed while it goes (its route already left).
  const manageWas = useRef<ManageTab | null>(null);
  if (r.name === "manage") manageWas.current = r.tab;
  if (inSets(r) && setsPage !== setsPageOf(r)) setSetsPage(setsPageOf(r));
  // One sheet on a phone, two pages in it: the sessions and the 终端 panel (the user, 2026-11-08),
  // the panel shown while its route is open.
  const sheetIsOut = sheetOut || setsOut;
  const below = phone && sheetIsOut ? sheetH : 0;
  // The panel's page drawn once the sheet has been out: the finger may drag it into sight.
  const [seen, setSeen] = useState(false);
  if (phone && sheetIsOut && !seen) setSeen(true);
  // And once the page is idle, before it is ever pulled: drawn first at the finger's first move,
  // that move stuttered (kumo, 2026-11-25).
  useEffect(() => void (phone && settled().then(() => setSeen(true))), [phone]);
  const list = useList();
  const ws = useWorkspaces();
  const update = useUpdate();
  useSettings(); // asked early: the chat's quota bars read it
  useTellLang(); // hosted agents are told the user's language
  useHome(); // paths show ~ for it
  useMe();
  useApps(); // a message's source shows its app's or login's avatar
  const confirms = useConfirms();
  const { expanded, toggle, more, showMore, foldBack } = useExpanded();
  // Each minute too: a subagent long quiet is let go.
  const minute = useMinute();
  const places = usePlaces();
  const treeNow = useMemo(() => buildTree(list ?? [], ws.data ?? [], t("nav.unsorted"), Math.max(minute, Date.now()), undefined, places), [list, ws.data, t("nav.unsorted"), minute, places]);
  // The list reorders at most once a beat as sessions work (motion.ts usePaced); what you did
  // yourself (a row dragged, a session made) is drawn at once.
  const tree = usePaced(treeNow, flat(treeNow).map((s) => s.id).join());

  useEffect(() => follow(open), [open]);
  // Unread, and the chats of turns that end off screen.
  const meNow = useMe().data;
  const unread = useRef<ReturnType<typeof watchUnread>>(undefined);
  useEffect(() => {
    unread.current = watchUnread();
    return () => unread.current?.stop();
  }, []);
  const turns = useRef(new Map<string, string>());
  useEffect(() => {
    // The list as this device kept it (device/first.ts): no turn is read as ended from it.
    if (!list || listFromDevice()) return;
    // Not those the list leaves out (a group at another machine no one opened): no count for them.
    unread.current?.take(list.filter((s) => !tree.unlisted.has(s.id)), meNow ?? LOCAL_ME);
    tellRail(list.filter((s) => !tree.unlisted.has(s.id)));
    watchTurns(list, turns.current, open);
  }, [list, meNow?.id]);
  // The chats you may open next, warmed (src/warm.ts).
  useEffect(() => warmUp(list, tree, open), [tree, open]);

  // On a phone a chat opened from the sheet puts it away, and with nothing open it is out, to stay:
  // there is nothing else to see.
  // In the same draw as the chat, not an effect's after: else the sheet, still out for a draw, took
  // the chat's size first and went down shrinking.
  const [sheetFor, setSheetFor] = useState(`${open} ${phone}`);
  if (sheetFor !== `${open} ${phone}`) {
    setSheetFor(`${open} ${phone}`);
    // A chat opened puts the sheet away; the home pane come to shows its splash, the sheet down as
    // at the first screen (kumo, 2026-11-25).
    if (phone) setSheetOut(false);
  }
  // Out by a swipe up from the left quarter (x), on the 终端 page; else on the sessions' (the user,
  // 2026-11-06). Put away, or brought to the sessions, the panel's route is left.
  // A swipe up from the left shows the 终端 page from its first px, not the sessions' turning to it
  // as it is let go (the user, 2026-11-08).
  const [pulled, setPulled] = useState<"me" | "sessions" | null>(null);
  // Drawn as it is first pulled too: dragged up before it had ever been out, it came up empty.
  if (phone && pulled && !seen) setSeen(true);
  // Just come out too: its page set at once, not slid to as it rises.
  const wasOut = useRef(sheetIsOut);
  const turnStill = !sheetIsOut || !wasOut.current;
  useEffect(() => void (wasOut.current = sheetIsOut));
  const setSheet = (o: boolean) => {
    setPulled(null);
    setSheetOut(o);
    if (inSets(route())) close(under);
  };
  // One page now (kumo, 2026-11-27: on a phone 管理 is the settings): the sessions.
  const toPage = () => setSheetOut(true);
  // Put away, the panel is drawn afresh once it is down: its first page, not renaming.
  const [setsRound, setSetsRound] = useState(0);
  useEffect(() => {
    if (setsOut) return;
    // Once the page has gone and the page is idle: drawn afresh, it must not land on a page turn's
    // last frames.
    let gone = false;
    const round = () => !gone && setSetsRound((n) => n + 1);
    const t = setTimeout(() => (window.requestIdleCallback ? window.requestIdleCallback(round, { timeout: 2000 }) : setTimeout(round, 200)), motion.dur(3) + 60);
    return () => ((gone = true), clearTimeout(t));
  }, [setsOut]);

  // Chat to chat on a computer, the main pane swaps quickly, drifting the way the sidebar lists them.
  const now = useRef({ tree, phone });
  now.current = { tree, phone };
  useEffect(() => onTurn(pageTurn(() => flat(now.current.tree).map((s) => s.id), () => !now.current.phone)), []);

  // On a computer the sidebar shut (sideshut.ts): the main pane's top bar has [Ir], pressed to open it.
  const [shutOn, setShutOn] = useState(shutKept);
  const shut = !phone && shutOn;
  const shutNow = useRef(shut);
  shutNow.current = shut;
  const setShut = useCallback((v: boolean) => (setShutOn(v), keepShut(v)), []);
  const onShut = useCallback(() => setShut(true), [setShut]);
  const onUnshut = useCallback(() => setShut(false), [setShut]);

  // The jump box, in the sidebar (the user, 2026-11-06); on a phone ⌘K brings the sheet out too, and
  // ⌘\ brings it out or puts it away; on a computer with the sidebar shut, it opens it.
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
        if (to && shutNow.current) setShut(false);
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
  const sheetNow = useRef(sheetIsOut);
  sheetNow.current = sheetIsOut;
  const setThemeKept = useCallback((v: ThemePref) => (setTheme(v), keepTheme(v)), []);
  // The chat's keys to the phone's sheet, the same each draw (the chat is drawn again only as its
  // props change).
  const sheetNowFn = useRef(setSheet);
  sheetNowFn.current = setSheet;
  // Pressed again while it is out, it is put away (kumo, 2026-11-25: no swipe brings or puts it away).
  const onSheet = useCallback(() => sheetNowFn.current(!sheetNow.current), []);
  // On a phone the me key opens 管理 at Ir, its settings (kumo, 2026-11-27); on a computer the
  // small panel rising from it, as before.
  const onMe = useCallback(() => (phoneNow.current ? openManage("ir") : go({ name: "settings" })), []);
  // The rows' own, the same each draw: a row is drawn again only as it changes (sidebar.tsx).
  const phoneNow = useRef(phone);
  phoneNow.current = phone;
  const onSelect = useCallback((id: string) => (openChat(id), phoneNow.current && setSheetOut(false)), []);
  const chatCode = useCallback(() => void loadChat().catch(() => {}), []);

  // The settings' own routes (an old link, a key that still goes there) on a phone: 管理's Ir.
  useEffect(() => void (phone && inSets(r) && openManage("ir")), [phone, r]);
  const onSettings = () => (phone ? openManage("ir") : inSets(r) ? close(under) : go({ name: "settings" }));
  const me = <MeKey open={inSets(r)} onOpen={onSettings} dot={!!update.data?.available || confirms.length > 0} code={loadPanels} manage={phone ? undefined : () => openManage()} />;
  const session = open ? list?.find((s) => s.id === open) : undefined;
  // Sidebar key and chat folder links share one dialog; bumping filesRound remounts a fresh
  // browser on each open, even if reopened mid-exit.
  const [filesHandle] = useState(() => Dialog.createHandle<BrowseAt>());
  const [filesRound, setFilesRound] = useState(0);
  // Any host or web login sees the key; the file routes enforce the login's scope per request.
  const filesAllowed = meNow?.host || meNow?.web;
  // A place no listed group holds, pressed (places.ts): the file browser there, 在此新建 in it.
  useEffect(() => onCreateAt((p) => filesAllowed && filesHandle.openWithPayload(p)), [filesAllowed, filesHandle]);
  const filesKey = filesAllowed ? (
    <FileBrowserHandle.Provider value={filesHandle}>
      <Suspense fallback={<span className="size-8 pointer-coarse:size-10" />}>
        <FileBrowserKey path={session?.cwd ?? ""} machine={session?.machine ?? ""} />
      </Suspense>
    </FileBrowserHandle.Provider>
  ) : undefined;
  const filesWindow = filesAllowed ? (
    <Dialog.Root handle={filesHandle} onOpenChange={(on) => { if (on) setFilesRound((round) => round + 1); }}>
      {({ payload }) => payload && (
        <FileWindow className="@container h-[min(640px,86dvh)]">
          <Suspense fallback={
            <>
              <Dialog.Title className="sr-only">{t("browse.title")}</Dialog.Title>
              <p role="status" className="m-auto text-sm text-ink-3">{t("msg.loading")}</p>
            </>
          }>
            <FileBrowser key={filesRound} machine={payload.machine ?? ""} path={payload.path} within={payload.within} workspace={payload.workspace} />
          </Suspense>
        </FileWindow>
      )}
    </Dialog.Root>
  ) : undefined;
  const sidebar = (
    <Sidebar
      tree={tree}
      expanded={expanded}
      toggle={toggle}
      more={more}
      showMore={showMore}
      foldBack={foldBack}
      selected={open ?? ""}
      onSelect={onSelect}
      noFoot={phone}
      phone={phone}
      searching={jumping}
      search={
        <Suspense>
          <Jump close={() => setJumping(false)} tree={tree} open={open} />
        </Suspense>
      }
      onSearch={() => setJumping((j) => !j)}
      confirms={confirms}
      onConfirm={(c) => (c.session && list?.some((s) => s.id === c.session) ? openChat(c.session) : openManage(needTab(c.kind)))}
      chatCode={chatCode}
      panelsCode={loadPanels}
      filesKey={filesKey}
      wsPlus={(w, trigger, o, setO) => <NewHere key={w.key} w={{ id: w.id, name: w.name, path: w.root }} trigger={trigger} open={o} setOpen={setO} opened={(id) => openChat(id)} sub={filesAllowed ? (at) => filesHandle.openWithPayload(at) : undefined} />}
      me={me}
      reveal={phone ? onSheet : undefined}
      shut={shut}
      onShut={phone ? undefined : onShut}
    />
  );

  const main = (
    // On a phone a tap on the chat puts the sheet away, the composer back (the user, 2026-11-06);
    // not one on its top bar: its ⋯ opens over the sheet (the user, 2026-11-06).
    <main className={cn("relative h-full min-w-0 flex-1 bg-paper", !phone && "g-turn")} onClickCapture={phone && (sheetOut || setsOut) ? (e) => !(e.target as Element).closest?.("header") && setSheet(false) : undefined}>
      {/* The pane's one splash, under whichever page is in it: not drawn anew from the empty pane
          to a channel made there (kumo, 2026-11-25). */}
      <Suspense fallback={null}>
        <PaneSplash open={under.name === "chat" ? under.id : undefined} off={under.name === "chat" && !!under.record} />
      </Suspense>
      <FileBrowserHandle.Provider value={filesAllowed ? filesHandle : null}>
        <Suspense fallback={<Waiting />}>
          {under.name === "chat" ? (
            <ChatPage key={`${under.id}:${under.record ? "r" : "c"}`} id={under.id} record={under.record} sheet={below} onSheet={phone ? onSheet : undefined} onMe={phone || shut ? onMe : undefined} onSide={shut ? onUnshut : undefined} />
          ) : (
            <Empty below={below} onSheet={phone ? onSheet : undefined} onMe={phone || shut ? onMe : undefined} onSide={shut ? onUnshut : undefined} />
          )}
        </Suspense>
      </FileBrowserHandle.Provider>
      <GameMount />
    </main>
  );

  // The settings: a small dialog as wide as the me popover, right above their key at the sidebar's
  // foot, its right edge on the key's, rising out of it (the user, 2026-11-06); the terminals its page.
  // As the me popover is (the user, 2026-11-06): no shade over the page, which stays live; a press
  // outside puts it away, but one on its own key, which does that itself.
  // The dialog keeps the page it showed while it goes (its route already left).
  const dialogPage: SetsPage = inSets(r) ? setsPageOf(r) : (setsPage ?? "settings");
  const settingsAt = useSettingsAt(inSets(r) && !phone);
  // As tall as its page, up to that room (the user, 2026-11-08: no empty foot).
  const [setsBox, setSetsBox] = useState<HTMLDivElement | null>(null);
  const fit = useFit(setsBox, inSets(r) && !phone);
  const room = settingsAt?.height;
  // The terminals and the credentials keep the settings' height, scrolling within (the user,
  // 2026-11-08: going in, the dialog neither grows nor shrinks); opened on them first, they fit.
  const settingsH = useRef<number | undefined>(undefined);
  if (dialogPage === "settings" && fit.h !== undefined) settingsH.current = fit.h;
  const boxH = dialogPage === "cache" && settingsH.current !== undefined ? settingsH.current : fit.h;
  const settingsStyle: CSSProperties | undefined = settingsAt && boxH !== undefined && room !== undefined ? { ...settingsAt, height: `min(${boxH}px, ${typeof room === "number" ? `${room}px` : room})` } : settingsAt;
  // The usage: the file browser's own window over the page (kumo, 2026-11-25), in a portrait
  // layout as tall as it is there. One height whatever is chosen in it, room enough, more scrolling
  // within (kumo, 2026-11-25: never taller or shorter for a choice). Closed, the step that opened it
  // is gone back.
  const usageWindow = (
    <Dialog.Root open={r.name === "usage"} onOpenChange={(o) => !o && route().name === "usage" && stepBack()}>
      <FileWindow className={cn("@container", phone ? "h-[min(640px,86dvh)]" : "h-[min(720px,90dvh)]")}>
        <Suspense fallback={<Waiting />}>
          <UsagePage />
        </Suspense>
      </FileWindow>
    </Dialog.Root>
  );
  // 管理: the usage's window, as tall (kumo, 2026-11-27).
  const manageWindow = (
    <Dialog.Root open={r.name === "manage"} onOpenChange={(o) => !o && route().name === "manage" && stepBack()}>
      <FileWindow className={cn("@container", phone ? "h-[min(640px,86dvh)]" : "h-[min(720px,90dvh)]")}>
        <Suspense fallback={<Waiting />}>
          <ManagePage tab={r.name === "manage" ? r.tab : (manageWas.current ?? "accounts")} theme={theme} setTheme={setThemeKept} />
        </Suspense>
      </FileWindow>
    </Dialog.Root>
  );
  const setsBody = (page: SetsPage) => (
    <Suspense fallback={<Waiting />}>
      <SetsPages page={page}>
        {(slide) =>
          page === "cache" ? (
            <div key="cache" className={cn(slide, "h-full")}>
              <CachePage onBack={back} />
            </div>
          ) : (
            <div key="settings" className={cn(slide, "h-full")} data-dir="back">
              {/* shown: on a phone it stays drawn in the sheet, put away or not (its update looks on each opening). */}
              <SettingsPage key={setsRound} theme={theme} setTheme={setThemeKept} shown={!phone || setsOut} />
            </div>
          )
        }
      </SetsPages>
    </Suspense>
  );
  const settings = phone ? null : (
    <Dialog.Root
      open={inSets(r)}
      modal={false}
      onOpenChange={(o, d) => {
        if (o || !inSets(route())) return;
        const e = d.event as (Event & { relatedTarget?: EventTarget | null }) | undefined;
        const key = (x?: EventTarget | null) => !!(x as Element | null)?.closest?.("[data-settings-key]");
        if ((d.reason === "outside-press" || d.reason === "focus-out") && (key(e?.target) || key(e?.relatedTarget))) return d.cancel();
        close(under);
      }}
    >
      <DialogPortal>
        <Dialog.Popup
          ref={setSetsBox}
          data-settings-dialog
          aria-label={t("nav.me")}
          style={settingsStyle}
          className={cn("g-dialog glass thick float fixed z-50 w-[min(288px,calc(100vw-16px))] overflow-hidden rounded-lg text-ink outline-none")}
        >
          {setsBody(dialogPage)}
        </Dialog.Popup>
      </DialogPortal>
    </Dialog.Root>
  );

  return (
    <Toast.Provider toastManager={toasts}>
      <Tooltip.Provider delay={450} closeDelay={60}>
        <AppTell />
        <div className="flex h-full overflow-hidden" data-still={params.get("still") === "1" || undefined}>
          {!phone && sidebar}
          {main}
          {phone && (
            <Sheet open={sheetIsOut} setOpen={setSheet} size="half" raised={jumping && !setsOut} name="phone" head={GRIP + TABS_H} from={() => true} onPull={(x) => setPulled(atLeft(x) ? "me" : "sessions")} label={setsOut ? t("nav.me") : t("nav.sessions")} onHeight={setSheetH}>
              <Pager
                // The tabs' bar is px-3, the list's rows end 4px further right: its "+" in line with
                // theirs (the user, 2026-11-08).
                ends={[<span key="s" className="-mr-1 flex"><HeadKeys filesKey={filesKey} searching={jumping} onSearch={() => setJumping((j) => !j)} panelsCode={loadPanels} /></span>]}
                page={0}
                still={turnStill}
                onPage={toPage}
                tabs={[{ label: t("nav.sessions"), aria: t("nav.sessions") }]}
              >
                {[sidebar]}
              </Pager>
            </Sheet>
          )}
        </div>
        {settings}
        {filesWindow}
        {usageWindow}
        {manageWindow}
        <Toast.Portal>
          <Toast.Viewport className={cn("fixed top-[60px] left-1/2 z-60 flex w-[min(400px,calc(100vw-24px))] -translate-x-1/2 flex-col items-center gap-2 outline-none")}>
            <ToastList onJump={(id) => openChat(id)} />
          </Toast.Viewport>
        </Toast.Portal>
      </Tooltip.Provider>
    </Toast.Provider>
  );
}

/** The settings' pages: the first one shown as the dialog (or the phone's tab) comes, still, as it
 *  rises with it; only a page gone to after that slides in (g-slide). Sliding the first too, the
 *  page drifted on after the dialog had stopped, its last frames off by a fraction of a pixel. */
function SetsPages({ page, children }: { page: SetsPage; children: (slide: string | undefined) => ReactNode }) {
  const [at, setAt] = useState(page);
  const [moved, setMoved] = useState(false);
  if (at !== page) {
    setAt(page);
    setMoved(true);
  }
  return children(moved ? "g-slide" : undefined);
}

/** Back a step in the settings' dialog. */
const back = () => (history.state?.depth > 1 ? history.back() : go({ name: "settings" }, true));

// The box at the empty pane's foot: the composer's own container (chat/composer.tsx: its glass,
// its corners, its row and padding, a key where its attach key is, its words' line), so the two
// are one thing from the pane to a chat (kumo, 2026-11-25); its words fainter than a placeholder's.
const PICK = "glass float rest relative flex w-full min-w-0 cursor-text items-end gap-0.5 rounded-[20px] p-2 text-left";
const PICK_KEY = "grid size-8 shrink-0 place-items-center text-ink-3 pointer-coarse:size-10";
const PICK_WORDS = "min-w-0 flex-1 self-center truncate px-0.5 py-1 text-md leading-[21px] text-ink-4";

/** The main pane with nothing open: the splash in its middle, a box where a chat's composer is. */
function Empty({ below, onSheet, onMe, onSide }: { below: number; onSheet?: () => void; onMe?: () => void; onSide?: () => void }) {
  // What ⋯ has with no chat open (kumo, 2026-11-25): the list, the settings, the usage.
  const lead = [...(onSheet ? [{ icon: <ListTree size={15} />, label: t("nav.sidebar"), run: onSheet }] : []), ...(onMe ? [{ icon: <Settings size={15} />, label: t("nav.me"), run: onMe }] : []), { icon: <BarChart3 size={15} />, label: t("usage.title"), run: () => openUsage() }, { icon: <Gamepad2 size={15} />, label: t("game.open"), run: openGame }];
  return (
    <div className="relative grid h-full place-items-center transition-[padding] duration-(--dur-3) ease-(--ease-out)" style={{ paddingBottom: below }}>
      {/* Where a chat's composer is, its look: pressed, the file browser, a channel made in the
          folder chosen there (kumo, 2026-11-25: the first screen, a phone's too). */}
      {/* As a chat's composer is (kumo, 2026-11-25: it stood over the sheet): where it is, covered by
          the sheet as it comes and gone once it is out (g-sink); a swipe up from it brings the sheet. */}
      <div {...{ [PULL]: "" }} data-covered={below > 0 || undefined} className="g-sink absolute inset-x-0 bottom-0 z-2 px-2.5 pb-[max(14px,calc(env(safe-area-inset-bottom)+6px))] sm:px-5">
        {/* A phone's: what a swipe up from here brings, the sheet of the groups; pressed, the same. */}
        <div className="relative mx-auto max-w-(--chat-w)">
        {onSheet ? (
          <div className={PICK}>
            {/* Where the attach key is: the file browser (kumo, 2026-11-25). The words: the sheet. */}
            <FileBrowserTrigger place={{ machine: "", path: "" }} aria-label={t("browse.title")} className={cn(PICK_KEY, "g-press rounded-md hover:bg-fill hover:text-ink")}>
              <FolderOpen size={17} />
            </FileBrowserTrigger>
            <button type="button" onClick={onSheet} className={cn(PICK_WORDS, "cursor-text text-left")}>
              {t("empty.swipe")}
            </button>
          </div>
        ) : (
          <FileBrowserTrigger place={{ machine: "", path: "" }} className={PICK}>
            <span className={PICK_KEY}>
              <FolderOpen size={17} />
            </span>
            <span className={PICK_WORDS}>{t("empty.pick")}</span>
          </FileBrowserTrigger>
        )}
        </div>
      </div>
      {/* The desktop app's keys where a chat's top bar has them (appkey.tsx); nothing in a browser.
          The strip they are in drags the window. */}
      <div data-app-drag className="absolute inset-x-0 top-0 z-2 flex h-12 items-center justify-end pr-2 pointer-coarse:h-13">
        {/* A phone's key to its sheet, where a chat's bar has it. */}
        {onSheet && (
          <button type="button" aria-label={t("nav.sidebar")} onClick={onSheet} className="g-press ml-2 flex items-center gap-2.5 rounded-md p-2 hover:bg-fill">
            <IrMark size={18} className="text-ink" />
            <span className="text-base font-semibold">Iridium</span>
          </button>
        )}
        {/* The link, as after a chat's name; ⋯ where a chat's bar has it, a computer's too (kumo,
            2026-11-25: its top bar's right was empty). */}
        {onSheet && <LinkDot className="mr-auto shrink-0" />}
        {/* A computer's with the sidebar shut: its head's, in a phone's order (kumo, 2026-11-26). */}
        {onSide && (
          <div className="mr-auto ml-4 flex min-w-0 items-center gap-2">
            <SideKey label={t("nav.unshut")} onClick={onSide} />
            <AppName />
            <LinkDot className="shrink-0" />
          </div>
        )}
        {(
          <Menu.Root>
            <Menu.Trigger render={<button type="button" aria-label={t("top.more")} className="g-press grid size-8 place-items-center rounded-md text-ink-2 hover:bg-fill hover:text-ink data-popup-open:bg-fill pointer-coarse:size-10" />}>
              <Ellipsis size={17} />
            </Menu.Trigger>
            <MenuPortal>
              <Menu.Positioner side="bottom" align="end" sideOffset={6} collisionPadding={8} className="z-50">
                <MenuItems lead={lead} items={[]} className="min-w-56" />
              </Menu.Positioner>
            </MenuPortal>
          </Menu.Root>
        )}
        <AppKey />
        <WinKeys />
      </div>
    </div>
  );
}

/** While a part's code comes: the top bar's place, frosted and empty. */
function Waiting() {
  return <div className="glass absolute inset-x-0 top-0 h-12 shadow-[0_0.5px_0_var(--edge)] pointer-coarse:h-13" />;
}

/** The settings' dialog as tall as the page in it would stand: its scroller's whole content and
 *  what is around it. Measured before the first paint as it opens, then as the page or what is in it
 *  changes: at once, in the frame it changed (a popup's box is not animated, docs/动画.md; what is
 *  in it is there as it opens, fetched ahead, and a row opened in it opens its own room). */
function useFit(el: HTMLElement | null, open: boolean) {
  const [h, setH] = useState<number>();
  useLayoutEffect(() => {
    if (!open || !el) return;
    const ro = new ResizeObserver(() => measure());
    let watched: Element[] = [];
    const measure = () => {
      const root = el.firstElementChild?.firstElementChild as HTMLElement | null | undefined;
      if (!root) return;
      const sc = root.querySelector<HTMLElement>(".overflow-y-auto");
      const want = [root, sc, sc?.firstElementChild].filter(Boolean) as Element[];
      if (want.some((x, i) => x !== watched[i]) || want.length !== watched.length) {
        ro.disconnect();
        want.forEach((x) => ro.observe(x));
        watched = want;
      }
      if (!sc) return setH(Math.ceil(root.scrollHeight));
      // What the scroller holds, to its last child's foot and its padding under it (its
      // scrollHeight is never less than itself: no measure of a page shorter than the dialog).
      // In layout px: the dialog may be scaled as it unfolds.
      const last = sc.lastElementChild;
      // The dialog's scale as its rects show it, read at once with them: its width on screen over its
      // layout width, unrounded (offsetHeight, rounded, was off by up to a pixel mid-unfold). Not the
      // computed transform: as it opens that says the unfold's 0.96 while the rects are not scaled
      // yet, and the dialog stood 4% too tall for a frame (kumo, 2026-11-25).
      const box = sc.getBoundingClientRect();
      const lw = parseFloat(getComputedStyle(el).width);
      const scale = lw > 0 ? el.getBoundingClientRect().width / lw : 1;
      const inner = last ? (last.getBoundingClientRect().bottom - box.top) / scale + sc.scrollTop + parseFloat(getComputedStyle(sc).paddingBottom) : 0;
      setH(Math.ceil(root.offsetHeight - sc.clientHeight + inner));
    };
    measure();
    // A page put in (another page, its code come): measured again.
    const mo = new MutationObserver(() => measure());
    mo.observe(el, { childList: true, subtree: true });
    return () => (ro.disconnect(), mo.disconnect());
  }, [open, el]);
  return { h };
}

/** Where the 终端 panel stands, open: above the me key (me.tsx data-settings-key), its left edge on
 *  the key's, as tall as fits there up to 600px; with too little room or no key in sight, at the
 *  window's bottom left. */
function useSettingsAt(open: boolean): CSSProperties | undefined {
  const [at, setAt] = useState<CSSProperties>();
  // The window made another size: placed again, not left where the key was.
  const [size, setSize] = useState(0);
  useEffect(() => {
    if (!open) return;
    const again = () => setSize((n) => n + 1);
    addEventListener("resize", again);
    return () => removeEventListener("resize", again);
  }, [open]);
  useLayoutEffect(() => {
    if (!open) return;
    const w = Math.min(288, innerWidth - 16);
    const k = document.querySelector("[data-settings-key]");
    // The sidebar shut (inert, sideshut.ts): its key out of sight.
    const b = k && !k.closest("[inert]") ? k.getBoundingClientRect() : undefined;
    const seen = b && b.width > 0 && b.right > 0 && b.left < innerWidth && b.bottom <= innerHeight;
    const room = seen ? b.top - 16 : 0;
    if (!seen || room < 360) {
      setAt({ left: 8, bottom: "max(8px, env(safe-area-inset-bottom))", height: "min(600px, calc(100dvh - 16px))" });
      return;
    }
    setAt({ left: Math.max(8, Math.min(innerWidth - w - 8, b.left)), bottom: innerHeight - b.top + 8, height: Math.min(600, room) });
  }, [open, size]);
  return at;
}
