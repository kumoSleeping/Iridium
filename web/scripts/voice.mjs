// Two pages in one channel's voice on a test daemon, each with Chrome's fake microphone (a beep
// a second): each must hear the other, and the numbers the popover measures are printed.
//   CHROME=<chrome> node scripts/voice.mjs <base URL> [seconds]
// The base is a test daemon's own page (scripts/test-deploy.sh up: no login). Never a real one.
// A public test instance: INVITES=1, and two one-time invite links on stdin, a line each; each page
// joins by one with a virtual passkey, in a browser context of its own. The links are not printed.
import puppeteer from "puppeteer-core";
const BASE = process.argv[2];
const SECONDS = Number(process.argv[3] ?? 12);
if (!BASE) throw new Error("usage: node scripts/voice.mjs <base URL> [seconds]");
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
let invites = [];
if (process.env.INVITES) {
  let text = "";
  for await (const chunk of process.stdin) text += chunk;
  invites = text.split("\n").map((l) => l.trim()).filter((l) => l.startsWith(BASE));
  if (invites.length < 2) throw new Error("two invite links on stdin");
}
let session = { id: process.env.SESSION };
if (!session.id) {
  // The host's page speaks Satori as the login local of the platform local (docs/satori.md).
  const list = await (await fetch(new URL("v1/channel.list", BASE), { method: "POST", headers: { "content-type": "application/json", "satori-platform": "local", "satori-user-id": "local" }, body: "{}" })).json();
  session = list.data?.find((c) => c.type === 0);
  if (!session) throw new Error("no session on the daemon");
}
const browser = await puppeteer.launch({
  executablePath: process.env.CHROME,
  headless: true,
  args: ["--use-fake-device-for-media-stream", "--use-fake-ui-for-media-stream", "--autoplay-policy=no-user-gesture-required", "--mute-audio"],
});
const errors = [];
async function member(name) {
  const invite = invites.shift();
  const page = await (invite ? await browser.createBrowserContext() : browser).newPage();
  if (invite) {
    const cdp = await page.createCDPSession();
    await cdp.send("WebAuthn.enable");
    await cdp.send("WebAuthn.addVirtualAuthenticator", { options: { protocol: "ctap2", transport: "internal", hasResidentKey: true, hasUserVerification: true, isUserVerified: true, automaticPresenceSimulation: true } });
    await page.goto(invite, { waitUntil: "networkidle2" });
    await page.waitForSelector("#name", { visible: true });
    if (!(await page.$eval("#name", (e) => e.value))) await page.type("#name", `voice-${name}`);
    await page.waitForFunction(() => !document.getElementById("join").disabled, { timeout: 10_000 });
    await page.click("#join");
    await page.waitForFunction(() => !location.pathname.startsWith("/_ir/"), { timeout: 20_000 });
  }
  page.on("pageerror", (e) => errors.push(`${name}: ${e.message}`));
  // Not Cloudflare's own analytics script, which the page's CSP turns away.
  page.on("console", (m) => m.type() === "error" && !m.text().includes("cloudflareinsights") && errors.push(`${name}: ${m.text()}`));
  await page.setViewport({ width: 1280, height: 800 });
  await page.goto(`${BASE}?lang=en#/s/${session.id}`, { waitUntil: "networkidle2" });
  await (await page.waitForSelector('button[aria-label="Voice"], button[aria-label$="in voice"]', { timeout: 10_000 })).click();
  await page.waitForSelector('button[aria-label="In voice"]', { timeout: 20_000 });
  return page;
}
/** The popover, opened again if it closed as the page lost the front: what it measures
 *  (state.ts Numbers) and how many members it lists. */
async function numbers(page) {
  await page.bringToFront();
  if (!(await page.$("[data-voice]"))) await page.click('button[aria-label="In voice"]');
  await page.waitForSelector("[data-voice]");
  await sleep(600);
  return { ...JSON.parse(await page.$eval("[data-voice]", (e) => e.dataset.voice)), members: await page.$$eval("[data-voice]", (es) => es[0].parentElement.querySelectorAll("ul li").length) };
}
const line = (n) => `end to end ${n.net + (n.buffer ?? 0) + n.out} ms | network ${n.net} ms · 95% ${n.net95} ms | buffer ${n.buffer} ms | round trip ${n.rtt} ms | ${n.kbps} kbps | up ${n.up} down ${n.down} | dropped·unsent·stalls ${n.late} · ${n.unsent} · ${n.stalls}`;
try {
  const a = await member("a");
  const b = await member("b");
  await sleep(SECONDS * 1000);
  let ok = true;
  for (const [name, page] of [["a", a], ["b", b]]) {
    const n = await numbers(page);
    console.log(`${name}: ${line(n)}`);
    if (n.members !== 2 || n.net === null || n.buffer === null) ok = false;
  }
  // A lower quality chosen: the rate follows, and the voice goes on.
  await numbers(a);
  await (await a.waitForSelector("xpath/.//button[@role='radio'][normalize-space(.)='Low']")).click();
  await a.waitForFunction(() => JSON.parse(document.querySelector("[data-voice]").dataset.voice).kbps === 12, { timeout: 5000 });
  const before = (await numbers(b)).down;
  await sleep(3000);
  const after = await numbers(b);
  console.log(`b, a at low: ${line(after)}`);
  if (after.down <= before) ok = false;
  // Muted: the other sees it. One leaves: the other sees that too.
  await numbers(a);
  await (await a.waitForSelector("xpath/.//button[contains(., 'Mute')]")).click();
  await numbers(b);
  await b.waitForSelector('svg[aria-label="Muted"]', { timeout: 5000 });
  await numbers(a);
  await (await a.waitForSelector("xpath/.//button[contains(., 'Leave')]")).click();
  await numbers(b);
  await b.waitForFunction(() => document.querySelectorAll("[data-voice]")[0].parentElement.querySelectorAll("ul li").length === 1, { timeout: 10_000 });
  if (errors.length) console.log(errors.join("\n"));
  console.log(ok && !errors.length ? "ok" : "FAIL");
  process.exitCode = ok && !errors.length ? 0 : 1;
} catch (e) {
  console.log(errors.join("\n"));
  console.log(`FAIL ${e.message}`);
  process.exitCode = 1;
} finally {
  await browser.close();
}
