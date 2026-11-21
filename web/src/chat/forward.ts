// Merged forwards in what is said (Satori's <message forward>, docs/channels.md "元素"): a channel
// keeps one as written; the page draws it as the card an agent's own record draws a relay with
// (said.tsx Relayed), pure here for tests. Each message of it: who said it, its words, and the
// forwards inside it, opened level by level.
import { parse, type El } from "../satori";

export type FwdMsg = { id: string; who: string; user: string; text: string; forwards: Fwd[] };
export type Fwd = { msgs: FwdMsg[] };

const OPEN = /<message\b[^<>]*?\sforward\b[^<>]*>/g;
const TAG = /<(\/?)message\b[^<>]*?(\/?)>/g;

/** Where the <message> opened before from closes, past its </message>; -1 if it does not. */
function endOf(text: string, from: number): number {
  let depth = 1;
  TAG.lastIndex = from;
  for (let m = TAG.exec(text); m; m = TAG.exec(text)) {
    if (m[1]) depth--;
    else if (!m[2]) depth++;
    if (depth === 0) return m.index + m[0].length;
  }
  return -1;
}

/** Whether text's position at is in Markdown code (a fenced block, an inline span). */
function inCode(text: string, at: number): boolean {
  const head = text.slice(0, at);
  let fenced = false;
  for (const line of head.split("\n")) if (/^\s*(```|~~~)/.test(line)) fenced = !fenced;
  if (fenced) return true;
  return (head.slice(head.lastIndexOf("\n") + 1).match(/`/g)?.length ?? 0) % 2 === 1;
}

/** What one element of a forwarded message says as words: text as it is, an <at> as @name, a
 *  line break, a picture or file by its title; the rest by its children. */
function wordsOf(els: El[]): string {
  let out = "";
  for (const e of els) {
    if (e.type === "text") out += e.text ?? "";
    else if (e.type === "br") out += "\n";
    else if (e.type === "at") out += `@${(typeof e.attrs.name === "string" && e.attrs.name) || (e.attrs.type === "all" ? "全体成员" : e.attrs.role === "agent" ? "全体Agent" : typeof e.attrs.id === "string" ? e.attrs.id : "")}`;
    else if (["img", "image", "video", "audio", "file"].includes(e.type)) out += `[${(typeof e.attrs.title === "string" && e.attrs.title) || e.type}]`;
    else if (e.type === "p") out += (out && !out.endsWith("\n") ? "\n" : "") + wordsOf(e.children) + "\n";
    else if (e.type !== "author" && !(e.type === "message" && e.attrs.forward !== undefined) && !e.type.includes(":")) out += wordsOf(e.children);
  }
  return out;
}

/** A <message forward> element as the page reads it. */
function fwdOf(e: El): Fwd {
  const msgs: FwdMsg[] = [];
  for (const m of e.children) {
    if (m.type !== "message") continue;
    const au = m.children.find((c) => c.type === "author");
    const str = (v: string | true | undefined) => (typeof v === "string" ? v : "");
    const who = str(au?.attrs.name) || str(au?.attrs.nick) || str(au?.attrs.id);
    const text = wordsOf(m.children).trim();
    const forwards = m.children.filter((c) => c.type === "message" && c.attrs.forward !== undefined).map(fwdOf);
    if (!who && !text && !forwards.length) continue;
    msgs.push({ id: str(m.attrs.id), who, user: str(au?.attrs.id), text, forwards });
  }
  return { msgs };
}

/** Words with the forwards written in them (outside code) taken out: what is left, and the
 *  forwards in order. */
export function splitForwards(text: string): { words: string; forwards: Fwd[] } {
  if (!text.includes("forward")) return { words: text, forwards: [] };
  const forwards: Fwd[] = [];
  let words = "";
  let at = 0;
  OPEN.lastIndex = 0;
  for (let m = OPEN.exec(text); m; m = OPEN.exec(text)) {
    const end = endOf(text, m.index + m[0].length);
    if (end < 0 || inCode(text, m.index)) continue;
    const el = parse(text.slice(m.index, end)).find((e) => e.type === "message");
    if (!el) continue;
    forwards.push(fwdOf(el));
    words += text.slice(at, m.index);
    at = end;
    OPEN.lastIndex = end;
  }
  if (!forwards.length) return { words: text, forwards };
  return { words: (words + text.slice(at)).replace(/\n{3,}/g, "\n\n").trim(), forwards };
}

/** How many messages a forward holds, those inside its messages' forwards not counted. */
export const forwardCount = (f: Fwd) => f.msgs.length;

/** A forward as plain lines (copied, quoted): who said each, its words; one inside set in. */
export function forwardPlain(f: Fwd, indent = ""): string {
  const out: string[] = [];
  for (const m of f.msgs) {
    const lines = m.text ? m.text.split("\n") : [];
    out.push(`${indent}${m.who || "?"}: ${lines[0] ?? ""}`.trimEnd());
    for (const l of lines.slice(1)) out.push(`${indent}  ${l}`);
    for (const g of m.forwards) out.push(forwardPlain(g, `${indent}  `));
  }
  return out.join("\n");
}

/** Words and the forwards taken out of them, as plain text (copied). */
export const withForwards = (text: string, fs: readonly Fwd[] = []) => [text, ...fs.map((f) => forwardPlain(f))].filter(Boolean).join("\n\n");

const split = new WeakMap<object, { words: string; forwards: Fwd[] }>();
/** A message's words and the forwards in them (splitForwards), kept per message: drawn often. */
export function msgWords(m: { text: string }): { words: string; forwards: Fwd[] } {
  let w = split.get(m);
  if (!w) split.set(m, (w = splitForwards(m.text)));
  return w;
}
