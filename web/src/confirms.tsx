// A confirmation an agent's ir asked a panel for (docs/api.md "确认"): a new panel's invite or a
// plugin's token, which must not reach the agent. Its card sits above the asking session's
// composer and on the settings' 终端 page, and the sidebar lists it under 需要你. Allowed, the
// secret goes to this panel alone, in a card where this one was (secret.tsx); the agent only learns how it went.
// A credential's (plan "凭据"): secret-set has a password box, whose value goes to the daemon and
// nowhere else; secret-use is allow, this session, or deny.
import { KeyRound, Link2, LockKeyhole, ShieldQuestion, Terminal as TermIcon } from "lucide-react";
import { ConfirmIcon, confirmName, confirmTitle, isPair } from "./confirmname";
import { useRef, useState, type ReactNode } from "react";
import { api } from "./api/client";
import type { Agent, ExecAgent, Need, Perms } from "./api/types.gen";
import type { Machine } from "./machines";
import { CardDetails, CardFrame, CardHead, Facts, MainKey, SecondKey } from "./card";
import { useMachines, useWhoList } from "./data";
import { t, useLang } from "./i18n";
import { openChat } from "./route";
import { showSecret } from "./secrets";
import { useList } from "./store";
import { errText, toast } from "./toast";
import { agentName, ago, Switch, titleOf } from "./ui";

export { confirmTitle, confirmName, ConfirmIcon };

/** at: where it is drawn (the asking session's id, or "terminals"): the secret shows there. */
export function ConfirmCard({ c, at: here }: { c: Need; at: string }) {
  if (c.kind === "ask") return <AskNeedCard c={c} />;
  if (c.kind === "secret_set") return <SecretSetCard c={c} />;
  if (c.kind === "secret_use") return <SecretUseCard c={c} />;
  if (c.kind === "machine_grant") return <MachineGrantCard c={c} />;
  if (isPair(c)) return <PairCard c={c} />;
  if (c.kind === "app_change" || c.kind === "login_join") return <AppNeedCard c={c} />;
  return <SecretGiveCard c={c} at={here} />;
}

function SecretGiveCard({ c, at: here }: { c: Need; at: string }) {
  const lang = useLang();
  const list = useList();
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState<"allow" | "deny" | null>(null);
  const [leaving, setLeaving] = useState(false);
  const from = list?.find((s) => s.id === c.session);
  const who = from ? titleOf(from) : c.asker;
  const answer = async (allow: boolean) => {
    setBusy(allow ? "allow" : "deny");
    try {
      const r = await api.answerConfirm(c.id, allow);
      setLeaving(true);
      if (r.invite) showSecret({ invite: r.invite, at: here });
      else if (r.state === "failed") toast(t("err.confirm"), { error: true, description: r.error });
      else toast(allow ? t("toast.allowed") : t("toast.denied"));
    } catch (e) {
      toast(t("err.confirm"), { error: true, description: errText(e) });
    } finally {
      setBusy(null);
    }
  };
  const at = (ms: number) => new Date(ms).toLocaleTimeString(lang === "zh" ? "zh-CN" : "en", { hour: "2-digit", minute: "2-digit" });
  return (
    <CardFrame label={confirmTitle(c)} leaving={leaving}>
      <CardHead
        icon={c.kind === "invite" ? <Link2 size={16} /> : <KeyRound size={16} />}
        title={confirmTitle(c)}
        tag={c.name}
        summary={t("confirm.asked", { who })}
        open={open}
        setOpen={setOpen}
        keys={
          <>
            <SecondKey disabled={!!busy} onClick={() => void answer(false)}>
              {t("ask.deny")}
            </SecondKey>
            <MainKey busy={busy === "allow"} disabled={!!busy} onClick={() => void answer(true)}>
              {t("confirm.allow")}
            </MainKey>
          </>
        }
      />
      {open && (
        <CardDetails>
          <Facts
            rows={[
              [t("confirm.asker"), c.asker],
              ...(from ? ([[t("confirm.session"), <button type="button" onClick={() => openChat(from.id)} className="text-left text-ink underline decoration-ink-4 underline-offset-2">{titleOf(from)}</button>]] as [string, React.ReactNode][]) : []),
              [t("confirm.until"), at(c.expiresAt)],
            ]}
          />
          <p className="m-0 text-xs break-words text-ink-3">{t("confirm.inviteHint")}</p>
        </CardDetails>
      )}
    </CardFrame>
  );
}

