// The link to the daemon: a dot by its round trip (green under 500ms, yellow to 2s, red slower or
// cut off) and the milliseconds, small (kumo, 2026-11-26: the five bars gone again). With the sidebar shown, right of the name at its top (the user,
// 2026-11-08); on a phone, in the chat's top bar left of ⋯ (2026-11-06). The daemon serving a
// newer build than the page's (and the page not loading it by itself yet), in its place ↻ 刷新,
// which loads it (2026-11-08). In the desktop app, with its own update downloaded, in its place
// 更新, which restarts the app on it (selfupdate.go, 2026-11-26).
import { CircleArrowUp, RotateCw } from "lucide-react";
import { t } from "./i18n";
import { ask, useApp } from "./irapp";
import { useLink } from "./store";
import { cn, Tip } from "./ui";
import { reloadServed, useStale } from "./upgrade";

export function LinkDot({ className, side = "bottom" }: { className?: string; side?: "top" | "bottom" }) {
  const link = useLink();
  const stale = useStale();
  const update = useApp()?.update;
  if (update && !stale && link.live !== false)
    return (
      <Tip label={t("app.restartTo", { v: update })} side={side}>
        <button type="button" onClick={() => ask("update", "restart")} className={cn("g-pop g-press flex items-center gap-1 rounded-md px-1.5 text-xs font-medium text-ink-2 hover:text-ink", className)}>
          <CircleArrowUp size={11} aria-hidden />
          {t("app.update")}
        </button>
      </Tip>
    );
  if (stale && link.live !== false)
    return (
      <Tip label={t("top.reloadHint")} side={side}>
        <button type="button" onClick={reloadServed} className={cn("g-pop g-press flex items-center gap-1 rounded-md px-1.5 text-xs font-medium text-ink-2 hover:text-ink", className)}>
          <RotateCw size={11} aria-hidden />
          {t("top.reload")}
        </button>
      </Tip>
    );
  const label = link.live === false ? t("top.offline") : link.rtt !== null ? t("top.online", { ms: link.rtt }) : t("top.connecting");
  const tone = link.live === false ? "bg-err" : link.rtt === null ? "bg-ink-4" : link.rtt < 500 ? "bg-run" : link.rtt <= 2000 ? "bg-need" : "bg-err";
  // Close after the name, little room (kumo, 2026-11-26): the number, its ms smaller and fainter
  // on its baseline, then the dot (kumo, 2026-11-26: the dot behind).
  return (
    <Tip label={label} side={side} tap>
      <span className={cn("-ml-1 flex items-center gap-1 text-[11px] leading-none text-ink-3 tabular-nums", className)} role="status" aria-label={label}>
        {link.live === false ? (
          t("top.offlineShort")
        ) : link.rtt !== null ? (
          <span className="flex items-baseline">
            {link.rtt}
            <span className="text-[9px] text-ink-4">ms</span>
          </span>
        ) : null}
        <span className={cn("size-1.5 shrink-0 rounded-full transition-colors duration-(--dur-2)", tone)} />
      </span>
    </Tip>
  );
}
