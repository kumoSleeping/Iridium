// 管理 (kumo, 2026-11-27): 应用管理, 角色 and 凭据 in one window over the page, drawn as the usage
// is (usage.tsx): its parts as words on the title's line (账号 · 应用 · 电脑 · Agent · 角色 · 凭据),
// on a narrow window a line of their own under it. Each part is two halves: its list on the left,
// the one chosen on the right; nothing opens a level deeper but a page of the right half
// (通行密钥, GitHub, a login of an app), which keeps the list where it is. A narrow window shows
// one half at a time, the chosen one sliding in with its way back. 新建 is one key at the foot's
// right, the same place everywhere: what it makes is a row at the top of the list till it is made.
// Changed is saved at once: an agent's dot pressed, a folder added, an approval mode chosen.
import { Dialog } from "@base-ui/react/dialog";
import { useQueryClient } from "@tanstack/react-query";
import { ChevronLeft, ChevronRight, FolderOpen, Link2, LoaderCircle, Monitor, PencilLine, Plus, Trash2, X } from "lucide-react";
import { useLayoutEffect, useState, type ReactNode } from "react";
import { api } from "./api/client";
import type { Agent, App, Computer, Login, Perms, PendingInvite, Role } from "./api/types.gen";
import { AddComputer, clock, deviceBadge, nameError, ofWeb, OpenFolder, PasskeysPage, Rename, revoke, setOwner, systems, useFacePick, useGrant } from "./apps";
import { MainKey, SecondKey } from "./card";
import { ConfirmCard, saysOwner } from "./confirms";
import { SecretsPane } from "./credentials";
import { isComputer, manages as managesApps, useAgents, useApps, useMe, useSettings } from "./data";
import { Bind, Bound, GitHubMark } from "./github";
import { t, useLang } from "./i18n";
import { modeName } from "./modes";
import { Pills } from "./pills";
import { RoleFace, RoleLine, useRoles } from "./roles";
import { NewRolePage, placeOf, RolePage } from "./rolespage";
import { MANAGE_TABS, needTab, openManage, type ManageTab } from "./route";
import { SecretHere } from "./secret";
import { showSecret } from "./secrets";
import { useConfirms, usePairing } from "./store";
import { errText, toast } from "./toast";
import { agentName, ago, Avatar, AvatarKey, cn, FaceChangeOver, IconKey, Logo, ServerFace, SureKey, Switch, Tip } from "./ui";
import { useQuery } from "./api/query";

/** Where a secret made here (an invite's link) shows. */
const HERE = "manage";
/** The row being made, at the top of a list. */
const NEW = "\u0000new";
/** Two halves from this width of the window on (the usage's). */
const WIDE = 640;
const TAB_KEY: Record<ManageTab, Parameters<typeof t>[0]> = {
  accounts: "manage.accounts",
  apps: "manage.apps",
  computers: "manage.computers",
  agents: "manage.agents",
  roles: "manage.roles",
  secrets: "manage.secrets",
};

function useWide() {
  const [el, setEl] = useState<HTMLDivElement | null>(null);
  const [wide, setWide] = useState(false);
  useLayoutEffect(() => {
    if (!el) return;
    setWide(el.clientWidth >= WIDE);
    const ro = new ResizeObserver(() => setWide(el.clientWidth >= WIDE));
    ro.observe(el);
    return () => ro.disconnect();
  }, [el]);
  return [setEl, wide] as const;
}

/** The parts one may see: the accounts, apps and computers those who manage; the credentials the
 *  host; the agents and roles everyone. */
function useTabs(): ManageTab[] {
  const me = useMe().data;
  const manages = managesApps(me);
  return MANAGE_TABS.filter((x) => (x === "accounts" || x === "apps" || x === "computers" ? manages : x === "secrets" ? !!me?.host : true));
}

export function ManagePage({ tab: asked }: { tab: ManageTab }) {
  useLang();
  const tabs = useTabs();
  const tab = tabs.includes(asked) ? asked : (tabs[0] ?? "roles");
  const [root, wide] = useWide();
  const busy = useApps().isFetching;
  const parts = <Tabs tabs={tabs} value={tab} onChange={(x) => openManage(x)} />;
  return (
    <div ref={root} className="relative flex min-h-0 flex-1 flex-col">
      <header className="flex h-12 shrink-0 items-center gap-1 px-3 pointer-coarse:h-13">
        <Dialog.Title className="m-0 min-w-0 flex-1 truncate text-sm font-semibold">{t("manage.title")}</Dialog.Title>
        {busy && <span className="g-fade mr-1.5 grid [animation-delay:200ms]"><LoaderCircle size={14} className="animate-spin text-ink-4" /></span>}
        {wide && <div className="mr-2">{parts}</div>}
        <Dialog.Close render={<IconKey label={t("nav.close")} className="-mr-1" />}>
          <X size={17} />
        </Dialog.Close>
      </header>
      {!wide && <div className="no-bar flex h-9 shrink-0 items-center overflow-x-auto px-3">{parts}</div>}
      <Part key={tab} tab={tab} wide={wide} />
    </div>
  );
}

/** The parts, as the usage's days: words, the chosen one on a soft pill that slides to it. */
function Tabs({ tabs, value, onChange }: { tabs: ManageTab[]; value: ManageTab; onChange: (x: ManageTab) => void }) {
  const at = Math.max(0, tabs.indexOf(value));
  return (
    <div role="tablist" aria-label={t("manage.title")} className="relative -ml-1 grid shrink-0" style={{ gridTemplateColumns: `repeat(${tabs.length}, minmax(0, 1fr))` }}>
      <span aria-hidden className="absolute inset-y-0 left-0 rounded-full bg-fill transition-transform duration-(--dur-3) ease-(--ease-out)" style={{ width: `${100 / tabs.length}%`, transform: `translateX(${at * 100}%)` }} />
      {tabs.map((x) => (
        <button
          key={x}
          type="button"
          role="tab"
          aria-selected={x === value}
          onClick={() => onChange(x)}
          className={cn("g-press relative h-7 px-3 text-xs whitespace-nowrap transition-colors duration-(--dur-2) pointer-coarse:h-9", x === value ? "font-medium text-ink" : "text-ink-3 hover:text-ink")}
        >
          {t(TAB_KEY[x])}
        </button>
      ))}
    </div>
  );
}

