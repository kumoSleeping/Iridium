import { describe, expect, it } from "vitest";
import { highlight, langOf, type Kind } from "../src/chat/hl";

const kinds = (text: string, lang: Parameters<typeof highlight>[1], k: Kind) =>
  highlight(text, lang)
    .filter((p) => p[1] === k)
    .map((p) => p[0]);

describe("hl", () => {
  it("joins back to the text", () => {
    for (const [s, l] of [
      ["cd /a && FOO=1 go test ./... 2>&1 | head -n 5 # all\necho \"$HOME/${X}\" 'q'", "sh"],
      ["x = '''a\nb''' # c\ndef f(): return 0x1F", "py"],
      ["const a = `t ${b}` /* c */ // d\nlet n = 1.5e3", "js"],
      ['echo "unterminated', "sh"],
    ] as const)
      expect(highlight(s, l).map((p) => p[0]).join("")).toBe(s);
  });

  it("knows a shell's commands, flags, strings, variables", () => {
    const s = "sudo FOO=1 git commit -m \"x $Y\" && ls -la | grep -n 'a b'; if true; then echo $HOME; fi";
    expect(kinds(s, "sh", "cmd")).toEqual(["sudo", "git", "ls", "grep", "true", "echo"]);
    expect(kinds(s, "sh", "kw")).toEqual(["if", "then", "fi"]);
    expect(kinds(s, "sh", "flag")).toEqual(["-m", "-la", "-n"]);
    expect(kinds(s, "sh", "str")).toEqual(['"x $Y"', "'a b'"]);
    expect(kinds(s, "sh", "var")).toEqual(["$HOME"]);
  });

  it("colours a here-document in the language it is written to", () => {
    const s = "cat > /tmp/a.py <<'EOF'\nimport sys # c\nEOF\npython3 /tmp/a.py";
    expect(kinds(s, "sh", "kw")).toEqual(["import"]);
    expect(kinds(s, "sh", "cmt")).toEqual(["# c"]);
    expect(kinds(s, "sh", "op")).toContain("EOF");
    expect(kinds(s, "sh", "cmd")).toEqual(["cat", "python3"]);
    expect(kinds("cat > a.md <<EOF\nif x\nEOF", "sh", "str")).toEqual(["if x\n"]);
  });

  it("names a language by fence or file", () => {
    expect(langOf("bash")).toBe("sh");
    expect(langOf("/x/y/main.go")).toBe("go");
    expect(langOf("web/src/a.tsx")).toBe("js");
    expect(langOf("README.md")).toBeUndefined();
  });
});

describe("hl output", () => {
  const out = (s: string, k: Kind) => kinds(s, "out", k);
  it("joins back and finds literals, quotes, places", () => {
    const s = "None {'findings': [{'line': 177, 'ok': True, 'detail': 'The original lis\nit's at internal/a.go:12:3 --- FAIL\nok  \tpkg\t0.12s";
    expect(highlight(s, "out").map((p) => p[0]).join("")).toBe(s);
    expect(out(s, "kw")).toEqual(["None", "True"]);
    expect(out(s, "str")).toEqual(["'findings'", "'line'", "'ok'", "'detail'"]);
    expect(out(s, "num")).toEqual(["177", "0.12"]);
    expect(out(s, "cmd")).toEqual(["internal/a.go:12:3"]);
    expect(out(s, "minus")).toEqual(["FAIL"]);
    expect(out(s, "plus")).toEqual(["ok"]);
  });
  it("colours a diff by line", () => {
    const s = "diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -1 +1 @@\n-old\n+new\n same";
    expect(out(s, "minus")).toEqual(["-old\n"]);
    expect(out(s, "plus")).toEqual(["+new\n"]);
  });
});
