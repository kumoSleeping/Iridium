// Fills a test daemon (scripts/test-deploy.sh up) with what the screenshots and the end-to-end run
// look for: the workspaces Iridium (with a folder web) and KDJ, Claude sessions on disk with tool
// calls, a picture and a file in a reply, one of them taken over and hosted (its context and
// quota), a hosted "Style sample" waiting on an approval, a letter from another session's ir, and a
// session in no workspace. Only for a test daemon with the fake claude: never a real one.
//   node scripts/seed.mjs <dir of test-deploy.sh>
import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { request } from "node:http";
import { randomUUID } from "node:crypto";
import { crc32, deflateSync } from "node:zlib";

const dir = process.argv[2] ?? `${process.env.TMPDIR ?? "/tmp"}/iridium-test-deploy`;
const env = Object.fromEntries(
  readFileSync(`${dir}/env`, "utf8")
    .split("\n")
    .filter((l) => l.includes("="))
    .map((l) => [l.slice(0, l.indexOf("=")), l.slice(l.indexOf("=") + 1)]),
);
const BASE = env.IRIDIUM_TEST_BASE;
const HOME = env.IRIDIUM_TEST_HOME;
const CLAUDE = env.IRIDIUM_TEST_CLAUDE;
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

async function api(method, path, body) {
  const r = await fetch(new URL(path, BASE), { method, headers: body ? { "content-type": "application/json" } : {}, body: body ? JSON.stringify(body) : undefined });
  const text = await r.text();
  if (!r.ok) throw new Error(`${method} ${path}: ${r.status} ${text}`);
  return text ? JSON.parse(text) : {};
}

/** A request on the unix socket, as an agent's ir in session `from` makes it. */
function asAgent(method, path, body, from) {
  return new Promise((resolve, reject) => {
    const b = JSON.stringify(body);
    const req = request({ socketPath: env.IRIDIUM_SOCKET, path, method, headers: { "content-type": "application/json", "content-length": Buffer.byteLength(b), "x-iridium-agent": "1", "x-iridium-session": from } }, (res) => {
      let out = "";
      res.on("data", (d) => (out += d));
      res.on("end", () => (res.statusCode < 300 ? resolve(out ? JSON.parse(out) : {}) : reject(new Error(`${path}: ${res.statusCode} ${out}`))));
    });
    req.on("error", reject);
    req.end(b);
  });
}

async function until(what, f, ms = 15000) {
  for (const end = Date.now() + ms; Date.now() < end; await sleep(150)) {
    const v = await f();
    if (v) return v;
  }
  throw new Error(`timed out: ${what}`);
}

/** A small PNG: a soft gradient, as a screenshot in a reply. */
function png(w, h) {
  const rows = [];
  for (let y = 0; y < h; y++) {
    const row = Buffer.alloc(1 + w * 3);
    for (let x = 0; x < w; x++) {
      row[1 + x * 3] = 150 + Math.round((x / w) * 60);
      row[2 + x * 3] = 180 + Math.round((y / h) * 40);
      row[3 + x * 3] = 220 - Math.round((x / w) * 40);
    }
    rows.push(row);
  }
  const chunk = (type, data) => {
    const len = Buffer.alloc(4);
    len.writeUInt32BE(data.length);
    const td = Buffer.concat([Buffer.from(type), data]);
    const crc = Buffer.alloc(4);
    crc.writeUInt32BE(crc32(td));
    return Buffer.concat([len, td, crc]);
  };
  const ihdr = Buffer.alloc(13);
  ihdr.writeUInt32BE(w, 0);
  ihdr.writeUInt32BE(h, 4);
  ihdr[8] = 8;
  ihdr[9] = 2;
  return Buffer.concat([Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]), chunk("IHDR", ihdr), chunk("IDAT", deflateSync(Buffer.concat(rows))), chunk("IEND", Buffer.alloc(0))]);
}

const iridium = `${HOME}/kumo/Iridium`;
const kdj = `${HOME}/kumo/KDJ`;
for (const d of [`${iridium}/web`, `${iridium}/internal/hold`, `${iridium}/docs`, `${kdj}/app`, `${HOME}/notes`]) mkdirSync(d, { recursive: true });
writeFileSync(`${iridium}/docs/pipe.png`, png(480, 300));
writeFileSync(`${iridium}/docs/notes.md`, "# The named pipe\n\nThe holder opens the pipe before the agent starts, so a writer never meets a closed end.\n");
writeFileSync(`${iridium}/internal/hold/hold.go`, "package hold\n");

// The workspaces first: what is seen after them goes into them.
const have = await api("GET", "/api/workspaces");
for (const path of [iridium, kdj]) if (!have.some((w) => w.path === path)) await api("POST", "/api/workspaces", { path });

