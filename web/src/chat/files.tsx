// The files a message gives (plan "网页": [文件] `path` lines, attachments): a picture as a thumbnail (about 1024px) that opens the
// original, a video or a sound to play in place, anything else a card to download. Only paths the
// server says the conversation named, and that exist, show; it serves them by the session.
import { Dialog } from "@base-ui/react/dialog";
import { Download, FileCode2, FileText, FileX2, X } from "lucide-react";
import { Shareable } from "../share";
import { createContext, useCallback, useContext, useState, type CSSProperties, type ReactElement, type ReactNode } from "react";
import { api } from "../api/client";
import type { FileRef } from "../api/types.gen";
import { t } from "../i18n";
import { cn, IconKey, Tip } from "../ui";

export const size = (n: number) => (n < 1024 ? `${n} B` : n < 1 << 20 ? `${(n / 1024).toFixed(1)} KB` : n < 1 << 30 ? `${(n / (1 << 20)).toFixed(1)} MB` : `${(n / (1 << 30)).toFixed(2)} GB`);

/** The files a message names, as the server found them (Block.files): only those that exist. */
export function Files({ session, files }: { session: string; files: FileRef[] }) {
  if (!files.length) return null;
  return (
    <div className="flex w-full flex-col items-start gap-2">
      {files.map((f) => (
        <One key={f.ref} session={session} f={f} />
      ))}
    </div>
  );
}

export const CODE = /\.(tsx?|jsx?|go|rs|py|css|json|ya?ml|toml|sh|md)$/i;

function One({ session, f }: { session: string; f: FileRef }) {
  return (
    <Shareable thing={{ what: "file", session, ref: f.ref }} copy={f.path || undefined}>
      <Bare session={session} f={f} />
    </Shareable>
  );
}

function Bare({ session, f }: { session: string; f: FileRef }) {
  const url = api.fileURL(session, f.ref, false, f.mtime);
  const name = f.name;
  if (f.kind === "image") return <Picture src={f.thumb ? api.thumbURL(session, f.ref, 1024, f.mtime) : url} full={url} name={name} w={f.w} h={f.h} />;
  if (f.kind === "video") return <video controls preload="metadata" src={url} className="block w-full max-w-[480px] rounded-lg bg-fill shadow-[0_0_0_0.5px_var(--edge)]" aria-label={name} />;
  if (f.kind === "audio") return <audio controls preload="metadata" src={url} className="w-full max-w-[340px]" aria-label={name} />;
  return <FileCard name={name} detail={size(f.size)} href={api.fileURL(session, f.ref, true, f.mtime)} code={CODE.test(f.name)} />;
}

/** The pictures sent from this page, by the path they were uploaded to: what the copy on this device
 *  shows (a blob URL) until the server's thumbnail has come, so a sent picture does not blink out. */
export const previews = new Map<string, string>();

/** An image that fades in once it has loaded (at once if it already has), over under if given.
 *  style: its aspect ratio, when known, so its place is kept before it loads. */
function Shown({ src, under, alt, className, style }: { src: string; under?: string; alt: string; className: string; style?: CSSProperties }) {
  const [ready, setReady] = useState(false);
  const done = useCallback((el: HTMLImageElement | null) => void (el?.complete && el.naturalWidth && setReady(true)), []);
  return (
    <>
      {under && <img src={under} alt="" aria-hidden className={cn(className, "absolute inset-0 size-full")} />}
      <img ref={done} src={src} alt={alt} loading="lazy" onLoad={() => setReady(true)} style={style} className={cn(className, "relative transition-opacity duration-(--dur-2) ease-(--ease-out)", ready ? "opacity-100" : "opacity-0")} />
    </>
  );
}

/** A picture: its thumbnail, opening the original over the page; small, in a row of files, as high
 *  as the composer's and its own shape, as it was there. under: what shows till it has come.
 *  w, h: its size as the server read it: its place is kept as it loads, the conversation does not
 *  move (a chat opened the first time, its pictures not yet in the cache). */
export function Picture({ src, full, name, small, under, w, h }: { src: string; full: string; name: string; small?: boolean; under?: string; w?: number; h?: number }) {
  return (
    <Zoom full={full} name={name}>
      <button type="button" aria-label={`${name} · ${t("msg.openImage")}`} className={cn("g-press relative block overflow-hidden rounded-lg bg-fill shadow-[0_0_0_0.5px_var(--edge)]", small ? "h-14" : "w-full max-w-[340px]")}>
        <Shown src={src} under={under} alt={name} style={w && h ? { aspectRatio: `${w} / ${h}` } : undefined} className={cn("block object-cover", small ? "h-full w-auto max-w-40 min-w-10" : "max-h-[420px] w-full")} />
      </button>
    </Zoom>
  );
}

/** children (a button) opens full over the page, to close by a click or download. */
export function Zoom({ full, name, children }: { full: string; name: string; children: ReactElement }) {
  return (
    <Dialog.Root>
      <Tip label={t("msg.openImage")}>
        <Dialog.Trigger render={children} />
      </Tip>
      <Dialog.Portal>
        <Dialog.Backdrop className="fixed inset-0 z-50 bg-paper/70 backdrop-blur-sm transition-opacity duration-(--dur-2) data-ending-style:opacity-0 data-starting-style:opacity-0" />
        <Dialog.Popup className="g-unfold fixed top-1/2 left-1/2 z-50 max-h-[92vh] w-max max-w-[min(1600px,94vw)] -translate-x-1/2 -translate-y-1/2 overflow-hidden rounded-xl shadow-(--shadow-float) outline-none">
          <Dialog.Title className="sr-only">{name}</Dialog.Title>
          <Dialog.Close className="block" aria-label={t("msg.close")}>
            <img src={full} alt={name} className="block max-h-[92vh] max-w-full object-contain" />
          </Dialog.Close>
          <a href={full} download={name} aria-label={t("msg.download")} className="glass g-press absolute top-2 right-11 grid size-8 place-items-center rounded-full text-ink-2 hover:text-ink">
            <Download size={15} />
          </a>
          <Dialog.Close aria-label={t("msg.close")} className="glass g-press absolute top-2 right-2 grid size-8 place-items-center rounded-full text-ink-2 hover:text-ink">
            <X size={15} />
          </Dialog.Close>
        </Dialog.Popup>
      </Dialog.Portal>
    </Dialog.Root>
  );
}

