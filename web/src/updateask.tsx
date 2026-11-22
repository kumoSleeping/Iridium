// The ask before an update (otae, 2026-11-22: a look never updates by itself; one 更新 only): what
// is new, the version now and the size, 取消 or 更新, out of what opened it. The settings' 版本 row
// (settings.tsx) and the 更新 key by the settings key (updatekey.tsx) open this one.
import { Popover } from "@base-ui/react/popover";
import { ArrowUpCircle } from "lucide-react";
import { useState } from "react";
import { api, ApiError } from "./api/client";
import type { UpdateState } from "./api/types.gen";
import { MainKey, SecondKey } from "./card";
import { t } from "./i18n";
import { errText, toast } from "./toast";
import { PopSurface } from "./ui";
import { expectUpgrade } from "./upgrade";

/** A download's size: "9.8 MB". */
export const mb = (n: number) => (n >= 1 << 20 ? `${(n / (1 << 20)).toFixed(1)} MB` : `${Math.max(1, Math.round(n / 1024))} KB`);

/** The ask for u's newer release, open out of anchor while open; may: the caller may update (else
 *  what is new alone). 更新 has the daemon download it, put it in place and restart; the page then
 *  loads the new build. The daemon's answers (409 nothing newer, a failed download) its state says;
 *  only a request that got no answer is told. */
export function UpdateAsk({ u, anchor, open, close, may, side = "top", align = "end" }: { u: UpdateState; anchor: Element | null; open: boolean; close: () => void; may: boolean; side?: "top" | "bottom"; align?: "start" | "end" }) {
  const [busy, setBusy] = useState(false);
  // The release's notes start with its own version ("- v0.1.31: …"): the title says it already.
  const notes = u.notes?.replace(/^\s*-?\s*v?\d+\.\d+\.\d+:\s*/, "").trim();
  const failed = u.phase === "failed" && u.error;
  const apply = () => {
    setBusy(true);
    expectUpgrade();
    void api.applyUpdate().then(
      () => (setBusy(false), close()),
      (e) => (setBusy(false), expectUpgrade(false), close(), !(e instanceof ApiError) && toast(t("err.update"), { error: true, description: errText(e) })),
    );
  };
  const shown = open && !!anchor && u.available && u.phase !== "downloading" && u.phase !== "restarting";
  return (
    <Popover.Root open={shown} onOpenChange={(o) => !o && close()}>
      <PopSurface anchor={anchor} side={side} align={align} className="flex max-h-[min(440px,var(--available-height))] w-[min(320px,calc(100vw-16px))] flex-col p-3">
        <Popover.Title className="m-0 flex items-center gap-2 text-sm font-semibold">
          <ArrowUpCircle size={15} className="shrink-0 text-ink-2" />
          <span className="truncate">{t("settings.updateAsk", { v: u.latest ?? "" })}</span>
        </Popover.Title>
        <p className="m-0 mt-1 pl-[23px] text-xs break-all text-ink-3 tabular-nums">
          {t("settings.current", { v: u.current })}
          {u.size ? ` · ${mb(u.size)}` : null}
        </p>
        {failed && <p className="m-0 mt-1 pl-[23px] text-xs break-all text-err">{t("settings.updateFailed", { e: u.error ?? "" })}</p>}
        {notes && <p className="no-bar m-0 mt-2.5 min-h-0 flex-1 overflow-y-auto pl-[23px] text-xs leading-5 whitespace-pre-wrap text-ink-2">{notes}</p>}
        {may && (
          <div className="mt-3 flex shrink-0 justify-end gap-1.5">
            <SecondKey onClick={close}>{t("me.cancel")}</SecondKey>
            <MainKey busy={busy} onClick={apply}>
              {t("settings.updateGo")}
            </MainKey>
          </div>
        )}
      </PopSurface>
    </Popover.Root>
  );
}