/** Who asked, and the card's time: the asking session's title (or the terminal), and until when. */
function useAsked(c: Need) {
  const lang = useLang();
  const list = useList();
  const from = list?.find((s) => s.id === c.session);
  const who = from ? titleOf(from) : c.asker;
  const until = new Date(c.expiresAt).toLocaleTimeString(lang === "zh" ? "zh-CN" : "en", { hour: "2-digit", minute: "2-digit" });
  const facts: [string, ReactNode][] = [
    [t("confirm.asker"), c.asker],
    ...(from ? ([[t("confirm.session"), <button type="button" onClick={() => openChat(from.id)} className="text-left text-ink underline decoration-ink-4 underline-offset-2">{titleOf(from)}</button>]] as [string, ReactNode][]) : []),
    [t("confirm.until"), until],
  ];
  return { who, facts };
}

/** An agent's ir secret set: the value is typed here, goes to the daemon, and is never shown again;
 *  the agent learns only that it is stored. Enter stores it. */
function SecretSetCard({ c }: { c: Need }) {
  const { facts } = useAsked(c);
  const [value, setValue] = useState("");
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState<"allow" | "deny" | null>(null);
  const [leaving, setLeaving] = useState(false);
  const box = useRef<HTMLInputElement>(null);
  const answer = async (allow: boolean) => {
    if (allow && !value) return box.current?.focus();
    setBusy(allow ? "allow" : "deny");
    try {
      const r = await api.answerConfirm(c.id, allow, allow ? { value } : undefined);
      setValue("");
      setLeaving(true);
      if (r.state === "failed") toast(t("err.confirm"), { error: true, description: r.error });
      else toast(allow ? t("secrets.stored", { name: c.name ?? "" }) : t("toast.denied"));
    } catch (e) {
      toast(t("err.confirm"), { error: true, description: errText(e) });
    } finally {
      setBusy(null);
    }
  };
  return (
    <CardFrame label={confirmTitle(c)} leaving={leaving}>
      <CardHead
        icon={<LockKeyhole size={16} />}
        title={c.replace ? t("secrets.replaceTitle") : confirmTitle(c)}
        tag={c.name}
        // What it is for, alone: who asked is in the details (the user, 2026-11-06).
        summary={c.note}
        open={open}
        setOpen={setOpen}
        keys={
          <>
            <SecondKey disabled={!!busy} onClick={() => void answer(false)}>
              {t("ask.deny")}
            </SecondKey>
            <MainKey busy={busy === "allow"} disabled={!!busy || !value} onClick={() => void answer(true)}>
              {t("secrets.store")}
            </MainKey>
          </>
        }
      />
      <form
        className="mt-2.5 pl-[26px]"
        onSubmit={(e) => {
          e.preventDefault();
          void answer(true);
        }}
      >
        <input
          ref={box}
          type="password"
          value={value}
          onChange={(e) => setValue(e.target.value)}
          autoComplete="new-password"
          spellCheck={false}
          name={`secret-${c.name}`}
          placeholder={t("secrets.valueOf", { name: c.name ?? "" })}
          aria-label={t("secrets.valueOf", { name: c.name ?? "" })}
          className="h-9 w-full rounded-md bg-fill px-2.5 font-mono text-sm outline-none placeholder:font-sans placeholder:text-ink-3 focus:bg-fill-2 pointer-coarse:h-11"
        />
      </form>
      {open && (
        <CardDetails>
          <Facts rows={facts} />
          <p className="m-0 text-xs text-ink-3">{t("secrets.setHint")}</p>
        </CardDetails>
      )}
    </CardFrame>
  );
}

