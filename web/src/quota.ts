// A quota window's names: its key, for hiding it in the settings (agent, account, window), and its
// name in the page's language.
import type { Agent, QuotaWindow } from "./api/types.gen";
import { t } from "./i18n";

/** A quota window's key, for hiding it in the settings: agent, account, window. */
export const quotaKey = (q: { agent: Agent; account?: string }, w: QuotaWindow) => [q.agent, q.account ?? "", w.group, w.model ?? ""].join(":");

const bare = (s: string) => s.toLowerCase().replace(/[^a-z0-9]/g, "");
/** Whether window w is this session's: one of all its models (no model), or of the one model it
 *  uses (Claude's Fable window only where Fable is in use, the user, 2026-11-06). using: what the
 *  session names the model it uses (its id, its pick, its name in the menu); none known, no
 *  model's window. Matched loosely, the model's name holding the window's: "Fable" is
 *  claude-fable-5-1's, "GPT-5.3-Codex-Spark" gpt-5.3-codex-spark's (not gpt-5.3-codex's). */
export function windowFor(w: QuotaWindow, using: (string | undefined)[]): boolean {
  if (!w.model) return true;
  const m = bare(w.model);
  return !!m && using.some((u) => {
    const b = u ? bare(u) : "";
    return !!b && b.includes(m);
  });
}

export function windowName(agent: Agent, w: QuotaWindow): string {
  const base = w.group === "session" ? t("quota.session") : w.group === "weekly" ? t("quota.weekly") : w.group;
  return `${agent === "claude" ? "Claude" : agent === "codex" ? "Codex" : agent} ${base}${w.model ? ` ${w.model}` : ""}`;
}
