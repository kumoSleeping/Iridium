import { describe, expect, it } from "vitest";
import { parseTable, previewKind, rangeTotal, textOf } from "../src/chat/preview-lib";

const utf8 = (s: string) => new TextEncoder().encode(s);

describe("preview", () => {
  it("knows a file's preview by its name, then its type", () => {
    expect(previewKind("README.md")).toBe("markdown");
    expect(previewKind("data.CSV")).toBe("csv");
    expect(previewKind("x.tsv")).toBe("tsv");
    expect(previewKind("run.log")).toBe("text");
    expect(previewKind("main.go")).toBe("code");
    expect(previewKind("config.yaml")).toBe("code");
    expect(previewKind("page.html")).toBe("code");
    expect(previewKind("Dockerfile")).toBe("code");
    expect(previewKind(".gitignore")).toBe("code");
    expect(previewKind("paper.pdf")).toBe("pdf");
    expect(previewKind("notes", "text/plain; charset=utf-8")).toBe("text");
    expect(previewKind("blob", "application/json")).toBe("code");
    expect(previewKind("a.zip", "application/zip")).toBeUndefined();
    expect(previewKind("a.bin")).toBeUndefined();
    expect(previewKind("photo.png", "image/png")).toBeUndefined();
  });

  it("reads the total a range response says", () => {
    expect(rangeTotal("bytes 0-1048575/5242880")).toBe(5242880);
    expect(rangeTotal("bytes 0-9/10")).toBe(10);
    expect(rangeTotal(null)).toBeUndefined();
  });

  it("takes bytes as text only when they are UTF-8", () => {
    expect(textOf(utf8("héllo 你好"), false)).toBe("héllo 你好");
    expect(textOf(new Uint8Array([0x61, 0, 0x62]), false)).toBeNull();
    expect(textOf(new Uint8Array([0xff, 0xfe, 0x41]), false)).toBeNull();
    // Cut inside 你 (e4 bd a0): the half character is left out, not taken as broken.
    const b = utf8("ab你");
    expect(textOf(b.subarray(0, 4), true)).toBe("ab");
    expect(textOf(b.subarray(0, 3), true)).toBe("ab");
    expect(textOf(b.subarray(0, 4), false)).toBeNull();
    expect(textOf(b, true)).toBe("ab你");
  });

  it("splits a table, quotes and all", () => {
    expect(parseTable('a,b,c\n1,"x, y",3\r\n2,"say ""hi""","two\nlines"\n', ",").rows).toEqual([
      ["a", "b", "c"],
      ["1", "x, y", "3"],
      ["2", 'say "hi"', "two\nlines"],
    ]);
    expect(parseTable("a\tb\n1\t2", "\t").rows).toEqual([
      ["a", "b"],
      ["1", "2"],
    ]);
    expect(parseTable("h\n1\n2\n3\n", ",", 2)).toEqual({ rows: [["h"], ["1"]], more: true });
    // Cut: the last row may be half, so it is left out.
    expect(parseTable("h\n1\n2,par", ",", 10, true).rows).toEqual([["h"], ["1"]]);
  });
});
