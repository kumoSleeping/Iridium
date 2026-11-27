// 管理 (kumo, 2026-11-27): one window over the page, drawn as the usage is (usage.tsx). No parts
// along its top (kumo: too many): one list at the left in three groups, 成员 (web logins, apps,
// unused invites), 电脑 (Iridium first) and Agent; the one chosen at the right. What belongs to one
// of them is in its detail: an agent's roles under it, the credentials and the idle stop under
// Iridium. A page of the right half (通行密钥, GitHub, a role, 凭据) keeps the list where it is; a
// narrow window shows one half at a time, the chosen one sliding in with its way back. 新建 is one
// key at the foot's right: a menu of what may be made, which comes as a row at the top of its group
// till it is made. Changed is saved at once.
import { Dialog } from "@base-ui/react/dialog";
import { Menu } from "@base-ui/react/menu";
import { useQueryClient } from "@tanstack/react-query";
import { ChevronLeft, ChevronRight, FolderOpen, KeyRound, Link2, LoaderCircle, Monitor, PencilLine, Plus, UserPlus, Waypoints, X, Trash2, Bot } from "lucide-react";
import { useLayoutEffect, useState, type ReactNode } from "react";
import { api } from "./api/client";
import type { Agent, App, Computer, Login, Perms, PendingInvite, Role } from "./api/types.gen";
import { AddComputer, clock, deviceBadge, nameError, ofWeb, OpenFolder, PasskeysPage, Rename, revoke, setOwner, systems, useFacePick, useGrant } from "./apps";
import { MainKey, SecondKey } from "./card";
import { ConfirmCard, saysOwner } from "./confirms";
import { SecretsPane, secretsQ } from "./credentials";
import { isComputer, manages as managesApps, useAgents, useApps, useMe, useSettings } from "./data";
import { Bind, Bound, GitHubMark } from "./github";
import { t, useLang } from "./i18n";
import { modeName } from "./modes";
import { Pills } from "./pills";
import { RoleFace, RoleLine, useRoles } from "./roles";
import { NewRolePage, placeOf, RolePage } from "./rolespage";
import { type ManageTab } from "./route";
import { SecretHere } from "./secret";
import { showSecret } from "./secrets";
import { MenuItems } from "./sidebar";
import { useConfirms, usePairing } from "./store";
import { errText, toast } from "./toast";
import { agentName, ago, Avatar, AvatarKey, cn, FaceChangeOver, IconKey, Logo, MenuPortal, ServerFace, SureKey, Switch, Tip } from "./ui";
import { useQuery } from "./api/query";

/** Where a secret made here (an invite's link) shows. */
const HERE = "manage";
/** Two halves from this width of the window on (the usage's). */
const WIDE = 640;

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

/** What is chosen: a row of the list ("l:" a login, "i:" an invite, "p:" an app, "c:" a computer,
 *  "a:" an agent), a page under one ("r:" a role, SECRETS), or a row being made (NEW_*). */
const SECRETS = "secrets";
const NEW_INVITE = "new:invite";
const NEW_COMPUTER = "new:computer";
const NEW_ROLE = "new:role";

