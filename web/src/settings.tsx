// The 终端 panel (plan "运行", "推送", "更新", "网页", "交互终端"): this terminal and the settings in
// one (the user, 2026-11-07), one list with no groups: its profile and device badge, appearance,
// language; for those who manage, the 终端, 凭据 and 用量 pages; Web Push and, while it is on, the events
// it pushes where no workspace, folder or session says otherwise (push.ts: their menus); what
// sending does while a turn runs; the update; signing out. Not here (the user, 2026-11-08): its
// permissions (the 终端 page shows each terminal's) and the approval mode of new sessions (the
// config file's, else each agent's own). Each a title and its control, no line explaining it;
// no version shown, no head, signing out a plain row, asked once (the user, 2026-11-08). Every quota bar shows: nothing here hides one.
// Changed is saved at once (GET/PUT /v1/internal/settings). Reaping idle agents and going on after a crash
// or a quota are not here: as the config file says, by default 1h and on (the user, 2026-11-07).
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronLeft, ChevronRight, LoaderCircle, LogOut, MonitorDown, RefreshCw, Share, SquarePlus } from "lucide-react";
import { useEffect, useState, type ReactNode } from "react";
import { api } from "./api/client";
import type { PushEvents, PushState, Settings, SettingsPatch, UpdateState } from "./api/types.gen";
import { manages as managesApps, pushQ, useApps, useMe, useSettings, useUpdate } from "./data";
import { t, useLang } from "./i18n";
import { install, useInstall } from "./install";
import { LangSwitch, ThemeSwitch, type ThemePref } from "./me";
import { AvatarCrop, Profile, ProfileMore } from "./profile";
import { PUSH_EVENTS, subscribe } from "./pushmenu";
import { go } from "./route";
import { useConfirms } from "./store";
import { errText, toast } from "./toast";
import { cn, IconKey, Switch, Tip } from "./ui";
import { UpdateAsk } from "./updateask";

/** The panel's own pages: the avatar's crop. */
export type Page = "main" | "crop";

/** In a small dialog rising from the me key (app.tsx); on a phone a sheet from the window's foot (the
 *  user, 2026-11-06). No close key: a press anywhere else puts it away (the user, 2026-11-06).
 *  initial: the page it opens on. */
