/// <reference types="vitest/config" />
// The web app (docs/plan.md "网页", "弱网"). `npm run build` writes it to internal/webui/dist, which
// the Go binary embeds (internal/webui): hashed files under assets/ kept for good, the HTML asked
// afresh, a brotli and a gzip beside each script and style.
import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import { readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { brotliCompressSync, constants, gzipSync } from "node:zlib";
import { defineConfig, type Plugin } from "vite";

/** A package's module, or a bundler's helper: what goes in the libraries' files, apart from ours. Not
 *  the icons: which of them a part uses changes with our code, and would rename the libraries' files. */
const LIB = /node_modules(?!\/lucide-react\/)|^\0/;

/** Each script, style and the HTML get a brotli (x.br) and a gzip (x.gz) beside them, at the highest
 *  levels, as they are built once; internal/webui sends whichever the browser takes. */
function precompress(): Plugin {
  return {
    name: "iridium-precompress",
    apply: "build",
    writeBundle({ dir }, bundle) {
      for (const name of Object.keys(bundle).filter((f) => /\.(js|css|html|svg|webmanifest)$/.test(f))) {
        const file = join(dir!, name);
        const raw = readFileSync(file);
        writeFileSync(`${file}.br`, brotliCompressSync(raw, { params: { [constants.BROTLI_PARAM_QUALITY]: 11 } }));
        writeFileSync(`${file}.gz`, gzipSync(raw, { level: 9 }));
      }
    },
  };
}

/** The parts that come only as they are opened (src/app.tsx), the chat first: their files the first
 *  screen does not load go in a <meta name="later"> the page fetches ahead once idle (src/prefetch.ts). */
const LATER = ["/src/chat/chat.tsx", "/src/settings.tsx", "/src/apps.tsx", "/src/jump.tsx", "/src/secret.tsx"];

/** LATER's files, in a <meta name="later">. */
function later(): Plugin {
  return {
    name: "iridium-later",
    apply: "build",
    transformIndexHtml: {
      order: "post",
      handler(_, { bundle }) {
        const files = (from: string[], into = new Set<string>()): Set<string> => {
          for (const name of from) {
            const c = bundle![name];
            if (c?.type !== "chunk" || into.has(name)) continue;
            into.add(name);
            c.viteMetadata?.importedCss.forEach((css) => into.add(css));
            files(c.imports, into);
          }
          return into;
        };
        const chunks = Object.values(bundle!).flatMap((c) => (c.type === "chunk" ? [c] : []));
        const part = (...ends: string[]) => ends.flatMap((p) => chunks.filter((c) => c.facadeModuleId?.endsWith(p)).map((c) => c.fileName));
        const loaded = files(chunks.filter((c) => c.isEntry).map((c) => c.fileName));
        const later = new Set<string>();
        for (const p of LATER) files(part(p), later);
        const content = [...later]
          .filter((f) => !loaded.has(f))
          .map((f) => `/${f}`)
          .join(" ");
        return [{ tag: "meta", attrs: { name: "later", content }, injectTo: "head" }];
      },
    },
  };
}

const api = process.env.IR_API ?? "http://127.0.0.1:7810";

export default defineConfig({
  plugins: [react(), tailwindcss(), precompress(), later()],
  build: {
    outDir: "../internal/webui/dist",
    emptyOutDir: true,
    manifest: true,
    chunkSizeWarningLimit: 1024, // test/build.test.ts guards what is sent
    rolldownOptions: {
      output: {
        // The libraries apart from our code, so an update that changes only ours keeps their files'
        // names: those of the first screen in one file, those two or more later parts share in
        // another, and each part's own in its own.
        codeSplitting: {
          groups: [
            { name: "lib", test: LIB, tags: ["$initial"], priority: 2 },
            { name: "lib-shared", test: LIB, minShareCount: 2, priority: 1 },
            { name: "lib", test: LIB, entriesAware: true },
          ],
        },
      },
    },
  },
  resolve: {
    alias: {
      // Streamdown imports rehype-raw for its default plugins, which src/chat/markdown.tsx turns off:
      // a big part of the chat's libraries, never run (Rhodium #356).
      "rehype-raw": `${import.meta.dirname}/src/chat/no-raw.ts`,
    },
  },
  server: {
    host: "127.0.0.1",
    port: 7820,
    // The daemon's own guard takes the page's Host and Origin as they are: keep the Host.
    proxy: {
      "/api": { target: api, changeOrigin: false },
      "/_ir": { target: api, changeOrigin: false },
      // Satori with ir (its WebSocket too), for an app tried against the dev server.
      "/v1": { target: api, changeOrigin: false, ws: true },
    },
  },
  test: {
    environment: "happy-dom",
    include: ["test/**/*.test.{ts,tsx}"],
    testTimeout: 120_000,
  },
});
