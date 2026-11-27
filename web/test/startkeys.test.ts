import { describe, expect, it } from "vitest";
import type { Attached } from "../src/api/types.gen";
import { nextPick, startKeys } from "../src/chat/meters";

const omd: Attached[] = [
  {
    place: "effort",
    from: "",
    content: { zh: '<button id="omd.ultra">Ultra</button><button id="omd.ultra.workflow">工作流</button><button id="omd.ultra.team">团队</button><button id="omd.ultra.free">自由</button>' },
    presets: ["standard"],
  },
];
const claude: Attached[] = [{ place: "effort", from: "", content: { "": '<button id="claude.ultracode">UltraCode</button>' } }];
const id = (x: string) => x;
const pressed = (pick: string, preset = "standard") =>
  startKeys(omd, "effort", "zh", pick, preset, id)
    .keys.filter((k) => k.pressed)
    .map((k) => k.press);

describe("startKeys", () => {
  it("shows a pick: the head pressed with any, the one picked", () => {
    expect(pressed("")).toEqual([]);
    expect(pressed("omd.ultra")).toEqual(["omd.ultra"]);
    expect(pressed("omd.ultra.team")).toEqual(["omd.ultra", "omd.ultra.team"]);
  });
  it("a key for some presets alone: off and its head disabled under another", () => {
    const { keys, fits } = startKeys(omd, "effort", "zh", "omd.ultra.team", "minimal", id);
    expect(fits).toBe(false);
    expect(keys.some((k) => k.pressed)).toBe(false);
    expect(keys[0].disabled).toBe(true);
    expect(startKeys(claude, "effort", "zh", "", "anything", id).fits).toBe(true);
    expect(startKeys(omd, "effort", "zh", "omd.ultra", "", id).fits).toBe(true);
    expect(startKeys(undefined, "effort", "zh", "x", "", id)).toEqual({ keys: [], fits: true });
  });
  it("presses go: off, each sub-mode, off", () => {
    const step = (pick: string) => nextPick(startKeys(omd, "effort", "zh", pick, "standard", id).keys);
    expect(step("")).toBe("omd.ultra.workflow");
    expect(step("omd.ultra.workflow")).toBe("omd.ultra.team");
    expect(step("omd.ultra.team")).toBe("omd.ultra.free");
    expect(step("omd.ultra.free")).toBe("");
    expect(step("omd.ultra")).toBe("");
  });
  it("a key of one button is a switch", () => {
    const step = (pick: string) => nextPick(startKeys(claude, "effort", "en", pick, "", id).keys);
    expect(step("")).toBe("claude.ultracode");
    expect(step("claude.ultracode")).toBe("");
    expect(nextPick([])).toBeUndefined();
  });
  it("presses as told", () => {
    expect(startKeys(claude, "effort", "en", "", "", (x) => `att:s1:ir:${x}`).keys[0].press).toBe("att:s1:ir:claude.ultracode");
  });
});