export function SettingsPage({ theme, setTheme, initial = "main" }: { theme: ThemePref; setTheme: (v: ThemePref) => void; initial?: Page }) {
  const lang = useLang();
  const qc = useQueryClient();
  const q = useSettings();
  const s = q.data ?? undefined;
  const off = !s; // not come yet, or an older daemon
  const me = useMe().data;
  const manages = managesApps(me);
  const terminals = useApps().data?.apps ?? [];
  const confirms = useConfirms();
  const upd = useUpdate().data;
  const put = async (p: SettingsPatch) => {
    if (!s) return;
    const next: Settings = { ...s, ...(p as Partial<Settings>), modes: p.modes ? Object.fromEntries(Object.entries({ ...s.modes, ...p.modes }).filter(([, v]) => v)) : s.modes, quotaHidden: p.quotaHidden ?? s.quotaHidden };
    qc.setQueryData(["settings"], next);
    try {
      qc.setQueryData(["settings"], await api.putSettings(p));
    } catch (e) {
      qc.setQueryData(["settings"], s);
      toast(t("err.settings"), { error: true, description: errText(e) });
    }
  };
  const pq = useQuery(pushQ);
  const push: PushState | null | undefined = pq.data;
  const setDefault = (k: keyof PushEvents, on: boolean) => {
    if (!push) return;
    const next = { ...push.defaults, [k]: on };
    qc.setQueryData(["push"], { ...push, defaults: next });
    api.pushDefaults(next).then(
      (st) => qc.setQueryData(["push"], st),
      (e) => (qc.invalidateQueries({ queryKey: ["push"] }), toast(t("err.settings"), { error: true, description: errText(e) })),
    );
  };
  const [pushing, setPushing] = useState(false);
  const pushed = s?.push ?? push?.enabled ?? false;
  const pushOn = async (on: boolean) => {
    if (!push) return;
    setPushing(true);
    try {
      if (on) await subscribe(push.publicKey);
      if (s) qc.setQueryData(["settings"], await api.putSettings({ push: on }));
      else await api.pushSwitch(on);
      qc.setQueryData(["push"], { ...push, enabled: on });
    } catch (e) {
      toast(t("err.push"), { error: true, description: errText(e) });
    } finally {
      setPushing(false);
    }
  };

  const [page, setPage] = useState<Page>(initial);
  const [picture, setPicture] = useState<string | null>(null);
  const back = () => setPage("main");
  const nav = (label: string, onClick: () => void, end?: ReactNode) => (
    <button type="button" onClick={onClick} className="g-press -mx-1.5 flex min-h-10 items-center gap-3 rounded-md px-1.5 py-1 text-left hover:bg-fill">
      <span className="min-w-0 flex-1 truncate text-sm">{label}</span>
      {end}
      <ChevronRight size={15} className="shrink-0 text-ink-4" />
    </button>
  );

  if (page === "crop")
    return (
      <div key={page} className="g-slide flex h-full flex-col">
        <header className="flex h-10.5 shrink-0 items-center gap-1 px-1.5 pt-1.5">
          <IconKey label={t("nav.me")} onClick={back} tipSide="bottom" className="size-7">
            <ChevronLeft size={16} />
          </IconKey>
          <h1 className="m-0 min-w-0 flex-1 truncate text-base font-semibold">{t("me.avatar")}</h1>
        </header>
        <div className="no-bar min-h-0 flex-1 overflow-y-auto pt-2 pb-4">
          <div className="flex flex-col px-3">{picture && <AvatarCrop src={picture} onBack={back} />}</div>
        </div>
      </div>
    );

  // One list, no groups: this terminal first, then each setting side by side (the user, 2026-11-07).
  return (
    <div className="flex h-full flex-col" data-dir="back">
      {/* No head: the profile says whose it is, the phone's tab what it is (the user, 2026-11-08). */}
      <div className="no-bar min-h-0 flex-1 overflow-y-auto pt-2.5 pb-4">
        <div className="flex flex-col px-3">
          <Profile onPicture={(url) => (setPicture(url), setPage("crop"))} />
          <ProfileMore />
          <Item label={t("me.theme")}>
            <ThemeSwitch theme={theme} setTheme={setTheme} />
          </Item>
          <Item label={t("me.lang")}>
            <LangSwitch lang={lang} />
          </Item>
          {manages &&
            nav(
              t("settings.terminalsRow"),
              () => go({ name: "apps" }),
              <>
                {confirms.length > 0 && <span className="size-2 shrink-0 rounded-full bg-need" aria-label={t("nav.needsYou")} />}
                {terminals.length > 0 && <span className="text-xs text-ink-3 tabular-nums">{terminals.length}</span>}
              </>,
            )}
          {me?.host && nav(t("secrets.title"), () => go({ name: "secrets" }))}
          {nav(t("role.title"), () => go({ name: "roles" }))}
          <InstallItem />
          <Item label={t("settings.pushOn")}>
            {pushing ? <LoaderCircle size={16} className="animate-spin text-ink-3" /> : <Switch label={t("settings.pushOn")} disabled={!push || !("PushManager" in window)} on={pushed} onChange={(v) => void pushOn(v)} />}
          </Item>
          {/* On, the default events slide open under it (off, shut): nothing below jumps. */}
          <div aria-hidden={!pushed || undefined} inert={!pushed || undefined} className={cn("grid transition-[grid-template-rows,opacity] duration-(--dur-3) ease-(--ease-out)", pushed && push ? "grid-rows-[1fr]" : "grid-rows-[0fr] opacity-0")}>
            <div className="min-h-0 overflow-hidden pl-3">
              {push &&
                PUSH_EVENTS.map((e) => (
                  <Item
                    key={e.k}
                    label={
                      <span className="flex items-center gap-2">
                        <span className="text-ink-3">{e.icon}</span>
                        {t(e.label)}
                      </span>
                    }
                  >
                    <Switch label={t(e.label)} on={push.defaults[e.k]} onChange={(v) => setDefault(e.k, v)} />
                  </Item>
                ))}
            </div>
          </div>
          {/* 自动更新: off, nothing is looked for or downloaded unasked; 检查更新 still does it all. */}
          {manages && upd?.source && (
            <Item label={t("settings.autoUpdate")}>
              <Switch label={t("settings.autoUpdate")} disabled={off} on={s?.autoUpdate ?? !upd.manual} onChange={(v) => void put({ autoUpdate: v })} />
            </Item>
          )}
          <UpdateItem manages={manages} />
          {me && !me.host && <SignOut />}
        </div>
      </div>
    </div>
  );
}

