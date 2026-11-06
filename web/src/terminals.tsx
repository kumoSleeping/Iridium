// The settings' 终端 page (plan "交互终端", docs/api.md "交互终端"): every interactive terminal —
// 本机, the web panels, the channels (plugins, peers) — with its avatar, name, kind, when it was
// last used and today's use; one opened shows its name and its permission table to change (Claude
// only shows: the web may turn it off, never on; that is ir's on the machine), and revoking it. The +
// makes a new panel's invite (its link and QR code) or a plugin's token, shown once. What agents'
// ir asked for waits on top. Changes are saved at once.
import { Popover } from "@base-ui/react/popover";
import { useQueryClient, type QueryClient } from "@tanstack/react-query";
import { ChevronLeft, ChevronRight, KeyRound, Link2, LoaderCircle, Plus, Trash2, X } from "lucide-react";
import { useEffect, useState, type ReactNode } from "react";
import { ApiError, api } from "./api/client";
import type { Agent, Rules, Terminal, Terminals } from "./api/types.gen";
import { CardFrame, CardHead, MainKey, SecondKey } from "./card";
import { ConfirmCard } from "./confirms";
import { useMe, useTerminals, useWorkspaces } from "./data";
import { t, useLang } from "./i18n";
import { LocalMark, Segmented } from "./me";
import { showSecret } from "./secrets";
import { Group, Item } from "./settings";
import { useConfirms } from "./store";
import { errText, toast } from "./toast";
import { agentName, AGENTS, ago, Avatar, cn, IconKey, Logo, PopSurface, Switch, tilde, Tip } from "./ui";


export const tokens = (n: number) => (n >= 1_000_000 ? `${(n / 1_000_000).toFixed(1)}M` : n >= 1000 ? `${Math.round(n / 100) / 10}k` : `${n}`);

const kindName = (x: Terminal) => (x.local ? t("term.local") : x.kind === "panel" ? t("term.panel") : t("term.channel"));

export function TerminalsPage({ onBack }: { onBack: () => void }) {
  useLang();
  const qc = useQueryClient();
  const q = useTerminals();
  const confirms = useConfirms();
  const [open, setOpen] = useState<string | null>(null);
  const list = q.data?.terminals ?? [];
  const invites = q.data?.invites ?? [];
  return (
    <div className="relative h-full">
      <header className="glass absolute inset-x-0 top-0 z-20 flex h-11 items-center gap-1 pr-1.5 pl-1.5 shadow-[0_0.5px_0_var(--edge)] pointer-coarse:h-12">
        <IconKey label={t("nav.settings")} onClick={onBack} tipSide="bottom" className="-mr-1">
          <ChevronLeft size={20} />
        </IconKey>
        <h1 className="m-0 min-w-0 flex-1 truncate text-base font-semibold">{t("settings.terminals")}</h1>
        {q.isFetching && <LoaderCircle size={14} className="animate-spin text-ink-4" />}
        <NewTerminal />
      </header>
      <div className="absolute inset-0 overflow-y-auto pt-14 pb-4 pointer-coarse:pt-15">
        <div className="flex flex-col gap-5 px-3">
          {confirms.length > 0 && (
            <div className="flex flex-col gap-2">
              {confirms.map((c) => (
                <ConfirmCard key={c.id} c={c} />
              ))}
            </div>
          )}
          {q.isError && <p className="m-0 text-sm text-ink-3">{errText(q.error)}</p>}
          <section className="flex flex-col" aria-label={t("settings.terminals")}>
            {list.map((x) => (
              <TerminalRow key={x.id} x={x} open={open === x.id} toggle={() => setOpen(open === x.id ? null : x.id)} />
            ))}
          </section>
          {invites.length > 0 && (
            <Group title={t("term.invites")}>
              {invites.map((i) => (
                <div key={i.id} className="flex min-h-10 items-center gap-3">
                  <Link2 size={15} className="shrink-0 text-ink-3" />
                  <span className="min-w-0 flex-1 truncate text-base">{i.setup ? t("term.setup") : i.name || <span className="text-ink-3">{t("term.unnamed")}</span>}</span>
                  {i.owner && <span className="shrink-0 text-xs text-ink-3">{t("term.withClaude")}</span>}
                  {i.expiresAt ? <span className="text-xs text-ink-3 tabular-nums">{t("term.until", { time: clock(i.expiresAt) })}</span> : null}
                  <IconKey label={t("term.revoke")} onClick={() => void revoke(qc, i.id, i.name || t("term.unnamed"))} className="size-7">
                    <X size={15} />
                  </IconKey>
                </div>
              ))}
            </Group>
          )}
        </div>
      </div>
    </div>
  );
}

