// The settings (plan "运行", "推送", "更新", "网页", "交互终端"): what the config file holds that
// the page edits (GET/PUT /api/settings): reaping idle agents, going on after a crash or a quota,
// each agent's approval mode for new sessions, Web Push (off by default) per workspace, the quota
// bars shown; the update; the way to the 终端 page; and this device's own: reminders, appearance,
// language. Changed is saved at once.
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { BellRing, ChevronLeft, ChevronRight, CircleAlert, FoldVertical, LoaderCircle, MessageSquareDot, Monitor, Moon, MonitorSmartphone, RefreshCw, Sun } from "lucide-react";
import { useState, type ReactNode } from "react";
import { api, missing } from "./api/client";
import type { PushEvents, PushState, Settings, SettingsPatch } from "./api/types.gen";
import { useAgents, useMe, useSettings, useTerminals, useUpdate, useWorkspaces } from "./data";
import { t, useLang } from "./i18n";
import { LangSwitch, Segmented, type ThemePref } from "./me";
import { modeName } from "./modes";
import { quotaKey, windowName } from "./quota";
import { go } from "./route";
import { useConfirms, useQuota } from "./store";
import { errText, toast } from "./toast";
import { agentName, cn, IconKey, Logo, Switch, Tip } from "./ui";
import { remindBy, reminding } from "./unread";

const REAP = ["0", "15m", "30m", "1h", "2h", "4h", "12h"];
const RULES: { k: keyof PushEvents; icon: ReactNode; label: Parameters<typeof t>[0] }[] = [
  { k: "needsYou", icon: <BellRing size={14} />, label: "push.needsYou" },
  { k: "error", icon: <CircleAlert size={14} />, label: "push.error" },
  { k: "turnEnd", icon: <MessageSquareDot size={14} />, label: "push.turnEnd" },
  { k: "compact", icon: <FoldVertical size={14} />, label: "push.compact" },
];
const DEFAULT_RULES: PushEvents = { needsYou: true, error: true, turnEnd: false, compact: false };

/** A duration as Go writes it ("1h0m0s"), as short as it goes ("1h"). */
export const shortDuration = (d: string) => d.replace(/([hm])0s$/, "$1").replace(/h0m$/, "h");