/** A computer turned down an agent's request (docs/exec.md "放开"): command lines off there, or a
 *  folder it does not serve. 放开 asks the computer, which asks the person at its terminal (y); the
 *  card waits for that answer, saying where it is given. */
function MachineGrantCard({ c }: { c: Need }) {
  const { who, facts } = useAsked(c);
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState<"allow" | "deny" | null>(null);
  const [leaving, setLeaving] = useState(false);
  const name = c.name ?? "";
  const answer = async (allow: boolean) => {
    setBusy(allow ? "allow" : "deny");
    try {
      const r = await api.answerConfirm(c.id, allow);
      setLeaving(true);
      if (r.state === "failed") toast(t("err.confirm"), { error: true, description: r.error });
      else if (!allow) toast(t("toast.denied"));
      else if (r.state === "allowed") toast(t("grant.done"));
      else toast(t("grant.declined", { name }), { error: true });
    } catch (e) {
      toast(t("err.confirm"), { error: true, description: errText(e) });
    } finally {
      setBusy(null);
    }
  };
  const waiting = busy === "allow";
  return (
    <CardFrame label={confirmTitle(c)} leaving={leaving}>
      <CardHead
        icon={<ConfirmIcon c={c} size={16} />}
        title={confirmTitle(c)}
        summary={waiting ? t("grant.waiting", { name }) : c.shell ? t("confirm.asked", { who }) : c.path}
        mono={!waiting && !c.shell}
        open={open}
        setOpen={setOpen}
        keys={
          <>
            <SecondKey disabled={!!busy} onClick={() => void answer(false)}>
              {t("ask.deny")}
            </SecondKey>
            <MainKey busy={waiting} disabled={!!busy} onClick={() => void answer(true)}>
              {t("grant.allow")}
            </MainKey>
          </>
        }
      />
      {open && (
        <CardDetails>
          <Facts rows={[[t("grant.computer"), name], ...(c.path ? ([[t("grant.folder"), <span className="font-mono text-xs break-all">{c.path}</span>]] as [string, ReactNode][]) : []), ...facts]} />
          <p className="m-0 text-xs text-ink-3">{t("grant.hint")}</p>
        </CardDetails>
      )}
    </CardFrame>
  );
}

/** ir run in a session whose opener is gone (or of a secret confirmed every time): allow it once,
 *  for the rest of the session, or not. */
function SecretUseCard({ c }: { c: Need }) {
  const { who, facts } = useAsked(c);
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState<"allow" | "session" | "deny" | null>(null);
  const [leaving, setLeaving] = useState(false);
  const answer = async (allow: boolean, forSession = false) => {
    setBusy(!allow ? "deny" : forSession ? "session" : "allow");
    try {
      const r = await api.answerConfirm(c.id, allow, forSession ? { forSession } : undefined);
      setLeaving(true);
      if (r.state === "failed") toast(t("err.confirm"), { error: true, description: r.error });
      else toast(allow ? t("toast.allowed") : t("toast.denied"));
    } catch (e) {
      toast(t("err.confirm"), { error: true, description: errText(e) });
    } finally {
      setBusy(null);
    }
  };
  const names = confirmName(c);
  return (
    <CardFrame label={confirmTitle(c)} leaving={leaving}>
      <CardHead
        icon={<TermIcon size={16} />}
        title={confirmTitle(c)}
        tag={names}
        mono
        summary={c.command || t("confirm.asked", { who })}
        open={open}
        setOpen={setOpen}
        keys={
          <>
            <SecondKey disabled={!!busy} onClick={() => void answer(false)}>
              {t("ask.deny")}
            </SecondKey>
            {!c.always && (
              <SecondKey disabled={!!busy} onClick={() => void answer(true, true)}>
                {t("secrets.forSession")}
              </SecondKey>
            )}
            <MainKey busy={busy === "allow"} disabled={!!busy} onClick={() => void answer(true)}>
              {t("secrets.allowOnce")}
            </MainKey>
          </>
        }
      />
      {open && (
        <CardDetails>
          <Facts rows={[[t("secrets.names"), names], ...(c.command ? ([[t("secrets.command"), <code className="font-mono text-xs">{c.command}</code>]] as [string, ReactNode][]) : []), ...facts]} />
          <p className="m-0 text-xs text-ink-3">{c.always ? t("secrets.alwaysHint") : t("secrets.useHint")}</p>
        </CardDetails>
      )}
    </CardFrame>
  );
}

