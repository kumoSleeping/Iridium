// A conversation's pictures in one viewer (chat/gallery.tsx): one pressed opens among the others,
// its place at the foot, ← and → and the side keys step, a first without its key before; one not
// among them (a tool's result, the composer's) opens alone, without keys or place.
import { act, type ReactNode } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, test } from "vitest";
import { Zoom } from "../src/chat/files";
import { Gallery } from "../src/chat/gallery";
import type { Shot } from "../src/chat/gallery-lib";
import { t } from "../src/i18n";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

function draw(node: ReactNode): HTMLDivElement {
  const host = document.createElement("div");
  document.body.append(host);
  act(() => createRoot(host).render(node));
  return host;
}

const shots: Shot[] = ["/a", "/b", "/c"].map((full) => ({ full, under: `${full}/thumb`, name: full.slice(1), w: 100, h: 50 }));
const pic = (full: string) => (
  <Zoom key={full} full={full} name={full}>
    <button type="button" data-pic={full} />
  </Zoom>
);
const press = (el: Element | null | undefined) => act(() => void (el as HTMLElement).click());
// From inside the popup, as a key pressed there goes (its popup stops arrows on their way up).
const key = (k: string) => act(() => void (document.querySelector("a[download]") ?? document.body).dispatchEvent(new KeyboardEvent("keydown", { key: k, bubbles: true })));
const shown = () => document.querySelector<HTMLImageElement>("img[alt]:not([alt=''])")?.getAttribute("src");
const side = (label: string) => document.querySelector(`button[aria-label="${label}"]`);

afterEach(() => void (document.body.innerHTML = ""));

describe("gallery", () => {
  test("opens among the others and steps by keys and side keys", async () => {
    const el = draw(<Gallery shots={() => shots}>{[pic("/a"), pic("/b"), pic("/c"), pic("/lone")]}</Gallery>);
    press(el.querySelector('[data-pic="/b"]'));
    await act(async () => {});
    expect(shown()).toBe("/b");
    expect(document.body.textContent).toContain("2 / 3");
    expect(side(t("msg.prevImage"))).not.toBeNull();
    expect(side(t("msg.nextImage"))).not.toBeNull();
    // Its thumbnail under it till it has come.
    expect(document.querySelector('img[src="/b/thumb"]')).not.toBeNull();
    key("ArrowLeft");
    expect(shown()).toBe("/a");
    expect(document.body.textContent).toContain("1 / 3");
    expect(side(t("msg.prevImage"))).toBeNull();
    key("ArrowLeft");
    expect(shown()).toBe("/a");
    press(side(t("msg.nextImage")));
    press(side(t("msg.nextImage")));
    expect(shown()).toBe("/c");
    expect(side(t("msg.nextImage"))).toBeNull();
    expect(document.querySelector('a[download="c"]')?.getAttribute("href")).toBe("/c");
  });

  test("one not among them opens alone", async () => {
    const el = draw(<Gallery shots={() => shots}>{[pic("/a"), pic("/lone")]}</Gallery>);
    press(el.querySelector('[data-pic="/lone"]'));
    await act(async () => {});
    expect(shown()).toBe("/lone");
    expect(document.body.textContent).not.toContain("/ 3");
    expect(side(t("msg.nextImage"))).toBeNull();
    key("ArrowRight");
    expect(shown()).toBe("/lone");
  });
});
