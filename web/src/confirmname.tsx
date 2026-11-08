// What the sidebar's 需要你 shows of a confirmation (confirms.tsx draws its card): its title, the
// name it is for, its icon. Apart from the cards, so the first screen does not carry them.
import { Bot, KeyRound, Link2, LockKeyhole, Monitor, Terminal as TermIcon } from "lucide-react";
import type { Confirm } from "./api/types.gen";
import { t } from "./i18n";

export const confirmTitle = (c: Confirm) =>
  c.kind === "exec-pair" || c.kind === "bot-pair"
    ? t("confirm.pair", { name: c.name ?? "" })
    : c.kind === "invite" ? t("confirm.invite") : c.kind === "secret-set" ? t("secrets.setTitle") : c.kind === "secret-use" ? t("secrets.useTitle") : t("confirm.token");

/** What a confirmation names: the invite's, token's or secret's name, the secrets a use wants, a
 *  pairing's code. */
export const confirmName = (c: Confirm) => (c.kind === "secret-use" ? (c.secrets ?? []).join(", ") : c.kind === "exec-pair" || c.kind === "bot-pair" ? c.code : c.name);

/** A confirmation's icon, as the sidebar and its card draw it. */
export function ConfirmIcon({ c, size, className }: { c: Confirm; size: number; className?: string }) {
  const I = c.kind === "exec-pair" ? Monitor : c.kind === "bot-pair" ? Bot : c.kind === "invite" ? Link2 : c.kind === "secret-set" ? LockKeyhole : c.kind === "secret-use" ? TermIcon : KeyRound;
  return <I size={size} className={className} />;
}
