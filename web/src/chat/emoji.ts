// The emoji a reaction may be (docs/channels.md "表情"): a compact table of the common ones,
// grouped, built in (nothing fetched); and the ones pressed lately, kept in this browser, the
// first two rows of a message's menu.
import { keep, kept } from "../ui";

/** The table's groups, in order (faces, hands, symbols, things, nature): a hairline between them. */
export const EMOJI: { key: "faces" | "hands" | "hearts" | "things" | "nature"; list: string[] }[] = [
  {
    key: "faces",
    list: [..."😀😃😄😁😆😅🤣😂🙂🙃😉😊😇🥰😍🤩😘😋😛😜🤪😝🤗🤭🤫🤔🤐🤨😐😑😶😏😒🙄😬😌😔😪😴😷🤒🤢🤮🥵🥶🥴😵🤯🤠🥳😎🤓🧐😕😟🙁😮😯😲😳🥺😦😧😨😰😥😢😭😱😖😣😞😓😩😫🥱😤😡😠🤬😈💀🤡👻👽🤖"],
  },
  { key: "hands", list: ["👍", "👎", "👌", "🤌", "✌️", "🤞", "🤟", "🤘", "🤙", "👈", "👉", "👆", "👇", "☝️", "✋", "🖐️", "👋", "🤝", "🙏", "👏", "🙌", "👐", "🤲", "💪", "✍️", "🫡", "🫶", "👀"] },
  { key: "hearts", list: ["❤️", "🧡", "💛", "💚", "💙", "💜", "🖤", "🤍", "🤎", "💔", "💕", "💞", "💖", "💯", "💢", "💥", "💫", "💦", "💬", "💤", "✅", "❌", "❓", "❗", "⭕", "🚫", "⚠️", "🔥", "✨", "⭐"] },
  { key: "things", list: ["🎉", "🎊", "🎁", "🏆", "🥇", "🎯", "🚀", "💡", "🔧", "🔨", "⚙️", "🧪", "📌", "📎", "📝", "📚", "💻", "📱", "🔍", "🔒", "🔑", "⏰", "⌛", "☕", "🍵", "🍺", "🍻", "🥂", "🍰", "🎂", "🍕", "🍔", "🍜", "🍣", "🍎", "🍉"] },
  { key: "nature", list: ["🌈", "☀️", "🌙", "⚡", "❄️", "🌊", "🌸", "🌹", "🍀", "🌱", "🐶", "🐱", "🐼", "🦊", "🐸", "🐧", "🦄", "🐝", "🐢", "🐟"] },
];

/** How many a row of the menu holds; its rows of emoji pressed lately. */
export const ROW = 6;
export const ROWS = 2;
/** Before any is pressed here. */
export const FIRST = ["👍", "❤️", "😂", "🎉", "👀", "🙏", "✅", "🔥", "🤔", "😮", "👏", "💯"];

const KEY = "emoji.recent";

/** recent with e first: each once, at most max. */
export function bumped(recent: string[], e: string, max = ROW * ROWS): string[] {
  return [e, ...recent.filter((x) => x !== e)].slice(0, max);
}

/** The emoji pressed lately, the latest first, filled up from FIRST to two rows. */
export function recentEmoji(raw: string | null = kept(KEY)): string[] {
  let mine: string[] = [];
  try {
    const v = raw ? (JSON.parse(raw) as unknown) : [];
    if (Array.isArray(v)) mine = v.filter((x): x is string => typeof x === "string" && x.length > 0 && x.length <= 16);
  } catch {}
  const out = [...new Set(mine)].slice(0, ROW * ROWS);
  for (const e of FIRST) if (out.length < ROW * ROWS && !out.includes(e)) out.push(e);
  return out;
}

/** e was pressed: first in the menu next time. */
export function usedEmoji(e: string) {
  keep(KEY, JSON.stringify(bumped(recentEmoji(), e)));
}
