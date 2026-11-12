// Minutes in a room, to see what grows with time: one page speaks a recording (looped), another
// listens, both with the voice's panel open (the gate's bar drawing). Every 20 s, of each page:
// how late its main thread runs (the worst of a 100 ms timer), its heap, and what the panel
// measures. Nothing should climb.
//   CHROME=<chrome> node scripts/voicesoak.mjs <base URL> <speech.wav, 48 kHz mono> [minutes]
// Only a test daemon's own page (scripts/test-deploy.sh up).
import puppeteer from "puppeteer-core";
const [BASE, SPEECH, MINUTES = "4"] = process.argv.slice(2);
if (!SPEECH) throw new Error("usage: node scripts/voicesoak.mjs <base URL> <speech.wav> [minutes]");
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const list = await (await fetch(new URL("v1/channel.list", BASE), { method: "POST", headers: { "content-type": "application/json", "satori-platform": "local", "satori-user-id": "local" }, body: "{}" })).json();
const session = process.env.SESSION ?? list.data?.find((c) => c.type === 0)?.id;
const flags = ["--use-fake-device-for-media-stream", "--use-fake-ui-for-media-stream", "--autoplay-policy=no-user-gesture-required", "--mute-audio", "--enable-precise-memory-info"];
const browsers = [];
async function open(name, extra) {
  const browser = await puppeteer.launch({ executablePath: process.env.CHROME, headless: true, args: [...flags, ...extra] });
  browsers.push(browser);
  const page = await browser.newPage();
  page.on("pageerror", (e) => console.log(`${name}: ${e.message}`));
  await page.setViewport({ width: 1280, height: 800 });
  await page.evaluateOnNewDocument(() => {
    // The worst a 100 ms timer ran late since last asked.
    let last = performance.now();
    window.__lag = 0;
    setInterval(() => {
      const now = performance.now();
      window.__lag = Math.max(window.__lag, now - last - 100);
      last = now;
    }, 100);
  });
  await page.goto(`${BASE}?lang=en#/s/${session}`, { waitUntil: "networkidle2" });
  await (await page.waitForSelector('button[aria-label^="New session in"]', { timeout: 20_000 })).click();
  await (await page.waitForSelector("xpath/.//button[starts-with(normalize-space(.), 'Voice')]", { timeout: 20_000 })).click();
  await page.waitForSelector('button[aria-label="In voice"]', { timeout: 20_000 });
  return page;
}
const read = (page) =>
  page.evaluate(() => {
    const lag = Math.round(window.__lag);
    window.__lag = 0;
    const el = document.querySelector("[data-voice]");
    const n = el ? JSON.parse(el.dataset.voice) : {};
    return { lag, heap: Math.round(performance.memory.usedJSHeapSize / 1e6), nodes: document.querySelectorAll("*").length, net: n.net, buffer: n.buffer, late: n.late, unsent: n.unsent, up: n.up, down: n.down };
  });
try {
  const listener = await open("listener", []);
  await listener.click('button[aria-label="Mute"]');
  const speaker = await open("speaker", [`--use-file-for-fake-audio-capture=${SPEECH}`]);
  for (const p of [listener, speaker]) if (!(await p.$("[data-voice]"))) await p.click('button[aria-label="In voice"]');
  const rows = [];
  for (let t = 20; t <= Number(MINUTES) * 60; t += 20) {
    await sleep(20_000);
    for (const p of [listener, speaker]) if (!(await p.$("[data-voice]"))) await p.click('button[aria-label="In voice"]');
    const [l, s] = [await read(listener), await read(speaker)];
    rows.push({ t, l, s });
    console.log(`${String(t).padStart(4)}s  listener: lag ${l.lag} ms heap ${l.heap} MB nodes ${l.nodes} net ${l.net} buffer ${l.buffer} late ${l.late} down ${Math.round(l.down / 1024)} KB | speaker: lag ${s.lag} ms heap ${s.heap} MB unsent ${s.unsent} up ${Math.round(s.up / 1024)} KB`);
  }
  // The voice still goes at the end as it did at the start: what came down in the last third
  // against the first.
  const third = Math.floor(rows.length / 3);
  const rate = (a, b) => (rows[b].l.down - rows[a].l.down) / (rows[b].t - rows[a].t);
  const first = rate(0, third), last = rate(rows.length - 1 - third, rows.length - 1);
  console.log(`down per second: ${Math.round(first)} B at first, ${Math.round(last)} B at the end`);
  process.exitCode = last > first * 0.7 ? 0 : 1;
  console.log(process.exitCode ? "FAIL" : "ok");
} finally {
  for (const b of browsers) await b.close();
}
