// The rows about this page and the Iridium it talks to, not about anyone in it: installing the page
// as an app, the update, signing out. The settings' panel (settings.tsx) and 管理's Ir (manage.tsx)
// draw them the same.
import { ChevronRight, LoaderCircle, LogOut, MonitorDown, RefreshCw, Share, SquarePlus } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { api } from "./api/client";
import type { UpdateState } from "./api/types.gen";
import { useUpdate } from "./data";
import { t, useLang } from "./i18n";
import { install, useInstall } from "./install";
import { Item } from "./rows";
import { errText, toast } from "./toast";
import { cn, IconKey, Tip } from "./ui";
import { UpdateAsk } from "./updateask";

/** 添加到主屏幕 (install.ts): the browser's prompt on a key, 安装为应用 on a computer; on iOS and Safari on a Mac, whose
 *  browsers keep it in their own menu, that menu's name. */
export function InstallItem() {
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

/** 检查更新, the update in one row (the user, 2026-11-09: no notice above the profile). Up to date,
 *  检查更新 with the version now (the user, 2026-11-12: not 已是最新; when it last looked in its tip) and a key that looks; a newer
 *  release found, it asks first (otae, 2026-11-22: never updated by the look alone): a popup out of
 *  the row (over the list, taking no room in it) with the version, what is in it, the version now
 *  and its size, 取消 or 更新, the one 更新 there is, which downloads it, puts it in place and
 *  restarts; the page then loads the new build (the user, 2026-11-08). A newer one out, the row is
 *  版本 with its version, pressed the same popup (no key of its own: two 更新 were one too many,
 *  otae), or how far its download is, a hairline along the row's foot. Those who do not manage see the versions, no key. The daemon
 *  never looks or downloads by itself (kumo, 2026-11-26): each time the settings open, for those
 *  who manage where there is a source, the page looks once (not again while a look runs; a failure
 *  quiet, the row says it). */
export function UpdateItem({ manages, shown }: { manages: boolean; shown: boolean }) {
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
  // Each opening looks once, as soon as it is known there is a source; one already running (another
  // page's, an update's) is this opening's. Put away, the next opening looks again.
  const may = shown && manages && !!u?.source;
  const looked = useRef(false);
  useEffect(() => {
    if (!shown) looked.current = false;
    if (!may || looked.current) return;
    looked.current = true;
    if (!working) look();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [may, shown]);
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
export function SignOut() {
  const [sure, setSure] = useState(false);
  useEffect(() => {
    if (!sure) return;
    const t = setTimeout(() => setSure(false), 3000);
    return () => clearTimeout(t);
  }, [sure]);
  const out = () =>
    void api.logout().then(
      () => void import("./device/forget").then((m) => m.forget()).finally(() => location.assign("/_ir/login")),
      (e) => toast(t("me.logout"), { error: true, description: errText(e) }),
    );
  return (
    <button type="button" onClick={() => (sure ? out() : setSure(true))} onBlur={() => setSure(false)} className={cn("g-press -mr-1.5 mt-2 flex h-8 max-w-full items-center gap-1.5 self-end rounded-md px-2 text-sm hover:bg-fill hover:text-ink pointer-coarse:h-10", sure ? "text-ink" : "text-ink-3")}>
      <LogOut size={14} className="shrink-0" />
      <span key={String(sure)} className="g-fade min-w-0 truncate">{sure ? t("me.logoutSure") : t("me.logout")}</span>
    </button>
  );
}
