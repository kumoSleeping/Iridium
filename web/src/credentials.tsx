// The settings' 凭据 page (plan "凭据", docs/api.md "凭据"): every credential by name — its note,
// who may use it, how often it was used — never its value, which can only be replaced. + stores a
// new one; one opened changes its note, its scope (all sessions, some workspaces, only those the
// owner's own terminals opened), "confirm every time", its value, or deletes it, and lists its last
// uses. What agents' ir asked for of credentials waits on top. Changes are saved at once.
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronLeft, LoaderCircle, LockKeyhole, Plus, Trash2, X } from "lucide-react";
import { useState } from "react";
import { api } from "./api/client";
import type { SecretInfo, Secrets, SecretScope, SetSecret } from "./api/types.gen";
import { ConfirmCard } from "./confirms";
import { useWorkspaces } from "./data";
import { t, useLang } from "./i18n";
import { Group, Item } from "./settings";
import { useConfirms } from "./store";
import { errText, toast } from "./toast";
import { ago, cn, IconKey, Switch } from "./ui";

export const secretsQ = { queryKey: ["secrets"], queryFn: api.secrets, staleTime: 30_000 };
const NAME = /^[A-Z][A-Z0-9_]{0,63}$/;
const BOX = "h-9 w-full min-w-0 rounded-md bg-fill px-2.5 text-sm outline-none placeholder:text-ink-3 focus:bg-fill-2 pointer-coarse:h-11";

export function SecretsPage({ onBack }: { onBack: () => void }) {
  useLang();
  const q = useQuery(secretsQ);
  const confirms = useConfirms().filter((c) => c.kind === "secret-set" || c.kind === "secret-use");
  const [adding, setAdding] = useState(false);
  const [open, setOpen] = useState<string | null>(null);
  const list = q.data?.secrets ?? [];
  return (
    <div className="flex h-full flex-col">
      <header className="flex h-10.5 shrink-0 items-center gap-1 px-1.5 pt-1.5">
        <IconKey label={t("nav.settings")} onClick={onBack} tipSide="bottom" className="size-7">
          <ChevronLeft size={16} />
        </IconKey>
        <h1 className="m-0 min-w-0 flex-1 truncate text-base font-semibold">{t("secrets.title")}</h1>
        {q.isFetching && <LoaderCircle size={14} className="animate-spin text-ink-4" />}
        <IconKey label={adding ? t("nav.close") : t("secrets.new")} onClick={() => setAdding(!adding)} tipSide="bottom" className="size-7">
          {adding ? <X size={16} /> : <Plus size={16} />}
        </IconKey>
      </header>
      <div className="min-h-0 flex-1 overflow-y-auto pt-2 pb-4">
        <div className="flex flex-col gap-5 px-3">
          {confirms.length > 0 && (
            <div className="flex flex-col gap-2">
              {confirms.map((c) => (
                <ConfirmCard key={c.id} c={c} at="secrets" />
              ))}
            </div>
          )}
          {adding && <NewSecret done={(name) => (setAdding(false), setOpen(name))} />}
          {q.isError && <p className="m-0 text-sm text-ink-3">{errText(q.error)}</p>}
          {q.data && !list.length && !adding && <p className="m-0 text-sm text-ink-3">{t("secrets.none")}</p>}
          <section className="flex flex-col" aria-label={t("secrets.title")}>
            {list.map((s) => (
              <SecretRow key={s.name} s={s} uses={q.data!} open={open === s.name} toggle={() => setOpen(open === s.name ? null : s.name)} />
            ))}
          </section>
        </div>
      </div>
    </div>
  );
}

