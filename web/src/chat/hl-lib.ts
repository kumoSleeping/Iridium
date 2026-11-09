// highlight.js, its core and a few languages, not all of them: fetched apart, when code is first
// shown (hl.tsx). Its output is escaped HTML.
import hljs from "highlight.js/lib/core";
import bash from "highlight.js/lib/languages/bash";
import c from "highlight.js/lib/languages/c";
import cpp from "highlight.js/lib/languages/cpp";
import css from "highlight.js/lib/languages/css";
import diff from "highlight.js/lib/languages/diff";
import dockerfile from "highlight.js/lib/languages/dockerfile";
import go from "highlight.js/lib/languages/go";
import ini from "highlight.js/lib/languages/ini";
import java from "highlight.js/lib/languages/java";
import javascript from "highlight.js/lib/languages/javascript";
import json from "highlight.js/lib/languages/json";
import makefile from "highlight.js/lib/languages/makefile";
import markdown from "highlight.js/lib/languages/markdown";
import python from "highlight.js/lib/languages/python";
import rust from "highlight.js/lib/languages/rust";
import sql from "highlight.js/lib/languages/sql";
import typescript from "highlight.js/lib/languages/typescript";
import xml from "highlight.js/lib/languages/xml";
import yaml from "highlight.js/lib/languages/yaml";

const LANGS = { bash, c, cpp, css, diff, dockerfile, go, ini, java, javascript, json, makefile, markdown, python, rust, sql, typescript, xml, yaml };
for (const [name, l] of Object.entries(LANGS)) hljs.registerLanguage(name, l);
hljs.registerAliases(["tsx", "mts"], { languageName: "typescript" });
hljs.registerAliases(["jsx", "mjs", "cjs"], { languageName: "javascript" });
hljs.registerAliases(["h"], { languageName: "c" });
hljs.registerAliases(["hpp", "cc"], { languageName: "cpp" });
hljs.registerAliases(["toml"], { languageName: "ini" });

// A guess is taken only among these, and only this sure, in all and for each line: plain words, a
// table, numbers stay plain; a long listing scores on a few of its words, code on most of its lines.
const GUESS = ["json", "python", "diff", "go", "typescript", "yaml", "xml", "bash"];
const SURE = 5;
const PER_LINE = 0.6;
// Past this much text, no colours: a long output is read in a glance, not parsed on every draw.
const MAX = 40_000;

/** A language by its name (a fence's) or a file's extension; undefined: not one known. */
export function langOf(name?: string): string | undefined {
  if (!name) return undefined;
  const n = name.toLowerCase();
  const key = hljs.getLanguage(n) ? n : /\.([a-z0-9]+)$/.exec(n)?.[1];
  return key && hljs.getLanguage(key) ? key : undefined;
}

export type Lit = { html: string; lang: string };

/** Code in a language (a name, a file's, or "auto": guessed), as highlight.js's HTML and the
 *  language's name; null: none known, or not sure enough. */
export function highlight(text: string, name: string): Lit | null {
  if (!text || text.length > MAX) return null;
  const lang = name === "auto" ? name : langOf(name);
  if (!lang) return null;
  if (lang === "auto") {
    if (isJSON(text)) return { html: hljs.highlight(text, { language: "json" }).value, lang: "json" };
    const r = hljs.highlightAuto(text, GUESS);
    const lines = text.split("\n").length;
    return r.language && r.relevance >= SURE && r.relevance >= lines * PER_LINE ? { html: r.value, lang: r.language } : null;
  }
  const l = hljs.getLanguage(lang);
  if (!l) return null;
  return { html: hljs.highlight(text, { language: lang, ignoreIllegals: true }).value, lang: lang };
}

function isJSON(s: string) {
  if (!/^\s*[[{]/.test(s)) return false;
  try {
    JSON.parse(s);
    return true;
  } catch {
    return false;
  }
}
