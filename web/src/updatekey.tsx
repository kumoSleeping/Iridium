// The update key by the settings key (sidebar.tsx): a part of its own, loaded once a newer
// release is found.
import { ArrowUpCircle, LoaderCircle } from "lucide-react";
import { useState } from "react";
import { api } from "./api/client";
import { manages, useMe, useUpdate } from "./data";
import { t } from "./i18n";
import { errText, toast } from "./toast";
import { Tip } from "./ui";
import { expectUpgrade } from "./upgrade";

/** A newer release found (the daemon looks by itself): 更新, left of the settings key, for those who
 *  may update (the user, 2026-11-06). Pressed, the daemon puts it in place and restarts, and the page
 *  loads the new build; downloading still, it says how far. */
export function UpdateKey() {
  const u = useUpdate().data;
  const me = useMe().data;
  const [busy, setBusy] = useState(false);
  if (!u?.available || !manages(me)) return null;
  const pct = u.phase === "downloading" && u.size ? Math.min(99, Math.floor(((u.got ?? 0) * 100) / u.size)) : null;
  const going = busy || u.phase === "restarting";
  const apply = () => {
    setBusy(true);
    expectUpgrade();
    void api.applyUpdate().then(
      () => setBusy(false),
      (e) => (setBusy(false), expectUpgrade(false), toast(t("err.update"), { error: true, description: errText(e) })),
    );
  };
  return (
    <Tip label={t("settings.updateTo", { v: u.latest ?? "" })}>
      <button type="button" disabled={going || u.phase === "checking"} onClick={apply} className="g-pop g-press flex h-7 items-center gap-1.5 rounded-full bg-ink px-3 text-sm font-medium text-on-ink tabular-nums disabled:opacity-60">
        {going ? <LoaderCircle size={13} className="animate-spin" /> : <ArrowUpCircle size={14} />}
        {pct !== null ? `${pct}%` : t("settings.update")}
      </button>
    </Tip>
  );
}
