// The "+" of a group or folder row: each opening on the row's own folder, not the subfolder made
// from it last time, nor one made late for an earlier opening.
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

const mkdir = vi.fn();
vi.mock("../src/api/client", async (orig) => {
  const m = await orig<typeof import("../src/api/client")>();
  return {
    ...m,
    api: {
      ...m.api,
      mkdir: (...a: unknown[]) => mkdir(...a),
      agents: async () => [{ agent: "claude" }, { agent: "codex" }, { agent: "pi" }, { agent: "dsh" }],
      folders: async (path: string) => ({ path, dirs: [] }),
      apps: async () => ({ apps: [], logins: [], invites: [] }),
      me: async () => ({ id: "local", name: "host", app: "local", host: true, rules: {}, usedToday: 0, public: false }),
    },
  };
});
const { WsPlus } = await import("../src/create");
const { t } = await import("../src/i18n");

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLDivElement;
let root: Root;
const qc = new QueryClient();
const w = { id: "w1", name: "a", path: "/srv/a" };
const show = (open: boolean) =>
  act(() =>
    root.render(
      <QueryClientProvider client={qc}>
        <WsPlus w={w as never} trigger={<button type="button">+</button>} open={open} setOpen={() => {}} opened={() => {}} />
      </QueryClientProvider>,
    ),
  );
const line = () => document.querySelector<HTMLButtonElement>(`button[aria-label="${t("new.folder")}"]`)?.textContent ?? "";
/** The new-subfolder key pressed, the name typed, Enter. */
const make = async (name: string) => {
  const key = document.querySelector<HTMLButtonElement>(`button[aria-label="${t("new.subFolder")}"]`);
  expect(key).toBeTruthy();
  await act(async () => key!.click());
  const input = document.querySelector<HTMLInputElement>("input")!;
  await act(async () => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(input, name);
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
  await act(async () => void input.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true })));
};

beforeEach(() => {
  mkdir.mockReset();
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
});

describe("a row's +", () => {
  test("opens again on its own folder, not the subfolder made last time", async () => {
    mkdir.mockResolvedValue({ path: "/srv/a/x" });
    await show(true);
    expect(line()).toMatch(/\/srv\/a$/);
    await make("x");
    expect(mkdir).toHaveBeenCalledWith({ path: "/srv/a", name: "x" });
    expect(line()).toMatch(/\/srv\/a\/x$/);
    await show(false);
    await show(true);
    expect(line()).toMatch(/\/srv\/a$/);
  });
  test("a subfolder made late, for an opening closed since, is not the folder of the next", async () => {
    let done!: (v: unknown) => void;
    mkdir.mockReturnValue(new Promise((r) => (done = r)));
    await show(true);
    await make("x");
    await show(false);
    await show(true);
    await act(async () => done({ path: "/srv/a/x" }));
    expect(line()).toMatch(/\/srv\/a$/);
  });
});
