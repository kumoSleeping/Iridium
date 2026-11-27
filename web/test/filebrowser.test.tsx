// File browser list (src/filebrowser.tsx): large folders draw only rows near the viewport in
// natural order with folders first, keep keyboard focus across scrolling and repeated .. climbs,
// and shut an open row menu on scroll or when a refresh removes its row.
import { Dialog } from "@base-ui/react/dialog";
import { QueryClientProvider } from "@tanstack/react-query";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, expect, test, vi } from "vitest";
import { api, queryClient } from "../src/api/client";
import type { DirEntry, DirListing } from "../src/api/types.gen";
import { LOCAL_ME } from "../src/data";
import FileBrowser from "../src/filebrowser";
import { FileWindow } from "../src/file-window";
import { t } from "../src/i18n";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const entry = (name: string, type = "file"): DirEntry => ({ name, type, size: 1, mtime: 1 });
const largeFolder = [
  ...Array.from({ length: 144 }, (_, i) => entry(`file ${144 - i}.png`)),
  ...Array.from({ length: 12 }, (_, i) => entry(`folder ${12 - i}`, "dir")),
];
const folderKey = (path: string) => ["browse-files", "", path];
let host: HTMLDivElement;
let root: Root;

function draw(path: string, directories: Record<string, DirEntry[]>) {
  const listing = (path: string): DirListing => ({ path, entries: directories[path] ?? [] });
  vi.spyOn(api, "browseFiles").mockImplementation(async (path) => listing(path));
  vi.spyOn(api, "browseFileURL").mockReturnValue("data:image/gif;base64,R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7");
  queryClient.setQueryData(["me"], LOCAL_ME);
  queryClient.setQueryData(["apps"], { apps: [] });
  for (const dir of Object.keys(directories)) queryClient.setQueryData(folderKey(dir), listing(dir));
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
  act(() => root.render(
    <QueryClientProvider client={queryClient}>
      <Dialog.Root open>
        <FileWindow>
          <FileBrowser machine="" path={path} />
        </FileWindow>
      </Dialog.Root>
    </QueryClientProvider>,
  ));
  const list = document.querySelector<HTMLUListElement>("ul[role=list]")!;
  const pane = list.parentElement!;
  // Happy DOM doesn't lay out the dialog. Give its scroll pane ten rows' worth of room.
  Object.defineProperty(pane, "clientHeight", { value: 360 });
  return { list, pane };
}

function button(scope: ParentNode, label: string) {
  const key = Array.from(scope.querySelectorAll("button")).find((key) =>
    (key.getAttribute("aria-label") ?? key.textContent?.trim()) === label,
  );
  if (!key) throw new Error(`No button named "${label}"`);
  return key;
}

async function scrollTo(pane: HTMLElement, top: number) {
  await act(async () => {
    pane.scrollTop = top;
    pane.dispatchEvent(new Event("scroll"));
    await new Promise<void>((done) => requestAnimationFrame(() => done()));
  });
}

afterEach(() => {
  if (root) act(() => root.unmount());
  host?.remove();
  queryClient.clear();
  vi.restoreAllMocks();
});

test("a long folder draws just the nearby rows, in natural order with folders first", async () => {
  const { list, pane } = draw("/top", { "/top": largeFolder });
  const labels = () => Array.from(list.querySelectorAll("button"), (row) => row.textContent);
  expect(list.children.length).toBeLessThan(50);
  expect(labels().slice(1, 23)).toEqual([
    ...Array.from({ length: 12 }, (_, i) => `folder ${i + 1}`),
    ...Array.from({ length: 10 }, (_, i) => `file ${i + 1}.png`),
  ]);
  await scrollTo(pane, 82 * 36);
  expect(list.children.length).toBeLessThan(50);
  expect(labels()).not.toContain("folder 1");
  const later = button(list, "file 70.png").closest("li");
  expect(later?.getAttribute("aria-setsize")).toBe(String(largeFolder.length + 1));
  expect(later?.getAttribute("aria-posinset")).toBe("83");
});

test("scrolling keeps the focused row and going up resets the view without breaking keyboard focus", async () => {
  const { list, pane } = draw("/top/mid/deep", {
    "/top/mid/deep": largeFolder, "/top/mid": [entry("mid.png")], "/top": [entry("top.png")],
  });
  await scrollTo(pane, 0); // Let the dialog place its initial focus before moving it to a row.
  expect(button(list, t("browse.up"))).toBe(list.firstElementChild?.firstElementChild);
  const file = button(list, "file 2.png");
  act(() => file.focus());
  await scrollTo(pane, 82 * 36);
  expect(document.activeElement).toBe(file);
  await scrollTo(pane, 0);
  const up = button(list, t("browse.up"));
  act(() => up.focus());
  await scrollTo(pane, 82 * 36);

  // A native button's Enter produces a click with detail 0 (Happy DOM doesn't synthesize it).
  const activate = () => act(() => {
    document.activeElement?.dispatchEvent(new MouseEvent("click", { bubbles: true, detail: 0 }));
  });
  activate();
  expect(pane.scrollTop).toBe(0);
  expect(button(list, "mid.png")).toBeTruthy();
  expect(document.activeElement).toBe(up);
  activate();
  expect(button(list, "top.png")).toBeTruthy();
  expect(document.activeElement).toBe(up);
});

test("a file switches the preview, and an open row menu closes on scroll or removal", async () => {
  const dirs = { "/top": largeFolder };
  const { list, pane } = draw("/top", dirs);
  act(() => button(list, "file 1.png").click());
  expect(document.querySelector("img[alt='file 1.png']")).not.toBeNull();
  const next = button(list, "file 2.png");
  act(() => next.click());
  expect(document.querySelector("img[alt='file 2.png']")).not.toBeNull();
  expect(document.querySelector("img[alt='file 1.png']")).toBeNull();

  const menu = () => document.querySelector("[role=menu][aria-label='file 2.png']");
  const openMenu = () => act(() => next.dispatchEvent(new MouseEvent("contextmenu", { bubbles: true, cancelable: true })));
  openMenu();
  expect(menu()).not.toBeNull();
  await scrollTo(pane, 36);
  expect(menu()).toBeNull();

  openMenu();
  expect(menu()).not.toBeNull();
  dirs["/top"] = largeFolder.filter(({ name }) => name !== "file 2.png");
  await act(async () => {
    button(document, t("top.reload")).click();
    await new Promise((done) => setTimeout(done, 15));
  });
  expect(Array.from(list.querySelectorAll("button"), (row) => row.textContent)).not.toContain("file 2.png");
  expect(menu()).toBeNull();
});
