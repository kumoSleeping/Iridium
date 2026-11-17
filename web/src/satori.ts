// Satori messages as the page reads them (docs/satori.md "记录"): message.list gives a session's
// transcript a message per block, its content standard elements and Iridium's own (iridium:…);
// blockOf reads the block back as the server has it (internal/satori/blocks.go, whose table
// internal/satori/testdata/blocks.json is tested on both sides).
import type { Block, BlockKind, Compaction, FileKind, FileRef, Image, Result, SystemEvent } from "./api/types.gen";

/** A Satori message, as far as the page reads it. */
export type SatoriMessage = { id: string; content: string; created_at?: number };

/** A parsed element: text (type "text", its words in text) or a tag. */
export type El = { type: string; attrs: Record<string, string | true>; children: El[]; text?: string };

const unescape = (s: string) =>
  s.includes("&")
    ? s.replace(/&(amp|lt|gt|quot|apos|#\d+|#[xX][0-9a-fA-F]+);/g, (all, n: string) => {
        switch (n) {
          case "amp":
            return "&";
          case "lt":
            return "<";
          case "gt":
            return ">";
          case "quot":
            return '"';
          case "apos":
            return "'";
        }
        const c = n[1] === "x" || n[1] === "X" ? parseInt(n.slice(2), 16) : parseInt(n.slice(1), 10);
        return c <= 0x10ffff ? String.fromCodePoint(c) : all;
      })
    : s;

