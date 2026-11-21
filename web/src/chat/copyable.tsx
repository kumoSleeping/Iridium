// Something with a menu of its own at a right click or a long press (a reply, a file): 引用 where
// it may be answered (a channel's message), 复制; 折叠 or 展开 on a message that folds
// (foldstate.ts FoldMenu). A right click on words chosen keeps the browser's menu. A message of a
// channel (act) has more (react.tsx, which the chat loads and puts in acts; a public link's page
// has none of it): the emoji to react with at the top, 撤回 at the foot, its reactions under it.
import { Menu } from "@base-ui/react/menu";
import { Copy, FoldVertical, Reply, UnfoldVertical } from "lucide-react";
import { useContext, useRef, useState, type ComponentType, type ReactNode } from "react";
import { FoldMenu } from "./foldstate";
import { t } from "../i18n";
import { toast } from "../toast";
import { own, useLongPress, usePointAnchor } from "../ui";
import type { Act } from "./react";

/** What a channel's message has besides, once react.tsx is loaded. */
export const acts: { Top?: ComponentType<{ act: Act }>; Foot?: ComponentType<{ act: Act }>; Under?: ComponentType<{ act: Act }> } = {};

const ITEM =
  "flex h-8 cursor-default items-center gap-2.5 rounded-md px-2.5 text-base outline-none select-none data-highlighted:bg-fill pointer-coarse:h-11";

/** children with 复制 (copy) in a menu of their own, and 引用 (quote) first given quote; with
 *  nothing to copy, children alone. */
export function Copyable({ copy, children, className, quote, act }: { copy?: string; children: ReactNode; className?: string; quote?: () => void; act?: Act }) {
  const [at, setAt] = useState<{ x: number; y: number } | null>(null);
  const down = useRef({ x: 0, y: 0 });
  const lp = useLongPress(() => setAt(down.current));
  const fold = useContext(FoldMenu);
  if (copy === undefined) return <div className={className}>{children}</div>;
  const anchor = usePointAnchor(at);
  return (
    <div
      className={className}
      {...lp}
      onPointerDown={(e) => {
        down.current = { x: e.clientX, y: e.clientY };
        lp.onPointerDown(e);
      }}
      onContextMenu={(e) => {
        lp.onContextMenu(e);
        if (e.defaultPrevented || !own(e) || getSelection()?.toString()) return;
        e.preventDefault();
        setAt({ x: e.clientX, y: e.clientY });
      }}
    >
      <FoldMenu.Provider value={null}>{children}</FoldMenu.Provider>
      {act && acts.Under && <acts.Under act={act} />}
      <Menu.Root open={at !== null} onOpenChange={(o) => !o && setAt(null)}>
        <Menu.Portal>
          <Menu.Positioner anchor={anchor} side="bottom" align="start" sideOffset={4} collisionPadding={8} className="z-50">
            <Menu.Popup finalFocus={false} className="g-unfold glass thick float min-w-48 rounded-lg p-1.5 text-ink outline-none">
              {act && acts.Top && <acts.Top act={act} />}
              {quote && (
                <Menu.Item onClick={quote} className={ITEM}>
                  <span className="text-ink-2">
                    <Reply size={15} />
                  </span>
                  {t("msg.quote")}
                </Menu.Item>
              )}
              <Menu.Item onClick={() => void navigator.clipboard.writeText(copy).then(() => toast(t("msg.copied")))} className={ITEM}>
                <span className="text-ink-2">
                  <Copy size={15} />
                </span>
                {t("msg.copy")}
              </Menu.Item>
              {fold && (
                <Menu.Item onClick={() => fold.set(!fold.folded)} className={ITEM}>
                  <span className="text-ink-2">{fold.folded ? <UnfoldVertical size={15} /> : <FoldVertical size={15} />}</span>
                  {t(fold.folded ? "menu.unfold" : "msg.fold")}
                </Menu.Item>
              )}
              {act && acts.Foot && <acts.Foot act={act} />}
            </Menu.Popup>
          </Menu.Positioner>
        </Menu.Portal>
      </Menu.Root>
    </div>
  );
}
