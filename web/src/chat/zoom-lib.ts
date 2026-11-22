// A picture opened over the page, to look closer (otae-c, 2026-11-22: as the Codex app's viewer,
// its ZoomableStage): the picture fit to the window is scale 1; zoomed, it is moved by an offset
// from the middle, its middle the transform's origin. Kept apart from zoom.tsx to be tested.

export type Pt = { x: number; y: number };
export const ORIGIN: Pt = { x: 0, y: 0 };

/** How far past its fit the picture goes: at least 5 times, and at least twice its own pixels. */
export const maxScale = (fitRatio: number) => Math.max(5, fitRatio > 0 ? 2 / fitRatio : 5);

export const clamp = (v: number, lo: number, hi: number) => Math.min(Math.max(v, lo), hi);

/** offset kept so the picture, w×h fit at scale, shows no more than 24px of the window (vw×vh)
 *  past its edge; one smaller than the window stays in its middle. */
export function bound(offset: Pt, scale: number, w: number, h: number, vw: number, vh: number): Pt {
  const room = (size: number, view: number) => (size > view ? (size - view) / 2 + 24 : 0);
  const mx = room(w * scale, vw);
  const my = room(h * scale, vh);
  return { x: clamp(offset.x, -mx, mx), y: clamp(offset.y, -my, my) };
}

/** The offset that keeps the point at (screen, its distance from the window's middle) where it is
 *  as the scale goes from `from` to `to`. */
export function zoomAt(offset: Pt, from: number, to: number, at: Pt): Pt {
  const k = to / from;
  return { x: at.x - (at.x - offset.x) * k, y: at.y - (at.y - offset.y) * k };
}
