// Who may move the page (docs/动画.md): what changes a row's place or height by an animation, or
// writes a scroll place, is one of the few listed here. A new one is not added to a component:
// it is said to the page's movers (chat/stick.tsx for the conversation's end, moves.ts for rows
// that change places), or, when it must be its own, added here with its reason in docs/动画.md.
import { readdirSync, readFileSync, statSync } from "node:fs";
import { join, relative } from "node:path";
import { expect, test } from "vitest";

const src = join(import.meta.dirname, "../src");
const files = (dir: string): string[] => readdirSync(dir).flatMap((f) => (statSync(join(dir, f)).isDirectory() ? files(join(dir, f)) : /\.(tsx?|css)$/.test(f) ? [join(dir, f)] : []));
const code = (f: string) =>
  readFileSync(f, "utf8")
    .split("\n")
    .filter((l) => !/^\s*(\/\/|\*|\/\*)/.test(l))
    .join("\n");
const using = (re: RegExp) =>
  files(src)
    .filter((f) => re.test(code(f)))
    .map((f) => relative(src, f))
    .sort();

test("an animation made by script only where it is listed", () => {
  expect(using(/\.animate\(/)).toEqual([
    "chat/channel/jump.ts", // the message a quote went to, lit for a moment (colour only)
    "chat/longwords.tsx", // long words opened or folded: their own height, told to the chat (layoutMoves)
    "chat/stick.tsx", // a fold pressed: its height and its rows' fading
    "motion.ts", // a row's keys sliding sideways (useGlide), a message sent rising in (pushIn, fadeIn)
    "moves.ts", // rows that change places
    "usage.tsx", // the usage panel's numbers
  ]);
});

test("a conversation's scroll place written only by its follow, and where a thing goes to something", () => {
  expect(using(/\bscrollTop\s*[+-]?=[^=]|scrollIntoView\(|\.scrollTo\(/).filter((f) => f.startsWith("chat/"))).toEqual([
    "chat/channel/jump.ts", // to the message a quote answers
    "chat/channel/nicklist.tsx", // the @ list's own rows
    "chat/composer.tsx", // the composer's own lists
    "chat/mention.tsx", // the composer's backdrop with its text
    "chat/meters.tsx", // the model list's own rows
    "chat/said.tsx", // a steps' popover's own list, kept at its newest
    "chat/stick.tsx",
  ]);
});

test("a row's room opened or shut by CSS only where it is listed", () => {
  expect(using(/g-reveal|grid-rows-\[0fr\]/).filter((f) => f.startsWith("chat/"))).toEqual([
    "chat/channel/members.tsx",
    "chat/channel/profilecard.tsx",
    "chat/channel/script.tsx",
    "chat/composer.tsx",
    "chat/dispatched.tsx",
    "chat/forwardcard.tsx",
    "chat/react.tsx",
    "chat/said.tsx",
  ]);
});
