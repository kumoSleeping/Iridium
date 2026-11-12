// Something with a menu of its own at a right click or a long press (a reply, a file): 复制. A right
// click on words chosen keeps the browser's menu.
import { Menu } from "@base-ui/react/menu";
import { Copy } from "lucide-react";
import { useRef, useState, type ReactNode } from "react";
import { t } from "../i18n";
import { toast } from "../toast";
import { own, useLongPress } from "../ui";

const ITEM =
  "flex h-8 cursor-default items-center gap-2.5 rounded-md px-2.5 text-base outline-none select-none data-highlighted:bg-fill pointer-coarse:h-11";

/** children with 复制 (copy) in a menu of their own; with nothing to copy, children alone. */
export function Copyable({ copy, children, className }: { copy?: string; children: ReactNode; className?: string }) {
  const [at, setAt] = useState<{ x: number; y: number } | null>(null);
  const down = useRef({ x: 0, y: 0 });
  const lp = useLongPress(() => setAt(down.current));
  if (copy === undefined) return <div className={className}>{children}</div>;
  const anchor = at && { getBoundingClientRect: () => DOMRect.fromRect({ x: at.x, y: at.y, width: 0, height: 0 }) };
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
      {children}
      <Menu.Root open={at !== null} onOpenChange={(o) => !o && setAt(null)}>
        <Menu.Portal>
          <Menu.Positioner anchor={anchor} side="bottom" align="start" sideOffset={4} collisionPadding={8} className="z-50">
            <Menu.Popup finalFocus={false} className="g-unfold glass thick float min-w-48 rounded-lg p-1.5 text-ink outline-none">
              <Menu.Item onClick={() => void navigator.clipboard.writeText(copy).then(() => toast(t("msg.copied")))} className={ITEM}>
                <span className="text-ink-2">
                  <Copy size={15} />
                </span>
                {t("msg.copy")}
              </Menu.Item>
            </Menu.Popup>
          </Menu.Positioner>
        </Menu.Portal>
      </Menu.Root>
    </div>
  );
}
