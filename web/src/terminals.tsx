// The settings' apps page (docs/api.md "应用"; 0.2 S1c reworks it): the host, ir_web's logins (the
// browsers) and the apps, each with its avatar, name, kind, when it was last used and today's use,
// Claude's mark if it has it. Nothing opens (the user, 2026-11-08): a right click or a long press
// has its menu — rename, change the avatar, remove; an app's also the logins it follows. The +
// makes an invite of ir_web (its link and QR code), says how an app or a computer asks to pair;
// no TOKEN is handed out here (pairing is the way in). What agents'
// ir asked for, and pairing requests, wait on top.
import { Popover } from "@base-ui/react/popover";
import { useQueryClient, type QueryClient } from "@tanstack/react-query";
import { Menu } from "@base-ui/react/menu";
import { ChevronLeft, Eye, ImageUp, Link2, LoaderCircle, Monitor, PencilLine, Plus, Trash2, X } from "lucide-react";
import { useRef, useState, type ReactNode } from "react";
import { ApiError, api } from "./api/client";
import type { Apps, Rules } from "./api/types.gen";
import { CardFrame, CardHead, MainKey, SecondKey } from "./card";
import { ConfirmCard, saysOwner } from "./confirms";
import { useApps, useMe } from "./data";
import { t, useLang } from "./i18n";
import { LocalMark } from "./me";
import { Crop } from "./profile";
import { SecretHere } from "./secret";
import { showSecret, useSecret } from "./secrets";
import { Group } from "./settings";
import { MenuItems } from "./sidebar";
import { useConfirms } from "./store";
import { errText, toast } from "./toast";
import { ago, Avatar, IconKey, Logo, own, PopSurface, Switch, Tip, tokens, useLongPress, useMenuAt } from "./ui";


/** A row: the host, an app, or a login of ir_web (web). */
type Row = Omit<Apps["apps"][number], "web"> & { web?: boolean };

/** The rows: the host, ir_web's logins, the apps (ir_web itself not a row of its own here). */
const rowsOf = (d?: Apps | null): Row[] => (d ? [...d.apps.filter((a) => a.host), ...d.logins.map((l) => ({ ...l, web: true })), ...d.apps.filter((a) => !a.host && !a.web)] : []);

const kindName = (x: Row) => (x.host ? t("term.local") : x.web ? t("term.panel") : t("term.channel"));

