// Where work was done when that is not the channel's folder (the user, 2026-11-18): said at the
// head of an agent's work line (work.tsx): a computer's face, its name and its link's lamp; a
// folder here its mark and its name.
import { Folder } from "lucide-react";
import { useMachines } from "../data";
import { t } from "../i18n";
import { LINK_COLOR, MachineFace, shownPath, useLink, type Machine } from "../machines";
import type { Place } from "../places";
import { useListRow } from "../store";
import { cn, tilde } from "../ui";
import { awayOf, folderName } from "./awayof";

/** Where session works when that is not channel's folder: both as the list has them. */
export function useAway(channel: string, session: string): Place | undefined {
  // Its two rows alone: a message's line is not drawn again as any other session changes.
  const own = channel === session;
  const home = useListRow(own ? undefined : channel);
  const at = useListRow(own ? undefined : session);
  if (own) return undefined;
  // Known only while both rows are listed. As last known meanwhile (the list not come yet, a row
  // gone from it while its computer's link is down): what was in the box stays in it, not out of
  // it and in again each time the link comes and goes (kumo, 2026-11-19).
  const key = `${channel} ${session}`;
  if (home?.cwd && at?.cwd) lastAway.set(key, awayOf(home, at));
  return lastAway.get(key);
}
const lastAway = new Map<string, Place | undefined>();

/** The computers as last listed, by id: one not listed for a moment keeps its name and face. */
const lastMachine = new Map<string, Machine>();

/** A computer's link: its lamp, and 已断开 once it has stayed away. */
function Link({ m }: { m: Machine }) {
  const link = useLink(m);
  return (
    <>
      <span role="img" aria-label={link === "away" ? t("status.offline") : undefined} className="size-1.5 shrink-0 rounded-full" style={{ background: LINK_COLOR[link] }} />
      {link === "away" && <span className="shrink-0 whitespace-nowrap text-err">{t("status.offline")}</span>}
    </>
  );
}

/** The line that says where: the mark, the name (never wrapped: cut short), the path after it,
 *  a gap between them and no dot (docs/前端设计规范.md: 不用“·”分隔). short: the mark and the
 *  name alone (a prefix of a line that says more). */
export function Where({ place, short }: { place?: Place; short?: boolean }) {
  const ms = useMachines();
  if (!place) return null;
  for (const x of ms) lastMachine.set(x.id, x);
  const m = place.machine ? (ms.find((x) => x.id === place.machine) ?? lastMachine.get(place.machine)) : undefined;
  const path = m ? shownPath(`${m.id}:${place.path.replaceAll("\\", "/")}`, ms).label : tilde(place.path);
  const name = m?.name ?? place.machine ?? folderName(place.path);
  return (
    // Short (a work line's head): kept whole, its name capped; the step after it gives way, not
    // run over by it (StarFreedomX, 2026-11-27: the name under the step's icon).
    <span className={cn("mr-1 flex items-center gap-1.5", short ? "shrink-0" : "min-w-0 shrink")} title={`${place.machine ? `${name}:` : ""}${place.path}`}>
      {m ? <MachineFace m={m} size={16} className={cn(!m.online && "opacity-50 grayscale")} /> : <Folder size={14} className="shrink-0" aria-hidden />}
      <span className={cn("shrink-0 truncate font-medium text-ink-2", short ? "max-w-40" : "max-w-[55%]")}>{name}</span>
      {m && <Link m={m} />}
      {!short && <span className="min-w-0 truncate">{m && place.machine && m.sep === "\\" ? path.replaceAll("/", "\\") : path}</span>}
    </span>
  );
}

// Two thirds of the column on a computer, and light (kumo, 2026-11-24: not the whole width, less grey).