const systems: Record<string, string> = { windows: "Windows", darwin: "macOS", linux: "Linux" };

/** Whether command lines would run at a machine that asks to join, as it says (docs/exec.md "握手"):
 *  none (its agents only), a person's on the page, or agents' too (ir sh). */
const shellOf = (c: Need) => (c.pair?.shell ? "agents" : "none");

/** The machine a pairing card is for: its system, agents and commands, e.g. "Windows · dsh 0.2.2 · 可执行命令". */
export const machineLine = (c: Need) =>
  [
    systems[c.pair?.os ?? ""],
    ...(c.pair?.agents ?? []).map((a) => [a.agent, a.version].filter(Boolean).join(" ")),
    t(`confirm.shell.${shellOf(c)}`),
  ]
    .filter(Boolean)
    .join(" · ");

/** An app asks to pair (draft 二「接入」): allowed when its code matches the one shown there; the
 *  TOKEN goes to it, never to this page. app_update: an app by that name is there; allowing gives
 *  it a new TOKEN (the old one void, its usage and permissions as they were). */
function PairCard({ c }: { c: Need }) {
  const computer = !!c.pair?.os;
  const update = c.kind === "app_update";
  const { facts } = useAsked(c);
  const [asNew, setAsNew] = useState(false);
  const [newName, setNewName] = useState("");
  const [struck, setStruck] = useState<string[]>([]);
  // The agents it carries, struck: turned off there from the start (app_join).
  const [off, setOff] = useState<string[]>([]);
  const carried = (!update && c.pair?.agents?.map((a) => a.agent)) || [];
  const given = c.wants && struck.length ? { agents: c.wants.agents.filter((a) => !struck.includes(`a:${a}`)), scope: c.wants.scope.filter((p) => !struck.includes(`s:${p}`)) } : undefined;
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState<"allow" | "deny" | null>(null);
  const [leaving, setLeaving] = useState(false);
  const answer = async (allow: boolean) => {
    setBusy(allow ? "allow" : "deny");
    try {
      const name = update && asNew ? newName.trim() : "";
      const r = await api.answerConfirm(c.id, allow, allow ? { ...(name ? { name } : {}), ...(given ? { perms: given } : {}), ...(off.length ? { agentsOff: off } : {}) } : undefined);
      setLeaving(true);
      if (r.state === "failed") toast(t("err.confirm"), { error: true, description: r.error });
      else toast(allow ? t("confirm.pairDone", { name: r.name ?? (name || c.name) ?? "" }) : t("toast.denied"));
    } catch (e) {
      toast(t("err.confirm"), { error: true, description: errText(e) });
    } finally {
      setBusy(null);
    }
  };
  const m = c.pair;
  const machines = useMachines();
  return (
    <CardFrame label={confirmTitle(c)} leaving={leaving}>
      <CardHead
        icon={<ConfirmIcon c={c} size={16} />}
        title={confirmTitle(c)}
        tag={computer ? machineLine(c) : m?.program || m?.type || t("term.channel")}
        summary={t("confirm.pairCode", { code: c.code ?? "" })}
        open={open}
        setOpen={setOpen}
        keys={
          <>
            <SecondKey disabled={!!busy} onClick={() => void answer(false)}>
              {t("ask.deny")}
            </SecondKey>
            <MainKey busy={busy === "allow"} disabled={!!busy || (update && asNew && !newName.trim())} onClick={() => void answer(true)}>
              {t("confirm.allow")}
            </MainKey>
          </>
        }
        extra={
          <>
            {update && (
              <>
                <p className={`m-0 text-xs ${c.app?.online && !asNew ? "text-err" : "text-ink-3"}`}>{asNew ? t("confirm.asNewHint") : c.app?.online ? t("confirm.updateOnline", { name: c.name ?? "" }) : t("confirm.updateHint")}</p>
                <div className="flex items-center gap-2">
                  <span className="min-w-0 flex-1 text-sm">{t("confirm.asNew")}</span>
                  <Switch label={t("confirm.asNew")} on={asNew} onChange={setAsNew} />
                </div>
                {asNew && (
                  <input autoFocus value={newName} maxLength={12} onChange={(e) => setNewName(e.target.value)} placeholder={t("term.name")} aria-label={t("term.name")} className="g-fade no-ring h-8 rounded-md bg-fill px-2.5 text-base placeholder:text-ink-3" />
                )}
              </>
            )}
            {m?.login && <p className="m-0 text-xs text-ink-3">{t("confirm.pairLogin", { login: m.login.name || m.login.id })}</p>}
            {carried.length > 0 && <Carried agents={carried} off={off} setOff={setOff} />}
            {(!update || asNew) && c.wants && <Wants wants={c.wants} struck={struck} setStruck={setStruck} />}
          </>
        }
      />
      {open && (
        <CardDetails>
          {update && <Compare c={c} was={machines.find((x) => x.id === c.app?.id)} />}
          <Facts
            rows={[
              ...(computer && !update ? ([[t("confirm.system"), [systems[m?.os ?? ""], m?.arch].filter(Boolean).join(" ") || "—"]] as [string, ReactNode][]) : []),
              ...(computer ? ([[t("confirm.commands"), t(`confirm.commands.${shellOf(c)}`)]] as [string, ReactNode][]) : []),
              ...(m?.program && !update ? ([[t("confirm.program"), m.program]] as [string, ReactNode][]) : []),
              ...facts.slice(-1),
            ]}
          />
          <p className="m-0 text-xs text-ink-3">{computer ? t("confirm.pairHint") : t("confirm.pairBotHint")}</p>
        </CardDetails>
      )}
    </CardFrame>
  );
}

