// The settings' 应用管理 (draft 四): two lists (k_glossary "电脑"). 应用: 网页 (ir_web) and the apps
// that speak; 电脑: the host (as Iridium) and the computers that joined, one flat list, those on one
// machine next to each other (docs/前端设计规范.md: no levels). A row
// is its avatar, name, whether it is online, when last active (a computer's system and agents);
// avatar and name are changed in place, removal is in its menu, and nothing opens but 网页's
// logins and invites (docs/前端设计规范.md). 配对 is a switch, on till turned off; requests wait as
// cards on top, the code checked against the one the app shows. No TOKEN is handed out here.
import { Menu } from "@base-ui/react/menu";
import { useQueryClient, type QueryClient } from "@tanstack/react-query";
import { Check, ChevronLeft, ChevronRight, Copy, Link2, LoaderCircle, PencilLine, Plus, SlidersHorizontal, Trash2, X } from "lucide-react";
import { Fragment, useEffect, useRef, useState, type ReactNode } from "react";
import { ApiError, api } from "./api/client";
import type { Agent, App, Apps, Computer, Login, Perms, PendingInvite } from "./api/types.gen";
import { CardFrame, CardHead, MainKey, SecondKey } from "./card";
import { ConfirmCard, saysOwner } from "./confirms";
import { isComputer, useApps, useMe } from "./data";
import { t, useLang } from "./i18n";
import { Code } from "./chat/hl";
import { Segmented } from "./me";
import { Crop } from "./profile";
import { SecretHere } from "./secret";
import { showSecret, useSecret } from "./secrets";
import { Group } from "./settings";
import { useConfirms, usePairing } from "./store";
import { errText, toast } from "./toast";
import { agentName, AGENTS as AGENT_MARKS, ago, Avatar, AvatarKey, cn, IconKey, IrMark, Logo, own, Switch, Tip, useLongPress, useMenuAt } from "./ui";
import { MenuItems, type MenuItem } from "./sidebar";

const systems: Record<string, string> = {
  windows: "Windows",
  darwin: "macOS",
  linux: "Linux",
};


/** The apps on the same machine as id, after it (Computer.under): listed next to it, not under it. */
const sameMachine = (apps: App[], id: string) => apps.filter((a) => a.computer?.under === id);

/** A machine's line: its system and agents, e.g. "Linux · claude 2.1 · codex". */
/** A computer's agents, those not turned off, that have a mark. */
const marked = (m: Computer) => m.agents.filter((a) => !a.off && (AGENT_MARKS as string[]).includes(a.agent));

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

type View = { kind: "list" } | { kind: "app"; id: string } | { kind: "add" };

/** In the settings, what opens is a page of its own that slides in, with its way back; no popover
 *  (docs/前端设计规范.md). */
export function AppsPage({ onBack }: { onBack: () => void }) {
  useLang();
  const q = useApps();
  const [view, setView] = useState<View>({ kind: "list" });
  const pairing = !!usePairing()?.open;
  const apps = q.data?.apps ?? [];
  const open = view.kind === "app" ? apps.find((a) => a.id === view.id) : undefined;
  const list = () => setView({ kind: "list" });
  // 网页 opens its logins and invites; an app or a computer has no page.
  const row = (a: App) =>
    a.web ? (
      <AppRow key={a.id} a={a} onOpen={() => setView({ kind: "app", id: a.id })} />
    ) : (
      <Fragment key={a.id}>
        <ItemRow a={a} />
        {sameMachine(apps, a.id).map((c) => (
          <ItemRow key={c.id} a={c} />
        ))}
      </Fragment>
    );
  const computers = apps.filter((a) => isComputer(a) && !a.computer?.under);
  if (open && q.data)
    return (
      <div key={open.id} className="g-slide h-full">
        <AppPage data={q.data} onBack={list} />
      </div>
    );
  if (view.kind === "add")
    return (
      <div className="g-slide h-full">
        <AddComputer onBack={list} />
      </div>
    );
  return (
    <Page title={t("settings.terminals")} onBack={onBack} backLabel={t("nav.me")} busy={q.isFetching} right={<PairingKey open={pairing} />}>
      <Needs />
      {q.isError && <p className="m-0 text-sm text-ink-3">{errText(q.error)}</p>}
      {q.data && (
        <>
          {apps.some((a) => !isComputer(a)) && <Group title={t("term.channel")}>{apps.filter((a) => !isComputer(a)).map(row)}</Group>}
          <Group
            title={t("apps.computer")}
            action={
              <IconKey label={t("apps.addComputer")} onClick={() => setView({ kind: "add" })} className="-my-2 size-6 pointer-coarse:size-8">
                <Plus size={14} />
              </IconKey>
            }
          >
            {computers.map(row)}
          </Group>
        </>
      )}
    </Page>
  );
}

