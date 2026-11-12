// The layout (plan "网页"): one main pane, the chat, on a computer and on a phone alike (the user,
// 2026-11-06). On a computer the sidebar always stands beside it, as wide as it is dragged; a window
// too narrow for both is a phone's, its sidebar a sheet from the foot in the composer's place
// (sheet.tsx), brought from the chat's ⋯ menu or by a swipe up. The 终端 panel, this terminal and the
// settings in one (the user, 2026-11-07), is a dialog rising from the me key at the sidebar's foot,
// the chat staying under it; on a phone a sheet too, by a swipe up from the left quarter or ⋯. The jump box
// (⌘K) opens in the sidebar, not over the page. The chat, the settings and the jump box come only as
// they are opened, or the page is idle: the first screen stays light.
import { Dialog } from "@base-ui/react/dialog";
import { Toast } from "@base-ui/react/toast";
import { Tooltip } from "@base-ui/react/tooltip";
import { lazy, memo, Suspense, useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type ComponentType, type CSSProperties, type ReactNode } from "react";
import { api, queryClient } from "./api/client";
import { watchTurns } from "./chats";
import { CreatePopover, WsPlus } from "./create";
import { agentsQ, pushQ, useApps, useHome, useMe, useSettings, useUpdate, useWorkspaces } from "./data";
import { t, useLang } from "./i18n";
import { applyTheme, initialTheme, keepTheme, MeKey, useTellLang, type ThemePref } from "./me";
import { motion, pageTurn } from "./motion";
import { prefetch } from "./prefetch";
import { close, go, onTurn, openChat, over, quietDialog, route, setUsageMachine, usagePoint, useRoute, type Route } from "./route";
import { Pager, TABS_H } from "./pager";
import { atLeft, atRight, GRIP, Sheet } from "./sheet";
import { HeadKeys, Sidebar } from "./sidebar";
import { useConfirms, useList } from "./store";
import { follow } from "./stream";
import { ToastList, toasts } from "./toast";
import { buildTree, flat } from "./tree";
import { usePlaces } from "./reorder";
import { cn, holdMenu, IrMark, kept, keep, params, PHONE, useMedia, useMinute } from "./ui";
import { watchUnread } from "./unread";
import { warmUp } from "./warm";

/** A part that comes only as it is opened, or the page is idle. Once its code came it is drawn
 *  straight, not through lazy, whose first draw shows the fallback even with the code at hand
 *  (Rhodium #386); which of the two is picked as it mounts, for good. load fetches and runs it.
 *  Drawn again only as its props change, not with every draw of the page: a page of the phone's
 *  sheet turned drew the chat, the usage and the settings all again, and stuttered. */
function part<P extends object>(get: () => Promise<ComponentType<P>>) {
  let code: ComponentType<P> | undefined;
  let coming: Promise<ComponentType<P>> | undefined;
  const load = () => (coming ??= get().then((c) => (code = memo(c) as unknown as ComponentType<P>)).catch((e) => ((coming = undefined), Promise.reject(e))));
  const Lazy = lazy(() => load().then((c) => ({ default: c })));
  function Part(p: P) {
    const [C] = useState(() => code ?? Lazy);
    return <C {...p} />;
  }
  return Object.assign(Part, { load });
}
const ChatPage = part(() => import("./chat/chat").then((m) => m.ChatPage));
const SettingsPage = part(() => import("./settings").then((m) => m.SettingsPage));
const AppsPage = part(() => import("./apps").then((m) => m.AppsPage));
const SecretsPage = part(() => import("./credentials").then((m) => m.SecretsPage));
const UsagePage = part(() => import("./usage").then((m) => m.UsagePage));
/** The page of the settings' dialog a route is: the settings, or one of their own pages. */
type SetsPage = "settings" | "apps" | "secrets" | "usage";
const setsPageOf = (r: Route): SetsPage => (r.name === "apps" || r.name === "secrets" || r.name === "usage" ? r.name : "settings");
const Jump = part(() => import("./jump").then((m) => m.Jump));
const NotesPanel = part(() => import("./notes").then((m) => m.NotesPanel));
export const loadChat = ChatPage.load;
/** The settings and the jump box, their code run, the settings' answers asked: once idle, or a
 *  pointer on their key. */
