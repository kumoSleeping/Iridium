// 分享到 ▸ (draft 四): a submenu of the apps whose logins may use the thing, each a submenu of
// those logins; one picked shares it with that login (POST /api/share), Iridium having checked it
// may use it.
import { Menu } from "@base-ui/react/menu";
import { useQuery } from "@tanstack/react-query";
import { ChevronRight, Share2 } from "lucide-react";
import type { CSSProperties } from "react";
import { api } from "./api/client";
import type { ShareRequest } from "./api/types.gen";
import { t } from "./i18n";
import { errText, toast } from "./toast";

type Thing = Omit<ShareRequest, "to" | "note">;

const ITEM = "flex h-8 cursor-default items-center gap-2.5 rounded-md px-2.5 text-base outline-none select-none data-highlighted:bg-fill data-popup-open:bg-fill data-disabled:opacity-40 pointer-coarse:h-11";
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
  const apps = q.data?.apps ?? [];
  const share = (app: string, login: string, name: string) =>
    void api.share({ ...thing, to: `${app}/${login}` }).then(
      () => toast(t("share.done", { name })),
      (e) => toast(t("err.share"), { error: true, description: errText(e) }),
    );
  if (!q.data) return <div className="flex h-8 items-center px-2.5 text-sm text-ink-3">{q.isError ? t("err.share") : t("share.loading")}</div>;
  if (!apps.length) return <div className="flex h-8 items-center px-2.5 text-sm text-ink-3">{t("share.none")}</div>;
  return (
    <div className="g-stagger flex flex-col">
      {apps.map((a, i) => (
        <Menu.SubmenuRoot key={a.id}>
          <Menu.SubmenuTrigger style={{ "--i": i } as CSSProperties} className={ITEM}>
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
