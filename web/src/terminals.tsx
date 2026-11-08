// The settings' 终端 page (plan "交互终端", docs/api.md "交互终端"): every interactive terminal —
// 本机, the web panels, the channels (plugins, peers) — with its avatar, name, kind, when it was
// last used and today's use; one opened shows its name and its permission table to change (Claude
// only shows: the web may turn it off, never on; that is ir's on the machine), and revoking it. The +
// makes a new panel's invite (its link and QR code) or a plugin's token, shown once. What agents'
// ir asked for waits on top. Changes are saved at once.
import { Popover } from "@base-ui/react/popover";
import { useQueryClient, type QueryClient } from "@tanstack/react-query";
import { Bot, ChevronLeft, ChevronRight, KeyRound, Link2, LoaderCircle, Plus, Trash2, X } from "lucide-react";
import { useEffect, useState, type ReactNode } from "react";
import { ApiError, api } from "./api/client";
import type { Rules, Terminal } from "./api/types.gen";
import { CardFrame, CardHead, MainKey, SecondKey } from "./card";
import { ConfirmCard, saysOwner } from "./confirms";
import { useMe, useTerminals } from "./data";
import { ExtensionPage, ExtensionRows, OfficialTag } from "./extensions";
import { t, useLang } from "./i18n";
import { LocalMark } from "./me";
import { SecretHere } from "./secret";
import { showSecret, useSecret } from "./secrets";
import { Group, Item } from "./settings";
import { useConfirms } from "./store";
import { errText, toast } from "./toast";
import { ago, Avatar, cn, IconKey, Logo, PopSurface, Switch, Tip, tokens } from "./ui";


const kindName = (x: Terminal) => (x.local ? t("term.local") : x.kind === "panel" ? t("term.panel") : t("term.channel"));

export function TerminalsPage({ onBack }: { onBack: () => void }) {
  useLang();
  const qc = useQueryClient();
  const q = useTerminals();
  const confirms = useConfirms();
  const secretHere = useSecret()?.at === "terminals";
  const [open, setOpen] = useState<string | null>(null);
  const [ext, setExt] = useState<string | null>(null);
  const list = q.data?.terminals ?? [];
  const invites = q.data?.invites ?? [];
  if (ext)
    return (
      <div key={ext} className="g-slide h-full">
        <ExtensionPage id={ext} onBack={() => setExt(null)} />
      </div>
    );
  return (
    <div className="flex h-full flex-col">
      <header className="flex h-10.5 shrink-0 items-center gap-1 px-1.5 pt-1.5">
        <IconKey label={t("nav.me")} onClick={onBack} tipSide="bottom" className="size-7">
          <ChevronLeft size={16} />
        </IconKey>
        <h1 className="m-0 min-w-0 flex-1 truncate text-base font-semibold">{t("settings.terminals")}</h1>
        {q.isFetching && <LoaderCircle size={14} className="animate-spin text-ink-4" />}
        <NewTerminal openExt={setExt} />
      </header>
      <div className="no-bar min-h-0 flex-1 overflow-y-auto pt-2 pb-4">
        <div className="flex flex-col gap-5 px-3">
          {(confirms.length > 0 || secretHere) && (
            <div className="flex flex-col gap-2">
              <SecretHere at="terminals" />
              {confirms.map((c) => (
                <ConfirmCard key={c.id} c={c} at="terminals" />
              ))}
            </div>
          )}
          {q.isError && <p className="m-0 text-sm text-ink-3">{errText(q.error)}</p>}
          <section className="flex flex-col" aria-label={t("settings.terminals")}>
            {list.map((x) => (
              <TerminalRow key={x.id} x={x} open={open === x.id} toggle={() => setOpen(open === x.id ? null : x.id)} />
            ))}
          </section>
          <ExtensionRows openExt={setExt} />
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
          {/* One line, cut at its end, never wrapped into columns (the panel is narrow). */}
          <span className="flex min-w-0 items-center gap-1.5 text-xs text-ink-3 tabular-nums">
            {x.online ? <span className="size-1.5 shrink-0 rounded-full bg-run" aria-hidden /> : null}
            <span className="min-w-0 truncate">
              {used} · {t("term.today", { n: tokens(x.usedToday) })}
            </span>
          </span>
        </span>
        {hasClaude(x.rules) && (
          <Tip label={t("term.claudeYes")}>
            <span className="hidden shrink-0 sm:flex">
              <Logo agent="claude" size={13} />
            </span>
          </Tip>
        )}
        <ChevronRight size={15} className="shrink-0 text-ink-4 transition-transform duration-(--dur-2)" style={{ transform: open ? "rotate(90deg)" : undefined }} />
      </button>
      {open &&
        (x.local ? (
          <div className="g-fade flex flex-col gap-2 pt-1 pr-2 pl-2 sm:pl-[52px]">
            <p className="m-0 text-sm text-ink-3">{t("term.localHint")}</p>
          </div>
        ) : (
          <Detail x={x} />
        ))}
    </div>
  );
}