/** fold: shut till its title is pressed (the approval modes, the user, 2026-11-06). */
export function Group({ title, children, fold, action }: { title: string; children: ReactNode; fold?: boolean; action?: ReactNode }) {
  const [open, setOpen] = useState(!fold);
  if (!fold)
    return (
      <section className="flex flex-col">
        <h2 className="m-0 mb-1 flex h-4 items-center text-xs font-medium text-ink-3">
          <span className="min-w-0 flex-1 truncate">{title}</span>
          {action}
        </h2>
        {children}
      </section>
    );
  return (
    <section className="flex flex-col">
      <h2 className="m-0 mb-1 text-xs font-medium text-ink-3">
        <button type="button" onClick={() => setOpen(!open)} aria-expanded={open} className="-mx-1 flex items-center gap-1 rounded-sm px-1 hover:text-ink-2">
          {title}
          <ChevronRight size={12} className="transition-transform duration-(--dur-2) ease-(--ease-out)" style={{ transform: open ? "rotate(90deg)" : undefined }} />
        </button>
      </h2>
      {open && <div className="g-fade flex flex-col">{children}</div>}
    </section>
  );
}

/** below: its control under the words, the whole width (a select whose options are long). */
export function Item({ label, hint, children, below }: { label: ReactNode; hint?: ReactNode; children: ReactNode; below?: boolean }) {
  if (below)
    return (
      <div className="flex flex-col gap-1.5 py-1.5">
        <div>
          <div className="text-sm">{label}</div>
          {hint && <div className="text-xs text-ink-3">{hint}</div>}
        </div>
        {children}
      </div>
    );
  return (
    <div className="flex min-h-10 items-center gap-3 py-1">
      <div className="min-w-0 flex-1">
        <div className="text-sm">{label}</div>
        {hint && <div className="text-xs text-ink-3">{hint}</div>}
      </div>
      <div className="flex shrink-0 items-center">{children}</div>
    </div>
  );
}

/** 添加到主屏幕 (install.ts), above Web Push (an iPhone pushes only to a page on its home screen):
 *  the browser's prompt on a key, 安装为应用 on a computer; on iOS and Safari on a Mac, whose
 *  browsers keep it in their own menu, that menu's name. */
function InstallItem() {
  const how = useInstall();
  if (!how) return null;
  if (how === "key") {
    const phone = matchMedia("(pointer: coarse)").matches;
    const label = t(phone ? "settings.install" : "settings.installApp");
    return (
      <Item label={label}>
        <IconKey label={label} onClick={() => void install()} className="size-7">
          {phone ? <SquarePlus size={15} /> : <MonitorDown size={15} />}
        </IconKey>
      </Item>
    );
  }
  return (
    <Item label={t(how === "ios" ? "settings.install" : "settings.installDock")}>
      <span className="flex items-center gap-1 text-xs text-ink-3">
        {how === "ios" && <Share size={13} />}
        {t(how === "ios" ? "settings.installIos" : "settings.installMac")}
      </span>
    </Item>
  );
}


/** Looking again on its own: a page that opens the settings with the last look over an hour ago
 *  asks for one (the daemon looks once a day by itself). */
const STALE = 3_600_000;

/** 检查更新, the update in one row (the user, 2026-11-09: no notice above the profile). Up to date,
 *  检查更新 with the version now (the user, 2026-11-12: not 已是最新; when it last looked in its tip) and a key that looks; a newer
 *  release found, it asks first (otae, 2026-11-22: never updated by the look alone): a popup out of
 *  the row (over the list, taking no room in it) with the version, what is in it, the version now
 *  and its size, 取消 or 更新, the one 更新 there is, which downloads it, puts it in place and
 *  restarts; the page then loads the new build (the user, 2026-11-08). A newer one out, the row is
 *  版本 with its version, pressed the same popup (no key of its own: two 更新 were one too many,
 *  otae), or how far its download is, a hairline along the row's foot. Those who do not manage see the versions, no key. Release builds download a newer one ahead
 *  by themselves unless 自动更新 is off (u.manual); then the page does not look on opening either. */