function Part({ tab, wide }: { tab: ManageTab; wide: boolean }) {
  switch (tab) {
    case "accounts":
      return <Accounts wide={wide} />;
    case "apps":
      return <Apps wide={wide} />;
    case "computers":
      return <Computers wide={wide} />;
    case "agents":
      return <Agents wide={wide} />;
    case "roles":
      return <Roles wide={wide} />;
    case "secrets":
      return <Secrets />;
  }
}

// --- the frame of a part ---

/** A part's two halves. sel: the row chosen ("" none); on a wide window the first is chosen till
 *  another is. detail: the right half for it; foot: under the list (the agents' settings); add:
 *  the 新建 key's, or its own key. */
function Split({ wide, sel, first, onBack, list, detail, foot, add, needs }: { wide: boolean; sel: string; first?: string; onBack: () => void; list: ReactNode; detail: ReactNode; foot?: ReactNode; add?: ReactNode; needs?: ReactNode }) {
  const open = !!sel || (wide && !!first);
  if (wide)
    return (
      <>
        <div className="grid min-h-0 flex-1 grid-cols-[16rem_minmax(0,1fr)] gap-4">
          <div className="no-bar flex min-h-0 flex-col overflow-y-auto pt-1 pb-20 pl-2">
            <div className="flex flex-col">{list}</div>
            {foot && <div className="mx-2 mt-3 flex flex-col border-t border-edge pt-2">{foot}</div>}
          </div>
          <div className="no-bar min-h-0 overflow-y-auto pt-1 pr-5 pb-20">
            {needs}
            {open && detail}
          </div>
        </div>
        {add}
      </>
    );
  if (open)
    return (
      <div className="g-slide flex min-h-0 flex-1 flex-col">
        <div className="flex h-9 shrink-0 items-center px-1.5">
          <IconKey label={t("nav.back")} onClick={onBack} className="size-8">
            <ChevronLeft size={17} />
          </IconKey>
        </div>
        <div className="no-bar min-h-0 flex-1 overflow-y-auto px-4 pb-6">{detail}</div>
      </div>
    );
  return (
    <>
      <div className="no-bar min-h-0 flex-1 overflow-y-auto px-1.5 pt-1 pb-20" data-dir="back">
        {needs && <div className="px-1.5">{needs}</div>}
        <div className="flex flex-col">{list}</div>
        {foot && <div className="mx-1.5 mt-3 flex flex-col border-t border-edge pt-2">{foot}</div>}
      </div>
      {add}
    </>
  );
}

/** 新建, at the window's foot on the right. */
function AddKey({ label = t("manage.new"), onClick, on, disabled }: { label?: string; onClick: () => void; on?: boolean; disabled?: boolean }) {
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      aria-pressed={on}
      className={cn("g-press absolute right-4 bottom-4 z-10 flex h-9 items-center gap-1.5 rounded-full px-4 text-sm font-medium shadow-(--shadow-float) disabled:opacity-40 pointer-coarse:h-11", on ? "bg-fill-2 text-ink" : "bg-ink text-on-ink")}
    >
      {!on && <Plus size={15} />}
      {label}
    </button>
  );
}

/** A row of a list: its face, name and line; chosen, on a soft ground. */
function Row({ face, name, line, on, onClick, end }: { face: ReactNode; name: ReactNode; line?: ReactNode; on: boolean; onClick: () => void; end?: ReactNode }) {
  return (
    <button type="button" onClick={onClick} aria-current={on || undefined} className={cn("g-press flex min-h-13 w-full items-center gap-3 rounded-lg px-2 text-left", on ? "bg-fill" : "hover:bg-fill/60")}>
      <span className="grid shrink-0 place-items-center">{face}</span>
      <span className="min-w-0 flex-1">
        <span className="block truncate text-sm font-medium">{name}</span>
        {line && <span className="no-bar flex min-w-0 items-center gap-1.5 overflow-x-auto text-xs whitespace-nowrap text-ink-3 tabular-nums">{line}</span>}
      </span>
      {end}
    </button>
  );
}

/** Online, or when last seen. */
function Seen({ online, at }: { online: boolean; at: number }) {
  return online ? (
    <>
      <span className="size-1.5 shrink-0 rounded-full bg-run" aria-hidden />
      {t("term.online")}
    </>
  ) : (
    <>{at ? ago(at) : ""}</>
  );
}

/** The right half's head: the face and the name large, its figures on the same line at the right,
 *  its keys after them. */
function Head({ face, name, figures, keys }: { face: ReactNode; name: ReactNode; figures?: ReactNode; keys?: ReactNode }) {
  return (
    <div className="flex min-h-16 items-center gap-3 border-b border-edge pb-3">
      <span className="grid shrink-0 place-items-center">{face}</span>
      <span className="min-w-0 flex-1 truncate text-2xl font-semibold">{name}</span>
      {figures && <span className="flex shrink-0 items-center gap-3 text-xs text-ink-3 tabular-nums">{figures}</span>}
      {keys}
    </div>
  );
}

/** A setting's line: its name at the left, its value or control after it; pressed (onClick), a
 *  page of the right half. */
