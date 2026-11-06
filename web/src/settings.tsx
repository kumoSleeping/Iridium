// The settings (plan "运行", "推送", "更新", "网页", "交互终端"): what the config file holds that
// the page edits (GET/PUT /api/settings): reaping idle agents, going on after a crash or a quota,
// each agent's approval mode for new sessions (folded till opened), Web Push (off by default) and,
// while it is on, the events it pushes where no workspace, folder or session says otherwise
// (push.tsx: their menus); the update; the way to the 终端 page; and this device's own: reminders, appearance,
// language. Each a title and its control, no line explaining it; the version at the foot (the user,
// 2026-11-06). Every quota bar shows: nothing here hides one. Changed is saved at once.
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronRight, LoaderCircle, LockKeyhole, Monitor, Moon, MonitorSmartphone, RefreshCw, Sun } from "lucide-react";
import { useEffect, useState, type ReactNode } from "react";
import { api } from "./api/client";
import type { PushEvents, PushState, Settings, SettingsPatch } from "./api/types.gen";
import { pushQ, useAgents, useMe, useSettings, useTerminals, useUpdate } from "./data";
import { t, useLang } from "./i18n";
import { LangSwitch, Segmented, type ThemePref } from "./me";
import { modeName } from "./modes";
import { PUSH_EVENTS } from "./pushmenu";
import { go } from "./route";
import { useConfirms } from "./store";
import { errText, toast } from "./toast";
import { Choose } from "./choose";
import { agentName, cn, IconKey, Logo, steerKey, Switch, Tip } from "./ui";
import { remindBy, reminding } from "./unread";

const REAP = ["0", "15m", "30m", "1h", "2h", "4h", "12h"];

/** A duration as Go writes it ("1h0m0s"), as short as it goes ("1h"). */
export const shortDuration = (d: string) => d.replace(/([hm])0s$/, "$1").replace(/h0m$/, "h");

/** In a small dialog above its key (app.tsx), as wide as the me popover; on a phone a sheet from the
 *  window's foot, as the me sheet is (the user, 2026-11-06). No close key: a press anywhere else
 *  puts it away (the user, 2026-11-06). */
