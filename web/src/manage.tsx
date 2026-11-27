// 管理 (kumo, 2026-11-27): one window over the page, drawn as the usage is (usage.tsx). No parts
// along its top (kumo: too many): one list at the left in three groups, 成员 (web logins, apps,
// unused invites), 电脑 (Iridium first) and Agent; the one chosen at the right. What belongs to one
// of them is in its detail: an agent's roles under it, the credentials and the idle stop under
// Iridium. A page of the right half (通行密钥, GitHub, a role, 凭据) keeps the list where it is; a
// narrow window shows one half at a time, the chosen one sliding in with its way back. 新建 is one
// key at the foot's right: a menu of what may be made, which comes as a row at the top of its group
// till it is made. Changed is saved at once.
import { Dialog } from "@base-ui/react/dialog";
import { useQueryClient } from "@tanstack/react-query";
import { ChevronLeft, ChevronRight, FolderOpen, Link2, Monitor, PencilLine, Plus, X, Trash2 } from "lucide-react";
import { useLayoutEffect, useState, type ReactNode } from "react";
import { api } from "./api/client";
import type { Agent, App, Computer, ExecAgent, Login, Need, Perms, PendingInvite, Role } from "./api/types.gen";
import { AddComputer, clock, nameError, ofWeb, OpenFolder, PasskeyList, passkeysQ, Rename, usePasskeys, revoke, setOwner, systems, useFacePick, useGrant } from "./apps";
import { MainKey, SecondKey } from "./card";
import { ConfirmCard, ConfirmIcon, confirmName, confirmTitle } from "./confirms";
import { isPair } from "./confirmname";
import { SecretsPane, secretsQ } from "./credentials";
import { isComputer, manages as managesApps, useAgents, useApps, useMe, useSettings } from "./data";
import { CacheRows } from "./device/page";
import { ahead as cacheAhead } from "./device/state";
import { CacheTotal } from "./device/total";
import { LangSwitch, ThemeSwitch, type ThemePref } from "./me";
import { InstallItem, SignOut, UpdateItem } from "./system";
import { GitHubFold, GitHubMark, GitHubWho, githubQ } from "./github";
import { t, useLang } from "./i18n";
import { modeName } from "./modes";
import { Pills } from "./pills";
import { RoleFace, RoleLine, useRoles } from "./roles";
import { NewRoleFold, placeOf, RoleFold } from "./rolespage";
import { Field, Unfold } from "./rows";
import { type ManageTab } from "./route";
import { showSecret, useSecret } from "./secrets";
import { useConfirms, usePairing } from "./store";
import { errText, toast } from "./toast";
import { agentName, ago, Avatar, AvatarKey, cn, CopyKey, FaceChangeOver, IconKey, IrMark, Logo, ServerFace, SureKey, Switch, Tip } from "./ui";
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
 *  "a:" an agent), or a row being made (NEW_*). What is under one unfolds in its detail. */
const NEW_INVITE = "new:invite";
const NEW_COMPUTER = "new:computer";
const NEW_APP = "new:app";
const IR = "ir";

