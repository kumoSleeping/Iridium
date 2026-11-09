// The pictures a tool's result gave the model (Result.images: a Read of a picture, a screenshot),
// under the step's line: each the one the model was given, read from the agent's record, never
// the file as it is now; pressed, it opens whole over the page. Its place is kept as it loads by
// its size, which the server tells for all but a few (a Codex script still running); one the
// server cannot give is a faint box with why, pressed to ask again.
import { ImageOff } from "lucide-react";
import { useEffect, useState } from "react";
import { api } from "../api/client";
import type { Image as Picture } from "../api/types.gen";
import { t } from "../i18n";
import { cn, Tip } from "../ui";
import { Zoom } from "./files";
import { pictureName, type Step } from "./fold";

/** The pictures of step s's result. */
export function Pictures({ session, s }: { session: string; s: Step }) {
  const r = s.result!;
  const images = r.images ?? [];
  return (
    <div className="mt-0.5 mb-1.5 ml-[7px] flex flex-col items-start gap-2 pl-3 shadow-[inset_1px_0_0_var(--edge)]">
      {images.map((p, n) => (
        <One key={n} url={api.imageURL(session, r.at, n)} p={p} name={(images.length > 1 ? `${n + 1}-` : "") + pictureName(s, p.mime)} />
      ))}
    </div>
  );
}

/** What became of a picture asked for: coming, here, or not given: its machine not connected
 *  (409), or not to be had (404, 410). One not in the record yet (425: a script still running) is
 *  asked again, a little later each time (at most every 16 s), `TRIES` times, about five minutes. */
type Fate = "coming" | "here" | "unreadable" | "offline";
const TRIES = 22;

/** The most a picture takes under its line, in CSS pixels. */
const MAX_W = 420;
const MAX_H = 300;

function One({ url, p, name }: { url: string; p: Picture; name: string }) {
  const [fate, setFate] = useState<Fate>("coming");
  const [tries, setTries] = useState(0);
  const [later, setLater] = useState(0); // asked again in this many ms; 0: not
  const [asked, setAsked] = useState(0); // asked again by a press: another address, not the cache's
  useEffect(() => {
    if (!later) return;
    const id = setTimeout(() => (setLater(0), setTries((n) => n + 1)), later);
    return () => clearTimeout(id);
  }, [later]);
  // Not given: ask once more for why, as an <img> is not told.
  const lost = () =>
    void fetch(url, { cache: "no-store" }).then(
      (r) => (r.status === 425 && tries < TRIES ? setLater(1000 * 2 ** Math.min(tries, 4)) : setFate(r.status === 409 ? "offline" : "unreadable")),
      () => setFate("offline"),
    );
  // Its size on the page, where the record tells: at most MAX_W × MAX_H, and the column's width.
  const k = p.w && p.h ? Math.min(1, MAX_W / p.w, MAX_H / p.h) : 0;
  const sized = k ? { width: Math.round(p.w! * k), aspectRatio: `${p.w} / ${p.h}` } : undefined;
  if (fate === "unreadable" || fate === "offline") {
    const why = fate === "offline" ? t("status.offline") : t("msg.unreadable");
    return (
      <Tip label={t("msg.retry")}>
        <button
          type="button"
          onClick={() => (setFate("coming"), setTries(0), setLater(0), setAsked(asked + 1))}
          aria-label={`${why} · ${t("msg.retry")}`}
          style={sized}
          className={cn("g-press flex max-w-full flex-col items-center justify-center gap-1.5 rounded-lg bg-fill text-xs text-ink-4 shadow-[inset_0_0_0_0.5px_var(--edge)] hover:text-ink-3", !sized && "h-24 w-40")}
        >
          <ImageOff size={16} aria-hidden />
          {why}
        </button>
      </Tip>
    );
  }
  return (
    <figure className="grid max-w-full gap-1">
      <Zoom full={url} name={name}>
        <button type="button" aria-label={`${name} · ${t("msg.openImage")}`} className={cn("g-press block max-w-full overflow-hidden rounded-lg shadow-[0_0_0_0.5px_var(--edge)]", fate === "coming" && "bg-fill", !sized && fate === "coming" && "h-24 w-40")}>
          <img
            key={`${asked}.${tries}`}
            src={asked || tries ? `${url}&try=${asked}.${tries}` : url}
            alt={name}
            loading="lazy"
            style={sized}
            onLoad={() => setFate("here")}
            onError={lost}
            className={cn("block max-w-full transition-opacity duration-(--dur-2) ease-(--ease-out)", sized ? "h-auto" : "h-auto max-h-[300px] w-auto sm:max-w-[420px]", fate === "here" ? (p.unsent ? "opacity-50" : "opacity-100") : "opacity-0")}
          />
        </button>
      </Zoom>
      {p.unsent && <figcaption className="text-xs text-ink-4">{t("msg.unsent")}</figcaption>}
    </figure>
  );
}
