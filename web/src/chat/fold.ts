// The tool calls and thoughts in a chat (Rhodium spec web-chat 196–199): those between two things
// said fold into one line; the calls still running at the end show on their own. Pure, for tests.
import type { Block } from "../api/types.gen";
import { t } from "../i18n";
import { cmdOfCall, renameOnly, type Cmd } from "./cmd";

export type Tool = Block & { kind: "tool" };
export type Step = Block & { kind: "tool" | "thinking" };
/** What the chat shows: what was said and system lines as they come, the steps between them as a
 *  fold, and the calls still running. */
export type Shown = (Block & { kind: "user" | "assistant" | "system" }) | { kind: "fold"; at: string; steps: Step[] } | { kind: "running"; at: string; tool: Tool };

export function folded(blocks: Block[]): Shown[] {
  // A result whose call was on another page goes with it by id.
  const results = new Map(blocks.flatMap((b) => (b.kind === "result" && b.id ? [[b.id, b] as const] : [])));
  const all = blocks.flatMap((b): Block[] => {
    // An agent naming its session (ir rename) is the list's, not the chat's.
    if (b.kind === "result" || (b.kind === "tool" && renameOnly(b))) return [];
    const r = b.kind === "tool" && !b.result && b.id ? results.get(b.id) : undefined;
    return r ? [{ ...b, result: { at: r.at, time: r.time, text: r.text ?? "", length: r.length, error: r.error } }] : [b];
  });
  let end = all.length;
  while (all[end - 1]?.kind === "tool" || all[end - 1]?.kind === "thinking") end--;
  const running = new Set(all.slice(end).filter((b): b is Tool => b.kind === "tool" && !b.result));
  const shown: Shown[] = [];
  for (const b of all) {
    if (b.kind === "tool" || b.kind === "thinking") {
      if (running.has(b as Tool)) continue;
      // An empty thought says nothing.
      if (b.kind === "thinking" && !b.text?.trim()) continue;
      const last = shown.at(-1);
      if (last?.kind === "fold") last.steps.push(b as Step);
      else shown.push({ kind: "fold", at: b.at, steps: [b as Step] });
    } else shown.push(b as Shown);
  }
  return [...shown, ...[...running].map((tool) => ({ kind: "running" as const, at: tool.at, tool }))];
}

export type Kind = "read" | "search" | "web" | "edit" | "command" | "agent" | "todo" | "ask" | "mcp" | "other" | "thinking";

const NAMES: Record<string, Kind> = {
  read: "read",
  notebookread: "read",
  view_image: "read",
  edit: "edit",
  multiedit: "edit",
  write: "edit",
  notebookedit: "edit",
  apply_patch: "edit",
  bash: "command",
  bashoutput: "command",
  killshell: "command",
  killbash: "command",
  taskoutput: "command",
  taskstop: "command",
  monitor: "command",
  exec_command: "command",
  grep: "search",
  glob: "search",
  find: "search",
  ls: "search",
  webfetch: "web",
  websearch: "web",
  web_search: "web",
  agent: "agent",
  task: "agent",
  sendmessage: "agent",
  spawn_agent: "agent",
  workflow: "agent", // a run of agents in the background
  todowrite: "todo",
  update_plan: "todo",
  askuserquestion: "ask",
  exitplanmode: "ask",
};

/** A call's kind by its name; an MCP server's tools are mcp. */
export const kindOf = (name = ""): Kind => (name.startsWith("mcp__") ? "mcp" : (NAMES[name.toLowerCase()] ?? "other"));
export const kindOfStep = (s: Step): Kind => (s.kind === "thinking" ? "thinking" : kindOf(s.name));

/** The input as an object; one cut short (Block.length) is read for its first string fields. */
export function inputOf(input = ""): Record<string, unknown> {
  try {
    const v = JSON.parse(input);
    return v && typeof v === "object" ? v : {};
  } catch {
    const out: Record<string, unknown> = {};
    for (const m of input.matchAll(/"(\w+)"\s*:\s*"((?:[^"\\]|\\.)*)"/g)) out[m[1]!] ??= m[2]!.replace(/\\n/g, "\n").replace(/\\(.)/g, "$1");
    return out;
  }
}

