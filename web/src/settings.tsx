// The settings (plan "运行", "推送", "更新", "网页"): what the config file holds that the page edits:
// reaping idle agents, going on after a crash or a quota, Web Push per workspace (off by default),
// the quota bars shown; the update; and this device's own: reminders, appearance, language. Changed
// is saved at once. A daemon that does not serve /api/settings yet leaves its part disabled.
import { useQueryClient } from "@tanstack/react-query";
import { BellRing, ChevronLeft, CircleAlert, FoldVertical, LoaderCircle, MessageSquareDot, Monitor, Moon, Sun } from "lucide-react";
import { useState, type ReactNode } from "react";
import type { PushRules, Settings } from "./api/assumed";
import { api } from "./api/client";
import { useSettings, useUpdate, useWorkspaces } from "./data";
import { t, useLang } from "./i18n";
import { LangSwitch, Segmented, type ThemePref } from "./me";
import { quotaKey, windowName } from "./quota";
import { useQuota } from "./store";
import { errText, toast } from "./toast";
import { cn, IconKey, Switch, Tip } from "./ui";
import { remindBy, reminding } from "./unread";

const REAP = ["0", "15m", "30m", "1h", "2h", "4h", "12h"];
const RULES: { k: keyof PushRules; icon: ReactNode; label: Parameters<typeof t>[0] }[] = [
  { k: "needsYou", icon: <BellRing size={14} />, label: "push.needsYou" },
  { k: "error", icon: <CircleAlert size={14} />, label: "push.error" },
  { k: "turnEnd", icon: <MessageSquareDot size={14} />, label: "push.turnEnd" },
  { k: "compact", icon: <FoldVertical size={14} />, label: "push.compact" },
];
const DEFAULT_RULES: PushRules = { needsYou: true, error: true, turnEnd: false, compact: false };