/** A computer's system, as its join line is written: macOS and Linux one (sh), Windows another. */
export type JoinOS = "unix" | "win";

/** The line that makes a computer join this Iridium while its terminal stays open (docs/exec.md
 *  "ir-computer"): it fetches the script this Iridium writes (internal/joinscript), which fetches
 *  ir-computer and runs it there. The options in the query, the address quoted once there is one.
 *  Windows: from cmd or PowerShell alike. */
export function joinScript(os: JoinOS, at: string, query = ""): string {
  const url = `${at}/join${os === "win" ? ".ps1" : ""}${query ? `?${query}` : ""}`;
  const arg = query ? `'${url}'` : url;
  return os === "win" ? `powershell -c "irm ${arg} | iex"` : `curl -fsSL ${arg} | sh`;
}

/** The 电脑 group's +, a page: the line to run on a computer, for its system; as it is, with only
 *  some folders, with commands. Each its copy key in its title's row; the line one line high,
 *  scrolled sideways (to its end at first), no room kept beside it (docs/前端设计规范.md "命令"). */
function AddComputer({ onBack }: { onBack: () => void }) {
  const [os, setOs] = useState<JoinOS>(/Win/.test(navigator.platform) ? "win" : "unix");
  const at = location.origin;
  const system = (v: JoinOS, label: string) => ({ v, label, icon: <span className="px-1 text-xs">{label}</span> });
  return (
    <Page title={t("apps.addComputer")} onBack={onBack} backLabel={t("settings.terminals")}>
      <Segmented label={t("apps.system")} value={os} onChange={(v) => setOs(v as JoinOS)} items={[system("unix", "macOS / Linux"), system("win", "Windows")]} />
      {(
        [
          ["apps.joinPlain", ""],
          ["apps.joinRoots", "roots=~/work,~/notes"],
          ["apps.joinShell", "shell"],
        ] as const
      ).map(([label, query]) => {
        const line = joinScript(os, at, query);
        return (
          <Group key={label} title={t(label)} action={<CopyKey text={line} />}>
            <Line text={line} lang={os === "win" ? "powershell" : "sh"} />
          </Group>
        );
      })}
    </Page>
  );
}

/** A command on one line, scrolled to its end: where the lines differ (the flags, the address). */
function Line({ text, lang }: { text: string; lang: string }) {
  const box = useRef<HTMLPreElement>(null);
  useEffect(() => {
    if (box.current) box.current.scrollLeft = box.current.scrollWidth;
  }, [text]);
  return (
    <pre ref={box} className="no-bar m-0 overflow-x-auto rounded-md bg-fill px-3 py-2 text-[13px] whitespace-pre">
      <code>
        <Code text={text} lang={lang} />
      </code>
    </pre>
  );
}

/** Copies text; a check for a moment after. */
function CopyKey({ text }: { text: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <IconKey
      label={copied ? t("msg.copied") : t("msg.copy")}
      onClick={() => void navigator.clipboard.writeText(text).then(() => (setCopied(true), setTimeout(() => setCopied(false), 1500)))}
      className="-my-2 size-6 pointer-coarse:size-8"
    >
      {copied ? <Check size={13} /> : <Copy size={13} />}
    </IconKey>
  );
}