function Field({ label, children, onClick }: { label: ReactNode; children: ReactNode; onClick?: () => void }) {
  const body = (
    <>
      <span className="w-20 shrink-0 text-sm text-ink-3">{label}</span>
      <span className="flex min-w-0 flex-1 items-center gap-2 text-sm">{children}</span>
      {onClick && <ChevronRight size={15} className="shrink-0 text-ink-4" />}
    </>
  );
  return onClick ? (
    <button type="button" onClick={onClick} className="g-press -mx-2 flex min-h-11 items-center gap-3 rounded-md px-2 text-left hover:bg-fill/60">
      {body}
    </button>
  ) : (
    <div className="flex min-h-11 items-center gap-3">{body}</div>
  );
}

/** A block of fields, a hairline above it. */
function Block({ title, children }: { title?: string; children: ReactNode }) {
  return (
    <section className="flex flex-col border-b border-edge py-2 last:border-b-0">
      {title && <span className="pt-1 text-[11px] leading-4 text-ink-4">{title}</span>}
      {children}
    </section>
  );
}

/** A page of the right half: its title after the way back, the list left as it is. */
function Sub({ title, tag, onBack, right, children }: { title: string; tag?: string; onBack: () => void; right?: ReactNode; children: ReactNode }) {
  return (
    <div className="g-slide flex flex-col">
      <div className="flex min-h-12 items-center gap-1 border-b border-edge pb-2">
        <IconKey label={t("nav.back")} onClick={onBack} className="-ml-1.5 size-7">
          <ChevronLeft size={16} />
        </IconKey>
        <span className="truncate text-base font-semibold">{title}</span>
        {tag && <span className="truncate text-sm text-ink-3">{tag}</span>}
        <span className="flex-1" />
        {right}
      </div>
      <div className="flex flex-col pt-2">{children}</div>
    </div>
  );
}

/** What waits for a person in a part: its needs (a pairing, a grant, a login's approval) as cards. */
function Needs({ tab }: { tab: ManageTab }) {
  const needs = useConfirms().filter((c) => needTab(c.kind) === tab);
  if (!needs.length) return null;
  return (
    <div className="flex flex-col gap-2 pb-3">
      {needs.map((c) => (
        <ConfirmCard key={c.id} c={c} at="terminals" />
      ))}
    </div>
  );
}

// --- who may use what ---

/** The agents a permission list offers by name; others it was given show too. */
const AGENTS: Agent[] = ["claude", "codex", "dsh", "pi", "agy"];
const ALL = "*";
const permsOf = (p?: Perms): Perms => p ?? { agents: [ALL], scope: [ALL] };

/** An agent as a key: its mark and name, a dot after them filled when it is on (the usage's). */
function AgentDot({ a, on, onClick }: { a: string; on: boolean; onClick?: () => void }) {
  const body = (
    <>
      <span className={cn("grid", !on && "opacity-40 grayscale")}>
        <Logo agent={a as Agent} size={14} />
      </span>
      <span className={on ? "text-ink" : "text-ink-3"}>{agentName(a as Agent)}</span>
      <span aria-hidden className="block size-2 shrink-0 rounded-full" style={{ color: "var(--ink-2)", background: on ? "var(--ink-2)" : "transparent", boxShadow: "inset 0 0 0 1.5px currentColor" }} />
    </>
  );
  return onClick ? (
    <button type="button" aria-pressed={on} onClick={onClick} className="g-press -mx-1 flex h-7 items-center gap-1.5 rounded-full px-1.5 text-sm hover:bg-fill">
      {body}
    </button>
  ) : (
    <span className="flex h-7 items-center gap-1.5 text-sm">{body}</span>
  );
}

/** Agent and 目录 of a login or an app: an agent's dot pressed turns it on or off, saved at once;
 *  the folders open under their line, all or some, each added or taken off saved at once. */
function Access({ perms, save }: { perms?: Perms; save: (p: Perms) => Promise<Perms> }) {
  const qc = useQueryClient();
  const was = permsOf(perms);
  const [p, setP] = useState<Perms>(was);
  const said = JSON.stringify(was);
  const [seen, setSeen] = useState(said);
  if (said !== seen) (setSeen(said), setP(was));
  const [folders, setFolders] = useState(false);
  const [place, setPlace] = useState("");
  const put = async (next: Perms) => {
    const before = p;
    setP(next);
    try {
      setP(await save(next));
      void qc.invalidateQueries({ queryKey: ["apps"] });
    } catch (e) {
      setP(before);
      toast(t("apps.perms"), { error: true, description: errText(e) });
    }
  };
  const all = p.agents.includes(ALL);
  const isOn = (a: string) => all || p.agents.includes(a);
  const shown = [...new Set([...AGENTS, ...p.agents.filter((a) => a !== ALL)])];
  const flip = (a: string) => {
    const on = isOn(a);
    const now = all ? shown : p.agents;
    let agents = on ? now.filter((x) => x !== a) : [...now, a];
    if (shown.every((x) => agents.includes(x))) agents = [ALL];
    void put({ ...p, agents });
  };
  const allScope = p.scope.includes(ALL);
  const addPlace = () => {
    const v = place.trim();
    if (v && !p.scope.includes(v)) void put({ ...p, scope: [...p.scope.filter((x) => x !== ALL), v] });
    setPlace("");
  };
  return (
    <>
      <Field label={t("manage.agent")}>
        <span className="flex flex-wrap items-center gap-x-3">
          {shown.map((a) => (
            <AgentDot key={a} a={a} on={isOn(a)} onClick={() => flip(a)} />
          ))}
        </span>
      </Field>
      <Field label={t("manage.folders")} onClick={() => setFolders(!folders)}>
        <span className="truncate">{allScope ? t("manage.all") : p.scope.length ? p.scope.join("、") : t("apps.scopeNone")}</span>
      </Field>
      {folders && (
        <div className="g-fade flex flex-col gap-1 pb-2 pl-23">
          <div className="flex min-h-9 items-center gap-2">
            <span className="min-w-0 flex-1 text-sm">{t("apps.allScope")}</span>
            <Switch label={t("apps.allScope")} on={allScope} onChange={(on) => void put({ ...p, scope: on ? [ALL] : [] })} />
          </div>
          {!allScope &&
            p.scope.map((s) => (
              <div key={s} className="flex min-h-8 items-center gap-2">
                <code className="min-w-0 flex-1 truncate font-mono text-xs">{s}</code>
                <IconKey label={t("apps.scopeRemove")} onClick={() => void put({ ...p, scope: p.scope.filter((x) => x !== s) })} className="size-6">
                  <X size={13} />
                </IconKey>
              </div>
            ))}
          {!allScope && (
            <form
              className="flex gap-1.5"
              onSubmit={(e) => {
                e.preventDefault();
                addPlace();
              }}
            >
              <input value={place} onChange={(e) => setPlace(e.target.value)} placeholder={t("apps.scopePlaceholder")} aria-label={t("apps.scopeAdd")} className="no-ring h-8 min-w-0 flex-1 rounded-md bg-fill px-2.5 font-mono text-xs placeholder:font-sans placeholder:text-ink-3" />
              <SecondKey type="submit" disabled={!place.trim()}>
                {t("apps.scopeAdd")}
              </SecondKey>
            </form>
          )}
        </div>
      )}
    </>
  );
}

