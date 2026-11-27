import { describe, expect, it } from "vitest";
import type { FileRef } from "../src/api/types.gen";
import { picturesOf, placed, shotsOf } from "../src/chat/gallery-lib";

const pic = (ref: string, path = `/p/${ref}.png`, kind: FileRef["kind"] = "image"): FileRef => ({ ref, path, name: `${ref}.png`, kind, mime: "image/png", size: 1, mtime: 1, thumb: true, w: 10, h: 20 }) as FileRef;
const link = (id: string, kind: FileRef["kind"] = "image") => pic(id, `ir:${id}`, kind);

describe("gallery", () => {
  it("finds the pictures words put in place, in order, not in code", () => {
    expect(placed("a ![x](ir:bbbbbbbbbb) b ![](<ir:aaaaaaaaaa>) [file](ir:cccccccccc) `![c](ir:dddddddddd)`\n```\n![d](ir:eeeeeeeeee)\n```")).toEqual(["bbbbbbbbbb", "aaaaaaaaaa"]);
  });

  it("orders an agent's pictures as it shows them: in place first, then under its words", () => {
    const files = [pic("under1"), link("aaaaaaaaaa"), link("bbbbbbbbbb"), link("cccccccccc"), pic("vid", "/p/v.mp4", "video"), pic("under2")];
    const words = "![](ir:bbbbbbbbbb) then ![](ir:aaaaaaaaaa), a link [x](ir:cccccccccc)";
    expect(picturesOf({ words, files, session: "s", person: false }).map((f) => f.ref)).toEqual(["bbbbbbbbbb", "aaaaaaaaaa", "under1", "under2"]);
  });

  it("takes a person's every picture, as its bubble shows them all", () => {
    const files = [pic("a"), pic("doc", "/p/a.txt", "file"), pic("b")];
    expect(picturesOf({ words: "", files, session: "c", person: true }).map((f) => f.ref)).toEqual(["a", "b"]);
  });

  it("gives each picture once, served by its message's session, its thumbnail under it", () => {
    const url = (s: string, f: FileRef) => `/${s}/${f.ref}`;
    const under = (s: string, f: FileRef) => `/${s}/${f.ref}/thumb`;
    const shots = shotsOf(
      [
        { words: "", files: [pic("a")], session: "chan", person: true },
        { words: "", files: [pic("b"), pic("c")], session: "agent", person: false },
        { words: "", files: [pic("a")], session: "chan", person: true },
      ],
      url,
      under,
    );
    expect(shots.map((s) => s.full)).toEqual(["/chan/a", "/agent/b", "/agent/c"]);
    expect(shots[1]).toEqual({ full: "/agent/b", under: "/agent/b/thumb", name: "b.png", w: 10, h: 20 });
  });
});