/** A page of 应用管理: its header (back, title, its key) and its scrolling body. */
function Page({ title, onBack, backLabel, busy, right, children }: { title: string; onBack: () => void; backLabel: string; busy?: boolean; right?: ReactNode; children: ReactNode }) {
  return (
    <div className="flex h-full min-h-0 flex-col">
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

/** 网页 in the list: it opens its logins and invites. */
function AppRow({ a, onOpen }: { a: App; onOpen: () => void }) {
  return (
    <button type="button" onClick={onOpen} className="g-press flex min-h-14 w-full items-center gap-3 rounded-lg px-2 text-left hover:bg-fill">
      <Avatar src={a.avatar} name={t("term.panel")} size={32} />
      <span className="min-w-0 flex-1">
        <span className="block truncate text-base font-medium">{t("term.panel")}</span>
        <Seen a={a} />
      </span>
      <ChevronRight size={15} className="shrink-0 text-ink-4" />
    </button>
  );
}

/** A row's second line: online or when last seen; 网页's logins, a computer's system, then the
 *  agents it brings as their marks (no versions). Always one line, every row the same height: what
 *  has no room scrolls sideways, nothing cut, no "·" between (the user, 2026-11-16). The host: its
 *  system and agents only. */
function Seen({ a }: { a: App }) {
  // A computer is online while its executor is connected (Computer.online); another app, while it
  // has a connection (App.online counts them).
  const online = a.computer ? a.computer.online : !!a.online;
  const used = a.host ? "" : online ? t("term.online") : ago(Math.max(a.computer?.seenAt ?? 0, a.lastSeenAt ?? 0) || a.createdAt || 0);
  const agents = a.computer ? marked(a.computer) : [];
  const words = [used, a.web && a.logins ? t("apps.loginCount", { n: a.logins }) : "", a.computer?.os ? (systems[a.computer.os] ?? a.computer.os) : ""].filter(Boolean);
  return (
    <span className="no-bar flex min-w-0 items-center gap-2.5 overflow-x-auto text-xs whitespace-nowrap text-ink-3 tabular-nums">
      {online && !a.host ? <span className="-mr-1 size-1.5 shrink-0 rounded-full bg-run" aria-hidden /> : null}
      {words.map((w) => (
        <span key={w} className="shrink-0">
          {w}
        </span>
      ))}
      {agents.length > 0 && (
        <span className="flex shrink-0 items-center gap-1.5">
          {agents.map((x) => (
            <Tip key={x.agent} label={agentName(x.agent as Agent)}>
              <Logo agent={x.agent as Agent} size={12} />
            </Tip>
          ))}
        </span>
      )}
    </span>
  );
}

/** An app or a computer in the list, no page of its own (docs/前端设计规范.md): its avatar and name
 *  changed in place, removal in its menu (right click, a long press). The host: nothing to change.
 *  The host is Iridium: its mark and name, nothing to change, the key's place kept (the rows line up). */
function ItemRow({ a }: { a: App }) {
  const qc = useQueryClient();
  const me = useMe().data;
  const [renaming, setRenaming] = useState(false);
  const [sure, setSure] = useState(false);
  const [busy, setBusy] = useState(false);
  const face = useFacePick(a.id);
  const menu = useRowMenu();
  const remove = t(isComputer(a) ? "apps.deleteComputer" : "apps.delete");
  return (
    <div className="flex flex-col">
      <div {...(a.host ? {} : menu.on)} className={cn("flex min-h-14 items-center gap-3 rounded-lg px-2", menu.at && "bg-fill")}>
        {a.host ? <IrMark size={32} className="text-ink" /> : <AvatarKey label={t("term.avatar")} onClick={face.open} src={a.avatar} name={a.name} size={32} type={a.type} />}
        <span className="min-w-0 flex-1">
          <span className="flex items-center gap-2">
            {renaming ? <Rename id={a.id} name={a.name} done={() => setRenaming(false)} /> : <span className="truncate text-base font-medium">{a.host ? "Iridium" : a.name}</span>}
            {a.id === me?.app && !a.host && <span className="shrink-0 text-xs text-ink-3">{t("term.this")}</span>}
          </span>
          <Seen a={a} />
        </span>
        {a.host ? (
          <span className="size-7 shrink-0 pointer-coarse:size-10" aria-hidden />
        ) : (
          !renaming && (
            <IconKey label={t("term.renameIt")} onClick={() => setRenaming(true)} className="size-7">
              <PencilLine size={14} />
            </IconKey>
          )
        )}
      </div>
      {face.crop}
      {sure && (
        <div className="g-fade py-2">
          <CardFrame label={t("term.revokeTitle", { name: a.name })}>
            <CardHead
              icon={<Trash2 size={16} />}
              title={t("term.revokeTitle", { name: a.name })}
              summary={t("term.revokeHint")}
              keys={
                <>
                  <SecondKey onClick={() => setSure(false)}>{t("me.cancel")}</SecondKey>
                  <MainKey busy={busy} onClick={() => (setBusy(true), void revoke(qc, a.id, a.name).then(() => (setBusy(false), setSure(false))))}>
                    {t("term.revoke")}
                  </MainKey>
                </>
              }
            />
          </CardFrame>
        </div>
      )}
      {face.input}
      {!a.host && menu.menu([{ icon: <Trash2 size={15} />, label: remove, run: () => setSure(true) }])}
    </div>
  );
}

/** A row's menu, at the pointer on a right click, under the row on a long press. */
function useRowMenu() {
  const [at, setAt] = useMenuAt();
  const box = useRef<HTMLDivElement>(null);
  const lp = useLongPress(() => {
    const r = box.current?.getBoundingClientRect();
    if (r) setAt({ x: r.left + 16, y: r.bottom });
  });
  const anchor = at && {
    getBoundingClientRect: () => DOMRect.fromRect({ x: at.x, y: at.y, width: 0, height: 0 }),
  };
  return {
    at,
    on: {
      ref: box,
      ...lp,
      onContextMenu: (e: React.MouseEvent) => own(e) && (e.preventDefault(), setAt({ x: e.clientX, y: e.clientY })),
    },
    menu: (items: MenuItem[]) => (
      <Menu.Root open={at !== null} onOpenChange={(o) => !o && setAt(null)}>
        <Menu.Portal>
          <Menu.Positioner anchor={anchor} side="bottom" align="start" sideOffset={4} collisionPadding={8} className="z-50">
            <MenuItems items={items} />
          </Menu.Positioner>
        </Menu.Portal>
      </Menu.Root>
    ),
  };
}

/** A new face for app or login id: a picture chosen, cropped in place under the row, saved. */
function useFacePick(id: string) {
  const qc = useQueryClient();
  const [picture, setPicture] = useState<string | null>(null);
  const file = useRef<HTMLInputElement>(null);
  const drop = () => setPicture((old) => (old && URL.revokeObjectURL(old), null));
  const save = async (blob: Blob) => {
    try {
      await api.profile({ avatar: blob }, id);
      drop();
      void qc.invalidateQueries({ queryKey: ["apps"] });
    } catch (e) {
      toast(t("term.avatar"), { error: true, description: errText(e) });
    }
  };
  return {
    open: () => file.current?.click(),
    input: (
      <input
        ref={file}
        type="file"
        accept="image/*"
        hidden
        onChange={(e) => {
          const f = e.target.files?.[0];
          if (f) setPicture((old) => (old && URL.revokeObjectURL(old), URL.createObjectURL(f)));
          e.target.value = "";
        }}
      />
    ),
    crop: picture && (
      <div className="g-fade flex justify-center py-2">
        <Crop src={picture} onCancel={drop} onDone={(b) => void save(b)} />
      </div>
    ),
  };
}

/** 网页's page: its browsers, and the unused invites as slots; its + (top right) a new invite. */
function AppPage({ data, onBack }: { data: Apps; onBack: () => void }) {
  const me = useMe().data;
  const [making, setMaking] = useState(false);
  const logins = data.logins.filter(ofWeb);
  return (
    <Page
      title={t("term.panel")}
      onBack={onBack}
      backLabel={t("settings.terminals")}
      right={
        <IconKey label={me?.public ? t("term.invite") : t("term.needPublic")} disabled={!me?.public || making} onClick={() => setMaking(true)} tipSide="bottom" className="size-7">
          <Plus size={16} />
        </IconKey>
      }
    >
      {making && <InviteForm done={() => setMaking(false)} />}
      <WebLogins logins={logins} invites={data.invites} />
    </Page>
  );
}

/** Rows under a title (a login's permissions, opened inline), else a plain list. */
function Rows({ title, children }: { title?: string; children: ReactNode }) {
  return title ? <Group title={title}>{children}</Group> : <section className="flex flex-col">{children}</section>;
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

/** A login, as an app's row: its avatar (pressed, a new one), name (its key renames it), when it
 *  was last active, waiting or not; its permissions and removal in its menu (right click, a long
 *  press). */
function LoginRow({ l, app, remove }: { l: Login; app: string; remove: () => void }) {
  const me = useMe().data;
  const [renaming, setRenaming] = useState(false);
  const [perms, setPerms] = useState(false);
  const face = useFacePick(l.id);
  const menu = useRowMenu();
  return (
    <div className="flex flex-col">
      <div {...menu.on} className={cn("flex min-h-12 items-center gap-3 rounded-lg px-2", menu.at && "bg-fill")}>
        <AvatarKey label={t("term.avatar")} onClick={face.open} src={l.avatar} name={l.name} size={28} />
        <span className="min-w-0 flex-1">
          <span className="flex items-center gap-2">
            {renaming ? <Rename id={l.id} name={l.name} done={() => setRenaming(false)} /> : <span className="truncate text-base">{l.name}</span>}
            {l.id === me?.login && <span className="shrink-0 text-xs text-ink-3">{t("term.this")}</span>}
            {!l.approvedAt && <span className="shrink-0 rounded-sm bg-fill-2 px-1.5 text-[11px] leading-[18px] text-ink-2">{t("apps.pending")}</span>}
          </span>
          <span className="flex items-center gap-1.5 text-xs whitespace-nowrap text-ink-3 tabular-nums">
            {l.online ? <span className="size-1.5 shrink-0 rounded-full bg-run" aria-hidden /> : null}
            {l.online ? t("term.online") : ago(l.lastSeenAt || l.createdAt || 0)}
          </span>
        </span>
        {!renaming && (
          <IconKey label={t("term.renameIt")} onClick={() => setRenaming(true)} className="size-7">
            <PencilLine size={14} />
          </IconKey>
        )}
      </div>
      {face.crop}
      {perms && (
        <div className="g-fade mb-2 ml-11 rounded-lg bg-fill/60 py-2">
          <PermsEditor title={t("apps.loginPerms")} perms={l.perms} save={(p) => api.loginPerms(app, l.id, p)} />
        </div>
      )}
      {face.input}
      {menu.menu([
        { icon: <SlidersHorizontal size={15} />, label: t("apps.perms"), run: () => setPerms(!perms) },
        { icon: <Trash2 size={15} />, label: t("apps.removeLogin"), run: remove },
      ])}
    </div>
  );
}

/** 网页's logins (the browsers' passkeys) and its unused invites as slots. */
function WebLogins({ logins, invites }: { logins: Login[]; invites: PendingInvite[] }) {
  const qc = useQueryClient();
  return (
    <Group title={t("apps.logins")}>
      {logins.map((l) => (
        <LoginRow key={l.id} l={l} app="ir_web" remove={() => void revoke(qc, l.id, l.name)} />
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
const AGENTS = ["claude", "codex", "pi", "dsh", "agy"];
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
    <Rows title={title}>
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
    </Rows>
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