/** A name in the head, its pencil after it renaming it in place. */
function Name({ id, name, fixed }: { id: string; name: string; fixed?: boolean }) {
  const [renaming, setRenaming] = useState(false);
  if (renaming) return <Rename id={id} name={name} done={() => setRenaming(false)} />;
  return (
    <span className="group flex min-w-0 items-center gap-1.5">
      <span className="truncate">{name}</span>
      {!fixed && (
        <IconKey label={t("term.renameIt")} onClick={() => setRenaming(true)} className="size-7 text-base opacity-0 group-hover:opacity-100 focus-visible:opacity-100 pointer-coarse:opacity-100">
          <PencilLine size={14} />
        </IconKey>
      )}
    </span>
  );
}

// --- 账号 ---

function Accounts({ wide }: { wide: boolean }) {
  const data = useApps().data;
  const me = useMe().data;
  const [sel, setSel] = useState("");
  const logins = (data?.logins ?? []).filter(ofWeb);
  const invites = data?.invites ?? [];
  const first = logins.find((l) => l.id === me?.login)?.id ?? logins[0]?.id;
  const at = sel || (wide ? (first ?? "") : "");
  const login = logins.find((l) => l.id === at);
  const invite = invites.find((i) => i.id === at);
  // One removed (here or elsewhere): back to the first.
  if (sel && sel !== NEW && data && !login && !invite) setSel("");
  const list = (
    <>
      {at === NEW && <Row face={<Pending />} name={t("manage.newInvite")} line={t("manage.notMade")} on onClick={() => {}} />}
      {logins.map((l) => (
        <Row key={l.id} face={<Avatar src={l.avatar} name={l.name} size={32} {...deviceBadge(l.device)} />} name={l.name} line={<><Seen online={l.online} at={l.lastSeenAt || l.createdAt || 0} />{!l.approvedAt && <span>· {t("apps.pending")}</span>}</>} on={l.id === at} onClick={() => setSel(l.id)} />
      ))}
      {invites.map((i) => (
        <Row key={i.id} face={<Pending />} name={i.setup ? t("term.setup") : i.name || t("manage.unusedInvite")} line={i.expiresAt ? t("term.until", { time: clock(i.expiresAt) }) : undefined} on={i.id === at} onClick={() => setSel(i.id)} />
      ))}
    </>
  );
  const detail =
    at === NEW ? (
      <NewInvite done={(id) => setSel(id ?? "")} />
    ) : login ? (
      <LoginDetail key={login.id} l={login} />
    ) : invite ? (
      <InviteDetail key={invite.id} i={invite} gone={() => setSel("")} />
    ) : null;
  return (
    <Split
      wide={wide}
      sel={sel}
      first={first}
      onBack={() => setSel("")}
      list={list}
      detail={detail}
      needs={<Needs tab="accounts" />}
      add={at !== NEW && <AddKey onClick={() => setSel(NEW)} disabled={!me?.public} label={me?.public ? t("manage.new") : t("term.needPublic")} />}
    />
  );
}

/** An invite's face: a link's mark in a faint circle. */
function Pending() {
  return (
    <span className="grid size-8 place-items-center rounded-full bg-fill text-ink-3">
      <Link2 size={14} />
    </span>
  );
}

function NewInvite({ done }: { done: (id?: string) => void }) {
  const qc = useQueryClient();
  const me = useMe().data;
  const [name, setName] = useState("");
  const [withClaude, setWithClaude] = useState(false);
  const [busy, setBusy] = useState(false);
  const canClaude = saysOwner(me);
  const make = async () => {
    setBusy(true);
    try {
      const inv = await api.invite(name.trim(), canClaude && withClaude);
      showSecret({ invite: inv, at: HERE });
      await qc.invalidateQueries({ queryKey: ["apps"] });
      done(inv.id);
    } catch (e) {
      toast(t("err.invite"), { error: true, description: nameError(e) });
      setBusy(false);
    }
  };
  return (
    <form
      className="flex flex-col"
      onSubmit={(e) => {
        e.preventDefault();
        void make();
      }}
    >
      <Head face={<Pending />} name={t("manage.newInvite")} />
      <Block>
        <Field label={t("term.name")}>
          <input autoFocus value={name} maxLength={12} onChange={(e) => setName(e.target.value)} placeholder={t("manage.nameLater")} aria-label={t("term.name")} className="no-ring h-9 w-full min-w-0 rounded-md bg-fill px-2.5 text-sm placeholder:text-ink-3" />
        </Field>
        {canClaude && (
          <Field label={t("term.withClaude")}>
            <Switch label={t("term.withClaude")} on={withClaude} onChange={setWithClaude} />
          </Field>
        )}
      </Block>
      <div className="flex justify-end gap-2 pt-3">
        <SecondKey onClick={() => done()}>{t("me.cancel")}</SecondKey>
        <MainKey type="submit" busy={busy}>
          {t("manage.makeLink")}
        </MainKey>
      </div>
    </form>
  );
}

