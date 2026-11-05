// A quota window's names: its key, for hiding it in the settings (agent, account, window), and its
// name in the page's language.
import type { Agent, QuotaWindow } from "./api/types.gen";
import { t } from "./i18n";

/** A quota window's key, for hiding it in the settings: agent, account, window. */
export const quotaKey = (q: { agent: Agent; account?: string }, w: QuotaWindow) => [q.agent, q.account ?? "", w.group, w.model ?? ""].join(":");

export function windowName(agent: Agent, w: QuotaWindow): string {
  const base = w.group === "session" ? t("quota.session") : w.group === "weekly" ? t("quota.weekly") : w.group;
  return `${agent === "claude" ? "Claude" : agent === "codex" ? "Codex" : agent} ${base}${w.model ? ` ${w.model}` : ""}`;
}
