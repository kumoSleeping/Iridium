// A confirmation an agent's ir asked a panel for (docs/api.md "确认"): a new panel's invite or a
// plugin's token, which must not reach the agent. Its card sits above the asking session's
// composer and on the settings' 终端 page, and the sidebar lists it under 需要你. Allowed, the
// secret goes to this panel alone, in a card where this one was (secret.tsx); the agent only learns how it went. One
// asked with --owner (the new terminal gets Claude) only a panel that has Claude allows.
// A credential's (plan "凭据"): secret-set has a password box, whose value goes to the daemon and
// nowhere else; secret-use is allow, this session, or deny, and only the owner's own allows it.
import { Bot, KeyRound, Link2, LockKeyhole, Monitor, Terminal as TermIcon } from "lucide-react";
import { ConfirmIcon, confirmName, confirmTitle } from "./confirmname";
import { useRef, useState, type ReactNode } from "react";
import { api } from "./api/client";
import type { Confirm, Me } from "./api/types.gen";
import { CardDetails, CardFrame, CardHead, Facts, MainKey, SecondKey } from "./card";
import { useMe } from "./data";
import { t, useLang } from "./i18n";
import { openChat } from "./route";
import { showSecret } from "./secrets";
import { useList } from "./store";
import { errText, toast } from "./toast";
import { Switch, titleOf } from "./ui";

export { confirmTitle, confirmName, ConfirmIcon };

/** This terminal may say a new one is the owner's own (--owner): a passkey panel that has Claude
 *  itself. The server checks the same (ownerSays); 本机's ir may too. */
export const saysOwner = (me?: Me) => !!me && me.kind === "panel" && !me.local && !!me.rules.claudeOwner;

/** at: where it is drawn (the asking session's id, or "terminals"): the secret shows there. */
export function ConfirmCard({ c, at: here }: { c: Confirm; at: string }) {
  if (c.kind === "secret-set") return <SecretSetCard c={c} />;
  if (c.kind === "secret-use") return <SecretUseCard c={c} />;
  if (c.kind === "exec-pair" || c.kind === "bot-pair") return <ExecPairCard c={c} />;
  return <SecretGiveCard c={c} at={here} />;
}

function SecretGiveCard({ c, at: here }: { c: Confirm; at: string }) {
  const lang = useLang();
  const list = useList();
  const me = useMe().data;
  const blocked = !!c.owner && !saysOwner(me);
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
      else if (r.token?.into) toast(t("confirm.tokenWritten", { path: r.token.into }));
      else if (r.token) showSecret({ token: r.token, at: here });
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
        tag={c.owner ? [c.name, t("term.withClaude")].filter(Boolean).join(" · ") : c.name}
        summary={blocked ? t("confirm.ownerElsewhere") : t("confirm.asked", { who })}
        open={open}
        setOpen={setOpen}
        keys={
          <>
            <SecondKey disabled={!!busy} onClick={() => void answer(false)}>
              {t("ask.deny")}
            </SecondKey>
            <MainKey busy={busy === "allow"} disabled={!!busy || blocked} onClick={() => void answer(true)}>
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
          <p className="m-0 text-xs break-words text-ink-3">{c.kind === "invite" ? t("confirm.inviteHint") : c.into ? t("confirm.tokenInto", { path: c.into }) : t("confirm.tokenHint")}</p>
          {c.owner && <p className="m-0 text-xs text-ink-3">{blocked ? t("confirm.ownerElsewhere") : t("confirm.ownerHint")}</p>}
        </CardDetails>
      )}
    </CardFrame>
  );
}