export function TerminalsPage({ onBack }: { onBack: () => void }) {
  useLang();
  const qc = useQueryClient();
  const q = useApps();
  const confirms = useConfirms();
  const secretHere = useSecret()?.at === "terminals";
  const list = rowsOf(q.data);
  const invites = q.data?.invites ?? [];
  return (
    <div className="flex h-full flex-col">
      <header className="flex h-10.5 shrink-0 items-center gap-1 px-1.5 pt-1.5">
        <IconKey label={t("nav.me")} onClick={onBack} tipSide="bottom" className="size-7">
          <ChevronLeft size={16} />
        </IconKey>
        <h1 className="m-0 min-w-0 flex-1 truncate text-base font-semibold">{t("settings.terminals")}</h1>
        {q.isFetching && <LoaderCircle size={14} className="animate-spin text-ink-4" />}
        <NewTerminal />
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
              <TerminalRow key={x.id} x={x} panels={list.filter((p) => p.web)} />
            ))}
          </section>
          {invites.length > 0 && (
            <Group title={t("term.invites")}>
              {invites.map((i) => (
                <div key={i.id} className="flex min-h-10 items-center gap-3">
                  <Link2 size={15} className="shrink-0 text-ink-3" />
                  <span className="min-w-0 flex-1 truncate text-base">{i.setup ? t("term.setup") : i.name || <span className="text-ink-3">{t("term.unnamed")}</span>}</span>
                  {i.claude && <span className="shrink-0 text-xs text-ink-3">{t("term.withClaude")}</span>}
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

/** Removes app, login or invite id, then lists them again. */
async function revoke(qc: QueryClient, id: string, name: string) {
  try {
    await api.revoke(id);
    toast(t("term.revoked", { name }));
  } catch (e) {
    toast(t("err.revoke"), { error: true, description: errText(e) });
  }
  await qc.invalidateQueries({ queryKey: ["apps"] });
}

/** One row. Its menu, a right click or a long press away (not the host's): rename it in place,
 *  change its avatar (a picture chosen, then cropped under the row), remove it (asked under the
 *  row); an app's, the logins it follows (chosen under the row). */
function TerminalRow({ x, panels }: { x: Row; panels: Row[] }) {
  const me = useMe().data;
  const qc = useQueryClient();
  const [menuAt, setMenuAt] = useMenuAt();
  const [renaming, setRenaming] = useState(false);
  const [sure, setSure] = useState(false);
  const [following, setFollowing] = useState(false);
  const [busy, setBusy] = useState(false);
  const [picture, setPicture] = useState<string | null>(null);
  const row = useRef<HTMLDivElement>(null);
  const file = useRef<HTMLInputElement>(null);
  const lp = useLongPress(() => {
    const r = row.current?.getBoundingClientRect();
    setMenuAt(r ? { x: r.left + 48, y: r.bottom } : { x: 40, y: 40 });
  });
  const used = x.online ? t("term.online") : x.host ? t("term.localShort") : ago(x.lastSeenAt || x.createdAt || 0);
  const menu = x.host
    ? undefined
    : {
        ...lp,
        onContextMenu: (e: React.MouseEvent) => {
          if (!own(e)) return;
          e.preventDefault();
          setMenuAt({ x: e.clientX, y: e.clientY });
        },
      };
  const pick = (f: File | undefined) => {
    if (f) setPicture((old) => (old && URL.revokeObjectURL(old), URL.createObjectURL(f)));
  };
  const dropPicture = () => setPicture((old) => (old && URL.revokeObjectURL(old), null));
  const saveAvatar = async (blob: Blob) => {
    try {
      await api.profile({ avatar: blob }, x.id);
      dropPicture();
      void qc.invalidateQueries({ queryKey: ["apps"] });
    } catch (e) {
      toast(t("term.avatar"), { error: true, description: errText(e) });
    }
  };
  const anchor = menuAt && { getBoundingClientRect: () => DOMRect.fromRect({ x: menuAt.x, y: menuAt.y, width: 0, height: 0 }) };
  return (
    <div className="flex flex-col">
      <div ref={row} {...menu} className="flex min-h-14 items-center gap-3 rounded-lg px-2 select-none [-webkit-touch-callout:none]">
        {x.host ? <LocalMark size={32} /> : <Avatar src={x.avatar} name={x.name} size={32} channel={!x.web} />}
        <span className="min-w-0 flex-1">
          <span className="flex items-center gap-2">
            {renaming ? <Rename x={x} done={() => setRenaming(false)} /> : <span className="truncate text-base font-medium">{x.host ? t("me.local") : x.name}</span>}
            <span className="shrink-0 rounded-sm bg-fill-2 px-1.5 text-[11px] leading-[18px] text-ink-2">{kindName(x)}</span>
            {x.id === me?.id && !x.host && <span className="shrink-0 text-xs text-ink-3">{t("term.this")}</span>}
          </span>
          {/* One line, cut at its end, never wrapped into columns (the panel is narrow). */}
          <span className="flex min-w-0 items-center gap-1.5 text-xs text-ink-3 tabular-nums">
            {x.online ? <span className="size-1.5 shrink-0 rounded-full bg-run" aria-hidden /> : null}
            <span className="min-w-0 truncate">
              {used} · {t("term.today", { n: tokens(x.usedToday) })}
              {followNames(x, panels) && <> · {t("term.followsShort", { names: followNames(x, panels) })}</>}
            </span>
          </span>
        </span>
        {hasClaude(x.rules) && (
          <Tip label={t("term.claudeYes")}>
            <span className="flex shrink-0">
              <Logo agent="claude" size={13} />
            </span>
          </Tip>
        )}
      </div>
      {picture && (
        <div className="g-fade flex justify-center">
          <Crop src={picture} onCancel={dropPicture} onDone={(b) => void saveAvatar(b)} />
        </div>
      )}
      {following && <Follows x={x} panels={panels} done={() => setFollowing(false)} />}
      {sure && (
        <div className="g-fade pt-1 pb-2">
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
        </div>
      )}
      {!x.host && (
        <>
          <input ref={file} type="file" accept="image/*" hidden onChange={(e) => (pick(e.target.files?.[0]), (e.target.value = ""))} />
          <Menu.Root open={menuAt !== null} onOpenChange={(o) => !o && setMenuAt(null)}>
            <Menu.Portal>
              <Menu.Positioner anchor={anchor} side="bottom" align="start" sideOffset={4} collisionPadding={8} className="z-50">
                <MenuItems
                  items={[
                    { icon: <PencilLine size={15} />, label: t("term.renameIt"), run: () => setRenaming(true) },
                    { icon: <ImageUp size={15} />, label: t("term.avatar"), run: () => file.current?.click() },
                    ...(!x.web ? [{ icon: <Eye size={15} />, label: t("term.follows"), run: () => setFollowing(true) }] : []),
                    { icon: <Trash2 size={15} />, label: t("term.revoke"), run: () => setSure(true) },
                  ]}
                />
              </Menu.Positioner>
            </Menu.Portal>
          </Menu.Root>
        </>
      )}
    </div>
  );
}

/** The names of the logins app x follows, "" for none. */
const followNames = (x: Row, panels: Row[]) =>
  (x.follows ?? [])
    .map((id) => panels.find((p) => p.id === id)?.name)
    .filter(Boolean)
    .join("、");

/** Under a Bot's row: the panels it follows, a switch each, saved as each is flipped. What a
 *  session asks while no chat points at it goes to the Bots that follow the panel that began its
 *  turn, and to no other (the user, 2026-11-09). */
function Follows({ x, panels, done }: { x: Row; panels: Row[]; done: () => void }) {
  const qc = useQueryClient();
  const [on, setOn] = useState<string[]>(x.follows ?? []);
  const flip = async (id: string, to: boolean) => {
    const was = on;
    const next = to ? [...on, id] : on.filter((p) => p !== id);
    setOn(next);
    try {
      await api.follows(x.id, next);
      void qc.invalidateQueries({ queryKey: ["apps"] });
    } catch (e) {
      setOn(was);
      toast(t("term.follows"), { error: true, description: errText(e) });
    }
  };
  return (
    <div className="g-fade flex flex-col gap-1 rounded-lg bg-fill px-3 pt-2 pb-2.5">
      <div className="flex items-center gap-2">
        <span className="min-w-0 flex-1 text-sm font-medium">{t("term.follows")}</span>
        <IconKey label={t("me.cancel")} onClick={done} className="size-6">
          <X size={14} />
        </IconKey>
      </div>
      <p className="m-0 text-xs text-ink-3">{t("term.followsHint")}</p>
      {!hasClaude(x.rules) && <p className="m-0 text-xs text-ink-3">{t("term.followsNoClaude")}</p>}
      {panels.length === 0 && <p className="m-0 text-xs text-ink-3">{t("term.followsNone")}</p>}
      {panels.map((p) => (
        <div key={p.id} className="flex min-h-10 items-center gap-2.5">
          <Avatar src={p.avatar} name={p.name} size={24} />
          <span className="min-w-0 flex-1 truncate text-base">{p.name}</span>
          <Switch label={p.name} on={on.includes(p.id)} onChange={(o) => void flip(p.id, o)} />
        </div>
      ))}
    </div>
  );
}

/** The name in place, chosen whole: Enter or leaving saves it, Escape keeps the old one. */
function Rename({ x, done }: { x: Row; done: () => void }) {
  const qc = useQueryClient();
  const [name, setName] = useState(x.name);
  const gone = useRef(false);
  const save = async () => {
    if (gone.current) return;
    gone.current = true;
    const to = name.trim();
    done();
    if (!to || to === x.name) return;
    try {
      await api.profile({ name: to }, x.id);
      void qc.invalidateQueries({ queryKey: ["apps"] });
    } catch (e) {
      const code = e instanceof ApiError ? (e.body as { nameError?: string } | undefined)?.nameError : undefined;
      toast(t("term.renameIt"), { error: true, description: code === "taken" ? t("me.nameTaken") : code ? t("me.nameBad") : errText(e) });
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

/** Claude: it was made the owner's own. Set then, never after (the user, 2026-11-08). */
export const hasClaude = (r: Rules) => !!r.claudeOwner;

/** The +: a new panel's invite (a public origin first), named; the link then shows once. How a Bot
 *  or a computer asks to pair (no TOKEN handed out here: the user, 2026-11-08). Below them the
 *  catalogue's install commands come with 新建应用 (S1c). */
function NewTerminal() {
  const qc = useQueryClient();
  const me = useMe().data;
  const [open, setOpen] = useState(false);
  const [kind, setKind] = useState<"invite" | "pair" | null>(null);
  const [name, setName] = useState("");
  const [withClaude, setWithClaude] = useState(false);
  const [busy, setBusy] = useState(false);
  const canClaude = saysOwner(me);
  const make = async () => {
    setBusy(true);
    try {
      showSecret({ invite: await api.invite(name.trim(), canClaude && withClaude), at: "terminals" });
      setOpen(false);
      void qc.invalidateQueries({ queryKey: ["apps"] });
    } catch (e) {
      const code = e instanceof ApiError ? (e.body as { nameError?: string } | undefined)?.nameError : undefined;
      toast(t("err.invite"), { error: true, description: code === "taken" ? t("me.nameTaken") : code ? t("me.nameBad") : errText(e) });
    } finally {
      setBusy(false);
    }
  };
  const choice = (k: "invite" | "pair", icon: ReactNode, label: string, hint: string, off?: boolean) => (
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
            {choice("pair", <Monitor size={16} />, t("term.pairTitle"), t("term.pairHint"))}
          </div>
        ) : kind === "pair" ? (
          <PairHow back={() => setKind(null)} />
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
              <span className="text-sm font-semibold">{t("term.invite")}</span>
            </div>
            <input autoFocus value={name} maxLength={12} onChange={(e) => setName(e.target.value)} placeholder={t("term.nameOptional")} aria-label={t("term.name")} className="no-ring h-9 rounded-md bg-fill px-2.5 text-base placeholder:text-ink-3" />
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
              <MainKey type="submit" busy={busy}>
                {t("term.make")}
              </MainKey>
            </div>
          </form>
        )}
      </PopSurface>
    </Popover.Root>
  );
}

/** How a Bot or a computer comes in: it asks this address to pair, and its request waits on this
 *  page as a card with the code it shows. */
function PairHow({ back }: { back: () => void }) {
  const at = location.origin;
  const line = (label: string, cmd: string) => (
    <div className="flex flex-col gap-1">
      <span className="text-xs text-ink-3">{label}</span>
      <code className="block rounded-md bg-fill px-2 py-1.5 font-mono text-xs break-all select-all">{cmd}</code>
    </div>
  );
  return (
    <div className="g-slide flex flex-col gap-2.5 p-1.5">
      <div className="flex items-center gap-1">
        <IconKey label={t("nav.back")} onClick={back} className="size-7">
          <ChevronLeft size={16} />
        </IconKey>
        <span className="text-sm font-semibold">{t("term.pairTitle")}</span>
      </div>
      {line(t("term.pairComputer"), `ir join ${at}`)}
      {line(t("term.pairBot"), t("term.pairBotCmd", { at }))}
      <p className="m-0 text-xs text-ink-3">{t("term.pairNote")}</p>
    </div>
  );
}
