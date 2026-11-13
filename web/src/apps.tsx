// The settings' 应用管理 (draft 四): two lists (k_glossary "电脑"). 应用: 网页 (ir_web) and the apps
// that speak, each with its avatar, name, whether it is online, when it was last active.
// 电脑: the host and the computers that joined, with their system and agents; apps on one machine
// are one computer's connections, listed in its page, not apart. A row opens the page: an app's
// permissions (agents, scope), the agents it carries and whether command lines run there, its
// logins (网页's: the browsers, and the unused invites as slots), rename, avatar, removal; the
// host's, its system, agents and connections only. 配对 is a switch, on till turned off; requests
// wait as cards on top, the code checked against the one the app shows. No TOKEN is handed out here.
import { Dialog } from "@base-ui/react/dialog";
import { useQueryClient, type QueryClient } from "@tanstack/react-query";
import { Check, ChevronLeft, ChevronRight, Link2, LoaderCircle, PencilLine, Plus, SlidersHorizontal, SquareTerminal, Trash2, X } from "lucide-react";
import { useEffect, useLayoutEffect, useRef, useState, type CSSProperties, type ReactNode } from "react";
import { ApiError, api } from "./api/client";
import type { Agent, App, Apps, Computer, Login, Perms, PendingInvite, Rules } from "./api/types.gen";
import { CardFrame, CardHead, MainKey, SecondKey } from "./card";
import { ConfirmCard, saysOwner } from "./confirms";
import { useApps, useMe } from "./data";
import { t, useLang } from "./i18n";
import { LocalMark } from "./me";
import { Crop } from "./profile";
import { SecretHere } from "./secret";
import { showSecret, useSecret } from "./secrets";
import { Group } from "./settings";
import { useConfirms, usePairing } from "./store";
import { errText, toast } from "./toast";
import { ago, Avatar, AvatarKey, cn, IconKey, Logo, Switch, Tip, tokens } from "./ui";

/** Claude: it was made the owner's own. Set then, never after (the user, 2026-11-08). */
export const hasClaude = (r: Rules) => !!r.claudeOwner;

const systems: Record<string, string> = {
  windows: "Windows",
  darwin: "macOS",
  linux: "Linux",
};

/** A computer (k_glossary "电脑"): the host, or an app that paired as one or has said its system. */
const isComputer = (a: App) => !!a.host || !!a.computer || a.type === "computer";

/** A computer's other connections: the apps on the same machine (Computer.under). */
const connectionsOf = (apps: App[], id: string) => apps.filter((a) => a.computer?.under === id);

/** Whether command lines run at a machine: none, a person's on the page, or agents' too (ir sh). */
const shellOf = (m: Computer) => (m.shell ? "agents" : "none");

/** A machine's line: its system and agents, e.g. "Linux · claude 2.1 · codex". */
const machineLine = (m: Computer) => [systems[m.os] ?? m.os, ...m.agents.filter((a) => !a.off).map((a) => [a.agent, a.version].filter(Boolean).join(" "))].filter(Boolean).join(" · ");

const clock = (ms: number) =>
  new Date(ms).toLocaleTimeString(document.documentElement.lang || undefined, {
    hour: "2-digit",
    minute: "2-digit",
  });

const nameError = (e: unknown) => {
  const code = e instanceof ApiError ? (e.body as { nameError?: string } | undefined)?.nameError : undefined;
  return code === "taken" ? t("me.nameTaken") : code ? t("me.nameBad") : errText(e);
};

/** A login of ir_web (its logins come with app "" from before 0.2's other apps, or "ir_web"). */
const ofWeb = (l: Login) => !l.app || l.app === "ir_web";

type View = { kind: "list" } | { kind: "app"; id: string };

/** side: in the settings' dialog (a computer), an app's page opens in a panel of its own beside it,
 *  the list staying (the user, 2026-11-12); on a phone, or with no room beside, it slides in. */
