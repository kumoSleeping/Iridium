// The build's sizes (plan "弱网"): the first screen at most 200KB gzipped; the chat and the topology
// view loaded only when opened, each under its own limit, their libraries apart from the first
// screen's; a brotli and a gzip beside each script and style; the later files listed for idle prefetch.
import { existsSync, mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { gzipSync } from "node:zlib";
import { afterAll, beforeAll, expect, test } from "vitest";

type Chunk = { file: string; imports?: string[]; dynamicImports?: string[]; css?: string[]; isEntry?: boolean; isDynamicEntry?: boolean };
let out = "";
let manifest: Record<string, Chunk> = {};

beforeAll(async () => {
  out = mkdtempSync(join(tmpdir(), "iridium-web-build-"));
  // The production build, as users get: vitest sets NODE_ENV=test, which would build React's development one.
  process.env.NODE_ENV = "production";
  const { build } = await import("vite");
  await build({ root: join(import.meta.dirname, ".."), mode: "production", logLevel: "silent", build: { outDir: out, emptyOutDir: true } });
  manifest = JSON.parse(readFileSync(join(out, ".vite/manifest.json"), "utf8"));
}, 120_000);
afterAll(() => out && rmSync(out, { recursive: true, force: true }));

const gz = (f: string) => gzipSync(readFileSync(join(out, f)), { level: 9 }).length;
/** A chunk's files with all it imports statically, and their styles. */
function files(key: string, into = new Set<string>()): Set<string> {
  const c = manifest[key]!;
  if (into.has(c.file)) return into;
  into.add(c.file);
  c.css?.forEach((f) => into.add(f));
  c.imports?.forEach((k) => files(k, into));
  return into;
}
const text = (fs: Iterable<string>) => [...fs].map((f) => readFileSync(join(out, f), "utf8")).join("\n");
const kb = (fs: Iterable<string>) => [...fs].reduce((n, f) => n + gz(f), 0) / 1024;

test("the first screen is at most 200KB gzipped, with neither the chat's Markdown nor the graph's library", () => {
  const first = files("index.html");
  const size = kb(first) + gz("index.html") / 1024 + gz("favicon.svg") / 1024 + gz("theme.js") / 1024;
  console.log(`first screen ${size.toFixed(1)}KB gzipped`);
  expect(size).toBeLessThanOrEqual(200);
  const js = text(first);
  expect(js).not.toContain("data-streamdown");
  expect(js).not.toContain("react-flow__");
  // React's production build.
  expect(js).not.toContain('unique "key" prop');
});

test("the chat and the topology come apart, each under its limit", () => {
  const first = files("index.html");
  const part = (key: string) => [...files(key)].filter((f) => !first.has(f));
  const chat = part("src/chat/chat.tsx");
  const topology = part("src/topology/topology.tsx");
  console.log(`chat ${kb(chat).toFixed(1)}KB, topology ${kb(topology).toFixed(1)}KB gzipped`);
  expect(manifest["src/chat/chat.tsx"]!.isDynamicEntry).toBe(true);
  expect(manifest["src/topology/topology.tsx"]!.isDynamicEntry).toBe(true);
  expect(kb(chat)).toBeLessThanOrEqual(200);
  expect(kb(topology)).toBeLessThanOrEqual(120);
  expect(text(chat)).toContain("data-streamdown");
  expect(text(chat)).not.toContain("react-flow__");
  expect(text(topology)).toContain("react-flow");
  expect(text(topology)).not.toContain("data-streamdown");
  // The page fetches them ahead once idle: the HTML lists them.
  const later = /<meta name="later" content="([^"]*)"/.exec(readFileSync(join(out, "index.html"), "utf8"))![1]!.split(" ");
  for (const f of [...chat, ...topology]) expect(later).toContain(`/${f}`);
  for (const f of first) expect(later).not.toContain(`/${f}`);
});

test("each script and style has a brotli and a gzip twin; the libraries apart from our code", () => {
  for (const c of Object.values(manifest)) for (const f of [c.file, ...(c.css ?? [])]) for (const ext of [".br", ".gz"]) expect([f + ext, existsSync(join(out, f + ext))]).toEqual([f + ext, true]);
  const names = Object.values(manifest).map((c) => c.file);
  expect(names.some((f) => /^assets\/lib-/.test(f))).toBe(true);
  expect(names.some((f) => /^assets\/lib~chat-/.test(f))).toBe(true);
});
