// What the sidebar's 需要你 shows of a need (confirms.tsx draws its card): its title, the name it
// is for, its icon. Apart from the cards, so the first screen does not carry them.
import { Bot, KeyRound, Link2, LockKeyhole, Monitor, RefreshCw, SlidersHorizontal, Terminal as TermIcon, UserPlus } from "lucide-react";
import type { Need } from "./api/types.gen";
import { t } from "./i18n";

/** A pairing need: an app asking to join, or to be paired again by a name already there. */
export const isPair = (c: Need) => c.kind === "app_join" || c.kind === "app_update";

export const confirmTitle = (c: Need) => {
  const name = c.name ?? "";
  switch (c.kind) {
    case "app_join":
      return t("confirm.pair", { name });
    case "app_update":
      return t("confirm.update", { name });
    case "app_change":
      return t("confirm.change", { name });
    case "login_join":
      return t("confirm.login", { name: `${c.app?.name ?? ""}/${name}` });
    case "invite":
      return t("confirm.invite");
    case "secret_set":
      return t("secrets.setTitle");
    case "secret_use":
      return t("secrets.useTitle");
  }
  return t("confirm.token");
};

/** What a need names: the invite's or secret's name, the secrets a use wants, a pairing's code. */
export const confirmName = (c: Need) => (c.kind === "secret_use" ? (c.secrets ?? []).join(", ") : isPair(c) ? c.code : c.kind === "login_join" ? undefined : c.name);

/** A need's icon, as the sidebar and its card draw it. */
export function ConfirmIcon({ c, size, className }: { c: Need; size: number; className?: string }) {
  const I = isPair(c)
    ? c.kind === "app_update"
      ? RefreshCw
      : c.pair?.os
        ? Monitor
        : Bot
    : c.kind === "app_change"
      ? SlidersHorizontal
      : c.kind === "login_join"
        ? UserPlus
        : c.kind === "invite"
          ? Link2
          : c.kind === "secret_set"
            ? LockKeyhole
            : c.kind === "secret_use"
              ? TermIcon
              : KeyRound;
  return <I size={size} className={className} />;
}
