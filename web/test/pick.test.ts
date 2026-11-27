// The invite menu's models at once (kumo, 2026-11-15): what was asked last kept in this browser,
// answered with before the server is; a fresh answer kept over it.
import { afterEach, describe, expect, test, vi } from "vitest";
import { api } from "../src/api/client";
import { draftEffort, draftName, lastEffort, rememberEffort } from "../src/chat/choices";
import { choicesQ, defaultFirst, draftDefault } from "../src/chat/channel/pick";

afterEach(() => (localStorage.clear(), vi.restoreAllMocks()));

describe("an agent kind's choices", () => {
  test("none kept: nothing at first; asked, kept for the next time", async () => {
    const q = choicesQ("claude", { channel: "c1" });
    expect(q.initialData()).toBeUndefined();
    const got = { models: [{ value: "haiku", name: "Haiku" }], efforts: ["low"] };
    vi.spyOn(api, "agentChoices").mockResolvedValue(got as never);
    expect(await q.queryFn()).toEqual(got);
    expect(choicesQ("claude", { channel: "c1" }).initialData()).toEqual(got);
    // Another place, another kind: their own.
    expect(choicesQ("claude", { channel: "c2" }).initialData()).toBeUndefined();
    expect(choicesQ("pi", { channel: "c1" }).initialData()).toBeUndefined();
  });
  test("an answer with no models is not kept over one that has them", async () => {
    const got = { models: [{ value: "haiku", name: "Haiku" }], efforts: [] };
    vi.spyOn(api, "agentChoices").mockResolvedValueOnce(got as never).mockResolvedValueOnce({ models: [], efforts: [] } as never);
    const q = choicesQ("claude", {});
    await q.queryFn();
    await q.queryFn();
    expect(q.initialData()).toEqual(got);
  });
});

describe("a new agent's model", () => {
  const models = [
    { value: "a/x", name: "X", group: "A" },
    { value: "b/y", name: "Y", group: "B" },
    { value: "b/z", name: "Z", group: "B", default: true },
  ];
  test("the agent's own default: named, sent as none; first in the menu, its group with it", () => {
    const d = draftDefault({ models, efforts: [] });
    expect([d.def?.name, d.send]).toEqual(["Z", ""]);
    expect(defaultFirst(models, d.def).map((m) => m.value)).toEqual(["b/z", "b/y", "a/x"]);
  });
  test("no default named: the model its kind last ran, sent by its value; none known: nothing", () => {
    const plain = models.map(({ default: _, ...m }) => m);
    const d = draftDefault({ models: plain, efforts: [], ran: "b/y" });
    expect([d.def?.name, d.send]).toEqual(["Y", "b/y"]);
    expect(draftDefault({ models: plain, efforts: [] })).toEqual({ def: undefined, send: "" });
    expect(draftDefault(undefined)).toEqual({ def: undefined, send: "" });
    expect(defaultFirst(plain, undefined)).toBe(plain);
    // The kind's row: its name and that model; not known, the name alone.
    expect(draftName("pi", { models: plain, efforts: [], ran: "b/y" })).toBe("pi · Y");
    expect(draftName("pi", undefined)).toBe("pi");
  });
});

describe("remembered starting effort", () => {
  const asked = {
    models: [
      { value: "opus", name: "Opus", resolved: "claude-opus-5-5", default: true, efforts: ["low", "medium", "high", "xhigh", "max"] },
      { value: "sonnet", name: "Sonnet", efforts: ["low", "medium", "high", "max"] },
      { value: "haiku", name: "Haiku", noEffort: true },
    ],
    efforts: ["low", "medium", "high"],
  };
  test("kept per agent kind in this browser", () => {
    expect(lastEffort("claude")).toBe("");
    rememberEffort("claude", "high");
    expect(lastEffort("claude")).toBe("high");
    expect(lastEffort("codex")).toBe("");
    rememberEffort("claude", "medium");
    expect(lastEffort("claude")).toBe("medium");
    rememberEffort("claude", "");
    expect(lastEffort("claude")).toBe("medium");
  });
  test("sent only when the target model takes that level", () => {
    expect(draftEffort(asked, "", "high")).toBe("high");
    expect(draftEffort(asked, "claude-opus-5-5", "xhigh")).toBe("xhigh");
    expect(draftEffort(asked, "sonnet", "xhigh")).toBe("");
    expect(draftEffort(asked, "haiku", "high")).toBe("");
    expect(draftEffort(undefined, "", "high")).toBe("high");
    expect(draftEffort(asked, "", "")).toBe("");
  });
});
