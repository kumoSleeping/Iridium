// The pictures of a conversation's messages, in their order, for the viewer to step through
// (gallery.tsx): from the messages (the log), not from what is drawn, as rows far back are drawn
// late (lazyrows.tsx). What a tool's result gave the model, a picture not yet sent and the
// composer's are not among them: each opens alone.
import type { FileRef } from "../api/types.gen";

/** A picture to step to: full, the file itself (the key: as its Zoom opens it); under, what shows
 *  till it has come (its thumbnail); w, h: its size, as the server read it. */
export type Shot = { full: string; under: string; name: string; w?: number; h?: number };

/** A message as far as its pictures go: its words as its Markdown shows them (Satori's elements
 *  turned), its files, and the session its files are served by. person: shown as a person's
 *  (every file under the bubble), else as an agent's (a file link where the words put it, the
 *  rest under them). */
export type Said = { words: string; files: FileRef[]; session: string; person: boolean };

/** The file links the words put in place as pictures (![…](ir:id)), by id, in order; code left out. */
export function placed(words: string): string[] {
  const out: string[] = [];
  for (const [i, part] of words.split(/(```[\s\S]*?(?:```|$)|`[^`\n]*`)/).entries()) {
    if (i % 2) continue;
    for (const m of part.matchAll(/!\[[^\]\n]*\]\(<?ir:([a-z2-7]{10})>?\)/g)) out.push(m[1]!);
  }
  return out;
}

/** A message's pictures in the order it shows them. */
export function picturesOf(m: Said): FileRef[] {
  const images = m.files.filter((f) => f.kind === "image");
  if (!images.length) return [];
  if (m.person) return images;
  const linked = new Map(images.filter((f) => f.path.startsWith("ir:")).map((f) => [f.ref, f]));
  const inPlace = placed(m.words).flatMap((id) => (linked.has(id) ? [linked.get(id)!] : []));
  return [...inPlace, ...images.filter((f) => !f.path.startsWith("ir:"))];
}

/** The pictures of the messages, each once (one shown twice is stepped to where it first is).
 *  url: the file served inline; under: what shows till it has come. */
export function shotsOf(msgs: Said[], url: (session: string, f: FileRef) => string, under: (session: string, f: FileRef, full: string) => string): Shot[] {
  const seen = new Set<string>();
  const out: Shot[] = [];
  for (const m of msgs)
    for (const f of picturesOf(m)) {
      const full = url(m.session, f);
      if (seen.has(full)) continue;
      seen.add(full);
      out.push({ full, under: under(m.session, f, full), name: f.name, ...(f.w && f.h ? { w: f.w, h: f.h } : {}) });
    }
  return out;
}
