// This panel's identity at the sidebar's bottom-left (plan "网页"): a terminal, not a person, shown
// like a signed-in account: its avatar and name, a key to the 终端 panel (settings.tsx), which holds
// its profile and every setting, one panel for both (the user, 2026-11-07). Local-only with no
// Passkey, it is 本机. Also the bits the panel and the pages share: the theme, the language, the
// segmented control, 本机's mark.
import { LayoutGrid, Monitor, Moon, Sun } from "lucide-react";
import { useEffect, type ReactNode } from "react";
import type { Me } from "./api/types.gen";
import { api, queryClient } from "./api/client";
import { LOCAL_ME, useMe, useSettings } from "./data";
import { setLang, t, useLang, type Lang } from "./i18n";
import { tellTheme } from "./irapp";
import { useConfirms } from "./store";
import { Avatar, cn, IconKey, IrMark, keep, kept, Tip } from "./ui";

export type ThemePref = "light" | "dark" | "auto";

export function initialTheme(): ThemePref {
  const q = new URLSearchParams(location.search).get("theme");
  if (q === "light" || q === "dark" || q === "auto") return q;
  const s = kept("theme");
  return s === "light" || s === "dark" ? s : "auto";
}

/** Its avatar and name, the key to the 终端 panel (data-settings-key: the panel rises from it, app.tsx);
 *  a dot on the avatar while a newer release is out; no version (the panel's 检查更新 says it, the user,
 *  2026-11-12). code: the panel's code fetched, as a pointer comes near. */
export function MeKey({ open, onOpen, dot, code, manage }: { open: boolean; onOpen: () => void; dot?: boolean; code?: () => void; manage?: () => void }) {
  const me: Me = useMe().data ?? LOCAL_ME;
  const local = me.host && !me.named; // the host as Iridium; named, as its own (Me.named)
  const name = local ? t("me.local") : me.name;
  // The dot: a request waiting (管理's 请求, kumo 2026-11-27), else a newer release.
  const needs = useConfirms().length > 0;
  // 管理's key at the pill's right end, over it (kumo, 2026-11-27): a key of its own, not in the
  // button; shown as the pointer is over the pill or the key has the focus (a touch screen, always).
  return (
    <div className="group/me relative flex w-full min-w-0">
    <Tip label={dot ? `${t("nav.me")} · ${t(needs ? "nav.needsYou" : "settings.updateReady")}` : t("nav.me")} align="start">
      <button
        type="button"
        data-settings-key
        aria-pressed={open}
        onClick={onOpen}
        onPointerEnter={code}
        onFocus={code}
        className={cn("g-press flex h-11 w-full min-w-0 items-center gap-2.5 rounded-full pl-1.5 pointer-coarse:h-12 pointer-coarse:pl-2", manage ? "pr-11" : "pr-3", open ? "g-picked" : "hover:g-picked")}
      >
        {/* A grid, not inline: no line's descent under the face, which sits centred in the key. */}
        <span className="relative grid shrink-0">
          {local ? <LocalMark size={32} /> : <Avatar src={me.avatar} name={name} size={32} pattern={me.id} />}
          {dot && <span className="g-pop absolute -top-0.5 -right-0.5 size-2.5 rounded-full bg-err shadow-[0_0_0_1.5px_var(--paper)]" aria-hidden />}
        </span>
        <span className="min-w-0 truncate text-md text-ink">{name}</span>
      </button>
    </Tip>
    {manage && (
      <IconKey label={t("manage.open")} onClick={manage} className="absolute top-1/2 right-1.5 size-8 -translate-y-1/2 text-ink-4 opacity-0 transition-[opacity,color] hover:text-ink-2 duration-(--dur-2) group-hover/me:opacity-100 focus-visible:opacity-100 pointer-coarse:size-9 pointer-coarse:opacity-100">
        <LayoutGrid size={16} />
      </IconKey>
    )}
    </div>
  );
}

/** The host's mark in place of an avatar: Iridium's own (kumo, 2026-11-16: one mark for the host
 *  everywhere, the one the computers list has). */
export function LocalMark({ size, tint }: { size: number; tint?: string }) {
  return <IrMark size={size} ground={tint ?? "var(--ink)"} />;
}

export function LangSwitch({ lang }: { lang: Lang }) {
  return (
    <Segmented
      label={t("me.lang")}
      value={lang}
      onChange={(v) => (setLang(v as Lang), tellLang(v as Lang))}
      items={[
        { v: "zh", label: "中文", icon: <span className="text-[13px]">中</span> },
        { v: "en", label: "English", icon: <span className="text-[12px] font-medium">EN</span> },
      ]}
    />
  );
}

/** Tells the daemon the user's language, which hosted agents are told from their next start
 *  (plan "会话标题"): the settings' 语言, once chosen; a page that finds none told yet tells its own. */
function tellLang(lang: Lang) {
  void api.putSettings({ lang }).then(
    (s) => queryClient.setQueryData(["settings"], s),
    () => {},
  );
}

export function useTellLang() {
  const told = useSettings().data?.lang;
  const lang = useLang();
  useEffect(() => {
    if (told === "") tellLang(lang);
  }, [told, lang]);
}

export function applyTheme(v: ThemePref, systemDark: boolean) {
  const theme = v === "auto" ? (systemDark ? "dark" : "light") : v;
  document.documentElement.dataset.theme = theme;
  // The browser's bar (an installed app's title bar) in the page's paper (index.html, theme.js).
  for (const m of document.querySelectorAll<HTMLMetaElement>('meta[name="theme-color"]')) {
    m.removeAttribute("media");
    m.content = theme === "dark" ? "#121212" : "#fdfdfd";
  }
}
export const keepTheme = (v: ThemePref) => (keep("theme", v === "auto" ? null : v), tellTheme(v));

export function ThemeSwitch({ theme, setTheme }: { theme: ThemePref; setTheme: (v: ThemePref) => void }) {
  return (
    <Segmented
      label={t("me.theme")}
      value={theme}
      onChange={(v) => setTheme(v as ThemePref)}
      items={[
        { v: "light", label: t("me.light"), icon: <Sun size={15} /> },
        { v: "dark", label: t("me.dark"), icon: <Moon size={15} /> },
        { v: "auto", label: t("me.auto"), icon: <Monitor size={15} /> },
      ]}
    />
  );
}

export function Segmented({ label, value, onChange, items }: { label: string; value: string; onChange: (v: string) => void; items: { v: string; label: string; icon: ReactNode }[] }) {
  return (
    <div role="radiogroup" aria-label={label} className="flex rounded-md bg-fill p-0.5">
      {items.map((it) => (
        <Tip key={it.v} label={it.label}>
          <button
            type="button"
            role="radio"
            aria-checked={value === it.v}
            aria-label={it.label}
            onClick={() => onChange(it.v)}
            className={cn("g-press grid h-7 min-w-8 place-items-center rounded-[5px] px-1.5 pointer-coarse:h-9", value === it.v ? "bg-paper text-ink shadow-[0_0_0_0.5px_var(--edge),0_1px_2px_var(--edge)]" : "text-ink-3 hover:text-ink")}
          >
            {it.icon}
          </button>
        </Tip>
      ))}
    </div>
  );
}