export function ManagePage({ tab, theme, setTheme }: { tab: ManageTab; theme: ThemePref; setTheme: (v: ThemePref) => void }) {
  useLang();
  const [root, wide] = useWide();
  const q = useApps();
  const data = q.data;
  const me = useMe().data;
  const manages = managesApps(me);
  const infos = (useAgents().data ?? []).filter((x) => !x.missing);
  const roles = useRoles();
  const pairing = !!usePairing()?.open;
  const needs = useConfirms();
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
    if (x === "ir") return IR;
    if (x === "apps" && apps[0]) return `p:${apps[0].id}`;
    if ((x === "computers" || x === "secrets") && host) return `c:${host.id}`;
    if ((x === "agents" || x === "roles" || !mine) && infos[0]) return `a:${infos[0].agent}`;
    return mine ? `l:${mine.id}` : "";
  };
  // Nothing chosen yet: the row the route stands for, worked out as the data comes (an old route's
  // part opens its row on a narrow window too, till the way back is taken).
  const [sel, setSel] = useState("");
  const [asked, setAsked] = useState(tab !== "accounts" && tab !== "ir");
  const [was, setWas] = useState(tab);
  if (tab !== was) (setWas(tab), setSel(""), setAsked(true));
  const at = sel || (wide || asked ? firstOf(tab) : "");
  // Iridium's 凭据 asked for from outside (the route's, 新建's): unfolded there, a new one being added.
  const [want, setWant] = useState(tab === "secrets" ? "show:0" : "");
  if (tab !== was && tab === "secrets") setWant(`show:${Date.now()}`);
  const [k, id] = at.startsWith("new:") || at === IR ? [at, ""] : [at.slice(0, 1), at.slice(2)];
  const login = k === "l" ? logins.find((l) => l.id === id) : undefined;
  const invite = k === "i" ? invites.find((i) => i.id === id) : undefined;
  const app = k === "p" ? apps.find((a) => a.id === id) : undefined;
  const computer = k === "c" ? computers.find((a) => a.id === id) : undefined;
  const agent = k === "a" ? infos.find((x) => x.agent === id)?.agent : undefined;
  const need = k === "q" ? needs.find((c) => c.id === id) : undefined;
  // One removed or answered (here or elsewhere): back to none chosen.
  if (sel && data && !sel.startsWith("new:") && sel !== IR && !login && !invite && !app && !computer && !agent && !need) setSel("");
  const lit = at;

  const pair = (on: boolean) => void api.pairing(on).catch((e) => toast(t("apps.pairing"), { error: true, description: errText(e) }));
  // Each group ends in a line of its own to make one (kumo, 2026-11-27: no key floating over the
  // window): pressed, it is the row being made, its form at the right.
  const list = (
    <>
      {/* This page and the Iridium it talks to (kumo, 2026-11-27): 外观, 语言, 缓存, 更新; on a
          phone the only settings there are. Oneself is among the members. */}
      {/* What waits for a person, a list of its own at the top (kumo, 2026-11-27: not cards over
          the detail); the avatar at the sidebar's foot has a dot while any does. */}
      {needs.length > 0 && (
        <Group title={t("manage.requests")}>
          {needs.map((c) => (
            <Row key={c.id} face={<NeedFace c={c} size={24} />} name={confirmTitle(c)} line={isPair(c) && c.code ? t("confirm.pairCode", { code: c.code }) : confirmName(c)} on={lit === `q:${c.id}`} onClick={() => setSel(`q:${c.id}`)} />
          ))}
        </Group>
      )}
      <Group>
        <Row face={<IrMark size={24} className="text-ink" />} name="Iridium" on={lit === IR} onClick={() => setSel(IR)} />
      </Group>
      {manages && (
        <Group title={t("manage.members")}>
          {logins.map((l) => (
            <Row key={l.id} face={<Avatar src={l.avatar} name={l.name} size={24} />} name={l.name} line={<><Seen online={l.online} at={l.lastSeenAt || l.createdAt || 0} />{!l.approvedAt && <span>{t("apps.pending")}</span>}</>} on={lit === `l:${l.id}`} onClick={() => setSel(`l:${l.id}`)} />
          ))}
          {invites.map((i) => (
            <Row key={i.id} face={<Pending />} name={i.setup ? t("term.setup") : i.name || t("manage.unusedInvite")} faint line={i.expiresAt ? t("term.until", { time: clock(i.expiresAt) }) : undefined} on={lit === `i:${i.id}`} onClick={() => setSel(`i:${i.id}`)} />
          ))}
          {me?.public && <AddRow label={t("manage.invite")} on={at === NEW_INVITE} onClick={() => setSel(NEW_INVITE)} />}
        </Group>
      )}
      {manages && (
        <Group title={t("manage.apps")}>
          {apps.map((a) => {
            const n = appLogins(a.id).length;
            return <Row key={a.id} face={<Avatar src={a.avatar} name={a.name} size={24} />} name={a.name} line={n > 1 ? t("apps.loginCount", { n }) : <Seen online={a.online} at={a.lastSeenAt || a.createdAt || 0} />} on={lit === `p:${a.id}`} onClick={() => setSel(`p:${a.id}`)} />;
          })}
          <AddRow label={t("manage.invite")} on={at === NEW_APP} onClick={() => (setSel(NEW_APP), pairing || pair(true))} />
        </Group>
      )}
      {manages && (
        <Group title={t("manage.computers")}>
          {computers.map((a) => (
            <Row key={a.id} face={<ComputerFace a={a} size={24} />} name={a.host ? "Iridium" : a.name} line={<>{!a.host && <Seen online={a.online} at={Math.max(a.computer?.seenAt ?? 0, a.lastSeenAt ?? 0) || a.createdAt || 0} />}<span>{osName(a.computer)}</span></>} on={lit === `c:${a.id}`} onClick={() => setSel(`c:${a.id}`)} />
          ))}
          <AddRow label={t("manage.invite")} on={at === NEW_COMPUTER} onClick={() => setSel(NEW_COMPUTER)} />
        </Group>
      )}
      <Group title={t("manage.agents")}>
        {infos.map((x) => {
          const n = roles.filter((r) => r.agent === x.agent).length;
          return <Row key={x.agent} face={<AgentFace a={x.agent} size={24} />} name={agentName(x.agent)} line={n ? t("manage.roleCount", { n }) : undefined} on={lit === `a:${x.agent}`} onClick={() => setSel(`a:${x.agent}`)} />;
        })}
      </Group>
    </>
  );

  const detail =
    at === IR ? (
      <IrDetail theme={theme} setTheme={setTheme} />
    ) : at === NEW_INVITE ? (
      <NewInvite done={(made) => setSel(made ? `i:${made}` : "")} />
    ) : at === NEW_APP ? (
      <div className="flex flex-col">
        <Head face={<Draft size={40} />} name={t("manage.inviteApp")} />
        <Block>
          <Field label={t("apps.pairing")}>
            <Switch label={t("apps.pairing")} on={pairing} onChange={pair} />
          </Field>
        </Block>
      </div>
    ) : at === NEW_COMPUTER ? (
      <div className="-mx-3">
        <AddComputer onBack={() => setSel("")} />
      </div>
    ) : login ? (
      <LoginDetail key={login.id} l={login} />
    ) : invite ? (
      <InviteDetail key={invite.id} i={invite} gone={() => setSel("")} />
    ) : app ? (
      <AppDetail key={app.id} a={app} logins={appLogins(app.id)} />
    ) : computer ? (
      <ComputerDetail key={computer.id} a={computer} want={computer.host ? want : ""} />
    ) : agent ? (
      <AgentDetail key={agent} a={agent} computers={computers} />
    ) : need ? (
      <NeedDetail key={need.id} c={need} />
    ) : null;

  return (
    <div ref={root} className="relative flex min-h-0 flex-1 flex-col">
      {/* No title at its top left (kumo, 2026-11-27). Wide: no band of its own over the halves
          (kumo, 2026-11-27: the top needless), the close key at the right of the right half's
          head (Head leaves it room). */}
      <Dialog.Title className="sr-only">{t("manage.title")}</Dialog.Title>
      {wide ? (
        <Dialog.Close render={<IconKey label={t("nav.close")} className="absolute top-[22px] right-3 z-10" />}>
          <X size={17} />
        </Dialog.Close>
      ) : (
        <header className="flex h-12 shrink-0 items-center gap-1 px-3 pointer-coarse:h-13">
          <span className="flex-1" />
          <Dialog.Close render={<IconKey label={t("nav.close")} className="-mr-1" />}>
            <X size={17} />
          </Dialog.Close>
        </header>
      )}
      {wide ? (
        <div className="grid min-h-0 flex-1 grid-cols-[16rem_minmax(0,1fr)] gap-2">
          <div className="no-bar flex min-h-0 flex-col overflow-y-auto border-r border-rail-edge bg-rail px-2 pt-3 pb-4">{list}</div>
          <div data-wide="" className="group/detail no-bar min-h-0 overflow-y-auto pt-3 pr-5 pb-6 pl-2">{detail}</div>
        </div>
      ) : at ? (
        <div key={at} className="g-slide flex min-h-0 flex-1 flex-col">
          <div className="flex h-9 shrink-0 items-center px-1.5">
            <IconKey label={t("nav.back")} onClick={() => (setSel(""), setAsked(false))} className="size-8">
              <ChevronLeft size={17} />
            </IconKey>
          </div>
          <div className="no-bar min-h-0 flex-1 overflow-y-auto px-4 pb-6">{detail}</div>
        </div>
      ) : (
        <div className="no-bar min-h-0 flex-1 overflow-y-auto px-3 pb-6" data-dir="back">
          {list}
        </div>
      )}
    </div>
  );
}

