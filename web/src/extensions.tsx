// Official extensions in 终端管理 (plan "插件"): plugins of Iridium's own release, not built in.
// One is installed when asked (its binary of this very version), then managed here: the scan
// that makes its QQ bot, its service, its owners, removing it. Agents do the same with the
// extension's own command line (iridium-qq setup).
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Bot, ChevronLeft, ChevronRight, LoaderCircle, Trash2 } from "lucide-react";
import { useState, type ReactNode } from "react";
import { api } from "./api/client";
import type { Extension } from "./api/types.gen";
import { MainKey, SecondKey } from "./card";
import { t, useLang } from "./i18n";
import { Item } from "./settings";
import { errText, toast } from "./toast";
import { IconKey, Switch } from "./ui";

/** The extensions; poll: kept fresh while a page shows them, quietly (no spinner), often only while
 *  something is under way. */
export function useExtensions(poll = false) {
  return useQuery({
    queryKey: ["extensions"],
    queryFn: api.extensions,
    staleTime: 5_000,
    refetchInterval: (q) => (!poll ? false : q.state.data?.some((x) => x.busy || x.status?.service === "activating") ? 1_500 : 10_000),
  });
}

const nameOf = (x: Pick<Extension, "id" | "name">) => (x.id === "qq" ? t("ext.qq") : x.name);

/** The tag every official extension carries. */
export function OfficialTag() {
  return <span className="shrink-0 rounded-sm bg-fill-2 px-1.5 text-[11px] leading-[18px] text-ink-2">{t("ext.official")}</span>;
}

/** One line of how the extension stands. */
function stateLine(x: Extension): { text: string; live?: boolean; bad?: boolean } {
  if (!x.installed) return { text: t("ext.notInstalled") };
  if (x.busy === "bind") return { text: t("ext.bind") };
  const s = x.status;
  if (!s?.bound) return { text: t("ext.notBound") };
  if (s.service === "active") {
    const r = s.running;
    if (r && !r.qq) return { text: `${t("ext.running")} · ${t("ext.qqOff")}`, bad: true };
    if (r && !r.iridium) return { text: `${t("ext.running")} · ${t("ext.irOff")}`, bad: true };
    return { text: `${t("ext.running")} · ${t("ext.qqOn")}`, live: true };
  }
  if (s.service === "activating") return { text: t("ext.starting") };
  if (s.service === "failed") return { text: t("ext.failed"), bad: true };
  return { text: t("ext.stopped") };
}

/** The installed extensions, each a row that opens its page. */
export function ExtensionRows({ openExt }: { openExt: (id: string) => void }) {
  const q = useExtensions();
  const list = (q.data ?? []).filter((x) => x.installed);
  if (list.length === 0) return null;
  return (
    <section className="flex flex-col" aria-label={t("ext.section")}>
      <h2 className="m-0 mb-1 px-2 text-xs font-medium text-ink-3">{t("ext.section")}</h2>
      {list.map((x) => {
        const st = stateLine(x);
        return (
          <button key={x.id} type="button" onClick={() => openExt(x.id)} className="g-press flex min-h-14 items-center gap-3 rounded-lg px-2 text-left hover:bg-fill">
            <span className="grid size-8 shrink-0 place-items-center rounded-full bg-fill-2 text-ink-2">
              <Bot size={17} />
            </span>
            <span className="min-w-0 flex-1">
              <span className="flex items-center gap-2">
                <span className="truncate text-base font-medium">{nameOf(x)}</span>
              </span>
              <span className="flex min-w-0 items-center gap-1.5 text-xs text-ink-3">
                {st.live ? <span className="size-1.5 shrink-0 rounded-full bg-run" aria-hidden /> : null}
                <span className={st.bad ? "min-w-0 truncate text-err" : "min-w-0 truncate"}>{st.text}</span>
              </span>
            </span>
            <ChevronRight size={15} className="shrink-0 text-ink-4" />
          </button>
        );
      })}
    </section>
  );
}

