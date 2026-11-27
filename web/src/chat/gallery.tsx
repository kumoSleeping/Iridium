// A conversation's pictures in one viewer (otae-c, 2026-11-27, as the Codex app's): a picture of
// its messages pressed opens there, and from it the one before or after, by the keys at its sides,
// ← and →, or a swipe at fit; its place among them at the foot. The list is the messages' (gallery-
// lib.ts), as they are now. A picture not among them (a tool's result, one not yet sent, the
// composer's) opens alone, the same window without the keys.
import { Dialog } from "@base-ui/react/dialog";
import { ChevronLeft, ChevronRight, Download, X } from "lucide-react";
import { createContext, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { t } from "../i18n";
import { DialogPortal, Tip } from "../ui";
import type { Shot } from "./gallery-lib";
import { Viewer } from "./zoom";

/** open: shows full among the conversation's pictures; false, it is not one of them. */
type Opener = { open: (full: string) => boolean };
const GalleryOf = createContext<Opener | null>(null);

/** The conversation's viewer, if a picture is drawn inside one (files.tsx Zoom). */
export const useGallery = () => useContext(GalleryOf);

/** Space shuts a preview, as Quick Look does on a Mac (kumo, 2026-11-23); not where one types. */
export const spaceShuts = (close: () => void) => (e: React.KeyboardEvent) => {
  if (e.key !== " " || e.repeat || e.metaKey || e.ctrlKey || e.altKey) return;
  const t = e.target as HTMLElement;
  if (t.closest("input,textarea,select,[contenteditable=''],[contenteditable=true]")) return;
  e.preventDefault();
  close();
};

const key = "glass g-press grid size-8 place-items-center rounded-full text-ink-2 hover:text-ink";
const side = "glass g-press absolute top-1/2 z-10 grid size-10 -translate-y-1/2 place-items-center rounded-full text-ink-2 hover:text-ink";

/** A picture over the page (zoom.tsx Viewer), its download key and close key at the top right;
 *  among others (at, of, step) the keys to the one before and after at its sides, a first or last
 *  without its key, and its place at the foot. Shut by a press beside it, Esc, Space or its key. */
function PictureWindow({ setOpen, shot, at, of, step }: { setOpen: (o: boolean) => void; shot: Shot; at?: number; of?: number; step?: (d: -1 | 1) => void }) {
  const many = step && at !== undefined && of !== undefined && of > 1;
  // ← and → on the window, as they go down (the popup stops them on their way up: Base UI's), so
  // with the focus gone with a first or last picture's key they still step.
  useEffect(() => {
    if (!many) return;
    const keys = (e: KeyboardEvent) => {
      if ((e.key !== "ArrowLeft" && e.key !== "ArrowRight") || e.metaKey || e.ctrlKey || e.altKey || e.shiftKey) return;
      e.preventDefault();
      step(e.key === "ArrowLeft" ? -1 : 1);
    };
    window.addEventListener("keydown", keys, true);
    return () => window.removeEventListener("keydown", keys, true);
  }, [many, step]);
  return (
    <DialogPortal>
      <Dialog.Backdrop className="fixed inset-0 z-50 bg-paper/70 backdrop-blur-sm transition-opacity duration-(--dur-2) data-ending-style:opacity-0 data-starting-style:opacity-0" />
      <Dialog.Popup onKeyDown={spaceShuts(() => setOpen(false))} className="fixed inset-0 z-50 outline-none transition-[opacity,scale] duration-(--dur-2) ease-(--ease-out) data-ending-style:scale-[0.98] data-ending-style:opacity-0 data-starting-style:scale-[0.98] data-starting-style:opacity-0">
        <Dialog.Title className="sr-only">{shot.name}</Dialog.Title>
        {/* Drawn anew for each picture: its scale, place and fit its own. */}
        <Viewer
          key={shot.full}
          src={shot.full}
          name={shot.name}
          under={shot.under !== shot.full ? shot.under : undefined}
          size={shot.w && shot.h ? { w: shot.w, h: shot.h } : undefined}
          close={() => setOpen(false)}
          step={many ? step : undefined}
          foot={many ? <span className="glass pointer-events-none absolute bottom-4 left-1/2 grid h-8 -translate-x-1/2 place-items-center rounded-full px-3 text-sm text-ink-2 tabular-nums">{`${at + 1} / ${of}`}</span> : undefined}
          corner={
            <>
              <a href={shot.full} download={shot.name} aria-label={t("msg.download")} className={key}>
                <Download size={15} />
              </a>
              <Dialog.Close aria-label={t("msg.close")} className={key}>
                <X size={15} />
              </Dialog.Close>
            </>
          }
        />
        {many && at > 0 && (
          <Tip label={t("msg.prevImage")}>
            <button type="button" aria-label={t("msg.prevImage")} onClick={() => step(-1)} className={`${side} left-4`}>
              <ChevronLeft size={18} />
            </button>
          </Tip>
        )}
        {many && at < of - 1 && (
          <Tip label={t("msg.nextImage")}>
            <button type="button" aria-label={t("msg.nextImage")} onClick={() => step(1)} className={`${side} right-4`}>
              <ChevronRight size={18} />
            </button>
          </Tip>
        )}
      </Dialog.Popup>
    </DialogPortal>
  );
}

/** One picture alone (files.tsx Zoom, not among a conversation's). */
export function LonePicture({ open, setOpen, full, name, children }: { open: boolean; setOpen: (o: boolean) => void; full: string; name: string; children?: ReactNode }) {
  return (
    <Dialog.Root open={open} onOpenChange={setOpen}>
      {children}
      <PictureWindow setOpen={setOpen} shot={{ full, under: full, name }} />
    </Dialog.Root>
  );
}

/** The viewer of a conversation's pictures, for those drawn inside: shots, its pictures as they are
 *  now (asked as one opens and as it is drawn again, so one come meanwhile is among them). */
export function Gallery({ shots, children }: { shots: () => Shot[]; children: ReactNode }) {
  const list = useRef(shots);
  list.current = shots;
  const [open, setOpen] = useState(false);
  const [full, setFull] = useState("");
  // The one shown last: kept as it shuts, and if its message is gone meanwhile.
  const last = useRef<Shot | null>(null);
  // Stable, so the rows that use it are not drawn again with the page.
  const opener = useMemo<Opener>(
    () => ({
      open: (f) => {
        const all = list.current();
        const at = all.findIndex((s) => s.full === f);
        if (at < 0) return false;
        // The one after it, ahead, for the first step.
        const next = all[at + 1];
        if (next) new Image().src = next.full;
        setFull(f);
        setOpen(true);
        return true;
      },
    }),
    [],
  );
  // Asked only while it is open; shutting, the one it showed, alone.
  const all = open ? shots() : [];
  const at = all.findIndex((s) => s.full === full);
  if (at >= 0) last.current = all[at]!;
  const shot = at >= 0 ? all[at]! : last.current;
  const step = (d: -1 | 1) => {
    const to = all[at + d];
    if (at < 0 || !to) return;
    setFull(to.full);
    // The one after it the same way, ahead, so a step on is there at once.
    const ahead = all[at + 2 * d];
    if (ahead) new Image().src = ahead.full;
  };
  return (
    <GalleryOf.Provider value={opener}>
      {children}
      <Dialog.Root open={open} onOpenChange={setOpen}>
        {shot && <PictureWindow setOpen={setOpen} shot={shot} at={at >= 0 ? at : undefined} of={all.length} step={at >= 0 ? step : undefined} />}
      </Dialog.Root>
    </GalleryOf.Provider>
  );
}