export function SettingsPage({ phone, onBack, theme, setTheme }: { phone: boolean; onBack: () => void; theme: ThemePref; setTheme: (v: ThemePref) => void }) {
  const lang = useLang();
  const qc = useQueryClient();
  const q = useSettings();
  const s = q.data ?? undefined;
  const off = !s; // not come yet, or an older daemon
  const ws = useWorkspaces().data ?? [];
  const agents = (useAgents().data ?? []).filter((a) => a.modes.length > 1);
  const quota = useQuota();
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
  // Push's keys and each workspace's events are the daemon's own (GET /api/push).
  const pq = useQuery({ queryKey: ["push"], queryFn: () => api.push().catch((e) => (missing(e) ? null : Promise.reject(e))), staleTime: 30_000 });
  const push: PushState | null | undefined = pq.data;
  const rules = (id: number): PushEvents => {
    const w = push?.workspaces.find((x) => x.workspace === id);
    return w ? { needsYou: w.needsYou, error: w.error, turnEnd: w.turnEnd, compact: w.compact } : DEFAULT_RULES;
  };
  const setRule = async (id: number, k: keyof PushEvents, on: boolean) => {
    if (!push) return;
    const next = { ...rules(id), [k]: on };
    qc.setQueryData(["push"], { ...push, workspaces: [...push.workspaces.filter((x) => x.workspace !== id), { workspace: id, ...next }] });
    await api.pushWorkspace(id, next).catch((e) => (qc.invalidateQueries({ queryKey: ["push"] }), toast(t("err.settings"), { error: true, description: errText(e) })));
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
  const windows = (quota?.agents ?? []).flatMap((a) => a.windows.map((w) => ({ key: quotaKey(a, w), name: windowName(a.agent, w), account: a.account })));
  const reap = s ? shortDuration(s.idleReap) : "1h";

  return (
    <div className="relative h-full">
      <header className="glass absolute inset-x-0 top-0 z-20 flex h-12 items-center gap-1 pr-2 pl-2 shadow-[0_0.5px_0_var(--edge)] pointer-coarse:h-13">
        {phone && (
          <IconKey label={t("nav.back")} onClick={onBack} tipSide="bottom" className="-mr-1">
            <ChevronLeft size={20} />
          </IconKey>
        )}
        <h1 className={cn("m-0 text-base font-semibold", !phone && "pl-2")}>{t("nav.settings")}</h1>
        {q.isFetching && <LoaderCircle size={14} className="ml-1 animate-spin text-ink-4" />}
      </header>
      <div className="absolute inset-0 overflow-y-auto pt-16 pb-10">
        <div className="mx-auto flex max-w-[620px] flex-col gap-7 px-4 sm:px-6">
          <UpdateRow />

          <Group title={t("settings.agents")}>
            <Item label={t("settings.idleReap")} hint={t("settings.idleReapHint")}>
              <select disabled={off} value={reap} onChange={(e) => void put({ idleReap: e.target.value })} aria-label={t("settings.idleReap")} className="no-ring h-8 rounded-md bg-fill px-2 text-sm disabled:opacity-40">
                {[...new Set([...REAP, reap])].map((v) => (
                  <option key={v} value={v}>
                    {v === "0" ? t("settings.never") : v}
                  </option>
                ))}
              </select>
            </Item>
            <Item label={t("settings.crashResume")} hint={t("settings.crashResumeHint")}>
              <Switch label={t("settings.crashResume")} disabled={off} on={!!s?.crashResume} onChange={(v) => void put({ crashResume: v })} />
            </Item>
            <Item label={t("settings.quotaResume")} hint={t("settings.quotaResumeHint")}>
              <Switch label={t("settings.quotaResume")} disabled={off} on={!!s?.quotaResume} onChange={(v) => void put({ quotaResume: v })} />
            </Item>
          </Group>

          {agents.length > 0 && (
            <Group title={t("settings.modes")}>
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
                  <select disabled={off} value={s?.modes?.[a.agent] ?? ""} onChange={(e) => void put({ modes: { [a.agent]: e.target.value } })} aria-label={`${t("settings.modes")} · ${agentName(a.agent)}`} className="no-ring h-8 max-w-48 rounded-md bg-fill px-2 text-sm disabled:opacity-40">
                    <option value="">{t("settings.agentDefault", { mode: modeName({ id: a.defaultMode }) })}</option>
                    {a.modes.map((m) => (
                      <option key={m} value={m}>
                        {modeName({ id: m })}
                      </option>
                    ))}
                  </select>
                </Item>
              ))}
            </Group>
          )}

          {manages && (
            <Group title={t("settings.terminals")}>
              <button type="button" onClick={() => go({ name: "terminals" })} className="g-press -mx-2 flex min-h-12 items-center gap-3 rounded-lg px-2 text-left hover:bg-fill">
                <MonitorSmartphone size={17} className="shrink-0 text-ink-2" />
                <span className="min-w-0 flex-1">
                  <span className="block text-base">{t("settings.terminalsRow")}</span>
                  <span className="block truncate text-xs text-ink-3">{t("settings.terminalsHint")}</span>
                </span>
                {confirms.length > 0 && <span className="size-2 shrink-0 rounded-full bg-need" aria-label={t("nav.needsYou")} />}
                {terminals.length > 0 && <span className="text-xs text-ink-3 tabular-nums">{terminals.length}</span>}
                <ChevronRight size={15} className="shrink-0 text-ink-4" />
              </button>
            </Group>
          )}

          <Group title={t("settings.push")}>
            <Item label={t("settings.pushOn")} hint={t("settings.pushHint")}>
              {pushing ? <LoaderCircle size={16} className="animate-spin text-ink-3" /> : <Switch label={t("settings.pushOn")} disabled={!push || !("PushManager" in window)} on={pushed} onChange={(v) => void pushOn(v)} />}
            </Item>
            {pushed && ws.length > 0 && (
              <div className="g-fade flex flex-col">
                <div className="flex h-8 items-center justify-end gap-3 pr-0.5 text-ink-3">
                  {RULES.map((r) => (
                    <Tip key={r.k} label={t(r.label)}>
                      <span className="grid w-8 place-items-center" aria-label={t(r.label)}>
                        {r.icon}
                      </span>
                    </Tip>
                  ))}
                </div>
                {ws.map((w) => (
                  <div key={w.id} className="flex min-h-9 items-center gap-3">
                    <span className="min-w-0 flex-1 truncate text-base">{w.name}</span>
                    {RULES.map((r) => (
                      <span key={r.k} className="grid w-8 place-items-center">
                        <input type="checkbox" checked={rules(w.id)[r.k]} onChange={(e) => void setRule(w.id, r.k, e.target.checked)} aria-label={`${w.name} · ${t(r.label)}`} className="size-4 accent-[var(--ink)]" />
                      </span>
                    ))}
                  </div>
                ))}
              </div>
            )}
            <Item label={t("settings.remind")} hint={t("settings.remindHint")}>
              <Switch
                label={t("settings.remind")}
                disabled={typeof Notification === "undefined"}
                on={remind && typeof Notification !== "undefined" && Notification.permission === "granted"}
                onChange={(v) => void remindBy(v).then(() => setRemind(v))}
              />
            </Item>
          </Group>

          {windows.length > 0 && (
            <Group title={t("settings.quota")}>
              {windows.map((w) => (
                <Item key={w.key} label={w.name} hint={w.account}>
                  <Switch label={w.name} disabled={off} on={!s?.quotaHidden?.includes(w.key)} onChange={(v) => s && void put({ quotaHidden: v ? s.quotaHidden.filter((k) => k !== w.key) : [...(s.quotaHidden ?? []), w.key] })} />
                </Item>
              ))}
            </Group>
          )}

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
        </div>
      </div>
    </div>
  );
}