/** A group of the list, its name small and faint over it (成列的对象 get a title). */
function Group({ title, children }: { title?: string; children: ReactNode }) {
  return (
    <section className="flex flex-col pt-3">
      {title && <span className="px-2 pb-1 text-[11px] leading-4 text-ink-4">{title}</span>}
      {children}
    </section>
  );
}

/** A group's last line: a plus and what it makes, faint till it is the row being made. */
function AddRow({ label, on, onClick }: { label: string; on: boolean; onClick: () => void }) {
  return <Row face={<Draft />} name={label} faint={!on} on={on} onClick={onClick} />;
}

/** A row being made: a plus in a faint circle. */
function Draft({ size = 24 }: { size?: number }) {
  return (
    <span className="grid place-items-center rounded-full g-disc text-ink-3" style={{ width: size, height: size }}>
      <Plus size={Math.round(size * 0.55)} />
    </span>
  );
}

function AgentFace({ a, size }: { a: Agent; size: number }) {
  return (
    <span className="grid place-items-center rounded-full g-disc" style={{ width: size, height: size }}>
      <Logo agent={a} size={Math.round(size / 2)} />
    </span>
  );
}

/** A row of the list, one line: its face and name, its state faint at the right; chosen, on a
 *  soft ground. */
function Row({ face, name, line, on, onClick, faint }: { face: ReactNode; name: ReactNode; line?: ReactNode; on: boolean; onClick: () => void; faint?: boolean }) {
  return (
    <button type="button" onClick={onClick} aria-current={on || undefined} className={cn("g-press flex h-10 w-full shrink-0 items-center gap-2.5 rounded-lg px-2 text-left pointer-coarse:h-12", on ? "g-picked" : "hover:g-picked")}>
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
    <div className="flex min-h-16 items-center gap-3 border-b border-edge pb-3 group-data-[wide]/detail:pr-10">
      <span className="grid shrink-0 place-items-center">{face}</span>
      <span className="min-w-0 flex-1 truncate text-2xl font-semibold">{name}</span>
      {figures && <span className="flex shrink-0 items-center gap-3 text-xs text-ink-3 tabular-nums">{figures}</span>}
      {keys}
    </div>
  );
}

/** One of a list in a detail (an app's login, an agent's role): its face, name and line; pressed,
 *  what it holds unfolds under it. */