export function SettingsPage({ theme, setTheme }: { theme: ThemePref; setTheme: (v: ThemePref) => void }) {
  const lang = useLang();
  const qc = useQueryClient();
  const q = useSettings();
  const s = q.data ?? undefined;
  const off = !s; // not come yet, or an older daemon
  const agents = (useAgents().data ?? []).filter((a) => a.modes.length > 1);
  const me = useMe().data;
  const manages = !!me && (me.local || me.kind === "panel");
  const terminals = useTerminals().data?.terminals ?? [];
  const confirms = useConfirms();
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
  const [remind, setRemind] = useState(reminding);
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
  const reap = s ? shortDuration(s.idleReap) : "1h";

  return (
    <div className="flex h-full flex-col">
      {/* A plain head, as the me popover's pages have (the user, 2026-11-06): no bar of its own. */}
      <header className="flex h-10.5 shrink-0 items-center gap-1 px-1.5 pt-1.5">
        <h1 className="m-0 pl-1.5 text-base font-semibold">{t("nav.settings")}</h1>
        {q.isFetching && <LoaderCircle size={14} className="ml-1 animate-spin text-ink-4" />}
      </header>
      <div className="no-bar min-h-0 flex-1 overflow-y-auto pt-2 pb-4">
        <div className="flex flex-col gap-5 px-3">
          <UpdateRow ready />

          <Group title={t("settings.agents")}>
            <Item label={t("settings.idleReap")}>
              <Choose disabled={off} value={reap} onChange={(v) => void put({ idleReap: v })} label={t("settings.idleReap")} options={[...new Set([...REAP, reap])].map((v) => ({ value: v, label: v === "0" ? t("settings.never") : v }))} />
            </Item>
            <Item label={t("settings.crashResume")}>
              <Switch label={t("settings.crashResume")} disabled={off} on={!!s?.crashResume} onChange={(v) => void put({ crashResume: v })} />
            </Item>
            <Item label={t("settings.quotaResume")}>
              <Switch label={t("settings.quotaResume")} disabled={off} on={!!s?.quotaResume} onChange={(v) => void put({ quotaResume: v })} />
            </Item>
            {/* One line, as the rows above (the user, 2026-11-06): the key's short word; the other key in the list's hint. */}
            <Item label={t("settings.enter")}>
              <Choose
                disabled={off}
                value={s?.enterInTurn || "steer"}
                onChange={(v) => void put({ enterInTurn: v })}
                label={t("settings.enter")}
                options={[
                  { value: "steer", label: t("settings.enterSteer"), hint: t("settings.enterSteerHint", { key: steerKey() }) },
                  { value: "queue", label: t("settings.enterQueue"), hint: t("settings.enterQueueHint", { key: steerKey() }) },
                ]}
              />
            </Item>
          </Group>

          {agents.length > 0 && (
            <Group title={t("settings.modes")} fold>
              {agents.map((a) => (
                <Item
                  key={a.agent}
                  label={
                    <span className="flex items-center gap-2">
                      <Logo agent={a.agent} size={15} />
                      {agentName(a.agent)}
                    </span>
                  }
                >
                  <Choose
                    disabled={off}
                    value={s?.modes?.[a.agent] ?? ""}
                    onChange={(v) => void put({ modes: { [a.agent]: v } })}
                    label={`${t("settings.modes")} · ${agentName(a.agent)}`}
                    options={[{ value: "", label: t("settings.agentDefault", { mode: modeName({ id: a.defaultMode }) }) }, ...a.modes.map((m) => ({ value: m, label: modeName({ id: m }) }))]}
                  />
                </Item>
              ))}
            </Group>
          )}

          {manages && (
            <Group title={t("settings.terminals")}>
              <button type="button" onClick={() => go({ name: "terminals" })} className="g-press -mx-1.5 flex min-h-11 items-center gap-2.5 rounded-md px-1.5 text-left hover:bg-fill">
                <MonitorSmartphone size={17} className="shrink-0 text-ink-2" />
                <span className="min-w-0 flex-1 text-sm">{t("settings.terminalsRow")}</span>
                {confirms.length > 0 && <span className="size-2 shrink-0 rounded-full bg-need" aria-label={t("nav.needsYou")} />}
                {terminals.length > 0 && <span className="text-xs text-ink-3 tabular-nums">{terminals.length}</span>}
                <ChevronRight size={15} className="shrink-0 text-ink-4" />
              </button>
              <button type="button" onClick={() => go({ name: "secrets" })} className="g-press -mx-1.5 flex min-h-11 items-center gap-2.5 rounded-md px-1.5 text-left hover:bg-fill">
                <LockKeyhole size={17} className="shrink-0 text-ink-2" />
                <span className="min-w-0 flex-1 text-sm">{t("secrets.title")}</span>
                <ChevronRight size={15} className="shrink-0 text-ink-4" />
              </button>
            </Group>
          )}

          <Group title={t("settings.push")}>
            <Item label={t("settings.pushOn")}>
              {pushing ? <LoaderCircle size={16} className="animate-spin text-ink-3" /> : <Switch label={t("settings.pushOn")} disabled={!push || !("PushManager" in window)} on={pushed} onChange={(v) => void pushOn(v)} />}
            </Item>
            {/* On, the default events slide open under it (off, shut): nothing below jumps. */}
            <div aria-hidden={!pushed || undefined} inert={!pushed || undefined} className={cn("grid transition-[grid-template-rows,opacity] duration-(--dur-3) ease-(--ease-out)", pushed && push ? "grid-rows-[1fr]" : "grid-rows-[0fr] opacity-0")}>
              <div className="min-h-0 overflow-hidden">
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
            <Item label={t("settings.remind")}>
              <Switch
                label={t("settings.remind")}
                disabled={typeof Notification === "undefined"}
                on={remind && typeof Notification !== "undefined" && Notification.permission === "granted"}
                onChange={(v) => void remindBy(v).then(() => setRemind(v))}
              />
            </Item>
          </Group>

          <Group title={t("settings.device")}>
            <Item label={t("me.theme")}>
              <Segmented
                label={t("me.theme")}
                value={theme}
                onChange={(v) => setTheme(v as ThemePref)}
                items={[
                  { v: "light", label: t("me.light"), icon: <Sun size={15} /> },
                  { v: "dark", label: t("me.dark"), icon: <Moon size={15} /> },
                  { v: "auto", label: t("me.auto"), icon: <Monitor size={15} /> },
                ]}
              />
            </Item>
            <Item label={t("me.lang")}>
              <LangSwitch lang={lang} />
            </Item>
          </Group>

          {manages && <UpdateGroup auto={s?.updateAuto ?? true} disabled={off} onAuto={(v) => void put({ updateAuto: v })} />}

          <UpdateRow />
        </div>
      </div>
    </div>
  );
}

