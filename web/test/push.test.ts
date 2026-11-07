// The push rules as the menus read them: the same as internal/push Resolve.
import { expect, test } from "vitest";
import type { PushState } from "../src/api/types.gen";
import { folderLevels, pushEvents, pushOn, wsLevels } from "../src/push";

const st: PushState = {
  enabled: true,
  publicKey: "",
  defaults: { needsYou: true, error: false, turnEnd: false },
  rules: [
    { scope: "workspace", target: "2", mode: "on", events: { needsYou: false, error: false, turnEnd: true } },
    { scope: "folder", target: "/w2/hush", mode: "off" },
    { scope: "session", target: "back", mode: "on" },
    { scope: "workspace", target: "1", mode: "off" },
  ],
};

test("the nearest rule says on or off; the events, the nearest on with its own", () => {
  const back = [{ scope: "session" as const, target: "back" }, ...folderLevels("/w2/hush", 2)];
  expect(pushOn(st, wsLevels(0))).toBe(true);
  expect(pushEvents(st, wsLevels(0))).toEqual(st.defaults);
  expect(pushOn(st, wsLevels(1))).toBe(false);
  expect(pushOn(st, folderLevels("/w2/hush", 2))).toBe(false);
  expect(pushOn(st, back)).toBe(true);
  expect(pushEvents(st, back).turnEnd).toBe(true);
  expect(pushOn(st, folderLevels("/w1/x", 1))).toBe(false);
});
