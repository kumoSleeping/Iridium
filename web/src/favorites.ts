// Favorites (kumo, 2026-11-26): each person's one list of channels (a session's is its own),
// at the top of the list of sessions while it holds any: none at first, it comes with the first
// one favoured (a row's menu) and goes with the last one taken off. One level, nothing made in it;
// a channel there stays in its own group too. Kept by the server under the login, so it follows the
// person to every device (internal/server/favorites.go); another's changes nothing of mine.
import { useEffect, useSyncExternalStore } from "react";
import { api, missing, queryClient } from "./api/client";
import type { Color, Favorites, FavoritesPatch } from "./api/types.gen";
import { useQuery } from "./api/query";
import { t } from "./i18n";
import { useCarried } from "./store";
import { errText, toast } from "./toast";
import { keep, kept } from "./ui";
import type { Edge } from "./reorder";

/** One key, asked again as the carried count moves: a key a count would leave the list empty for
 *  a moment (the new key's first fetch) after a change, the block gone and back (kumo, 2026-11-26). */
const KEY = ["favorites"];
/** The list as changed here, till the server has said it back: drawn in place of the cache. */
let mine: string[] | null = null;
/** The changes, one after another: the last one put is what stays. */
let queue: Promise<unknown> = Promise.resolve();
/** The carried count last asked again for. */
let seen: number | undefined;
const subs = new Set<() => void>();
const emit = () => subs.forEach((f) => f());
const subscribe = (f: () => void) => (subs.add(f), () => void subs.delete(f));
const NONE: string[] = [];
const EMPTY: Favorites = { channels: NONE };

/** The list as last known here, kept on the device: the page reloaded draws the block with the
 *  sessions' list, not a moment after it, the groups pushed down (kumo, 2026-11-26). Asked again
 *  at once all the same. */
const STORED = "favorites";
const stored = (): Favorites | undefined => {
  try {
    const f = JSON.parse(kept(STORED) ?? "null") as Favorites | null;
    return f && Array.isArray(f.channels) ? f : undefined;
  } catch {
    return undefined;
  }
};
/** f into the cache, and kept on the device. */
const known = (f: Favorites) => {
  queryClient.setQueryData(KEY, f);
  keep(STORED, JSON.stringify(f));
};

const favQ = {
  queryKey: KEY,
  queryFn: () =>
    api.favorites().then(
      (f) => (keep(STORED, JSON.stringify(f)), f),
      (e) => (missing(e) ? EMPTY : Promise.reject(e)),
    ),
  initialData: stored,
  initialDataUpdatedAt: 0,
  staleTime: 30_000,
};

/** My favorites, by channel id, in my order. */
export function useFavorites(): string[] {
  const v = useCarried<number>("favorites");
  useEffect(() => {
    if (v === undefined || v === seen) return;
    const first = seen === undefined;
    seen = v;
    if (!first) void queryClient.invalidateQueries({ queryKey: KEY });
  }, [v]);
  const q = useQuery(favQ, queryClient);
  const local = useSyncExternalStore(subscribe, () => mine);
  return local ?? q.data?.channels ?? NONE;
}

/** My favorites' colour tag (the folder's icon tinted), mine alone. */
export const useFavoritesColor = (): Color | undefined => useQuery(favQ, queryClient).data?.color || undefined;

/** The name I gave my favorites, none: the page's own (收藏). */
export const useFavoritesName = (): string | undefined => useQuery(favQ, queryClient).data?.name || undefined;

/** Tags my favorites with c ("" takes it off). */
export const setFavoritesColor = (c: Color | "") => patchFavorites({ color: c as Color }, { color: c || undefined });

/** Names my favorites ("" gives them the page's own name back). */
export const setFavoritesName = (name: string) => patchFavorites({ name }, { name: name.trim() || undefined });

/** Every favourite taken off at once (全部取消收藏): the block goes. */
export const clearFavorites = () => put([]);

/** My favorites' colour or name changed: drawn so at once (shown), then patched; failed, as it was. */
function patchFavorites(p: FavoritesPatch, shown: Partial<Favorites>) {
  const was = queryClient.getQueryData<Favorites>(KEY) ?? EMPTY;
  void queryClient.cancelQueries({ queryKey: KEY });
  queryClient.setQueryData<Favorites>(KEY, { ...was, ...shown });
  void api.patchFavorites(p).then(
    (f) => known(f),
    (e: unknown) => {
      queryClient.setQueryData(KEY, was);
      toast(t("err.action"), { error: true, description: errText(e) });
    },
  );
}

const now = () => mine ?? queryClient.getQueryData<Favorites>(KEY)?.channels ?? NONE;

/** Makes my favorites list (the desktop app asks it too: crossfav.ts): drawn so at once, then put; what the server says back goes into the
 *  cache before the local list gives way to it, so the list never steps back. A failed put: as it was. */
export function put(list: string[]) {
  mine = list;
  emit();
  void queryClient.cancelQueries({ queryKey: KEY });
  queue = queue.then(() =>
    api.putFavorites(list).then(
      (f) => {
        known(f);
        if (mine === list) (mine = null), emit();
      },
      (e: unknown) => {
        if (mine === list) (mine = null), emit();
        toast(t("err.action"), { error: true, description: errText(e) });
      },
    ),
  );
}

export const isFavorite = (id: string) => now().includes(id);

/** Whether the session opened is in sight among my favorites (their block open, it among them):
 *  its own group and folders are then not opened for it (otae-c, 2026-11-26: opened from the
 *  favorites, its folder far below opened too). */
export const inOpenFavorites = (id: string, open: boolean, favs: readonly string[]) => open && favs.includes(id);

/** Favoured (last of them) or taken off. */
export const setFavorite = (id: string, on: boolean) => {
  const was = now();
  if (on === was.includes(id)) return;
  put(on ? [...was, id] : was.filter((x) => x !== id));
};

/** id put at edge of target among the favorites: moved, or favoured there if it was not. */
export function placeFavorite(id: string, target: string, edge: Edge) {
  put(favoriteAt(now(), id, target, edge));
}

/** The list with id at edge of target (id moved, or added there). */
export function favoriteAt(list: string[], id: string, target: string, edge: Edge): string[] {
  const rest = list.filter((x) => x !== id);
  const j = rest.indexOf(target);
  if (j < 0) return [...rest, id];
  rest.splice(j + (edge === "bottom" ? 1 : 0), 0, id);
  return rest;
}
