// The settings' 凭据 page (plan "凭据", docs/api.md "凭据"): every credential by name — its note,
// who may use it, when it was last used — never its value. + stores a new one; a row's menu (a right
// click or a long press) deletes it, nothing else. What agents' ir asked for of credentials waits
// on top.
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Menu } from "@base-ui/react/menu";
import { ChevronLeft, LoaderCircle, LockKeyhole, Plus, Trash2, X } from "lucide-react";
import { useRef, useState } from "react";
import { api } from "./api/client";
import type { SecretInfo, SecretScope } from "./api/types.gen";
import { CardFrame, CardHead, MainKey, SecondKey } from "./card";
import { ConfirmCard } from "./confirms";
import { t, useLang } from "./i18n";
import { MenuItems } from "./sidebar";
import { useConfirms } from "./store";
import { errText, toast } from "./toast";
import { ago, cn, IconKey, own, useLongPress, useMenuAt } from "./ui";

export const secretsQ = { queryKey: ["secrets"], queryFn: api.secrets, staleTime: 30_000 };
const NAME = /^[A-Z][A-Z0-9_]{0,63}$/;
const BOX = "h-9 w-full min-w-0 rounded-md bg-fill px-2.5 text-sm outline-none placeholder:text-ink-3 focus:bg-fill-2 pointer-coarse:h-11";

export function SecretsPage({ onBack }: { onBack: () => void }) {
  useLang();
  const q = useQuery(secretsQ);
  const confirms = useConfirms().filter((c) => c.kind === "secret-set" || c.kind === "secret-use");
  const [adding, setAdding] = useState(false);
  const list = q.data?.secrets ?? [];
  return (
    <div className="flex h-full flex-col">
      <header className="flex h-10.5 shrink-0 items-center gap-1 px-1.5 pt-1.5">
        <IconKey label={t("nav.me")} onClick={onBack} tipSide="bottom" className="size-7">
          <ChevronLeft size={16} />
        </IconKey>
        <h1 className="m-0 min-w-0 flex-1 truncate text-base font-semibold">{t("secrets.title")}</h1>
        {q.isFetching && <LoaderCircle size={14} className="animate-spin text-ink-4" />}
        <IconKey label={adding ? t("nav.close") : t("secrets.new")} onClick={() => setAdding(!adding)} tipSide="bottom" className="size-7">
          {adding ? <X size={16} /> : <Plus size={16} />}
        </IconKey>
      </header>
      <div className="no-bar min-h-0 flex-1 overflow-y-auto pt-2 pb-4">
        <div className="flex flex-col gap-5 px-3">
          {confirms.length > 0 && (
            <div className="flex flex-col gap-2">
              {confirms.map((c) => (
                <ConfirmCard key={c.id} c={c} at="secrets" />
              ))}
            </div>
          )}
          {adding && <NewSecret done={() => setAdding(false)} />}
          {q.isError && <p className="m-0 text-sm text-ink-3">{errText(q.error)}</p>}
          {q.data && !list.length && !adding && <p className="m-0 text-sm text-ink-3">{t("secrets.none")}</p>}
          <section className="flex flex-col" aria-label={t("secrets.title")}>
            {list.map((s) => (
              <SecretRow key={s.name} s={s} />
            ))}
          </section>
        </div>
      </div>
    </div>
  );
}

/** Stores a value under a new name (or replaces one by that name). */
function NewSecret({ done }: { done: () => void }) {
  const qc = useQueryClient();
  const [name, setName] = useState("");
  const [value, setValue] = useState("");
  const [note, setNote] = useState("");
  const [busy, setBusy] = useState(false);
  const ok = NAME.test(name) && !!value;
  const store = async () => {
    if (!ok) return;
    setBusy(true);
    try {
      await api.putSecret(name, { value, ...(note.trim() ? { note: note.trim() } : {}) });
      setValue("");
      toast(t("secrets.stored", { name }));
      await qc.invalidateQueries({ queryKey: ["secrets"] });
      done();
    } catch (e) {
      toast(t("err.secret"), { error: true, description: errText(e) });
    } finally {
      setBusy(false);
    }
  };
  return (
    <form
      className="g-pop flex flex-col gap-2"
      onSubmit={(e) => {
        e.preventDefault();
        void store();
      }}
    >
      <input autoFocus value={name} onChange={(e) => setName(e.target.value.toUpperCase().replace(/[^A-Z0-9_]/g, "_"))} placeholder={t("secrets.namePlaceholder")} aria-label={t("secrets.name")} spellCheck={false} className={cn(BOX, "font-mono")} />
      <input type="password" value={value} onChange={(e) => setValue(e.target.value)} autoComplete="new-password" placeholder={t("secrets.value")} aria-label={t("secrets.value")} className={cn(BOX, "font-mono")} />
      <input value={note} onChange={(e) => setNote(e.target.value)} maxLength={200} placeholder={t("secrets.notePlaceholder")} aria-label={t("secrets.note")} className={BOX} />
      <button type="submit" disabled={!ok || busy} className="g-press flex h-9 items-center justify-center gap-1.5 rounded-md bg-ink text-sm font-medium text-on-ink disabled:opacity-40 pointer-coarse:h-11">
        {busy && <LoaderCircle size={13} className="animate-spin" />}
        {t("secrets.store")}
      </button>
    </form>
  );
}