/** An extension's page: install, scan, the service, owners, remove. */
export function ExtensionPage({ id, onBack }: { id: string; onBack: () => void }) {
  useLang();
  const qc = useQueryClient();
  const q = useExtensions(true);
  const x = q.data?.find((e) => e.id === id);
  const [doing, setDoing] = useState<string | null>(null);
  const run = async (what: string, f: () => Promise<Extension>) => {
    setDoing(what);
    try {
      const r = await f();
      qc.setQueryData<Extension[]>(["extensions"], (l) => l?.map((e) => (e.id === r.id ? r : e)));
      if (r.error) toast(t("err.extension"), { error: true, description: r.error });
      if (what === "remove") onBack();
    } catch (e) {
      toast(t("err.extension"), { error: true, description: errText(e) });
    } finally {
      setDoing(null);
      void qc.invalidateQueries({ queryKey: ["extensions"] });
      void qc.invalidateQueries({ queryKey: ["terminals"] });
    }
  };
  const s = x?.status;
  const st = x ? stateLine(x) : null;
  const active = s?.service === "active" || s?.service === "activating";
  return (
    <div className="flex h-full flex-col">
      <header className="flex h-10.5 shrink-0 items-center gap-1 px-1.5 pt-1.5">
        <IconKey label={t("nav.back")} onClick={onBack} tipSide="bottom" className="size-7">
          <ChevronLeft size={16} />
        </IconKey>
        <h1 className="m-0 flex min-w-0 flex-1 items-center gap-2 text-base font-semibold">
          <span className="truncate">{x ? nameOf(x) : t("ext.qq")}</span>
          <OfficialTag />
        </h1>
      </header>
      <div className="no-bar min-h-0 flex-1 overflow-y-auto pt-2 pb-4">
        <div className="flex flex-col gap-4 px-4">
          {q.isError && <p className="m-0 text-sm text-ink-3">{errText(q.error)}</p>}
          {x && !x.installed && (
            <Item label={t("ext.status")} hint={t("ext.qqHint")}>
              <MainKey busy={doing === "install" || x.busy === "install"} onClick={() => void run("install", () => api.extension(id, "install"))}>
                {doing === "install" ? t("ext.installing") : t("ext.install")}
              </MainKey>
            </Item>
          )}
          {x?.installed && x.busy === "bind" && <Scan x={x} cancel={() => void run("unbind", () => api.unbind(id))} />}
          {x?.installed && x.busy !== "bind" && !s?.bound && (
            <Item label={t("ext.status")} hint={t("ext.notBound")}>
              <MainKey busy={doing === "bind"} onClick={() => void run("bind", () => api.extension(id, "bind"))}>
                {t("ext.bind")}
              </MainKey>
            </Item>
          )}
          {x?.installed && s?.bound && x.busy !== "bind" && (
            <>
              <Item label={t("ext.status")}>
                <span className={st?.bad ? "text-right text-sm text-err" : "text-right text-sm text-ink-2"}>{st?.text}</span>
              </Item>
              {(s.running?.qqError || s.running?.iridiumError) && <p className="m-0 -mt-2 text-xs break-words text-ink-3">{s.running.qqError || s.running.iridiumError}</p>}
              <Item label={t("ext.service")}>
                <Switch label={t("ext.service")} on={active} disabled={doing === "service"} onChange={(on) => void run("service", () => api.extension(id, on ? "start" : "stop"))} />
              </Item>
              <Item label={t("ext.bot")}>
                <span className="flex min-w-0 items-center gap-2">
                  <span className="truncate text-sm text-ink-2 tabular-nums">{s.running?.bot ? `${s.running.bot} · ${s.appId}` : s.appId}</span>
                  <SecondKey disabled={doing === "bind"} onClick={() => void run("bind", () => api.extension(id, "bind"))}>
                    {t("ext.bind")}
                  </SecondKey>
                </span>
              </Item>
            </>
          )}
          {x?.error && <p className="m-0 text-xs break-words text-err">{x.error}</p>}
          {x?.installed && (
            <div className="flex justify-end pt-2">
              <button type="button" disabled={doing === "remove"} onClick={() => void run("remove", () => api.removeExtension(id))} className="g-press flex h-8 items-center gap-1.5 rounded-md px-2.5 text-sm text-err hover:bg-fill disabled:opacity-50">
                {doing === "remove" ? <LoaderCircle size={14} className="animate-spin" /> : <Trash2 size={14} />}
                {t("ext.remove")}
              </button>
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

/** The scan under way: its QR code, the link for a phone, cancel. */
function Scan({ x, cancel }: { x: Extension; cancel: () => void }): ReactNode {
  const b = x.bind;
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
