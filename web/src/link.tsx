// The link to the daemon: a dot green under 0.5 s round trip, amber to 2 s, red beyond or cut off;
// the time beside it. With the sidebar shown, at the right of its foot, by you (the user,
// 2026-11-08); on a phone, in the chat's top bar left of ⋯ (2026-11-06).
import { t } from "./i18n";
import { useLink } from "./store";
import { cn, Tip } from "./ui";

export function LinkDot({ className, side = "bottom" }: { className?: string; side?: "top" | "bottom" }) {
  const link = useLink();
  const label = link.live === false ? t("top.offline") : link.rtt !== null ? t("top.online", { ms: link.rtt }) : t("top.connecting");
  const tone = link.live === false ? "bg-err" : link.rtt === null ? "bg-ink-4" : link.rtt < 500 ? "bg-run" : link.rtt <= 2000 ? "bg-need" : "bg-err";
  return (
    <Tip label={label} side={side}>
      <span className={cn("flex items-center gap-1.5 px-1.5 text-xs text-ink-3 tabular-nums", className)} role="status" aria-label={label}>
        <span className={cn("size-1.5 rounded-full transition-colors duration-(--dur-2)", tone)} />
        {link.live === false ? t("top.offlineShort") : link.rtt !== null ? `${link.rtt} ms` : ""}
      </span>
    </Tip>
  );
}