/** A file sent with your words, as its card was in the composer (as high as a session's beside it,
 *  mention.tsx MentionCard): its icon, its name, its size; pressed, it downloads. No href: not yet
 *  landed. */
export function SentFile({ name, bytes, href }: { name: string; bytes?: number; href?: string }) {
  const Icon = CODE.test(name) ? FileCode2 : FileText;
  const body = (
    <>
      <Icon size={16} className="shrink-0 text-ink-3" />
      <span className="min-w-0">
        <span className="block truncate text-sm text-ink">{name}</span>
        {bytes !== undefined && <span className="block truncate text-xs text-ink-3 tabular-nums">{size(bytes)}</span>}
      </span>
    </>
  );
  const box = "flex h-11 w-44 max-w-full items-center gap-2 rounded-lg bg-paper/70 px-2.5 text-left shadow-[0_0_0_0.5px_var(--edge)]";
  if (!href) return <span className={box}>{body}</span>;
  return (
    <Tip label={t("msg.download")}>
      <a href={href} download={name} className={cn("g-press hover:bg-paper", box)}>
        {body}
      </a>
    </Tip>
  );
}

/** A file to download: its name, its size, the download key. Spans, as it may stand in a paragraph
 *  (a reply's file link). */
export function FileCard({ name, detail, href, code, onRemove, progress }: { name: string; detail: string; href?: string; code?: boolean; onRemove?: () => void; progress?: number }) {
  const Icon = code ? FileCode2 : FileText;
  return (
    <span className="relative my-1 flex w-full max-w-[340px] items-center gap-3 overflow-hidden rounded-lg py-2 pr-1.5 pl-3 shadow-[0_0_0_0.5px_var(--edge),0_0_0_1px_var(--edge)]">
      <Icon size={20} className="shrink-0 text-ink-3" />
      <span className="block min-w-0 flex-1">
        <span className="block truncate text-sm">{name}</span>
        <span className="block text-xs text-ink-3 tabular-nums">{detail}</span>
      </span>
      {href && (
        <Tip label={t("msg.download")}>
          <a href={href} download={name} aria-label={t("msg.download")} className="g-press grid size-8 shrink-0 place-items-center rounded-md text-ink-2 hover:bg-fill hover:text-ink pointer-coarse:size-10">
            <Download size={16} />
          </a>
        </Tip>
      )}
      {onRemove && (
        <IconKey label={t("composer.remove")} onClick={onRemove}>
          <X size={15} />
        </IconKey>
      )}
      {progress !== undefined && progress < 1 && <span className={cn("absolute bottom-0 left-0 h-[2px] origin-left bg-ink-3 transition-transform")} style={{ width: "100%", transform: `scaleX(${progress})` }} />}
    </span>
  );
}

/** The files a reply's links may name (docs/api.md "文件链接"), by id: its Block.files with a path
 *  ir:<id>. None while the reply is still being written (a draft). */
const Linkable = createContext<{ session: string; files: Map<string, FileRef> } | null>(null);

/** Gives the Markdown inside the files its links name. */
export function LinkFiles({ session, files, children }: { session: string; files: FileRef[]; children: ReactNode }) {
  const links = new Map(files.filter((f) => isLink(f)).map((f) => [f.ref, f]));
  return <Linkable.Provider value={{ session, files: links }}>{children}</Linkable.Provider>;
}

/** f is a file link's (ir:<id>), shown where the reply puts it, not under it. */
export const isLink = (f: FileRef) => f.path.startsWith("ir:");

/** A file link where the reply puts it: ![caption](ir:id) a picture, a video or a sound in place (any
 *  other file a card); [text](ir:id) a file to download, in the line. Unknown: struck, its words
 *  kept; in a draft (no files yet) its words only. */
export function Linked({ id, label, inline }: { id: string; label: string; inline: boolean }) {
  const it = useContext(Linkable);
  const f = it?.files.get(id);
  if (!it) return <span className="text-ink-3">{label}</span>;
  if (!f)
    return (
      <Tip label={t("msg.fileGone")}>
        <span className="inline-flex items-baseline gap-1 text-ink-3 line-through decoration-ink-4">
          <FileX2 size={13} className="shrink-0 self-center" />
          {label || id}
        </span>
      </Tip>
    );
  if (inline) {
    const href = api.fileURL(it.session, f.ref, true, f.mtime);
    return (
      <Tip label={`${f.name} · ${size(f.size)}`}>
        <a href={href} download={f.name} className="inline-flex items-baseline gap-1 rounded-sm text-ink underline decoration-ink-4 underline-offset-2 hover:decoration-ink-2">
          <Download size={13} className="shrink-0 self-center text-ink-3" />
          {label || f.name}
        </a>
      </Tip>
    );
  }
  return (
    <span className="my-1 block">
      <One session={it.session} f={f} />
    </span>
  );
}