/** An update's two sides (decision 11): the app there now (was: its computer, if it is one) and
 *  the one asking, side by side: system, program, agents, last seen; the one there now in red
 *  while online. */
function Compare({ c, was }: { c: Need; was?: Machine }) {
  const m = c.pair;
  const system = (os?: string, arch?: string) => [systems[os ?? ""] ?? os, arch].filter(Boolean).join(" ") || "—";
  const agents = (list?: ExecAgent[]) => (list?.length ? list.map((a) => [a.agent, a.version].filter(Boolean).join(" ")).join("、") : "—");
  const rows: [string, ReactNode, ReactNode][] = [
    [t("confirm.system"), was ? system(was.os, was.arch) : "—", system(m?.os, m?.arch)],
    [t("confirm.program"), was?.program || "—", m?.program || "—"],
    [t("apps.carried"), agents(was?.agents), agents(m?.agents)],
    [t("confirm.lastSeen"), c.app?.online ? <span className="text-err">{t("term.online")}</span> : ago(c.app?.lastSeenAt || c.app?.createdAt), ago(c.createdAt)],
  ];
  return (
    <div className="grid grid-cols-[auto_1fr_1fr] gap-x-3 gap-y-1">
      <span />
      <span className="text-ink-3">{t("confirm.old")}</span>
      <span className="text-ink-3">{t("confirm.new")}</span>
      {rows.map(([k, a, b]) => (
        <div key={k} className="contents">
          <span className="text-ink-3">{k}</span>
          <span className="min-w-0 break-all text-ink-2">{a}</span>
          <span className="min-w-0 break-all text-ink-2">{b}</span>
        </div>
      ))}
    </div>
  );
}

