// A small highlighter, no grammar library: a shell command (a tool's `command`, here-documents in it
// in the language they are written to), and code in a few languages by name or file extension.
// Pieces only, in order: joined they are the text again.

export type Kind = "cmd" | "kw" | "str" | "cmt" | "var" | "num" | "flag" | "op" | "plus" | "minus";
export type Piece = [text: string, kind?: Kind];
/** out: what a command printed, any language or none: only what is plain in any (see output). */
export type Lang = "sh" | "py" | "go" | "js" | "rs" | "c" | "json" | "out";

const EXT: Record<string, Lang> = {
  sh: "sh", bash: "sh", zsh: "sh", shell: "sh", console: "sh",
  py: "py", python: "py", python3: "py",
  go: "go", golang: "go",
  js: "js", mjs: "js", cjs: "js", jsx: "js", ts: "js", tsx: "js", mts: "js", javascript: "js", typescript: "js", node: "js",
  rs: "rs", rust: "rs",
  c: "c", h: "c", cc: "c", cpp: "c", hpp: "c", java: "c", kt: "c", swift: "c", cs: "c",
  json: "json", jsonc: "json",
};

/** A language by its name (a fence's) or a file's extension; undefined: not one known. */
export function langOf(name?: string): Lang | undefined {
  if (!name) return undefined;
  const n = name.toLowerCase();
  return EXT[n] ?? EXT[/\.([a-z0-9]+)$/.exec(n)?.[1] ?? ""];
}

const KW: Record<Exclude<Lang, "sh" | "out">, Set<string>> = {
  py: new Set("and as assert async await break class continue def del elif else except False finally for from global if import in is lambda None nonlocal not or pass raise return True try while with yield self print len range open".split(" ")),
  go: new Set("break case chan const continue default defer else fallthrough for func go goto if import interface map package range return select struct switch type var nil true false err error string int int64 bool byte any make new append len".split(" ")),
  js: new Set("as async await break case catch class const continue default delete do else export extends false finally for from function if import in instanceof interface let new null of return static super switch this throw true try type typeof undefined var void while yield".split(" ")),
  rs: new Set("as async await break const continue crate else enum extern false fn for if impl in let loop match mod move mut pub ref return self Self static struct super trait true type unsafe use where while Some None Ok Err".split(" ")),
  c: new Set("auto break case char class const continue default do double else enum extern false float for if int long new null nullptr private protected public return short signed sizeof static struct switch this true typedef union unsigned void volatile while".split(" ")),
  json: new Set(["true", "false", "null"]),
};

const SH_KW = new Set("if then else elif fi for while until do done case esac in function select time".split(" "));
// Words that run the next word as a command.
const SH_PRE = new Set(["sudo", "env", "exec", "nohup", "xargs", "time", "command", "nice", "timeout"]);

/** Code in a language, in pieces. */
export function highlight(text: string, lang: Lang): Piece[] {
  return lang === "sh" ? shell(text) : lang === "out" ? output(text) : code(text, lang);
}

const OUT_KW = /^(True|False|None|null|true|false|nil|undefined|NaN)$/;
const OUT_OK = /^(ok|PASS|PASSED|passed|success|succeeded|done|Done)$/;
const OUT_BAD = /^(FAIL|FAILED|failed|error|Error|ERROR|panic|fatal|Fatal|FATAL|Traceback|warning|Warning|WARN)$/;

/** Output: a diff's lines red and green; else quoted text closed on its line, numbers, literals,
 *  file:line places, and words of passing and failing. An apostrophe in a word opens nothing. */
