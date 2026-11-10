// The "+" of a folder row: a subfolder made beside the folder's line, then an agent picked.
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, test } from "vitest";
import type { Machine } from "../src/machines";
import { NewSession, retarget, subName } from "../src/create";

const page = (onMake?: (n: string) => Promise<boolean>, folder = "/home/u/ir/web") =>
  renderToStaticMarkup(
    <QueryClientProvider client={new QueryClient()}>
      <NewSession ws="ir" folder={folder} busy={null} onFolder={() => {}} onPick={() => {}} onMake={onMake} />
    </QueryClientProvider>,
  );
const key = /aria-label="(新建子文件夹|New subfolder)"/;
const win = { id: "m1", name: "otae-2", os: "windows", home: "C:\\Users\\o", sep: "\\", agents: [{ agent: "dsh" }], online: true } as Machine;

describe("subfolder", () => {
  test("a name, not a path", () => {
    expect(subName("  api-v2 ")).toBe("api-v2");
    for (const v of ["", "  ", ".", "..", "a/b", "a\\b", "a\0b"]) expect(subName(v)).toBeNull();
  });
  test("its key only where it can be made", () => {
    expect(page()).not.toMatch(/新建子文件夹|New subfolder/);
    expect(page(async () => true)).toMatch(key);
  });
  test("none at a machine no more there: its line says so", () => {
    const gone = page(async () => true, "m9:/root");
    expect(gone).not.toMatch(key);
    expect(gone).toMatch(/已移除的应用|Removed app/);
  });
  test("an agent at the folder's machine, picked while it was made, opens in the one made", () => {
    const to = { m: win, cwd: "C:\\Code" };
    expect(retarget("m1:C:/Code/api", to, [win])).toEqual({ m: win, cwd: "C:\\Code\\api" });
    expect(retarget("/home/u/ir/api", undefined, [win])).toBeUndefined();
  });
});
