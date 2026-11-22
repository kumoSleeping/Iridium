// Roles (src/roles.tsx): a role's line, and what starting as one sends.
import { describe, expect, test } from "vitest";
import type { Model, Role } from "../src/api/types.gen";
import { startRequest } from "../src/create";
import { setLang } from "../src/i18n";
import { effortName, picked, roleLine, roleModel, roleStart } from "../src/roles";

const fish: Role = { id: "r1", name: "大肥鱼", agent: "dsh", model: "deepseek/flash", modelName: "DeepSeek Harness deepseek/flash" };
const started = picked(fish, { effort: "high" });
const models: Model[] = [
  { value: "deepseek/pro", name: "Pro", default: true },
  { value: "deepseek/flash", name: "Flash", effortNames: { ultra: "Ultra" } },
  { value: "alias", name: "Alias", resolved: "deepseek/full-id" },
] as Model[];

describe("a role's line", () => {
  test("agent, model by its kind's name, the effort picked in the page's language", () => {
    setLang("en");
    expect(roleLine(started, models)).toBe("DeepSeek Harness · Flash · High");
    setLang("zh");
    expect(roleLine(started, models)).toBe("DeepSeek Harness · Flash · 高");
    expect(roleLine(started, models, false)).toBe("Flash · 高");
    expect(roleLine(fish, models)).toBe("DeepSeek Harness · Flash");
  });
  test("the models not known yet: the model's id", () => {
    expect(roleModel(fish)).toBe("deepseek/flash");
    expect(roleLine(fish)).toBe("DeepSeek Harness · deepseek/flash");
  });
  test("a model kept by the id it resolves to", () => {
    expect(roleModel({ ...fish, model: "deepseek/full-id" }, models)).toBe("Alias");
  });
  test("what is not set is left out", () => {
    expect(roleLine({ id: "r2", name: "x", agent: "claude" })).toBe("Claude");
  });
  test("an effort the page has no name for: the model's name of it, else as it is", () => {
    expect(effortName("ultra", models[1])).toBe("Ultra");
    expect(effortName("ultra")).toBe("ultra");
  });
});

describe("starting as a role", () => {
  test("unchanged: the role alone, its effort the agent's own", () => {
    expect(roleStart(fish)).toEqual({ role: "r1" });
    expect(roleStart(fish, { model: "", effort: "" })).toEqual({ role: "r1" });
    expect(roleStart(fish, { model: "deepseek/flash" })).toEqual({ role: "r1" });
  });
  test("only what this once changes is sent; an effort picked always (the role keeps none)", () => {
    expect(roleStart(fish, { effort: "high" })).toEqual({ role: "r1", effort: "high" });
    expect(roleStart(fish, { effort: "max" })).toEqual({ role: "r1", effort: "max" });
    expect(roleStart(fish, { model: "deepseek/pro", preset: "plan" })).toEqual({ role: "r1", model: "deepseek/pro", preset: "plan" });
  });
  test("the role as it would start", () => {
    expect(picked(fish, { effort: "max", model: "" })).toMatchObject({ model: "deepseek/flash", effort: "max" });
  });
  test("a new session's request: the role's agent in the folder, with the role", () => {
    expect(startRequest("dsh", "/home/x", 3, undefined, ["ir_web/*"], roleStart(fish, { effort: "low" }))).toEqual({ agent: "dsh", cwd: "/home/x", workspace: 3, members: { people: ["ir_web/*"] }, role: "r1", effort: "low" });
    expect(startRequest("dsh", "C:\\Code", undefined, "m1", undefined, roleStart(fish))).toEqual({ agent: "dsh", cwd: "C:\\Code", machine: "m1", role: "r1" });
  });
  test("with no role, as before", () => {
    expect(startRequest("claude", "~")).toEqual({ agent: "claude", cwd: "~" });
  });
});
