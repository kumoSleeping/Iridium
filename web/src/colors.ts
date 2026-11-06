// Colour tags (the user, 2026-11-06): at the foot of a session's or a folder's menu, a row of
// six dots; one press tags it, a press on its own colour takes the tag off. A session's is a
// highlighter's stroke over its name (sidebar.tsx Marked), a folder's tints the folder. Kept by
// the daemon: a session's on its row (Session.color), a folder's by path in the carried state
// "colors".
import type { Color, FolderColors } from "./api/types.gen";
import { useCarried } from "./store";

export const COLORS: Color[] = ["red", "orange", "yellow", "green", "blue", "purple"];

export const colorVar = (c: Color) => `var(--tag-${c})`;

const NONE: FolderColors = {};
/** Every folder's colour tag, by path; none for a terminal that is not a panel. */
export const useFolderColors = () => useCarried<FolderColors>("colors") ?? NONE;
