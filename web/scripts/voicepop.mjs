// Listens for clicks. One page speaks a recording into a folder group's voice (Chrome's fake microphone
// fed a WAV), another hears it and hands over every sample it played; while it speaks, the
// speaker mutes and unmutes, changes quality and noise suppression, and (JITTER=1) the listener's
// link stalls and gives all at once, as a bad network does. What was played is written as a WAV
// and searched for clicks: a sample far from the one before it, where those around are not.
//   CHROME=<chrome> [JITTER=1] [NOISE=off|browser|strong] [QUALITY=auto|high|mid|low] [STEADY=1]
//     node scripts/voicepop.mjs <base URL> <speech.wav, 48 kHz mono> <out.wav>
// STEADY=1: the speaker changes nothing. Only a test daemon's own page (scripts/test-deploy.sh up).
import { readFileSync, writeFileSync } from "node:fs";
import puppeteer from "puppeteer-core";
const [BASE, SPEECH, OUT] = process.argv.slice(2);
if (!OUT) throw new Error("usage: node scripts/voicepop.mjs <base URL> <speech.wav> <out.wav>");
const RATE = 48000;
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const seconds = (readFileSync(SPEECH).length - 44) / 2 / RATE;

const list = await (await fetch(new URL("v1/channel.list", BASE), { method: "POST", headers: { "content-type": "application/json", "satori-platform": "local", "satori-user-id": "local" }, body: "{}" })).json();
const session = process.env.SESSION ?? list.data?.find((c) => c.type === 0)?.id;
if (!session) throw new Error("no session on the daemon");

const flags = ["--use-fake-device-for-media-stream", "--use-fake-ui-for-media-stream", "--autoplay-policy=no-user-gesture-required", "--mute-audio"];
const speakerBrowser = await puppeteer.launch({ executablePath: process.env.CHROME, headless: true, args: [...flags, `--use-file-for-fake-audio-capture=${SPEECH}`] });
const listenerBrowser = await puppeteer.launch({ executablePath: process.env.CHROME, headless: true, args: flags });
const errors = [];
async function open(browser, name, before) {
  const page = await browser.newPage();
  page.on("pageerror", (e) => errors.push(`${name}: ${e.message}`));
  page.on("console", (m) => m.type() === "error" && errors.push(`${name}: ${m.text()}`));
  await page.setViewport({ width: 1280, height: 800 });
  await page.evaluateOnNewDocument(before, process.env.NOISE ?? "strong", process.env.QUALITY ?? "auto", !!process.env.JITTER);
  await page.goto(`${BASE}?lang=en#/s/${session}`, { waitUntil: "networkidle2" });
  // Into the voice of the first folder group: its "+", then 语音 in it.
  await (await page.waitForSelector('button[aria-label^="New session in"]', { timeout: 20_000 })).click();
  await (await page.waitForSelector("xpath/.//button[starts-with(normalize-space(.), 'Voice')]", { timeout: 20_000 })).click();
  await page.waitForSelector('button[aria-label="In voice"]', { timeout: 20_000 });
  return page;
}
/** A key of the open popover, by its words. */
async function press(page, words, role = "button") {
  // The microphone's key stands in the dock itself.
  if (words === "Mute" || words === "Unmute") return void (await page.click(`button[aria-label="${words}"]`));
  if (!(await page.$("[data-voice]"))) await page.click('button[aria-label="In voice"]');
  const sel = role === "radio" ? `xpath/.//button[@role='radio'][normalize-space(.)='${words}']` : `xpath/.//button[contains(., '${words}')]`;
  await (await page.waitForSelector(sel, { timeout: 5000 })).click();
}
let code = 1;
try {
  const listener = await open(listenerBrowser, "listener", (_n, _q, jitter) => {
    window.__rec = [];
    window.__irVoiceTap = (pcm) => window.__rec.push(pcm);
    if (!jitter) return;
    // The room's link held back a moment every few seconds, then all of it at once, in order.
    const Real = window.WebSocket;
    window.WebSocket = class extends Real {
      constructor(u, p) {
        super(u, p);
        if (!String(u).endsWith("/voice")) return;
        let handler = null;
        let stalled = false;
        const held = [];
        Object.defineProperty(this, "onmessage", { set: (f) => (handler = f), get: () => handler });
        this.addEventListener("message", (e) => (stalled ? held.push(e) : handler?.(e)));
        const stall = () => {
          stalled = true;
          setTimeout(() => {
            stalled = false;
            for (const e of held.splice(0)) handler?.(e);
            setTimeout(stall, 1200 + Math.random() * 2500);
          }, 120 + Math.random() * 800);
        };
        setTimeout(stall, 3000);
      }
    };
  });
  await press(listener, "Mute"); // its own beeps kept out of the room
  const speaker = await open(speakerBrowser, "speaker", (noise, quality) => {
    localStorage.setItem("voice.noise", noise);
    localStorage.setItem("voice.quality", quality);
  });
  const t0 = Date.now();
  const at = async (s, what, f) => {
    await sleep(Math.max(0, t0 + s * 1000 - Date.now()));
    console.log(`  ${s}s ${what}`);
    await f();
  };
  if (!process.env.STEADY) {
    await at(6, "mute", () => press(speaker, "Mute"));
    await at(8, "unmute", () => press(speaker, "Unmute"));
    await at(11, "quality low", () => press(speaker, "Low", "radio"));
    await at(14, "quality high", () => press(speaker, "High", "radio"));
    await at(17, "noise standard", () => press(speaker, "Standard", "radio"));
    await at(20, "noise off", () => press(speaker, "Off", "radio"));
    await at(23, "noise strong", () => press(speaker, "Strong", "radio"));
    await at(26, "quality medium", () => press(speaker, "Medium", "radio"));
  }
  await sleep(Math.max(0, t0 + (seconds + 2) * 1000 - Date.now()));
  if (!(await listener.$("[data-voice]"))) await listener.click('button[aria-label="In voice"]');
  const n = JSON.parse(await (await listener.waitForSelector("[data-voice]")).evaluate((e) => e.dataset.voice));
  const b64 = await listener.evaluate(() => {
    const n = window.__rec.reduce((a, c) => a + c.length, 0);
    const pcm = new Int16Array(n);
    let i = 0;
    for (const c of window.__rec) for (const x of c) pcm[i++] = Math.max(-32768, Math.min(32767, Math.round(x * 32767)));
    let s = "";
    const u8 = new Uint8Array(pcm.buffer);
    for (let k = 0; k < u8.length; k += 0x8000) s += String.fromCharCode(...u8.subarray(k, k + 0x8000));
    return btoa(s);
  });
  const raw = Buffer.from(b64, "base64");
  const head = Buffer.alloc(44);
  head.write("RIFF", 0);
  head.writeUInt32LE(36 + raw.length, 4);
  head.write("WAVEfmt ", 8);
  head.writeUInt32LE(16, 16);
  head.writeUInt16LE(1, 20);
  head.writeUInt16LE(1, 22);
  head.writeUInt32LE(RATE, 24);
  head.writeUInt32LE(RATE * 2, 28);
  head.writeUInt16LE(2, 32);
  head.writeUInt16LE(16, 34);
  head.write("data", 36);
  head.writeUInt32LE(raw.length, 40);
  writeFileSync(OUT, Buffer.concat([head, raw]));
  const x = new Int16Array(raw.buffer, raw.byteOffset, raw.length / 2);
  const found = clicks(x);
  const src = readFileSync(SPEECH);
  const base = clicks(new Int16Array(src.buffer, src.byteOffset + 44, (src.length - 44) >> 1));
  console.log(`listener: network ${n.net} ms · 95% ${n.net95} ms | buffer ${n.buffer} ms | dropped·unsent·stalls ${n.late} · ${n.unsent} · ${n.stalls}`);
  console.log(`played ${(x.length / RATE).toFixed(1)} s, ${loud(x)}% of it with sound (the recording: ${loud(new Int16Array(src.buffer, src.byteOffset + 44, (src.length - 44) >> 1))}%)`);
  console.log(`clicks: ${found.length} (in the recording itself: ${base.length})${found.length ? ": " + found.slice(0, 12).map((c) => `${c.at.toFixed(2)}s ${c.kind} ${c.step.toFixed(2)}`).join(", ") : ""}`);
  if (errors.length) console.log(errors.join("\n"));
  code = found.length <= base.length && !errors.length && x.length > RATE * 5 ? 0 : 1;
  console.log(code ? "FAIL" : "ok");
} catch (e) {
  console.log(errors.join("\n"));
  console.log(`FAIL ${e.message}`);
} finally {
  await speakerBrowser.close();
  await listenerBrowser.close();
  process.exitCode = code;
}