export function ManagePage({ tab }: { tab: ManageTab }) {
  useLang();
  const [root, wide] = useWide();
  const q = useApps();
  const data = q.data;
  const me = useMe().data;
  const manages = managesApps(me);
  const infos = (useAgents().data ?? []).filter((x) => !x.missing);
  const roles = useRoles();
  const pairing = !!usePairing()?.open;
  const all = useComputers();
  const computers = manages ? all : [];
  const host = computers.find((a) => a.host);
  const logins = manages ? (data?.logins ?? []).filter(ofWeb) : [];
  const apps = manages ? (data?.apps ?? []).filter((a) => !isComputer(a) && !a.web && !a.host) : [];
  const invites = manages ? (data?.invites ?? []) : [];
  const appLogins = (id: string) => (data?.logins ?? []).filter((l) => l.app === id);
  // The row a part of the old routes (#/settings/apps …, a need's) stands for.
  const firstOf = (x: ManageTab): string => {
    const mine = logins.find((l) => l.id === me?.login) ?? logins[0];
    if (x === "apps" && apps[0]) return `p:${apps[0].id}`;
    if (x === "computers" && host) return `c:${host.id}`;
    if (x === "secrets" && me?.host) return SECRETS;
    if ((x === "agents" || x === "roles" || !mine) && infos[0]) return `a:${infos[0].agent}`;
    return mine ? `l:${mine.id}` : "";
  };
  // Nothing chosen yet: the row the route stands for, worked out as the data comes (an old route's
  // part opens its row on a narrow window too, till the way back is taken).
  const [sel, setSel] = useState("");
  const [asked, setAsked] = useState(tab !== "accounts");
  const [was, setWas] = useState(tab);
  if (tab !== was) (setWas(tab), setSel(""), setAsked(true));
  const at = sel || (wide || asked ? firstOf(tab) : "");
  const [k, id] = at.startsWith("new:") || at === SECRETS ? [at, ""] : [at.slice(0, 1), at.slice(2)];
  const login = k === "l" ? logins.find((l) => l.id === id) : undefined;
  const invite = k === "i" ? invites.find((i) => i.id === id) : undefined;
  const app = k === "p" ? apps.find((a) => a.id === id) : undefined;
  const computer = k === "c" ? computers.find((a) => a.id === id) : undefined;
  const agent = k === "a" ? infos.find((x) => x.agent === id)?.agent : undefined;
  const role = k === "r" ? roles.find((r) => r.id === id) : undefined;
  // One removed (here or elsewhere): back to none chosen.
  if (sel && data && !sel.startsWith("new:") && sel !== SECRETS && !login && !invite && !app && !computer && !agent && !role) setSel("");
  // The row lit for what is chosen: a page's, its owner's.
  const lit = role ? `a:${role.agent}` : at === SECRETS && host ? `c:${host.id}` : at;
  const back = () => (setSel(role ? `a:${role.agent}` : at === SECRETS && host && wide ? `c:${host.id}` : ""), !wide && !role && setAsked(false));

  const list = (
    <>
      {manages && (
        <Group title={t("manage.members")}>
          {at === NEW_INVITE && <Row face={<Pending />} name={t("manage.newInvite")} line={t("manage.notMade")} on onClick={() => {}} />}
          {logins.map((l) => (
            <Row key={l.id} face={<Avatar src={l.avatar} name={l.name} size={24} {...deviceBadge(l.device)} />} name={l.name} line={<><Seen online={l.online} at={l.lastSeenAt || l.createdAt || 0} />{!l.approvedAt && <span>{t("apps.pending")}</span>}</>} on={lit === `l:${l.id}`} onClick={() => setSel(`l:${l.id}`)} />
          ))}
          {apps.map((a) => {
            const n = appLogins(a.id).length;
            return <Row key={a.id} face={<Avatar src={a.avatar} name={a.name} size={24} type={a.type} />} name={a.name} line={n > 1 ? t("apps.loginCount", { n }) : <Seen online={a.online} at={a.lastSeenAt || a.createdAt || 0} />} on={lit === `p:${a.id}`} onClick={() => setSel(`p:${a.id}`)} />;
          })}
          {invites.map((i) => (
            <Row key={i.id} face={<Pending />} name={i.setup ? t("term.setup") : i.name || t("manage.unusedInvite")} faint line={i.expiresAt ? t("term.until", { time: clock(i.expiresAt) }) : undefined} on={lit === `i:${i.id}`} onClick={() => setSel(`i:${i.id}`)} />
          ))}
        </Group>
      )}
      {(computers.length > 0 || at === NEW_COMPUTER) && (
        <Group title={t("manage.computers")}>
          {at === NEW_COMPUTER && <Row face={<Draft />} name={t("manage.newComputer")} line={t("manage.waitJoin")} on onClick={() => {}} />}
          {computers.map((a) => (
            <Row key={a.id} face={<ComputerFace a={a} size={24} />} name={a.host ? "Iridium" : a.name} line={<>{!a.host && <Seen online={a.online} at={Math.max(a.computer?.seenAt ?? 0, a.lastSeenAt ?? 0) || a.createdAt || 0} />}<span>{osName(a.computer)}</span></>} on={lit === `c:${a.id}`} onClick={() => setSel(`c:${a.id}`)} />
          ))}
        </Group>
      )}
      <Group title={t("manage.agents")}>
        {at === NEW_ROLE && <Row face={<Draft />} name={t("role.new")} on onClick={() => {}} />}
        {infos.map((x) => {
          const n = roles.filter((r) => r.agent === x.agent).length;
          return <Row key={x.agent} face={<AgentFace a={x.agent} size={24} />} name={agentName(x.agent)} line={n ? t("manage.roleCount", { n }) : undefined} on={lit === `a:${x.agent}`} onClick={() => setSel(`a:${x.agent}`)} />;
        })}
      </Group>
    </>
  );

  const detail =
    at === NEW_INVITE ? (
      <NewInvite done={(made) => setSel(made ? `i:${made}` : "")} />
    ) : at === NEW_COMPUTER ? (
      <div className="-mx-3">
        <AddComputer onBack={() => setSel("")} />
      </div>
    ) : at === NEW_ROLE ? (
      <div className="-mx-3">
        <NewRolePage made={(r: Role) => setSel(`r:${r.id}`)} />
      </div>
    ) : at === SECRETS ? (
      <SecretsSub onBack={back} />
    ) : login ? (
      <LoginDetail key={login.id} l={login} />
    ) : invite ? (
      <InviteDetail key={invite.id} i={invite} gone={() => setSel("")} />
    ) : app ? (
      <AppDetail key={app.id} a={app} logins={appLogins(app.id)} />
    ) : computer ? (
      <ComputerDetail key={computer.id} a={computer} open={setSel} />
    ) : agent ? (
      <AgentDetail key={agent} a={agent} computers={computers} open={setSel} />
    ) : role ? (
      <Sub title={role.name} tag={agentName(role.agent)} onBack={back}>
        <div className="-mx-3">
          <RolePage key={role.id} role={role} />
        </div>
      </Sub>
    ) : null;

  const makes = [
    manages && me?.public && { icon: <UserPlus size={15} />, label: t("manage.inviteMember"), run: () => setSel(NEW_INVITE) },
    manages && { icon: <Waypoints size={15} />, label: pairing ? t("manage.stopPairing") : t("manage.pairApp"), run: () => void api.pairing(!pairing).catch((e) => toast(t("apps.pairing"), { error: true, description: errText(e) })) },
    manages && { icon: <Monitor size={15} />, label: t("manage.addComputer"), run: () => setSel(NEW_COMPUTER) },
    { icon: <Bot size={15} />, label: t("role.new"), run: () => setSel(NEW_ROLE) },
    me?.host && { icon: <KeyRound size={15} />, label: t("manage.newSecret"), run: () => setSel(SECRETS) },
  ].filter((x) => !!x);
  const add = !at.startsWith("new:") && <AddMenu items={makes} pairing={pairing} />;
  const needs = <Needs skip={at === SECRETS} />;

  return (
    <div ref={root} className="relative flex min-h-0 flex-1 flex-col">
      <header className="flex h-12 shrink-0 items-center gap-1 px-3 pointer-coarse:h-13">
        <Dialog.Title className="m-0 min-w-0 flex-1 truncate text-sm font-semibold">{t("manage.title")}</Dialog.Title>
        {q.isFetching && <span className="g-fade mr-1.5 grid [animation-delay:200ms]"><LoaderCircle size={14} className="animate-spin text-ink-4" /></span>}
        <Dialog.Close render={<IconKey label={t("nav.close")} className="-mr-1" />}>
          <X size={17} />
        </Dialog.Close>
      </header>
      {wide ? (
        <div className="grid min-h-0 flex-1 grid-cols-[16rem_minmax(0,1fr)] gap-4">
          <div className="no-bar flex min-h-0 flex-col overflow-y-auto pb-20 pl-3">{list}</div>
          <div className="no-bar min-h-0 overflow-y-auto pt-1 pr-5 pb-20">
            {needs}
            {detail}
          </div>
        </div>
      ) : at ? (
        <div key={at} className="g-slide flex min-h-0 flex-1 flex-col">
          {!role && at !== SECRETS && (
            <div className="flex h-9 shrink-0 items-center px-1.5">
              <IconKey label={t("nav.back")} onClick={() => (setSel(""), setAsked(false))} className="size-8">
                <ChevronLeft size={17} />
              </IconKey>
            </div>
          )}
          <div className="no-bar min-h-0 flex-1 overflow-y-auto px-4 pb-6">{detail}</div>
        </div>
      ) : (
        <div className="no-bar min-h-0 flex-1 overflow-y-auto px-3 pb-20" data-dir="back">
          <div className="px-1">{needs}</div>
          {list}
        </div>
      )}
      {add}
    </div>
  );
}

