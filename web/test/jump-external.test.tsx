// Jump on-demand external search + adopt, deduplicated against sessions already listed in Iridium.
import { QueryClientProvider } from "@tanstack/react-query";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { beforeEach, describe, expect, test, vi } from "vitest";
import { api, queryClient } from "../src/api/client";
import type { Session } from "../src/api/types.gen";
import { Jump } from "../src/jump";
import { setCarried } from "../src/store";
import { buildTree } from "../src/tree";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const ses = (id: string, title: string, cwd = "/work") =>
  ({ id, title, cwd, updatedAt: 100, agent: "dsh", status: "idle", hosted: false, live: false, workspace: 1 }) as Session;

describe("Jump external Desktop session search", () => {
  beforeEach(() => {
    queryClient.clear();
    vi.spyOn(api, "apps").mockResolvedValue({
      apps: [{ id: "m1", name: "otae", type: "computer", online: true, usedToday: 0, computer: { os: "windows", home: "C:\\Users\\otae", sep: "\\", agents: [{ agent: "dsh" }], seenAt: 1 } }],
      logins: [],
      invites: [],
    });
  });

  test("typing queries external sessions on demand, deduplicates known ones, and adopts on click", async () => {
    const existing = [ses("s-known", "DSH rewrite")];
    setCarried("list", existing);
    const tree = buildTree(existing, [{ id: 1, name: "ir", path: "/work" }], "未归类", 0);
    const searchSpy = vi.spyOn(api, "searchExternal").mockResolvedValue([
      { id: "s-known", agent: "dsh", cwd: "/work", title: "DSH rewrite", updatedAt: 100 },
      { id: "s-ext", agent: "dsh", machine: "m1", cwd: "C:/Code/dsh", title: "Windows rewrite debug", updatedAt: 200 },
    ]);
    const adoptSpy = vi.spyOn(api, "adoptExternal").mockResolvedValue({ id: "s-ext", ok: true });
    const close = vi.fn();

    const host = document.createElement("div");
    document.body.append(host);
    const root = createRoot(host);
    await act(async () =>
      root.render(
        <QueryClientProvider client={queryClient}>
          <Jump close={close} tree={tree} open={null} />
        </QueryClientProvider>,
      ),
    );

    // Nothing typed: no external query issued.
    expect(searchSpy).not.toHaveBeenCalled();

    // Type a query and wait past debounce.
    const input = host.querySelector<HTMLInputElement>("input")!;
    await act(async () => {
      const set = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!;
      set.call(input, "rewrite");
      input.dispatchEvent(new Event("input", { bubbles: true }));
    });
    await act(async () => void (await new Promise((r) => setTimeout(r, 260))));
    await act(async () => void (await new Promise((r) => setTimeout(r, 20))));

    expect(searchSpy).toHaveBeenCalledWith("rewrite", 30, expect.anything());
    // Known session is deduplicated; only the unsynced external session shows in the unsynced section.
    const extBtn = host.querySelector<HTMLButtonElement>("#jump-ext-s-ext");
    expect(extBtn).toBeTruthy();
    expect(host.querySelector("#jump-ext-s-known")).toBeNull();
    expect(extBtn!.textContent).toContain("Windows rewrite debug");

    // Clicking adopts and closes Jump.
    await act(async () => extBtn!.click());
    expect(adoptSpy).toHaveBeenCalledWith({
      id: "s-ext",
      agent: "dsh",
      machine: "m1",
      cwd: "C:/Code/dsh",
      title: "Windows rewrite debug",
      updatedAt: 200,
    });
    expect(close).toHaveBeenCalledTimes(1);

    await act(async () => root.unmount());
    host.remove();
  });
});
