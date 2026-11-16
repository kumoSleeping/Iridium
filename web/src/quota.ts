// A quota window's names: its key, for hiding it in the settings (agent, account, window), and its
// name in the page's language.
import type { Agent, QuotaWindow } from "./api/types.gen";
import { t } from "./i18n";
import { agentName } from "./ui";

/** A quota window's key, for hiding it in the settings: agent, account, window. */
export const quotaKey = (q: { agent: Agent; account?: string }, w: QuotaWindow) => [q.agent, q.account ?? "", w.group, w.model ?? ""].join(":");

const bare = (s: string) => s.toLowerCase().replace(/[^a-z0-9]/g, "");
/** Whether window w is this session's: one of all its models (no model), Claude's Fable window
 *  (always, the user, 2026-11-15), or of the models it uses (one of a window shared by several,
 *  Antigravity's "Claude · GPT-OSS", the user, 2026-11-16). using: what the
 *  session names the model it uses (its id, its pick, its name in the menu); none known, no
 *  model's window. Matched loosely, the model's name holding the window's: "Fable" is
 *  claude-fable-5-1's, "GPT-5.3-Codex-Spark" gpt-5.3-codex-spark's (not gpt-5.3-codex's). */
export function windowFor(w: QuotaWindow, using: (string | undefined)[]): boolean {
  if (!w.model) return true;
  if (bare(w.model) === "fable") return true;
  return w.model.split(" · ").some((name) => {
    const m = bare(name);
    return !!m && using.some((u) => {
      const b = u ? bare(u) : "";
      return !!b && b.includes(m);
    });
  });
}

/** A window's name: the agent's, its span, the models it is for ("Claude 7 天 Fable"); Antigravity's
 *  windows are its models' families' own, by them and the span ("Gemini 5 小时", the user, 2026-11-16). */
export function windowName(agent: Agent, w: QuotaWindow): string {
  const base = w.group === "session" ? t("quota.session") : w.group === "weekly" ? t("quota.weekly") : w.group;
  if (agent === "agy" && w.model) return `${w.model} ${base}`;
  return `${agentName(agent)} ${base}${w.model ? ` ${w.model}` : ""}`;
}
