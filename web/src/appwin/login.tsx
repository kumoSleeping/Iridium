// The app's own page (docs/app.md "应用页面"; kumo, 2026-11-22), a small upright window, an
// ordinary one. One part shown at a time; the one leaving fades out, then the one coming fades in.
//  - who (the first start): a face, a name, 开始: this computer's own Iridium.
//  - the list: 服务器 above (此电脑, its switch runs it and no more, 公网访问 under it; each
//    Iridium: pressed, its window comes and this one goes; the one used last ticked; 添加服务器,
//    a field once pressed), 授权操作 under (each Iridium logged in to: its link, its switch, › to
//    what it may do here).
//  - one Iridium's 授权操作: 命令行, each agent, 文件夹.
// A part is drawn once its state is there, never before.
import {
  ArrowRight,
  CircleArrowUp,
  ChevronLeft,
  ChevronRight,
  PencilLine,
  EyeOff,
  Folder,
  FolderPlus,
  Globe,
  LoaderCircle,
  Monitor,
  Pause,
  Play,
  Plus,
  ShieldCheck,
  Ticket,
  SquareTerminal,
  Trash2,
  UserRound,
} from "lucide-react";
import { Menu } from "@base-ui/react/menu";
import { type ReactNode, useEffect, useRef, useState } from "react";
import type { Agent } from "../api/types.gen";
import { Crop } from "../profile";
import { agentName, cn, CopyKey, FaceChangeOver, IconKey, IrMark, Logo, SureKey, Switch, Tip, useMedia, usePointAnchor, MenuPortal } from "../ui";
import { ask, clipboardKeys, followTheme, onAnswer, whenReady } from "./bridge";
import { Fold, useFolding } from "./fold";
import { AppSplash } from "./splash";
import { WinKeyPair } from "../winkey";

type Link = { up: boolean; rtt?: number; since: string };
type Server = {
  url: string;
  host: string;
  loggedIn: boolean;
  name?: string;
  title?: string; // its name in the app, given on its page
  avatar?: string; // who one is there: their face
  face?: string; // the Iridium's own (GET /v1/internal/face), a data: URL; none: not set
  local?: boolean;
  status?: string; // this computer's own: 运行中, 启动中…
  joined?: boolean;
  shell?: boolean;
  roots?: string[];
  agentsOff?: string[] | null;
  link?: Link;
  said?: string;
};
type Me = { name?: string; avatar?: string };
type Pub = {
  busy: boolean;
  note: string;
  link: string;
  invite: string;
  error: string;
};
type State = {
  os?: string; // the app's system: darwin, windows, linux
  servers: Server[];
  current?: string; // the Iridium the window in front shows
  me?: Me;
  sysName?: string;
  agents?: string[];
  server?: boolean;
  public?: boolean;
  publicState?: Pub;
  publicAddress?: string;
  hideLocal?: boolean; // 此电脑 hidden here (the tray's 此电脑 › 隐藏)
  update?: string; // the app's update ready, for a restart (selfupdate.go); "": none
  updatePct?: number; // it downloading: how far
  updateError?: string; // its download failed
};
/** The app's own update, left of the close key: nothing, 下载中 37%, 重启以更新到 v…, or 更新失败
 *  (pressed: looked for again). */
function UpdateKey({ st }: { st: State }) {
  const key = "g-press flex h-7 items-center gap-1 rounded-md px-2 text-xs font-medium text-ink-2 hover:bg-fill hover:text-ink";
  if (st.update)
    return (
      <button type="button" className={key} onClick={() => ask("ir:update:restart")}>
        <CircleArrowUp size={13} aria-hidden />
        重启以更新到 {st.update}
      </button>
    );
  if (typeof st.updatePct === "number")
    return <span className="px-2 text-xs font-medium text-ink-3 tabular-nums">下载中 {st.updatePct}%</span>;
  if (st.updateError)
    return (
      <Tip label={st.updateError} side="bottom">
        <button type="button" className={cn(key, "text-ink-3")} onClick={() => ask("ir:update")}>
          更新失败
        </button>
      </Tip>
    );
  return null;
}

type Note = { text: string; error?: boolean; server?: string };
type Part = "who" | "add" | "local" | { server: string };

/** The part the app asks for (internal/desktop/mainwin.go showHome): who, add, local,
 *  grant:<address>; null: none said. */
function partOf(v: string): Part | null {
  if (v === "who" || v === "add" || v === "local") return v;
  if (v.startsWith("grant:")) return { server: v.slice(6) };
  return null;
}

const FADE = 160; // ms, out then in

