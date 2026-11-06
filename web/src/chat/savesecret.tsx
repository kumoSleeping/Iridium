// Over the composer when the draft has what looks like a key or a password (secretish.ts): store it
// as a credential, and the draft says {{NAME}} in its place, which the agent puts into a command with
// ir run; or leave it (plan "凭据"). The value goes to the daemon and is gone from the draft.
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { LoaderCircle, LockKeyhole } from "lucide-react";
import { useState } from "react";
import { api } from "../api/client";
import { t } from "../i18n";
import { errText, toast } from "../toast";
import type { Secretish } from "./secretish";

const NAME = /^[A-Z][A-Z0-9_]{0,63}$/;

/** What shows of a value: its first four characters and its last two. */
export const glimpse = (v: string) => (v.length > 10 ? `${v.slice(0, 4)}…${v.slice(-2)}` : "…");

export function SaveSecret({ found, saved, skip }: { found: Secretish; saved: (name: string) => void; skip: () => void }) {
  const qc = useQueryClient();
  const names = useQuery({ queryKey: ["secrets"], queryFn: api.secrets, staleTime: 30_000, select: (s) => s.secrets.map((x) => x.name) }).data ?? [];
  const [name, setName] = useState(found.name);
  const [busy, setBusy] = useState(false);
  const taken = names.includes(name);
  const store = async () => {
    if (!NAME.test(name)) return;
    setBusy(true);
    try {
      await api.putSecret(name, { value: found.value });
      void qc.invalidateQueries({ queryKey: ["secrets"] });
      toast(t("secrets.stored", { name }));
      saved(name);
    } catch (e) {
      toast(t("err.secret"), { error: true, description: errText(e) });
    } finally {
      setBusy(false);
    }
  };
  return (
    <form
      role="dialog"
      aria-label={t("secrets.saveAsk")}
      className="g-pop glass thick float absolute inset-x-0 bottom-full z-10 mb-2 flex flex-col gap-2 rounded-lg p-2.5 @container"
      onSubmit={(e) => {
        e.preventDefault();
        void store();
      }}
    >
      <div className="flex items-start gap-2 text-sm">
        <LockKeyhole size={15} className="mt-0.5 shrink-0 text-ink-2" />
        <span className="min-w-0 flex-1">
          {t("secrets.saveAsk")} <code className="font-mono text-xs text-ink-3">{glimpse(found.value)}</code>
          <span className="block text-xs text-ink-3">{t("secrets.saveHint", { name: `{{${name || "NAME"}}}` })}</span>
        </span>
      </div>
      <div className="flex items-center gap-1.5 pl-[23px]">
        <input
          value={name}
          onChange={(e) => setName(e.target.value.toUpperCase().replace(/[^A-Z0-9_]/g, "_"))}
          aria-label={t("secrets.name")}
          spellCheck={false}
          className="h-8 min-w-0 flex-1 rounded-md bg-fill px-2 font-mono text-sm outline-none focus:bg-fill-2 pointer-coarse:h-10"
        />
        <button type="button" onClick={skip} className="g-press h-8 shrink-0 rounded-md bg-fill px-3 text-sm hover:bg-fill-2 pointer-coarse:h-10">
          {t("secrets.saveNo")}
        </button>
        <button type="submit" disabled={busy || !NAME.test(name)} className="g-press flex h-8 shrink-0 items-center gap-1.5 rounded-md bg-ink px-3 text-sm font-medium text-on-ink disabled:opacity-50 pointer-coarse:h-10">
          {busy && <LoaderCircle size={13} className="animate-spin" />}
          {taken ? t("secrets.saveReplace") : t("secrets.saveYes")}
        </button>
      </div>
    </form>
  );
}