export function AppsPage({ onBack, side }: { onBack: () => void; side?: boolean }) {
  useLang();
  const q = useApps();
  const [view, setView] = useState<View>({ kind: "list" });
  const pairing = !!usePairing()?.open;
  const apps = q.data?.apps ?? [];
  const open = view.kind === "app" ? apps.find((a) => a.id === view.id) : undefined;
  const at = useBeside(!!side && !!open);
  const list = () => setView({ kind: "list" });
  const go = (id: string) => setView({ kind: "app", id });
  const row = (a: App) => <AppRow key={a.id} a={a} machine={a.computer} conns={connectionsOf(apps, a.id).length} on={!!at && a.id === open?.id} onOpen={() => go(a.id)} />;
  const computers = apps.filter((a) => isComputer(a) && !a.computer?.under);
  if (open && q.data && !at)
    return (
      <div key={open.id} className="g-slide h-full">
        <AppPage app={open} data={q.data} machine={open.computer} onBack={list} go={go} />
      </div>
    );
  return (
    <Page title={t("settings.terminals")} onBack={onBack} backLabel={t("nav.me")} busy={q.isFetching} right={<PairingKey open={pairing} />}>
      {pairing && <p className="m-0 px-1 text-xs text-ink-3">{t("apps.pairHint")}</p>}
      <Needs />
      {q.isError && <p className="m-0 text-sm text-ink-3">{errText(q.error)}</p>}
      {q.data && (
        <>
          {apps.some((a) => !isComputer(a)) && <Group title={t("term.channel")}>{apps.filter((a) => !isComputer(a)).map(row)}</Group>}
          <Group title={t("apps.computer")}>{computers.map(row)}</Group>
        </>
      )}
      {/* Nested in the settings' dialog: a press in it does not put the settings away; one outside
          it (another row too) puts it away. */}
      <Dialog.Root open={!!(open && at)} modal={false} onOpenChange={(o) => !o && list()}>
        <Dialog.Portal>
          <Dialog.Popup aria-label={open?.web ? t("term.panel") : open?.name} style={at} className="g-beside glass thick float fixed z-50 overflow-hidden rounded-lg text-ink outline-none">
            {open && q.data && <AppPage key={open.id} app={open} data={q.data} machine={open.computer} onBack={list} go={go} closes />}
          </Dialog.Popup>
        </Dialog.Portal>
      </Dialog.Root>
    </Page>
  );
}

/** Where an app's panel goes: right of the settings' dialog, its top and height; none when there
 *  is no room there (then the page slides in within the dialog). */
function useBeside(on: boolean): CSSProperties | undefined {
  const [at, setAt] = useState<CSSProperties>();
  useLayoutEffect(() => {
    if (!on) return setAt(undefined);
    const place = () => {
      const r = document.querySelector("[data-settings-dialog]")?.getBoundingClientRect();
      const left = r ? r.right + 8 : 0;
      const w = Math.min(340, innerWidth - left - 8);
      setAt(r && w >= 280 ? { left, top: r.top, width: w, height: r.height } : undefined);
    };
    place();
    addEventListener("resize", place);
    return () => removeEventListener("resize", place);
  }, [on]);
  return at;
}

/** A page of 应用管理: its header (back, title, its key) and its scrolling body. */
function Page({ title, onBack, backLabel, busy, right, closes, children }: { title: string; onBack: () => void; backLabel: string; busy?: boolean; right?: ReactNode; closes?: boolean; children: ReactNode }) {
  return (
    <div className="flex h-full flex-col">
      {/* closes: a panel of its own, beside the list: shut by an ✕ at its right, not a back key. */}
      <header className={cn("flex h-10.5 shrink-0 items-center gap-1 px-1.5 pt-1.5", closes && "pl-3")}>
        {!closes && (
          <IconKey label={backLabel} onClick={onBack} tipSide="bottom" className="size-7">
            <ChevronLeft size={16} />
          </IconKey>
        )}
        <h1 className="m-0 min-w-0 flex-1 truncate text-base font-semibold">{title}</h1>
        {busy && <LoaderCircle size={14} className="animate-spin text-ink-4" />}
        {right}
        {closes && (
          <IconKey label={t("nav.close")} onClick={onBack} tipSide="bottom" className="size-7">
            <X size={15} />
          </IconKey>
        )}
      </header>
      <div className="no-bar min-h-0 flex-1 overflow-y-auto pt-2 pb-4">
        <div className="flex flex-col gap-5 px-3">{children}</div>
      </div>
    </div>
  );
}

