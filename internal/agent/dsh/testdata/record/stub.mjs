// A fake Anthropic Messages endpoint for recording dsh's SDK traffic without a real model.
// Every request is logged to $STUB_LOG. Replies: if the last user turn says RUNTOOL:<name> and
// carries no tool result yet, a tool_use of that tool (input from STUB_INPUT_<name> JSON, else
// {"command":"echo hi"}); if it says SLOW, the text streams over 6s; else a short text.
import http from "node:http";
import fs from "node:fs";

const log = process.env.STUB_LOG || "/dev/null";
let n = 0;
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

function lastUser(body) {
  const msgs = body.messages || [];
  const m = msgs[msgs.length - 1];
  if (!m) return { text: "", hasResult: false };
  const parts = typeof m.content === "string" ? [{ type: "text", text: m.content }] : m.content || [];
  const text = parts.filter((p) => p.type === "text").map((p) => p.text).join("\n");
  const hasResult = parts.some((p) => p.type === "tool_result");
  // The prompt as first given: search every user message for the directive.
  const all = msgs.filter((x) => x.role === "user").map((x) => (typeof x.content === "string" ? x.content : (x.content || []).filter((p) => p.type === "text").map((p) => p.text).join("\n"))).join("\n");
  return { text, all, hasResult };
}

http
  .createServer(async (req, res) => {
    let raw = "";
    for await (const c of req) raw += c;
    const id = ++n;
    let body = {};
    try { body = JSON.parse(raw || "{}"); } catch {}
    fs.appendFileSync(log, JSON.stringify({ id, method: req.method, url: req.url, headers: { ...req.headers, authorization: undefined, "x-api-key": undefined }, body }) + "\n");
    if (!req.url.includes("/messages")) {
      res.writeHead(404, { "content-type": "application/json" });
      return res.end(JSON.stringify({ type: "error", error: { type: "not_found_error", message: "stub" } }));
    }
    const u = lastUser(body);
    const tool = /RUNTOOL:(\w+)/.exec(u.all || "")?.[1];
    const slow = /SLOW/.test(u.text);
    const model = body.model || "stub";
    if (!body.stream) {
      res.writeHead(200, { "content-type": "application/json" });
      return res.end(JSON.stringify({ id: `msg_${id}`, type: "message", role: "assistant", model, content: [{ type: "text", text: "Stub title" }], stop_reason: "end_turn", usage: { input_tokens: 10, output_tokens: 3 } }));
    }
    res.writeHead(200, { "content-type": "text/event-stream", "cache-control": "no-cache" });
    const ev = (type, data) => res.write(`event: ${type}\ndata: ${JSON.stringify({ type, ...data })}\n\n`);
    ev("message_start", { message: { id: `msg_${id}`, type: "message", role: "assistant", model, content: [], stop_reason: null, usage: { input_tokens: 100, output_tokens: 1, cache_read_input_tokens: 40 } } });
    if (tool && !u.hasResult) {
      let inputs = {}; try { inputs = JSON.parse(fs.readFileSync(process.env.STUB_INPUTS || new URL("inputs.json", import.meta.url).pathname, "utf8")); } catch {}
      const key = /INPUT:(\w+)/.exec(u.all || "")?.[1] ?? tool;
      const input = inputs[key] ? JSON.stringify(inputs[key]) : JSON.stringify({ command: "echo hi" });
      ev("content_block_start", { index: 0, content_block: { type: "text", text: "" } });
      ev("content_block_delta", { index: 0, delta: { type: "text_delta", text: "Running it." } });
      ev("content_block_stop", { index: 0 });
      ev("content_block_start", { index: 1, content_block: { type: "tool_use", id: `toolu_${id}`, name: tool, input: {} } });
      ev("content_block_delta", { index: 1, delta: { type: "input_json_delta", partial_json: input } });
      ev("content_block_stop", { index: 1 });
      ev("message_delta", { delta: { stop_reason: "tool_use", stop_sequence: null }, usage: { output_tokens: 20 } });
    } else {
      ev("content_block_start", { index: 0, content_block: { type: "text", text: "" } });
      const words = (u.hasResult ? "Done after the tool." : `Echo: ${u.text.slice(0, 60)}`).split(" ");
      for (const w of words) {
        if (slow) await sleep(6000 / words.length);
        ev("content_block_delta", { index: 0, delta: { type: "text_delta", text: w + " " } });
      }
      ev("content_block_stop", { index: 0 });
      ev("message_delta", { delta: { stop_reason: "end_turn", stop_sequence: null }, usage: { output_tokens: 12 } });
    }
    ev("message_stop", {});
    res.end();
  })
  .listen(Number(process.env.STUB_PORT || 18732), "127.0.0.1", () => console.error("stub listening"));