const ALL = "*";
const permWord = (v: string, kind: "a" | "s") => (v === ALL ? t(kind === "a" ? "apps.allAgents" : "apps.allScope") : v);

/** What a pairing app asks for, each a chip a person may strike out: what is allowed is the rest. */
/** The agents a computer asking to pair carries: each a chip, tapped off to turn it off there. */
function Carried({ agents, off, setOff }: { agents: string[]; off: string[]; setOff: (s: string[]) => void }) {
  return (
    <div className="flex flex-col gap-1">
      <span className="text-xs text-ink-3">{t("confirm.carried")}</span>
      <div className="flex flex-wrap gap-1">
        {agents.map((a) => {
          const no = off.includes(a);
          return (
            <button key={a} type="button" aria-pressed={!no} onClick={() => setOff(no ? off.filter((x) => x !== a) : [...off, a])} className={`g-press h-6 max-w-full truncate rounded-full px-2 text-xs ${no ? "bg-fill text-ink-4 line-through" : "bg-fill-2 text-ink"}`}>
              {agentName(a as Agent)}
            </button>
          );
        })}
      </div>
    </div>
  );
}

function Wants({ wants, struck, setStruck }: { wants: Perms; struck: string[]; setStruck: (s: string[]) => void }) {
  const chip = (key: string, label: string) => {
    const off = struck.includes(key);
    return (
      <button key={key} type="button" aria-pressed={!off} onClick={() => setStruck(off ? struck.filter((k) => k !== key) : [...struck, key])} className={`g-press h-6 max-w-full truncate rounded-full px-2 text-xs ${off ? "bg-fill text-ink-4 line-through" : "bg-fill-2 text-ink"}`}>
        {label}
      </button>
    );
  };
  return (
    <div className="flex flex-col gap-1">
      <span className="text-xs text-ink-3">{t("confirm.wants")}</span>
      <div className="flex flex-wrap gap-1">
        {wants.agents.map((a) => chip(`a:${a}`, permWord(a, "a")))}
        {wants.scope.map((p) => chip(`s:${p}`, permWord(p, "s")))}
      </div>
    </div>
  );
}

/** What app_change asks for over what it has: each more (+) and less (−). */
const changeLines = (c: Need): [string, ReactNode][] => {
  const has = c.has ?? { agents: [ALL], scope: [ALL] };
  const wants = c.wants ?? { agents: [], scope: [] };
  const diff = (a: string[], b: string[], kind: "a" | "s") => [...b.filter((v) => !a.includes(v)).map((v) => `+ ${permWord(v, kind)}`), ...a.filter((v) => !b.includes(v)).map((v) => `− ${permWord(v, kind)}`)].join("、");
  const rows: [string, ReactNode][] = [];
  const agents = diff(has.agents, wants.agents, "a");
  const scope = diff(has.scope, wants.scope, "s");
  if (agents) rows.push([t("apps.agentsShort"), agents]);
  if (scope) rows.push([t("apps.scope"), scope]);
  return rows;
};

/** An app asks for other permissions (app_change), or a login of an app spoke the first time
 *  (login_join): allow or deny. (0.2 S1c draws what changes.) */