/** 配对: a switch, on till turned off (kept across restarts). Requests wait as cards above, the
 *  code checked against the one the app shows. */
function PairingKey({ open }: { open: boolean }) {
  const [busy, setBusy] = useState(false);
  const set = async (on: boolean) => {
    setBusy(true);
    try {
      await api.pairing(on);
    } catch (e) {
      toast(t("apps.pairing"), { error: true, description: errText(e) });
    } finally {
      setBusy(false);
    }
  };
  return (
    <label className="flex shrink-0 items-center gap-2 text-xs text-ink-2">
      {t("apps.pairing")}
      <Switch label={t("apps.pairing")} on={open} disabled={busy} onChange={(v) => void set(v)} />
    </label>
  );
}

/** What waits for a person here: the needs and a secret allowed here. */
function Needs() {
  const needs = useConfirms();
  const secretHere = useSecret()?.at === "terminals";
  if (needs.length === 0 && !secretHere) return null;
  return (
    <div className="flex flex-col gap-2">
      <SecretHere at="terminals" />
      {needs.map((c) => (
        <ConfirmCard key={c.id} c={c} at="terminals" />
      ))}
    </div>
  );
}

/** One app or computer in the list; it opens its page. conns: a computer's other connections. */
function AppRow({ a, machine, conns, on, onOpen }: { a: App; machine?: Computer; conns?: number; on?: boolean; onOpen?: () => void }) {
  const me = useMe().data;
  const used = a.online ? t("term.online") : a.host ? t("term.localShort") : ago(a.lastSeenAt || a.createdAt || 0);
  const body = (
    <>
      {a.host ? <LocalMark size={32} /> : <Avatar src={a.avatar} name={a.web ? t("term.panel") : a.name} size={32} type={a.type} />}
      <span className="min-w-0 flex-1">
        <span className="flex items-center gap-2">
          <span className="truncate text-base font-medium">{a.host ? t("me.local") : a.web ? t("term.panel") : a.name}</span>
          {a.id === me?.app && !a.host && !a.web && <span className="shrink-0 text-xs text-ink-3">{t("term.this")}</span>}
        </span>
        {/* One line, cut at its end, never wrapped into columns (the panel is narrow). */}
        <span className="flex min-w-0 items-center gap-1.5 text-xs text-ink-3 tabular-nums">
          {a.online ? <span className="size-1.5 shrink-0 rounded-full bg-run" aria-hidden /> : null}
          <span className="min-w-0 truncate">
            {used}
            {conns ? <> · {t("apps.connCount", { n: conns + 1 })}</> : null} · {t("term.today", { n: tokens(a.usedToday) })}
            {a.web && a.logins ? <> · {t("apps.loginCount", { n: a.logins })}</> : null}
            {machine && <> · {machineLine(machine)}</>}
          </span>
        </span>
      </span>
      {hasClaude(a.rules) && !a.web && (
        <Tip label={t("term.claudeYes")}>
          <span className="flex shrink-0">
            <Logo agent="claude" size={13} />
          </span>
        </Tip>
      )}
      {onOpen && <ChevronRight size={15} className="shrink-0 text-ink-4" />}
    </>
  );
  const cls = "flex min-h-14 w-full items-center gap-3 rounded-lg px-2 text-left";
  return onOpen ? (
    <button type="button" onClick={onOpen} aria-pressed={on} className={cn(cls, "g-press hover:bg-fill", on && "bg-fill")}>
      {body}
    </button>
  ) : (
    <div className={cls}>{body}</div>
  );
}

/** An app's page: what it is, its permissions, what it carries, its logins, removal; a computer's
 *  also its other connections. The host's: its computer and connections only. */
