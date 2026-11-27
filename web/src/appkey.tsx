// The desktop app's key at the top bar's right (irapp.ts): in the app only. Pressed, the app's own
// window comes (internal/desktop/picker.go, web/src/appwin): 服务器 and 授权操作; the app's own, so
// this page cannot press its switches. Pressed again, that window goes.
import { MonitorSmartphone } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { useMe } from "./data";
import { t } from "./i18n";
import { ask, useApp, useWinMax } from "./irapp";
import { go, over, route } from "./route";
import { IconKey } from "./ui";
import { WinKeyPair } from "./winkey";

export function AppKey() {
  const app = useApp();
  if (!app || app.rail) return null;
  return (
    <IconKey
      label={t("app.title")}
      tipSide="bottom"
      onClick={() => ask("home")}
    >
      <MonitorSmartphone size={16} />
    </IconKey>
  );
}

/** Who this page is, told to the app. Once in the page. In a Mac window's top strip the key was
 *  tried and taken back (kumo, 2026-11-22: pressed twice there, the window went full screen). */
export function AppTell() {
  const inApp = !!useApp();
  useTellMe(inApp);
  useClosePage(inApp);
  return null;
}

/** ⌘W, Ctrl+W in the app (internal/desktop/chrome.go closePage; kumo, 2026-11-22): the page in
 *  front shut, the window only from the first page. The settings' dialog shuts as by Esc; a chat
 *  goes back to nothing open; with nothing open, the window closes. */
function useClosePage(inApp: boolean) {
  useEffect(() => {
    if (!inApp) return;
    const w = window as unknown as { __irClosePage?: () => void };
    w.__irClosePage = () => {
      const r = route();
      if (over(r)) {
        const at = document.activeElement ?? document.body;
        at.dispatchEvent(
          new KeyboardEvent("keydown", {
            key: "Escape",
            code: "Escape",
            bubbles: true,
          }),
        );
      } else if (r.name !== "home") go({ name: "home" });
      else ask("win", "close");
    };
    return () => void delete w.__irClosePage;
  }, [inApp]);
}

/** The window's keys at the page's top right, in the app only (winkey.tsx; kumo, 2026-11-22): no
 *  title bar on a Mac or on Windows. */
export function WinKeys({ className }: { className?: string }) {
  const app = useApp();
  const full = useWinMax();
  // Only an app that has no title bar and takes the keys (it says its system, os): one from before
  // keeps its own frame, and would take a press for nothing.
  if (!app?.os) return null;
  return (
    <WinKeyPair
      mac={app.os === "darwin"}
      full={full}
      ask={(v) => ask("win", v)}
      className={className}
    />
  );
}

/** The name at the sidebar's top left: Iridium, or in the app the name this Iridium goes by in it
 *  (its windows' titles, the task bar's; kumo, 2026-11-22: one person, several Iridiums, a window
 *  each). Pressed, home (kumo, 2026-11-26); in the app, a right click makes it a field, the app's
 *  alone: the Iridium knows nothing of it. */
export function AppName() {
  const app = useApp();
  const [editing, setEditing] = useState(false);
  const done = useRef(false);
  // An app from before (no os in its hello) takes no name: the mark's name, as in a browser.
  const named = !!app?.os;
  const name = (named && app.title) || "Iridium";
  if (named && editing) {
    const end = (v?: string) => {
      if (done.current) return;
      done.current = true;
      setEditing(false);
      if (v !== undefined && v.trim() !== (app.title ?? ""))
        ask("title", v.trim() === "Iridium" ? "" : v.trim());
    };
    return (
      <input
        autoFocus
        aria-label={t("app.rename")}
        defaultValue={app.title ?? ""}
        placeholder="Iridium"
        maxLength={40}
        onFocus={(e) => e.currentTarget.select()}
        onKeyDown={(e) => {
          e.stopPropagation();
          if (e.nativeEvent.isComposing || e.keyCode === 229) return;
          if (e.key === "Enter") end(e.currentTarget.value);
          else if (e.key === "Escape") end();
        }}
        onBlur={(e) => end(e.currentTarget.value)}
        className="no-ring h-7 w-full min-w-0 rounded-sm bg-fill px-1.5 text-base font-semibold tracking-[-0.01em] placeholder:text-ink-4"
      />
    );
  }
  return (
    <button
      type="button"
      onClick={() => go({ name: "home" })}
      onContextMenu={
        named
          ? (e) => {
              e.preventDefault();
              done.current = false;
              setEditing(true);
            }
          : undefined
      }
      className="-mx-1.5 min-w-0 truncate rounded-sm px-1.5 py-0.5 text-left text-base font-semibold tracking-[-0.01em] hover:bg-fill"
    >
      {name}
    </button>
  );
}

/** In the app, who this page is (name, face) told to it, for its own page's list of Iridiums. */
function useTellMe(inApp: boolean) {
  const me = useMe().data;
  const name = me?.name ?? "";
  const avatar = me?.avatar ?? "";
  useEffect(() => {
    if (!inApp || !name) return;
    let gone = false;
    const tell = (face: string) =>
      !gone && ask("me", JSON.stringify({ name, avatar: face }));
    if (!avatar) return void tell("");
    fetch(avatar)
      .then((r) => (r.ok ? r.blob() : Promise.reject()))
      .then(
        (b) =>
          new Promise<string>((done) => {
            const f = new FileReader();
            f.onload = () => done(String(f.result));
            f.readAsDataURL(b);
          }),
      )
      .then(tell, () => tell(""));
    return () => void (gone = true);
  }, [inApp, name, avatar]);
}
