// Adding the page to the home screen (plan "网页"): Chrome and Edge offer it by an event the page
// keeps (beforeinstallprompt) and shows on a key; iOS and Safari on a Mac have no such event, only
// their own menu, so the row names its steps. Installed, or a browser with neither, no row.
import { useSyncExternalStore } from "react";

type Prompt = Event & { prompt: () => Promise<void>; userChoice: Promise<{ outcome: string }> };

/** key: the browser's prompt is ready; ios / mac: the steps in its menu; null: no row. */
export type Install = "key" | "ios" | "mac" | null;

let ask: Prompt | null = null;
const told = new Set<() => void>();
const changed = () => told.forEach((f) => f());

const standalone = () => matchMedia("(display-mode: standalone)").matches || (navigator as { standalone?: boolean }).standalone === true;
const ua = navigator.userAgent;
const ios = /iPhone|iPad|iPod/.test(ua) || (/Macintosh/.test(ua) && navigator.maxTouchPoints > 1);
const macSafari = !ios && /Macintosh/.test(ua) && /Safari\//.test(ua) && !/Chrome|Chromium|Edg|Firefox|OPR/.test(ua);

/** Keeps the browser's offer for the key; runs at start (main.tsx), as the event comes once. */
export function watchInstall() {
  addEventListener("beforeinstallprompt", (e) => {
    e.preventDefault();
    ask = e as Prompt;
    changed();
  });
  addEventListener("appinstalled", () => {
    ask = null;
    changed();
  });
}

const now = (): Install => (standalone() ? null : ask ? "key" : ios ? "ios" : macSafari ? "mac" : null);

export function useInstall(): Install {
  return useSyncExternalStore((f) => (told.add(f), () => void told.delete(f)), now, now);
}

/** Shows the browser's own prompt; its offer is spent either way. */
export async function install() {
  const p = ask;
  if (!p) return;
  await p.prompt();
  await p.userChoice.catch(() => undefined);
  ask = null;
  changed();
}
