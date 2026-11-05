// What scrolls as a chat does (Rhodium's stick.tsx): it opens at the newest, or where it was left,
// and stays at the newest as more comes; scrolled up, it brings the pages before without moving what
// you read, and what comes below shows a key to go down.
import { ArrowDown } from "lucide-react";
import { useLayoutEffect, useRef, useState, type CSSProperties, type ReactNode, type RefObject } from "react";
import { flushSync } from "react-dom";
import { t } from "../i18n";

const NEAR = 600; // px from the top where the page before is brought
const END = 40; // px from the bottom that count as at the newest

export function Stick({ tail, older, place, style, children, scroller, bottom }: { tail: unknown; older: () => Promise<boolean>; place: { top: number | null }; style?: CSSProperties; children: ReactNode; scroller?: RefObject<HTMLDivElement | null>; bottom: number }) {
  const own = useRef<HTMLDivElement>(null);
  const list = scroller ?? own;
  const atEnd = useRef(place.top === null);
  const drawn = useRef<unknown>(undefined);
  const paging = useRef(false);
  const [fresh, setFresh] = useState(false);
  const [, redraw] = useState(0);

  const back = async () => {
    const el = list.current;
    if (!el || paging.current) return;
    paging.current = true;
    try {
      while (el.isConnected && el.scrollTop < NEAR && (await older())) {
        const h = el.scrollHeight;
        flushSync(() => redraw((n) => n + 1));
        el.scrollTop += el.scrollHeight - h;
      }
    } finally {
      paging.current = false;
    }
  };

  useLayoutEffect(() => {
    const el = list.current!;
    const first = drawn.current === undefined;
    const was = drawn.current;
    drawn.current = tail;
    if (first && place.top !== null) el.scrollTop = place.top;
    else if (first || atEnd.current) el.scrollTop = el.scrollHeight;
    else if (was !== tail) setFresh(true);
    void back().catch(() => {});
  });

  // Content that grows (a picture loading, a fold opened at the bottom, the composer growing) keeps it at the newest.
  useLayoutEffect(() => {
    const el = list.current!;
    const ro = new ResizeObserver(() => void (atEnd.current && (el.scrollTop = el.scrollHeight)));
    ro.observe(el);
    if (el.firstElementChild) ro.observe(el.firstElementChild);
    return () => {
      ro.disconnect();
      const end = el.scrollHeight - el.scrollTop - el.clientHeight < END;
      place.top = end ? null : el.scrollTop;
    };
  }, []);

  const scrolled = () => {
    const el = list.current!;
    atEnd.current = el.scrollHeight - el.scrollTop - el.clientHeight < END;
    if (atEnd.current) setFresh(false);
    void back().catch(() => {});
  };

  return (
    <>
      <div ref={list} role="log" aria-label={t("chat.log")} onScroll={scrolled} className="absolute inset-0 overflow-y-auto [overflow-anchor:none]" style={style}>
        {children}
      </div>
      {fresh && (
        <button
          type="button"
          onClick={() => list.current!.scrollTo({ top: list.current!.scrollHeight, behavior: "smooth" })}
          className="g-pop g-press glass thick float absolute left-1/2 z-10 flex h-8 -translate-x-1/2 items-center gap-1.5 rounded-full px-3 text-xs font-medium text-ink"
          style={{ bottom: bottom + 10 }}
        >
          {t("chat.newer")}
          <ArrowDown size={13} />
        </button>
      )}
    </>
  );
}