const scopeLabel = (s: SecretScope) => (s === "workspaces" ? t("secrets.scopeWorkspaces") : s === "owner" ? t("secrets.scopeOwner") : t("secrets.scopeAll"));

/** One credential's row. Its menu, a right click or a long press away, only deletes it (asked
 *  under the row: a value deleted is gone for good). */
function SecretRow({ s }: { s: SecretInfo }) {
  const qc = useQueryClient();
  const [menuAt, setMenuAt] = useMenuAt();
  const [sure, setSure] = useState(false);
  const [busy, setBusy] = useState(false);
  const row = useRef<HTMLDivElement>(null);
  const lp = useLongPress(() => {
    const r = row.current?.getBoundingClientRect();
    setMenuAt(r ? { x: r.left + 40, y: r.bottom } : { x: 40, y: 40 });
  });
  const remove = async () => {
    setBusy(true);
    try {
      await api.deleteSecret(s.name);
      toast(t("secrets.deleted", { name: s.name }));
    } catch (e) {
      toast(t("err.secret"), { error: true, description: errText(e) });
    }
    setBusy(false);
    await qc.invalidateQueries({ queryKey: ["secrets"] });
  };
  const scope = s.scope || "all";
  const anchor = menuAt && { getBoundingClientRect: () => DOMRect.fromRect({ x: menuAt.x, y: menuAt.y, width: 0, height: 0 }) };
  return (
    <div className="flex flex-col">
      <div
        ref={row}
        {...lp}
        onContextMenu={(e) => {
          lp.onContextMenu(e);
          if (!own(e)) return;
          e.preventDefault();
          setMenuAt({ x: e.clientX, y: e.clientY });
        }}
        className="flex min-h-12 items-center gap-2.5 rounded-lg px-2 select-none [-webkit-touch-callout:none]"
      >
        <LockKeyhole size={16} className="shrink-0 text-ink-3" />
        <span className="min-w-0 flex-1">
          <span className="block truncate font-mono text-sm font-medium">{s.name}</span>
          <span className="block truncate text-xs text-ink-3">{[s.note, scopeLabel(scope) + (s.always ? ` · ${t("secrets.always")}` : "")].filter(Boolean).join(" · ")}</span>
        </span>
        <span className="shrink-0 text-xs text-ink-3 tabular-nums">{s.lastUse ? ago(s.lastUse.at) : t("secrets.unused")}</span>
      </div>
      {sure && (
        <div className="g-fade pt-1 pb-2">
          <CardFrame label={t("secrets.deleteTitle", { name: s.name })}>
            <CardHead
              icon={<Trash2 size={16} />}
              title={t("secrets.deleteTitle", { name: s.name })}
              summary={t("secrets.deleteHint")}
              keys={
                <>
                  <SecondKey onClick={() => setSure(false)}>{t("me.cancel")}</SecondKey>
                  <MainKey busy={busy} onClick={() => void remove()}>
                    {t("secrets.delete")}
                  </MainKey>
                </>
              }
            />
          </CardFrame>
        </div>
      )}
      <Menu.Root open={menuAt !== null} onOpenChange={(o) => !o && setMenuAt(null)}>
        <Menu.Portal>
          <Menu.Positioner anchor={anchor} side="bottom" align="start" sideOffset={4} collisionPadding={8} className="z-50">
            <MenuItems items={[{ icon: <Trash2 size={15} />, label: t("secrets.delete"), run: () => setSure(true) }]} />
          </Menu.Positioner>
        </Menu.Portal>
      </Menu.Root>
    </div>
  );
}