function output(s: string): Piece[] {
  const out: Piece[] = [];
  const diff = /^@@ .* @@/m.test(s) || /^diff --git /m.test(s);
  let plain = "";
  const push = (t: string, k?: Kind) => {
    if (!k) return void (plain += t);
    if (plain) out.push([plain]), (plain = "");
    out.push([t, k]);
  };
  for (const line of s.split(/(?<=\n)/)) {
    if (diff && /^[+-]/.test(line) && !/^(\+\+\+|---) /.test(line)) {
      push(line, line[0] === "+" ? "plus" : "minus");
      continue;
    }
    if (diff && /^@@/.test(line)) {
      push(line, "var");
      continue;
    }
    let i = 0;
    while (i < line.length) {
      const c = line[i]!;
      const prev = line[i - 1] ?? " ";
      const rest = line.slice(i);
      if ((c === "'" || c === '"') && !/\w/.test(prev)) {
        const e = quoted(line, i, c, false);
        if (line[e - 1] === c && e - i > 1 && !/\w/.test(line[e] ?? " ")) {
          push(line.slice(i, e), "str");
          i = e;
          continue;
        }
      }
      const place = !/[\w./-]/.test(prev) && /^[\w./-]*\w\.[A-Za-z]{1,5}:\d+(:\d+)?/.exec(rest);
      if (place) {
        push(place[0], "cmd");
        i += place[0].length;
        continue;
      }
      if (/[\w$]/.test(c) && !/[\w$.]/.test(prev)) {
        const w = /^[\w$]+(\.\d+)?/.exec(rest)![0];
        const k = /^\d[\d.]*$/.test(w) || /^0x[0-9a-f]+$/i.test(w) ? "num" : OUT_KW.test(w) ? "kw" : OUT_OK.test(w) ? "plus" : OUT_BAD.test(w) ? "minus" : undefined;
        push(w, k);
        i += w.length;
        continue;
      }
      push(c);
      i++;
    }
  }
  if (plain) out.push([plain]);
  return out;
}

function code(s: string, lang: Exclude<Lang, "sh" | "out">): Piece[] {
  const out: Piece[] = [];
  const kw = KW[lang];
  const hash = lang === "py";
  const slash = lang !== "py" && lang !== "json";
  let i = 0;
  let plain = "";
  const push = (t: string, k: Kind) => {
    if (plain) out.push([plain]), (plain = "");
    out.push([t, k]);
  };
  while (i < s.length) {
    const c = s[i]!;
    if ((hash && c === "#") || (slash && s.startsWith("//", i))) {
      const e = lineEnd(s, i);
      push(s.slice(i, e), "cmt");
      i = e;
    } else if (slash && s.startsWith("/*", i)) {
      const e = s.indexOf("*/", i + 2);
      const end = e < 0 ? s.length : e + 2;
      push(s.slice(i, end), "cmt");
      i = end;
    } else if (c === '"' || c === "'" || (c === "`" && lang !== "py" && lang !== "json")) {
      const triple = lang === "py" && s.startsWith(c.repeat(3), i);
      const end = triple ? close(s, i + 3, c.repeat(3)) : quoted(s, i, c, c === "`");
      push(s.slice(i, end), "str");
      i = end;
    } else if (/[0-9]/.test(c) && !/[\w$]/.test(s[i - 1] ?? "")) {
      const m = /^(0x[0-9a-fA-F_]+|[0-9][0-9_]*(\.[0-9_]+)?([eE][+-]?[0-9]+)?)/.exec(s.slice(i))!;
      push(m[0], "num");
      i += m[0].length;
    } else if (/[A-Za-z_$]/.test(c)) {
      const m = /^[\w$]+/.exec(s.slice(i))![0];
      if (kw.has(m)) push(m, "kw");
      else plain += m;
      i += m.length;
    } else {
      plain += c;
      i++;
    }
  }
  if (plain) out.push([plain]);
  return out;
}

function lineEnd(s: string, i: number) {
  const e = s.indexOf("\n", i);
  return e < 0 ? s.length : e;
}

function close(s: string, from: number, end: string) {
  const e = s.indexOf(end, from);
  return e < 0 ? s.length : e + end.length;
}

/** The end of a string opened by q at i: a backslash escapes; one not multiline ends at the line. */
function quoted(s: string, i: number, q: string, multiline: boolean) {
  let j = i + 1;
  while (j < s.length) {
    const c = s[j]!;
    if (c === "\\") j += 2;
    else if (c === q) return j + 1;
    else if (c === "\n" && !multiline) return j;
    else j++;
  }
  return s.length;
}

