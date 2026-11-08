// Official Bots (plan "官方 Bot"): Bots of Iridium's own release, not built in. Only the + of 终端管理
// shows one (the user, 2026-11-08): choosing it installs it if need be and goes straight to the
// scan; bound, it is a Bot in the list as any other, removed there (its service goes with it).
// Agents do the same with its own command line (iridium-qq setup).
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronLeft, LoaderCircle } from "lucide-react";
import { useEffect, useRef, useState, type ReactNode } from "react";
import { api } from "./api/client";
import type { Extension } from "./api/types.gen";
import { MainKey, SecondKey } from "./card";
import { t, useLang } from "./i18n";
import { errText } from "./toast";
import { IconKey } from "./ui";

/** The official Bots; poll: kept fresh while the page shows one, quietly, often while it works. */
export function useExtensions(poll = false) {
  return useQuery({
    queryKey: ["extensions"],
    queryFn: api.extensions,
    staleTime: 5_000,
    refetchInterval: (q) => (!poll ? false : q.state.data?.some((x) => x.busy) ? 1_500 : 10_000),
  });
}

/** The tag an official Bot carries in the + menu. */
export function OfficialTag() {
  return <span className="shrink-0 rounded-sm bg-fill-2 px-1.5 text-[11px] leading-[18px] text-ink-2">{t("ext.official")}</span>;
}

/** Adding an official Bot: installed if need be, then its scan; bound, back to the list. */
export function ExtensionPage({ id, onBack }: { id: string; onBack: () => void }) {
  useLang();
  const qc = useQueryClient();
  const q = useExtensions(true);
  const x = q.data?.find((e) => e.id === id);
  const [phase, setPhase] = useState<"install" | "bind" | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const started = useRef(false);
  const sawBind = useRef(false);
  const keep = (r: Extension) => qc.setQueryData<Extension[]>(["extensions"], (l) => l?.map((e) => (e.id === r.id ? r : e)));

  const go = async (from: Extension) => {
    setErr(null);
    sawBind.current = false;
    try {
      let r = from;
      if (!r.installed) {
        setPhase("install");
        r = await api.extension(id, "install");
        keep(r);
        if (!r.installed) return void (setErr(r.error || t("err.extension")), setPhase(null));
      }
      setPhase("bind");
      if (r.busy !== "bind") {
        r = await api.extension(id, "bind");
        keep(r);
        if (r.error && r.busy !== "bind") return void (setErr(r.error), setPhase(null));
      }
    } catch (e) {
      setErr(errText(e));
      setPhase(null);
    }
  };

  useEffect(() => {
    if (!x || started.current) return;
    started.current = true;
    void go(x);
  });

  // The scan over: bound, the Bot is in the list; else what went wrong.
  useEffect(() => {
    if (phase !== "bind" || !x) return;
    if (x.busy === "bind") {
      sawBind.current = true;
      return;
    }
    if (!sawBind.current) return;
    setPhase(null);
    if (x.error) setErr(x.error);
    else {
      void qc.invalidateQueries({ queryKey: ["terminals"] });
      onBack();
    }
  }, [phase, x, qc, onBack]);

  const leave = () => {
    if (x?.busy === "bind") void api.unbind(id).catch(() => {});
    onBack();
  };

  return (
    <div className="flex h-full flex-col">
      <header className="flex h-10.5 shrink-0 items-center gap-1 px-1.5 pt-1.5">
        <IconKey label={t("nav.back")} onClick={leave} tipSide="bottom" className="size-7">
          <ChevronLeft size={16} />
        </IconKey>
        <h1 className="m-0 flex min-w-0 flex-1 items-center gap-2 text-base font-semibold">
          <span className="truncate">{t("ext.qq")}</span>
          <OfficialTag />
        </h1>
      </header>
      <div className="no-bar min-h-0 flex-1 overflow-y-auto pt-2 pb-4">
        <div className="flex flex-col gap-4 px-4">
          {q.isError && <p className="m-0 text-sm text-ink-3">{errText(q.error)}</p>}
          {err ? (
            <div className="g-fade flex flex-col items-start gap-3">
              <p className="m-0 text-sm break-words text-err">{err}</p>
              <MainKey onClick={() => x && void go(x)}>{t("ext.retry")}</MainKey>
            </div>
          ) : phase === "install" || !x ? (
            <p className="m-0 flex items-center gap-2 text-sm text-ink-3">
              <LoaderCircle size={14} className="animate-spin" />
              {t("ext.installing")}
            </p>
          ) : (
            <Scan x={x} cancel={leave} />
          )}
        </div>
      </div>
    </div>
  );
}

/** The scan under way: its QR code, the link for a phone, cancel. */
function Scan({ x, cancel }: { x: Extension; cancel: () => void }): ReactNode {
  const b = x.busy === "bind" ? x.bind : undefined;
  return (
    <div className="g-fade flex flex-col items-center gap-3 rounded-lg bg-fill px-4 py-4">
      {b ? (
        <img src={b.qr} alt={b.url} className="size-48 rounded-md bg-white p-2" />
      ) : (
        <span className="grid size-48 place-items-center text-sm text-ink-3">
          <LoaderCircle size={16} className="animate-spin" />
          {t("ext.waitQR")}
        </span>
      )}
      <div className="flex items-center gap-2">
        {b && (
          <a href={b.url} target="_blank" rel="noreferrer" className="g-press flex h-8 items-center rounded-md bg-fill-2 px-3 text-sm hover:bg-edge pointer-coarse:h-10">
            {t("ext.openInQQ")}
          </a>
        )}
        <SecondKey onClick={cancel} className="bg-fill-2 hover:bg-edge">
          {t("me.cancel")}
        </SecondKey>
      </div>
    </div>
  );
}
