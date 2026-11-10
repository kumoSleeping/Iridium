import { readFileSync } from "node:fs";
import { join } from "node:path";
import { expect, test } from "vitest";
import { formatPlace, parsePlace, placeFromKey, placeKey, splitPlace } from "../src/places";

// The cases internal/place is tested by too.
type Cases = {
  apps: Record<string, string>;
  split: { in: string; name?: string; path: string }[];
  parse: { in: string; self: string; app?: string; path?: string; error?: boolean }[];
  format: { app: string; path: string; out: string }[];
  key: { app: string; path: string; key: string; back?: string }[];
};
const c: Cases = JSON.parse(readFileSync(join(import.meta.dirname, "../../internal/place/testdata/places.json"), "utf8"));
const lookup = (name: string) => Object.entries(c.apps).find(([, n]) => n.toLowerCase() === name.toLowerCase())?.[0];

test("split", () => {
  for (const x of c.split) expect(splitPlace(x.in), x.in).toEqual(x.name ? { name: x.name, path: x.path } : null);
});

test("parse", () => {
  for (const x of c.parse) {
    const p = parsePlace(x.in, x.self || undefined, lookup);
    if (x.error) expect(p, x.in).toBeNull();
    else expect(p, x.in).toEqual(x.app ? { machine: x.app, path: x.path } : { path: x.path });
  }
});

test("format", () => {
  for (const x of c.format) expect(formatPlace(x.app ? { machine: x.app, path: x.path } : { path: x.path }, (id) => c.apps[id])).toBe(x.out);
});

test("key", () => {
  for (const x of c.key) {
    const p = x.app ? { machine: x.app, path: x.path } : { path: x.path };
    expect(placeKey(p)).toBe(x.key);
    expect(placeFromKey(x.key)).toEqual(x.app ? { machine: x.app, path: x.back ?? x.path } : { path: x.back ?? x.path });
  }
});
