import { describe, expect, it } from "vitest";
import { highlight, langOf } from "../src/chat/hl-lib";
import { partialInput } from "../src/chat/partial";

describe("hl", () => {
  it("names a language by fence or file", () => {
    expect(langOf("bash")).toBe("bash");
    expect(langOf("/x/y/main.go")).toBe("go");
    expect(langOf("web/src/a.tsx")).toBe("tsx");
    expect(langOf("notes.txt")).toBeUndefined();
    expect(highlight("x", "notes.txt")).toBeNull();
  });

  it("colours a command, escaped", () => {
    const r = highlight("echo '<b>' && ls -la $HOME", "bash")!;
    expect(r.lang).toBe("bash");
    expect(r.html).toContain("hljs-");
    expect(r.html).not.toContain("<b>");
  });

  it("guesses output only when sure", () => {
    expect(highlight("exit 0\n9\n0.1.30\n\n[exited with code 0]", "auto")).toBeNull();
    expect(highlight("ok  \tgithub.com/x/y\t0.12s\n--- FAIL: TestX (0.00s)\n    a_test.go:12: got 1", "auto")).toBeNull();
    const row = "session-0ee dsh idle    otae-2:C:\\Code\\Ds_Benchmark\\ds-1017 用SVG绘制\n";
    expect(highlight("4393fdf8    dsh idle    otae-2:C:\\Code 运行 hostname 并发送\n" + row.repeat(30) + "=== read ===\n> [ir:83d26b50-0b41] 运行 ir send 83d26b50 'hello: <x>'", "auto")).toBeNull();
    expect(highlight("package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"hi\")\n}\n", "auto")?.lang).toBe("go");
    expect(highlight('{\n  "a": [1, 2],\n  "b": {"c": true, "d": null}\n}', "auto")?.lang).toBe("json");
    expect(highlight("None {'findings': [{'title': 'x', 'line': 177, 'ok': True}]}", "auto")?.lang).toBe("python");
    expect(highlight("diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -1,2 +1,2 @@\n-old\n+new\n same", "auto")?.lang).toBe("diff");
  });
});

it("reads an input cut short field by field", () => {
  const raw = '{\n  "command": "cat > a.py <<\'EOF\'\\nimport sys\\nEOF\\ngit st", "description": "x';
  expect(partialInput(raw)).toEqual({ command: "cat > a.py <<'EOF'\nimport sys\nEOF\ngit st", description: "x" });
});
