// The build's sizes (plan "弱网"): the first screen at most 250KB gzipped (the user, 2026-11-06; was 200KB); the chat loaded only when
// opened, under its own limit, its libraries apart from the first screen's; a brotli and a gzip
// beside each script and style; the later files listed for idle prefetch.
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

test("the first screen is at most 250KB gzipped, without the chat's Markdown", () => {
  const first = files("index.html");
  const size = kb(first) + gz("index.html") / 1024 + gz("favicon.svg") / 1024 + gz("theme.js") / 1024;
  console.log(`first screen ${size.toFixed(1)}KB gzipped`);
  expect(size).toBeLessThanOrEqual(250);
  const js = text(first);
  expect(js).not.toContain("data-streamdown");
  // React's production build.
  expect(js).not.toContain('unique "key" prop');
});

test("the chat comes apart, under its limit", () => {
  const first = files("index.html");
  const chat = [...files("src/chat/chat.tsx")].filter((f) => !first.has(f));
  console.log(`chat ${kb(chat).toFixed(1)}KB gzipped`);
  expect(manifest["src/chat/chat.tsx"]!.isDynamicEntry).toBe(true);
  expect(kb(chat)).toBeLessThanOrEqual(200);
  expect(text(chat)).toContain("data-streamdown");
  // The page fetches them ahead once idle: the HTML lists them.
  const later = /<meta name="later" content="([^"]*)"/.exec(readFileSync(join(out, "index.html"), "utf8"))![1]!.split(" ");
  for (const f of chat) expect(later).toContain(`/${f}`);
  for (const f of first) expect(later).not.toContain(`/${f}`);
});

test("the settings and the jump box are fetched ahead too, after the chat", () => {
  const first = files("index.html");
  const later = /<meta name="later" content="([^"]*)"/.exec(readFileSync(join(out, "index.html"), "utf8"))![1]!.split(" ");
  for (const key of ["src/settings.tsx", "src/terminals.tsx", "src/jump.tsx"]) {
    expect(manifest[key]!.isDynamicEntry).toBe(true);
    for (const f of files(key)) if (!first.has(f)) expect(later).toContain(`/${f}`);
  }
  expect(later[0]).toBe(`/${manifest["src/chat/chat.tsx"]!.file}`);
});

test("each script and style has a brotli and a gzip twin; the libraries apart from our code", () => {
  for (const c of Object.values(manifest)) for (const f of [c.file, ...(c.css ?? [])]) for (const ext of [".br", ".gz"]) expect([f + ext, existsSync(join(out, f + ext))]).toEqual([f + ext, true]);
  const names = Object.values(manifest).map((c) => c.file);
  expect(names.some((f) => /^assets\/lib-/.test(f))).toBe(true);
  expect(names.some((f) => /^assets\/lib~chat-/.test(f))).toBe(true);
});
