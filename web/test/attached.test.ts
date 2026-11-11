import { describe, expect, it } from "vitest";
import type { SessionState } from "../src/api/types.gen";
import { attachedKeys, keyCycle } from "../src/chat/meters";

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

describe("keyCycle", () => {
  const SVG = "data:image/svg+xml;base64,PHN2Zy8+";
  const keys = (on: string) =>
    attachedKeys(
      st([
        {
          place: "effort",
          from: "m:pc",
          content: {
            "": `<button id="u"${on ? " pressed" : ""}>Ultra</button>` + ["wf", "team", "free"].map((x) => `<button id="u.${x}"${x === on ? " pressed" : ""}${x === "team" ? " disabled" : ""}><img src="${x === "wf" ? SVG : "https://x/y.svg"}"/>${x}</button>`).join(""),
          },
        },
      ]),
      "effort",
      "zh",
    );

  it("reads a button's mark, a data: image only", () => {
    expect(keys("").map((k) => k.icon)).toEqual([undefined, SVG, undefined, undefined]);
  });

  it("goes off, each sub-mode not disabled, off", () => {
    const off = keyCycle(keys(""))!;
    expect(off.cur).toBeUndefined();
    expect(off.next.press).toBe("att:s1:m:pc:u.wf");
    expect(off.want).toEqual({ "att:s1:m:pc:u": true, "att:s1:m:pc:u.wf": true });
    const wf = keyCycle(keys("wf"))!;
    expect(wf.cur?.label).toBe("wf");
    expect(wf.next.press).toBe("att:s1:m:pc:u.free"); // team disabled: passed over
    expect(wf.want).toEqual({ "att:s1:m:pc:u": true, "att:s1:m:pc:u.free": true, "att:s1:m:pc:u.wf": false });
    const free = keyCycle(keys("free"))!;
    expect(free.next.press).toBe("att:s1:m:pc:u");
    expect(free.want).toEqual({ "att:s1:m:pc:u": false });
  });

  it("is a switch without sub-modes", () => {
    const one = keyCycle([{ press: "", label: "UltraCode", pressed: false, disabled: false }])!;
    expect(one.next.press).toBe("");
    expect(one.want).toEqual({ "": true });
    expect(keyCycle([])).toBeUndefined();
  });
});
