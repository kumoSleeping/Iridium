// The settings' 应用管理 (draft 四): the host, 网页 (ir_web) and the other apps in one list, each
// with its avatar, name, type, whether it is online, when it was last active; one that carries
// agents also its system and agents. The host is listed, not managed. A row opens the app's page:
// its permissions (agents, scope), the agents it carries and whether command lines run there, its
// logins (网页's: the browsers, and the unused invites as slots), rename, avatar, removal. 新建应用
// opens pairing for 10 minutes and lists the official apps' install commands; requests wait as
// cards on top, the code checked against the one the app shows. No TOKEN is handed out here.
import { useQuery, useQueryClient, type QueryClient } from "@tanstack/react-query";
import { Check, ChevronLeft, ChevronRight, Copy, Link2, LoaderCircle, PencilLine, Plus, SlidersHorizontal, Trash2, X } from "lucide-react";
import { useEffect, useRef, useState, type ReactNode } from "react";
import { ApiError, api } from "./api/client";
import type { Agent, App, Apps, Computer, Login, Perms, PendingInvite, Rules } from "./api/types.gen";
import { CardFrame, CardHead, MainKey, SecondKey } from "./card";
import { ConfirmCard, saysOwner } from "./confirms";
import { isPair } from "./confirmname";
import { useApps, useMe } from "./data";
import { t, useLang } from "./i18n";
import { LocalMark } from "./me";
import { Crop } from "./profile";
import { SecretHere } from "./secret";
import { showSecret, useSecret } from "./secrets";
import { Group } from "./settings";
import { useConfirms, usePairing } from "./store";
import { errText, toast } from "./toast";
import { ago, Avatar, IconKey, Logo, Switch, Tip, tokens, typeMark } from "./ui";

/** Claude: it was made the owner's own. Set then, never after (the user, 2026-11-08). */
export const hasClaude = (r: Rules) => !!r.claudeOwner;

const systems: Record<string, string> = {
  windows: "Windows",
  darwin: "macOS",
  linux: "Linux",
};

/** What an app is shown as: the host, 网页, or its type (电脑, Bot…), else 应用. */
const typeName = (a: App) => {
  if (a.host) return t("term.local");
  if (a.web) return t("term.panel");
  const m = typeMark(a.type);
  return m ? t(m[1]) : a.type || t("term.channel");
};

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

type View = { kind: "list" } | { kind: "app"; id: string } | { kind: "new" };

export function AppsPage({ onBack }: { onBack: () => void }) {
  useLang();
  const q = useApps();
  const [view, setView] = useState<View>({ kind: "list" });
  const apps = q.data?.apps ?? [];
  const open = view.kind === "app" ? apps.find((a) => a.id === view.id) : undefined;
  if (open && q.data)
    return (
      <div key={open.id} className="g-slide h-full">
        <AppPage app={open} data={q.data} machine={open.computer} onBack={() => setView({ kind: "list" })} />
      </div>
    );
  if (view.kind === "new")
    return (
      <div key="new" className="g-slide h-full">
        <NewApp onBack={() => setView({ kind: "list" })} />
      </div>
    );
  return (
    <Page
      title={t("settings.terminals")}
      onBack={onBack}
      backLabel={t("nav.me")}
      busy={q.isFetching}
      right={
        <Tip label={t("apps.new")} side="bottom">
          <button
            type="button"
            aria-label={t("apps.new")}
            onClick={() => setView({ kind: "new" })}
            className="g-press grid size-7 place-items-center rounded-md text-ink-2 hover:bg-fill hover:text-ink pointer-coarse:size-10"
          >
            <Plus size={16} />
          </button>
        </Tip>
      }
    >
      <Needs />
      {q.isError && <p className="m-0 text-sm text-ink-3">{errText(q.error)}</p>}
      <section className="flex flex-col" aria-label={t("settings.terminals")}>
        {apps.map((a) => (
          <AppRow key={a.id} a={a} machine={a.computer} onOpen={a.host ? undefined : () => setView({ kind: "app", id: a.id })} />
        ))}
      </section>
    </Page>
  );
}

/** A page of 应用管理: its header (back, title, what is on its right) and its scrolling body. */
function Page({ title, onBack, backLabel, busy, right, children }: { title: string; onBack: () => void; backLabel: string; busy?: boolean; right?: ReactNode; children: ReactNode }) {
  return (
    <div className="flex h-full flex-col">
      <header className="flex h-10.5 shrink-0 items-center gap-1 px-1.5 pt-1.5">
        <IconKey label={backLabel} onClick={onBack} tipSide="bottom" className="size-7">
          <ChevronLeft size={16} />
        </IconKey>
        <h1 className="m-0 min-w-0 flex-1 truncate text-base font-semibold">{title}</h1>
        {busy && <LoaderCircle size={14} className="animate-spin text-ink-4" />}
        {right}
      </header>
      <div className="no-bar min-h-0 flex-1 overflow-y-auto pt-2 pb-4">
        <div className="flex flex-col gap-5 px-3">{children}</div>
      </div>
    </div>
  );
}