function AppPage({ app, data, machine, onBack, go, closes }: { app: App; data: Apps; machine?: Computer; onBack: () => void; go: (id: string) => void; closes?: boolean }) {
  const qc = useQueryClient();
  const [renaming, setRenaming] = useState(false);
  const [sure, setSure] = useState(false);
  const [busy, setBusy] = useState(false);
  const [picture, setPicture] = useState<string | null>(null);
  const file = useRef<HTMLInputElement>(null);
  const logins = data.logins.filter((l) => (app.web ? ofWeb(l) : l.app === app.id));
  const pick = (f: File | undefined) => {
    if (f) setPicture((old) => (old && URL.revokeObjectURL(old), URL.createObjectURL(f)));
  };
  const dropPicture = () => setPicture((old) => (old && URL.revokeObjectURL(old), null));
  const saveAvatar = async (blob: Blob) => {
    try {
      await api.profile({ avatar: blob }, app.id);
      dropPicture();
      void qc.invalidateQueries({ queryKey: ["apps"] });
    } catch (e) {
      toast(t("term.avatar"), { error: true, description: errText(e) });
    }
  };
  const title = app.web ? t("term.panel") : app.host ? t("me.local") : app.name;
  const conns = connectionsOf(data.apps, app.id);
  const under = machine?.under ? data.apps.find((a) => a.id === machine.under) : undefined;
  const computer = machine && (
    <Group title={t("apps.computer")}>
      {under && <Line label={t("apps.on")}>{under.host ? t("me.local") : under.name}</Line>}
      <Line label={t("confirm.system")}>{[systems[machine.os] ?? machine.os, machine.arch].filter(Boolean).join(" ") || "—"}</Line>
    </Group>
  );
  // What runs there, one row each: its agents (switched here, but the host's), then command lines
  // (the computer's own setting, shown, not switched here).
  const runs = machine && (
    <Group title={t("apps.runs")}>
      <CarriedAgents app={app.id} agents={machine.agents} fixed={app.host} />
      <div className="flex min-h-9 items-center gap-3 px-2 text-sm">
        <SquareTerminal size={14} className="shrink-0 text-ink-2" />
        <span className="min-w-0 flex-1">{t("confirm.commands")}</span>
        <span className="text-ink-3">{t(`confirm.commands.${shellOf(machine)}`)}</span>
      </div>
    </Group>
  );
  const connections = conns.length > 0 && (
    <Group title={t("apps.conns")}>
      {conns.map((a) => (
        <AppRow key={a.id} a={a} machine={a.computer} onOpen={() => go(a.id)} />
      ))}
    </Group>
  );
  return (
    <Page title={title} onBack={onBack} backLabel={t("settings.terminals")} closes={closes}>
      <section className="flex flex-col gap-2">
        <div className="flex items-center gap-3 px-2">
          {app.host ? (
            <LocalMark size={40} />
          ) : app.web ? (
            <Avatar src={app.avatar} name={title} size={40} />
          ) : (
            <AvatarKey label={t("term.avatar")} onClick={() => file.current?.click()} src={app.avatar} name={app.name} size={40} type={app.type} />
          )}
          <span className="min-w-0 flex-1">
            <span className="flex items-center gap-2">
              {renaming ? <Rename id={app.id} name={app.name} done={() => setRenaming(false)} /> : <span className="truncate text-base font-medium">{title}</span>}
              {!app.web && !app.host && !renaming && (
                <IconKey label={t("term.renameIt")} onClick={() => setRenaming(true)} className="size-6">
                  <PencilLine size={13} />
                </IconKey>
              )}
            </span>
            <span className="flex items-center gap-1.5 text-xs text-ink-3 tabular-nums">
              {app.online ? <span className="size-1.5 shrink-0 rounded-full bg-run" aria-hidden /> : null}
              {app.online ? t("term.online") : app.host ? t("term.localShort") : ago(app.lastSeenAt || app.createdAt || 0)} · {t("term.today", { n: tokens(app.usedToday) })}
            </span>
          </span>
          {hasClaude(app.rules) && !app.web && (
            <Tip label={t("term.claudeYes")}>
              <span className="flex shrink-0">
                <Logo agent="claude" size={14} />
              </span>
            </Tip>
          )}
        </div>
        {picture && (
          <div className="g-fade flex justify-center">
            <Crop src={picture} onCancel={dropPicture} onDone={(b) => void saveAvatar(b)} />
          </div>
        )}
        <input ref={file} type="file" accept="image/*" hidden onChange={(e) => (pick(e.target.files?.[0]), (e.target.value = ""))} />
      </section>
      {app.web ? (
        <WebLogins logins={logins} invites={data.invites} />
      ) : app.host ? (
        <>
          {computer}
          {runs}
          {connections}
        </>
      ) : (
        <>
          <PermsEditor perms={app.perms} save={(p) => api.appPerms(app.id, p)} />
          {computer}
          {runs}
          {connections}
          {logins.length > 0 && (
            <Group title={t("apps.logins")}>
              {logins.map((l) => (
                <LoginRow key={l.id} l={l} app={app.id} remove={() => void removeLogin(qc, app.id, l)} />
              ))}
            </Group>
          )}
          <section className="flex flex-col gap-2">
            {sure ? (
              <CardFrame label={t("term.revokeTitle", { name: app.name })}>
                <CardHead
                  icon={<Trash2 size={16} />}
                  title={t("term.revokeTitle", { name: app.name })}
                  summary={t("term.revokeHint")}
                  keys={
                    <>
                      <SecondKey onClick={() => setSure(false)}>{t("me.cancel")}</SecondKey>
                      <MainKey busy={busy} onClick={() => (setBusy(true), void revoke(qc, app.id, app.name).then((ok) => (setBusy(false), ok && onBack())))}>
                        {t("term.revoke")}
                      </MainKey>
                    </>
                  }
                />
              </CardFrame>
            ) : (
              <button type="button" onClick={() => setSure(true)} className="g-press flex h-9 items-center gap-2 self-start rounded-md px-2 text-sm text-err hover:bg-fill">
                <Trash2 size={15} />
                {t(machine?.under ? "apps.deleteConn" : isComputer(app) ? "apps.deleteComputer" : "apps.delete")}
              </button>
            )}
          </section>
        </>
      )}
    </Page>
  );
}

