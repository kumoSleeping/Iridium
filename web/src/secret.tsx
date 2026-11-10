// The secret's card (src/secrets.ts): an invite's link and its QR code, or a token, with a copy
// key, said to be shown once. In the page where it was made or allowed, among the cards there (the
// chat's, above the composer; the 终端 page's), not a dialog over everything (the user,
// 2026-11-06). Closing it forgets it.
import { Check, Copy, KeyRound, Link2, X } from "lucide-react";
import { useState } from "react";
import { CardFrame } from "./card";
import { t, useLang } from "./i18n";
import { showSecret, useSecret, type Secret } from "./secrets";
import { cn, IconKey } from "./ui";

/** The secret made or allowed here (at: a session's id, or "terminals"), if there is one. */
export function SecretHere({ at }: { at: string }) {
  const secret = useSecret();
  return secret?.at === at ? <SecretCard key={secret.key} secret={secret} /> : null;
}

function SecretCard({ secret }: { secret: Secret }) {
  const lang = useLang();
  const [copied, setCopied] = useState(false);
  const [leaving, setLeaving] = useState(false);
  const invite = "invite" in secret ? secret.invite : null;
  const token = "token" in secret ? secret.token : null;
  const value = invite?.url ?? token?.token ?? "";
  const copy = () => void navigator.clipboard?.writeText(value).then(() => (setCopied(true), setTimeout(() => setCopied(false), 1500)));
  const until = invite ? new Date(invite.expiresAt).toLocaleTimeString(lang === "zh" ? "zh-CN" : "en", { hour: "2-digit", minute: "2-digit" }) : "";
  const close = () => (setLeaving(true), setTimeout(() => showSecret(null), 140));
  const title = invite ? t("secret.invite") : t("secret.token");
  return (
    <CardFrame label={title} leaving={leaving}>
      <div className="flex items-start gap-2.5">
        <span className="mt-0.5 shrink-0 text-ink-2">{invite ? <Link2 size={16} /> : <KeyRound size={16} />}</span>
        <div className="flex min-w-0 flex-1 flex-col gap-2">
          <div className="flex min-h-6 items-center gap-1.5 text-sm font-semibold">
            <span className="truncate">{title}</span>
            {(invite?.name || token?.app.name) && <span className="min-w-0 flex-1 truncate font-normal text-ink-3">{invite?.name || token?.app.name}</span>}
            <IconKey label={t("secret.done")} onClick={close} className="-my-1 -mr-1 ml-auto size-7 shrink-0">
              <X size={15} />
            </IconKey>
          </div>
          {/* The QR beside the link on a wide card, above it on a narrow one. */}
          <div className="flex flex-col items-start gap-3 @sm:flex-row">
            {invite?.qr && <img src={invite.qr} alt={t("secret.qr")} width={132} height={132} className="shrink-0 rounded-md bg-white p-1.5 [image-rendering:pixelated]" />}
            <div className="flex w-full min-w-0 flex-col gap-1.5">
              <div className="flex items-start gap-1.5 rounded-md bg-fill px-2.5 py-2">
                <code className="min-w-0 flex-1 font-mono text-xs leading-5 break-all text-ink select-all" data-secret>
                  {value}
                </code>
                <button type="button" onClick={copy} aria-label={t("msg.copy")} className={cn("g-press -my-0.5 grid size-7 shrink-0 place-items-center rounded-md hover:bg-fill-2", copied ? "text-ink" : "text-ink-2")}>
                  {copied ? <Check size={15} /> : <Copy size={15} />}
                </button>
              </div>
              <p className="m-0 text-xs text-ink-3">{invite ? t("secret.inviteHint", { time: until }) : t("secret.tokenHint")}</p>
            </div>
          </div>
        </div>
      </div>
    </CardFrame>
  );
}
