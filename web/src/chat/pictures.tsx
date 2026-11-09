// The pictures a tool's result gave the model (Result.images: a Read of a picture, a screenshot),
// under the step's line: each the one the model was given, read from the agent's record, never
// the file as it is now; pressed, it opens whole over the page. Its place is kept by its size as
// the record tells it; one the server cannot give is a faint box with why.
import { ImageOff } from "lucide-react";
import { useState } from "react";
import { api } from "../api/client";
import type { Image as Picture } from "../api/types.gen";
import { t } from "../i18n";
import { cn } from "../ui";
import { Zoom } from "./files";

/** The pictures of the result at, a step's whose name (for a download) is name. */
export function Pictures({ session, at, images, name }: { session: string; at: string; images: Picture[]; name: string }) {
  return (
    <div className="mt-0.5 mb-1.5 ml-[7px] flex flex-col items-start gap-2 pl-3 shadow-[inset_1px_0_0_var(--edge)]">
      {images.map((p, n) => (
        <One key={n} url={api.imageURL(session, at, n)} p={p} name={images.length > 1 ? `${n + 1}-${name}` : name} />
      ))}
    </div>
  );
}

/** What became of a picture asked for: coming, here, or not given (offline: its machine is not
 *  connected; the server says 409). */
type Fate = "coming" | "here" | "unreadable" | "offline";

/** The most a picture takes under its line, in CSS pixels. */
const MAX_W = 420;
const MAX_H = 300;

function One({ url, p, name }: { url: string; p: Picture; name: string }) {
  const [fate, setFate] = useState<Fate>("coming");
  const [tries, setTries] = useState(0);
  // Not given: ask once more for why, as an <img> is not told.
  const lost = () => void fetch(url, { cache: "no-store" }).then((r) => setFate(r.status === 409 ? "offline" : "unreadable"), () => setFate("offline"));
  // Its size on the page, where the record tells: at most MAX_W × MAX_H, and the column's width.
  const k = p.w && p.h ? Math.min(1, MAX_W / p.w, MAX_H / p.h) : 0;
  const sized = k ? { width: Math.round(p.w! * k), aspectRatio: `${p.w} / ${p.h}` } : undefined;
  if (fate === "unreadable" || fate === "offline")
    return (
      <button type="button" onClick={() => (setFate("coming"), setTries(tries + 1))} aria-label={t("msg.retry")} className="g-press flex h-24 w-40 flex-col items-center justify-center gap-1.5 rounded-lg bg-fill text-xs text-ink-4 shadow-[inset_0_0_0_0.5px_var(--edge)]">
        <ImageOff size={16} aria-hidden />
        {fate === "offline" ? t("status.offline") : t("msg.unreadable")}
      </button>
    );
  return (
    <figure className="grid gap-1">
      <Zoom full={url} name={name}>
        <button type="button" aria-label={`${name} · ${t("msg.openImage")}`} className={cn("g-press block max-w-full overflow-hidden rounded-lg shadow-[0_0_0_0.5px_var(--edge)]", fate === "coming" && "bg-fill", !sized && fate === "coming" && "h-24 w-40")}>
          <img
            key={tries}
            src={tries ? `${url}&try=${tries}` : url}
            alt={name}
            loading="lazy"
            style={sized}
            onLoad={() => setFate("here")}
            onError={lost}
            className={cn("block h-auto max-w-full transition-opacity duration-(--dur-2) ease-(--ease-out)", !sized && "max-h-[300px] w-auto sm:max-w-[420px]", fate === "here" ? (p.unsent ? "opacity-50" : "opacity-100") : "opacity-0")}
          />
        </button>
      </Zoom>
      {p.unsent && <figcaption className="text-xs text-ink-4">{t("msg.unsent")}</figcaption>}
    </figure>
  );
}