function FoldRow({ face, name, line, open, onClick }: { face: ReactNode; name: ReactNode; line?: ReactNode; open: boolean; onClick: () => void }) {
  return (
    <button type="button" onClick={onClick} aria-expanded={open} className="g-press -mx-2 flex min-h-11 items-center gap-3 rounded-md px-2 text-left hover:bg-wash">
      {face}
      <span className="min-w-0 flex-1 truncate text-sm">{name}</span>
      {line && <span className="flex min-w-0 shrink items-center gap-1.5 truncate text-xs text-ink-3 tabular-nums">{line}</span>}
      <ChevronRight size={15} className={cn("shrink-0 text-ink-4 transition-transform duration-(--dur-2) ease-(--ease-out)", open && "rotate-90")} />
    </button>
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

/** A need's face: its icon on a round ground. */
function NeedFace({ c, size }: { c: Need; size: number }) {
  return (
    <span className="g-disc grid shrink-0 place-items-center rounded-full text-ink-2" style={{ width: size, height: size }}>
      <ConfirmIcon c={c} size={Math.round(size * 0.55)} />
    </span>
  );
}

/** A request chosen: an app asking to join drawn as the window draws the rest; another kind, its
 *  card as the chat has it. */
function NeedDetail({ c }: { c: Need }) {
  if (c.kind === "app_join") return <PairDetail c={c} />;
  return (
    <div className="@container pt-2">
      <ConfirmCard c={c} at="terminals" />
    </div>
  );
}

/** An app or a computer asking to join: its code to check against the one it shows, what it
 *  asks for (an agent turned off is not given), the agents it brings (one turned off is off there
 *  from the start); 拒绝 or 允许 at the foot. The TOKEN goes to it, never here. */
function PairDetail({ c }: { c: Need }) {
  const m = c.pair;
  const wants = c.wants;
  const asked = wants ? (wants.agents.includes(ALL) ? AGENTS : wants.agents) : [];
  const [no, setNo] = useState<string[]>([]);
  const carried = m?.agents?.map((a) => a.agent) ?? [];
  const [off, setOff] = useState<string[]>([]);
  const [busy, setBusy] = useState<"" | "allow" | "deny">("");
  const answer = async (allow: boolean) => {
    setBusy(allow ? "allow" : "deny");
    const perms = wants && no.length ? { agents: asked.filter((a) => !no.includes(a)), scope: wants.scope } : undefined;
    try {
      const r = await api.answerConfirm(c.id, allow, allow ? { ...(perms ? { perms } : {}), ...(off.length ? { agentsOff: off } : {}) } : undefined);
      if (r.state === "failed") toast(t("err.confirm"), { error: true, description: r.error });
      else toast(allow ? t("confirm.pairDone", { name: r.name ?? c.name ?? "" }) : t("toast.denied"));
    } catch (e) {
      toast(t("err.confirm"), { error: true, description: errText(e) });
    } finally {
      setBusy("");
    }
  };
  const flip = (list: string[], set: (v: string[]) => void, a: string) => set(list.includes(a) ? list.filter((x) => x !== a) : [...list, a]);
  return (
    <div className="flex flex-col">
      <Head
        face={<NeedFace c={c} size={40} />}
        name={c.name}
        figures={
          <>
            <span>{m?.os ? (systems[m.os] ?? m.os) : m?.program || m?.type || t("term.channel")}</span>
            {c.expiresAt ? <span>{t("term.until", { time: clock(c.expiresAt) })}</span> : null}
          </>
        }
      />
      <Block>
        <Field label={t("manage.pairCode")}>
          <span className="font-mono text-2xl font-semibold tracking-[0.3em] text-ink tabular-nums">{c.code}</span>
        </Field>
        {m?.login && <Field label={t("manage.logins")}>{m.login.name || m.login.id}</Field>}
      </Block>
      {asked.length > 0 && (
        <Block>
          <AgentLines>
            {asked.map((a) => (
              <AgentLine key={a} a={a} on={!no.includes(a)} onChange={() => flip(no, setNo, a)} />
            ))}
          </AgentLines>
          <Field label={t("manage.folders")}>{wants!.scope.includes(ALL) ? t("manage.all") : wants!.scope.join("、")}</Field>
        </Block>
      )}
      {carried.length > 0 && (
        <Block title={t("manage.carried")}>
          <AgentLines>
            {carried.map((a) => (
              <AgentLine key={a} a={a} on={!off.includes(a)} onChange={() => flip(off, setOff, a)} />
            ))}
          </AgentLines>
        </Block>
      )}
      <div className="flex justify-end gap-2 pt-3">
        <SecondKey disabled={!!busy} onClick={() => void answer(false)}>
          {t("ask.deny")}
        </SecondKey>
        <MainKey busy={busy === "allow"} disabled={!!busy} onClick={() => void answer(true)}>
          {t("confirm.allow")}
        </MainKey>
      </div>
    </div>
  );
}

/** An agent's row making a new role for it, among its roles. */
const NEW_ROLE_HERE = "\u0000new";

// --- Ir ---

/** This page's and its Iridium's: how it looks, its language, what this device keeps, the update,
 *  installing it, signing out. */
function IrDetail({ theme, setTheme }: { theme: ThemePref; setTheme: (v: ThemePref) => void }) {
  const lang = useLang();
  const me = useMe().data;
  const [cache, setCache] = useState(false);
  return (
    <div className="flex flex-col">
      <Head face={<IrMark size={40} className="text-ink" />} name="Iridium" />
      <Block>
        <Field label={t("me.theme")}>
          <ThemeSwitch theme={theme} setTheme={setTheme} />
        </Field>
        <Field label={t("me.lang")}>
          <LangSwitch lang={lang} />
        </Field>
        <Field label={t("cache.title")} onClick={() => (cacheAhead(), setCache(!cache))} open={cache}>
          <CacheTotal />
        </Field>
        <Unfold open={cache}>
          <CacheRows />
        </Unfold>
        <InstallItem />
        <UpdateItem manages={managesApps(me)} shown />
        {me && !me.host && <SignOut />}
      </Block>
    </div>
  );
}

// --- who may use what ---

/** The agents a permission list offers by name; others it was given show too. */
const AGENTS: Agent[] = ["claude", "codex", "dsh", "pi", "agy"];
const ALL = "*";
const permsOf = (p?: Perms): Perms => p ?? { agents: [ALL], scope: [ALL] };

/** An agent as a line of its own (kumo, 2026-11-27: side by side there was no room for more):
 *  its mark and name, faint when off; its switch at the right, or the state as words. */
function AgentLine({ a, on, onChange }: { a: string; on: boolean; onChange?: (on: boolean) => void }) {
  return (
    <span className="flex h-9 items-center gap-2 pointer-coarse:h-11">
      <span className={cn("grid transition-[opacity,filter] duration-(--dur-2)", !on && "opacity-35 grayscale")}>
        <Logo agent={a as Agent} size={15} />
      </span>
      <span className={cn("min-w-0 flex-1 truncate transition-colors duration-(--dur-2)", on ? "text-ink" : "text-ink-3")}>{agentName(a as Agent)}</span>
      {onChange ? <Switch label={agentName(a as Agent)} on={on} onChange={onChange} /> : <span className={cn("text-xs", on ? "text-ink-2" : "text-ink-4")}>{on ? t("manage.on") : t("manage.off")}</span>}
    </span>
  );
}

/** The agents, one a line, with no name over them: their marks say what they are. */
function AgentLines({ children }: { children: ReactNode }) {
  return <div className="flex flex-col py-1 text-sm">{children}</div>;
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
      <AgentLines>
        {shown.map((a) => (
          <AgentLine key={a} a={a} on={isOn(a)} onChange={() => flip(a)} />
        ))}
      </AgentLines>
      <Field label={t("manage.folders")} onClick={() => setFolders(!folders)}>
        <span className="truncate">{allScope ? t("manage.all") : p.scope.length ? p.scope.join("、") : t("apps.scopeNone")}</span>
      </Field>
      {folders && (
        <div className="g-fade flex flex-col gap-1 pb-2">
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
    <span className="grid size-6 place-items-center rounded-full g-disc text-ink-3">
      <Link2 size={12} />
    </span>
  );
}

function NewInvite({ done }: { done: (id?: string) => void }) {
  const qc = useQueryClient();
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);
  const make = async () => {
    setBusy(true);
    try {
      const inv = await api.invite(name.trim());
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
      <InviteLink id={i.id} />
    </div>
  );
}

/** An invite's link, as the window draws a line: its copy key at the right of its name, the link
 *  on one line under it (sideways when long), its QR code under that. Shown only where it was just
 *  made (the server keeps no link): after that, the state alone. */
function InviteLink({ id }: { id: string }) {
  const secret = useSecret();
  const inv = secret && "invite" in secret && secret.at === HERE && secret.invite.id === id ? secret.invite : null;
  if (!inv)
    return (
      <Block>
        <Field label={t("manage.link")}>
          <span className="text-ink-3">{t("manage.linkHidden")}</span>
        </Field>
      </Block>
    );
  return (
    <Block>
      <Field label={t("manage.link")}>
        <CopyKey text={inv.url} />
      </Field>
      <code data-secret className="no-bar block overflow-x-auto rounded-md bg-fill px-3 py-2 font-mono text-xs leading-5 whitespace-nowrap text-ink select-all">
        {inv.url}
      </code>
      {inv.qr && <img src={inv.qr} alt={t("secret.qr")} width={140} height={140} className="mt-3 rounded-md bg-white p-1.5 [image-rendering:pixelated]" />}
    </Block>
  );
}

function LoginDetail({ l }: { l: Login }) {
  const qc = useQueryClient();
  const me = useMe().data;
  const apps = useApps().data?.apps ?? [];
  const gh = useQuery(githubQ).data?.bindings.find((b) => b.member === l.id);
  const [open, setOpen] = useState<"" | "passkeys" | "github">("");
  const flip = (x: "passkeys" | "github") => setOpen((o) => (o === x ? "" : x));
  useQuery({ ...passkeysQ(l.id), enabled: !!me?.public }); // asked ahead: it unfolds whole
  const face = useFacePick((b) => api.profile({ avatar: b }, l.id).then(() => qc.invalidateQueries({ queryKey: ["apps"] })));
  const mine = l.id === me?.login;
  const computers = apps.filter((a) => a.owner?.id === l.id);
  return (
    <div className="flex flex-col">
      <Head
        face={<AvatarKey label={t("term.avatar")} onClick={face.open} src={l.avatar} name={l.name} size={40} />}
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
          <>
            <Field label={t("manage.passkeys")} onClick={() => flip("passkeys")} open={open === "passkeys"}>
              {l.passkeys ? t("manage.count", { n: l.passkeys }) : <span className="text-ink-3">{t("manage.none")}</span>}
            </Field>
            <Unfold open={open === "passkeys"}>
              <PasskeysFold login={l} />
            </Unfold>
          </>
        )}
        {(gh || mine) && (
          <>
          <Field label={<span className="flex items-center gap-1.5"><GitHubMark size={14} />GitHub</span>} onClick={() => flip("github")} open={open === "github"}>
            {gh ? (
              <GitHubWho login={gh.login} avatar={gh.avatar} />
            ) : (
              <span className="text-ink-3">{t("manage.unbound")}</span>
            )}
          </Field>
          <Unfold open={open === "github"}>
            <GitHubFold member={l.id} mine={mine} />
          </Unfold>
          </>
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

/** A login's passkeys under their line: a new device's link first, then each one. */
function PasskeysFold({ login }: { login: Login }) {
  const { add } = usePasskeys(login);
  return (
    <div className="flex flex-col">
      <PasskeyList login={login} />
      <button type="button" onClick={() => void add()} className="g-press -mx-2 flex min-h-10 items-center gap-2 rounded-md px-2 text-left text-sm text-ink-2 hover:bg-wash">
        <Plus size={15} className="text-ink-3" />
        {t("passkey.add")}
      </button>
    </div>
  );
}


function AppDetail({ a, logins }: { a: App; logins: Login[] }) {
  const qc = useQueryClient();
  const [sub, setSub] = useState("");
  const face = useFacePick((b) => api.profile({ avatar: b }, a.id).then(() => qc.invalidateQueries({ queryKey: ["apps"] })));
  return (
    <div className="flex flex-col">
      <Head
        face={<AvatarKey label={t("term.avatar")} onClick={face.open} src={a.avatar} name={a.name} size={40} />}
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
            <div key={l.id} className="flex flex-col">
              <FoldRow face={<Avatar src={l.avatar} name={l.name} size={24} />} name={l.name} line={<Seen online={l.online} at={l.lastSeenAt || l.createdAt || 0} />} open={sub === l.id} onClick={() => setSub(sub === l.id ? "" : l.id)} />
              <Unfold open={sub === l.id}>
                <div className="flex flex-col pl-9">
                  <Access perms={l.perms} save={(p) => api.loginPerms(a.id, l.id, p)} />
                  <Field label={t("apps.removeLogin")}>
                    <span className="flex-1" />
                    <SureKey label={t("apps.removeLogin")} sure={t("term.revokeSure")} run={() => void revoke(qc, l.id, l.name).then(() => setSub(""))} className="size-8">
                      <Trash2 size={15} />
                    </SureKey>
                  </Field>
                </div>
              </Unfold>
            </div>
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
  return a.host ? <ServerFace size={size} /> : <Avatar src={a.avatar} name={a.name} size={size} />;
}

/** want: asked from outside (the route's 凭据, 新建's credential), as "<what>:<count>": 凭据
 *  unfolded, a new one being added too. */
function ComputerDetail({ a, want = "" }: { a: App; want?: string }) {
  const pairing = !!usePairing()?.open;
  const qc = useQueryClient();
  const me = useMe().data;
  const owner = useApps().data?.logins.find((l) => ofWeb(l) && l.id === a.owner?.id);
  const gh = useQuery(githubQ).data?.bindings.find((b) => b.member === "local");
  const grant = useGrant(a);
  const [folders, setFolders] = useState(false);
  // A credential waiting on a person: 凭据 open, its card there (the needs above leave it out).
  const asks = useConfirms().some((c) => c.kind.startsWith("secret_"));
  const [sub, setSub] = useState<"" | "github" | "secrets">(want || (a.host && asks) ? "secrets" : "");
  const [adding, setAdding] = useState(want.startsWith("new"));
  const [wanted, setWanted] = useState(want);
  if (want !== wanted) (setWanted(want), want && (setSub("secrets"), setAdding(want.startsWith("new"))));
  const flip = (x: "github" | "secrets") => setSub((o) => (o === x ? "" : x));
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
            <AvatarKey label={t("term.avatar")} onClick={face.open} src={a.avatar} name={a.name} size={40} />
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
        {m && m.agents.length > 0 && (
          <AgentLines>
            {m.agents.map((x) => (
              <AgentLine key={x.agent} a={x.agent} on={!x.off} />
            ))}
          </AgentLines>
        )}
        {!a.host && m && (
          <Field label={t("apps.shell")}>
            {may ? <Switch label={t("apps.shell")} on={!!m.shell} onChange={(on) => void grant({ shell: on })} /> : <span className={m.shell ? "" : "text-ink-3"}>{m.shell ? t("manage.on") : t("manage.off")}</span>}
          </Field>
        )}
        {!a.host && m && (
          <Field label={t("manage.folders")}>
            <span className="min-w-0 truncate">{m.roots?.length ? m.roots.join("、") : t("grant.allFolders")}</span>
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
            <span className="min-w-0 truncate">{a.owner ? (owner?.name ?? a.owner.name) : <span className="text-ink-3">{t("manage.none")}</span>}</span>
            {me?.web && me.login && (
              <SecondKey onClick={() => void setOwner(qc, a.id, a.owner?.id === me.login ? { clear: true } : {})}>{a.owner?.id === me.login ? t("apps.notMine") : t("apps.mine")}</SecondKey>
            )}
          </Field>
        )}
        {a.host && managesApps(me) && (
          <Field label={t("apps.pairing")}>
            <span className="flex-1" />
            <Switch label={t("apps.pairing")} on={pairing} onChange={(on) => void api.pairing(on).catch((e) => toast(t("apps.pairing"), { error: true, description: errText(e) }))} />
          </Field>
        )}
        {a.host && me?.host && s && (
          <Field label={t("manage.idle")}>
            <Pills label={t("manage.idle")} value={idle} onChange={setIdle} options={[...new Set([...IDLE, idle])].map((v) => ({ value: v, label: idleName(v) }))} />
          </Field>
        )}
        {a.host && me?.host && (
          <>
            <Field label={t("manage.secrets")} onClick={() => flip("secrets")} open={sub === "secrets"}>
              {secrets?.length ? t("manage.count", { n: secrets.length }) : <span className="text-ink-3">{t("manage.none")}</span>}
            </Field>
            <Unfold open={sub === "secrets"}>
              <div className="flex flex-col gap-2">
                <SecretsPane adding={adding} done={() => setAdding(false)} />
                {!adding && (
                  <button type="button" onClick={() => setAdding(true)} className="g-press -mx-2 flex min-h-10 items-center gap-2 rounded-md px-2 text-left text-sm text-ink-2 hover:bg-wash">
                    <Plus size={15} className="text-ink-3" />
                    {t("manage.newSecret")}
                  </button>
                )}
              </div>
            </Unfold>
          </>
        )}
        {a.host && (gh || me?.host) && (
          <>
            <Field label={<span className="flex items-center gap-1.5"><GitHubMark size={14} />GitHub</span>} onClick={() => flip("github")} open={sub === "github"}>
              {gh ? (
                <GitHubWho login={gh.login} avatar={gh.avatar} />
              ) : (
                <span className="text-ink-3">{t("manage.unbound")}</span>
              )}
            </Field>
            <Unfold open={sub === "github"}>
              <GitHubFold member="local" mine={!!me?.host} />
            </Unfold>
          </>
        )}
      </Block>
    </div>
  );
}

// --- Agent ---

/** How long an idle agent is kept before it is stopped ("0" never). */
const IDLE = ["0", "30m", "1h", "4h"] as const;
const idleName = (v: string) => (v === "0" ? t("manage.off") : v === "30m" ? t("manage.minutes", { n: 30 }) : v.endsWith("h") ? t("manage.hours", { n: Number(v.slice(0, -1)) }) : v);

function AgentDetail({ a, computers }: { a: Agent; computers: App[] }) {
  const [role, setRole] = useState("");
  const [comp, setComp] = useState("");
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
          <span className="grid size-10 place-items-center rounded-full g-disc">
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
            const foldable = x.sync !== undefined || !!x.pathNeeded;
            const line = (
              <>
                {!c.host && <Seen online={c.online} at={Math.max(c.computer?.seenAt ?? 0, c.lastSeenAt ?? 0)} />}
                {x.version && <span>{x.version}</span>}
                <span>{osName(c.computer)}</span>
                <span className={cn("w-4 shrink-0 text-right", x.off ? "text-ink-4" : "text-ink-2")}>{x.off ? t("manage.off") : t("manage.on")}</span>
              </>
            );
            if (!foldable) {
              return (
                <div key={c.id} className="flex min-h-11 items-center gap-3">
                  <ComputerFace a={c} size={24} />
                  <span className={cn("min-w-0 flex-1 truncate text-sm", !c.host && !c.online && "text-ink-3")}>{c.host ? "Iridium" : c.name}</span>
                  <span className="flex shrink-0 items-center gap-1.5 text-xs text-ink-3 tabular-nums">{line}</span>
                </div>
              );
            }
            return (
              <div key={c.id} className="flex flex-col">
                <FoldRow
                  face={<ComputerFace a={c} size={24} />}
                  name={<span className={cn(!c.host && !c.online && "text-ink-3")}>{c.host ? "Iridium" : c.name}</span>}
                  line={line}
                  open={comp === c.id}
                  onClick={() => setComp(comp === c.id ? "" : c.id)}
                />
                <Unfold open={comp === c.id}>
                  <AgentComputerFold computer={c} agent={a} info={x} />
                </Unfold>
              </div>
            );
          })}
        </Block>
      )}
      <Block title={t("manage.roles")}>
          {roles.map((r) => (
            <div key={r.id} className="flex flex-col">
              <FoldRow face={<RoleFace role={r} size={24} />} name={r.name} line={<RoleLine role={r} at={placeOf(r)} className="truncate" />} open={role === r.id} onClick={() => setRole(role === r.id ? "" : r.id)} />
              <Unfold open={role === r.id}>
                <RoleFold role={r} gone={() => setRole("")} />
              </Unfold>
            </div>
          ))}
        <div className="flex flex-col">
          <FoldRow face={<Draft />} name={<span className="text-ink-3">{t("role.new")}</span>} open={role === NEW_ROLE_HERE} onClick={() => setRole(role === NEW_ROLE_HERE ? "" : NEW_ROLE_HERE)} />
          <Unfold open={role === NEW_ROLE_HERE}>
            <NewRoleFold key={String(role === NEW_ROLE_HERE)} agent={a} made={(r: Role) => setRole(r.id)} />
          </Unfold>
        </div>
      </Block>
    </div>
  );
}