export function Login() {
  const [st, setSt] = useState<State | null>(null);
  const [part, setPart] = useState<Part | null>(
    partOf(location.hash.replace(/^#/, "")),
  );
  const [full, setFull] = useState(false); // the window maximised or full screen
  const [me, setMe] = useState<Me>({});
  const [busy, setBusy] = useState(""); // a server being logged in to; "+" one typed; "local"
  const [note, setNote] = useState<Note | null>(null);

  useEffect(() => {
    const stop = onAnswer((d) => {
      followTheme(d);
      if (typeof d.max === "boolean") setFull(d.max);
      if (typeof d.view === "string") {
        const p = partOf(d.view);
        if (p) (setNote(null), setPart(p));
      }
      if (Array.isArray(d.servers)) {
        const s = d as unknown as State;
        setSt(s);
        // A server removed while its login waited: its wait gone with it (gui.go stopLogin).
        // Kept, it held every later 连接 back with nothing shown (StarFreedomX, 2026-11-23).
        const left = (b: string) =>
          b === "" ||
          b === "+" ||
          b === "local" ||
          s.servers.some((x) => x.url === b);
        setBusy((b) => (left(b) ? b : ""));
        setNote((n) => (n?.server && !left(n.server) ? null : n));
        if (s.me) setMe((m) => ({ ...s.me, ...m }));
        setPart((p) => p ?? (s.servers.length === 0 ? "who" : "add"));
      }
      const server = typeof d.server === "string" ? d.server : undefined;
      if (typeof d.code === "string") {
        setNote({ text: `在浏览器中核对代码 ${d.code} 后允许`, server });
        // An address typed is in the list now: its wait is its row's, the field put away. One
        // the app began (an Iridium's window at its login page, gui.go needlogin) spins its row too.
        if (server) setBusy((b) => (b === "+" || b === "" ? server : b));
      }
      // Logged in (gui.go "done"): the wait over, its spinner and its code gone (kumo,
      // 2026-11-22: it spun on, and the next 添加服务器 spun before anything was typed).
      if (typeof d.done === "string") (setBusy(""), setNote(null));
      if (typeof d.error === "string")
        (setBusy(""), setNote({ text: d.error, error: true, server }));
    });
    whenReady(() => ask("ir:hello"));
    return stop;
  }, []);

  const go = (p: Part) => (setNote(null), setPart(p));
  // 本机与授权 in two columns when there is room (820 points and more), else one at a time.
  const wide = useMedia("(min-width: 820px)");
  const granted = st?.servers.filter((s) => !s.local && s.loggedIn) ?? [];
  // The Iridium shown at the right: the one asked for (#grant:), else the first.
  const asked = part && typeof part === "object" ? part.server : undefined;
  const chosen = granted.some((s) => s.url === asked) ? asked : granted[0]?.url;
  const login = (to: string, key: string) => {
    // A login waiting in the browser gives way to a new one (login.go: one at a time); an
    // address still being checked, or the same one again, does not.
    if (!to || busy === "+" || busy === "local" || busy === to) return;
    setBusy(key);
    setNote(null);
    if (!ask("ir:login:" + to))
      (setBusy(""), setNote({ text: "无法连接应用", error: true }));
  };
  const keepMe = (m: Me) => (setMe(m), ask("ir:me:" + JSON.stringify(m)));

  return (
    <div className="h-full bg-paper text-ink select-none [&_*:focus-visible]:outline-none">
      {/* The window's top bar, as an Iridium's page's (no title bar on any system: kumo,
          2026-11-22): the part's name at the left (the mark too on the first page, which has no
          rail beside it), the window's keys at the right, the window dragged by it. */}
      <header
        data-app-drag
        className="fixed inset-x-0 top-0 z-10 flex h-12 items-center gap-1.5 bg-paper pr-2 pl-4 text-sm font-semibold tracking-tight"
      >
        {part === "who" && <IrMark size={15} className="text-ink" />}
        <span className="flex-1">
          {part === "who"
            ? "Iridium"
            : part === "add"
              ? "添加服务器"
              : part
                ? "本机与授权"
                : ""}
        </span>
        {st && <UpdateKey st={st} />}
        <WinKeyPair
          mac={st?.os === "darwin"}
          full={full}
          ask={(v) => ask("ir:win:" + v)}
        />
      </header>
      <main
        className={cn(
          "no-bar flex h-full flex-col items-center overflow-y-auto",
          part === "who" && "justify-center",
          part === "add" && "w-full",
        )}
      >
        {st && part && (
          <Swap id={wide && part !== "who" && part !== "add" ? "local" : typeof part === "object" ? "s:" + part.server : part}>
            {part === "add" ? (
              <AddPage
                st={st}
                busy={busy}
                note={note}
                login={login}
                back={st.servers.length === 0 ? () => go("who") : undefined}
              />
            ) : wide && part !== "who" ? (
              // 本机与授权, wide (otae-c, 霓虹, 2026-11-26): 此电脑 and 授权操作 at the left, the
              // Iridium chosen there at the right, whole; none to grant to: the left alone.
              <div className={cn("grid w-[min(880px,calc(100vw-48px))] gap-8 pt-16 pb-6", granted.length ? "grid-cols-[minmax(0,1fr)_minmax(0,1fr)]" : "max-w-[420px] grid-cols-1")}>
                <div className="flex min-w-0 flex-col">
                  <Servers mode="local" st={st} busy={busy} note={note} login={login} />
                  <Grants st={st} shown={chosen ?? ""} open={(s) => go({ server: s.url })} />
                </div>
                {granted.length > 0 && (
                  <div className="flex min-w-0 flex-col">
                    <Heading>授权设置</Heading>
                    <ServerPage s={st.servers.find((s) => s.url === chosen)} agents={st.agents ?? []} />
                  </div>
                )}
              </div>
            ) : (
              <div
                className={cn(
                  "flex w-[min(420px,calc(100vw-24px))] flex-col",
                  part === "who" ? "py-16" : "pt-16 pb-6",
                )}
              >
                {part === "who" ? (
                  <Who
                    me={me}
                    setMe={setMe}
                    keepMe={keepMe}
                    sysName={st.sysName ?? ""}
                    entering={
                      busy === "local" ||
                      st.servers.some((s) => s.local && s.status === "启动中")
                    }
                    note={note}
                    enter={() => {
                      setBusy("local");
                      setNote(null);
                      ask(
                        "ir:enter:" +
                          JSON.stringify({
                            ...me,
                            name: me.name?.trim() || st.sysName,
                          }),
                      );
                    }}
                    more={() => (
                      keepMe({ ...me, name: me.name?.trim() ?? "" }),
                      go("add")
                    )}
                  />
                ) : part === "local" ? (
                  <>
                    <Servers
                      mode="local"
                      st={st}
                      busy={busy}
                      note={note}
                      login={login}
                    />
                    <Grants st={st} open={(s) => go({ server: s.url })} />
                  </>
                ) : (
                  <ServerPage
                    s={st.servers.find((s) => s.url === part.server)}
                    agents={st.agents ?? []}
                    back={() => go("local")}
                  />
                )}
              </div>
            )}
          </Swap>
        )}
      </main>
    </div>
  );
}

/** The part shown: when id changes, the old one fades out, then the new one fades in. */
function Swap({ id, children }: { id: string; children: ReactNode }) {
  const [shown, setShown] = useState({ id, children });
  const [out, setOut] = useState(false);
  useEffect(() => {
    if (id === shown.id) return setShown({ id, children });
    setOut(true);
    const t = setTimeout(
      () => (setShown({ id, children }), setOut(false)),
      FADE,
    );
    return () => clearTimeout(t);
  }, [id, children]); // eslint-disable-line react-hooks/exhaustive-deps
  return (
    <div
      key={shown.id}
      className={cn(
        "flex w-full flex-col items-center transition-opacity ease-(--ease-out)",
        out ? "opacity-0" : "g-fade opacity-100",
      )}
      style={{ transitionDuration: `${FADE}ms` }}
    >
      {shown.children}
    </div>
  );
}

function Who({
  me,
  setMe,
  keepMe,
  sysName,
  entering,
  note,
  enter,
  more,
}: {
  me: Me;
  setMe: (m: Me) => void;
  keepMe: (m: Me) => void;
  sysName: string;
  entering: boolean;
  note: Note | null;
  enter: () => void;
  more: () => void;
}) {
  const [picture, setPicture] = useState<string | null>(null);
  const file = useRef<HTMLInputElement>(null);
  const drop = () =>
    setPicture((old) => (old && URL.revokeObjectURL(old), null));
  return (
    <>
      <div className="flex flex-col items-center gap-4 pb-6">
        <button
          type="button"
          aria-label="头像"
          onClick={() => file.current?.click()}
          className="group g-press relative grid size-22 place-items-center overflow-hidden rounded-full bg-fill text-ink-3"
        >
          {me.avatar ? (
            <img
              src={me.avatar}
              alt=""
              draggable={false}
              className="size-full object-cover"
            />
          ) : (
            <UserRound size={44} strokeWidth={1.5} />
          )}
          <FaceChangeOver size={88} />
        </button>
        <input
          ref={file}
          type="file"
          accept="image/*"
          hidden
          onChange={(e) => {
            const f = e.target.files?.[0];
            if (f)
              setPicture(
                (old) => (
                  old && URL.revokeObjectURL(old),
                  URL.createObjectURL(f)
                ),
              );
            e.target.value = "";
          }}
        />
        <Fold state={picture ? "in" : "out"}>
          {picture && (
            <Crop
              src={picture}
              onCancel={drop}
              onDone={(blob) => {
                const r = new FileReader();
                r.onload = () => (
                  keepMe({ ...me, avatar: String(r.result) }),
                  drop()
                );
                r.readAsDataURL(blob);
              }}
            />
          )}
        </Fold>
        <form
          className="flex w-full flex-col gap-2"
          onSubmit={(e) => (e.preventDefault(), !entering && enter())}
        >
          <input
            value={me.name ?? ""}
            onChange={(e) => setMe({ ...me, name: e.target.value })}
            onBlur={() => keepMe(me)}
            onKeyDown={(e) =>
              clipboardKeys(e, (v) => setMe({ ...me, name: v }))
            }
            placeholder={sysName || "名字"}
            aria-label="名字"
            spellCheck={false}
            autoComplete="name"
            className="h-10 rounded-lg bg-fill px-3 text-center text-sm outline-none select-text placeholder:text-ink-3"
          />
          <button
            type="submit"
            disabled={entering}
            className="g-press flex h-10 items-center justify-center gap-2 rounded-lg bg-ink text-sm font-medium text-paper disabled:opacity-80"
          >
            {entering && <LoaderCircle size={15} className="animate-spin" />}
            开始
          </button>
        </form>
        <Fold state={note && !note.server ? "in" : "out"}>
          <p
            className={cn(
              "m-0 text-center text-xs",
              note?.error ? "text-err" : "text-ink-3",
            )}
          >
            {note?.text}
          </p>
        </Fold>
      </div>
      <Row
        icon={<Plus size={16} />}
        title="添加服务器"
        onClick={more}
        end={<ChevronRight size={16} className="text-ink-3" />}
      />
    </>
  );
}

/** A row of the app's lists: a face or an icon, a name, a second line, what is at its right. The
 *  rows are all of one height (docs/前端设计规范.md "行高一致"). */
function Row({
  icon,
  title,
  sub,
  end,
  onClick,
  onContextMenu,
  tone,
  active,
}: {
  icon: ReactNode;
  title: ReactNode;
  sub?: ReactNode;
  end?: ReactNode;
  onClick?: () => void;
  onContextMenu?: (e: React.MouseEvent) => void;
  tone?: string;
  active?: boolean; // the one shown beside the list
}) {
  const body = (
    <>
      <span className="grid size-9 shrink-0 place-items-center">{icon}</span>
      <span className="flex min-w-0 flex-1 flex-col">
        <span className={cn("truncate text-sm font-medium", tone)}>
          {title}
        </span>
        {sub !== undefined && (
          <span className="truncate text-xs text-ink-3">{sub}</span>
        )}
      </span>
    </>
  );
  return (
    <div
      onContextMenu={onContextMenu}
      className={cn(
        "group flex min-h-13 items-center gap-1 rounded-md pr-2",
        onClick && "hover:bg-fill",
        active && "bg-fill",
      )}
    >
      {onClick ? (
        <button
          type="button"
          onClick={onClick}
          className="g-press flex min-w-0 flex-1 items-center gap-3 py-2 pl-2 text-left"
        >
          {body}
        </button>
      ) : (
        <span className="flex min-w-0 flex-1 items-center gap-3 py-2 pl-2">
          {body}
        </span>
      )}
      {end && <span className="flex shrink-0 items-center gap-2">{end}</span>}
    </div>
  );
}

const DOT = {
  on: "var(--plus)",
  slow: "var(--need)",
  away: "var(--err)",
  none: "var(--ink-4)",
};

/** A join's link as its row says it: a lamp and its words. */
function linkOf(s: Server): { dot: string; text: string } {
  if (!s.joined) return { dot: DOT.none, text: "未授权" };
  const l = s.link;
  if (l?.up)
    return { dot: DOT.on, text: l.rtt ? `已连接 ${l.rtt} ms` : "已连接" };
  // No record of it yet (just granted, the runtime starting), or one without a time: 连接中.
  const since = l ? Date.parse(l.since) : NaN;
  const mins = since > 0 ? Math.floor((Date.now() - since) / 60000) : 0;
  if (mins < 1) return { dot: DOT.slow, text: "连接中" };
  return {
    dot: DOT.away,
    text:
      mins < 60 ? `断开 ${mins} 分钟` : `断开 ${Math.floor(mins / 60)} 小时`,
  };
}

function Lamp({ color }: { color: string }) {
  return (
    <span
      className="mr-1.5 inline-block size-1.5 rounded-full align-middle"
      style={{ background: color }}
    />
  );
}

function Face({ s, size = 36 }: { s: Server; size?: number }) {
  // The Iridium's own face (GET /v1/internal/face, set in its 应用管理), not who one is there (kumo,
  // 2026-11-23: as a game's server list, the server's picture, the name one's own): round; none
  // set, Iridium's mark in the circle. No mark on a face set here: every one is an Iridium.
  // None set: its name's first letter in a grey circle, as the app's rail draws it.
  if (!s.face)
    return (
      <span
        className="grid shrink-0 place-items-center rounded-full bg-fill-2 font-semibold text-ink-2"
        style={{ width: size, height: size, fontSize: size * 0.4 }}
      >
        {[...(s.title || s.host)][0]?.toUpperCase()}
      </span>
    );
  return (
    <img
      src={s.face}
      alt=""
      draggable={false}
      className="shrink-0 rounded-full object-cover"
      style={{ width: size, height: size }}
    />
  );
}

/** Who one is on an Iridium (its page told the app): its face and name, in that Iridium's own page
 *  of the picker. */
function Account({ s }: { s: Server }) {
  if (!s.loggedIn || !s.name) return null;
  return (
    <span className="flex min-w-0 items-center gap-1.5">
      {s.avatar ? (
        <img
          src={s.avatar}
          alt=""
          draggable={false}
          className="size-4 shrink-0 rounded-full object-cover"
        />
      ) : null}
      <span className="truncate">{s.name}</span>
    </span>
  );
}

/** The picker's list: 此电脑, each Iridium, 添加服务器. */
function Servers({
  mode,
  st,
  busy,
  note,
  login,
  back,
}: {
  /** add: 添加服务器 (the rail's +): the field, and those not logged in to, to log in;
   *  local: 本机与授权's 此电脑 (the rail's foot). Those logged in to are the rail's. */
  mode: "add" | "local";
  st: State;
  busy: string;
  note: Note | null;
  login: (to: string, key: string) => void;
  back?: () => void;
}) {
  const local = st.servers.find((s) => s.local);
  const remote =
    mode === "add"
      ? st.servers.filter((s) => !s.local && (!s.loggedIn || busy === s.url))
      : [];
  const rows = useFolding(remote, (s) => s.url);
  const on = !!local && st.server !== false;
  const p = st.publicState;
  const mineOf = (url?: string) =>
    note && url && note.server === url ? note : null;
  const localNote = mineOf(local?.url);
  const menu = useServerMenu();

  return (
    <>
      {back && (
        <button
          type="button"
          onClick={back}
          className="g-press mb-2 flex items-center gap-1 self-start rounded-md px-1 py-1 text-sm text-ink-2 hover:text-ink"
        >
          <ChevronLeft size={16} />
          返回
        </button>
      )}

      {mode === "local" && <Heading>此电脑</Heading>}
      <Fold state={mode === "local" ? "in" : "out"}>
        {/* 此电脑 (kumo, 2026-11-22): one row. Its lamp and state, the public address after it; at
          its right two keys, no switches: 公网访问 (lit when on) and run or stop. Stopping, and
          closing the public address, are pressed twice (docs/前端设计规范.md "确认在原处"). */}
        <Row
          icon={
            <span className="grid size-9 place-items-center rounded-full bg-fill text-ink-2">
              <Monitor size={16} />
            </span>
          }
          title="此电脑"
          sub={
            localNote ? (
              <span className={localNote.error ? "text-err" : undefined}>
                {localNote.text}
              </span>
            ) : (
              <>
                <Lamp
                  color={
                    !on
                      ? DOT.none
                      : (local?.status ?? "运行中") === "运行中"
                        ? DOT.on
                        : DOT.slow
                  }
                />
                {on ? (local?.status ?? "运行中") : "已停止"}
                {on && p?.busy && p.note
                  ? " · " + p.note
                  : on && st.public && st.publicAddress
                    ? " · " + st.publicAddress.replace(/^https?:\/\//, "")
                    : ""}
              </>
            )
          }
          onClick={local && on ? () => ask("ir:open:" + local.url) : undefined}
          end={
            <>
              {on &&
                (p?.busy ? (
                  <span className="grid size-8 place-items-center">
                    <LoaderCircle
                      size={15}
                      className="animate-spin text-ink-3"
                    />
                  </span>
                ) : st.public ? (
                  <SureKey
                    label="公网访问：已开启"
                    sure="确认关闭公网访问"
                    run={() => ask("ir:public:off")}
                    className="text-plus hover:text-plus"
                  >
                    <Globe size={16} />
                  </SureKey>
                ) : (
                  <IconKey
                    label="开启公网访问"
                    onClick={() => ask("ir:public:on")}
                    className="text-ink-4"
                  >
                    <Globe size={16} />
                  </IconKey>
                ))}
              {on ? (
                <SureKey
                  label="停止"
                  sure="确认停止"
                  run={() => ask("ir:server:off")}
                >
                  <Pause size={15} />
                </SureKey>
              ) : (
                <IconKey label="启动" onClick={() => ask("ir:server:on")}>
                  <Play size={15} />
                </IconKey>
              )}
            </>
          }
        />
        <Fold state={on ? "in" : "out"}>
          <Fold state={p?.link ? "in" : "out"}>
            <Row
              icon={<span />}
              title="Cloudflare 授权"
              sub={p?.link}
              end={p?.link && <CopyKey text={p.link} />}
            />
          </Fold>
          <Fold state={p?.invite ? "in" : "out"}>
            <Row
              icon={<span />}
              title="邀请"
              sub={p?.invite}
              end={p?.invite && <CopyKey text={p.invite} />}
            />
          </Fold>
          <Fold state={p?.error ? "in" : "out"}>
            <Row icon={<span />} title={p?.error} tone="text-err" />
          </Fold>
        </Fold>
        {/* In the rail or not (Config.HideLocal; the tray's 此电脑 › 隐藏): nothing of it stops. */}
        <Fold state={local ? "in" : "out"}>
          <Row
            icon={
              <span className="grid size-9 place-items-center text-ink-2">
                <EyeOff size={16} />
              </span>
            }
            title={<span className="font-normal">在左侧栏显示此电脑</span>}
            end={
              <Switch
                label="在左侧栏显示此电脑"
                on={!st.hideLocal}
                onChange={(v) => ask("ir:hidelocal:" + (v ? "off" : "on"))}
              />
            }
          />
        </Fold>
      </Fold>
      {mode === "add" && rows.length > 0 && <Heading>未登录</Heading>}
      {rows.map(({ item: s, key, state }) => {
        const mine = mineOf(s.url);
        const naming = menu.renaming === s.url;
        return (
          <Fold key={key} state={state}>
            <Row
              icon={<Face s={s} />}
              title={
                naming ? (
                  <TitleField s={s} done={menu.stopRenaming} />
                ) : (
                  s.title || s.host
                )
              }
              onContextMenu={menu.open(s)}
              sub={
                mine ? (
                  <span className={mine.error ? "text-err" : undefined}>
                    {mine.text}
                  </span>
                ) : (
                  <>
                    <Lamp color={s.loggedIn ? DOT.on : DOT.none} />
                    {s.loggedIn ? s.host : "未登录"}
                  </>
                )
              }
              onClick={
                naming
                  ? undefined
                  : () =>
                      s.loggedIn ? ask("ir:open:" + s.url) : login(s.url, s.url)
              }
              end={
                busy === s.url ? (
                  <LoaderCircle
                    size={16}
                    className="mr-1.5 animate-spin text-ink-3"
                  />
                ) : (
                  <>
                    {/* 移除: there as the row is pointed at (the app's window is a computer's). */}
                    <span className="opacity-0 transition-opacity duration-(--dur-1) group-hover:opacity-100 focus-within:opacity-100">
                      <SureKey
                        label="移除"
                        sure="确认移除"
                        run={() => ask("ir:remove:" + s.url)}
                        className="size-7"
                      >
                        <Trash2 size={14} />
                      </SureKey>
                    </span>
                  </>
                )
              }
            />
          </Fold>
        );
      })}

      {menu.menu((url) => [
        {
          icon: <Trash2 size={15} />,
          label: "移除",
          sure: "确认移除",
          run: () => ask("ir:remove:" + url),
        },
      ])}
    </>
  );
}

/** 添加服务器 (the rail's +; otae-c, 霓虹, 2026-11-26): the Iridium pages' splash over a field for
 *  an address or an invite link, in the middle of the page; what came of it under the field; those
 *  in the list not logged in to under that, to log in. */
function AddPage({
  st,
  busy,
  note,
  login,
  back,
}: {
  st: State;
  busy: string;
  note: Note | null;
  login: (to: string, key: string) => void;
  back?: () => void;
}) {
  return (
    <div className="flex min-h-full w-full flex-col items-center px-6 pt-12 pb-8">
      <AppSplash className="h-[clamp(180px,38vh,340px)] w-full max-w-[720px] shrink-0" />
      <div className="flex w-full max-w-[460px] flex-col">
        <AddField st={st} busy={busy} login={login} />
        <Fold state={note && !note.server ? "in" : "out"}>
          <p
            className={cn(
              "m-0 px-4 pt-2 text-center text-xs",
              note?.error ? "text-err" : "text-ink-3",
            )}
          >
            {note?.text}
          </p>
        </Fold>
        <div className="mt-6 flex flex-col">
          <Servers mode="add" st={st} busy={busy} note={note} login={login} />
        </div>
        {back && (
          <button
            type="button"
            onClick={back}
            className="g-press mt-4 flex items-center gap-1 self-center rounded-md px-2 py-1 text-sm text-ink-3 hover:text-ink"
          >
            <ChevronLeft size={16} />
            返回
          </button>
        )}
      </div>
    </div>
  );
}

/** The field of 添加服务器: one card 48 points high, as the composer's. Its mark says what is in
 *  it (an invite link, an address); its key at the right connects, as Enter does; a paste is
 *  handled as it lands. No field waits with the focus in it (kumo, 2026-11-22). Empty, with 此电脑
 *  hidden, its key brings 此电脑 back. */
function AddField({
  st,
  busy,
  login,
}: {
  st: State;
  busy: string;
  login: (to: string, key: string) => void;
}) {
  const [address, setAddress] = useState("");
  useEffect(() => {
    if (busy === "") setAddress("");
  }, [busy]);
  const v = address.trim();
  const invite = /\/_ir\/join/.test(v);
  const go = (to: string) => to && login(to, "+");
  return (
    <form
      className="flex h-12 items-center gap-2 rounded-xl border border-edge bg-paper pr-1.5 pl-3.5 shadow-[0_1px_3px_rgb(0_0_0/0.05)] transition-[border-color] focus-within:border-ink-4"
      onSubmit={(e) => (e.preventDefault(), go(v))}
    >
      <span className="grid size-5 shrink-0 place-items-center text-ink-3">
        {invite ? <Ticket size={16} /> : <Globe size={16} />}
      </span>
      <input
        value={address}
        onChange={(e) => setAddress(e.target.value)}
        onPaste={(e) => {
          const p = e.clipboardData.getData("text").trim();
          if (!p) return;
          e.preventDefault();
          setAddress(p);
          go(p);
        }}
        onKeyDown={(e) => {
          clipboardKeys(e, (x) => (setAddress(x), x.trim() && go(x.trim())));
          if (e.key === "Escape") setAddress("");
        }}
        placeholder="服务器地址或邀请链接"
        aria-label="服务器地址或邀请链接"
        spellCheck={false}
        autoCapitalize="off"
        autoComplete="url"
        className="h-full min-w-0 flex-1 bg-transparent text-sm outline-none select-text placeholder:text-ink-3"
      />
      {/* Pressed, the field keeps the focus: it would go, and the key with it. */}
      {busy === "+" ? (
        <span className="grid size-9 shrink-0 place-items-center">
          <LoaderCircle size={16} className="animate-spin text-ink-3" />
        </span>
      ) : !v && st.hideLocal ? (
        <IconKey
          label="添加此电脑"
          onMouseDown={(e) => e.preventDefault()}
          onClick={() => ask("ir:hidelocal:off")}
          className="size-9 rounded-lg"
        >
          <Monitor size={16} />
        </IconKey>
      ) : (
        <button
          type="submit"
          aria-label="连接"
          disabled={!v}
          onMouseDown={(e) => e.preventDefault()}
          className="g-press grid size-9 shrink-0 place-items-center rounded-lg bg-ink text-paper transition-opacity disabled:opacity-25"
        >
          <ArrowRight size={16} />
        </button>
      )}
    </form>
  );
}

/** 授权操作: the Iridiums this computer lets work here, as many as wanted, each with its link and
 *  its switch; › to what it may do here. Those not logged in to are not here: nothing can be
 *  granted to them yet; with none, the part is not there. */
function Grants({
  st,
  open,
  shown,
}: {
  st: State;
  open: (s: Server) => void;
  shown?: string; // beside the list (wide): the one shown, its row pressed shows it
}) {
  const rows = useFolding(
    st.servers.filter((s) => !s.local && s.loggedIn),
    (s) => s.url,
  );
  const menu = useServerMenu();
  return (
    // Open from the first draw when there are any: taken from the rows, which come a draw later,
    // the part folded open from the top each time the list was shown (kumo, 2026-11-22).
    <Fold state={st.servers.some((s) => !s.local && s.loggedIn) ? "in" : "out"}>
      <Heading className="mt-3">授权操作</Heading>
      {rows.map(({ item: s, key, state }) => {
        const l = linkOf(s);
        return (
          <Fold key={key} state={state}>
            <Row
              active={shown === s.url}
              onClick={shown !== undefined ? () => open(s) : undefined}
              icon={<Face s={s} />}
              title={
                menu.renaming === s.url ? (
                  <TitleField s={s} done={menu.stopRenaming} />
                ) : (
                  s.title || s.host
                )
              }
              onContextMenu={menu.open(s)}
              sub={
                <>
                  <Lamp color={l.dot} />
                  {l.text}
                </>
              }
              end={
                <>
                  <Switch
                    label={"授权操作 " + s.host}
                    on={!!s.joined}
                    onChange={(v) =>
                      ask(
                        "ir:terms:" + JSON.stringify({ url: s.url, joined: v }),
                      )
                    }
                  />
                  {shown === undefined && (
                    <IconKey label={s.host} onClick={() => open(s)}>
                      <ChevronRight size={16} />
                    </IconKey>
                  )}
                </>
              }
            />
          </Fold>
        );
      })}
      {menu.menu((url) => [
        {
          icon: <ChevronRight size={15} />,
          label: "授权设置",
          run: () => {
            const t = st.servers.find((x) => x.url === url);
            if (t) open(t);
          },
        },
      ])}
    </Fold>
  );
}

/** The name an Iridium goes by in this app (its windows' titles, the task bar's, the tray's; kumo,
 *  2026-11-23): pressed, a field. The app's alone, so any Iridium's, of any version; its page's own
 *  top left (appkey.tsx AppName) sets the same. Empty: its address. */
function Retitle({ s }: { s: Server }) {
  const [editing, setEditing] = useState(false);
  if (editing) return <TitleField s={s} done={() => setEditing(false)} />;
  return (
    <button
      type="button"
      aria-label="编辑名称"
      onClick={() => setEditing(true)}
      className="-mx-1 max-w-full truncate rounded-sm px-1 text-left hover:bg-fill"
    >
      {s.title || s.host}
    </button>
  );
}

/** The field an Iridium's name here is typed in: Enter or leaving keeps it, Esc does not. */
function TitleField({ s, done }: { s: Server; done: () => void }) {
  const [v, setV] = useState(s.title ?? "");
  const ended = useRef(false);
  const end = (keep: boolean) => {
    if (ended.current) return;
    ended.current = true;
    done();
    const t = v.trim();
    if (keep && t !== (s.title ?? ""))
      ask("ir:title:" + JSON.stringify({ url: s.url, title: t }));
  };
  return (
    <input
      autoFocus
      value={v}
      maxLength={40}
      placeholder={s.host}
      aria-label="编辑名称"
      spellCheck={false}
      onFocus={(e) => e.currentTarget.select()}
      onChange={(e) => setV(e.target.value)}
      onBlur={() => end(true)}
      onClick={(e) => e.stopPropagation()}
      onKeyDown={(e) => {
        if (e.nativeEvent.isComposing || e.keyCode === 229) return;
        if (e.key === "Enter") end(true);
        else if (e.key === "Escape") end(false);
        else clipboardKeys(e, setV);
      }}
      className="h-7 w-full min-w-0 rounded-md bg-fill px-1.5 text-sm font-medium outline-none select-text placeholder:text-ink-3"
    />
  );
}

type MenuEntry = {
  icon: ReactNode;
  label: string;
  run: () => void;
  sure?: string;
};

/** A row's menu at the pointer, on a right click (kumo, 2026-11-23): what one changes of an
 *  Iridium here; 编辑名称 first. One at a time, for the lists of the picker. */
function useServerMenu() {
  const [at, setAt] = useState<{ url: string; x: number; y: number } | null>(
    null,
  );
  const [renaming, setRenaming] = useState<string | null>(null);
  const [armed, setArmed] = useState<string>();
  const anchor = usePointAnchor(at && { x: at.x, y: at.y });
  return {
    renaming,
    stopRenaming: () => setRenaming(null),
    open: (s: Server) => (e: React.MouseEvent) => {
      e.preventDefault();
      setArmed(undefined);
      setAt({ url: s.url, x: e.clientX, y: e.clientY });
    },
    menu: (items: (s: string) => MenuEntry[]) => (
      <Menu.Root open={at !== null} onOpenChange={(o) => !o && setAt(null)}>
        <MenuPortal>
          <Menu.Positioner
            anchor={anchor}
            side="bottom"
            align="start"
            sideOffset={4}
            collisionPadding={8}
            className="z-50"
          >
            <Menu.Popup
              finalFocus={false}
              className="g-unfold glass thick float min-w-40 rounded-lg p-1.5 text-ink outline-none"
            >
              {at &&
                ([] as MenuEntry[])
                  .concat(
                    {
                      icon: <PencilLine size={15} />,
                      label: "编辑名称",
                      run: () => setRenaming(at.url),
                    },
                    items(at.url),
                  )
                  .map((it) => {
                    const asks = !!it.sure && armed !== it.label;
                    const red = !!it.sure && armed === it.label;
                    return (
                      <Menu.Item
                        key={it.label}
                        closeOnClick={!asks}
                        onClick={asks ? () => setArmed(it.label) : it.run}
                        className={cn(
                          "flex h-8 cursor-default items-center gap-2.5 rounded-md px-2.5 text-sm outline-none select-none data-highlighted:bg-fill",
                          red && "text-err",
                        )}
                      >
                        <span className={red ? "text-err" : "text-ink-2"}>
                          {it.icon}
                        </span>
                        {red ? it.sure : it.label}
                      </Menu.Item>
                    );
                  })}
            </Menu.Popup>
          </Menu.Positioner>
        </MenuPortal>
      </Menu.Root>
    ),
  };
}

/** A list's title in a popover, as the web app's (create.tsx). */
function Heading({
  children,
  className,
}: {
  children: ReactNode;
  className?: string;
}) {
  return (
    <p className={cn("m-0 px-2.5 pt-1.5 pb-1 text-xs text-ink-3", className)}>
      {children}
    </p>
  );
}

/** One server's own page: 授权操作, then what it may do here; 移除. */
function ServerPage({
  s,
  agents,
  back,
}: {
  s?: Server;
  agents: string[];
  back?: () => void; // none: beside the list (wide), no 返回
}) {
  if (!s) return null;
  const terms = (t: object) =>
    ask("ir:terms:" + JSON.stringify({ url: s.url, ...t }));
  const roots = s.roots ?? [];
  const off = s.agentsOff ?? [];
  const l = linkOf(s);
  return (
    <>
      {back && (
        <button
          type="button"
          onClick={back}
          className="g-press mb-2 flex items-center gap-1 self-start rounded-md px-1 py-1 text-sm text-ink-2 hover:text-ink"
        >
          <ChevronLeft size={16} />
          返回
        </button>
      )}
      <Row
        icon={<Face s={s} />}
        title={<Retitle s={s} />}
        sub={
          <>
            <Lamp color={l.dot} />
            {l.text}
          </>
        }
      />
      {s.loggedIn && s.name && (
        <Row
          icon={<span />}
          title={<span className="font-normal text-ink-2">登录身份</span>}
          end={
            <span className="max-w-40 text-sm text-ink-2">
              <Account s={s} />
            </span>
          }
        />
      )}
      <div className="mt-4" />
      <Row
        icon={<ShieldCheck size={16} className="text-ink-2" />}
        title="授权操作"
        sub="允许在这台电脑上操作"
        end={
          <Switch
            label="授权操作"
            on={!!s.joined}
            onChange={(v) => terms({ joined: v })}
          />
        }
      />
      <Fold state={s.joined ? "in" : "out"}>
        <Row
          icon={<SquareTerminal size={16} className="text-ink-2" />}
          title="命令行"
          sub="执行命令、打开终端"
          end={
            <Switch
              label="命令行"
              on={!!s.shell}
              onChange={(v) => terms({ shell: v })}
            />
          }
        />
        {agents.map((a) => (
          <Row
            key={a}
            icon={<Logo agent={a as Agent} size={16} />}
            title={agentName(a as Agent)}
            end={
              <Switch
                label={agentName(a as Agent)}
                on={!off.includes(a)}
                onChange={(v) => terms({ agents: { [a]: v } })}
              />
            }
          />
        ))}
        <Row
          icon={<Folder size={16} className="text-ink-2" />}
          title="文件夹"
          sub={roots.length ? `仅 ${roots.length} 个` : "全部"}
          end={
            <IconKey
              label="添加文件夹"
              onClick={() => ask("ir:pickroot:" + s.url)}
            >
              <FolderPlus size={15} />
            </IconKey>
          }
        />
        {roots.map((r) => (
          <Row
            key={r}
            icon={<span />}
            title={<span className="font-normal text-ink-2">{r}</span>}
            end={
              <SureKey
                label="移除"
                sure="确认移除"
                run={() =>
                  roots.length > 1
                    ? terms({ roots: roots.filter((x) => x !== r) })
                    : terms({ all: true })
                }
                className="size-7"
              >
                <Trash2 size={14} />
              </SureKey>
            }
          />
        ))}
      </Fold>
    </>
  );
}