const clock = (ms: number) => new Date(ms).toLocaleTimeString(document.documentElement.lang || undefined, { hour: "2-digit", minute: "2-digit" });

/** Revokes terminal or invite id, then lists them again. */
async function revoke(qc: QueryClient, id: string, name: string) {
  try {
    await api.revoke(id);
    toast(t("term.revoked", { name }));
  } catch (e) {
    toast(t("err.revoke"), { error: true, description: errText(e) });
  }
  await qc.invalidateQueries({ queryKey: ["terminals"] });
}

/** One terminal's row, and opened, its page: name, permissions, revoke. */
function TerminalRow({ x, open, toggle }: { x: Terminal; open: boolean; toggle: () => void }) {
  const me = useMe().data;
  const used = x.online ? t("term.online") : x.local ? t("term.localShort") : ago(x.lastSeenAt || x.createdAt);
  return (
    <div className={cn("flex flex-col", open && "pb-4")}>
      <button type="button" onClick={toggle} aria-expanded={open} className="g-press flex min-h-14 items-center gap-3 rounded-lg px-2 text-left hover:bg-fill">
        {x.local ? <LocalMark size={32} /> : <Avatar src={x.avatar} name={x.name} size={32} channel={x.kind === "channel"} />}
        <span className="min-w-0 flex-1">
          <span className="flex items-center gap-2">
            <span className="truncate text-base font-medium">{x.local ? t("me.local") : x.name}</span>
            <span className="shrink-0 rounded-sm bg-fill-2 px-1.5 text-[11px] leading-[18px] text-ink-2">{kindName(x)}</span>
            {x.id === me?.id && !x.local && <span className="shrink-0 text-xs text-ink-3">{t("term.this")}</span>}
          </span>
          <span className="flex items-center gap-1.5 text-xs text-ink-3 tabular-nums">
            {x.online ? <span className="size-1.5 rounded-full bg-ink-3" aria-hidden /> : null}
            <span>{used}</span>
            <span aria-hidden>·</span>
            <span>{t("term.today", { n: tokens(x.usedToday) })}</span>
          </span>
        </span>
        <span className="hidden shrink-0 gap-1 sm:flex">
          {AGENTS.filter((a) => x.rules.agents?.includes(a)).map((a) => (
            <Logo key={a} agent={a} size={13} />
          ))}
        </span>
        <ChevronRight size={15} className="shrink-0 text-ink-4 transition-transform duration-(--dur-2)" style={{ transform: open ? "rotate(90deg)" : undefined }} />
      </button>
      {open && (x.local ? <p className="g-fade m-0 pt-1 pr-2 pl-[52px] text-sm text-ink-3">{t("term.localHint")}</p> : <Detail x={x} />)}
    </div>
  );
}

