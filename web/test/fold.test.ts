// A fold's line: the lines each kind of edit adds and takes, and the line read out.
import { afterAll, beforeAll, describe, expect, test } from "vitest";
import type { Block } from "../src/api/types.gen";
import { argOf, diffOf, diffSum, diffText, foldLine, foldSaid, lineDiff, patchDiff, type Step } from "../src/chat/fold";
import { setLang } from "../src/i18n";

const call = (name: string, input: unknown, more: Partial<Omit<Block, "kind">> = {}): Step => ({ at: `${name}.${Math.random()}`, kind: "tool", name, input: typeof input === "string" ? input : JSON.stringify(input), result: { at: "r", text: "ok" }, ...more });

describe("lineDiff", () => {
  test("only the lines that differ count, past the common start and end", () => {
    expect(lineDiff("a\nb\nc", "a\nB\nc")).toEqual({ added: 1, taken: 1 });
    expect(lineDiff("a\nc\n", "a\nb\nc\n")).toEqual({ added: 1, taken: 0 });
    expect(lineDiff("a\nb\nc\nd", "a\nd")).toEqual({ added: 0, taken: 2 });
    expect(lineDiff("x\ny\nz", "y\nq\nz\nw")).toEqual({ added: 2, taken: 1 });
    expect(lineDiff("", "one\ntwo")).toEqual({ added: 2, taken: 0 });
    expect(lineDiff(undefined, undefined)).toEqual({ added: 0, taken: 0 });
  });
});

describe("patchDiff", () => {
  test("a unified diff's and an apply_patch's + and − lines, headers left out", () => {
    expect(patchDiff("--- a/x\n+++ b/x\n@@ -1,2 +1,2 @@\n a\n-b\n+B\n+C\n")).toEqual({ added: 2, taken: 1 });
    expect(patchDiff("*** Begin Patch\n*** Add File: n.txt\n+one\n+two\n*** Update File: o.txt\n@@\n-x\n+y\n*** End Patch")).toEqual({ added: 3, taken: 1 });
  });
});

describe("diffOf", () => {
  test("Claude's Edit, MultiEdit, Write and NotebookEdit", () => {
    expect(diffOf({ name: "Edit", input: JSON.stringify({ file_path: "/a", old_string: "a\nb", new_string: "a\nB\nc" }) })).toEqual({ added: 2, taken: 1 });
    expect(diffOf({ name: "MultiEdit", input: JSON.stringify({ file_path: "/a", edits: [{ old_string: "x", new_string: "y" }, { old_string: "p", new_string: "p\nq" }] }) })).toEqual({ added: 2, taken: 1 });
    expect(diffOf({ name: "Write", input: JSON.stringify({ file_path: "/a", content: "1\n2\n3\n" }) })).toEqual({ added: 3, taken: 0 });
    expect(diffOf({ name: "NotebookEdit", input: JSON.stringify({ notebook_path: "/n", new_source: "a\nb" }) })).toEqual({ added: 2, taken: 0 });
  });
  test("pi's edit, one or several, and write", () => {
    expect(diffOf({ name: "edit", input: JSON.stringify({ path: "/a", oldText: "a", newText: "b" }) })).toEqual({ added: 1, taken: 1 });
    expect(diffOf({ name: "edit", input: JSON.stringify({ path: "/a", edits: [{ oldText: "a", newText: "b" }, { oldText: "c\nd", newText: "c" }] }) })).toEqual({ added: 1, taken: 2 });
    expect(diffOf({ name: "write", input: JSON.stringify({ path: "/a", content: "x" }) })).toEqual({ added: 1, taken: 0 });
  });
  test("Codex's apply_patch, live and from its transcript", () => {
    const live = { changes: [{ path: "/w/n.txt", kind: { type: "add" }, diff: "hi\nthere\n" }, { path: "/w/o.txt", kind: { type: "update", move_path: null }, diff: "@@ -1 +1 @@\n-a\n+b\n" }, { path: "/w/d.txt", kind: { type: "delete" }, diff: "gone\n" }] };
    expect(diffOf({ name: "apply_patch", input: JSON.stringify(live) })).toEqual({ added: 3, taken: 2 });
    const kept = { changes: [{ path: "/w/n.txt", type: "add", content: "hi\n" }, { path: "/w/o.txt", type: "update", unified_diff: "@@ -1,2 +1,1 @@\n-a\n-b\n+c\n" }] };
    expect(diffOf({ name: "apply_patch", input: JSON.stringify(kept) })).toEqual({ added: 2, taken: 2 });
    expect(argOf({ name: "apply_patch", input: JSON.stringify(kept) }, "/w")).toEqual({ arg: "n.txt o.txt", code: true, diff: { added: 2, taken: 2 } });
  });
  test("no edit, or one that changes nothing, has none", () => {
    expect(diffOf({ name: "Bash", input: JSON.stringify({ command: "ls" }) })).toBeUndefined();
    expect(diffOf({ name: "Edit", input: JSON.stringify({ file_path: "/a", old_string: "a", new_string: "a" }) })).toBeUndefined();
    expect(diffOf({ name: "Write", input: "{not json" })).toBeUndefined();
  });
  test("an input cut short counts what the page has", () => {
    const whole = JSON.stringify({ file_path: "/a", old_string: "a", new_string: "b\nc", replace_all: false });
    expect(diffOf({ name: "Edit", input: whole.slice(0, whole.indexOf("replace_all")) })).toEqual({ added: 2, taken: 1 });
  });
});

describe("a fold's line", () => {
  const steps: Step[] = [
    call("Bash", { command: "ls" }),
    { at: "t", kind: "thinking", text: "hmm" },
    call("Bash", { command: "go test" }, { result: { at: "r", text: "FAIL", error: true } }),
    call("Edit", { file_path: "/a", old_string: "a\nb", new_string: "a\nB\nc" }),
    call("Write", { file_path: "/b", content: "1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n" }),
    call("Edit", { file_path: "/a", old_string: "x\ny\nz", new_string: "x" }),
    call("Edit", { file_path: "/a", old_string: "q", new_string: "r" }, { result: { at: "r", text: "no match", error: true } }),
  ];
  test("each name once, its count, failures, and the lines its edits changed; failed edits changed none", () => {
    const runs = foldLine(steps);
    expect(runs).toEqual([
      { name: "Bash", n: 2, failed: 1 },
      { name: "", n: 1, failed: 0 },
      { name: "Edit", n: 3, failed: 1, diff: { added: 2, taken: 3 } },
      { name: "Write", n: 1, failed: 0, diff: { added: 10, taken: 0 } },
    ]);
    expect(diffSum(runs)).toEqual({ added: 12, taken: 3 });
    expect(diffSum(foldLine(steps.slice(0, 3)))).toBeUndefined();
    expect(diffText({ added: 12, taken: 3 })).toBe("+12 −3");
    expect(diffText({ added: 10, taken: 0 })).toBe("+10");
    expect(diffText({ added: 0, taken: 4 })).toBe("−4");
  });
  describe("read out", () => {
    beforeAll(() => setLang("en"));
    afterAll(() => setLang("zh"));
    test("in English", () => {
      expect(foldSaid(foldLine(steps))).toBe("Tool calls: Bash ×2, Thinking, Edit ×3 +2 −3, Write +10, 2 failed");
      expect(foldSaid(foldLine(steps.slice(0, 2)))).toBe("Tool calls: Bash, Thinking");
    });
    test("in Chinese", () => {
      setLang("zh");
      expect(foldSaid(foldLine(steps))).toBe("工具调用：Bash ×2、思考、Edit ×3 +2 −3、Write +10、2 个出错");
    });
  });
});