/** A group of the list, its name small and faint over it (成列的对象 get a title). */
function Group({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="flex flex-col pt-3">
      <span className="px-2 pb-1 text-[11px] leading-4 text-ink-4">{title}</span>
      {children}
    </section>
  );
}

/** 新建, at the window's foot on the right: a menu of what may be made. Pairing open, it says so. */
function AddMenu({ items, pairing }: { items: { icon: ReactNode; label: string; run: () => void }[]; pairing: boolean }) {
  return (
    <Menu.Root>
      <Menu.Trigger className="g-press absolute right-4 bottom-4 z-10 flex h-9 items-center gap-1.5 rounded-full bg-ink px-4 text-sm font-medium text-on-ink shadow-(--shadow-float) outline-none pointer-coarse:h-11">
        <Plus size={15} />
        {pairing ? t("manage.pairing") : t("manage.new")}
      </Menu.Trigger>
      <MenuPortal>
        <Menu.Positioner side="top" align="end" sideOffset={8} collisionPadding={8} className="z-50">
          <MenuItems items={items} />
        </Menu.Positioner>
      </MenuPortal>
    </Menu.Root>
  );
}

/** A row being made: a plus in a faint circle. */
function Draft() {
  return (
    <span className="grid size-6 place-items-center rounded-full bg-fill text-ink-3">
      <Plus size={13} />
    </span>
  );
}

