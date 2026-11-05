// The files a reply names (plan "网页"): a picture as a thumbnail (about 1024px) that opens the
// original, a video or a sound to play in place, anything else a card to download. Only paths the
// server says the conversation named, and that exist, show; it serves them by the session.
import { Dialog } from "@base-ui/react/dialog";
import { Download, FileCode2, FileText, X } from "lucide-react";
import { api } from "../api/client";
import type { FileRef } from "../api/types.gen";
import { t } from "../i18n";
import { cn, IconKey, Tip } from "../ui";

export const size = (n: number) => (n < 1024 ? `${n} B` : n < 1 << 20 ? `${(n / 1024).toFixed(1)} KB` : n < 1 << 30 ? `${(n / (1 << 20)).toFixed(1)} MB` : `${(n / (1 << 30)).toFixed(2)} GB`);

/** The files a message names, as the server found them (Block.files): only those that exist. */
export function Files({ session, files, labels }: { session: string; files: FileRef[]; labels?: Map<string, string> }) {
  if (!files.length) return null;
  return (
    <div className="flex w-full flex-col items-start gap-2">
      {files.map((f) => (
        <One key={f.ref} session={session} f={f} label={labels?.get(f.path)} />
      ))}
    </div>
  );
}

const CODE = /\.(tsx?|jsx?|go|rs|py|css|json|ya?ml|toml|sh|md)$/i;

function One({ session, f, label }: { session: string; f: FileRef; label?: string }) {
  const url = api.fileURL(session, f.ref);
  const name = label ?? f.name;
  if (f.kind === "image") return <Picture src={f.thumb ? api.thumbURL(session, f.ref) : url} full={url} name={name} />;
  if (f.kind === "video") return <video controls preload="metadata" src={url} className="block w-full max-w-[480px] rounded-lg bg-fill shadow-[0_0_0_0.5px_var(--edge)]" aria-label={name} />;
  if (f.kind === "audio") return <audio controls preload="metadata" src={url} className="w-full max-w-[340px]" aria-label={name} />;
  return <FileCard name={name} detail={size(f.size)} href={api.fileURL(session, f.ref, true)} code={CODE.test(f.name)} />;
}

/** A picture: its thumbnail, opening the original over the page. */
export function Picture({ src, full, name, small }: { src: string; full: string; name: string; small?: boolean }) {
  return (
    <Dialog.Root>
      <Tip label={t("msg.openImage")}>
        <Dialog.Trigger
          render={
            <button type="button" aria-label={`${name} · ${t("msg.openImage")}`} className={cn("g-press block overflow-hidden rounded-lg bg-fill shadow-[0_0_0_0.5px_var(--edge)]", small ? "size-16" : "w-full max-w-[340px]")}>
              <img src={src} alt={name} loading="lazy" className={cn("block w-full object-cover", small ? "h-full" : "max-h-[420px]")} />
            </button>
          }
        />
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

/** A file to download: its name, its size, the download key. */
export function FileCard({ name, detail, href, code, onRemove, progress }: { name: string; detail: string; href?: string; code?: boolean; onRemove?: () => void; progress?: number }) {
  const Icon = code ? FileCode2 : FileText;
  return (
    <div className="relative flex w-full max-w-[340px] items-center gap-3 overflow-hidden rounded-lg py-2 pr-1.5 pl-3 shadow-[0_0_0_0.5px_var(--edge),0_0_0_1px_var(--edge)]">
      <Icon size={20} className="shrink-0 text-ink-3" />
      <div className="min-w-0 flex-1">
        <div className="truncate text-sm">{name}</div>
        <div className="text-xs text-ink-3 tabular-nums">{detail}</div>
      </div>
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
    </div>
  );
}
