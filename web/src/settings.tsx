// The 终端 panel (plan "运行", "更新", "网页", "交互终端"): this terminal and the settings in one
// (the user, 2026-11-07), one list with no groups: its profile and device badge, appearance,
// language; for those who manage, the 终端, 凭据 and 用量 pages; the update; signing out. No Web
// Push and no 自动更新 (kumo, 2026-11-26: both gone, updates only by hand). Not here (the user,
// 2026-11-08): its permissions (the 终端 page shows each terminal's) and the approval mode of new sessions (the
// config file's, else each agent's own). Each a title and its control, no line explaining it;
// no version shown, no head, signing out a plain row, asked once (the user, 2026-11-08). Every quota bar shows: nothing here hides one.
// Changed is saved at once (GET/PUT /v1/internal/settings). Reaping idle agents and going on after a crash
// or a quota are not here: as the config file says, by default 1h and on (the user, 2026-11-07).
import { ChevronLeft, ChevronRight } from "lucide-react";
import { useState, type ReactNode } from "react";
import { CacheTotal } from "./device/total";
import { ahead as cacheAhead } from "./device/state";
import { manages as managesApps, useMe } from "./data";
import { t, useLang } from "./i18n";
import { LangSwitch, ThemeSwitch, type ThemePref } from "./me";
import { AvatarCrop, Profile } from "./profile";
import { go, openManage } from "./route";
import { useConfirms } from "./store";
import { IconKey } from "./ui";
import { InstallItem, SignOut, UpdateItem } from "./system";
import { Item } from "./rows";

/** The panel's own pages: the avatar's crop. */
export type Page = "main" | "crop";

/** In a small dialog rising from the me key (app.tsx); on a phone a sheet from the window's foot (the
 *  user, 2026-11-06). No close key: a press anywhere else puts it away (the user, 2026-11-06).
 *  initial: the page it opens on; shown: out in front (a phone's sheet keeps it drawn when put away). */
export function SettingsPage({ theme, setTheme, initial = "main", shown = true }: { theme: ThemePref; setTheme: (v: ThemePref) => void; initial?: Page; shown?: boolean }) {
  const lang = useLang();
  const me = useMe().data;
  const manages = managesApps(me);
  const confirms = useConfirms();
  const [page, setPage] = useState<Page>(initial);
  const [picture, setPicture] = useState<string | null>(null);
  const back = () => setPage("main");
  const nav = (label: string, onClick: () => void, end?: ReactNode, ahead?: () => void) => (
    <button type="button" onClick={onClick} onPointerEnter={ahead} onPointerDown={ahead} onFocus={ahead} className="g-press -mx-1.5 flex min-h-10 items-center gap-3 rounded-md px-1.5 py-1 text-left hover:bg-fill">
      <span className="min-w-0 flex-1 truncate text-sm">{label}</span>
      {end}
      <ChevronRight size={15} className="shrink-0 text-ink-4" />
    </button>
  );

  if (page === "crop")
    return (
      <div key={page} className="g-slide flex h-full flex-col">
        <header className="flex h-10.5 shrink-0 items-center gap-1 px-1.5 pt-1.5">
          <IconKey label={t("nav.me")} onClick={back} tipSide="bottom" className="size-7">
            <ChevronLeft size={16} />
          </IconKey>
          <h1 className="m-0 min-w-0 flex-1 truncate text-base font-semibold">{t("me.avatar")}</h1>
        </header>
        <div className="no-bar min-h-0 flex-1 overflow-y-auto pt-2 pb-4">
          <div className="flex flex-col px-3">{picture && <AvatarCrop src={picture} onBack={back} />}</div>
        </div>
      </div>
    );

  // One list, no groups: this terminal first, then each setting side by side (the user, 2026-11-07).
  return (
    <div className="flex h-full flex-col" data-dir="back">
      {/* No head: the profile says whose it is, the phone's tab what it is (the user, 2026-11-08). */}
      <div className="no-bar min-h-0 flex-1 overflow-y-auto pt-2.5 pb-4">
        <div className="flex flex-col px-3">
          <Profile onPicture={(url) => (setPicture(url), setPage("crop"))} />
          <Item label={t("me.theme")}>
            <ThemeSwitch theme={theme} setTheme={setTheme} />
          </Item>
          <Item label={t("me.lang")}>
            <LangSwitch lang={lang} />
          </Item>
          {/* 应用管理, 凭据 and 角色 in one window (kumo, 2026-11-27). */}
          {nav(
            t("manage.title"),
            () => openManage(manages ? "accounts" : "roles"),
            confirms.length > 0 && <span className="size-2 shrink-0 rounded-full bg-need" aria-label={t("nav.needsYou")} />,
          )}
          {nav(t("cache.title"), () => go({ name: "cache" }), <CacheTotal />, cacheAhead)}
          <InstallItem />
          <UpdateItem manages={manages} shown={shown} />
          {me && !me.host && <SignOut />}
        </div>
      </div>
    </div>
  );
}