/** The agents an app carries, each switched on or off there: one off is not started there nor
 *  offered for new sessions. */
function CarriedAgents({ app, agents, fixed }: { app: string; agents: Computer["agents"]; fixed?: boolean }) {
  const qc = useQueryClient();
  const [busy, setBusy] = useState<string | null>(null);
  const set = async (agent: string, on: boolean) => {
    setBusy(agent);
    try {
      const off = agents.filter((a) => (a.agent === agent ? !on : a.off)).map((a) => a.agent);
      await api.appAgents(app, off);
      await qc.invalidateQueries({ queryKey: ["apps"] });
    } catch (e) {
      toast(t("apps.carried"), { error: true, description: errText(e) });
    } finally {
      setBusy(null);
    }
  };
  return (
    <>
      {agents.map((a) => (
        <div key={a.agent} className="flex min-h-9 items-center gap-3 px-2 text-sm">
          <Logo agent={a.agent as Agent} size={14} />
          <span className={`min-w-0 flex-1 truncate ${a.off ? "text-ink-3" : ""}`}>
            {a.agent}
            {a.version && <span className="ml-1.5 text-xs text-ink-3 tabular-nums">{a.version}</span>}
          </span>
          {busy === a.agent && <LoaderCircle size={13} className="animate-spin text-ink-4" />}
          {!fixed && <Switch label={a.agent} on={!a.off} disabled={!!busy} onChange={(on) => void set(a.agent, on)} />}
        </div>
      ))}
    </>
  );
}

/** A label and its value, a row of a group. */
function Line({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex min-h-9 items-center gap-3 px-2 text-sm">
      <span className="w-20 shrink-0 text-ink-3">{label}</span>
      <span className="min-w-0 flex-1 break-words">{children}</span>
    </div>
  );
}

/** Removes app, ir_web's login or invite id; false if it failed. */
async function revoke(qc: QueryClient, id: string, name: string) {
  let ok = true;
  try {
    await api.revoke(id);
    toast(t("term.revoked", { name }));
  } catch (e) {
    ok = false;
    toast(t("err.revoke"), { error: true, description: errText(e) });
  }
  await qc.invalidateQueries({ queryKey: ["apps"] });
  return ok;
}

async function removeLogin(qc: QueryClient, app: string, l: Login) {
  try {
    await api.removeLogin(app, l.id);
    toast(t("apps.loginRemoved", { name: l.name }));
  } catch (e) {
    toast(t("err.revoke"), { error: true, description: errText(e) });
  }
  await qc.invalidateQueries({ queryKey: ["apps"] });
}

/** A login: its avatar, name, when it was last active, waiting or not; renamed (ir_web's) or
 *  removed here. */
