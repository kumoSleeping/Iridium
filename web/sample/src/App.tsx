// The style sample: one sidebar design; on a desktop the sidebar and the chat side by side, on a phone
// the sidebar is the first screen and a session's chat replaces it. Fake data, no backend.
// URL knobs for screenshots: ?theme=light|dark ?lang=zh|en ?view=list|chat ?approval=0 ?details=1
// ?open=plus|newSession|picker|me|perms|context|mode|menu|wsplus ?toast=1 ?still=1
import { Popover } from "@base-ui/react/popover";
import { Toast } from "@base-ui/react/toast";
import { Tooltip } from "@base-ui/react/tooltip";
import { useCallback, useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore } from "react";
import { ApprovalCard, Composer, ModeKey } from "./Composer";
import { CreatePopover, NewSession, FolderPicker, type NewPage } from "./Create";
import { MessageView, TopBar } from "./Chat";
import { chat, modes, pick, reply, terminals, workspaces as seed, type Agent, type Message, type Mode, type Session, type Workspace } from "./data";
import { t, useLang } from "./i18n";
import { MeChip, type ThemePref } from "./Me";
import { flySend } from "./motion";
import { Sidebar } from "./Sidebar";
import { Logo, params, PopSurface } from "./ui";

const toasts = Toast.createToastManager();

function useMedia(q: string) {
  return useSyncExternalStore(
    (f) => {
      const m = matchMedia(q);
      m.addEventListener("change", f);
      return () => m.removeEventListener("change", f);
    },
    () => matchMedia(q).matches,
  );
}

function initialTheme(): ThemePref {
  const q = params.get("theme");
  if (q === "light" || q === "dark" || q === "auto") return q;
  try {
    const s = localStorage.getItem("theme");
    if (s === "light" || s === "dark" || s === "auto") return s;
  } catch {}
  return "auto";
}

const openParam = params.get("open");

