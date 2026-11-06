// Records `dsh --profile sdk` as Iridium's holder does: out.log is stdout byte for byte, in.log
// each line written to stdin after out.log's size at the time and a space.
// Usage: node record.mjs <dir> <steps.json> [patch]
// steps: {send:{method,params}, reply?:false} | {wait:ms} | {until:"idle"} | {kill:"SIGTERM"}
// "answers": results for server requests, in order (answered 300ms later).
import { spawn } from "node:child_process";
import fs from "node:fs";

const [dir, stepsFile, patch] = process.argv.slice(2);
const steps = JSON.parse(fs.readFileSync(stepsFile, "utf8"));
fs.mkdirSync(dir, { recursive: true });
const out = fs.openSync(`${dir}/out.log`, "w");
const inl = fs.openSync(`${dir}/in.log`, "w");
const args = ["--profile", "sdk", ...(patch ? ["--patch", patch] : [])];
const p = spawn("dsh", args, { env: process.env, stdio: ["pipe", "pipe", "pipe"] });
p.stderr.pipe(fs.createWriteStream(`${dir}/err.log`));
let size = 0;
let buf = "";
const seen = [];
const answers = steps.answers || [];
let seq = 0;
const send = (msg) => {
  const line = JSON.stringify(msg);
  fs.writeSync(inl, `${size} ${line}\n`);
  p.stdin.write(line + "\n");
};
p.stdout.on("data", (d) => {
  fs.writeSync(out, d);
  size += d.length;
  buf += d.toString("utf8");
  let i;
  while ((i = buf.indexOf("\n")) >= 0) {
    const line = buf.slice(0, i);
    buf = buf.slice(i + 1);
    let m;
    try { m = JSON.parse(line); } catch { continue; }
    seen.push(m);
    if (m.id !== undefined && m.method) {
      const a = answers.shift();
      if (a !== undefined) setTimeout(() => send({ jsonrpc: "2.0", id: m.id, result: a }), 300);
    }
  }
});
let exited = false;
p.on("exit", () => { exited = true; });
const until = (pred, ms = 30000) => new Promise((res) => {
  const start = seen.length;
  const t0 = Date.now();
  const iv = setInterval(() => {
    if (seen.slice(start).some(pred) || Date.now() - t0 > ms || exited) { clearInterval(iv); res(); }
  }, 20);
});
for (const s of steps.steps) {
  if (s.send) {
    const id = `p-${++seq}`;
    send({ jsonrpc: "2.0", id, method: s.send.method, ...(s.send.params ? { params: s.send.params } : {}) });
    if (s.reply !== false) await until((m) => m.id === id && !m.method);
  } else if (s.wait) {
    await new Promise((r) => setTimeout(r, s.wait));
  } else if (s.until) {
    await until((m) => m.method === "session.status" && m.params?.status === s.until, s.ms || 30000);
  } else if (s.kill) {
    p.kill(s.kill);
  }
}
if (!exited) { p.stdin.end(); await new Promise((r) => setTimeout(r, 7000)); if (!exited) p.kill("SIGTERM"); }
await new Promise((r) => setTimeout(r, 300));
