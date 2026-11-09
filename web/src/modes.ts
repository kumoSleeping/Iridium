// The approval modes' names in the page's language (the mode key, the settings' defaults); a mode
// not known here shows as the agent calls it.
import { t } from "./i18n";

const MODE_KEY: Record<string, Parameters<typeof t>[0]> = {
  default: "mode.default",
  manual: "mode.default",
  ask: "mode.default",
  acceptEdits: "mode.acceptEdits",
  plan: "mode.plan",
  auto: "mode.auto",
  dontAsk: "mode.dontAsk",
  bypassPermissions: "mode.bypass",
  untrusted: "mode.untrusted",
  "on-request": "mode.onRequest",
  granular: "mode.granular",
  never: "mode.never",
  "read-only": "mode.readOnly",
  "workspace-write": "mode.workspaceWrite",
  "danger-full-access": "mode.fullAccess",
};

export const modeName = (m: { id: string }) => (MODE_KEY[m.id] ? t(MODE_KEY[m.id]!) : m.id);