/** Per-computer agent settings unfolded under a computer row in AgentDetail: "sync new sessions"
 *  toggle (bidirectional with the host app) and, only when the local host app is neither connected
 *  nor auto-discovered, the manual install path fallback. */
function AgentComputerFold({ computer, agent, info }: { computer: App; agent: Agent; info: ExecAgent }) {
  const qc = useQueryClient();
  const [syncOn, setSyncOn] = useState(!!info.sync);
  const [savingSync, setSavingSync] = useState(false);
  const [pathVal, setPathVal] = useState(info.path ?? "");
  const [savingPath, setSavingPath] = useState(false);

  // Follow live updates from the host app (e.g. toggled on Desktop).
  const [seenSync, setSeenSync] = useState(info.sync);
  if (info.sync !== seenSync) {
    setSeenSync(info.sync);
    setSyncOn(!!info.sync);
  }
  const [seenPath, setSeenPath] = useState(info.path);
  if (info.path !== seenPath) {
    setSeenPath(info.path);
    setPathVal(info.path ?? "");
  }

  const toggleSync = async (next: boolean) => {
    setSyncOn(next);
    setSavingSync(true);
    try {
      const got = await api.putAgentConfig(computer.id, agent, { sync: next });
      if (got.sync !== undefined) setSyncOn(got.sync);
      void qc.invalidateQueries({ queryKey: ["apps"] });
    } catch (e) {
      setSyncOn(!next);
      toast(t("err.config"), { error: true, description: errText(e) });
    } finally {
      setSavingSync(false);
    }
  };

  const dirty = pathVal.trim() !== (info.path ?? "").trim();
  const savePath = async () => {
    if (!dirty || savingPath) return;
    setSavingPath(true);
    try {
      const got = await api.putAgentConfig(computer.id, agent, { path: pathVal.trim() });
      setPathVal(got.path ?? "");
      void qc.invalidateQueries({ queryKey: ["apps"] });
      toast(t("manage.saved"));
    } catch (e) {
      toast(t("err.config"), { error: true, description: errText(e) });
    } finally {
      setSavingPath(false);
    }
  };

  return (
    <div className="flex flex-col pl-9 pr-1">
      {info.sync !== undefined && (
        <Field label={t("manage.syncNew")}>
          <Switch on={syncOn} disabled={savingSync} onChange={(v) => void toggleSync(v)} label={t("manage.syncNew")} />
        </Field>
      )}
      {info.pathNeeded && (
        <div className="flex flex-col gap-1.5 py-2 sm:flex-row sm:items-center sm:gap-3">
          <label htmlFor={`path-${computer.id}-${agent}`} className="shrink-0 text-sm whitespace-nowrap">
            {t("manage.desktopPath")}
          </label>
          <div className="flex min-w-0 flex-1 items-center gap-1.5 sm:justify-end">
            <input
              id={`path-${computer.id}-${agent}`}
              type="text"
              value={pathVal}
              disabled={savingPath}
              placeholder={t("manage.desktopPathPlaceholder")}
              onChange={(e) => setPathVal(e.target.value)}
              onKeyDown={(e) => e.key === "Enter" && void savePath()}
              className="h-8 min-w-0 flex-1 rounded-md bg-fill px-2.5 font-mono text-xs text-ink placeholder:text-ink-3 sm:max-w-[310px]"
            />
            {dirty && (
              <button
                type="button"
                disabled={savingPath}
                onClick={() => void savePath()}
                className="g-press h-8 shrink-0 rounded-md bg-ink px-2.5 text-xs font-medium text-paper disabled:opacity-50"
              >
                {t("manage.save")}
              </button>
            )}
          </div>
        </div>
      )}
    </div>
  );
}

