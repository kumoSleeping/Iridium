// The "+": whose agents a folder offers, and a place pressed elsewhere opening it (places.ts).
import { describe, expect, test } from "vitest";
import type { Machine } from "../src/machines";
import { agentsAt } from "../src/machines";
import { createAt, onCreateAt, onOpenPlace, openPlace, type Place } from "../src/places";

const win: Machine = { id: "m1", name: "otae-2", os: "windows", home: "C:\\Users\\o", sep: "\\", agents: [{ agent: "dsh" }], online: true } as Machine;

describe("agentsAt", () => {
  test("a folder here offers this machine's agents", () => {
    expect(agentsAt("/home/otae/ir", [win])).toBe("here");
    expect(agentsAt("~", [win])).toBe("here");
  });
  test("a folder at another machine offers that machine's alone, in its own path", () => {
    expect(agentsAt("m1:C:/Code/Ds_Benchmark", [win])).toEqual({ m: win, cwd: "C:\\Code\\Ds_Benchmark" });
  });
  test("a machine paired with no agents is there, not gone (the + says it has none)", () => {
    const bare = { ...win, id: "m2", agents: [] };
    expect(agentsAt("m2:C:/Code", [win, bare])).toEqual({ m: bare, cwd: "C:\\Code" });
  });
  test("a machine no more there offers none", () => {
    expect(agentsAt("m9:/root", [win])).toBe("gone");
    expect(agentsAt("m1:C:/x", [])).toBe("gone");
  });
});

describe("places", () => {
  test("createAt reaches the + while it listens", () => {
    const got: Place[] = [];
    const stop = onCreateAt((p) => got.push(p));
    createAt({ machine: "m1", path: "C:/Code" });
    stop();
    createAt({ path: "/root" });
    expect(got).toEqual([{ machine: "m1", path: "C:/Code" }]);
  });
  test("openPlace goes to the list; with no list, to the +", () => {
    const listed: Place[] = [];
    const made: Place[] = [];
    const stopMade = onCreateAt((p) => made.push(p));
    const stopList = onOpenPlace((p) => listed.push(p));
    openPlace({ path: "/home/otae/ir" });
    stopList();
    openPlace({ machine: "m1", path: "C:/Code" });
    stopMade();
    expect(listed).toEqual([{ path: "/home/otae/ir" }]);
    expect(made).toEqual([{ machine: "m1", path: "C:/Code" }]);
  });
});