function shell(s: string): Piece[] {
  const out: Piece[] = [];
  let plain = "";
  const push = (t: string, k?: Kind) => {
    if (!k) return void (plain += t);
    if (plain) out.push([plain]), (plain = "");
    out.push([t, k]);
  };
  const docs: { end: string; dash: boolean }[] = [];
  let lineStart = 0; // where this line began, to guess a here-document's language from it
  let head = true; // the next word is a command
  let i = 0;
  while (i < s.length) {
    const c = s[i]!;
    if (c === "\n") {
      push(c);
      i++;
      head = head || s[i - 2] !== "\\";
      // Here-documents begin on the next line, each to its own end word.
      for (const d of docs.splice(0)) {
        const lang = langOf(docLang(s.slice(lineStart, i)));
        const re = new RegExp(`^${d.dash ? "\\t*" : ""}${d.end.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}$`, "m");
        const m = re.exec(s.slice(i));
        const bodyEnd = m ? i + m.index : s.length;
        const body = s.slice(i, bodyEnd);
        if (lang) body && out.push(...flush(), ...highlight(body, lang));
        else push(body, "str");
        i = bodyEnd;
        if (m) {
          push(m[0], "op");
          i += m[0].length;
        }
      }
      lineStart = i;
      head = true;
      continue;
    }
    if (c === " " || c === "\t") {
      push(c);
      i++;
      continue;
    }
    if (c === "\\") {
      push(s.slice(i, i + 2));
      i += 2;
      continue;
    }
    if (c === "#" && /[\s;|&(]/.test(s[i - 1] ?? " ")) {
      const e = lineEnd(s, i);
      push(s.slice(i, e), "cmt");
      i = e;
      continue;
    }
    const doc = /^<<(-?)\s*(['"]?)([\w.-]+)\2/.exec(s.slice(i));
    if (doc) {
      docs.push({ end: doc[3]!, dash: doc[1] === "-" });
      push(doc[0], "op");
      i += doc[0].length;
      continue;
    }
    const op = /^(&&|\|\||;;|[0-9]?>>?&?[0-9-]?|<<<|[|;&<()]|\$\(|`)/.exec(s.slice(i));
    if (op) {
      push(op[0], "op");
      i += op[0].length;
      if (/^(&&|\|\||;;|[|;&(]|\$\(|`)$/.test(op[0])) head = true;
      continue;
    }
    // A word: its quoted parts and $variables shown as such, the rest by where it stands.
    let j = i;
    const parts: Piece[] = [];
    let bare = "";
    const flushBare = () => bare && (parts.push([bare]), (bare = ""));
    while (j < s.length && !/[\s|;&<>()`]/.test(s[j]!)) {
      const d = s[j]!;
      if (d === "'") {
        flushBare();
        const e = close(s, j + 1, "'");
        parts.push([s.slice(j, e), "str"]);
        j = e;
      } else if (d === '"') {
        flushBare();
        const e = quoted(s, j, '"', true);
        parts.push([s.slice(j, e), "str"]);
        j = e;
      } else if (d === "$" && s[j + 1] !== "(") {
        flushBare();
        const m = /^\$(\{[^}\n]*\}?|[\w@*#?$!-])?[\w]*/.exec(s.slice(j))![0];
        parts.push([m, "var"]);
        j += m.length;
      } else if (d === "$") {
        break;
      } else if (d === "\\") {
        bare += s.slice(j, j + 2);
        j += 2;
      } else {
        bare += d;
        j++;
      }
    }
    flushBare();
    if (j === i) {
      push(c);
      i++;
      continue;
    }
    const word = s.slice(i, j);
    const one = parts.length === 1 && !parts[0]![1];
    let kind: Kind | undefined;
    if (one && head && SH_KW.has(word)) kind = "kw";
    else if (one && head && /^[A-Za-z_]\w*=/.test(word)) kind = undefined;
    else if (one && head) kind = "cmd";
    else if (one && /^--?[A-Za-z]/.test(word)) kind = "flag";
    if (one) push(word, kind);
    else for (const p of parts) push(p[0], p[1]);
    if (head) head = (one && (SH_KW.has(word) || SH_PRE.has(word))) || /^[A-Za-z_]\w*=/.test(word);
    i = j;
  }
  if (plain) out.push([plain]);
  return out;

  function flush(): Piece[] {
    const p: Piece[] = plain ? [[plain]] : [];
    plain = "";
    return p;
  }
}

/** The language a here-document's line writes it in: a file it goes to, or the program reading it. */
function docLang(line: string) {
  const file = /(?:>>?|\btee(?:\s+-a)?)\s*['"]?([^\s'"|;&]+)/.exec(line)?.[1];
  if (file && langOf(file)) return file;
  const prog = /\b(python3?|node|bash|sh|zsh|go)\b/.exec(line)?.[1];
  return prog === "go" ? undefined : prog;
}