function InviteDetail({ i, gone }: { i: PendingInvite; gone: () => void }) {
  const qc = useQueryClient();
  const name = i.setup ? t("term.setup") : i.name || t("manage.unusedInvite");
  return (
    <div className="flex flex-col">
      <Head
        face={<Pending />}
        name={name}
        figures={i.expiresAt ? <span>{t("term.until", { time: clock(i.expiresAt) })}</span> : undefined}
        keys={
          <SureKey label={t("term.revoke")} sure={t("term.revokeSure")} run={() => void revoke(qc, i.id, name).then(gone)} className="size-8">
            <Trash2 size={15} />
          </SureKey>
        }
      />
      <div className="@container pt-3">
        <SecretHere at={HERE} />
      </div>
      {i.claude && (
        <Block>
          <Field label={t("term.withClaude")}>{t("manage.yes")}</Field>
        </Block>
      )}
    </div>
  );
}

function LoginDetail({ l }: { l: Login }) {
  const qc = useQueryClient();
  const me = useMe().data;
  const apps = useApps().data?.apps ?? [];
  const gh = useQuery(githubQ).data?.bindings.find((b) => b.member === l.id);
  const [sub, setSub] = useState<"" | "passkeys" | "github">("");
  const face = useFacePick((b) => api.profile({ avatar: b }, l.id).then(() => qc.invalidateQueries({ queryKey: ["apps"] })));
  const mine = l.id === me?.login;
  const computers = apps.filter((a) => a.owner?.id === l.id);
  if (sub === "passkeys")
    return (
      <div className="g-slide -mx-3">
        <PasskeysPage login={l} onBack={() => setSub("")} />
      </div>
    );
  if (sub === "github")
    return (
      <Sub title="GitHub" tag={l.name} onBack={() => setSub("")}>
        <div className="max-w-sm pt-2">{gh ? <Bound member={l.id} mine={mine} done={() => {}} /> : <Bind done={() => setSub("")} />}</div>
      </Sub>
    );
  return (
    <div className="flex flex-col">
      <Head
        face={<AvatarKey label={t("term.avatar")} onClick={face.open} src={l.avatar} name={l.name} size={40} {...deviceBadge(l.device)} />}
        name={<Name id={l.id} name={l.name} />}
        figures={
          <>
            {mine && <span>{t("term.this")}</span>}
            {!l.approvedAt && <span>{t("apps.pending")}</span>}
            <span className="flex items-center gap-1.5"><Seen online={l.online} at={l.lastSeenAt || l.createdAt || 0} /></span>
          </>
        }
        keys={
          <SureKey label={t("apps.removeLogin")} sure={t("term.revokeSure")} run={() => void revoke(qc, l.id, l.name)} className="size-8">
            <Trash2 size={15} />
          </SureKey>
        }
      />
      {face.crop}
      {face.input}
      <Block>
        <Access perms={l.perms} save={(p) => api.loginPerms("ir_web", l.id, p)} />
      </Block>
      <Block>
        {me?.public && (
          <Field label={t("manage.passkeys")} onClick={() => setSub("passkeys")}>
            {l.passkeys ? t("manage.count", { n: l.passkeys }) : <span className="text-ink-3">{t("manage.none")}</span>}
          </Field>
        )}
        {(gh || mine) && (
          <Field label="GitHub" onClick={() => setSub("github")}>
            {gh ? (
              <span className="flex items-center gap-1.5">
                <GitHubMark size={14} />@{gh.login}
              </span>
            ) : (
              <span className="text-ink-3">{t("manage.unbound")}</span>
            )}
          </Field>
        )}
        {computers.length > 0 && (
          <Field label={t("manage.computers")}>
            <span className="flex flex-wrap gap-x-3">
              {computers.map((c) => (
                <span key={c.id} className="flex items-center gap-1">
                  <Monitor size={13} className="text-ink-4" aria-hidden />
                  {c.name}
                </span>
              ))}
            </span>
          </Field>
        )}
      </Block>
    </div>
  );
}

const githubQ = { queryKey: ["github"], queryFn: () => api.github(), staleTime: 60_000 };

// --- 应用 ---

function Apps({ wide }: { wide: boolean }) {
  const data = useApps().data;
  const pairing = !!usePairing()?.open;
  const [sel, setSel] = useState("");
  const [busy, setBusy] = useState(false);
  const apps = (data?.apps ?? []).filter((a) => !isComputer(a) && !a.web && !a.host);
  const first = apps[0]?.id;
  const at = sel || (wide ? (first ?? "") : "");
  const app = apps.find((a) => a.id === at);
  if (sel && data && !app) setSel("");
  const logins = (id: string) => (data?.logins ?? []).filter((l) => l.app === id);
  const pair = async () => {
    setBusy(true);
    try {
      await api.pairing(!pairing);
    } catch (e) {
      toast(t("apps.pairing"), { error: true, description: errText(e) });
    } finally {
      setBusy(false);
    }
  };
  const list = (
    <>
      {data && !apps.length && <p className="m-0 px-2 py-3 text-sm text-ink-3">{t("manage.noApps")}</p>}
      {apps.map((a) => {
        const n = logins(a.id).length;
        return <Row key={a.id} face={<Avatar src={a.avatar} name={a.name} size={32} type={a.type} />} name={a.name} line={<><Seen online={a.online} at={a.lastSeenAt || a.createdAt || 0} />{n > 1 && <span>· {t("apps.loginCount", { n })}</span>}</>} on={a.id === at} onClick={() => setSel(a.id)} />;
      })}
    </>
  );
  return (
    <Split
      wide={wide}
      sel={sel}
      first={first}
      onBack={() => setSel("")}
      list={list}
      detail={app && <AppDetail key={app.id} a={app} logins={logins(app.id)} />}
      needs={<Needs tab="apps" />}
      add={<AddKey onClick={() => void pair()} on={pairing} disabled={busy} label={pairing ? t("manage.pairing") : t("manage.pair")} />}
    />
  );
}