function LoginRow({ l, app, remove, rename }: { l: Login; app: string; remove: () => void; rename?: boolean }) {
  const me = useMe().data;
  const [renaming, setRenaming] = useState(false);
  const [perms, setPerms] = useState(false);
  const narrowed = !!l.perms && !(l.perms.agents.includes("*") && l.perms.scope.includes("*"));
  return (
    <div className="flex flex-col">
      <div className="flex min-h-12 items-center gap-3 px-2">
        <Avatar src={l.avatar} name={l.name} size={28} />
        <span className="min-w-0 flex-1">
          <span className="flex items-center gap-2">
            {renaming ? <Rename id={l.id} name={l.name} done={() => setRenaming(false)} /> : <span className="truncate text-base">{l.name}</span>}
            {l.id === me?.login && <span className="shrink-0 text-xs text-ink-3">{t("term.this")}</span>}
            {!l.approvedAt && <span className="shrink-0 rounded-sm bg-fill-2 px-1.5 text-[11px] leading-[18px] text-ink-2">{t("apps.pending")}</span>}
          </span>
          <span className="flex items-center gap-1.5 text-xs text-ink-3 tabular-nums">
            {l.online ? <span className="size-1.5 shrink-0 rounded-full bg-run" aria-hidden /> : null}
            {l.online ? t("term.online") : ago(l.lastSeenAt || l.createdAt || 0)} · {t("term.today", { n: tokens(l.usedToday) })}
          </span>
        </span>
        {hasClaude(l.rules) && (
          <Tip label={t("term.claudeYes")}>
            <span className="flex shrink-0">
              <Logo agent="claude" size={13} />
            </span>
          </Tip>
        )}
        {rename && !renaming && (
          <IconKey label={t("term.renameIt")} onClick={() => setRenaming(true)} className="size-7">
            <PencilLine size={14} />
          </IconKey>
        )}
        <IconKey label={t("apps.perms")} onClick={() => setPerms(!perms)} className={`size-7 ${narrowed ? "text-ink" : ""}`}>
          <SlidersHorizontal size={14} />
        </IconKey>
        <IconKey label={t("apps.removeLogin")} onClick={remove} className="size-7">
          <X size={15} />
        </IconKey>
      </div>
      {perms && (
        <div className="g-fade mb-2 ml-11 rounded-lg bg-fill/60 py-2">
          <PermsEditor title={t("apps.loginPerms")} perms={l.perms} save={(p) => api.loginPerms(app, l.id, p)} />
        </div>
      )}
    </div>
  );
}

/** 网页's logins (the browsers' passkeys), its unused invites as slots, and a new invite. */
function WebLogins({ logins, invites }: { logins: Login[]; invites: PendingInvite[] }) {
  const qc = useQueryClient();
  const me = useMe().data;
  const [making, setMaking] = useState(false);
  return (
    <Group title={t("apps.logins")}>
      {logins.map((l) => (
        <LoginRow key={l.id} l={l} app="ir_web" rename remove={() => void revoke(qc, l.id, l.name)} />
      ))}
      {invites.map((i) => (
        <div key={i.id} className="flex min-h-10 items-center gap-3 px-2">
          <Link2 size={15} className="mx-1.5 shrink-0 text-ink-3" />
          <span className="min-w-0 flex-1 truncate text-base">{i.setup ? t("term.setup") : i.name || <span className="text-ink-3">{t("term.unnamed")}</span>}</span>
          {i.claude && <span className="shrink-0 text-xs text-ink-3">{t("term.withClaude")}</span>}
          {i.expiresAt ? <span className="text-xs text-ink-3 tabular-nums">{t("term.until", { time: clock(i.expiresAt) })}</span> : null}
          <IconKey label={t("term.revoke")} onClick={() => void revoke(qc, i.id, i.name || t("term.unnamed"))} className="size-7">
            <X size={15} />
          </IconKey>
        </div>
      ))}
      <SecretHere at="terminals" />
      {making ? (
        <InviteForm done={() => setMaking(false)} />
      ) : (
        <button
          type="button"
          disabled={!me?.public}
          onClick={() => setMaking(true)}
          className="g-press flex min-h-10 items-center gap-3 rounded-md px-2 text-left text-sm hover:bg-fill disabled:opacity-45 disabled:hover:bg-transparent"
        >
          <Plus size={15} className="mx-1.5 shrink-0 text-ink-2" />
          <span className="min-w-0 flex-1">
            <span className="block">{t("term.invite")}</span>
            <span className="block text-xs text-ink-3">{me?.public ? t("term.inviteHint") : t("term.needPublic")}</span>
          </span>
        </button>
      )}
    </Group>
  );
}

