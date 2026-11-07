// Web Push's rules (docs/api.md "推送"): the settings' total switch and default events, and over
// them what a workspace, a folder or a session says from its menu (right click, long-press):
// 跟随上级, 开 or 关, and with 开 its own events. The nearest rule says on or off; the events are
// the nearest 开 one's with events of its own, else the defaults (internal/push Resolve). The menus
// show it with the total switch off too, and a level set 开 there turns the switch on, without a
// word (the user, 2026-11-07). The menus' part (pushmenu.tsx) comes after the first screen: it stays
// under its size (web/test/build.test.ts).
import { useQuery } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import type { PushEvents, PushScope, PushState, Session } from "./api/types.gen";
import { pushQ } from "./data";

/** A rule's place: a workspace's id, a folder's absolute path, a session's id. */
export type PushLevel = { scope: PushScope; target: string };
/** A workspace's levels, its own only (above it, the defaults). */
export const wsLevels = (ws: number): PushLevel[] => [{ scope: "workspace", target: String(ws) }];
/** A folder's levels, nearest first. */
export const folderLevels = (path: string, ws: number): PushLevel[] => [{ scope: "folder", target: path }, ...wsLevels(ws)];
/** A session's levels, nearest first: itself, its folder (its cwd), its workspace. */
export const sessionLevels = (s: Session): PushLevel[] => [{ scope: "session", target: s.id }, ...folderLevels(s.cwd, s.workspace ?? 0)];

export const ruleAt = (st: PushState, l: PushLevel) => st.rules.find((r) => r.scope === l.scope && r.target === l.target);
/** On or off by the nearest rule; none: on. */
export const pushOn = (st: PushState, levels: PushLevel[]) => (levels.map((l) => ruleAt(st, l)).find((r) => r && r.mode !== "inherit")?.mode ?? "on") === "on";
/** The events of the nearest rule on with events of its own, else the defaults. */
export const pushEvents = (st: PushState, levels: PushLevel[]): PushEvents => levels.map((l) => ruleAt(st, l)).find((r) => r?.mode === "on" && r.events)?.events ?? st.defaults;

type Menus = typeof import("./pushmenu");
let menus: Menus | undefined;
let coming: Promise<Menus> | undefined;
const loadMenus = () => (coming ??= import("./pushmenu").then((m) => (menus = m)).catch((e) => ((coming = undefined), Promise.reject(e))));

/** The push state and the menus' part (fetched once the state is here); else null: this page may
 *  not see push. */
export function usePushMenus(): { st: PushState; m: Menus } | null {
  const st = useQuery(pushQ).data;
  const [m, setM] = useState(menus);
  const here = !!st;
  useEffect(() => {
    if (here && !m) void loadMenus().then(setM, () => {});
  }, [here, m]);
  return st && m ? { st, m } : null;
}