/** What waits for a person here: the needs (only pairing ones if pair) and a secret allowed here. */
function Needs({ pair }: { pair?: boolean }) {
  const needs = useConfirms().filter((c) => !pair || isPair(c));
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

/** One app in the list; it opens its page (not the host's). */
function AppRow({ a, machine, onOpen }: { a: App; machine?: Computer; onOpen?: () => void }) {
  const me = useMe().data;
  const used = a.online ? t("term.online") : a.host ? t("term.localShort") : ago(a.lastSeenAt || a.createdAt || 0);
  const body = (
    <>
      {a.host ? <LocalMark size={32} /> : <Avatar src={a.avatar} name={a.web ? t("term.panel") : a.name} size={32} type={a.type} />}
      <span className="min-w-0 flex-1">
        <span className="flex items-center gap-2">
          <span className="truncate text-base font-medium">{a.host ? t("me.local") : a.web ? t("term.panel") : a.name}</span>
          {!a.web && <span className="shrink-0 rounded-sm bg-fill-2 px-1.5 text-[11px] leading-[18px] text-ink-2">{typeName(a)}</span>}
          {a.id === me?.app && !a.host && !a.web && <span className="shrink-0 text-xs text-ink-3">{t("term.this")}</span>}
        </span>
        {/* One line, cut at its end, never wrapped into columns (the panel is narrow). */}
        <span className="flex min-w-0 items-center gap-1.5 text-xs text-ink-3 tabular-nums">
          {a.online ? <span className="size-1.5 shrink-0 rounded-full bg-run" aria-hidden /> : null}
          <span className="min-w-0 truncate">
            {used} · {t("term.today", { n: tokens(a.usedToday) })}
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
    <button type="button" onClick={onOpen} className={`${cls} g-press hover:bg-fill`}>
      {body}
    </button>
  ) : (
    <div className={cls}>{body}</div>
  );
}

/** An app's page: what it is, its permissions, what it carries, its logins, removal. */
function AppPage({ app, data, machine, onBack }: { app: App; data: Apps; machine?: Computer; onBack: () => void }) {
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
  const title = app.web ? t("term.panel") : app.name;
  return (
    <Page title={title} onBack={onBack} backLabel={t("settings.terminals")}>
      <section className="flex flex-col gap-2">
        <div className="flex items-center gap-3 px-2">
          {app.web ? (
            <Avatar src={app.avatar} name={title} size={40} />
          ) : (
            <button type="button" onClick={() => file.current?.click()} aria-label={t("term.avatar")} className="g-press rounded-full">
              <Avatar src={app.avatar} name={app.name} size={40} type={app.type} />
            </button>
          )}
          <span className="min-w-0 flex-1">
            <span className="flex items-center gap-2">
              {renaming ? <Rename id={app.id} name={app.name} done={() => setRenaming(false)} /> : <span className="truncate text-base font-medium">{title}</span>}
              {!app.web && <span className="shrink-0 rounded-sm bg-fill-2 px-1.5 text-[11px] leading-[18px] text-ink-2">{typeName(app)}</span>}
              {!app.web && !renaming && (
                <IconKey label={t("term.renameIt")} onClick={() => setRenaming(true)} className="size-6">
                  <PencilLine size={13} />
                </IconKey>
              )}
            </span>
            <span className="flex items-center gap-1.5 text-xs text-ink-3 tabular-nums">
              {app.online ? <span className="size-1.5 shrink-0 rounded-full bg-run" aria-hidden /> : null}
              {app.online ? t("term.online") : ago(app.lastSeenAt || app.createdAt || 0)} · {t("term.today", { n: tokens(app.usedToday) })}
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
      ) : (
        <>
          <PermsEditor perms={app.perms} save={(p) => api.appPerms(app.id, p)} />
          {machine && (
            <Group title={t("apps.computer")}>
              <Line label={t("confirm.system")}>{[systems[machine.os] ?? machine.os, machine.arch].filter(Boolean).join(" ") || "—"}</Line>
              {machine.agents.length ? <CarriedAgents app={app.id} agents={machine.agents} /> : <Line label={t("apps.carried")}>{t("apps.none")}</Line>}
              <Line label={t("confirm.commands")}>{t(`confirm.commands.${shellOf(machine)}`)}</Line>
            </Group>
          )}
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
                {t("apps.delete")}
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
function CarriedAgents({ app, agents }: { app: string; agents: Computer["agents"] }) {
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
          <span className="w-20 shrink-0 text-ink-3">{a === agents[0] ? t("apps.carried") : ""}</span>
          <Logo agent={a.agent as Agent} size={14} />
          <span className={`min-w-0 flex-1 truncate ${a.off ? "text-ink-3" : ""}`}>{[a.agent, a.version].filter(Boolean).join(" ")}</span>
          {busy === a.agent && <LoaderCircle size={13} className="animate-spin text-ink-4" />}
          <Switch label={a.agent} on={!a.off} disabled={!!busy} onChange={(on) => void set(a.agent, on)} />
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

/** m:ss till until. */
export const left = (until: number, now: number) => {
  const s = Math.max(0, Math.round((until - now) / 1000));
  return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, "0")}`;
};

/** 新建应用: pairing opens for 10 minutes as it shows (closed early here); the official apps'
 *  install commands and ir pair for any other program; requests come in as cards on top. */
function NewApp({ onBack }: { onBack: () => void }) {
  const lang = useLang();
  const win = usePairing();
  const [now, setNow] = useState(Date.now());
  const [err, setErr] = useState<string | null>(null);
  const cat = useQuery({
    queryKey: ["catalogue"],
    queryFn: () => api.catalogue(),
    staleTime: Infinity,
  });
  const opened = useRef(false);
  const set = async (open: boolean) => {
    try {
      await api.pairing(open);
      setErr(null);
    } catch (e) {
      setErr(errText(e));
    }
  };
  useEffect(() => {
    if (opened.current) return;
    opened.current = true;
    void set(true);
  }, []);
  const until = win?.open ? (win.until ?? 0) : 0;
  const open = until > now;
  useEffect(() => {
    if (!until) return;
    setNow(Date.now());
    const id = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(id);
  }, [until]);
  const origin = location.origin;
  return (
    <Page title={t("apps.new")} onBack={onBack} backLabel={t("settings.terminals")}>
      <div className="flex min-h-11 items-center gap-3 rounded-lg bg-fill px-3">
        <span className={`size-2 shrink-0 rounded-full ${open ? "bg-run" : "bg-ink-4"}`} aria-hidden />
        <span className="min-w-0 flex-1 text-sm tabular-nums">{open ? t("apps.pairOpen", { left: left(until, now) }) : (err ?? t("apps.pairClosed"))}</span>
        {open ? <SecondKey onClick={() => void set(false)}>{t("apps.pairClose")}</SecondKey> : <MainKey onClick={() => void set(true)}>{t("apps.pairReopen")}</MainKey>}
      </div>
      <Needs pair />
      <p className="m-0 px-1 text-xs text-ink-3">{t("apps.pairNote")}</p>
      {(cat.data ?? []).map((c) => (
        <Group key={c.id} title={c.name}>
          <p className="m-0 mb-1.5 px-1 text-sm text-ink-2">{lang === "zh" ? c.description.zh : c.description.en}</p>
          {c.install.map((s, i) => (
            <Command key={i} label={s.os ? systems[s.os] : undefined} cmd={s.command.replaceAll("{origin}", origin)} />
          ))}
        </Group>
      ))}
      <Group title={t("apps.other")}>
        <p className="m-0 mb-1.5 px-1 text-sm text-ink-2">{t("apps.otherHint")}</p>
        <Command cmd={t("term.pairBotCmd", { at: origin })} />
      </Group>
    </Page>
  );
}

/** A command to copy, under the system it is for. */
function Command({ label, cmd }: { label?: string; cmd: string }) {
  const [done, setDone] = useState(false);
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(cmd);
      setDone(true);
      setTimeout(() => setDone(false), 1500);
    } catch (e) {
      toast(t("apps.copy"), { error: true, description: errText(e) });
    }
  };
  return (
    <div className="flex flex-col gap-1 px-1 pb-1.5">
      {label && <span className="text-xs text-ink-3">{label}</span>}
      <div className="flex items-start gap-1 rounded-md bg-fill py-1 pr-1 pl-2">
        <code className="min-w-0 flex-1 py-0.5 font-mono text-xs break-all select-all">{cmd}</code>
        <IconKey label={done ? t("apps.copied") : t("apps.copy")} onClick={() => void copy()} className="size-6 shrink-0">
          {done ? <Check size={13} /> : <Copy size={13} />}
        </IconKey>
      </div>
    </div>
  );
}
