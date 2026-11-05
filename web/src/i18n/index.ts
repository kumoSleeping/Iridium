// A tiny t(): zh.ts is the source, en.ts is typed to the same keys. The language follows the browser
// until it is changed here (kept in localStorage); ?lang= overrides for screenshots.
import { useSyncExternalStore } from "react";
import { en } from "./en";
import { zh, type Key } from "./zh";

export type Lang = "zh" | "en";
const dicts = { zh, en } as const;

function initial(): Lang {
  const q = new URLSearchParams(location.search).get("lang");
  if (q === "zh" || q === "en") return q;
  try {
    const s = localStorage.getItem("lang");
    if (s === "zh" || s === "en") return s;
  } catch {}
  return navigator.language.toLowerCase().startsWith("zh") ? "zh" : "en";
}

let lang: Lang = initial();
const subs = new Set<() => void>();
document.documentElement.lang = lang === "zh" ? "zh-CN" : "en";

export function setLang(l: Lang) {
  lang = l;
  document.documentElement.lang = l === "zh" ? "zh-CN" : "en";
  try {
    localStorage.setItem("lang", l);
  } catch {}
  subs.forEach((f) => f());
}

export function t(key: Key, vars?: Record<string, string | number>): string {
  let s: string = dicts[lang][key];
  if (vars) for (const [k, v] of Object.entries(vars)) s = s.replace(`{${k}}`, String(v));
  return s;
}

/** Re-render on language change; returns the current language. */
export function useLang(): Lang {
  return useSyncExternalStore(
    (f) => (subs.add(f), () => subs.delete(f)),
    () => lang,
  );
}
