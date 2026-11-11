// The folder groups and the folders' marks, as the stream carries them in one state, "guilds"
// (api.Guilds: the groups in the order they were dragged to, the folders' colour, pin and the key
// each was dragged to). The list reads them as it always did: the groups (Workspace[]), where the
// rows were put (Places) and the folders' colours, each made once per state.
import type { Color, FolderPatch, Guilds, Workspace } from "./api/types.gen";
import { useCarried } from "./store";

/** Where the rows of the list were put by hand: the groups' rows by key (w<id>) in their order,
 *  the folders' keys by path (as grouped), the folders pinned. */
export type Places = { groups: string[]; order: Record<string, number>; pinned: string[] };
/** The folders' colour tags, by path. */
export type FolderColors = Record<string, Color>;

export const NO_GUILDS: Guilds = { guilds: [], folders: [] };

const placesMade = new WeakMap<Guilds, Places>();
const colorsMade = new WeakMap<Guilds, FolderColors>();

export function placesOf(g: Guilds): Places {
  let p = placesMade.get(g);
  if (!p) {
    p = { groups: g.guilds.map((w) => `w${w.id}`), order: {}, pinned: [] };
    for (const f of g.folders) {
      if (f.order) p.order[f.path] = f.order;
      if (f.pinned) p.pinned.push(f.path);
    }
    placesMade.set(g, p);
  }
  return p;
}

export function colorsOf(g: Guilds): FolderColors {
  let c = colorsMade.get(g);
  if (!c) {
    c = {};
    for (const f of g.folders) if (f.color) c[f.path] = f.color;
    colorsMade.set(g, c);
  }
  return c;
}

/** g with the folder of path marked as p says (order 0, color "" take theirs off). */
export function withMark(g: Guilds, path: string, p: FolderPatch): Guilds {
  const was = g.folders.find((f) => f.path === path) ?? { path };
  const f = { ...was, ...(p.order !== undefined ? { order: p.order } : {}), ...(p.pinned !== undefined ? { pinned: p.pinned } : {}), ...(p.color !== undefined ? { color: p.color } : {}) };
  const folders = g.folders.filter((x) => x.path !== path);
  return { ...g, folders: f.order || f.pinned || f.color ? [...folders, f] : folders };
}

/** g with its groups in the order of keys (w<id>); those not in it after them. */
export function withGroupOrder(g: Guilds, keys: string[]): Guilds {
  const rank = (w: Workspace) => {
    const i = keys.indexOf(`w${w.id}`);
    return i < 0 ? keys.length : i;
  };
  return { ...g, guilds: [...g.guilds].sort((a, b) => rank(a) - rank(b)) };
}

/** The carried "guilds", if the stream has it. */
export const useGuilds = () => useCarried<Guilds>("guilds");