export function SettingsPage({ phone, onBack, theme, setTheme }: { phone: boolean; onBack: () => void; theme: ThemePref; setTheme: (v: ThemePref) => void }) {
  const lang = useLang();
  const qc = useQueryClient();
  const q = useSettings();
  const s = q.data ?? undefined;
  const off = !s; // not served yet, or not come
  const ws = useWorkspaces().data ?? [];
  const quota = useQuota();
  const put = async (p: Partial<Settings>) => {
    if (!s) return;
    qc.setQueryData(["settings"], { ...s, ...p });
    try {
      qc.setQueryData(["settings"], await api.putSettings(p));
    } catch (e) {
      qc.setQueryData(["settings"], s);
      toast(t("err.settings"), { error: true, description: errText(e) });
    }
  };
  const rules = (id: number) => ({ ...DEFAULT_RULES, ...s?.push.workspaces[String(id)] });
  const setRule = (id: number, k: keyof PushRules, on: boolean) => s && void put({ push: { ...s.push, workspaces: { ...s.push.workspaces, [String(id)]: { ...rules(id), [k]: on } } } });
  const [remind, setRemind] = useState(reminding);
  const [pushing, setPushing] = useState(false);
  const pushOn = async (on: boolean) => {
    if (!s) return;
    setPushing(true);
    try {
      if (on) await subscribe();
      await put({ push: { ...s.push, enabled: on } });
    } catch (e) {
      toast(t("err.push"), { error: true, description: errText(e) });
    } finally {
      setPushing(false);
    }
  };
  const windows = (quota?.agents ?? []).flatMap((a) => a.windows.map((w) => ({ key: quotaKey(a, w), name: windowName(a.agent, w), account: a.account })));

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
          {off && !q.isPending && <p className="m-0 text-sm text-ink-3">{t("settings.notServed")}</p>}

          <Group title={t("settings.agents")}>
            <Item label={t("settings.idleReap")} hint={t("settings.idleReapHint")}>
              <select disabled={off} value={s?.idleReap ?? "1h"} onChange={(e) => void put({ idleReap: e.target.value })} aria-label={t("settings.idleReap")} className="no-ring h-8 rounded-md bg-fill px-2 text-sm disabled:opacity-40">
                {[...new Set([...REAP, s?.idleReap ?? "1h"])].map((v) => (
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

          <Group title={t("settings.push")}>
            <Item label={t("settings.pushOn")} hint={t("settings.pushHint")}>
              {pushing ? <LoaderCircle size={16} className="animate-spin text-ink-3" /> : <Switch label={t("settings.pushOn")} disabled={off || !("PushManager" in window)} on={!!s?.push.enabled} onChange={(v) => void pushOn(v)} />}
            </Item>
            {s?.push.enabled && ws.length > 0 && (
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
                        <input type="checkbox" checked={rules(w.id)[r.k]} onChange={(e) => setRule(w.id, r.k, e.target.checked)} aria-label={`${w.name} · ${t(r.label)}`} className="size-4 accent-[var(--ink)]" />
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
                  <Switch label={w.name} disabled={off} on={!s?.quotaHidden.includes(w.key)} onChange={(v) => s && void put({ quotaHidden: v ? s.quotaHidden.filter((k) => k !== w.key) : [...s.quotaHidden, w.key] })} />
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

function Group({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="flex flex-col">
      <h2 className="m-0 mb-1 text-xs font-medium text-ink-3">{title}</h2>
      {children}
    </section>
  );
}

function Item({ label, hint, children }: { label: string; hint?: string; children: ReactNode }) {
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

/** The update, when the daemon says there is one: its version, its notes, the key. */
function UpdateRow() {
  const u = useUpdate().data;
  const [busy, setBusy] = useState(false);
  if (!u) return null;
  if (!u.available) return <p className="m-0 text-xs text-ink-3 tabular-nums">Iridium {u.current}</p>;
  return (
    <section className="g-pop flex flex-col gap-2 rounded-lg p-3 shadow-[0_0_0_1px_var(--edge)]">
      <div className="flex items-center gap-3">
        <span className="size-2 shrink-0 rounded-full bg-err" aria-hidden />
        <span className="min-w-0 flex-1 text-base font-semibold tabular-nums">{t("settings.updateTo", { v: u.latest ?? "" })}</span>
        <button
          type="button"
          disabled={busy}
          onClick={() => (setBusy(true), void api.applyUpdate().then(() => toast(t("settings.updating")), (e) => (setBusy(false), toast(t("err.update"), { error: true, description: errText(e) }))))}
          className="g-press flex h-8 items-center gap-1.5 rounded-md bg-ink px-3.5 text-sm font-medium text-on-ink disabled:opacity-50"
        >
          {busy && <LoaderCircle size={14} className="animate-spin" />}
          {t("settings.update")}
        </button>
      </div>
      {u.notes && <p className="m-0 max-h-40 overflow-y-auto text-sm whitespace-pre-wrap text-ink-2">{u.notes}</p>}
      <p className="m-0 text-xs text-ink-3 tabular-nums">{t("settings.current", { v: u.current })}</p>
    </section>
  );
}

/** Push on: the worker, the browser's leave, a subscription with the daemon's key, sent to it. */
async function subscribe() {
  if (!("serviceWorker" in navigator) || !("PushManager" in window)) throw new Error(t("err.noPush"));
  if ((await Notification.requestPermission()) !== "granted") throw new Error(t("err.pushDenied"));
  const reg = await navigator.serviceWorker.register("/sw.js");
  await navigator.serviceWorker.ready;
  const { key } = await api.pushKey();
  const raw = atob(key.replace(/-/g, "+").replace(/_/g, "/").padEnd(Math.ceil(key.length / 4) * 4, "="));
  const sub = (await reg.pushManager.getSubscription()) ?? (await reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: Uint8Array.from(raw, (c) => c.charCodeAt(0)) }));
  await api.pushSubscribe(sub.toJSON());
}
