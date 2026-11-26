// An ask for manual approval (session/manual.go) drawn as any ask above the composer: its title,
// the agent's reason, how long at most, agree or refuse; no tool input, no words to send back.
import { act, type ReactNode } from "react";
import { createRoot } from "react-dom/client";
import { expect, test } from "vitest";
import type { Ask } from "../src/api/types.gen";
import { Asks } from "../src/chat/asks";
import { t } from "../src/i18n";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

function draw(node: ReactNode): HTMLDivElement {
  const host = document.createElement("div");
  document.body.append(host);
  act(() => createRoot(host).render(node));
  return host;
}

test("a manual ask: its reason, its steps, agree or refuse", () => {
  const a: Ask = { requestId: "manual-1", kind: "manual", tool: "", input: { reason: "npx is needed to build", steps: 3 }, description: "npx is needed to build" };
  const el = draw(<Asks session="s1" asks={[a]} cwd="/w" />);
  const text = el.textContent ?? "";
  for (const s of [t("ask.manual"), "npx is needed to build", t("ask.manualSteps", { n: 3 }), t("ask.agree"), t("ask.refuse")]) expect(text).toContain(s);
  expect(text).not.toContain(t("ask.always"));
  expect(el.querySelector("input")).toBeNull();
});