/** A new login slot of 网页: named; the link then shows once, here. */
function InviteForm({ done }: { done: () => void }) {
  const qc = useQueryClient();
  const me = useMe().data;
  const [name, setName] = useState("");
  const [withClaude, setWithClaude] = useState(false);
  const [busy, setBusy] = useState(false);
  const canClaude = saysOwner(me);
  const make = async () => {
    setBusy(true);
    try {
      showSecret({
        invite: await api.invite(name.trim(), canClaude && withClaude),
        at: "terminals",
      });
      done();
      void qc.invalidateQueries({ queryKey: ["apps"] });
    } catch (e) {
      toast(t("err.invite"), { error: true, description: nameError(e) });
    } finally {
      setBusy(false);
    }
  };
  return (
    <form
      className="g-fade flex flex-col gap-2 rounded-lg bg-fill p-2.5"
      onSubmit={(e) => {
        e.preventDefault();
        void make();
      }}
    >
      <input
        autoFocus
        value={name}
        maxLength={12}
        onChange={(e) => setName(e.target.value)}
        placeholder={t("term.nameOptional")}
        aria-label={t("term.name")}
        className="no-ring h-9 rounded-md bg-fill-2 px-2.5 text-base placeholder:text-ink-3"
      />
      {canClaude && (
        <div className="flex items-center gap-2 px-0.5">
          <span className="min-w-0 flex-1">
            <span className="block text-sm">{t("term.withClaude")}</span>
            <span className="block text-xs text-ink-3">{t("term.withClaudeHint")}</span>
          </span>
          <Switch label={t("term.withClaude")} on={withClaude} onChange={setWithClaude} />
        </div>
      )}
      <div className="flex justify-end gap-2">
        <SecondKey onClick={done}>{t("me.cancel")}</SecondKey>
        <MainKey type="submit" busy={busy}>
          {t("term.make")}
        </MainKey>
      </div>
    </form>
  );
}

/** The agents a permission list offers by name; others it was given show too. */
const AGENTS = ["claude", "codex", "pi", "dsh"];
const ALL = "*";
const permsOf = (p?: Perms): Perms => p ?? { agents: [ALL], scope: [ALL] };
const same = (a: string[], b: string[]) => a.length === b.length && a.every((v, i) => v === b[i]);

/** What an app may use: its agents (all, or some) and its scope (all, or places "应用名:路径").
 *  Changed here, saved by whoever approves. */