/** Who asked, and the card's time: the asking session's title (or the terminal), and until when. */
function useAsked(c: Confirm) {
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
function SecretSetCard({ c }: { c: Confirm }) {
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

/** ir run in a session another terminal opened (or of a secret confirmed every time): allow it
 *  once, for the rest of the session, or not. Only the owner's own allows it. */
function SecretUseCard({ c }: { c: Confirm }) {
  const me = useMe().data;
  const blocked = !saysOwner(me);
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
        mono={!blocked}
        summary={blocked ? t("secrets.ownerElsewhere") : c.command || t("confirm.asked", { who })}
        open={open}
        setOpen={setOpen}
        keys={
          <>
            <SecondKey disabled={!!busy} onClick={() => void answer(false)}>
              {t("ask.deny")}
            </SecondKey>
            {!c.always && (
              <SecondKey disabled={!!busy || blocked} onClick={() => void answer(true, true)}>
                {t("secrets.forSession")}
              </SecondKey>
            )}
            <MainKey busy={busy === "allow"} disabled={!!busy || blocked} onClick={() => void answer(true)}>
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
const shellOf = (c: Confirm) => (c.machine?.noShell ? "none" : c.machine?.agentShell ? "agents" : "page");

/** The machine an exec-pair card is for: its system, agents and commands, e.g. "Windows · dsh 0.2.2 · 可执行命令". */
export const machineLine = (c: Confirm) =>
  [
    systems[c.machine?.os ?? ""],
    ...(c.machine?.agents ?? []).map((a) => [a.agent, a.version].filter(Boolean).join(" ")),
    t(`confirm.shell.${shellOf(c)}`),
  ]
    .filter(Boolean)
    .join(" · ");

/** Another machine's executor (exec-pair) or a Bot (bot-pair) asks to join (docs/exec.md "连接"):
 *  allowed when its code matches the one shown there; the TOKEN goes to it, never to this page. A
 *  panel that has Claude may say it is the owner's own machine (or Bot), which then has Claude. A
 *  Bot by its name already there may be replaced: the same one, a new TOKEN, its Claude as it was. */
function ExecPairCard({ c }: { c: Confirm }) {
  const me = useMe().data;
  const bot = c.kind === "bot-pair";
  const { facts } = useAsked(c);
  const [own, setOwn] = useState(false);
  const [replace, setReplace] = useState(false);
  const canOwn = saysOwner(me) && !replace;
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState<"allow" | "deny" | null>(null);
  const [leaving, setLeaving] = useState(false);
  const answer = async (allow: boolean) => {
    setBusy(allow ? "allow" : "deny");
    try {
      const r = await api.answerConfirm(c.id, allow, allow ? { owner: canOwn && own, replace: !!c.replace && replace } : undefined);
      setLeaving(true);
      if (r.state === "failed") toast(t("err.confirm"), { error: true, description: r.error });
      else toast(allow ? t("confirm.pairDone", { name: r.name ?? c.name ?? "" }) : t("toast.denied"));
    } catch (e) {
      toast(t("err.confirm"), { error: true, description: errText(e) });
    } finally {
      setBusy(null);
    }
  };
  const m = c.machine;
  return (
    <CardFrame label={confirmTitle(c)} leaving={leaving}>
      <CardHead
        icon={bot ? <Bot size={16} /> : <Monitor size={16} />}
        title={confirmTitle(c)}
        tag={bot ? m?.program || t("term.channel") : machineLine(c)}
        summary={t("confirm.pairCode", { code: c.code ?? "" })}
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
      {c.replace && (
        <div className="mt-2.5 flex items-center gap-2 pl-[26px]">
          <span className="min-w-0 flex-1">
            <span className="block text-sm">{t("confirm.pairReplace", { name: c.name ?? "" })}</span>
            <span className="block text-xs text-ink-3">{t("confirm.pairReplaceHint")}</span>
          </span>
          <Switch label={t("confirm.pairReplace", { name: c.name ?? "" })} on={replace} onChange={setReplace} />
        </div>
      )}
      {canOwn && (
        <div className="g-fade mt-2.5 flex items-center gap-2 pl-[26px]">
          <span className="min-w-0 flex-1">
            <span className="block text-sm">{bot ? t("term.withClaude") : t("confirm.pairOwner")}</span>
            <span className="block text-xs text-ink-3">{bot ? t("confirm.pairBotOwnerHint") : t("confirm.pairOwnerHint")}</span>
          </span>
          <Switch label={bot ? t("term.withClaude") : t("confirm.pairOwner")} on={own} onChange={setOwn} />
        </div>
      )}
      {open && (
        <CardDetails>
          <Facts
            rows={[
              ...(bot ? [] : ([[t("confirm.system"), [systems[m?.os ?? ""], m?.arch].filter(Boolean).join(" ") || "—"]] as [string, ReactNode][])),
              ...(bot ? [] : ([[t("confirm.commands"), t(`confirm.commands.${shellOf(c)}`)]] as [string, ReactNode][])),
              ...(m?.program ? ([[t("confirm.program"), m.program]] as [string, ReactNode][]) : []),
              ...facts.slice(-1),
            ]}
          />
          <p className="m-0 text-xs text-ink-3">{bot ? t("confirm.pairBotHint") : t("confirm.pairHint")}</p>
        </CardDetails>
      )}
    </CardFrame>
  );
}
