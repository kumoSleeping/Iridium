// Colour tags (the user, 2026-11-06): at the foot of a session's or a folder's menu, a row of
// six dots; one press tags it, a press on its own colour takes the tag off. A session's is a
// highlighter's stroke over its name (sidebar.tsx Marked), a folder's tints the folder. Kept by
// the daemon: a session's on its row (Session.color), a folder's on its mark in the carried
// "guilds".
import type { Color } from "./api/types.gen";
import { colorsOf, NO_GUILDS, useGuilds, type FolderColors } from "./guilds";

export const COLORS: Color[] = ["red", "orange", "yellow", "green", "blue", "purple"];

export const colorVar = (c: Color) => `var(--tag-${c})`;

/** Every folder's colour tag, by path. */
export const useFolderColors = (): FolderColors => colorsOf(useGuilds() ?? NO_GUILDS);