const loadPanels = () => {
  for (const p of [SettingsPage, AppsPage, SecretsPage, UsagePage, Jump]) void p.load().catch(() => {});
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
  // Open unless folded, but the archive, a workflow run's agents and the rest of a folder's
  // sessions (tree.ts foldRest): those wait to be opened. Those hung under a launcher ("hung:")
  // shown (2026-11-10).
  const shut = (k: string) => k === "archive" || k.startsWith("run:") || k.startsWith("more:");
  const expanded = (k: string) => over[k] ?? !shut(k);
  const toggle = (k: string) =>
    setOver((o) => {
      const n = { ...o, [k]: !(o[k] ?? !shut(k)) };
      keep("expanded", JSON.stringify(n));
      return n;
    });
  return { expanded, toggle };
}

export function App() {
  useLang();
  const phone = useMedia(PHONE);
  // The settings' steps out of the address with the sidebar shown (route.ts quietDialog).
  quietDialog(!phone);
  // On a phone, whether the sidebar's sheet is out, and how tall it is: the chat keeps its newest
  // above it while it is out.
  const [sheetOut, setSheetOut] = useState(() => phone && route().name !== "chat");
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
  const setsOut = phone && over(r);
  const [setsPage, setSetsPage] = useState<SetsPage | null>(null);
  // On a phone the usage is a page of the sheet's own (the user, 2026-11-08): the 设置 page keeps
  // the last of its own pages under it.
  const ownPage = !phone || r.name !== "usage";
  if (over(r) && ownPage && setsPage !== setsPageOf(r)) setSetsPage(setsPageOf(r));
  // One sheet on a phone, two pages in it: the sessions and the 终端 panel (the user, 2026-11-08),
  // the panel shown while its route is open.
  const sheetIsOut = sheetOut || setsOut;
  const below = phone && sheetIsOut ? sheetH : 0;
  // The panel's page drawn once the sheet has been out: the finger may drag it into sight.
  const [seen, setSeen] = useState(false);
  if (phone && sheetIsOut && !seen) setSeen(true);
  const list = useList();
  const ws = useWorkspaces();
  const update = useUpdate();
  useSettings(); // asked early: the chat's quota bars read it
  useTellLang(); // hosted agents are told the user's language
  useHome(); // paths show ~ for it
  useMe();
  useApps(); // a message's source shows its app's or login's avatar
  const confirms = useConfirms();
  const { expanded, toggle } = useExpanded();
  // Each minute too: a subagent long quiet is let go.
  const minute = useMinute();
  const places = usePlaces();
  const tree = useMemo(() => buildTree(list ?? [], ws.data ?? [], t("nav.unsorted"), Math.max(minute, Date.now()), undefined, places), [list, ws.data, t("nav.unsorted"), minute, places]);

  useEffect(() => follow(open), [open]);
  // Unread, and the chats of turns that end off screen.
  const unread = useRef<ReturnType<typeof watchUnread>>(undefined);
  useEffect(() => {
    unread.current = watchUnread();
    return () => unread.current?.stop();
  }, []);
  const turns = useRef(new Map<string, string>());
  useEffect(() => {
    if (!list) return;
    // Not those the list leaves out (a group at another machine no one opened): no count for them.
    unread.current?.take(list.filter((s) => !tree.unlisted.has(s.id)));
    watchTurns(list, turns.current, open);
  }, [list]);
  // The chats you may open next, warmed (src/warm.ts).
  useEffect(() => warmUp(list, tree, open), [tree, open]);

  // On a phone a chat opened from the sheet puts it away, and with nothing open it is out, to stay:
  // there is nothing else to see.
  // In the same draw as the chat, not an effect's after: else the sheet, still out for a draw, took
  // the chat's size first and went down shrinking.
  const [sheetFor, setSheetFor] = useState(`${open} ${phone}`);
  if (sheetFor !== `${open} ${phone}`) {
    setSheetFor(`${open} ${phone}`);
    if (phone) setSheetOut(!open);
  }
  // Out by a swipe up from the left quarter (x), on the 终端 page; else on the sessions' (the user,
  // 2026-11-06). Put away, or brought to the sessions, the panel's route is left.
  // A swipe up from the left shows the 终端 page from its first px, not the sessions' turning to it
  // as it is let go (the user, 2026-11-08).
  const [pulled, setPulled] = useState<"me" | "sessions" | "notes" | null>(null);
  // The notes: on a phone the sheet's last page, brought by a swipe up from the right quarter (the
  // user, 2026-11-09); on a computer only the composer's (chat/notekey.tsx).
  const [notesPage, setNotesPage] = useState(false);
  // Drawn once first shown, then kept, hidden: a note dragged away from them (to the sessions) is
  // still theirs to drop.
  const [notesSeen, setNotesSeen] = useState(false);
  // Out, its page is the route's; going down, it stays as it was; coming up, the swipe's.
  const lastPage = useRef(2);
  const sheetPage = sheetIsOut ? (setsOut ? (r.name === "usage" ? 1 : 0) : notesPage ? 3 : 2) : pulled ? (pulled === "me" ? 0 : pulled === "notes" ? 3 : 2) : lastPage.current;
  if (!notesSeen && phone && sheetPage === 3) setNotesSeen(true);
  lastPage.current = sheetPage;
  // Just come out too: its page set at once, not slid to as it rises.
  const wasOut = useRef(sheetIsOut);
  const turnStill = !sheetIsOut || !wasOut.current;
  useEffect(() => void (wasOut.current = sheetIsOut));
  const setSheet = (o: boolean, x?: number) => {
    setPulled(null);
    if (o && x !== undefined && atLeft(x)) return go({ name: "settings" });
    if (o && x !== undefined) setNotesPage(atRight(x));
    setSheetOut(o || !open);
    if (over(route())) close(under);
  };
  // A tab, or a swipe across: the sheet stays out, the back key going back to the sessions.
  // 终端 the left page, as a swipe up from the left brings it; the sessions the right (the user, 2026-11-08);
  // the usage between them (the user, 2026-11-08).
  const toPage = (i: number) => (setSheetOut(true), i >= 2 && setNotesPage(i === 3), i === 0 ? go({ name: "settings" }, route().name === "usage") : i === 1 ? (setUsageMachine(), go({ name: "usage" }, over(route()))) : over(route()) && close(under));
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
  const sheetNow = useRef(sheetIsOut);
  sheetNow.current = sheetIsOut;
  const setThemeKept = useCallback((v: ThemePref) => (setTheme(v), keepTheme(v)), []);
  // The chat's keys to the phone's sheet, the same each draw (the chat is drawn again only as its
  // props change).
  const sheetNowFn = useRef(setSheet);
  sheetNowFn.current = setSheet;
  const onSheet = useCallback(() => sheetNowFn.current(true), []);
  const onMe = useCallback(() => go({ name: "settings" }), []);
  // The rows' own, the same each draw: a row is drawn again only as it changes (sidebar.tsx).
  const phoneNow = useRef(phone);
  phoneNow.current = phone;
  const onSelect = useCallback((id: string) => (openChat(id), phoneNow.current && setSheetOut(false)), []);
  const chatCode = useCallback(() => void loadChat().catch(() => {}), []);

  const onSettings = () => (over(r) ? close(under) : go({ name: "settings" }));
  const me = <MeKey open={over(r)} onOpen={onSettings} dot={!!update.data?.available} code={loadPanels} />;
  const sidebar = (
    <Sidebar
      tree={tree}
      expanded={expanded}
      toggle={toggle}
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
      onConfirm={(c) => (c.session && list?.some((s) => s.id === c.session) ? openChat(c.session) : c.kind === "share" && c.share?.session ? (openChat(c.share.session), void api.dropNeed(c.id).catch(() => {})) : go({ name: c.kind.startsWith("secret_") ? "secrets" : "apps" }, over(r)))}
      chatCode={chatCode}
      panelsCode={loadPanels}
      createKey={(trigger) => <CreatePopover trigger={trigger} />}
      wsPlus={(w, trigger, o, setO) => <WsPlus key={w.key} w={{ id: w.id, name: w.name, path: w.root }} trigger={trigger} open={o} setOpen={setO} opened={(id) => openChat(id)} />}
      me={me}
      reveal={phone ? onSheet : undefined}
    />
  );

  const main = (
    // On a phone a tap on the chat puts the sheet away, the composer back (the user, 2026-11-06);
    // not one on its top bar: its ⋯ opens over the sheet (the user, 2026-11-06).
    <main className={cn("relative h-full min-w-0 flex-1 bg-paper", !phone && "g-turn")} onClickCapture={phone && ((sheetOut && open) || setsOut) ? (e) => !(e.target as Element).closest?.("header") && setSheet(false) : undefined}>
      <Suspense fallback={<Waiting />}>
        {under.name === "chat" ? (
          <ChatPage key={under.id} id={under.id} sheet={below} onSheet={phone ? onSheet : undefined} onMe={phone ? onMe : undefined} />
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
  const usageOut = !phone && r.name === "usage";
  // The dialog keeps the page it showed while it goes (its route already left): the usage fading
  // as itself, never as the settings (the user, 2026-11-08).
  const dialogPage: SetsPage = over(r) ? setsPageOf(r) : (setsPage ?? "settings");
  const usageLook = !phone && dialogPage === "usage";
  const settingsAt = useSettingsAt(over(r) && !phone, usageLook);
  // As tall as its page, up to that room (the user, 2026-11-08: no empty foot); the usage keeps its own.
  const [setsBox, setSetsBox] = useState<HTMLDivElement | null>(null);
  const fit = useFit(setsBox, over(r) && !phone && !usageLook);
  const room = settingsAt?.height;
  // The terminals and the credentials keep the settings' height, scrolling within (the user,
  // 2026-11-08: going in, the dialog neither grows nor shrinks); opened on them first, they fit.
  const settingsH = useRef<number | undefined>(undefined);
  if (dialogPage === "settings" && fit.h !== undefined) settingsH.current = fit.h;
  const boxH = (dialogPage === "apps" || dialogPage === "secrets") && settingsH.current !== undefined ? settingsH.current : fit.h;
  const settingsStyle: CSSProperties | undefined = settingsAt && boxH !== undefined && !usageLook && room !== undefined ? { ...settingsAt, height: `min(${boxH}px, ${typeof room === "number" ? `${room}px` : room})` } : settingsAt;
  // The usage panel floats as a row's menu does: one of them at a time (ui.tsx holdMenu).
  const underNow = useRef(under);
  underNow.current = under;
  useEffect(() => (usageOut ? holdMenu(() => route().name === "usage" && close(underNow.current)) : undefined), [usageOut]);
  const usagePage = () => (
    <Suspense fallback={<Waiting />}>
      <UsagePage />
    </Suspense>
  );
  const setsBody = (page: SetsPage) => (
    <Suspense fallback={<Waiting />}>
      <SetsPages page={page}>
        {(slide) =>
          page === "apps" ? (
            <div key="apps" className={cn(slide, "h-full")}>
              <AppsPage onBack={back} side={!phone} />
            </div>
          ) : page === "secrets" ? (
            <div key="secrets" className={cn(slide, "h-full")}>
              <SecretsPage onBack={back} />
            </div>
          ) : page === "usage" ? (
            <div key="usage" className={cn(slide, "h-full")}>
              {usagePage()}
            </div>
          ) : (
            <div key="settings" className={cn(slide, "h-full")} data-dir="back">
              <SettingsPage key={setsRound} theme={theme} setTheme={setThemeKept} />
            </div>
          )
        }
      </SetsPages>
    </Suspense>
  );
  const settings = phone ? null : (
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
          ref={setSetsBox}
          data-settings-dialog
          aria-label={t("nav.me")}
          style={settingsStyle}
          data-glide-h={fit.glide || undefined}
          className={cn("g-dialog glass thick float fixed z-50 overflow-hidden rounded-lg text-ink outline-none", usageLook ? "w-[min(360px,calc(100vw-16px))]" : "w-[min(288px,calc(100vw-16px))]")}
        >
          {setsBody(dialogPage)}
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
            <Sheet open={sheetIsOut} setOpen={setSheet} size={!open ? "full" : "half"} raised={(jumping && !setsOut) || (notesPage && !setsOut && sheetIsOut)} name="phone" head={GRIP + TABS_H} from={() => true} onPull={(x) => setPulled(atLeft(x) ? "me" : atRight(x) ? "notes" : "sessions")} label={setsOut ? (r.name === "usage" ? t("usage.title") : t("nav.me")) : t("nav.sessions")} onHeight={setSheetH}>
              <Pager
                // The tabs' bar is px-3, the list's rows end 4px further right: its "+" in line with
                // theirs (the user, 2026-11-08).
                ends={[null, null, <span key="s" className="-mr-1 flex"><HeadKeys createKey={(trigger) => <CreatePopover trigger={trigger} />} /></span>, null]}
                page={sheetPage}
                still={turnStill}
                onPage={toPage}
                tabs={[
                  { label: <>{t("nav.me")}{update.data?.available && <span className="size-1.5 rounded-full bg-err" aria-hidden />}</>, aria: update.data?.available ? `${t("nav.me")} · ${t("settings.updateReady")}` : t("nav.me") },
                  { label: t("usage.title"), aria: t("usage.title") },
                  // A note dragged and held on it turns to the sessions (notes.tsx).
                  { label: t("nav.sessions"), aria: t("nav.sessions"), spring: true },
                  { label: t("note.title"), aria: t("note.title") },
                ]}
              >
                {[
                  <div key="me" className="absolute inset-0">{seen && setsBody(setsPage ?? "settings")}</div>,
                  <div key="usage" className="absolute inset-0">{seen && usagePage()}</div>,
                  sidebar,
                  <div key="notes" className="absolute inset-0">
                    {notesSeen && (
                      <Suspense>
                        <NotesPanel />
                      </Suspense>
                    )}
                  </div>,
                ]}
              </Pager>
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

/** The main pane with nothing open: the faint mark alone, in the middle of what the phone's sheet
 *  leaves free. */
function Empty({ below }: { below: number }) {
  return (
    <div className="grid h-full place-items-center transition-[padding] duration-(--dur-3) ease-(--ease-out)" style={{ paddingBottom: below }} aria-hidden>
      <IrMark size={44} className="text-fill-2" />
    </div>
  );
}

/** While a part's code comes: the top bar's place, frosted and empty. */
function Waiting() {
  return <div className="glass absolute inset-x-0 top-0 h-12 shadow-[0_0.5px_0_var(--edge)] pointer-coarse:h-13" />;
}

/** The settings' dialog as tall as the page in it would stand: its scroller's whole content and
 *  what is around it. Measured before the first paint as it opens, then as the page or what is in it
 *  changes, the dialog gliding to it (motion.css .g-dialog[data-glide-h]). */
function useFit(el: HTMLElement | null, open: boolean) {
  const [h, setH] = useState<number>();
  const [glide, setGlide] = useState(false);
  useLayoutEffect(() => {
    if (!open) return setGlide(false);
    if (!el) return;
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
      // The dialog's own scale, exact (a rect over offsetHeight, a whole number, was off by up to a
      // pixel mid-unfold, and the height glided there and back as it settled).
      const box = sc.getBoundingClientRect();
      const m = getComputedStyle(el).transform;
      const scale = (m && m !== "none" ? new DOMMatrixReadOnly(m).a : 1) || 1;
      const inner = last ? (last.getBoundingClientRect().bottom - box.top) / scale + sc.scrollTop + parseFloat(getComputedStyle(sc).paddingBottom) : 0;
      setH(Math.ceil(root.offsetHeight - sc.clientHeight + inner));
    };
    measure();
    // A page put in (another page, its code come): measured again.
    const mo = new MutationObserver(() => measure());
    mo.observe(el, { childList: true, subtree: true });
    // Its first height at once; after that, a new one glides.
    const f = requestAnimationFrame(() => setGlide(true));
    return () => (ro.disconnect(), mo.disconnect(), cancelAnimationFrame(f));
  }, [open, el]);
  return { h, glide };
}

/** Where the 终端 panel stands, open: above the me key (me.tsx data-settings-key), its left edge on
 *  the key's, as tall as fits there up to 600px; with too little room or no key in sight, at the
 *  window's bottom left. */
function useSettingsAt(open: boolean, usage: boolean): CSSProperties | undefined {
  const [at, setAt] = useState<CSSProperties>();
  useLayoutEffect(() => {
    if (!open) return;
    // The usage: at the point it was asked from, as a menu is (the user, 2026-11-08), below it if
    // it fits, else above; its right edge on it for end.
    const p = usage && usagePoint();
    if (p) {
      const w = Math.min(360, innerWidth - 16);
      const h = Math.min(560, innerHeight - 16);
      const left = Math.max(8, Math.min(innerWidth - w - 8, p.end ? p.x - w : p.x));
      const top = p.y + h + 8 <= innerHeight ? p.y : Math.max(8, p.y - h);
      setAt({ left, top, height: h });
      return;
    }
    const w = Math.min(288, innerWidth - 16);
    const b = document.querySelector("[data-settings-key]")?.getBoundingClientRect();
    const seen = b && b.width > 0 && b.right > 0 && b.left < innerWidth && b.bottom <= innerHeight;
    const room = seen ? b.top - 16 : 0;
    if (!seen || room < 360) {
      setAt({ left: 8, bottom: "max(8px, env(safe-area-inset-bottom))", height: "min(600px, calc(100dvh - 16px))" });
      return;
    }
    setAt({ left: Math.max(8, Math.min(innerWidth - w - 8, b.left)), bottom: innerHeight - b.top + 8, height: Math.min(600, room) });
  }, [open, usage]);
  return at;
}
