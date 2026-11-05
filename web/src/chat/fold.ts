// The tool calls and thoughts in a chat (Rhodium spec web-chat 196–199): those between two things
// said fold into one line; the calls still running at the end show on their own. Pure, for tests.
import type { Block } from "../api/types.gen";

export type Tool = Block & { kind: "tool" };
export type Step = Block & { kind: "tool" | "thinking" };
/** What the chat shows: what was said and system lines as they come, the steps between them as a
 *  fold, and the calls still running. */
export type Shown = (Block & { kind: "user" | "assistant" | "system" }) | { kind: "fold"; at: string; steps: Step[] } | { kind: "running"; at: string; tool: Tool };

export function folded(blocks: Block[]): Shown[] {
  // A result whose call was on another page goes with it by id.
  const results = new Map(blocks.flatMap((b) => (b.kind === "result" && b.id ? [[b.id, b] as const] : [])));
  const all = blocks.flatMap((b): Block[] => {
    if (b.kind === "result") return [];
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
const lines = (s: unknown) => (typeof s === "string" && s ? s.split("\n").length : 0);

/** What a call is about, on one line: its file, command, pattern, address or what it is told; code:
 *  shown in monospace. stat: an edit's lines added and taken. */
export function argOf(t: { name?: string; input?: string }, cwd?: string): { arg: string; code: boolean; stat?: string } {
  const i = inputOf(t.input);
  const str = (...ks: string[]) => ks.map((k) => i[k]).find((v): v is string => typeof v === "string" && v !== "");
  const name = (t.name ?? "").toLowerCase();
  const file = str("file_path", "notebook_path", "path");
  if (["edit", "multiedit", "write", "notebookedit"].includes(name) && file) {
    const added = name === "write" ? lines(i.content) : name === "multiedit" && Array.isArray(i.edits) ? i.edits.reduce((n: number, e: { new_string?: string }) => n + lines(e.new_string), 0) : lines(i.new_string);
    const taken = name === "write" ? 0 : name === "multiedit" && Array.isArray(i.edits) ? i.edits.reduce((n: number, e: { old_string?: string }) => n + lines(e.old_string), 0) : lines(i.old_string);
    return { arg: rel(file, cwd), code: true, stat: added || taken ? `+${added} −${taken}` : undefined };
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

/** A fold's line: each name once, with ×n for more than one, in the order first called. */
export function foldLine(steps: Step[]): { name: string; n: number; failed: number }[] {
  const runs: { name: string; n: number; failed: number }[] = [];
  for (const s of steps) {
    const name = s.kind === "thinking" ? "" : (s.name ?? "?");
    const r = runs.find((x) => x.name === name);
    const bad = s.kind === "tool" && s.result?.error ? 1 : 0;
    if (r) (r.n++, (r.failed += bad));
    else runs.push({ name, n: 1, failed: bad });
  }
  return runs;
}
