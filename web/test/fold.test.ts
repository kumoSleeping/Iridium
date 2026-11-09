import { partialInput, tail } from "../src/chat/partial";
// A fold's line: the lines each kind of edit adds and takes, and the line read out.
import { afterAll, beforeAll, describe, expect, test } from "vitest";
import type { Block } from "../src/api/types.gen";
import { cmdOf, cmdOfCall, renameOnly } from "../src/chat/cmd";
import { argOf, diffOf, diffSum, diffText, folded, foldLine, foldSaid, lineDiff, patchDiff, pictureName, runCmds, type Step } from "../src/chat/fold";
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

describe("a shell call's sort", () => {
  test("ir's commands", () => {
    expect(cmdOf('ir send abc "hi"')).toBe("ir-send");
    expect(cmdOf('cd ~/x && ir send abc "hi"')).toBe("ir-send");
    expect(cmdOf("IRIDIUM_X=1 ir archive ab")).toBe("ir-archive");
    expect(cmdOf("ir unarchive ab")).toBe("ir-unarchive");
    expect(cmdOf("ir new claude --cwd ~/x 'go'")).toBe("ir-new");
    expect(cmdOf("ir wait ab --timeout 60")).toBe("ir-wait");
    expect(cmdOf("ir read ab | tail -20")).toBe("ir-read");
    expect(cmdOf("ir ls --all")).toBe("ir-list");
    expect(cmdOf("ir find holder")).toBe("ir-find");
    expect(cmdOf("ir stop ab")).toBe("ir-stop");
    expect(cmdOf("ir cp a.txt kumo-mac:~/")).toBe("ir-files");
    expect(cmdOf("ir files ab")).toBe("ir-files");
    expect(cmdOf("~/.local/bin/iridium send ab x")).toBe("ir-send");
    expect(cmdOf("ir invite --ttl 1h")).toBe("ir-access");
    expect(cmdOf("ir access --claude off")).toBe("ir-access");
    expect(cmdOf("ir revoke ab")).toBe("ir-access");
    expect(cmdOf("ir profile kumo --name K")).toBe("ir-profile");
    expect(cmdOf("ir public off")).toBe("net");
    expect(cmdOf("ir update --check")).toBe("install");
    expect(cmdOf("ir ws rename ab docs")).toBe("ir-ws");
    expect(cmdOf("ir machines")).toBe("ir-machines");
    expect(cmdOf("ir sh kumo-mac --cwd ~/x -- git status")).toBe("remote");
    expect(cmdOf("ir secret set gh --note x")).toBe("ir-secret");
    expect(cmdOf("ir run --secret gh -- gh pr list")).toBe("ir-secret");
    expect(cmdOf("ir usage me -d 7")).toBe("ir-usage");
    expect(cmdOf('ir bot "done"')).toBe("ir-bot");
    expect(cmdOf("ir pair host:7810 --name b --into f")).toBe("ir-access");
    expect(cmdOf("ir join host:7810")).toBe("remote");
    expect(cmdOf("ir rename 'title'")).toBe("ir-rename");
    expect(cmdOf("ir --help")).toBeUndefined();
  });
  test("git, past its own options", () => {
    expect(cmdOf("git -C repo commit -m x")).toBe("git-commit");
    expect(cmdOf("git -c user.name=x --no-pager log --oneline -5")).toBe("git-log");
    expect(cmdOf("git push origin main")).toBe("git-push");
    expect(cmdOf("git diff --stat | head")).toBe("git-diff");
    expect(cmdOf("git status --short")).toBe("git-status");
    expect(cmdOf("git pull --rebase")).toBe("git-pull");
    expect(cmdOf("git grep -n foo")).toBe("search");
    expect(cmdOf("git stash list")).toBe("git");
  });
  test("tests, builds, installs", () => {
    expect(cmdOf("go test ./...")).toBe("test");
    expect(cmdOf("~/.local/bin/go vet ./...")).toBe("test");
    expect(cmdOf("npm test")).toBe("test");
    expect(cmdOf("cd web && npm run check 2>&1 | tail -30")).toBe("test");
    expect(cmdOf("npx vitest run test/fold.test.ts")).toBe("test");
    expect(cmdOf("go build -o /tmp/ir ./cmd/iridium")).toBe("build");
    expect(cmdOf("npm --prefix web run build")).toBe("build");
    expect(cmdOf("npm ci")).toBe("install");
    expect(cmdOf("make")).toBe("build");
  });
  test("the network, other machines, services", () => {
    expect(cmdOf("curl -s http://127.0.0.1:7810/api/home | jq .")).toBe("net");
    expect(cmdOf("wget -q https://x/y")).toBe("net");
    expect(cmdOf("ssh kumo-mac 'ls ~'")).toBe("remote");
    expect(cmdOf("scp a kumo-mac:/tmp")).toBe("remote");
    expect(cmdOf("rsync -av a/ b/")).toBe("remote");
    expect(cmdOf("sudo systemctl --user status iridium-test-x")).toBe("service");
    expect(cmdOf("docker ps")).toBe("service");
  });
  test("searching, reading, files, scripts", () => {
    expect(cmdOf('rg -n "kindOf" src')).toBe("search");
    expect(cmdOf("grep -rn foo . | head")).toBe("search");
    expect(cmdOf("find . -name '*.ts'")).toBe("search");
    expect(cmdOf("cat a.txt")).toBe("read");
    expect(cmdOf("sed -n 1,40p src/a.ts")).toBe("read");
    expect(cmdOf("head -5 a; tail b")).toBe("read");
    expect(cmdOf("ls -la src")).toBe("list");
    expect(cmdOf("sed -i 's/a/b/' x")).toBe("file");
    expect(cmdOf("cat > out.txt <<'EOF'\nhi\nEOF")).toBe("file");
    expect(cmdOf("echo x 2>/dev/null")).toBeUndefined();
    expect(cmdOf("mkdir -p a/b && mv x a/b")).toBe("file");
    expect(cmdOf("rm -rf /tmp/x")).toBe("delete");
    expect(cmdOf("python3 - <<'EOF'\nprint(1)\nEOF")).toBe("script");
    expect(cmdOf("node scripts/seed.mjs")).toBe("script");
  });
  test("past cd, variables, sudo, env, wrappers and comments; through pipes and bash -c", () => {
    expect(cmdOf("env -u X FOO=1 BAR='a b' git status")).toBe("git-status");
    expect(cmdOf("export X=1; cd /tmp && timeout 30 go test ./...")).toBe("test");
    expect(cmdOf("echo hi | ir send ab -")).toBe("ir-send");
    expect(cmdOf("cat log | grep err")).toBe("read");
    expect(cmdOf("X=$(git rev-parse HEAD) && git show $X")).toBe("git-diff");
    expect(cmdOf("(cd web && npm test)")).toBe("test");
    expect(cmdOf("# look\nls")).toBe("list");
    expect(cmdOf("bash -lc 'cd /x && git log -1'")).toBe("git-log");
    expect(cmdOf("for f in *.ts; do wc -l $f; done")).toBe("read");
  });
  test("a command we don't know falls to its description, then to none", () => {
    expect(cmdOf("./scripts/check.sh", "Run the test suite")).toBe("test");
    expect(cmdOf("frobnicate", "构建前端")).toBe("build");
    expect(cmdOf("frobnicate")).toBeUndefined();
    expect(cmdOf("", "")).toBeUndefined();
    expect(cmdOf("go test ./...", "Build it")).toBe("test");
  });
  test("from a call: Bash, pi's bash and Codex's exec_command and shell; other tools have none", () => {
    expect(cmdOfCall({ name: "Bash", input: JSON.stringify({ command: "git -C r commit -m x", description: "Commit" }) })).toBe("git-commit");
    expect(cmdOfCall({ name: "bash", input: JSON.stringify({ command: "ir wait ab" }) })).toBe("ir-wait");
    expect(cmdOfCall({ name: "exec_command", input: JSON.stringify({ cmd: "rg foo" }) })).toBe("search");
    expect(cmdOfCall({ name: "shell", input: JSON.stringify({ command: ["bash", "-lc", "go test ./..."] }) })).toBe("test");
    expect(cmdOfCall({ name: "Bash", input: '{"command":"curl -s http://x/a?q=1 | jq' })).toBe("net");
    expect(cmdOfCall({ name: "Grep", input: JSON.stringify({ pattern: "x" }) })).toBeUndefined();
    expect(cmdOfCall({ name: "Bash", input: JSON.stringify({ command: "frobnicate" }) })).toBeUndefined();
  });
  test("a call that only names the session (ir rename) is left out of the chat", () => {
    const r = (name: string, command: unknown) => renameOnly({ name, input: JSON.stringify({ command }) });
    expect(r("Bash", 'ir rename "会话命名"')).toBe(true);
    expect(r("exec_command", "bash -lc 'ir rename x'")).toBe(true);
    expect(r("shell", ["bash", "-lc", "ir rename x"])).toBe(true);
    expect(renameOnly({ name: "Bash", input: '{"command":"ir rename \\"会话' })).toBe(true);
    expect(r("Bash", "ir rename x && rm -rf y")).toBe(false);
    expect(r("Bash", "ir rename x > /tmp/y")).toBe(false);
    expect(r("Bash", "ir ls")).toBe(false);
    expect(r("Read", "ir rename x")).toBe(false);
    const shown = folded([
      { at: "0", kind: "user", text: "go" },
      { at: "1", kind: "tool", id: "a", name: "Bash", input: JSON.stringify({ command: "ir rename x" }) },
      { at: "2", kind: "result", id: "a", text: "" },
      { at: "3", kind: "assistant", text: "ok" },
    ]);
    expect(shown.map((s) => s.kind)).toEqual(["user", "assistant"]);
  });
  test("a fold's run of one name counts its calls by sort, in order", () => {
    const think: Step = { at: "t", kind: "thinking", text: "hmm" };
    expect(runCmds([call("Bash", { command: "ir send a x" }), think, call("Read", { file_path: "/a" }), call("Bash", { command: "cd ~ && ir send b y" })], "Bash")).toEqual([{ cmd: "ir-send", n: 2 }]);
    expect(runCmds([call("Bash", { command: "ir send a x" }), call("Bash", { command: "git status" }), call("Bash", { command: "ir send a x" })], "Bash")).toEqual([{ cmd: "ir-send", n: 2 }, { cmd: "git-status", n: 1 }]);
    expect(runCmds([call("Bash", { command: "ir send a x" }), call("Bash", { command: "frobnicate" })], "Bash")).toEqual([{ cmd: "ir-send", n: 1 }, { cmd: undefined, n: 1 }]);
    expect(runCmds([call("Read", { file_path: "/a" }), call("Read", { file_path: "/b" })], "Read")).toEqual([{ cmd: undefined, n: 2 }]);
  });
});