/** A terminal opened: its name, its permission table, revoking it. */
function Detail({ x }: { x: Terminal }) {
  const qc = useQueryClient();
  const [name, setName] = useState(x.name);
  const [sure, setSure] = useState(false);
  const [busy, setBusy] = useState(false);
  useEffect(() => setName(x.name), [x.name]);
  const rename = async () => {
    const to = name.trim();
    if (!to || to === x.name) return setName(x.name);
    try {
      await api.profile({ name: to }, x.id);
      void qc.invalidateQueries({ queryKey: ["terminals"] });
    } catch (e) {
      setName(x.name);
      const code = e instanceof ApiError ? (e.body as { nameError?: string } | undefined)?.nameError : undefined;
      toast(t("me.rename"), { error: true, description: code === "taken" ? t("me.nameTaken") : code ? t("me.nameBad") : errText(e) });
    }
  };
  return (
    <div className="g-fade flex flex-col gap-5 pt-2 pr-2 pl-2 sm:pl-[52px]">
      <Item label={t("term.rename")}>
        <input
          value={name}
          maxLength={12}
          aria-label={t("term.rename")}
          onChange={(e) => setName(e.target.value)}
          onBlur={() => void rename()}
          onKeyDown={(e) => e.key === "Enter" && !e.nativeEvent.isComposing && (e.currentTarget as HTMLInputElement).blur()}
          className="no-ring h-8 w-40 rounded-md bg-fill px-2 text-sm"
        />
      </Item>
      <RulesEditor x={x} />
      <div className="flex flex-col gap-2">
        {sure ? (
          <CardFrame label={t("term.revokeTitle", { name: x.name })}>
            <CardHead
              icon={<Trash2 size={16} />}
              title={t("term.revokeTitle", { name: x.name })}
              summary={t("term.revokeHint")}
              keys={
                <>
                  <SecondKey onClick={() => setSure(false)}>{t("me.cancel")}</SecondKey>
                  <MainKey busy={busy} onClick={() => (setBusy(true), void revoke(qc, x.id, x.name).finally(() => setBusy(false)))}>
                    {t("term.revoke")}
                  </MainKey>
                </>
              }
            />
          </CardFrame>
        ) : (
          <div className="flex items-center justify-between gap-3 text-xs text-ink-3">
            <span className="tabular-nums">
              {x.kind === "panel" ? `${t("term.passkeys")} ${x.passkeys ?? 0} · ${t("term.logins")} ${x.logins ?? 0} · ` : ""}
              {x.createdAt ? t("term.created", { time: new Date(x.createdAt).toLocaleDateString() }) : ""}
            </span>
            <button type="button" onClick={() => setSure(true)} className="g-press flex h-8 items-center gap-1.5 rounded-md px-2.5 text-sm text-err hover:bg-fill">
              <Trash2 size={14} />
              {t("term.revoke")}
            </button>
          </div>
        )}
      </div>
    </div>
  );
}

/** Claude is on: the server marked the terminal the owner's own. */
export const hasClaude = (r: Rules) => !!r.claudeOwner && (r.agents ?? []).includes("claude");

/** A terminal's permission table (docs/api.md "权限表"), each change saved at once. Claude only
 *  shows, on or off, and may be turned off: only a person at the machine turns it on (Claude's
 *  terms: the owner's own terminals only), with ir. */