const rel = (p: string, cwd?: string) => (cwd && p.startsWith(cwd + "/") ? p.slice(cwd.length + 1) : p);

/** Lines added and taken. */
export type Diff = { added: number; taken: number };
const split = (s: unknown): string[] => (typeof s === "string" && s ? s.replace(/\n$/, "").split("\n") : []);
const plus = (a: Diff | undefined, b: Diff): Diff => ({ added: (a?.added ?? 0) + b.added, taken: (a?.taken ?? 0) + b.taken });

/** The lines b adds to a and takes from it: past their common start and end, less the longest run
 *  they share (a middle too long to compare counts whole). */
export function lineDiff(a: unknown, b: unknown): Diff {
  const x = split(a);
  const y = split(b);
  let s = 0;
  while (s < x.length && s < y.length && x[s] === y[s]) s++;
  let e = 0;
  while (e < x.length - s && e < y.length - s && x[x.length - 1 - e] === y[y.length - 1 - e]) e++;
  const p = x.slice(s, x.length - e);
  const q = y.slice(s, y.length - e);
  let common = 0;
  if (p.length && q.length && p.length * q.length <= 250_000) {
    let prev = new Array<number>(q.length + 1).fill(0);
    for (const u of p) {
      const row = [0];
      for (let j = 0; j < q.length; j++) row.push(u === q[j] ? prev[j]! + 1 : Math.max(prev[j + 1]!, row[j]!));
      prev = row;
    }
    common = prev[q.length]!;
  }
  return { added: q.length - common, taken: p.length - common };
}

/** A patch's + and − lines: a unified diff's or a Codex apply_patch's, headers left out. */
export function patchDiff(text: unknown): Diff {
  const d = { added: 0, taken: 0 };
  const ls = typeof text === "string" ? text.split("\n") : [];
  for (let i = 0; i < ls.length; i++) {
    const l = ls[i]!;
    if (l.startsWith("--- ") && ls[i + 1]?.startsWith("+++ ")) i++;
    else if (/^(@@|\*\*\* |diff --git |index )/.test(l)) continue;
    else if (l.startsWith("+")) d.added++;
    else if (l.startsWith("-")) d.taken++;
  }
  return d;
}

/** One of Codex's file changes: {path, kind: {type}, diff} live, {path, type, content | unified_diff}
 *  in its transcript; an added file's text is all added, a deleted one's all taken. */
function changeDiff(c: unknown): Diff {
  const o = (c && typeof c === "object" ? c : {}) as Record<string, unknown>;
  const kind = o.kind && typeof o.kind === "object" ? (o.kind as Record<string, unknown>).type : o.type;
  const text = o.diff ?? o.unified_diff ?? o.content;
  if (kind === "add") return { added: split(text).length, taken: 0 };
  if (kind === "delete") return { added: 0, taken: split(text).length };
  return patchDiff(text);
}

/** An edit call's lines added and taken, from what it was given: Claude's Edit, MultiEdit, Write and
 *  NotebookEdit, pi's edit (oldText/newText, or edits of them) and write, Codex's apply_patch. A
 *  write counts its whole text as added. An input cut short counts what the page has. */
export function diffOf(t: { name?: string; input?: string }): Diff | undefined {
  if (kindOf(t.name) !== "edit") return undefined;
  const name = (t.name ?? "").toLowerCase();
  const i = inputOf(t.input);
  const pair = (e: unknown): Diff => {
    const o = (e && typeof e === "object" ? e : {}) as Record<string, unknown>;
    return lineDiff(o.old_string ?? o.oldText, o.new_string ?? o.newText);
  };
  const sum = (es: unknown[], f: (e: unknown) => Diff) => es.reduce<Diff>((a, e) => plus(a, f(e)), { added: 0, taken: 0 });
  const d =
    name === "write"
      ? { added: split(i.content).length, taken: 0 }
      : name === "notebookedit"
        ? { added: i.edit_mode === "delete" ? 0 : split(i.new_source).length, taken: 0 }
        : name === "apply_patch"
          ? Array.isArray(i.changes)
            ? sum(i.changes, changeDiff)
            : patchDiff(i.input ?? i.patch)
          : Array.isArray(i.edits)
            ? sum(i.edits, pair)
            : pair(i);
  return d.added || d.taken ? d : undefined;
}