function UpdateItem({ manages }: { manages: boolean }) {
  const lang = useLang();
  const u = useUpdate().data;
  const [busy, setBusy] = useState(false);
  // Asking whether to update to the newer one found.
  const [ask, setAsk] = useState(false);
  // The 更新 key, which the ask pops out of.
  const [keyEl, setKeyEl] = useState<HTMLButtonElement | null>(null);
  const working = u?.phase === "checking" || u?.phase === "downloading" || u?.phase === "restarting";
  // asking: a newer one found is asked about (the key; not the look on opening).
  const look = (asking = false) => {
    setBusy(true);
    void api.checkUpdate().then(
      (st: UpdateState) => (setBusy(false), asking && st.available && setAsk(true)),
      () => setBusy(false), // quiet: the status says it failed
    );
  };
  // Opened with the last look stale: look now, saying nothing if it fails.
  const stale = manages && !!u?.source && !u.manual && u.phase === "idle" && Date.now() - (u.checkedAt ?? 0) > STALE;
  useEffect(() => {
    if (stale) look();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [stale]);
  if (!u) return null;
  // Kept while the daemon restarts (available is off by then): the row does not change back as the
  // new build loads.
  const newer = u.available || u.phase === "restarting";
  if (!(manages && u.source) && !newer) return null;
  const pct = u.phase === "downloading" && u.size ? Math.min(99, Math.floor(((u.got ?? 0) * 100) / u.size)) : null;
  const spin = busy || working;
  const failed = u.phase === "failed" && u.error;

  if (newer) {
    const state = pct !== null ? `${pct}%` : u.phase === "restarting" ? t("settings.restarting") : failed ? t("settings.failedShort") : "";
    return (
      <div className="relative">
        {/* The row is the one way in: what is new and the one 更新 are in what it opens. */}
        <button
          ref={setKeyEl}
          type="button"
          disabled={pct !== null || u.phase === "restarting"}
          onClick={() => setAsk(true)}
          aria-haspopup="dialog"
          className="g-press -mx-1.5 flex min-h-10 w-[calc(100%+0.75rem)] items-center gap-3 rounded-md px-1.5 py-1 text-left hover:bg-fill disabled:hover:bg-transparent"
        >
          <span className="flex min-w-0 flex-1 items-center gap-1.5 text-sm">
            <span className="size-1.5 shrink-0 rounded-full bg-err" aria-hidden />
            {t("settings.version")}
          </span>
          {state && <span className={cn("text-xs tabular-nums", failed && pct === null ? "text-err" : "text-ink-3")}>{state}</span>}
          <span className="text-xs font-medium text-ink tabular-nums">{u.latest}</span>
          {pct === null && u.phase !== "restarting" ? <ChevronRight size={15} className="shrink-0 text-ink-4" /> : spin && <LoaderCircle size={13} className="shrink-0 animate-spin text-ink-3" />}
        </button>
        {/* Along the row's foot, taking no room. */}
        {pct !== null && (
          <div className="g-fade absolute inset-x-0 bottom-0 h-0.5 overflow-hidden rounded-full bg-fill" aria-hidden>
            <div className="h-full bg-ink-3 transition-[width] duration-300 ease-out" style={{ width: `${pct}%` }} />
          </div>
        )}
        <UpdateAsk u={u} anchor={keyEl} open={ask} close={() => setAsk(false)} may={manages} />
      </div>
    );
  }

  const checked = u.checkedAt ? new Date(u.checkedAt).toLocaleString(lang === "zh" ? "zh-CN" : "en", { month: "numeric", day: "numeric", hour: "2-digit", minute: "2-digit" }) : "";
  const status = u.phase === "checking" || busy ? t("settings.checking") : u.phase === "failed" ? t("settings.checkFailed") : u.current;
  return (
    <Item label={t("settings.check")}>
      <span className="flex items-center gap-1">
        <Tip label={failed ? u.error : (status === u.current && checked) || status}>
          <span className={`max-w-40 truncate text-xs tabular-nums ${u.phase === "failed" ? "text-err" : "text-ink-3"}`}>{status}</span>
        </Tip>
        <IconKey label={t("settings.check")} disabled={spin} onClick={() => look(true)} className="size-7">
          <RefreshCw size={14} className={spin ? "animate-spin" : undefined} />
        </IconKey>
      </span>
    </Item>
  );
}

/** 退出此终端: a quiet key at the list's foot, on the right (the user, 2026-11-08); never red; the
 *  first press only asks, the second signs out; left alone it asks no more. */
function SignOut() {
  const [sure, setSure] = useState(false);
  useEffect(() => {
    if (!sure) return;
    const t = setTimeout(() => setSure(false), 3000);
    return () => clearTimeout(t);
  }, [sure]);
  const out = () => void api.logout().then(() => location.assign("/_ir/login"), (e) => toast(t("me.logout"), { error: true, description: errText(e) }));
  return (
    <button type="button" onClick={() => (sure ? out() : setSure(true))} onBlur={() => setSure(false)} className={cn("g-press -mr-1.5 mt-2 flex h-8 max-w-full items-center gap-1.5 self-end rounded-md px-2 text-sm hover:bg-fill hover:text-ink pointer-coarse:h-10", sure ? "text-ink" : "text-ink-3")}>
      <LogOut size={14} className="shrink-0" />
      <span key={String(sure)} className="g-fade min-w-0 truncate">{sure ? t("me.logoutSure") : t("me.logout")}</span>
    </button>
  );
}