function RulesEditor({ x }: { x: Terminal }) {
  const qc = useQueryClient();
  const ws = useWorkspaces().data ?? [];
  const [r, setR] = useState<Rules>(x.rules);
  const [saving, setSaving] = useState(false);
  useEffect(() => setR(x.rules), [x.rules]);
  const put = async (next: Rules) => {
    const was = r;
    setR(next);
    setSaving(true);
    try {
      const got = await api.setRules(x.id, { rules: next });
      qc.setQueryData<Terminals | null>(["terminals"], (d) => d && { ...d, terminals: d.terminals.map((y) => (y.id === x.id ? got : y)) });
    } catch (e) {
      setR(was);
      toast(t("err.rules"), { error: true, description: errText(e) });
    } finally {
      setSaving(false);
    }
  };
  const agents = r.agents ?? [];
  const claude = hasClaude(r);
  const toggleAgent = (a: Agent) => void put({ ...r, agents: agents.includes(a) ? agents.filter((y) => y !== a) : [...agents, a] });
  const flag = (k: "seeOthers" | "filesIn" | "filesOut" | "answer" | "mustApprove", label: string, hint?: string) => (
    <Item label={label} hint={hint}>
      <Switch label={label} on={!!r[k]} onChange={(v) => void put({ ...r, [k]: v })} />
    </Item>
  );
  return (
    <Group title={saving ? `${t("me.perms")} ·` : t("me.perms")}>
      <Item label={t("perm.agents")}>
        <div className="flex gap-1">
          {AGENTS.filter((a) => a !== "claude").map((a) => (
            <Tip key={a} label={agentName(a)}>
              <button
                type="button"
                aria-pressed={agents.includes(a)}
                aria-label={agentName(a)}
                onClick={() => toggleAgent(a)}
                className={cn("g-press grid size-8 place-items-center rounded-md pointer-coarse:size-10", agents.includes(a) ? "bg-paper shadow-[0_0_0_0.5px_var(--edge),0_1px_2px_var(--edge)]" : "opacity-35 grayscale hover:opacity-60")}
              >
                <Logo agent={a} size={16} />
              </button>
            </Tip>
          ))}
        </div>
      </Item>
      <Item label={agentName("claude")} hint={claude ? t("term.claudeOnHint") : t("term.claudeOffHint", { name: /\s/.test(x.name) ? `"${x.name}"` : x.name })}>
        <span className="flex items-center gap-1">
          <span className={cn("px-1 text-sm", claude ? "text-ink-2" : "text-ink-3")}>{claude ? t("term.on") : t("term.off")}</span>
          {claude && (
            <button type="button" onClick={() => void put({ ...r, agents: agents.filter((y) => y !== "claude"), claudeOwner: undefined })} className="g-press h-8 rounded-md px-2.5 text-sm text-ink-2 hover:bg-fill">
              {t("term.claudeTurnOff")}
            </button>
          )}
        </span>
      </Item>
      <Item label={t("term.places")} hint={r.allDirs ? undefined : placesHint(r, ws)}>
        <Segmented
          label={t("term.places")}
          value={r.allDirs ? "all" : "some"}
          onChange={(v) => void put({ ...r, allDirs: v === "all" })}
          items={[
            { v: "all", label: t("term.allPlaces"), icon: <span className="px-1 text-xs">{t("term.allPlaces")}</span> },
            { v: "some", label: t("term.somePlaces"), icon: <span className="px-1 text-xs">{t("term.somePlaces")}</span> },
          ]}
        />
      </Item>
      {!r.allDirs && (
        <div className="g-fade flex flex-col gap-2 pb-2">
          {ws.length > 0 && (
            <div className="flex flex-wrap gap-1.5">
              {ws.map((w) => {
                const on = (r.workspaces ?? []).includes(w.id);
                return (
                  <button key={w.id} type="button" aria-pressed={on} onClick={() => void put({ ...r, workspaces: on ? r.workspaces.filter((y) => y !== w.id) : [...(r.workspaces ?? []), w.id] })} className={cn("g-press h-7 rounded-md px-2.5 text-sm", on ? "bg-ink text-on-ink" : "bg-fill hover:bg-fill-2")}>
                    {w.name}
                  </button>
                );
              })}
            </div>
          )}
          <Lines label={t("term.dirs")} value={(r.dirs ?? []).map((d) => tilde(d)).join("\n")} save={(v) => void put({ ...r, dirs: v.split("\n").map((l) => l.trim()).filter(Boolean) })} />
        </div>
      )}
      {flag("seeOthers", t("perm.others"))}
      {flag("filesIn", t("perm.copyIn"))}
      {flag("filesOut", t("perm.copyOut"))}
      {flag("answer", t("perm.answer"))}
      {flag("mustApprove", t("perm.mustApprove"), t("term.mustApproveHint"))}
      <Item label={t("perm.daily")} hint={t("term.dailyHint")}>
        <NumberIn value={Math.round((r.dailyTokens ?? 0) / 1000)} save={(k) => void put({ ...r, dailyTokens: k * 1000 })} label={t("perm.daily")} />
      </Item>
      <Item label={t("term.allCommands")}>
        <Switch label={t("term.allCommands")} on={!!r.allCommands} onChange={(v) => void put({ ...r, allCommands: v })} />
      </Item>
      {!r.allCommands && <Lines label={t("term.commandsHint")} one value={(r.commands ?? []).join(" ")} save={(v) => void put({ ...r, commands: v.split(/\s+/).filter(Boolean).map((c) => (c.startsWith("/") ? c : `/${c}`)) })} />}
    </Group>
  );
}

const placesHint = (r: Rules, ws: { id: number; name: string }[]) => {
  const n = (r.workspaces ?? []).length + (r.dirs ?? []).length;
  return n ? [...(r.workspaces ?? []).map((id) => ws.find((w) => w.id === id)?.name ?? `#${id}`), ...(r.dirs ?? []).map((d) => tilde(d))].join(t("msg.sep")) : t("perm.none");
};

/** Text saved as it is left: lines (folders) or one line (commands). */
function Lines({ label, value, save, one }: { label: string; value: string; save: (v: string) => void; one?: boolean }) {
  const [v, setV] = useState(value);
  useEffect(() => setV(value), [value]);
  const done = () => v !== value && save(v);
  const cls = "no-ring w-full rounded-md bg-fill px-2.5 py-1.5 font-mono text-xs leading-5 placeholder:font-sans placeholder:text-ink-3";
  return one ? (
    <input value={v} placeholder={label} aria-label={label} onChange={(e) => setV(e.target.value)} onBlur={done} onKeyDown={(e) => e.key === "Enter" && (e.currentTarget as HTMLInputElement).blur()} className={cn(cls, "h-8")} />
  ) : (
    <textarea value={v} placeholder={label} aria-label={label} rows={Math.min(5, Math.max(2, v.split("\n").length))} onChange={(e) => setV(e.target.value)} onBlur={done} className={cn(cls, "resize-none")} />
  );
}