export function Group({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="flex flex-col">
      <h2 className="m-0 mb-1 text-xs font-medium text-ink-3">{title}</h2>
      {children}
    </section>
  );
}

export function Item({ label, hint, children }: { label: ReactNode; hint?: ReactNode; children: ReactNode }) {
  return (
    <div className="flex min-h-11 items-center gap-4 py-1">
      <div className="min-w-0 flex-1">
        <div className="text-base">{label}</div>
        {hint && <div className="text-xs text-ink-3">{hint}</div>}
      </div>
      <div className="flex shrink-0 items-center">{children}</div>
    </div>
  );
}

/** The update (the carried "update"): the version now and a key to look again; once a newer one is
 *  out, a small notice with its version, its notes and the 更新 key, then how installing goes. */
function UpdateRow() {
  const lang = useLang();
  const u = useUpdate().data;
  const [busy, setBusy] = useState<"check" | "apply" | null>(null);
  if (!u) return null;
  const run = (k: "check" | "apply", f: () => Promise<unknown>, done?: string) => {
    setBusy(k);
    void f().then(
      () => (setBusy(null), done && toast(done)),
      (e) => (setBusy(null), toast(t("err.update"), { error: true, description: errText(e) })),
    );
  };
  const working = u.phase === "checking" || u.phase === "downloading" || u.phase === "restarting";
  const phase = u.phase === "downloading" ? t("settings.downloading") : u.phase === "restarting" ? t("settings.restarting") : u.phase === "checking" ? t("settings.checking") : "";
  const checked = u.checkedAt ? new Date(u.checkedAt).toLocaleString(lang === "zh" ? "zh-CN" : "en", { month: "numeric", day: "numeric", hour: "2-digit", minute: "2-digit" }) : "";
  if (!u.available)
    return (
      <div className="-mb-3 flex items-center gap-2 text-xs text-ink-3 tabular-nums">
        <span>Iridium {u.current}</span>
        {u.source && (
          <>
            <span aria-hidden>·</span>
            <span>{working ? phase : u.phase === "failed" ? t("settings.updateFailed", { e: u.error ?? "" }) : checked ? t("settings.checked", { time: checked }) : t("settings.upToDate")}</span>
            <IconKey label={t("settings.check")} disabled={busy !== null || working} onClick={() => run("check", api.checkUpdate)} className="size-6">
              <RefreshCw size={12} className={busy === "check" || u.phase === "checking" ? "animate-spin" : undefined} />
            </IconKey>
          </>
        )}
      </div>
    );
  return (
    <section className="g-pop flex flex-col gap-2 rounded-lg p-3 shadow-[0_0_0_1px_var(--edge)]" aria-label={t("settings.updateReady")}>
      <div className="flex items-center gap-3">
        <span className="size-2 shrink-0 rounded-full bg-err" aria-hidden />
        <span className="min-w-0 flex-1 text-base font-semibold tabular-nums">{t("settings.updateTo", { v: u.latest ?? "" })}</span>
        {working && <span className="text-xs text-ink-3">{phase}</span>}
        <button
          type="button"
          disabled={busy !== null || working}
          onClick={() => run("apply", () => api.applyUpdate(), t("settings.updating"))}
          className="g-press flex h-8 items-center gap-1.5 rounded-md bg-ink px-3.5 text-sm font-medium text-on-ink disabled:opacity-50"
        >
          {(busy === "apply" || working) && <LoaderCircle size={14} className="animate-spin" />}
          {t("settings.update")}
        </button>
      </div>
      {u.notes && <p className="m-0 max-h-40 overflow-y-auto text-sm whitespace-pre-wrap text-ink-2">{u.notes}</p>}
      <p className="m-0 text-xs text-ink-3 tabular-nums">
        {t("settings.current", { v: u.current })}
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