/** fold: shut till its title is pressed (the approval modes, the user, 2026-11-06). */
export function Group({ title, children, fold }: { title: string; children: ReactNode; fold?: boolean }) {
  const [open, setOpen] = useState(!fold);
  if (!fold)
    return (
      <section className="flex flex-col">
        <h2 className="m-0 mb-1 text-xs font-medium text-ink-3">{title}</h2>
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

/** A download's size: "9.8 MB". */
const mb = (n: number) => (n >= 1 << 20 ? `${(n / (1 << 20)).toFixed(1)} MB` : `${Math.max(1, Math.round(n / 1024))} KB`);

/** Looking again on its own: a page that opens the settings with the last look over an hour ago
 *  asks for one (the daemon looks once a day by itself). */
const STALE = 3_600_000;

/** The update group (managers only, with a source): 自动下载更新 (a newer one is fetched ahead, so
 *  更新 only restarts) and 检查更新 with how the last look went. */
function UpdateGroup({ auto, disabled, onAuto }: { auto: boolean; disabled: boolean; onAuto: (v: boolean) => void }) {
  const lang = useLang();
  const u = useUpdate().data;
  const [busy, setBusy] = useState(false);
  const working = u?.phase === "checking" || u?.phase === "downloading" || u?.phase === "restarting";
  const look = (quiet?: boolean) => {
    setBusy(true);
    void api.checkUpdate().then(
      () => setBusy(false),
      (e) => (setBusy(false), !quiet && toast(t("err.update"), { error: true, description: errText(e) })),
    );
  };
  // Opened with the last look stale: look now, saying nothing if it fails.
  const stale = !!u?.source && u.phase === "idle" && Date.now() - (u.checkedAt ?? 0) > STALE;
  useEffect(() => {
    if (stale) look(true);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [stale]);
  if (!u?.source) return null;
  const checked = u.checkedAt ? new Date(u.checkedAt).toLocaleString(lang === "zh" ? "zh-CN" : "en", { month: "numeric", day: "numeric", hour: "2-digit", minute: "2-digit" }) : "";
  const status =
    u.phase === "checking" || busy
      ? t("settings.checking")
      : u.phase === "downloading"
        ? t("settings.downloading")
        : u.phase === "failed"
          ? t("settings.checkFailed")
          : u.available
            ? t("settings.updateTo", { v: u.latest ?? "" })
            : checked
              ? `${t("settings.upToDate")} · ${checked}`
              : "";
  return (
    <Group title={t("settings.updates")}>
      <Item label={t("settings.updateAuto")}>
        <Switch label={t("settings.updateAuto")} disabled={disabled} on={auto} onChange={onAuto} />
      </Item>
      <Item label={t("settings.check")}>
        <span className="flex items-center gap-1">
          <Tip label={u.phase === "failed" && u.error ? u.error : status}>
            <span className={`max-w-40 truncate text-xs tabular-nums ${u.phase === "failed" ? "text-err" : "text-ink-3"}`}>{status}</span>
          </Tip>
          <IconKey label={t("settings.check")} disabled={busy || working} onClick={() => look()} className="size-7">
            <RefreshCw size={14} className={busy || u.phase === "checking" ? "animate-spin" : undefined} />
          </IconKey>
        </span>
      </Item>
    </Group>
  );
}

/** The update (the carried "update"): at the foot the version now; once a newer one is out, at the
 *  top (ready) a small notice with its version and size, its notes and the 更新 key, then how
 *  downloading and installing go. */
function UpdateRow({ ready }: { ready?: boolean }) {
  const u = useUpdate().data;
  const me = useMe().data;
  const manages = !!me && (me.local || me.kind === "panel");
  const [busy, setBusy] = useState(false);
  if (!u) return null;
  if (!ready) return <div className="pt-2 text-center text-xs text-ink-4 tabular-nums">Iridium {u.current}</div>;
  if (!u.available) return null;
  const apply = () => {
    setBusy(true);
    void api.applyUpdate().then(
      () => (setBusy(false), toast(t("settings.updating"))),
      (e) => (setBusy(false), toast(t("err.update"), { error: true, description: errText(e) })),
    );
  };
  const working = u.phase === "checking" || u.phase === "downloading" || u.phase === "restarting";
  const pct = u.phase === "downloading" && u.size ? Math.min(99, Math.floor(((u.got ?? 0) * 100) / u.size)) : null;
  const phase =
    u.phase === "downloading" ? (pct !== null ? `${t("settings.downloading")} ${pct}%` : t("settings.downloading")) : u.phase === "restarting" ? t("settings.restarting") : u.phase === "checking" ? t("settings.checking") : u.ready ? t("settings.downloaded") : "";
  return (
    <section className="g-pop flex flex-col gap-2 rounded-md p-2.5 shadow-[0_0_0_1px_var(--edge)]" aria-label={t("settings.updateReady")}>
      <div className="flex items-center gap-3">
        <span className="size-2 shrink-0 rounded-full bg-err" aria-hidden />
        <span className="min-w-0 flex-1 truncate text-sm font-semibold tabular-nums">
          {t("settings.updateTo", { v: u.latest ?? "" })}
        </span>
        {phase && <span className="shrink-0 text-xs text-ink-3 tabular-nums">{phase}</span>}
        {manages && (
          <button
            type="button"
            disabled={busy || working}
            onClick={apply}
            className="g-press flex h-7 shrink-0 items-center gap-1.5 rounded-md bg-ink px-3 text-sm font-medium text-on-ink disabled:opacity-50"
          >
            {(busy || u.phase === "restarting") && <LoaderCircle size={14} className="animate-spin" />}
            {t("settings.update")}
          </button>
        )}
      </div>
      {u.phase === "downloading" && pct !== null && (
        <div className="h-1 overflow-hidden rounded-full bg-fill" aria-hidden>
          <div className="h-full rounded-full bg-ink-3 transition-[width] duration-300 ease-out" style={{ width: `${pct}%` }} />
        </div>
      )}
      {u.notes && <p className="m-0 max-h-40 overflow-y-auto text-sm whitespace-pre-wrap text-ink-2">{u.notes}</p>}
      <p className="m-0 text-xs text-ink-3 tabular-nums">
        {t("settings.current", { v: u.current })}
        {u.size ? ` · ${mb(u.size)}` : null}
        {u.phase === "failed" && u.error ? <span className="text-err"> · {t("settings.updateFailed", { e: u.error })}</span> : null}
      </p>
    </section>
  );
}

/** Push on: the worker, the browser's leave, a subscription with the daemon's key, sent to it. */
async function subscribe(key: string) {
  if (!("serviceWorker" in navigator) || !("PushManager" in window)) throw new Error(t("err.noPush"));
  if ((await Notification.requestPermission()) !== "granted") throw new Error(t("err.pushDenied"));
  const reg = await navigator.serviceWorker.register("/sw.js");
  await navigator.serviceWorker.ready;
  const raw = atob(key.replace(/-/g, "+").replace(/_/g, "/").padEnd(Math.ceil(key.length / 4) * 4, "="));
  const sub = (await reg.pushManager.getSubscription()) ?? (await reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: Uint8Array.from(raw, (c) => c.charCodeAt(0)) }));
  const j = sub.toJSON();
  await api.pushSubscribe({ endpoint: j.endpoint ?? sub.endpoint, keys: { p256dh: j.keys?.p256dh ?? "", auth: j.keys?.auth ?? "" } });
}
