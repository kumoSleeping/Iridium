// The voice's small parts that are on the page before any room is joined: the dock's place (the
// dock itself, key.tsx, comes only once in a room), the entry in a folder group's "+", and the
// count on a group's row of who is in its voice.
import { AudioLines, PhoneOff } from "lucide-react";
import { lazy, Suspense } from "react";
import { t } from "../i18n";
import { useVoiceRooms } from "../store";
import { cn, Tip } from "../ui";
import { join, leave, useVoice } from "./state";

const Part = lazy(() => import("./key").then((m) => ({ default: m.VoiceDock })));
/** The voice this page is in, where it stands: beside me at the sidebar's foot, or (a phone) in
 *  the chat's bar. Nothing, and nothing loaded, while in no room. */
export function VoiceDock(p: { side?: "top" | "bottom"; className?: string }) {
  if (useVoice().status === "off") return null;
  return (
    <Suspense>
      <Part {...p} />
    </Suspense>
  );
}

/** A folder group's voice, in its "+": into it (how many are there, if any), or out of it. */
export function VoiceEntry({ guild, done }: { guild: number; done: () => void }) {
  const v = useVoice();
  const there = useVoiceRooms()?.[String(guild)]?.length ?? 0;
  const here = v.room === String(guild) && v.status !== "off";
  return (
    <button type="button" onClick={() => ((here ? leave() : join(String(guild))), done())} className={cn("flex h-9 w-full items-center gap-2 rounded-md px-2 text-left text-sm hover:bg-fill pointer-coarse:h-11", here && "text-err")}>
      {here ? <PhoneOff size={16} className="shrink-0" /> : <AudioLines size={16} className="shrink-0 text-ink-3" />}
      <span className="min-w-0 flex-1 truncate">{here ? t("voice.leave") : t("voice.join")}</span>
      {there > 0 && <span className="shrink-0 text-xs text-run tabular-nums">{t("voice.count", { n: there })}</span>}
    </button>
  );
}

/** On a folder group's row: how many are in its voice, to those not in it too; pressed, into it. */
export function VoiceMark({ guild }: { guild: number }) {
  const v = useVoice();
  const there = useVoiceRooms()?.[String(guild)]?.length ?? 0;
  if (!there) return null;
  const here = v.room === String(guild) && v.status !== "off";
  return (
    <Tip label={t("voice.here", { n: there })} side="bottom">
      <button type="button" disabled={here} aria-label={t("voice.here", { n: there })} onClick={(e) => (e.stopPropagation(), join(String(guild)))} className="g-pop g-press flex h-6 items-center gap-0.5 rounded-sm px-1 text-xs text-run tabular-nums hover:bg-fill-2 disabled:hover:bg-transparent pointer-coarse:h-8">
        <AudioLines size={12} />
        {there}
      </button>
    </Tip>
  );
}