const TAG = /<(\/?)([A-Za-z][\w:.-]*)((?:\s+[^\s=/>]+(?:="[^"]*")?)*)\s*(\/?)>/g;
const ATTR = /([^\s=/>]+)(?:="([^"]*)")?/g;

/** Parses content into elements, its text as it is (no space trimmed): what blocks.go writes. */
export function parse(content: string): El[] {
  const root: El = { type: "", attrs: {}, children: [] };
  const stack = [root];
  let last = 0;
  const text = (s: string) => s && stack.at(-1)!.children.push({ type: "text", attrs: {}, children: [], text: unescape(s) });
  for (const m of content.matchAll(TAG)) {
    text(content.slice(last, m.index));
    last = m.index + m[0].length;
    const [, close, type, rawAttrs, self] = m;
    if (close) {
      const i = stack.findLastIndex((e) => e.type === type);
      if (i > 0) stack.length = i;
      continue;
    }
    const attrs: El["attrs"] = {};
    for (const a of rawAttrs.matchAll(ATTR)) attrs[a[1]] = a[2] === undefined ? true : unescape(a[2]);
    const el: El = { type, attrs, children: [] };
    stack.at(-1)!.children.push(el);
    if (!self) stack.push(el);
  }
  text(content.slice(last));
  return root.children;
}

const textOf = (els: El[]) =>
  els
    .filter((e) => e.type === "text")
    .map((e) => e.text)
    .join("");
const str = (v: string | true | undefined) => (typeof v === "string" ? v : undefined);
const int = (v: string | true | undefined) => (typeof v === "string" && v !== "" ? Number(v) : undefined);
/** Sets o[k] to v unless v is nothing (undefined, "", 0, false): the server leaves those out. */
function put<T extends object, K extends keyof T>(o: T, k: K, v: T[K] | undefined) {
  if (v !== undefined && v !== "" && v !== 0 && v !== false) o[k] = v;
}

const images = (els: El[]): Image[] =>
  els
    .filter((e) => e.type === "iridium:image")
    .map((e) => {
      const im: Image = {};
      put(im, "mime", str(e.attrs.mime));
      put(im, "w", int(e.attrs.width));
      put(im, "h", int(e.attrs.height));
      put(im, "unsent", e.attrs.unsent === true);
      return im;
    });

const FILES: Record<string, FileKind> = { img: "image", video: "video", audio: "audio", file: "file" };

/** A file element (<img>/<video>/<audio>/<file> by an internal: URL) as the file it names. */
function fileOf(e: El): FileRef | undefined {
  const src = str(e.attrs.src);
  if (!(e.type in FILES) || !src?.startsWith("internal:")) return;
  const f: FileRef = {
    ref: src.slice(src.lastIndexOf("/") + 1),
    name: str(e.attrs.title) ?? "",
    path: str(e.attrs.path) ?? "",
    size: int(e.attrs.size) ?? 0,
    mime: str(e.attrs.mime) ?? "",
    kind: (str(e.attrs.kind) as FileKind) ?? FILES[e.type],
    mtime: int(e.attrs.mtime) ?? 0,
  };
  put(f, "thumb", e.attrs.thumb === true);
  put(f, "w", int(e.attrs.width));
  put(f, "h", int(e.attrs.height));
  put(f, "sha256", str(e.attrs.sha256));
  return f;
}

const KINDS = new Set(["user", "assistant", "thinking", "tool", "result", "system"]);

/** A message of a relay's forward: its number ("3-5" a turn told in a line), who said it, its
 *  words. */
export type RelayMsg = { id: string; who: string; user: string; text: string; turn?: boolean };
/** A channel's message as an agent of it was given it (internal/satori/relay.go): which channel,
 *  the number that called it, the standing words where they were given (full), and the
 *  messages forwarded, the one that called it last. */
export type Relay = { channel: string; name: string; seq: number; full: boolean; context: string; msgs: RelayMsg[] };
/** The relay a user block is, by the block blockOf made. */
export const relays = new WeakMap<Block, Relay>();

function relayOf(els: El[]): Relay | undefined {
  const r = els.find((e) => e.type === "iridium:relay");
  const fwd = els.find((e) => e.type === "message" && e.attrs.forward !== undefined);
  if (!r || !fwd) return undefined;
  const msgs = fwd.children
    .filter((e) => e.type === "message")
    .map((m) => {
      const au = m.children.find((e) => e.type === "author");
      return { id: str(m.attrs.id) ?? "", who: str(au?.attrs.name) ?? "", user: str(au?.attrs.id) ?? "", text: textOf(m.children), ...(m.children.some((e) => e.type === "iridium:turn") ? { turn: true } : {}) };
    });
  return { channel: str(r.attrs.channel) ?? "", name: str(r.attrs.name) ?? "", seq: int(r.attrs.seq) ?? 0, full: r.attrs.full === true, context: textOf(r.children), msgs };
}

/** Message m of a session's transcript (message.list) as its block. */
export function blockOf(m: SatoriMessage): Block {
  const els = parse(m.content);
  const mark = els.find((e) => e.type.startsWith("iridium:") && KINDS.has(e.type.slice(8)));
  const kind = (mark?.type.slice(8) ?? "assistant") as BlockKind;
  const b: Block = { at: m.id, kind };
  put(b, "time", m.created_at);
  const words = kind === "user" || kind === "assistant" ? textOf(els) : textOf(mark?.children ?? []);
  put(b, "text", words);
  const a = mark?.attrs ?? {};
  put(b, "length", int(a.length));
  put(b, "from", str(a.from));
  put(b, "fromTerminal", str(a.terminal));
  put(b, "fromLogin", str(a.login));
  put(b, "fromDevice", str(a.device) as Block["fromDevice"]);
  put(b, "id", str(a.id));
  put(b, "name", str(a.name));
  const kids = mark?.children ?? [];
  const input = kids.find((e) => e.type === "iridium:input");
  if (input) put(b, "input", textOf(input.children));
  const res = kids.find((e) => e.type === "iridium:result");
  if (res) {
    const r: Result = { at: str(res.attrs.at) ?? "", text: textOf(res.children) };
    put(r, "time", int(res.attrs.time));
    put(r, "length", int(res.attrs.length));
    put(r, "error", res.attrs.error === true);
    const ims = images(res.children);
    if (ims.length) r.images = ims;
    b.result = r;
  }
  put(b, "error", a.error === true);
  const ims = images(kids);
  if (ims.length) b.images = ims;
  put(b, "event", str(a.event) as SystemEvent | undefined);
  const cp = kids.find((e) => e.type === "iridium:compact");
  if (cp) {
    const c: Compaction = {};
    put(c, "auto", cp.attrs.auto === true);
    put(c, "before", int(cp.attrs.before));
    put(c, "after", int(cp.attrs.after));
    put(c, "ms", int(cp.attrs.ms));
    b.compact = c;
  }
  const files = els.map(fileOf).filter((f): f is FileRef => !!f);
  if (files.length) b.files = files;
  if (kind === "user") {
    const r = relayOf(els);
    if (r) {
      relays.set(b, r);
      // Its words, as other views read a message: those that called it.
      put(b, "text", r.msgs.at(-1)?.text);
    }
  }
  return b;
}