/** What a call is about, on one line: its file, command, pattern, address or what it is told; code:
 *  shown in monospace. diff: an edit's lines added and taken. */
export function argOf(t: { name?: string; input?: string }, cwd?: string): { arg: string; code: boolean; diff?: Diff } {
  const i = inputOf(t.input);
  const str = (...ks: string[]) => ks.map((k) => i[k]).find((v): v is string => typeof v === "string" && v !== "");
  const name = (t.name ?? "").toLowerCase();
  const file = str("file_path", "notebook_path", "path");
  if (kindOf(t.name) === "edit") {
    const files = file ? [file] : Array.isArray(i.changes) ? i.changes.flatMap((c: { path?: unknown }) => (typeof c?.path === "string" ? [c.path] : [])) : [];
    if (files.length) return { arg: files.map((f) => rel(f, cwd)).join(" "), code: true, diff: diffOf(t) };
  }
  if (file) return { arg: rel(file, cwd), code: true };
  const cmd = str("command", "cmd");
  if (cmd) return { arg: cmd.replace(/\s+/g, " ").trim(), code: true };
  const pattern = str("pattern", "query", "url");
  if (pattern) return { arg: pattern, code: name !== "websearch" };
  const said = str("description", "prompt", "subject", "plan");
  if (said) return { arg: said.replace(/\s+/g, " ").trim(), code: false };
  if (Array.isArray(i.todos)) return { arg: `${i.todos.length}`, code: false };
  const first = Object.values(i).find((v): v is string => typeof v === "string");
  return { arg: first ? first.replace(/\s+/g, " ").slice(0, 200) : "", code: true };
}

/** A run of calls by one name in a fold: how many, how many failed, the lines its edits changed
 *  (failed ones changed nothing). Thinking's name is "". */
export type Run = { name: string; n: number; failed: number; diff?: Diff };

/** A fold's line: each name once, with ×n for more than one, in the order first called. */
export function foldLine(steps: Step[]): Run[] {
  const runs: Run[] = [];
  for (const s of steps) {
    const name = s.kind === "thinking" ? "" : (s.name ?? "?");
    let r = runs.find((x) => x.name === name);
    if (!r) runs.push((r = { name, n: 0, failed: 0 }));
    r.n++;
    if (s.kind === "tool" && s.result?.error) r.failed++;
    else if (s.kind === "tool") {
      const d = diffOf(s);
      if (d) r.diff = plus(r.diff, d);
    }
  }
  return runs;
}

/** The sorts of shell command the calls by this name in a fold run, in order: one when they all
 *  run the same sort (or none, another tool's), else each call's, for an icon each. */
export function runCmds(steps: Step[], name: string): (Cmd | undefined)[] {
  const cs = steps.filter((s) => s.kind === "tool" && s.name === name).map(cmdOfCall);
  return new Set(cs).size === 1 ? cs.slice(0, 1) : cs;
}

/** All the lines a fold's runs changed; undefined for none. */
export const diffSum = (runs: Run[]): Diff | undefined => runs.reduce<Diff | undefined>((a, r) => (r.diff ? plus(a, r.diff) : a), undefined);

/** "+12 −3", either side left out at 0. */
export const diffText = (d: Diff) => [d.added && `+${d.added}`, d.taken && `−${d.taken}`].filter(Boolean).join(" ");

/** A fold's line said: "Tool calls: Bash ×2, Thinking, Edit +12 −3, 1 failed". */
export function foldSaid(runs: Run[]): string {
  const bad = runs.reduce((n, r) => n + r.failed, 0);
  const said = runs.map((r) => [r.name || t("msg.thinking"), r.n > 1 && `×${r.n}`, r.diff && diffText(r.diff)].filter(Boolean).join(" "));
  if (bad) said.push(t("msg.failedN", { n: bad }));
  return t("msg.toolsSaid", { list: said.join(t("msg.sep")) });
}
