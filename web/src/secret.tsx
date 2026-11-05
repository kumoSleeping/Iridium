// The secret's dialog (src/secrets.ts): an invite's link and its QR code, or a token, with a copy
// key, said to be shown once. Closing it forgets it. Loaded only when there is one.
import { Dialog } from "@base-ui/react/dialog";
import { Check, Copy, KeyRound, Link2 } from "lucide-react";
import { useState } from "react";
import { t, useLang } from "./i18n";
import { showSecret, type Secret } from "./secrets";
import { cn } from "./ui";

export function SecretDialog({ secret }: { secret: Secret }) {
  const lang = useLang();
  const [copied, setCopied] = useState(false);
  const invite = "invite" in secret ? secret.invite : null;
  const token = "token" in secret ? secret.token : null;
  const value = invite?.url ?? token?.token ?? "";
  const copy = () => void navigator.clipboard?.writeText(value).then(() => (setCopied(true), setTimeout(() => setCopied(false), 1500)));
  const until = invite ? new Date(invite.expiresAt).toLocaleTimeString(lang === "zh" ? "zh-CN" : "en", { hour: "2-digit", minute: "2-digit" }) : "";
  return (
    <Dialog.Root open onOpenChange={(o) => !o && showSecret(null)}>
      <Dialog.Portal>
        <Dialog.Backdrop className="fixed inset-0 z-60 bg-ink/15 transition-opacity duration-(--dur-2) data-ending-style:opacity-0 data-starting-style:opacity-0" />
        <Dialog.Popup className="g-unfold glass thick float fixed top-1/2 left-1/2 z-60 flex w-[min(360px,calc(100vw-24px))] -translate-x-1/2 -translate-y-1/2 flex-col gap-3 rounded-xl p-4 text-ink outline-none">
          <div className="flex items-center gap-2.5">
            <span className="text-ink-2">{invite ? <Link2 size={17} /> : <KeyRound size={17} />}</span>
            <Dialog.Title className="m-0 min-w-0 flex-1 truncate text-base font-semibold">
              {invite ? t("secret.invite") : t("secret.token")}
              {(invite?.name || token?.terminal.name) && <span className="ml-1.5 font-normal text-ink-3">{invite?.name || token?.terminal.name}</span>}
            </Dialog.Title>
          </div>
          {invite?.qr && (
            <div className="grid place-items-center">
              <img src={invite.qr} alt={t("secret.qr")} width={196} height={196} className="rounded-md bg-white p-2 [image-rendering:pixelated]" />
            </div>
          )}
          <div className="flex items-start gap-1.5 rounded-md bg-fill px-2.5 py-2">
            <code className="min-w-0 flex-1 font-mono text-xs leading-5 break-all text-ink select-all" data-secret>
              {value}
            </code>
            <button type="button" onClick={copy} aria-label={t("msg.copy")} className={cn("g-press -my-0.5 grid size-7 shrink-0 place-items-center rounded-md hover:bg-fill-2", copied ? "text-ink" : "text-ink-2")}>
              {copied ? <Check size={15} /> : <Copy size={15} />}
            </button>
          </div>
          <Dialog.Description className="m-0 text-xs text-ink-3">{invite ? t("secret.inviteHint", { time: until }) : t("secret.tokenHint")}</Dialog.Description>
          <div className="flex justify-end">
            <Dialog.Close className="g-press h-8 rounded-md bg-ink px-3.5 text-sm font-medium text-on-ink">{t("secret.done")}</Dialog.Close>
          </div>
        </Dialog.Popup>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
