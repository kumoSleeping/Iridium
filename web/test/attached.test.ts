import { describe, expect, it } from "vitest";
import type { SessionState } from "../src/api/types.gen";
import { attachedKeys } from "../src/chat/meters";

const st = (attached: SessionState["attached"]) => ({ id: "s1", attached }) as SessionState;

describe("attachedKeys", () => {
  it("reads the buttons of the piece at a place, in the page's language", () => {
    const s = st([
      { place: "menu", from: "a:x", content: { "": '<button id="no">No</button>' } },
      {
        place: "effort",
        from: "m:pc",
        content: {
          zh: '<button id="omd.ultra" type="action" pressed title="协作 &amp; 编排">Ultra</button><button id="omd.ultra.free" disabled>自由</button>',
          en: '<button id="omd.ultra">Ultra</button>',
        },
      },
    ]);
    expect(attachedKeys(s, "effort", "zh")).toEqual([
      { press: "att:s1:m:pc:omd.ultra", label: "Ultra", pressed: true, disabled: false, title: "协作 & 编排" },
      { press: "att:s1:m:pc:omd.ultra.free", label: "自由", pressed: false, disabled: true, title: undefined },
    ]);
    expect(attachedKeys(s, "effort", "en")[0].pressed).toBe(false);
    expect(attachedKeys(s, "effort", "fr")[0].pressed).toBe(true); // the first, none for it
    expect(attachedKeys(s, "row", "zh")).toEqual([]);
  });
});