describe("a call's input as it is written", () => {
  test("the string fields so far, escapes read, one cut in an escape as written", () => {
    expect(partialInput('{"command":"ir send ab \\"hi')).toEqual({ command: 'ir send ab "hi' });
    expect(partialInput('{"file_path":"/a/b.ts","old_string":"x\\ny","new')).toEqual({ file_path: "/a/b.ts", old_string: "x\ny" });
    expect(partialInput('{"command":"echo \\')).toEqual({ command: "echo " });
    expect(partialInput("")).toEqual({});
  });
  test("the tail shows what is being typed", () => {
    expect(tail("a\n  b", 90)).toBe("a b");
    expect(tail("x".repeat(100), 10)).toBe("…" + "x".repeat(10));
  });
});

describe("a step's pictures", () => {
  test("a result on another page brings its pictures to its call", () => {
    const images = [{ mime: "image/png", w: 640, h: 420 }];
    const shown = folded([
      { at: "1.0", kind: "result", id: "c", text: "Read image file", images },
      { at: "2.0", kind: "tool", id: "c", name: "read", input: '{"path":"/w/chart.png"}' },
      { at: "3.0", kind: "assistant", text: "a chart" },
    ]);
    const fold = shown[0];
    expect(fold?.kind === "fold" && fold.steps[0]?.result?.images).toEqual(images);
  });
  test("they are downloaded by the picture's own name, else the tool's, as what they are", () => {
    expect(pictureName(call("Read", { file_path: "/w/shots/login.PNG" }), "image/png")).toBe("login.png");
    expect(pictureName(call("read_image", { file_path: "C:\\Users\\kumo\\err.webp" }), "image/webp")).toBe("err.webp");
    expect(pictureName(call("read", { path: "/w/a.png" }), "image/jpeg")).toBe("a.jpg");
    expect(pictureName(call("view_image", { path: "/w/notes.txt" }))).toBe("view_image.png");
    expect(pictureName(call("mcp__browser__screenshot", {}), "image/jpeg")).toBe("mcp__browser__screenshot.jpg");
  });
});
