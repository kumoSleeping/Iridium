// Words between agents, long (kumo, 2026-11-15: 给人的话不折，agent 之间的话、子代理交回的结果
// 折起): their first lines, fading out at the foot, and a quiet line under them, 还有 N 行, as the
// sidebar's 还有 N 个; pressed, the rest opens in place, its height gliding, and 收起 folds it
// again. Only the words fold: the head over them stays. Any message folds the same way by hand
// (its menu's 折叠), and old ones read already of themselves (kumo, 2026-11-21: foldstate.ts).
import { Ellipsis } from "lucide-react";
import { useLayoutEffect, useRef, useState, type ReactNode, type RefObject } from "react";
import { flushSync } from "react-dom";
import { t, useLang } from "../i18n";
import { motion, reduced } from "../motion";
import { moveLog } from "../motionlog";
import { layoutMoves } from "./stick";
import { cn } from "../ui";
import { FOLD_AT, FOLD_SHOW } from "./channel/folds";
import { FoldMenu, useFold, type Fold } from "./foldstate";

/** One of the words' lines: 15px type on 23px lines. */
const LINE = 23;

/** el's height glides from what it was (from) to what it is now. */
const glide = (el: HTMLElement, from: number) => (moveLog("longwords", "fold", { dy: el.offsetHeight - from }), el).animate([{ height: `${from}px` }, { height: `${el.offsetHeight}px` }], { duration: motion.dur(2), easing: motion.ease("out") });

/** A fold set from elsewhere (its message's menu): how tall it was just before (0: nothing to
 *  glide), whether it was set by its own key (mine), its element. */
type Prev = { h: number; mine: boolean; el: HTMLElement | null };

/** fold: these are words between agents; lines: how long they are. ctl: the message's fold, set by
 *  hand or of itself (foldstate.ts), in place of fold: folded, its first lines (FOLD_SHOW of long
 *  words, else one), the rest a line saying how many. onOpen: as it is opened (the whole of words
 *  cut short asked for: one key for both, kumo, 2026-11-21). prev: Foldable's. quiet: its key says
 *  展开 alone, not how many lines (a step's record, kumo, 2026-11-24). at, shows: past how many
 *  lines it folds and how many of the words' lines high it is then, when not the words' own. */
export function LongWords({ fold, lines, children, ctl, onOpen, prev, quiet, at = FOLD_AT, shows = FOLD_SHOW }: { fold: boolean; lines: number; children: ReactNode; ctl?: Fold; onOpen?: () => void; prev?: RefObject<Prev>; quiet?: boolean; at?: number; shows?: number }) {
  useLang();
  const [own, setOwn] = useState(false);
  // Opened by a press here: 收起 under it, to fold it again.
  const [pressed, setPressed] = useState(false);
  const box = useRef<HTMLDivElement>(null);
  const outer = useRef<HTMLDivElement>(null);
  const long = lines > at;
  const open = ctl ? !ctl.folded : own;
  // Folded or opened from its menu: its height glides as a press's does, from how tall it was then
  // first, then said to move of itself: the chat sees no change. Nothing measured otherwise.
  useLayoutEffect(() => {
    const p = prev?.current;
    if (!p) return;
    p.el = outer.current;
    if (!p.h || !p.el) return;
    const h = p.h;
    p.h = 0;
    if (!reduced()) (glide(p.el, h), layoutMoves(p.el, motion.dur(2)));
  });
  if (!ctl && (!fold || !long)) return <>{children}</>;
  const folds = !open || pressed;
  const show = long ? shows : 1;
  const toggle = () => {
    const set = () => {
      setPressed(true);
      if (prev?.current) prev.current.mine = true;
      if (ctl) ctl.set(open);
      else setOwn(!open);
      if (!open) onOpen?.();
    };
    const el = box.current;
    if (!el || reduced()) return set();
    // Its height is its own to draw: the chat only keeps its end under it (stick.tsx).
    layoutMoves(el, motion.dur(2));
    const from = el.offsetHeight;
    flushSync(set);
    glide(el, from);
  };
  const more = lines - show;
  return (
    <div ref={outer} className="flex min-w-0 flex-col">
      {/* Its first lines, the last fading out: the last line and a half, never more (a step's
          record shows 28 lines and faded the 13 under them to nothing, kumo, 2026-11-24: 别虚化这么多). */}
      <div ref={box} className={cn("min-w-0", folds && "overflow-clip", !open && "[mask-image:linear-gradient(to_bottom,black_max(55%,calc(100%_-_35px)),transparent)]")} style={open ? undefined : { maxHeight: show * LINE }}>
        {children}
      </div>
      {folds && (
        <button type="button" onClick={toggle} aria-expanded={open} className="g-press mt-1 flex h-6 w-fit items-center gap-1.5 rounded-md text-xs text-ink-3 hover:text-ink-2 pointer-coarse:h-9">
          <Ellipsis size={13} />
          {open ? t("nav.less") : more > 0 && !quiet ? t("chat.moreLines", { n: more }) : t("menu.unfold")}
        </button>
      )}
    </div>
  );
}

/** A message that folds (foldstate.ts): 折叠 / 展开 in the menu of what copies it (FoldMenu), its
 *  words folded where they are drawn (wrap them: LongWords). auto: folded of itself till set by
 *  hand; lines: how long its words are. */
export function Foldable({ scope, id, auto, lines, children }: { scope: string; id: string; auto: boolean; lines: number; children: (wrap: (words: ReactNode) => ReactNode) => ReactNode }) {
  const base = useFold(scope, id, auto);
  const prev = useRef<Prev>({ h: 0, mine: false, el: null });
  const ctl: Fold = {
    folded: base.folded,
    set: (f) => {
      const p = prev.current;
      if (!p.mine && p.el) p.h = p.el.offsetHeight;
      p.mine = false;
      base.set(f);
    },
  };
  return (
    <FoldMenu.Provider value={ctl}>
      {children((words) => (
        <LongWords fold lines={lines} ctl={ctl} prev={prev}>
          {words}
        </LongWords>
      ))}
    </FoldMenu.Provider>
  );
}
