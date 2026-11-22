// The update key by the settings key (sidebar.tsx): a part of its own, loaded once a newer
// release is found.
import { ArrowUpCircle, LoaderCircle } from "lucide-react";
import { useState } from "react";
import { manages, useMe, useUpdate } from "./data";
import { t } from "./i18n";
import { Tip } from "./ui";
import { UpdateAsk } from "./updateask";

/** A newer release found (the daemon looks by itself): its version, left of the settings key, for
 *  those who may update (the user, 2026-11-06). Pressed, the same ask as the settings' 版本 row
 *  (updateask.tsx; otae, 2026-11-22: not at once; the version, not 更新: the ask's 更新并重启 is the
 *  one key that updates); downloading still, it says how far. */
export function UpdateKey() {
  const u = useUpdate().data;
  const me = useMe().data;
  const [ask, setAsk] = useState(false);
  const [key, setKey] = useState<HTMLButtonElement | null>(null);
  if (!u?.available || !manages(me)) return null;
  const pct = u.phase === "downloading" && u.size ? Math.min(99, Math.floor(((u.got ?? 0) * 100) / u.size)) : null;
  const going = u.phase === "restarting";
  return (
    <>
      <Tip label={t("settings.updateTo", { v: u.latest ?? "" })}>
        <button ref={setKey} type="button" disabled={going || pct !== null || u.phase === "checking"} onClick={() => setAsk(true)} aria-haspopup="dialog" className="g-pop g-press flex h-7 items-center gap-1.5 rounded-full bg-ink px-3 text-sm font-medium text-on-ink tabular-nums disabled:opacity-60">
          {going ? <LoaderCircle size={13} className="animate-spin" /> : <ArrowUpCircle size={14} />}
          {pct !== null ? `${pct}%` : u.latest}
        </button>
      </Tip>
      <UpdateAsk u={u} anchor={key} open={ask} close={() => setAsk(false)} may side="top" align="start" />
    </>
  );
}