export function App() {
  const lang = useLang();
  const phone = useMedia("(max-width: 767px)");
  const systemDark = useMedia("(prefers-color-scheme: dark)");
  const [theme, setTheme] = useState<ThemePref>(initialTheme);
  useEffect(() => {
    document.documentElement.dataset.theme = theme === "auto" ? (systemDark ? "dark" : "light") : theme;
  }, [theme, systemDark]);

  const [ws, setWs] = useState<Workspace[]>(() => structuredClone(seed));
  const [expanded, setExpanded] = useState(() => new Set(["w-ir", "w-kdj", "w-none", "f-web"]));
  const [selected, setSelected] = useState("s-sample");
  const [view, setView] = useState<"list" | "chat">(params.get("view") === "list" ? "list" : "chat");
  const [msgs, setMsgs] = useState<Record<string, Message[]>>({});
  const [stream, setStream] = useState<{ sid: string; n: number } | null>(null);
  const [approvalOn, setApprovalOn] = useState(params.get("approval") !== "0");
  const [leaving, setLeaving] = useState(false);
  const [details, setDetails] = useState(params.get("details") === "1");
  const [mode, setMode] = useState<Mode>("default");
  const [draft, setDraft] = useState(params.get("draft") ?? "");
  const [open, setOpen] = useState<string | null>(openParam);
  const [newPage, setNewPage] = useState<NewPage>(openParam === "picker" ? "workspace" : openParam === "newSession" ? "session" : "menu");
  const [hoverWs, setHoverWs] = useState<string | null>(openParam === "wsplus" ? "w-ir" : null);
  const [me, setMe] = useState(terminals.me);
  const composer = useRef<HTMLTextAreaElement>(null);
  const scroller = useRef<HTMLDivElement>(null);
  const stack = useRef<HTMLDivElement>(null);
  const [stackH, setStackH] = useState(140);
  const fly = useRef<{ id: string; from: { x: number; y: number } } | null>(null);
  const bubbles = useRef(new Map<string, HTMLDivElement>());

  const setOpenSafe = (k: string, o: boolean) => setOpen((c) => (o ? k : c === k ? null : c));
  const setOpenKey = (k: string) => (o: boolean) => setOpenSafe(k, o);

  // Lookups
  const find = useCallback(
    (id: string) => {
      for (const w of ws) {
        const s = w.sessions.find((x) => x.id === id);
        if (s) return { s, w, folder: w.root };
        for (const f of w.folders) {
          const s2 = f.sessions.find((x) => x.id === id);
          if (s2) return { s: s2, w, folder: `${w.root}/${f.name}` };
        }
      }
      return null;
    },
    [ws],
  );
  const cur = find(selected) ?? find("s-sample")!;
  const update = (id: string, fn: (s: Session) => Session) =>
    setWs((all) => all.map((w) => ({ ...w, sessions: w.sessions.map((s) => (s.id === id ? fn(s) : s)), folders: w.folders.map((f) => ({ ...f, sessions: f.sessions.map((s) => (s.id === id ? fn(s) : s)) })) })));
  const toTop = (id: string) =>
    setWs((all) =>
      all.map((w) => {
        const up = (list: Session[]) => {
          const i = list.findIndex((s) => s.id === id);
          return i > 0 ? [list[i], ...list.slice(0, i), ...list.slice(i + 1)] : list;
        };
        return { ...w, sessions: up(w.sessions), folders: w.folders.map((f) => ({ ...f, sessions: up(f.sessions) })) };
      }),
    );

  const messages = msgs[selected] ?? (selected.startsWith("n") ? [] : chat);
  const showApproval = approvalOn && selected === "s-sample";

  // Bottom stack height → the chat's bottom padding.
  useLayoutEffect(() => {
    const el = stack.current;
    if (!el) return;
    const ro = new ResizeObserver(() => setStackH(el.offsetHeight));
    ro.observe(el);
    return () => ro.disconnect();
  }, [view, phone]);

  // Stay at the bottom while the content grows, unless scrolled up; land at the bottom when a chat opens.
  const atBottom = useRef(true);
  useLayoutEffect(() => {
    const el = scroller.current;
    if (!el) return;
    atBottom.current = true;
    el.scrollTop = el.scrollHeight;
  }, [selected, view, stackH]);
  useEffect(() => {
    const el = scroller.current;
    const inner = el?.firstElementChild;
    if (!el || !inner) return;
    const onScroll = () => (atBottom.current = el.scrollHeight - el.scrollTop - el.clientHeight < 24);
    const ro = new ResizeObserver(() => atBottom.current && (el.scrollTop = el.scrollHeight));
    el.addEventListener("scroll", onScroll, { passive: true });
    ro.observe(inner);
    return () => (el.removeEventListener("scroll", onScroll), ro.disconnect());
  }, [phone]);

  // The send flight, once the new bubble is in the DOM.
  useLayoutEffect(() => {
    const f = fly.current;
    if (!f) return;
    const el = bubbles.current.get(f.id);
    if (!el) return;
    fly.current = null;
    const sc = scroller.current!;
    sc.scrollTop = sc.scrollHeight;
    flySend(el, f.from);
  }, [msgs]);

  // Stream the fake reply.
  useEffect(() => {
    if (!stream) return;
    const full = pick(reply, lang);
    if (stream.n >= full.length) {
      setMsgs((m) => ({ ...m, [stream.sid]: [...(m[stream.sid] ?? []), { kind: "agent", id: `a${Date.now()}`, blocks: [{ t: "text", text: reply }] }] }));
      setStream(null);
      update(stream.sid, (s) => ({ ...s, status: "idle" }));
      return;
    }
    const id = window.setTimeout(() => setStream({ ...stream, n: stream.n + 2 }), 28);
    return () => window.clearTimeout(id);
  }, [stream, lang]);

  useEffect(() => {
    const el = scroller.current;
    if (stream && el && el.scrollHeight - el.scrollTop - el.clientHeight < 120) el.scrollTop = el.scrollHeight;
  }, [stream]);

  // A toast for the screenshot.
  useEffect(() => {
    if (params.get("toast") === "1") toasts.add({ title: pick(seed[0].sessions[0].title, lang), description: t("toast.turnDone"), timeout: 0, data: { agent: "codex", jump: "s-holder" } });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const send = () => {
    const text = draft.trim();
    if (!text) return;
    const ta = composer.current!;
    const r = ta.getBoundingClientRect();
    const cs = getComputedStyle(ta);
    const id = `u${Date.now()}`;
    fly.current = { id, from: { x: r.left + parseFloat(cs.paddingLeft), y: r.top + parseFloat(cs.paddingTop) } };
    const sid = selected;
    setMsgs((m) => ({ ...m, [sid]: [...(m[sid] ?? messages), { kind: "you", id, text }] }));
    setDraft("");
    ta.style.height = "auto";
    toTop(sid);
    update(sid, (s) => ({ ...s, status: "running", unread: false }));
    window.setTimeout(() => setStream({ sid, n: 0 }), 650);
  };

  const select = (id: string) => {
    setSelected(id);
    update(id, (s) => ({ ...s, unread: false }));
    if (phone) {
            setView("chat");
    }
  };

  const answer = (a: "allow" | "deny" | "always") => {
    setLeaving(true);
    window.setTimeout(() => {
      setApprovalOn(false);
      setLeaving(false);
      update("s-sample", (s) => ({ ...s, status: a === "deny" ? "idle" : "running" }));
    }, 140);
    toasts.add({ title: a === "deny" ? t("toast.denied") : t("toast.allowed"), description: a === "always" ? t("ask.always") : undefined, timeout: 2500 });
  };

  const newSession = (agent: Agent, w: Workspace, folder: string) => {
    const id = `n${Date.now()}`;
    const s: Session = { id, agent, title: { zh: "新会话", en: "New session" }, status: "starting" };
    setWs((all) =>
      all.map((x) => {
        if (x.id !== w.id) return x;
        if (folder === x.root) return { ...x, sessions: [s, ...x.sessions] };
        const name = folder.slice(x.root.length + 1);
        const f = x.folders.find((y) => y.name === name);
        return f ? { ...x, folders: x.folders.map((y) => (y === f ? { ...y, sessions: [s, ...y.sessions] } : y)) } : { ...x, folders: [...x.folders, { id: `f${id}`, name, sessions: [s] }] };
      }),
    );
    setExpanded((e) => new Set(e).add(w.id));
    setOpen(null);
    setSelected(id);
    if (phone) {
            setView("chat");
    }
    window.setTimeout(() => update(id, (x) => ({ ...x, status: "idle" })), 1400);
    window.setTimeout(() => composer.current?.focus(), 80);
  };

  const newWorkspace = (root: string, name: string) => {
    const id = `w${Date.now()}`;
    setWs((all) => {
      const i = all.findIndex((w) => w.id === "w-none");
      const w: Workspace = { id, name, root, sessions: [], folders: [] };
      return [...all.slice(0, i), w, ...all.slice(i)];
    });
    setExpanded((e) => new Set(e).add(id));
    setOpen(null);
    toasts.add({ title: t("toast.created"), description: name, timeout: 2500 });
  };

  const cycleMode = () => setMode((m) => modes[(modes.indexOf(m) + 1) % modes.length]);

  const sidebar = (
    <Sidebar
      workspaces={ws}
      expanded={expanded}
      toggle={(id) =>
        setExpanded((e) => {
          const n = new Set(e);
          if (n.has(id)) n.delete(id);
          else n.add(id);
          return n;
        })
      }
      selected={phone && view === "list" ? "" : selected}
      onSelect={select}
      phone={phone}
      needs={1}
      plusShown={(id) => hoverWs === id || open === `wsplus:${id}` || (open === "wsplus" && id === "w-ir")}
      setHoverWs={setHoverWs}
      createKey={(trigger) => (
        <CreatePopover
          open={open === "plus" || open === "picker" || open === "newSession"}
          onOpenChange={(o) => {
            setOpenSafe(open === "picker" || open === "newSession" ? open : "plus", o);
            if (o) setNewPage("menu");
          }}
          page={newPage}
          setPage={setNewPage}
          trigger={trigger}
          ws={pick(cur.w.name, lang)}
          root={cur.w.root}
          startNewFolder={params.get("newfolder") === "1"}
          onSession={(a, folder) => newSession(a, cur.w, folder)}
          onWorkspace={newWorkspace}
        />
      )}
      wsPlus={(w, trigger) => <WsPlus key={w.id} w={w} trigger={trigger} open={open === `wsplus:${w.id}` || (open === "wsplus" && w.id === "w-ir")} setOpen={(o) => setOpenSafe(`wsplus:${w.id}`, o)} onPick={(a, folder) => newSession(a, w, folder)} />}
      me={<MeChip me={me} onMe={setMe} theme={theme} setTheme={(v) => (setTheme(v), localStorage.setItem("theme", v))} lang={lang} open={open === "me" || open === "perms"} onOpenChange={setOpenKey("me")} initialPage={openParam === "perms" ? "perms" : "main"} />}
    />
  );

  const chatPane = (
    <main className="relative h-full min-w-0 flex-1">
      <div ref={scroller} className="absolute inset-0 overflow-y-auto" style={{ paddingTop: 64, paddingBottom: stackH + 16 }}>
        <div className="mx-auto flex max-w-[760px] flex-col gap-5 px-4 sm:px-6">
          {messages.map((m) => (
            <MessageView
              key={m.kind === "you" || m.kind === "agent" ? m.id : JSON.stringify(m.text)}
              m={m}
              flyRef={m.kind === "you" ? (el) => void (el ? bubbles.current.set(m.id, el) : bubbles.current.delete(m.id)) : undefined}
              onFile={(name) => toasts.add({ title: t("toast.downloading"), description: name, timeout: 2500 })}
              onJump={select}
            />
          ))}
          {stream && stream.sid === selected && (
            <div className="g-fade prose-ir pr-6 text-md">
              <p>
                {pick(reply, lang).slice(0, stream.n)}
                <span className="ml-0.5 inline-block h-[1.05em] w-[2px] translate-y-[3px] animate-pulse bg-ink-3" />
              </p>
            </div>
          )}
        </div>
      </div>
      <TopBar s={cur.s} folder={cur.folder} phone={phone} onBack={() => (setView("list"))} menuOpen={open === "menu"} setMenuOpen={setOpenKey("menu")} />
      <div ref={stack} className="absolute inset-x-0 bottom-0 z-10 px-2.5 pb-[max(6px,env(safe-area-inset-bottom))] sm:px-5">
        <div className="mx-auto max-w-[760px]">
          {showApproval && <ApprovalCard leaving={leaving} onAnswer={answer} detailsOpen={details} setDetailsOpen={setDetails} />}
          <Composer ref={composer} value={draft} setValue={setDraft} onSend={send} onCycleMode={cycleMode} ctxOpen={open === "context"} setCtxOpen={setOpenKey("context")} />
          <div className="flex h-9 items-center px-1">
            <ModeKey mode={mode} setMode={setMode} open={open === "mode"} onOpenChange={setOpenKey("mode")} />
          </div>
        </div>
      </div>
    </main>
  );

  return (
    <Toast.Provider toastManager={toasts}>
      <Tooltip.Provider delay={450} closeDelay={60}>
        <div className="flex h-full overflow-hidden" data-still={params.get("still") === "1" || undefined}>
          {phone ? (
            <div className="relative h-full w-full overflow-hidden">
              <div className="g-push absolute inset-0 flex bg-paper" data-at={view === "list" ? "here" : "under"} inert={view !== "list"}>
                {sidebar}
              </div>
              <div className="g-push absolute inset-0 z-10 flex bg-paper shadow-[-1px_0_0_var(--edge)]" data-at={view === "chat" ? "here" : "off"} inert={view !== "chat"}>
                {chatPane}
              </div>
            </div>
          ) : (
            <>
              {sidebar}
              {chatPane}
            </>
          )}
        </div>
        <Toast.Portal>
          <Toast.Viewport className="fixed top-[60px] left-1/2 z-60 flex w-[min(400px,calc(100vw-24px))] -translate-x-1/2 flex-col items-center gap-2 outline-none">
            <ToastList onJump={select} />
          </Toast.Viewport>
        </Toast.Portal>
      </Tooltip.Provider>
    </Toast.Provider>
  );
}

function ToastList({ onJump }: { onJump: (id: string) => void }) {
  const { toasts: list } = Toast.useToastManager();
  return list.map((toast) => {
    const d = toast.data as { agent?: Agent; jump?: string } | undefined;
    return (
      <Toast.Root key={toast.id} toast={toast} className="g-pop-t glass thick float flex h-10 max-w-full items-center gap-2.5 rounded-full pr-1.5 pl-3.5 text-sm text-ink">
        {d?.agent && <Logo agent={d.agent} size={15} />}
        <Toast.Title className="min-w-0 truncate font-medium" />
        <Toast.Description className="shrink-0 text-ink-2" />
        {d?.jump ? (
          <Toast.Close onClick={() => onJump(d.jump!)} className="g-press ml-1 h-7 shrink-0 rounded-full bg-fill px-3 text-xs font-medium hover:bg-fill-2">
            {t("toast.open")}
          </Toast.Close>
        ) : (
          <span className="w-2" />
        )}
      </Toast.Root>
    );
  });
}

/** The "+" on a workspace row: new session there. */
function WsPlus({ w, trigger, open, setOpen, onPick }: { w: Workspace; trigger: React.ReactElement; open: boolean; setOpen: (o: boolean) => void; onPick: (a: Agent, folder: string) => void }) {
  const lang = useLang();
  const [page, setPage] = useState<"session" | "folder">("session");
  const [folder, setFolder] = useState(w.root);
  return (
    <Popover.Root
      open={open}
      onOpenChange={(o) => {
        setOpen(o);
        if (o) setPage("session");
      }}
    >
      <Popover.Trigger render={trigger} />
      <PopSurface side="bottom" align="end" finalFocus={false}>
        <div key={page} className={page === "folder" ? "g-fade" : ""}>
          {page === "session" ? (
            <NewSession ws={pick(w.name, lang)} folder={folder} onFolder={() => setPage("folder")} onPick={(a) => onPick(a, folder)} />
          ) : (
            <FolderPicker
              initial={folder}
              onChoose={(p) => {
                setFolder(p);
                setPage("session");
              }}
            />
          )}
        </div>
      </PopSurface>
    </Popover.Root>
  );
}