function PermsEditor({ perms, save: put, title }: { perms?: Perms; save: (p: Perms) => Promise<Perms>; title?: string }) {
  const qc = useQueryClient();
  const was = permsOf(perms);
  const [p, setP] = useState<Perms>(was);
  const [place, setPlace] = useState("");
  const [busy, setBusy] = useState(false);
  useEffect(() => setP(permsOf(perms)), [perms]);
  const dirty = !same(p.agents, was.agents) || !same(p.scope, was.scope);
  const allAgents = p.agents.includes(ALL);
  const allScope = p.scope.includes(ALL);
  const flipAgent = (a: string, on: boolean) =>
    setP({
      ...p,
      agents: on ? [...p.agents.filter((x) => x !== ALL), a] : p.agents.filter((x) => x !== a),
    });
  const addPlace = () => {
    const v = place.trim();
    if (v && !p.scope.includes(v)) setP({ ...p, scope: [...p.scope.filter((x) => x !== ALL), v] });
    setPlace("");
  };
  const save = async () => {
    setBusy(true);
    try {
      setP(await put(p));
      toast(t("apps.saved"));
      void qc.invalidateQueries({ queryKey: ["apps"] });
    } catch (e) {
      toast(t("apps.perms"), { error: true, description: errText(e) });
    } finally {
      setBusy(false);
    }
  };
  return (
    <Group title={title ?? t("apps.perms")}>
      <div className="flex min-h-10 items-center gap-2 px-2">
        <span className="min-w-0 flex-1 text-sm">{t("apps.allAgents")}</span>
        <Switch label={t("apps.allAgents")} on={allAgents} onChange={(on) => setP({ ...p, agents: on ? [ALL] : [] })} />
      </div>
      {!allAgents && (
        <div className="g-fade flex flex-wrap gap-1.5 px-2 pb-2">
          {[...new Set([...AGENTS, ...p.agents])].map((a) => {
            const on = p.agents.includes(a);
            return (
              <button
                key={a}
                type="button"
                aria-pressed={on}
                onClick={() => flipAgent(a, !on)}
                className={`g-press flex h-7 items-center gap-1 rounded-full px-2.5 text-sm ${on ? "bg-ink text-on-ink" : "bg-fill text-ink-2 hover:bg-fill-2"}`}
              >
                {on && <Check size={13} />}
                {a}
              </button>
            );
          })}
        </div>
      )}
      <div className="flex min-h-10 items-center gap-2 px-2">
        <span className="min-w-0 flex-1 text-sm">{t("apps.allScope")}</span>
        <Switch label={t("apps.allScope")} on={allScope} onChange={(on) => setP({ ...p, scope: on ? [ALL] : [] })} />
      </div>
      {!allScope && (
        <div className="g-fade flex flex-col gap-1 px-2 pb-2">
          {p.scope.length === 0 && <p className="m-0 text-xs text-ink-3">{t("apps.scopeNone")}</p>}
          {p.scope.map((s) => (
            <div key={s} className="flex min-h-8 items-center gap-2">
              <code className="min-w-0 flex-1 truncate font-mono text-xs">{s}</code>
              <IconKey label={t("apps.scopeRemove")} onClick={() => setP({ ...p, scope: p.scope.filter((x) => x !== s) })} className="size-6">
                <X size={13} />
              </IconKey>
            </div>
          ))}
          <form
            className="flex gap-1.5"
            onSubmit={(e) => {
              e.preventDefault();
              addPlace();
            }}
          >
            <input
              value={place}
              onChange={(e) => setPlace(e.target.value)}
              placeholder={t("apps.scopePlaceholder")}
              aria-label={t("apps.scopeAdd")}
              className="no-ring h-8 min-w-0 flex-1 rounded-md bg-fill px-2.5 font-mono text-xs placeholder:font-sans placeholder:text-ink-3"
            />
            <SecondKey type="submit" disabled={!place.trim()}>
              {t("apps.scopeAdd")}
            </SecondKey>
          </form>
        </div>
      )}
      {dirty && (
        <div className="g-fade flex justify-end gap-2 px-2">
          <SecondKey onClick={() => setP(was)}>{t("apps.reset")}</SecondKey>
          <MainKey busy={busy} onClick={() => void save()}>
            {t("apps.save")}
          </MainKey>
        </div>
      )}
    </Group>
  );
}

/** The name in place, chosen whole: Enter or leaving saves it, Escape keeps the old one. */
function Rename({ id, name: was, done }: { id: string; name: string; done: () => void }) {
  const qc = useQueryClient();
  const [name, setName] = useState(was);
  const gone = useRef(false);
  const save = async () => {
    if (gone.current) return;
    gone.current = true;
    const to = name.trim();
    done();
    if (!to || to === was) return;
    try {
      await api.profile({ name: to }, id);
      void qc.invalidateQueries({ queryKey: ["apps"] });
    } catch (e) {
      toast(t("term.renameIt"), { error: true, description: nameError(e) });
    }
  };
  return (
    <input
      autoFocus
      value={name}
      maxLength={12}
      aria-label={t("term.rename")}
      onFocus={(e) => e.currentTarget.select()}
      onChange={(e) => setName(e.target.value)}
      onBlur={() => void save()}
      onKeyDown={(e) => {
        if (e.nativeEvent.isComposing) return;
        if (e.key === "Enter") e.currentTarget.blur();
        if (e.key === "Escape") ((gone.current = true), done());
      }}
      className="no-ring h-7 w-36 min-w-0 rounded-md bg-fill px-2 text-base font-medium select-text"
    />
  );
}
