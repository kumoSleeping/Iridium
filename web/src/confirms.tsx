// A confirmation an agent's ir asked a panel for (docs/api.md "确认"): a new panel's invite or a
// plugin's token, which must not reach the agent. Its card sits above the asking session's
// composer and on the settings' 终端 page, and the sidebar lists it under 需要你. Allowed, the
// secret goes to this panel alone, in the secret's dialog; the agent only learns how it went.
import { KeyRound, Link2 } from "lucide-react";
import { useState } from "react";
import { api } from "./api/client";
import type { Confirm } from "./api/types.gen";
import { CardDetails, CardFrame, CardHead, Facts, MainKey, SecondKey } from "./card";
import { t, useLang } from "./i18n";
import { openChat } from "./route";
import { showSecret } from "./secrets";
import { useList } from "./store";
import { errText, toast } from "./toast";
import { titleOf } from "./ui";

export const confirmTitle = (c: Confirm) => (c.kind === "invite" ? t("confirm.invite") : t("confirm.token"));

export function ConfirmCard({ c }: { c: Confirm }) {
  const lang = useLang();
  const list = useList();
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState<"allow" | "deny" | null>(null);
  const [leaving, setLeaving] = useState(false);
  const from = list?.find((s) => s.id === c.session);
  const who = from ? titleOf(from) : c.asker;
  const answer = async (allow: boolean) => {
    setBusy(allow ? "allow" : "deny");
    try {
      const r = await api.answerConfirm(c.id, allow);
      setLeaving(true);
      if (r.invite) showSecret({ invite: r.invite });
      else if (r.token) showSecret({ token: r.token });
      else if (r.state === "failed") toast(t("err.confirm"), { error: true, description: r.error });
      else toast(allow ? t("toast.allowed") : t("toast.denied"));
    } catch (e) {
      toast(t("err.confirm"), { error: true, description: errText(e) });
    } finally {
      setBusy(null);
    }
  };
  const at = (ms: number) => new Date(ms).toLocaleTimeString(lang === "zh" ? "zh-CN" : "en", { hour: "2-digit", minute: "2-digit" });
  return (
    <CardFrame label={confirmTitle(c)} leaving={leaving}>
      <CardHead
        icon={c.kind === "invite" ? <Link2 size={16} /> : <KeyRound size={16} />}
        title={confirmTitle(c)}
        tag={c.name}
        summary={t("confirm.asked", { who })}
        open={open}
        setOpen={setOpen}
        keys={
          <>
            <SecondKey disabled={!!busy} onClick={() => void answer(false)}>
              {t("ask.deny")}
            </SecondKey>
            <MainKey busy={busy === "allow"} disabled={!!busy} onClick={() => void answer(true)}>
              {t("confirm.allow")}
            </MainKey>
          </>
        }
      />
      {open && (
        <CardDetails>
          <Facts
            rows={[
              [t("confirm.asker"), c.asker],
              ...(from ? ([[t("confirm.session"), <button type="button" onClick={() => openChat(from.id)} className="text-left text-ink underline decoration-ink-4 underline-offset-2">{titleOf(from)}</button>]] as [string, React.ReactNode][]) : []),
              [t("confirm.until"), at(c.expiresAt)],
            ]}
          />
          <p className="m-0 text-xs text-ink-3">{c.kind === "invite" ? t("confirm.inviteHint") : t("confirm.tokenHint")}</p>
        </CardDetails>
      )}
    </CardFrame>
  );
}
