// What a file shows as when pressed (plan "网页", 2026-11-12), pure for tests: its kind of preview by
// its name, else its type; its first bytes as text, if they are; a table's rows.

/** How a file previews: Markdown drawn, a table, code in colours, plain text; a PDF in a tab of its
 *  own; none, it downloads. */
export type PreviewKind = "markdown" | "csv" | "tsv" | "code" | "text" | "pdf";

/** At most this much of a file is fetched to preview it. */
export const PREVIEW_BYTES = 1 << 20;
/** At most this many rows of a table are drawn. */
export const PREVIEW_ROWS = 5000;

const TEXT = new Set(["txt", "text", "log", "out", "err"]);
// Code and config, coloured where hl.tsx knows the language; markup (html, svg, xml) only as source.
const CODE = new Set(
  (
    "js jsx mjs cjs ts tsx mts cts go rs py pyi rb java kt kts scala c h cc cpp cxx hpp hh cs swift m mm php pl lua r dart zig nim ex exs erl hs ml clj " +
    "sh bash zsh fish ps1 psm1 bat cmd css scss sass less html htm xhtml svg xml xsl vue svelte astro json jsonc json5 jsonl ndjson yaml yml toml ini cfg conf " +
    "env properties sql graphql gql proto diff patch tf hcl nix gradle cmake mk make dockerfile containerfile gitignore gitattributes editorconfig lock csproj sln"
  ).split(" "),
);
// Files known by their whole name.
const NAMED = new Set(["dockerfile", "containerfile", "makefile", "gnumakefile", "cmakelists.txt", "license", "readme", "procfile", "justfile", "vagrantfile", "gemfile", "rakefile"]);

/** The preview a file of name and mime type has; undefined: none (it downloads). By its extension
 *  first, then its type: text/*, JSON. */
export function previewKind(name: string, mime?: string): PreviewKind | undefined {
  const n = name.toLowerCase();
  const ext = /\.([a-z0-9]+)$/.exec(n)?.[1];
  if (ext === "md" || ext === "markdown" || ext === "mdx") return "markdown";
  if (ext === "csv") return "csv";
  if (ext === "tsv" || ext === "tab") return "tsv";
  if (ext === "pdf") return "pdf";
  if (ext && TEXT.has(ext)) return "text";
  if (ext && CODE.has(ext)) return "code";
  if (NAMED.has(n) || (n.startsWith(".") && CODE.has(n.slice(1)))) return "code";
  const m = (mime ?? "").split(";")[0]!.trim().toLowerCase();
  if (m === "application/pdf") return "pdf";
  if (m === "text/markdown") return "markdown";
  if (m === "text/csv") return "csv";
  if (m === "text/tab-separated-values") return "tsv";
  if (m === "application/json" || m.endsWith("+json") || m === "application/xml" || m.endsWith("+xml")) return "code";
  if (m.startsWith("text/")) return "text";
  return undefined;
}

/** The total size a response's Content-Range gives ("bytes 0-1048575/5242880"); undefined: not
 *  said. */
export function rangeTotal(header: string | null): number | undefined {
  const m = /\/(\d+)\s*$/.exec(header ?? "");
  return m ? Number(m[1]) : undefined;
}

/** bytes as UTF-8 text; null when they are not (a NUL, a byte UTF-8 has not). cut: the file goes
 *  on past them, so a character its last bytes begin is left out, not taken as broken. */
export function textOf(bytes: Uint8Array, cut: boolean): string | null {
  if (bytes.includes(0)) return null;
  let end = bytes.length;
  if (cut) {
    // Back over a last character begun but not ended: at most 3 bytes 10xxxxxx after its lead.
    let i = end - 1;
    while (i >= 0 && end - i <= 4 && (bytes[i]! & 0xc0) === 0x80) i--;
    if (i >= 0) {
      const b = bytes[i]!;
      const len = b >= 0xf0 ? 4 : b >= 0xe0 ? 3 : b >= 0xc0 ? 2 : 1;
      if (i + len > end) end = i;
    }
  }
  try {
    return new TextDecoder("utf-8", { fatal: true, ignoreBOM: false }).decode(bytes.subarray(0, end));
  } catch {
    return null;
  }
}

/** text's rows, its fields split at sep (',' or '\t'): a field in double quotes may hold sep, a
 *  line break and "" for a quote (RFC 4180). At most max rows; more: whether there were more. cut:
 *  the text stops short of the file's end, its last row maybe half, so left out. */
export function parseTable(text: string, sep: string, max = PREVIEW_ROWS, cut = false): { rows: string[][]; more: boolean } {
  const rows: string[][] = [];
  let row: string[] = [];
  let field = "";
  let quoted = false;
  let i = 0;
  let more = false;
  const endRow = () => {
    row.push(field);
    field = "";
    if (rows.length >= max) more = true;
    else rows.push(row);
    row = [];
  };
  for (; i < text.length; i++) {
    const c = text[i]!;
    if (quoted) {
      if (c === '"') {
        if (text[i + 1] === '"') (field += '"'), i++;
        else quoted = false;
      } else field += c;
      continue;
    }
    if (c === '"' && field === "") quoted = true;
    else if (c === sep) row.push(field), (field = "");
    else if (c === "\n" || c === "\r") {
      if (c === "\r" && text[i + 1] === "\n") i++;
      endRow();
      if (more) break;
    } else field += c;
  }
  if (!more && !cut && (field !== "" || row.length)) endRow();
  return { rows, more };
}