function NumberIn({ value, save, label }: { value: number; save: (n: number) => void; label: string }) {
  const [v, setV] = useState(String(value));
  useEffect(() => setV(String(value)), [value]);
  const done = () => {
    const n = Math.max(0, Math.round(Number(v) || 0));
    setV(String(n));
    if (n !== value) save(n);
  };
  return <input inputMode="numeric" value={v} aria-label={label} onChange={(e) => setV(e.target.value.replace(/[^0-9]/g, ""))} onBlur={done} onKeyDown={(e) => e.key === "Enter" && (e.currentTarget as HTMLInputElement).blur()} className="no-ring h-8 w-20 rounded-md bg-fill px-2 text-right text-sm tabular-nums" />;
}

/** The +: a new panel's invite (a public origin first) or a plugin's token, named; the secret then
 *  shows once. */
function NewTerminal() {
  const qc = useQueryClient();
  const me = useMe().data;
  const [open, setOpen] = useState(false);
  const [kind, setKind] = useState<"invite" | "token" | null>(null);
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);
  const make = async () => {
    if (kind === "token" && !name.trim()) return;
    setBusy(true);
    try {
      if (kind === "invite") showSecret({ invite: await api.invite(name.trim()) });
      else showSecret({ token: await api.token(name.trim()) });
      setOpen(false);
      void qc.invalidateQueries({ queryKey: ["terminals"] });
    } catch (e) {
      const code = e instanceof ApiError ? (e.body as { nameError?: string } | undefined)?.nameError : undefined;
      toast(t("err.invite"), { error: true, description: code === "taken" ? t("me.nameTaken") : code ? t("me.nameBad") : errText(e) });
    } finally {
      setBusy(false);
    }
  };
  const choice = (k: "invite" | "token", icon: ReactNode, label: string, hint: string, off?: boolean) => (
    <button type="button" disabled={off} onClick={() => (setKind(k), setName(""))} className="flex min-h-11 items-start gap-2.5 rounded-md px-2 py-1.5 text-left hover:bg-fill disabled:opacity-45 disabled:hover:bg-transparent">
      <span className="mt-0.5 text-ink-2">{icon}</span>
      <span className="min-w-0 flex-1">
        <span className="block text-base">{label}</span>
        <span className="block text-xs text-ink-3">{hint}</span>
      </span>
    </button>
  );
  return (
    <Popover.Root open={open} onOpenChange={(o) => (setOpen(o), !o && setTimeout(() => setKind(null), 150))}>
      <Tip label={t("term.new")} side="bottom">
        <Popover.Trigger
          render={
            <button type="button" aria-label={t("term.new")} className="g-press grid size-8 place-items-center rounded-md text-ink-2 hover:bg-fill hover:text-ink data-popup-open:bg-fill pointer-coarse:size-10">
              <Plus size={18} />
            </button>
          }
        />
      </Tip>
      <PopSurface side="bottom" align="end" className="w-[min(300px,calc(100vw-16px))] p-1.5">
        {kind === null ? (
          <div className="flex flex-col">
            {choice("invite", <Link2 size={16} />, t("term.invite"), me?.public ? t("term.inviteHint") : t("term.needPublic"), !me?.public)}
            {choice("token", <KeyRound size={16} />, t("term.token"), t("term.tokenHint"))}
          </div>
        ) : (
          <form
            className="g-slide flex flex-col gap-2 p-1.5"
            onSubmit={(e) => {
              e.preventDefault();
              void make();
            }}
          >
            <div className="flex items-center gap-1">
              <IconKey label={t("nav.back")} onClick={() => setKind(null)} className="size-7">
                <ChevronLeft size={16} />
              </IconKey>
              <span className="text-sm font-semibold">{kind === "invite" ? t("term.invite") : t("term.token")}</span>
            </div>
            <input autoFocus value={name} maxLength={12} onChange={(e) => setName(e.target.value)} placeholder={kind === "invite" ? t("term.nameOptional") : t("term.name")} aria-label={t("term.name")} className="no-ring h-9 rounded-md bg-fill px-2.5 text-base placeholder:text-ink-3" />
            <div className="flex justify-end">
              <MainKey type="submit" busy={busy} disabled={kind === "token" && !name.trim()}>
                {t("term.make")}
              </MainKey>
            </div>
          </form>
        )}
      </PopSurface>
    </Popover.Root>
  );
}
