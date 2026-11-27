// Folding a sidebar group or folder folds any expanded sessions inside it (otae-c, 2026-11-27).
import { Tooltip } from "@base-ui/react/tooltip";
import { QueryClientProvider } from "@tanstack/react-query";
import { act, useState } from "react";
import { createRoot } from "react-dom/client";
import { beforeEach, describe, expect, test, vi } from "vitest";
import { api, queryClient } from "../src/api/client";
import type { Session } from "../src/api/types.gen";
import { Sidebar } from "../src/sidebar";
import { buildTree } from "../src/tree";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const ses = (id: string, cwd: string, updatedAt: number) =>
  ({ id, cwd, updatedAt, agent: "claude", status: "idle", hosted: false, live: false, workspace: 1 }) as Session;

describe("sidebar fold resets expanded sessions", () => {
  beforeEach(() => {
    vi.spyOn(api, "favorites").mockResolvedValue({ channels: [] });
    vi.spyOn(api, "apps").mockResolvedValue({ apps: [], logins: [], invites: [] });
    vi.spyOn(api, "me").mockResolvedValue({ id: "me", host: true } as never);
    vi.spyOn(api, "workspaces").mockResolvedValue([]);
    vi.spyOn(api, "settings").mockResolvedValue({} as never);
  });

  test("folding an open folder or group calls foldBack for itself and nested folders", async () => {
    const sessions = [
      ...Array.from({ length: 8 }, (_, i) => ses(`r${i}`, "/w", 100 - i)),
      ...Array.from({ length: 8 }, (_, i) => ses(`a${i}`, "/w/sub", 200 - i)),
      ...Array.from({ length: 8 }, (_, i) => ses(`b${i}`, "/w/sub/deep", 300 - i)),
    ];
    const tree = buildTree(sessions, [{ id: 1, name: "proj", path: "/w" }], "未归类", 0);
    const foldBack = vi.fn();

    function Harness() {
      const [open, setOpen] = useState<Record<string, boolean>>({});
      const expanded = (k: string) => open[k] ?? k !== "archive";
      const toggle = (k: string) => setOpen((o) => ({ ...o, [k]: !(o[k] ?? k !== "archive") }));
      return (
        <QueryClientProvider client={queryClient}>
          <Tooltip.Provider>
            <Sidebar
              tree={tree}
              expanded={expanded}
              toggle={toggle}
              more={() => 5}
              showMore={() => {}}
              foldBack={foldBack}
              selected=""
              onSelect={() => {}}
              phone={false}
              searching={false}
              search={null}
              onSearch={() => {}}
              confirms={[]}
              onConfirm={() => {}}
              wsPlus={(_w, el) => el}
              me={null}
              chatCode={() => {}}
              panelsCode={() => {}}
            />
          </Tooltip.Provider>
        </QueryClientProvider>
      );
    }

    const host = document.createElement("div");
    document.body.append(host);
    const root = createRoot(host);
    await act(async () => root.render(<Harness />));

    const findRow = (label: string) =>
      [...host.querySelectorAll<HTMLElement>('[role="treeitem"]')].find((el) => el.textContent?.includes(label))!;

    // Folding folder "sub" folds sessions in "sub" and its nested folder "deep".
    const subRow = findRow("sub");
    expect(subRow).toBeTruthy();
    await act(async () => subRow.click());
    expect(foldBack).toHaveBeenCalledTimes(1);
    expect(foldBack).toHaveBeenLastCalledWith(["w1/sub", "w1/sub/deep"]);

    // Expanding "sub" again does NOT call foldBack.
    await act(async () => findRow("sub").click());
    expect(foldBack).toHaveBeenCalledTimes(1);

    // Folding group "proj" folds sessions at the group root and all nested folders.
    await act(async () => findRow("proj").click());
    expect(foldBack).toHaveBeenCalledTimes(2);
    expect(foldBack).toHaveBeenLastCalledWith(["w1", "w1/sub", "w1/sub/deep"]);

    await act(async () => root.unmount());
    host.remove();
  });
});