function AppDetail({ a, logins }: { a: App; logins: Login[] }) {
  const qc = useQueryClient();
  const [sub, setSub] = useState("");
  const face = useFacePick((b) => api.profile({ avatar: b }, a.id).then(() => qc.invalidateQueries({ queryKey: ["apps"] })));
  const login = logins.find((l) => l.id === sub);
  if (login)
    return (
      <Sub
        title={login.name}
        tag={a.name}
        onBack={() => setSub("")}
        right={
          <SureKey label={t("apps.removeLogin")} sure={t("term.revokeSure")} run={() => void revoke(qc, login.id, login.name).then(() => setSub(""))} className="size-8">
            <Trash2 size={15} />
          </SureKey>
        }
      >
        <Access perms={login.perms} save={(p) => api.loginPerms(a.id, login.id, p)} />
      </Sub>
    );
  return (
    <div className="flex flex-col">
      <Head
        face={<AvatarKey label={t("term.avatar")} onClick={face.open} src={a.avatar} name={a.name} size={40} type={a.type} />}
        name={<Name id={a.id} name={a.name} />}
        figures={
          <span className="flex items-center gap-1.5">
            <Seen online={a.online} at={a.lastSeenAt || a.createdAt || 0} />
          </span>
        }
        keys={
          <SureKey label={t("apps.delete")} sure={t("term.revokeSure")} run={() => void revoke(qc, a.id, a.name)} className="size-8">
            <Trash2 size={15} />
          </SureKey>
        }
      />
      {face.crop}
      {face.input}
      <Block>
        <Access perms={a.perms} save={(p) => api.appPerms(a.id, p)} />
      </Block>
      {logins.length > 0 && (
        <Block title={t("manage.logins")}>
          {logins.map((l) => (
            <button key={l.id} type="button" onClick={() => setSub(l.id)} className="g-press -mx-2 flex min-h-11 items-center gap-3 rounded-md px-2 text-left hover:bg-fill/60">
              <Avatar src={l.avatar} name={l.name} size={24} />
              <span className="min-w-0 flex-1 truncate text-sm">{l.name}</span>
              <span className="flex shrink-0 items-center gap-1.5 text-xs text-ink-3 tabular-nums">
                <Seen online={l.online} at={l.lastSeenAt || l.createdAt || 0} />
              </span>
              <ChevronRight size={15} className="shrink-0 text-ink-4" />
            </button>
          ))}
        </Block>
      )}
    </div>
  );
}

// --- 电脑 ---

/** The computers, the host first, those on one machine next to each other. */
function useComputers(): App[] {
  const apps = useApps().data?.apps ?? [];
  const host = apps.filter((a) => a.host);
  const tops = apps.filter((a) => !a.host && isComputer(a) && !a.computer?.under);
  return [...host, ...tops.flatMap((c) => [c, ...apps.filter((a) => a.computer?.under === c.id)])];
}

const osName = (m?: Computer) => (m?.os ? (systems[m.os] ?? m.os) : "");

function ComputerFace({ a, size }: { a: App; size: number }) {
  return a.host ? <ServerFace size={size} /> : <Avatar src={a.avatar} name={a.name} size={size} type={a.type} />;
}

function Computers({ wide }: { wide: boolean }) {
  const computers = useComputers();
  const [sel, setSel] = useState("");
  const first = computers[0]?.id;
  const at = sel || (wide ? (first ?? "") : "");
  const c = computers.find((a) => a.id === at);
  if (sel && sel !== NEW && computers.length && !c) setSel("");
  const list = (
    <>
      {at === NEW && (
        <Row
          face={
            <span className="grid size-8 place-items-center rounded-full bg-fill text-ink-3">
              <Plus size={15} />
            </span>
          }
          name={t("manage.newComputer")}
          line={t("manage.waitJoin")}
          on
          onClick={() => {}}
        />
      )}
      {computers.map((a) => (
        <Row key={a.id} face={<ComputerFace a={a} size={32} />} name={a.host ? "Iridium" : a.name} line={<>{!a.host && <Seen online={a.online} at={Math.max(a.computer?.seenAt ?? 0, a.lastSeenAt ?? 0) || a.createdAt || 0} />}<span>{osName(a.computer)}</span></>} on={a.id === at} onClick={() => setSel(a.id)} />
      ))}
    </>
  );
  return (
    <Split
      wide={wide}
      sel={sel}
      first={first}
      onBack={() => setSel("")}
      list={list}
      detail={
        at === NEW ? (
          <div className="-mx-3">
            <AddComputer onBack={() => setSel("")} />
          </div>
        ) : (
          c && <ComputerDetail key={c.id} a={c} />
        )
      }
      needs={<Needs tab="computers" />}
      add={at !== NEW && <AddKey onClick={() => setSel(NEW)} />}
    />
  );
}

