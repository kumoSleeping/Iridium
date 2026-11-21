// The composer's words as its box holds them (the user, 2026-11-12; refs.ts Mention): a session or
// a place named is its mark and its name, a member named (kumo, 2026-11-21) its @name, each one
// piece the caret steps over and an edit takes whole. Apart from refs.ts: only the composer's.
import { nickSpans } from "./channel/lib";
import { mentions, type Mention } from "./refs";

/** The slot a mention's mark (an agent's logo, a folder) is drawn in, in the composer's box. */
export const MARK = "\u2003";

/** A member named in the composer's words (@nick, @name; kumo, 2026-11-21): one piece as a
 *  mention is, its letters the words' own. */
export type NickRef = { kind: "nick"; nick: string };

/** The composer's words as its box holds them (the user, 2026-11-12): each mention its mark's slot
 *  and its name, the link behind it kept aside; each member named (names: those @ may name) as
 *  it is written, one piece all the same. refs: where each is, in value (from, to) and in the
 *  words (at, end). */
export type Boxed = { value: string; refs: { from: number; to: number; at: number; end: number; m: Mention | NickRef }[] };

export function boxed(words: string, names: readonly string[] = []): Boxed {
  let value = "";
  let at = 0;
  const refs: Boxed["refs"] = [];
  for (const p of mentions(words)) {
    if (typeof p === "string") {
      for (const n of nickSpans(p, [...names])) refs.push({ from: value.length + n.from, to: value.length + n.to, at: at + n.from, end: at + n.to, m: { kind: "nick", nick: n.nick } });
      value += p;
      at += p.length;
      continue;
    }
    const shown = MARK + (p.kind === "session" ? p.title : p.name);
    refs.push({ from: value.length, to: value.length + shown.length, at, end: at + p.text.length, m: p });
    value += shown;
    at += p.text.length;
  }
  return { value, refs };
}

/** Where the box's position i is in the words (inside a mention: its start). */
export function wordsAt(b: Boxed, i: number): number {
  let d = 0;
  for (const r of b.refs) {
    if (r.from >= i) break;
    if (r.to > i) return r.at;
    d += r.end - r.at - (r.to - r.from);
  }
  return i + d;
}

/** Where the words' position i is in the box (inside a link: its start). */
export function boxAt(b: Boxed, i: number): number {
  let d = 0;
  for (const r of b.refs) {
    if (r.at >= i) break;
    if (r.end > i) return r.from;
    d += r.end - r.at - (r.to - r.from);
  }
  return i - d;
}

/** The words after the box was edited from what boxed(words) gave to value: a mention the edit
 *  touches goes whole (one Backspace takes it), letters typed inside one go after it. caret: where
 *  the box's caret then is. at: the box's caret after the edit, which settles where letters
 *  like their neighbours went (an a typed before an a). */
export function edited(words: string, value: string, at?: number, names: readonly string[] = []): { words: string; caret: number } {
  const b = boxed(words, names);
  const was = b.value;
  // What follows the caret is what was there; then what precedes the change.
  let s = 0;
  while (s < was.length && s < value.length - (at ?? 0) && was[was.length - 1 - s] === value[value.length - 1 - s]) s++;
  let p = 0;
  while (p < was.length - s && p < value.length - s && was[p] === value[p]) p++;
  const typed = value.slice(p, value.length - s);
  let lo = p, hi = was.length - s;
  for (const r of b.refs) {
    if (lo === hi) {
      if (r.from < lo && lo < r.to) lo = hi = r.to;
    } else if (r.from < hi && r.to > lo) {
      lo = Math.min(lo, r.from);
      hi = Math.max(hi, r.to);
    }
  }
  const head = words.slice(0, wordsAt(b, lo)) + typed;
  return { words: head + words.slice(wordsAt(b, hi)), caret: boxed(head).value.length };
}

/** The caret moved into a mention's name: from one of its edges (was: an arrow key stepping in),
 *  on to the other, past it (back to the nearer one, an arrow could never get past it); from
 *  elsewhere, to its nearer edge. */
export function outOf(b: Boxed, i: number, was?: number): number {
  const r = b.refs.find((r) => r.from < i && i < r.to);
  if (!r) return i;
  if (was === r.from) return r.to;
  if (was === r.to) return r.from;
  return i - r.from < r.to - i ? r.from : r.to;
}
