// The files a reply names (plan "网页"): a picture as a thumbnail (about 1024px) that opens the
// original, a video or a sound to play in place, anything else a card to download. Only paths the
// server says the conversation named, and that exist, show; it serves them by the session.
import { Dialog } from "@base-ui/react/dialog";
import { useQuery } from "@tanstack/react-query";
import { Download, FileCode2, FileText, X } from "lucide-react";
import type { FileInfo } from "../api/assumed";
import { api, missing } from "../api/client";
import { t } from "../i18n";
import { cn, IconKey, Tip } from "../ui";
import { AUDIO, VIDEO, type FileRef } from "./refs";

export const size = (n: number) => (n < 1024 ? `${n} B` : n < 1 << 20 ? `${(n / 1024).toFixed(1)} KB` : n < 1 << 30 ? `${(n / (1 << 20)).toFixed(1)} MB` : `${(n / (1 << 30)).toFixed(2)} GB`);

export function Files({ session, refs }: { session: string; refs: FileRef[] }) {
  const paths = refs.map((r) => r.path);
  const q = useQuery({
    queryKey: ["files", session, paths.join("\n")],
    enabled: paths.length > 0,
    staleTime: Infinity,
    queryFn: () => api.files(session, paths).catch((e) => (missing(e) ? ([] as FileInfo[]) : Promise.reject(e))),
  });
  const found = (q.data ?? []).filter((f) => paths.includes(f.path));
  if (!found.length) return null;
  return (
    <div className="flex w-full flex-col items-start gap-2">
      {found.map((f) => (
        <One key={f.path} session={session} f={f} label={refs.find((r) => r.path === f.path)?.label} />
      ))}
    </div>
  );
}

function One({ session, f, label }: { session: string; f: FileInfo; label?: string }) {
  const url = api.fileURL(session, f.path);
  if (f.image || f.type.startsWith("image/")) return <Picture src={api.thumbURL(session, f.path)} full={url} name={label ?? f.name} />;
  if (VIDEO.test(f.path) || f.type.startsWith("video/")) return <video controls preload="metadata" src={url} className="block w-full max-w-[480px] rounded-lg bg-fill shadow-[0_0_0_0.5px_var(--edge)]" aria-label={f.name} />;
  if (AUDIO.test(f.path) || f.type.startsWith("audio/")) return <audio controls preload="metadata" src={url} className="w-full max-w-[340px]" aria-label={f.name} />;
  return <FileCard name={label ?? f.name} detail={size(f.size)} href={api.fileURL(session, f.path, true)} code={/\.(tsx?|jsx?|go|rs|py|css|json|ya?ml|toml|sh|md)$/i.test(f.name)} />;
}

/** A picture: its thumbnail, opening the original over the page. */
export function Picture({ src, full, name }: { src: string; full: string; name: string }) {
  return (
    <Dialog.Root>
      <Tip label={t("msg.openImage")}>
        <Dialog.Trigger
          render={
            <button type="button" aria-label={`${name} · ${t("msg.openImage")}`} className="g-press block w-full max-w-[340px] overflow-hidden rounded-lg bg-fill shadow-[0_0_0_0.5px_var(--edge)]">
              <img src={src} alt={name} loading="lazy" className="block max-h-[420px] w-full object-cover" />
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