function AgentFace({ a, size }: { a: Agent; size: number }) {
  return (
    <span className="grid place-items-center rounded-full bg-fill" style={{ width: size, height: size }}>
      <Logo agent={a} size={Math.round(size / 2)} />
    </span>
  );
}

/** A row of the list, one line: its face and name, its state faint at the right; chosen, on a
 *  soft ground. */
function Row({ face, name, line, on, onClick, faint }: { face: ReactNode; name: ReactNode; line?: ReactNode; on: boolean; onClick: () => void; faint?: boolean }) {
  return (
    <button type="button" onClick={onClick} aria-current={on || undefined} className={cn("g-press flex h-10 w-full shrink-0 items-center gap-2.5 rounded-lg px-2 text-left pointer-coarse:h-12", on ? "bg-fill" : "hover:bg-fill/60")}>
      <span className="grid shrink-0 place-items-center">{face}</span>
      <span className={cn("min-w-0 flex-1 truncate text-sm", faint && "text-ink-3")}>{name}</span>
      {line && <span className="flex shrink-0 items-center gap-1.5 text-xs whitespace-nowrap text-ink-3 tabular-nums">{line}</span>}
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
      <span className="w-24 shrink-0 text-sm whitespace-nowrap text-ink-3">{label}</span>
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

/** What waits for a person (a pairing, a grant, a login's approval) as cards; skip: the
 *  credentials' own, shown in their page. */
function Needs({ skip }: { skip?: boolean }) {
  const needs = useConfirms().filter((c) => !(skip && c.kind.startsWith("secret_")));
  if (!needs.length) return null;
  return (
    <div className="flex flex-col gap-2 pt-2 pb-3">
      {needs.map((c) => (
        <ConfirmCard key={c.id} c={c} at="terminals" />
      ))}
    </div>
  );
}

/** 凭据, a page under Iridium: its list, + adding one. */
function SecretsSub({ onBack }: { onBack: () => void }) {
  const [adding, setAdding] = useState(false);
  return (
    <Sub
      title={t("manage.secrets")}
      tag="Iridium"
      onBack={onBack}
      right={
        <IconKey label={t("manage.newSecret")} onClick={() => setAdding(!adding)} className="size-7">
          <Plus size={16} />
        </IconKey>
      }
    >
      <SecretsPane adding={adding} done={() => setAdding(false)} />
    </Sub>
  );
}

// --- who may use what ---

/** The agents a permission list offers by name; others it was given show too. */
const AGENTS: Agent[] = ["claude", "codex", "dsh", "pi", "agy"];
const ALL = "*";
const permsOf = (p?: Perms): Perms => p ?? { agents: [ALL], scope: [ALL] };

/** An agent as a key: its mark and name, both faint when it is off (no dot after them: kumo,
 *  2026-11-27). */
function AgentDot({ a, on, onClick }: { a: string; on: boolean; onClick?: () => void }) {
  const body = (
    <>
      <span className={cn("grid transition-[opacity,filter] duration-(--dur-2)", !on && "opacity-35 grayscale")}>
        <Logo agent={a as Agent} size={14} />
      </span>
      <span className={cn("transition-colors duration-(--dur-2)", on ? "text-ink" : "text-ink-4")}>{agentName(a as Agent)}</span>
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
        <div className="g-fade flex flex-col gap-1 pb-2 pl-27">
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

// --- 成员 ---

/** An invite's face: a link's mark in a faint circle. */
function Pending() {
  return (
    <span className="grid size-6 place-items-center rounded-full bg-fill text-ink-3">
      <Link2 size={12} />
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

function ComputerDetail({ a, open }: { a: App; open: (sel: string) => void }) {
  const qc = useQueryClient();
  const me = useMe().data;
  const owner = useApps().data?.logins.find((l) => ofWeb(l) && l.id === a.owner?.id);
  const gh = useQuery(githubQ).data?.bindings.find((b) => b.member === "local");
  const grant = useGrant(a);
  const [folders, setFolders] = useState(false);
  const [sub, setSub] = useState<"" | "github">("");
  const face = useFacePick((b) => api.profile({ avatar: b }, a.id).then(() => qc.invalidateQueries({ queryKey: ["apps"] })));
  const hostFace = useFacePick((b) => api.serverFace(b), t("apps.serverFace"));
  const s = useSettings().data;
  const secrets = useQuery({ ...secretsQ, enabled: !!a.host && !!me?.host }).data?.secrets;
  const idle = s?.idleReap ?? "1h";
  const setIdle = (v: string) =>
    void api.putSettings({ idleReap: v }).then(
      (got) => qc.setQueryData(["settings"], got),
      (e) => toast(t("manage.idle"), { error: true, description: errText(e) }),
    );
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
        {a.host && me?.host && s && (
          <Field label={t("manage.idle")}>
            <Pills label={t("manage.idle")} value={idle} onChange={setIdle} options={[...new Set([...IDLE, idle])].map((v) => ({ value: v, label: idleName(v) }))} />
          </Field>
        )}
        {a.host && me?.host && (
          <Field label={t("manage.secrets")} onClick={() => open(SECRETS)}>
            {secrets?.length ? t("manage.count", { n: secrets.length }) : <span className="text-ink-3">{t("manage.none")}</span>}
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

function AgentDetail({ a, computers, open }: { a: Agent; computers: App[]; open: (sel: string) => void }) {
  const roles = useRoles().filter((r) => r.agent === a);
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
            <span className="no-bar -my-1 min-w-0 overflow-x-auto overflow-y-hidden py-1 [&_button]:whitespace-nowrap">
              <Pills label={t("manage.approval")} value={mode} onChange={setMode} options={[{ value: "", label: t("manage.default") }, ...info.modes.map((m) => ({ value: m, label: modeName({ id: m }) }))]} />
            </span>
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
                <span className={cn("w-4 shrink-0 text-right text-xs", x.off ? "text-ink-4" : "text-ink-2")}>{x.off ? t("manage.off") : t("manage.on")}</span>
              </div>
            );
          })}
        </Block>
      )}
      {roles.length > 0 && (
        <Block title={t("manage.roles")}>
          {roles.map((r) => (
            <button key={r.id} type="button" onClick={() => open(`r:${r.id}`)} className="g-press -mx-2 flex min-h-11 items-center gap-3 rounded-md px-2 text-left hover:bg-fill/60">
              <RoleFace role={r} size={24} />
              <span className="min-w-0 flex-1 truncate text-sm">{r.name}</span>
              <RoleLine role={r} at={placeOf(r)} className="min-w-0 shrink truncate text-xs text-ink-3" />
              <ChevronRight size={15} className="shrink-0 text-ink-4" />
            </button>
          ))}
        </Block>
      )}
    </div>
  );
}

