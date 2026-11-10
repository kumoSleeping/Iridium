// 分享到 ▸ (draft 四): a submenu of the apps whose logins may use the thing, each a submenu of
// those logins; one picked shares it with that login (POST /api/share), Iridium having checked it
// may use it.
import { Menu } from "@base-ui/react/menu";
import { useQuery } from "@tanstack/react-query";
import { ChevronRight, Copy, Share2 } from "lucide-react";
import { useRef, useState, type CSSProperties, type ReactNode } from "react";
import { api } from "./api/client";
import type { ShareRequest } from "./api/types.gen";
import { t } from "./i18n";
import { errText, toast } from "./toast";
import { own, useLongPress } from "./ui";

type Thing = Omit<ShareRequest, "to" | "note">;

const ITEM =
  "flex h-8 cursor-default items-center gap-2.5 rounded-md px-2.5 text-base outline-none select-none data-highlighted:bg-fill data-popup-open:bg-fill data-disabled:opacity-40 pointer-coarse:h-11";
const POPUP = "g-unfold glass thick float min-w-44 rounded-lg p-1.5 text-ink outline-none";

/** 分享到 in a menu: i is where the menu's stagger is. */
export function ShareSubmenu({ thing, i }: { thing: Thing; i: number }) {
  return (
    <Menu.SubmenuRoot>
      <Menu.SubmenuTrigger style={{ "--i": i } as CSSProperties} className={ITEM}>
        <span className="text-ink-2">
          <Share2 size={15} />
        </span>
        <span className="flex-1">{t("share.to")}</span>
        <ChevronRight size={14} className="text-ink-4" />
      </Menu.SubmenuTrigger>
      <Menu.Portal>
        <Menu.Positioner className="z-50" sideOffset={4} collisionPadding={8}>
          <Menu.Popup className={POPUP}>
            <Targets thing={thing} />
          </Menu.Popup>
        </Menu.Positioner>
      </Menu.Portal>
    </Menu.SubmenuRoot>
  );
}

function Targets({ thing }: { thing: Thing }) {
  const q = useQuery({ queryKey: ["shareTargets", thing], queryFn: () => api.shareTargets(thing), staleTime: 10_000 });
  const [note, setNote] = useState("");
  const apps = q.data?.apps ?? [];
  const share = (app: string, login: string, name: string) =>
    void api.share({ ...thing, to: `${app}/${login}`, ...(note.trim() ? { note: note.trim() } : {}) }).then(
      () => toast(t("share.done", { name })),
      (e) => toast(t("err.share"), { error: true, description: errText(e) }),
    );
  if (!q.data) return <div className="flex h-8 items-center px-2.5 text-sm text-ink-3">{q.isError ? t("err.share") : t("share.loading")}</div>;
  if (!apps.length) return <div className="flex h-8 items-center px-2.5 text-sm text-ink-3">{t("share.none")}</div>;
  return (
    <div className="g-stagger flex flex-col">
      {/* A note goes with it: typed here, the menu's own keys left alone. */}
      <input
        value={note}
        onChange={(e) => setNote(e.target.value)}
        onKeyDown={(e) => e.stopPropagation()}
        placeholder={t("share.note")}
        aria-label={t("share.note")}
        maxLength={500}
        style={{ "--i": 0 } as CSSProperties}
        className="mb-1 h-8 rounded-md bg-fill px-2.5 text-base text-ink outline-none placeholder:text-ink-4 pointer-coarse:h-11"
      />
      {apps.map((a, i) => (
        <Menu.SubmenuRoot key={a.id}>
          <Menu.SubmenuTrigger style={{ "--i": i + 1 } as CSSProperties} className={ITEM}>
            <span className="flex-1 truncate">{a.id === "ir_web" ? t("term.panel") : a.name}</span>
            <ChevronRight size={14} className="text-ink-4" />
          </Menu.SubmenuTrigger>
          <Menu.Portal>
            <Menu.Positioner className="z-50" sideOffset={4} collisionPadding={8}>
              <Menu.Popup className={POPUP}>
                <div className="g-stagger flex flex-col">
                  {a.logins.map((l, j) => (
                    <Menu.Item key={l.id} onClick={() => share(a.id, l.id, l.name)} style={{ "--i": j } as CSSProperties} className={ITEM}>
                      <span className="truncate">{l.name}</span>
                    </Menu.Item>
                  ))}
                </div>
              </Menu.Popup>
            </Menu.Positioner>
          </Menu.Portal>
        </Menu.SubmenuRoot>
      ))}
    </div>
  );
}

/** Something with a menu of its own at a right click or a long press (a reply, a file): 复制 if
 *  copy is given, and 分享到. A right click on words chosen keeps the browser's menu. */
export function Shareable({ thing, copy, children, className }: { thing: Thing; copy?: string; children: ReactNode; className?: string }) {
  const [at, setAt] = useState<{ x: number; y: number } | null>(null);
  const down = useRef({ x: 0, y: 0 });
  const lp = useLongPress(() => setAt(down.current));
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
              <div className="g-stagger flex flex-col">
                {copy !== undefined && (
                  <Menu.Item onClick={() => void navigator.clipboard.writeText(copy).then(() => toast(t("msg.copied")))} style={{ "--i": 0 } as CSSProperties} className={ITEM}>
                    <span className="text-ink-2">
                      <Copy size={15} />
                    </span>
                    {t("msg.copy")}
                  </Menu.Item>
                )}
                <ShareSubmenu thing={thing} i={copy !== undefined ? 1 : 0} />
              </div>
            </Menu.Popup>
          </Menu.Positioner>
        </Menu.Portal>
      </Menu.Root>
    </div>
  );
}
