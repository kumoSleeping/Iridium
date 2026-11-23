// The desktop app's window keys (internal/desktop/chrome.go): the windows have no title bar, their
// keys are the pages' own, the top bar's own icon keys (kumo, 2026-11-22: no round marks). Three:
// minimise (QGI and kumo, 2026-11-23: a window had no way down to the task bar or the Dock), full
// screen, close. Full screen is each system's own (kumo, 2026-11-22): a Mac's full screen (its green
// button), Windows' maximise and back.
import { Maximize2, Minimize2, Minus, Square, Copy, X } from "lucide-react";
import { t } from "./i18n";
import { cn, IconKey } from "./ui";

/** Minimise, full screen (a Mac) or maximise (Windows, Linux), close. ask: the app asked a key's verb. */
export function WinKeyPair({
  mac,
  full,
  ask,
  className,
}: {
  mac: boolean;
  full: boolean;
  ask: (verb: string) => void;
  className?: string;
}) {
  return (
    <span
      data-win-keys=""
      className={cn("flex shrink-0 items-center gap-0.5", className)}
    >
      <IconKey label={t("app.min")} tipSide="bottom" onClick={() => ask("min")}>
        <Minus size={15} />
      </IconKey>
      {mac ? (
        <IconKey
          label={full ? t("app.unfull") : t("app.full")}
          tipSide="bottom"
          onClick={() => ask("full")}
        >
          {full ? <Minimize2 size={15} /> : <Maximize2 size={15} />}
        </IconKey>
      ) : (
        <IconKey
          label={full ? t("app.restore") : t("app.max")}
          tipSide="bottom"
          onClick={() => ask("max")}
        >
          {full ? (
            <Copy size={13} className="-scale-x-100" />
          ) : (
            <Square size={13} />
          )}
        </IconKey>
      )}
      <IconKey
        label={t("app.close")}
        tipSide="bottom"
        onClick={() => ask("close")}
      >
        <X size={16} />
      </IconKey>
    </span>
  );
}