/** The clicks in x, of two kinds. A hard edge: sound that starts or stops at a step (over 0.03)
 *  out of or into 2 ms of silence, which is what a cut, a gate or an empty queue would make. A
 *  spike: a sample over 0.25 from the one before it, ten times as far as is usual around it
 *  (a loud voice's own pulses reach 0.2).
 *  Those within 10 ms of each other are one. */
function clicks(x) {
  const out = [];
  const v = (i) => x[i] / 32768;
  const quiet = (from, to) => {
    for (let k = from; k < to; k++) if (Math.abs(v(k)) > 0.004) return false;
    return true;
  };
  for (let n = 100; n < x.length - 100; n++) {
    const step = Math.abs(v(n) - v(n - 1));
    if (step < 0.03) continue;
    let kind = "";
    if (quiet(n - 97, n) || quiet(n + 1, n + 97)) kind = "edge";
    else if (step > 0.25) {
      const near = [];
      for (let k = n - 48; k <= n + 48; k++) if (Math.abs(k - n) > 2) near.push(Math.abs(v(k) - v(k - 1)));
      near.sort((a, b) => a - b);
      if (step > Math.max(near[near.length >> 1], 0.002) * 10) kind = "spike";
    }
    if (!kind) continue;
    if (out.length && n / RATE - out.at(-1).at < 0.01) {
      out.at(-1).step = Math.max(out.at(-1).step, step);
      continue;
    }
    out.push({ at: n / RATE, step, kind });
  }
  return out;
}
/** The share of x's 20 ms blocks with sound in them (over -50 dBFS), in percent. */
function loud(x) {
  let on = 0;
  let blocks = 0;
  for (let i = 0; i + 960 <= x.length; i += 960) {
    let sum = 0;
    for (let k = i; k < i + 960; k++) sum += (x[k] / 32768) ** 2;
    blocks++;
    if (Math.sqrt(sum / 960) > 0.003) on++;
  }
  return blocks ? Math.round((on / blocks) * 100) : 0;
}
