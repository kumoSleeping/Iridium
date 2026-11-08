// How a session's last process ended badly, under its chat: one quiet line (the cause in words),
// and what it last wrote to stderr behind 详情. The server's text is "<from> exited N" or
// "<from> killed by SIG", then ": " and stderr's last lines joined by " / " (session/hosted.go);
// any other error is shown as it is.
import { ChevronRight, TriangleAlert } from "lucide-react";
import { useState } from "react";
import { t } from "../i18n";
import { cn } from "../ui";

/** The server's error as a cause in words and the lines behind it. */
export function crashParts(error: string): { cause: string; lines: string[] } {
  const m = /^(?:agent|holder|\S+)(?: exited (-?\d+))?(?: killed by (\w+))?(?:: ([\s\S]*))?$/.exec(error);
  if (!m || (!m[1] && !m[2])) return { cause: error.length > 120 ? error.slice(0, 120) + "…" : error, lines: error.length > 120 ? [error] : [] };
  const cause = m[2] ? t("chat.crashSignal", { signal: m[2] }) : t("chat.crashCode", { code: m[1] });
  return { cause, lines: (m[3] ?? "").split(" / ").filter(Boolean) };
}

export function Crash({ error }: { error: string }) {
  const [open, setOpen] = useState(false);
  const { cause, lines } = crashParts(error);
  return (
    <div className="mx-auto flex max-w-full flex-col items-center gap-1.5 text-xs text-ink-3">
      <p className="m-0 flex items-center gap-1.5">
        <TriangleAlert size={13} className="shrink-0 text-err" aria-hidden />
        <span>{t("chat.crashed")}</span>
        <span className="text-ink-4">·</span>
        <span>{cause}</span>
        {lines.length > 0 && (
          <button type="button" onClick={() => setOpen((o) => !o)} aria-expanded={open} className="ml-0.5 inline-flex cursor-pointer items-center text-ink-3 hover:text-ink-2">
            {t("chat.crashDetails")}
            <ChevronRight size={12} className={cn("transition-transform duration-(--dur-2) ease-(--ease-out)", open && "rotate-90")} aria-hidden />
          </button>
        )}
      </p>
      {open && (
        <pre className="g-pop m-0 max-h-40 w-full max-w-[640px] overflow-auto rounded-md bg-fill px-3 py-2 text-left font-mono text-[11px] leading-[17px] whitespace-pre-wrap text-ink-2">
          {lines.join("\n")}
        </pre>
      )}
    </div>
  );
}
