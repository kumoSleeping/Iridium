// What a turn shows while nothing is in the transcript yet (the user, 2026-11-06: no blank while the
// agent writes): a tool call as it is written, its command typed out live; and, when the turn has
// gone quiet a moment, three dots breathing at the conversation's end.
import { useEffect, useState } from "react";
import { t } from "../i18n";
import { cn } from "../ui";
import { cmdOfCall } from "./cmd";
import { argOf, kindOf } from "./fold";
import { partialInput, tail } from "./partial";
import { KindIcon } from "./said";

/** A tool call being written: its icon and name at once, a light sweeping across them, then what
 *  it is about typed out as it comes, with a caret; written whole, it waits for its block. */
export function ToolDraft({ name, input, whole }: { name: string; input: string; whole: boolean }) {
  const call = { name, input: JSON.stringify(partialInput(input)) };
  const a = argOf(call);
  return (
    <div className="g-fade flex h-7 min-w-0 items-center gap-2 text-sm" aria-live="off">
      <KindIcon kind={kindOf(name)} cmd={cmdOfCall(call)} />
      <span className="shine shrink-0">{name}</span>
      {a.arg ? (
        <span className={cn("min-w-0 truncate text-ink-3", a.code ? "font-mono text-xs" : "text-xs", !whole && "caret")}>{tail(a.arg)}</span>
      ) : (
        <span className="shine min-w-0 truncate text-xs">{t("chat.writingCall")}</span>
      )}
    </div>
  );
}

/** How long a turn may show nothing new before the dots come. */
const QUIET = 600;

/** Three dots breathing one after another at the conversation's end, while the turn runs and has
 *  shown nothing new for a moment (thinking, the network, a message not taken yet). quiet: what
 *  changes as something new shows; the dots wait QUIET again from then. */
export function Waiting({ on, quiet }: { on: boolean; quiet: string }) {
  const [shown, setShown] = useState(false);
  useEffect(() => {
    setShown(false);
    if (!on) return;
    const timer = setTimeout(() => setShown(true), QUIET);
    return () => clearTimeout(timer);
  }, [on, quiet]);
  if (!on || !shown) return null;
  return (
    <div className="g-fade flex h-7 items-center gap-1 pl-0.5" role="status" aria-label={t("chat.working")}>
      {[0, 1, 2].map((i) => (
        <span key={i} className="wait-dot" style={{ animationDelay: `${i * 160}ms` }} />
      ))}
    </div>
  );
}