function AppNeedCard({ c }: { c: Need }) {
  const { facts } = useAsked(c);
  const [open, setOpen] = useState(c.kind === "app_change");
  const [busy, setBusy] = useState<"allow" | "deny" | null>(null);
  const [leaving, setLeaving] = useState(false);
  const answer = async (allow: boolean) => {
    setBusy(allow ? "allow" : "deny");
    try {
      const r = await api.answerConfirm(c.id, allow);
      setLeaving(true);
      if (r.state === "failed") toast(t("err.confirm"), { error: true, description: r.error });
      else toast(allow ? t("toast.allowed") : t("toast.denied"));
    } catch (e) {
      toast(t("err.confirm"), { error: true, description: errText(e) });
    } finally {
      setBusy(null);
    }
  };
  return (
    <CardFrame label={confirmTitle(c)} leaving={leaving}>
      <CardHead
        icon={<ConfirmIcon c={c} size={16} />}
        title={confirmTitle(c)}
        tag={c.app?.name}
        summary={c.kind === "login_join" ? t("confirm.loginHint") : t("confirm.changeHint")}
        open={open}
        setOpen={setOpen}
        keys={
          <>
            <SecondKey disabled={!!busy} onClick={() => void answer(false)}>
              {t("ask.deny")}
            </SecondKey>
            <MainKey busy={busy === "allow"} disabled={!!busy} onClick={() => void answer(true)}>
              {t("confirm.allow")}
            </MainKey>
          </>
        }
      />
      {open && (
        <CardDetails>
          <Facts rows={[...(c.kind === "app_change" ? changeLines(c) : []), ...facts.slice(-1)]} />
        </CardDetails>
      )}
    </CardFrame>
  );
}

/** A session's ask in the needs list (k_glossary "需要"): one choice answers it here, through its
 *  card as the session's own panel does, when its keys fit a row (two, short); one of more choices
 *  (several questions, many or long options) opens the session. */
function AskNeedCard({ c }: { c: Need }) {
  const list = useList();
  const who = useWhoName(c.origin);
  const card = c.card;
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState<string | null>(null);
  const [leaving, setLeaving] = useState(false);
  const from = list?.find((s) => s.id === c.session);
  const fits = !!card && card.options.length <= 2 && card.options.every((o) => [...o.text].length <= 8);
  const simple = !!card && fits && (card.kind !== "question" || (card.summary.length <= 1 && !card.multi?.length));
  const answer = async (option: string) => {
    if (!card || !c.session) return;
    setBusy(option);
    try {
      await api.answer(c.session, { card: card.id, option });
      setLeaving(true);
    } catch (e) {
      toast(t("err.answer"), { error: true, description: errText(e) });
    } finally {
      setBusy(null);
    }
  };
  const go = () => c.session && openChat(c.session);
  return (
    <CardFrame label={confirmTitle(c)} leaving={leaving}>
      <CardHead
        icon={<ShieldQuestion size={16} />}
        title={confirmTitle(c)}
        tag={from ? titleOf(from) : c.name}
        summary={card?.summary[0] ?? card?.title}
        open={open}
        setOpen={setOpen}
        keys={
          simple && card ? (
            card.options.map((o) =>
              o.primary ? (
                <MainKey key={o.id} busy={busy === o.id} disabled={!!busy} onClick={() => void answer(o.id)}>
                  {o.text}
                </MainKey>
              ) : (
                <SecondKey key={o.id} disabled={!!busy} onClick={() => void answer(o.id)}>
                  {o.text}
                </SecondKey>
              ),
            )
          ) : (
            <MainKey onClick={go}>{t("need.openSession")}</MainKey>
          )
        }
      />
      {open && (
        <CardDetails>
          <Facts
            rows={[
              ...(card ? ([[card.title, card.summary.join("\n")]] as [string, ReactNode][]) : []),
              ...(who ? ([[t("need.origin"), who]] as [string, ReactNode][]) : []),
              [t("confirm.session"), <button type="button" onClick={go} className="text-left text-ink underline decoration-ink-4 underline-offset-2">{from ? titleOf(from) : c.name}</button>],
            ]}
          />
        </CardDetails>
      )}
    </CardFrame>
  );
}

/** The name of an app or login by its ID, as the list of who is there has it. */
function useWhoName(id?: string) {
  const who = useWhoList();
  if (!id) return undefined;
  return who.find((w) => w.id === id)?.name ?? id;
}