/** Stores a value under a new name (or replaces one by that name). */
function NewSecret({ done }: { done: (name: string) => void }) {
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
      done(name);
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

/** One credential's row, and opened, its page. */
function SecretRow({ s, uses, open, toggle }: { s: SecretInfo; uses: Secrets; open: boolean; toggle: () => void }) {
  const qc = useQueryClient();
  const ws = useWorkspaces().data ?? [];
  const [saving, setSaving] = useState(false);
  const [note, setNote] = useState(s.note ?? "");
  const [value, setValue] = useState("");
  const [sure, setSure] = useState(false);
  const put = async (p: SetSecret, said?: string) => {
    setSaving(true);
    try {
      const next = await api.putSecret(s.name, p);
      qc.setQueryData<Secrets>(["secrets"], (old) => old && { ...old, secrets: old.secrets.map((x) => (x.name === s.name ? next : x)) });
      if (said) toast(said);
    } catch (e) {
      toast(t("err.secret"), { error: true, description: errText(e) });
    } finally {
      setSaving(false);
    }
  };
  const remove = async () => {
    try {
      await api.deleteSecret(s.name);
      toast(t("secrets.deleted", { name: s.name }));
    } catch (e) {
      toast(t("err.secret"), { error: true, description: errText(e) });
    }
    await qc.invalidateQueries({ queryKey: ["secrets"] });
  };
  const mine = uses.uses.filter((u) => u.secret === s.name).slice(0, 8);
  const scope = s.scope || "all";
  const chosen = s.workspaces ?? [];
  return (
    <div className={cn("flex flex-col", open && "pb-4")}>
      <button type="button" onClick={toggle} aria-expanded={open} className="g-press flex min-h-12 items-center gap-2.5 rounded-lg px-2 text-left hover:bg-fill">
        <LockKeyhole size={16} className="shrink-0 text-ink-3" />
        <span className="min-w-0 flex-1">
          <span className="block truncate font-mono text-sm font-medium">{s.name}</span>
          <span className="block truncate text-xs text-ink-3">{[s.note, scopeLabel(scope) + (s.always ? ` · ${t("secrets.always")}` : "")].filter(Boolean).join(" · ")}</span>
        </span>
        <span className="shrink-0 text-xs text-ink-3 tabular-nums">{s.lastUse ? ago(s.lastUse.at) : t("secrets.unused")}</span>
      </button>
      {open && (
        <div className="g-fade flex flex-col gap-4 px-2 pt-2">
          <Group title={saving ? `${t("secrets.about")} ·` : t("secrets.about")}>
            <input value={note} onChange={(e) => setNote(e.target.value)} onBlur={() => note.trim() !== (s.note ?? "") && void put({ note: note.trim() })} maxLength={200} placeholder={t("secrets.notePlaceholder")} aria-label={t("secrets.note")} className={BOX} />
            <div className="mt-1.5 text-xs text-ink-3">{t("secrets.by", { who: s.by || "—", n: String(s.uses) })}</div>
          </Group>
          <Group title={t("secrets.who")}>
            <div role="radiogroup" aria-label={t("secrets.who")} className="flex rounded-md bg-fill p-0.5">
              {(["all", "workspaces", "owner"] as const).map((v) => (
                <button
                  key={v}
                  type="button"
                  role="radio"
                  aria-checked={scope === v}
                  onClick={() => scope !== v && void put({ scope: v, ...(v === "workspaces" ? { workspaces: chosen } : {}) })}
                  className={cn("g-press h-7 min-w-0 flex-1 truncate rounded-[5px] px-1.5 text-xs pointer-coarse:h-9", scope === v ? "bg-paper text-ink shadow-[0_0_0_0.5px_var(--edge),0_1px_2px_var(--edge)]" : "text-ink-3 hover:text-ink")}
                >
                  {scopeLabel(v)}
                </button>
              ))}
            </div>
            {scope === "workspaces" && (
              <div className="g-fade mt-1.5 flex flex-col">
                {ws.length === 0 && <p className="m-0 text-xs text-ink-3">{t("secrets.noWorkspaces")}</p>}
                {ws.map((w) => (
                  <Item key={w.id} label={w.name}>
                    <Switch label={w.name} on={chosen.includes(w.id)} onChange={(on) => void put({ scope: "workspaces", workspaces: on ? [...chosen, w.id] : chosen.filter((x) => x !== w.id) })} />
                  </Item>
                ))}
              </div>
            )}
            <Item label={t("secrets.always")} hint={t("secrets.alwaysRowHint")}>
              <Switch label={t("secrets.always")} on={!!s.always} onChange={(v) => void put({ always: v })} />
            </Item>
          </Group>
          <Group title={t("secrets.replace")}>
            <form
              className="flex gap-1.5"
              onSubmit={(e) => {
                e.preventDefault();
                if (value) void put({ value }, t("secrets.stored", { name: s.name })).then(() => setValue(""));
              }}
            >
              <input type="password" value={value} onChange={(e) => setValue(e.target.value)} autoComplete="new-password" placeholder={t("secrets.newValue")} aria-label={t("secrets.newValue")} className={cn(BOX, "flex-1 font-mono")} />
              <button type="submit" disabled={!value || saving} className="g-press h-9 shrink-0 rounded-md bg-fill px-3 text-sm hover:bg-fill-2 disabled:opacity-40 pointer-coarse:h-11">
                {t("secrets.replaceKey")}
              </button>
            </form>
          </Group>
          {mine.length > 0 && (
            <Group title={t("secrets.uses")}>
              {mine.map((u, i) => (
                <div key={i} className="flex min-h-8 items-center gap-2 text-xs">
                  <code className="min-w-0 flex-1 truncate font-mono text-ink-2">{u.command || "—"}</code>
                  <span className="shrink-0 text-ink-3">{u.terminal}</span>
                  <span className="shrink-0 text-ink-3 tabular-nums">{ago(u.at)}</span>
                </div>
              ))}
            </Group>
          )}
          {/* Twice: a value deleted is gone for good. */}
          <button type="button" onClick={() => (sure ? void remove() : setSure(true))} onBlur={() => setSure(false)} className="g-press -mx-1.5 flex min-h-10 items-center gap-2 rounded-md px-1.5 text-left text-sm text-err hover:bg-fill">
            <Trash2 size={15} />
            <span key={String(sure)} className="g-fade">{sure ? t("secrets.deleteSure") : t("secrets.delete")}</span>
          </button>
        </div>
      )}
    </div>
  );
}
