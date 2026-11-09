// File links (ir:<id>) in a reply's Markdown: where the words put them, as their kind shows.
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, test } from "vitest";
import type { FileRef } from "../src/api/types.gen";
import { Markdown } from "../src/chat/markdown";

const png: FileRef = { ref: "abcdefghij", name: "chart.png", path: "ir:abcdefghij", size: 3926, mime: "image/png", kind: "image", thumb: true, mtime: 1, w: 640, h: 420 };
const csv: FileRef = { ref: "bcdefghijk", name: "data.csv", path: "ir:bcdefghijk", size: 52, mime: "text/csv", kind: "file", mtime: 2 };
const text = "Before.\n\n![a chart](ir:abcdefghij)\n\nAfter, and [the data](ir:bcdefghijk), [gone](ir:cdefghijkl).";

describe("file links", () => {
  test("a picture in place, a file to download in the line, one not given struck", () => {
    const html = renderToStaticMarkup(<Markdown text={text} session="s1" files={[png, csv]} />);
    expect(html.indexOf("Before.")).toBeLessThan(html.indexOf("/files/abcdefghij/thumb"));
    expect(html.indexOf("/files/abcdefghij/thumb")).toBeLessThan(html.indexOf("After"));
    expect(html).toMatch(/href="[^"]*\/files\/bcdefghijk\?download[^"]*" download="data.csv"/);
    expect(html).toContain("the data");
    expect(html).toMatch(/line-through[^>]*>.*gone/);
    expect(html).not.toContain('src=""');
  });
  test("in a draft (no files yet): the words only", () => {
    const html = renderToStaticMarkup(<Markdown text={text} />);
    expect(html).not.toContain("/files/");
    expect(html).toContain("the data");
    expect(html).toContain("a chart");
  });
});