/** A terminal opened: its name, whether it has Claude, revoking it. */
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
      {/* Read only: Claude is set as a terminal is made (the user, 2026-11-08). */}
      <Item label={<span className="flex items-center gap-2"><Logo agent="claude" size={15} />Claude</span>} hint={t(hasClaude(x.rules) ? "term.claudeYes" : "term.claudeNo")}>
        <span className="text-sm text-ink-2">{hasClaude(x.rules) ? t("term.on") : t("term.off")}</span>
      </Item>
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

/** Claude: the terminal was made the owner's own. Set then, never after (the user, 2026-11-08). */
export const hasClaude = (r: Rules) => !!r.claudeOwner;

/** The +: a new panel's invite (a public origin first) or a plugin's token, named; the secret then
 *  shows once. Below them the official extensions, each opening its page. */
function NewTerminal({ openExt }: { openExt: (id: string) => void }) {
  const qc = useQueryClient();
  const me = useMe().data;
  const [open, setOpen] = useState(false);
  const [kind, setKind] = useState<"invite" | "token" | null>(null);
  const [name, setName] = useState("");
  const [withClaude, setWithClaude] = useState(false);
  const [busy, setBusy] = useState(false);
  const canClaude = saysOwner(me);
  const make = async () => {
    if (kind === "token" && !name.trim()) return;
    setBusy(true);
    try {
      const owner = canClaude && withClaude;
      if (kind === "invite") showSecret({ invite: await api.invite(name.trim(), owner), at: "terminals" });
      else showSecret({ token: await api.token(name.trim(), owner), at: "terminals" });
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
    <button type="button" disabled={off} onClick={() => (setKind(k), setName(""), setWithClaude(false))} className="flex min-h-11 items-start gap-2.5 rounded-md px-2 py-1.5 text-left hover:bg-fill disabled:opacity-45 disabled:hover:bg-transparent">
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
            <button type="button" aria-label={t("term.new")} className="g-press grid size-7 place-items-center rounded-md text-ink-2 hover:bg-fill hover:text-ink data-popup-open:bg-fill pointer-coarse:size-10">
              <Plus size={16} />
            </button>
          }
        />
      </Tip>
      <PopSurface side="bottom" align="end" className="w-[min(300px,calc(100vw-16px))] p-1.5">
        {kind === null ? (
          <div className="flex flex-col">
            {choice("invite", <Link2 size={16} />, t("term.invite"), me?.public ? t("term.inviteHint") : t("term.needPublic"), !me?.public)}
            {choice("token", <KeyRound size={16} />, t("term.token"), t("term.tokenHint"))}
            <div role="separator" className="mx-1 my-1.5 h-px bg-edge" />
            <button type="button" onClick={() => (setOpen(false), openExt("qq"))} className="flex min-h-11 items-start gap-2.5 rounded-md px-2 py-1.5 text-left hover:bg-fill">
              <span className="mt-0.5 text-ink-2">
                <Bot size={16} />
              </span>
              <span className="min-w-0 flex-1">
                <span className="flex items-center gap-2 text-base">
                  {t("ext.qq")}
                  <OfficialTag />
                </span>
                <span className="block text-xs text-ink-3">{t("ext.qqHint")}</span>
              </span>
            </button>
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
            {canClaude && (
              <div className="flex items-center gap-2 px-0.5">
                <span className="min-w-0 flex-1">
                  <span className="block text-sm">{t("term.withClaude")}</span>
                  <span className="block text-xs text-ink-3">{t("term.withClaudeHint")}</span>
                </span>
                <Switch label={t("term.withClaude")} on={withClaude} onChange={setWithClaude} />
              </div>
            )}
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
