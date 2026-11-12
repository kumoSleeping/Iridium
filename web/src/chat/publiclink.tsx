// A session's public link (docs/api.md "公开链接"), from the chat's ⋯: anyone with it reads the
// conversation, nothing more. Only its hash is kept, so the link shows once, as it is made; made
// again, the old one stops working; revoked, at once.
import { Popover } from "@base-ui/react/popover";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Copy, Globe } from "lucide-react";
import { useState, type RefObject } from "react";
import { api } from "../api/client";
import { MainKey, SecondKey } from "../card";
import { t } from "../i18n";
import { errText, toast } from "../toast";
import { ago } from "../ui";

export function PublicLink({ session, anchor, open, onOpenChange }: { session: string; anchor: RefObject<HTMLElement | null>; open: boolean; onOpenChange: (o: boolean) => void }) {
  const qc = useQueryClient();
  const key = ["publicLink", session];
  const q = useQuery({ queryKey: key, queryFn: () => api.publicLink(session), enabled: open, staleTime: 0 });
  const [url, setUrl] = useState("");
  const [busy, setBusy] = useState(false);
  const run = async (f: () => Promise<unknown>) => {
    setBusy(true);
    try {
      await f();
    } catch (e) {
      toast(t("err.action"), { error: true, description: errText(e) });
    } finally {
      setBusy(false);
      void qc.invalidateQueries({ queryKey: key });
    }
  };
  const make = () => run(async () => setUrl((await api.makePublicLink(session)).url ?? ""));
  const revoke = () =>
    run(async () => {
      await api.revokePublicLink(session);
      setUrl("");
      toast(t("public.revoked"));
    });
  const copy = () => void navigator.clipboard.writeText(url).then(() => toast(t("msg.copied")));
  const st = q.data;
  return (
    <Popover.Root
      open={open}
      onOpenChange={(o) => {
        onOpenChange(o);
        if (!o) setUrl("");
      }}
    >
      <Popover.Portal>
        <Popover.Positioner anchor={anchor} side="bottom" align="end" sideOffset={6} collisionPadding={8} className="z-50">
          <Popover.Popup initialFocus={(type) => type === "keyboard"} className="g-unfold glass thick float flex w-[min(340px,calc(100vw-16px))] flex-col gap-2.5 rounded-lg p-3.5 text-ink outline-none">
            <div className="flex items-center gap-2 text-base font-semibold">
              <Globe size={15} className="text-ink-2" />
              {t("public.menu")}
            </div>
            <p className="m-0 text-sm text-ink-3">{t("public.about")}</p>
            {url ? (
              <>
                <div className="flex items-center gap-1.5">
                  <input readOnly value={url} aria-label={t("public.menu")} onFocus={(e) => e.currentTarget.select()} className="h-8 min-w-0 flex-1 rounded-md bg-fill px-2.5 font-mono text-xs text-ink outline-none pointer-coarse:h-10" />
                  <MainKey onClick={copy}>
                    <Copy size={13} />
                    {t("msg.copy")}
                  </MainKey>
                </div>
                <p className="m-0 text-xs text-ink-3">{t("public.once")}</p>
              </>
            ) : (
              <p className="m-0 text-sm">{!st ? t("view.loading") : !st.ready ? t("public.notReady") : st.on ? t("public.on", { ago: ago(st.createdAt) }) : t("public.off")}</p>
            )}
            {st?.ready && (
              <div className="flex justify-end gap-1.5">
                {st.on && (
                  <SecondKey disabled={busy} onClick={() => void revoke()}>
                    {t("public.revoke")}
                  </SecondKey>
                )}
                {!url && (
                  <MainKey busy={busy} onClick={() => void make()}>
                    {st.on ? t("public.renew") : t("public.make")}
                  </MainKey>
                )}
              </div>
            )}
          </Popover.Popup>
        </Popover.Positioner>
      </Popover.Portal>
    </Popover.Root>
  );
}