function ComputerDetail({ a }: { a: App }) {
  const qc = useQueryClient();
  const me = useMe().data;
  const owner = useApps().data?.logins.find((l) => ofWeb(l) && l.id === a.owner?.id);
  const gh = useQuery(githubQ).data?.bindings.find((b) => b.member === "local");
  const grant = useGrant(a);
  const [folders, setFolders] = useState(false);
  const [sub, setSub] = useState<"" | "github">("");
  const face = useFacePick((b) => api.profile({ avatar: b }, a.id).then(() => qc.invalidateQueries({ queryKey: ["apps"] })));
  const hostFace = useFacePick((b) => api.serverFace(b), t("apps.serverFace"));
  const m = a.computer;
  const may = !a.host && !!m?.grant && a.online;
  if (sub === "github")
    return (
      <Sub title="GitHub" tag="Iridium" onBack={() => setSub("")}>
        <div className="max-w-sm pt-2">{gh ? <Bound member="local" mine={!!me?.host} done={() => {}} /> : <Bind done={() => setSub("")} />}</div>
      </Sub>
    );
  return (
    <div className="flex flex-col">
      <Head
        face={
          a.host ? (
            <Tip label={t("apps.serverFace")}>
              <button type="button" aria-label={t("apps.serverFace")} onClick={hostFace.open} className="group g-press grid shrink-0 rounded-full">
                <ServerFace size={40} over={<FaceChangeOver size={40} />} />
              </button>
            </Tip>
          ) : (
            <AvatarKey label={t("term.avatar")} onClick={face.open} src={a.avatar} name={a.name} size={40} type={a.type} />
          )
        }
        name={a.host ? "Iridium" : <Name id={a.id} name={a.name} />}
        figures={
          <>
            {a.id === me?.app && !a.host && <span>{t("term.this")}</span>}
            {!a.host && (
              <span className="flex items-center gap-1.5">
                <Seen online={a.online} at={Math.max(m?.seenAt ?? 0, a.lastSeenAt ?? 0) || a.createdAt || 0} />
              </span>
            )}
            {m?.latency ? <span>{m.latency} ms</span> : null}
            <span>{osName(m)}</span>
          </>
        }
        keys={
          !a.host && (
            <SureKey label={t("apps.deleteComputer")} sure={t("term.revokeSure")} run={() => void revoke(qc, a.id, a.name)} className="size-8">
              <Trash2 size={15} />
            </SureKey>
          )
        }
      />
      {face.crop}
      {hostFace.crop}
      {face.input}
      {hostFace.input}
      <Block>
        {m && (
          <Field label={t("manage.agent")}>
            <span className="flex flex-wrap items-center gap-x-3">
              {m.agents.map((x) => (
                <Tip key={x.agent} label={x.version ? `${agentName(x.agent as Agent)} ${x.version}` : agentName(x.agent as Agent)}>
                  <span>
                    <AgentDot a={x.agent} on={!x.off} />
                  </span>
                </Tip>
              ))}
            </span>
          </Field>
        )}
        {!a.host && m && (
          <Field label={t("apps.shell")}>
            {may ? <Switch label={t("apps.shell")} on={!!m.shell} onChange={(on) => void grant({ shell: on })} /> : <span className={m.shell ? "" : "text-ink-3"}>{m.shell ? t("manage.on") : t("manage.off")}</span>}
          </Field>
        )}
        {!a.host && m && (
          <Field label={t("manage.folders")}>
            <span className="min-w-0 flex-1 truncate">{m.roots?.length ? m.roots.join("、") : t("grant.allFolders")}</span>
            {may && (
              <IconKey label={t("grant.addFolder")} onClick={() => setFolders(true)} className="size-7">
                <FolderOpen size={14} />
              </IconKey>
            )}
          </Field>
        )}
        {folders && m && <OpenFolder a={a} m={m} grant={grant} done={() => setFolders(false)} />}
      </Block>
      <Block>
        {!a.host && m && (
          <Field label={t("manage.owner")}>
            <span className="min-w-0 flex-1 truncate">{a.owner ? (owner?.name ?? a.owner.name) : <span className="text-ink-3">{t("manage.none")}</span>}</span>
            {me?.web && me.login && (
              <SecondKey onClick={() => void setOwner(qc, a.id, a.owner?.id === me.login ? { clear: true } : {})}>{a.owner?.id === me.login ? t("apps.notMine") : t("apps.mine")}</SecondKey>
            )}
          </Field>
        )}
        {a.host && (gh || me?.host) && (
          <Field label="GitHub" onClick={() => setSub("github")}>
            {gh ? (
              <span className="flex items-center gap-1.5">
                <GitHubMark size={14} />@{gh.login}
              </span>
            ) : (
              <span className="text-ink-3">{t("manage.unbound")}</span>
            )}
          </Field>
        )}
      </Block>
    </div>
  );
}

// --- Agent ---

/** How long an idle agent is kept before it is stopped ("0" never). */
const IDLE = ["0", "30m", "1h", "4h"] as const;
const idleName = (v: string) => (v === "0" ? t("manage.off") : v === "30m" ? t("manage.minutes", { n: 30 }) : v.endsWith("h") ? t("manage.hours", { n: Number(v.slice(0, -1)) }) : v);

