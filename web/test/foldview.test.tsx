// Folded words and forwards as drawn (chat/longwords.tsx, chat/forwardcard.tsx): one line under
// what is folded, saying how much; a forward's card its head line till pressed, then its messages.
import { act, type ReactNode } from "react";
import { createRoot } from "react-dom/client";
import { describe, expect, test } from "vitest";
import { ForwardCard } from "../src/chat/forwardcard";
import { splitForwards } from "../src/chat/forward";
import { LongWords } from "../src/chat/longwords";
import { t } from "../src/i18n";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

function draw(node: ReactNode): HTMLDivElement {
  const host = document.createElement("div");
  document.body.append(host);
  act(() => createRoot(host).render(node));
  return host;
}

describe("folded words", () => {
  const ctl = (folded: boolean) => ({ folded, set: () => {} });
  test("folded: the first lines and one line under them, how many more", () => {
    const el = draw(<LongWords fold={false} lines={10} ctl={ctl(true)}><p>words</p></LongWords>);
    expect(el.innerHTML).toContain("max-height: 69px");
    expect(el.querySelectorAll("button")).toHaveLength(1);
    expect(el.textContent).toContain(t("chat.moreLines", { n: 7 }));
    // Short words folded by hand: one line, and its key says it opens.
    const short = draw(<LongWords fold={false} lines={1} ctl={ctl(true)}><p>words</p></LongWords>);
    expect(short.innerHTML).toContain("max-height: 23px");
    expect(short.querySelector("button")?.textContent).toBe(t("menu.unfold"));
  });
  test("not folded: the words alone, no key", () => {
    expect(draw(<LongWords fold={false} lines={10} ctl={ctl(false)}><p>words</p></LongWords>).querySelector("button")).toBeNull();
    expect(draw(<LongWords fold lines={3}><p>words</p></LongWords>).innerHTML).toBe("<p>words</p>");
  });
});

describe("a forward's card", () => {
  test("its head line alone till pressed, then each message, a forward inside its own card", () => {
    const { forwards } = splitForwards(`<message forward><message><author name="Ann"/>hello</message><message><author name="Bob"/><message forward><message><author name="Cy"/>deep</message></message></message></message>`);
    const el = draw(<ForwardCard f={forwards[0]!} />);
    const head = el.querySelector("button")!;
    expect(head.textContent).toContain(t("channel.forward", { n: 2 }));
    expect(head.getAttribute("aria-expanded")).toBe("false");
    expect(el.textContent).not.toContain("hello");
    act(() => head.click());
    expect(head.getAttribute("aria-expanded")).toBe("true");
    expect(el.textContent).toContain("hello");
    expect(el.textContent).toContain("Bob");
    expect(el.textContent).not.toContain("deep");
    const inner = el.querySelectorAll("button")[1]!;
    expect(inner.textContent).toContain(t("channel.forward", { n: 1 }));
    act(() => inner.click());
    expect(el.textContent).toContain("deep");
  });
});
