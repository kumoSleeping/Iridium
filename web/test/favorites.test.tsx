import { act } from "react";
import { createRoot } from "react-dom/client";
import { beforeEach, describe, expect, test, vi } from "vitest";
import { api, queryClient } from "../src/api/client";
import { clearFavorites, favoriteAt, inOpenFavorites, setFavorite, setFavoritesName, useFavorites, useFavoritesName } from "../src/favorites";
import { setCarried } from "../src/store";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

describe("favorites", () => {
  beforeEach(() => {
    localStorage.clear();
    queryClient.removeQueries({ queryKey: ["favorites"] });
  });

  // kumo, 2026-11-26: reloaded, the block came a moment after the list, the groups pushed down.
  test("reloaded: the list kept on the device drawn at once, then asked again", async () => {
    localStorage.setItem("favorites", JSON.stringify({ channels: ["A", "B"] }));
    const got = vi.spyOn(api, "favorites").mockResolvedValue({ channels: ["B"] });
    const seen: string[][] = [];
    function Probe() {
      seen.push(useFavorites());
      return null;
    }
    const host = document.createElement("div");
    document.body.append(host);
    await act(async () => createRoot(host).render(<Probe />));
    await act(async () => void (await new Promise((r) => setTimeout(r, 10))));
    expect(seen[0]).toEqual(["A", "B"]);
    expect(got).toHaveBeenCalled();
    expect(seen.at(-1)).toEqual(["B"]);
    expect(JSON.parse(localStorage.getItem("favorites")!)).toEqual({ channels: ["B"] });
  });

  // otae-c, 2026-11-26: a favourite opened opened its own folder far below as well.
  test("one opened from the favourites in sight: its own group left as it is", () => {
    expect(inOpenFavorites("a", true, ["a", "b"])).toBe(true);
    expect(inOpenFavorites("a", false, ["a", "b"])).toBe(false); // the block folded: its group opens
    expect(inOpenFavorites("c", true, ["a", "b"])).toBe(false);
  });

  test("a favourite moved, or one from elsewhere put, at a row's edge", () => {
    expect(favoriteAt(["a", "b", "c"], "c", "a", "top")).toEqual(["c", "a", "b"]);
    expect(favoriteAt(["a", "b", "c"], "a", "c", "bottom")).toEqual(["b", "c", "a"]);
    expect(favoriteAt(["a", "b"], "x", "b", "top")).toEqual(["a", "x", "b"]);
    expect(favoriteAt(["a", "b"], "x", "gone", "top")).toEqual(["a", "b", "x"]);
  });

  // kumo, 2026-11-26: the block came, went and came again as the first was favoured (and the
  // other way as the last was taken off): the put's answer handed over to a cache not yet fetched.
  test("the first favoured and the last taken off: the list never steps back", async () => {
    let server: string[] = [];
    const tick = () => new Promise((r) => setTimeout(r, 0));
    vi.spyOn(api, "favorites").mockImplementation(async () => {
      await tick();
      return { channels: [...server] };
    });
    // The server's answer comes late; the stream's count moves before it, as it does there.
    vi.spyOn(api, "putFavorites").mockImplementation(async (chs: string[]) => {
      server = [...chs];
      setCarried("favorites", Date.now());
      await new Promise((r) => setTimeout(r, 20));
      return { channels: [...server] };
    });
    const seen: number[] = [];
    function Probe() {
      seen.push(useFavorites().length);
      return null;
    }
    const host = document.createElement("div");
    document.body.append(host);
    setCarried("favorites", 1);
    await act(async () => createRoot(host).render(<Probe />));
    await act(async () => void (await new Promise((r) => setTimeout(r, 30))));
    seen.length = 0;

    await act(async () => setFavorite("A", true));
    await act(async () => void (await new Promise((r) => setTimeout(r, 80))));
    expect(seen.at(-1)).toBe(1);
    expect(seen.slice(seen.indexOf(1))).not.toContain(0);

    seen.length = 0;
    await act(async () => setFavorite("A", false));
    await act(async () => void (await new Promise((r) => setTimeout(r, 80))));
    expect(seen.at(-1)).toBe(0);
    expect(seen.slice(seen.indexOf(0))).not.toContain(1);
    expect(queryClient.getQueryData(["favorites"])).toEqual({ channels: [] });
  });

  // kumo, 2026-11-26: 改名 and 全部取消收藏 in its row's menu.
  test("named: drawn at once, kept on the device; all taken off: none left", async () => {
    vi.spyOn(api, "favorites").mockResolvedValue({ channels: ["A", "B"] });
    vi.spyOn(api, "patchFavorites").mockImplementation(async (p) => ({ channels: ["A", "B"], name: p.name || undefined }));
    vi.spyOn(api, "putFavorites").mockImplementation(async (chs: string[]) => ({ channels: [...chs], name: "常用" }));
    let ids: string[] = [];
    let name: string | undefined;
    function Probe() {
      ids = useFavorites();
      name = useFavoritesName();
      return null;
    }
    const host = document.createElement("div");
    document.body.append(host);
    await act(async () => createRoot(host).render(<Probe />));
    await act(async () => void (await new Promise((r) => setTimeout(r, 10))));
    expect(ids).toEqual(["A", "B"]);
    await act(async () => setFavoritesName("常用"));
    await act(async () => void (await new Promise((r) => setTimeout(r, 10))));
    expect(name).toBe("常用");
    expect(JSON.parse(localStorage.getItem("favorites")!).name).toBe("常用");
    await act(async () => setFavoritesName(""));
    await act(async () => void (await new Promise((r) => setTimeout(r, 10))));
    expect(name).toBeUndefined();
    await act(async () => clearFavorites());
    expect(ids).toEqual([]);
    await act(async () => void (await new Promise((r) => setTimeout(r, 10))));
    expect(ids).toEqual([]);
  });
});