// Claude sessions on disk, as Claude writes them.
const ago = (min) => new Date(Date.now() - min * 60_000).toISOString();
function transcript(cwd, lines) {
  const id = randomUUID();
  const at = `${CLAUDE}/projects/${cwd.replace(/[/.]/g, "-")}`;
  mkdirSync(at, { recursive: true });
  let parent = null;
  const out = lines.map(([min, type, content]) => {
    const uuid = randomUUID();
    const l = { parentUuid: parent, isSidechain: false, type, uuid, timestamp: ago(min), cwd, sessionId: id, version: "2.1.318", message: type === "user" ? { role: "user", content } : { role: "assistant", model: "claude-haiku-4-5", content } };
    parent = uuid;
    return JSON.stringify(l);
  });
  writeFileSync(`${at}/${id}.jsonl`, out.join("\n") + "\n");
  return id;
}
const text = (t) => ({ type: "text", text: t });
const testOut = [
  "=== RUN   TestPipeReopen",
  "    hold.go:88: write |1: broken pipe (the reader was not there yet)",
  "--- FAIL: TestPipeReopen (0.21s)",
  ...Array.from({ length: 60 }, (_, i) => `    hold_test.go:${140 + i}: line ${i}: wrote ${i * 512} bytes, read ${i * 512} bytes, pipe open`),
  "FAIL",
  "FAIL\tgithub.com/kumoSleeping/iridium/internal/hold\t0.412s",
].join("\n");
const holder = transcript(iridium, [
  [42, "user", "Holder: named pipe tests. Run the holder's tests and tell me what fails."],
  [41, "assistant", [text("I'll read the holder first, then run its tests."), { type: "tool_use", id: "toolu_read", name: "Read", input: { file_path: `${iridium}/internal/hold/hold.go` } }]],
  [41, "user", [{ type: "tool_result", tool_use_id: "toolu_read", content: "80\t\tf, err := os.OpenFile(fifo, os.O_WRONLY, 0)\n88\t\tif _, err := f.Write(line); err != nil {\n89\t\t\treturn err" }]],
  [40, "assistant", [{ type: "tool_use", id: "toolu_test", name: "Bash", input: { command: "go test ./internal/hold/ -run Pipe -v", description: "Run the pipe tests" } }]],
  [39, "user", [{ type: "tool_result", tool_use_id: "toolu_test", content: testOut, is_error: true }]],
  [38, "assistant", [text(`One test fails: \`TestPipeReopen\`. The write at \`hold.go:88\` meets a pipe whose reader is gone, because the holder reopens the FIFO only after the agent restarts.\n\n![the pipe between the holder and the agent](docs/pipe.png)\n\nThe fix: keep a reader open on the FIFO for the holder's whole life, so a writer never sees a closed end. Notes in [notes.md](docs/notes.md).`)]],
]);
transcript(`${iridium}/web`, [
  [180, "user", "Settings page: put the push switch above the workspaces' events"],
  [179, "assistant", [text("Moved it: the switch first, the events per workspace under it, shown only once push is on.")]],
]);
transcript(`${iridium}/web`, [
  [300, "user", "Topology view: lay it out with dagre, workspace → folder → session"],
  [299, "assistant", [text("Done: dagre lays it out left to right, the folder level dropped where a folder has one session.")]],
]);
transcript(kdj, [
  [600, "user", "Mini player: pop it out of the corner instead of flying"],
  [598, "assistant", [text("It now scales out of its corner in 180 ms, no flight path.")]],
]);
transcript(`${kdj}/app`, [
  [900, "user", "List scroll direction: no stutter, no blank, numbers always"],
  [897, "assistant", [text("Three causes found; the list now owns its scrolling.")]],
]);
transcript(`${HOME}/notes`, [
  [1500, "user", "Notes on cloudflared tunnels and DNS records"],
  [1499, "assistant", [text("cloudflared cannot delete DNS records; the dashboard or an API token can.")]],
]);

const list = () => api("GET", "/api/sessions");
await until("the sessions on disk", async () => (await list()).some((s) => s.id === holder), 90_000);

// Taken over: a message from this machine's page resumes it as hosted, with context and quota.
await api("POST", `/api/sessions/${holder}/send`, { text: "Re-run them with the reader kept open." });
await until("the hosted reply", async () => (await api("GET", `/api/sessions/${holder}`)).last?.text?.startsWith("ok: "));

// A hosted session waiting on an approval.
const style = (await api("POST", "/api/sessions", { agent: "claude", cwd: `${iridium}/web`, text: "Style sample: the confirmation card above the composer. ASK" })).id;
await until("the approval", async () => (await api("GET", `/api/sessions/${style}`)).asks?.length > 0);

// A letter: the Style sample's agent writes to the holder session with ir send.
await asAgent("POST", `/api/sessions/${holder}/send`, { text: "The card's details open under it; check the spacing on a phone." }, style);
await until("the letter's reply", async () => (await api("GET", `/api/sessions/${holder}`)).status === "idle");

console.log(JSON.stringify({ holder, style }));
