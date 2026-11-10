// The transcript as Satori messages (src/satori.ts): each block of the table the server's encoder
// is tested on (internal/satori/testdata/blocks.json) reads back as the server sends it.
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, test } from "vitest";
import type { Block } from "../src/api/types.gen";
import { blockOf, parse } from "../src/satori";

const table = JSON.parse(readFileSync(join(import.meta.dirname, "../../internal/satori/testdata/blocks.json"), "utf8")) as {
  cases: { name: string; block: Block; content: string }[];
};

describe("blockOf", () => {
  test.each(table.cases.map((c) => [c.name, c] as const))("%s", (_, c) => {
    expect(blockOf({ id: c.block.at, content: c.content, created_at: c.block.time })).toEqual(c.block);
  });
  test("character references, bare attributes, a tag left open", () => {
    expect(parse(`a &#x3C;&#60;&lt;b&gt; <x k="&quot;1&quot;" on/>`)).toEqual([
      { type: "text", attrs: {}, children: [], text: "a <<<b> " },
      { type: "x", attrs: { k: '"1"', on: true }, children: [] },
    ]);
    expect(blockOf({ id: "1.0", content: "<iridium:thinking>half" }).text).toBe("half");
  });
});