function Agents({ wide }: { wide: boolean }) {
  const me = useMe().data;
  const infos = (useAgents().data ?? []).filter((x) => !x.missing);
  const computers = useComputers();
  const s = useSettings().data;
  const qc = useQueryClient();
  const [sel, setSel] = useState("");
  const first = infos[0]?.agent;
  const at = sel || (wide ? (first ?? "") : "");
  const info = infos.find((x) => x.agent === at);
  const at_ = (a: string) => computers.filter((c) => c.computer?.agents.some((x) => x.agent === a && !x.off));
  const idle = s?.idleReap ?? "1h";
  const setIdle = (v: string) =>
    void api.putSettings({ idleReap: v }).then(
      (got) => qc.setQueryData(["settings"], got),
      (e) => toast(t("manage.idle"), { error: true, description: errText(e) }),
    );
  const list = infos.map((x) => {
    const n = at_(x.agent).length;
    return (
      <Row
        key={x.agent}
        face={
          <span className="grid size-8 place-items-center rounded-full bg-fill">
            <Logo agent={x.agent} size={16} />
          </span>
        }
        name={agentName(x.agent)}
        line={n ? t("manage.computerCount", { n }) : undefined}
        on={x.agent === at}
        onClick={() => setSel(x.agent)}
      />
    );
  });
  const foot = me?.host && s && (
    <div className="flex flex-col gap-1.5 px-2 py-1">
      <span className="text-sm text-ink-3">{t("manage.idle")}</span>
      <Pills label={t("manage.idle")} value={idle} onChange={setIdle} options={[...new Set([...IDLE, idle])].map((v) => ({ value: v, label: idleName(v) }))} />
    </div>
  );
  return <Split wide={wide} sel={sel} first={first} onBack={() => setSel("")} list={list} foot={foot} detail={info && <AgentDetail key={info.agent} a={info.agent} computers={computers} />} />;
}

function AgentDetail({ a, computers }: { a: Agent; computers: App[] }) {
  const me = useMe().data;
  const info = useAgents().data?.find((x) => x.agent === a);
  const s = useSettings().data;
  const qc = useQueryClient();
  const host = computers.find((c) => c.host)?.computer?.agents.find((x) => x.agent === a);
  const there = computers.filter((c) => c.computer?.agents.some((x) => x.agent === a));
  const mode = s?.modes?.[a] ?? "";
  const setMode = (v: string) =>
    void api.putSettings({ modes: { [a]: v } }).then(
      (got) => qc.setQueryData(["settings"], got),
      (e) => toast(t("manage.approval"), { error: true, description: errText(e) }),
    );
  return (
    <div className="flex flex-col">
      <Head
        face={
          <span className="grid size-10 place-items-center rounded-full bg-fill">
            <Logo agent={a} size={20} />
          </span>
        }
        name={agentName(a)}
        figures={host?.version ? <span>{host.version}</span> : undefined}
      />
      {me?.host && s && info && info.modes.length > 1 && (
        <Block>
          <Field label={t("manage.approval")}>
            <Pills label={t("manage.approval")} value={mode} onChange={setMode} options={[{ value: "", label: t("manage.default") }, ...info.modes.map((m) => ({ value: m, label: modeName({ id: m }) }))]} />
          </Field>
        </Block>
      )}
      {there.length > 0 && (
        <Block title={t("manage.computers")}>
          {there.map((c) => {
            const x = c.computer!.agents.find((y) => y.agent === a)!;
            return (
              <div key={c.id} className="flex min-h-11 items-center gap-3">
                <ComputerFace a={c} size={24} />
                <span className={cn("min-w-0 flex-1 truncate text-sm", !c.host && !c.online && "text-ink-3")}>{c.host ? "Iridium" : c.name}</span>
                <span className="flex shrink-0 items-center gap-1.5 text-xs text-ink-3 tabular-nums">
                  {!c.host && <Seen online={c.online} at={Math.max(c.computer?.seenAt ?? 0, c.lastSeenAt ?? 0)} />}
                  {x.version && <span>{x.version}</span>}
                  <span>{osName(c.computer)}</span>
                </span>
                <span aria-label={x.off ? t("manage.off") : t("manage.on")} className="block size-2.5 shrink-0 rounded-full" style={{ color: "var(--ink-2)", background: x.off ? "transparent" : "var(--ink-2)", boxShadow: "inset 0 0 0 1.5px currentColor" }} />
              </div>
            );
          })}
        </Block>
      )}
    </div>
  );
}

// --- 角色 ---

function Roles({ wide }: { wide: boolean }) {
  const roles = useRoles();
  const [sel, setSel] = useState("");
  const first = roles[0]?.id;
  const at = sel || (wide ? (first ?? "") : "");
  const role = roles.find((r) => r.id === at);
  if (sel && sel !== NEW && !role) setSel("");
  const list = (
    <>
      {at === NEW && (
        <Row
          face={
            <span className="grid size-8 place-items-center rounded-full bg-fill text-ink-3">
              <Plus size={15} />
            </span>
          }
          name={t("role.new")}
          on
          onClick={() => {}}
        />
      )}
      {!roles.length && at !== NEW && <p className="m-0 px-2 py-3 text-sm text-ink-3">{t("role.none")}</p>}
      {roles.map((r) => (
        <Row key={r.id} face={<RoleFace role={r} size={32} />} name={r.name} line={<RoleLine role={r} at={placeOf(r)} className="truncate" />} on={r.id === at} onClick={() => setSel(r.id)} />
      ))}
    </>
  );
  // The role's own page, as it was in the settings, its way back the list's.
  const detail =
    at === NEW ? (
      <div className="-mx-3">
        <NewRolePage made={(r: Role) => setSel(r.id)} />
      </div>
    ) : (
      role && (
        <div className="-mx-3">
          <RolePage key={role.id} role={role} />
        </div>
      )
    );
  return <Split wide={wide} sel={sel} first={first} onBack={() => setSel("")} list={list} detail={detail} add={at !== NEW && <AddKey onClick={() => setSel(NEW)} />} />;
}

// --- 凭据 ---

function Secrets() {
  const [adding, setAdding] = useState(false);
  return (
    <>
      <div className="no-bar min-h-0 flex-1 overflow-y-auto px-4 pt-2 pb-20">
        <div className="mx-auto max-w-2xl">
          <SecretsPane adding={adding} done={() => setAdding(false)} />
        </div>
      </div>
      <AddKey onClick={() => setAdding(!adding)} on={adding} label={adding ? t("me.cancel") : t("manage.new")} />
    </>
  );
}
