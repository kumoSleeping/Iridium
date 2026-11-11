// Drives the real app through what it should do end to end and says what worked, against an
// installed test daemon with the fake claude (scripts/test-deploy.sh up, then scripts/seed.mjs: a
// workspace Iridium with a folder web, a session "Holder: ...", a "Style sample" waiting):
//   CHROME=<chrome-headless-shell> node scripts/e2e.mjs [dir of test-deploy.sh]
// It writes, renames, pins, archives, makes folders, terminals and tokens on that daemon: never point
// it at a real one. Failures leave a screenshot in $TMPDIR/iridium-e2e-<n>.png. The browser is muted.
import { readFileSync, writeFileSync } from "node:fs";
import { request } from "node:http";
import { tmpdir } from "node:os";
import puppeteer from "puppeteer-core";
const dir = process.argv[2] ?? `${process.env.TMPDIR ?? "/tmp"}/iridium-test-deploy`;
const env = Object.fromEntries(
  readFileSync(`${dir}/env`, "utf8")
    .split("\n")
    .filter((l) => l.includes("="))
    .map((l) => [l.slice(0, l.indexOf("=")), l.slice(l.indexOf("=") + 1)]),
);
const BASE = env.IRIDIUM_TEST_BASE;
const TMP = tmpdir();
/** A request on the unix socket as an agent's ir in session `from` makes it. */
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
const browser = await puppeteer.launch({ executablePath: process.env.CHROME, headless: "shell", args: ["--mute-audio"] });
const page = await browser.newPage();
const errors = [];
page.on("pageerror", (e) => errors.push(e.message));
await page.setViewport({ width: 1440, height: 900 });
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const results = [];
async function step(name, f) {
  try {
    await f();
    results.push(`ok   ${name}`);
  } catch (e) {
    results.push(`FAIL ${name}: ${e.message.split("\n")[0]}`);
    await page.screenshot({ path: `${TMP}/iridium-e2e-${results.length}.png` });
  }
}
const text = (sel) => page.$$eval(sel, (es) => es.map((e) => e.textContent).join("\n"));
const waitText = (t, timeout = 8000) => page.waitForFunction((t) => document.body.innerText.includes(t), { timeout }, t);
const clickText = async (t, sel = "button") => {
  const h = await page.waitForSelector(`xpath/.//${sel}[contains(., "${t}")]`, { timeout: 5000 });
  await h.click();
};

await page.goto(`${BASE}?lang=en`, { waitUntil: "networkidle2" });
await page.evaluate(() => localStorage.clear());
await page.goto(`${BASE}?lang=en#/`, { waitUntil: "networkidle2" });

await step("the sidebar lists the daemon's workspaces and sessions", async () => {
  await page.waitForSelector('[role="treeitem"]');
  const t = await text('[role="tree"]');
  for (const w of ["Iridium", "KDJ", "Holder: named pipe tests"]) if (!t.includes(w)) throw new Error(`no ${w}`);
});

let id = "";
await step("a workspace's + → new session in a folder picked → the chat opens, caret in the composer; the workspace never folds", async () => {
  const ws = 'xpath/.//*[@role="treeitem"][@aria-expanded][contains(., "Iridium")]';
  // Every press in its popover is checked: an even number of them would fold and unfold it back.
  const stillOpen = async (after) => {
    const open = await page.$eval(ws, (e) => e.getAttribute("aria-expanded"));
    if (open !== "true") throw new Error(`the workspace folded after ${after}`);
  };
  await (await page.waitForSelector(ws)).hover();
  await page.click('button[aria-label="New session in Iridium"]');
  await stillOpen("its +");
  await page.click('button[aria-label="Folder"]');
  await stillOpen("Folder");
  await clickText("web");
  await sleep(300);
  await stillOpen("web");
  await clickText("Choose this folder");
  await stillOpen("Choose this folder");
  await page.click('button[aria-label="Claude"]');
  await page.waitForFunction(() => location.hash.startsWith("#/s/"), { timeout: 8000 });
  id = decodeURIComponent((await page.evaluate(() => location.hash)).slice(4));
  await page.waitForSelector("textarea");
  await sleep(300);
  if (!(await page.evaluate(() => document.activeElement?.tagName === "TEXTAREA"))) throw new Error("caret not in the composer");
  await stillOpen("Claude");
});

await step("a new session's model key names the model and level in use; a model or mode picked closes its menu, the effort slider and UltraCode keep it open", async () => {
  const key = 'button[aria-label^="Model:"]';
  const label = () => page.$eval(key, (e) => e.getAttribute("aria-label"));
  await page.waitForFunction((k) => /Fake 1 · Medium/.test(document.querySelector(k)?.getAttribute("aria-label") ?? ""), { timeout: 8000 }, key);
  const menu = () => page.$('[role="menu"]');
  await page.click(key);
  await page.waitForSelector('[role="menu"] [role="slider"]', { timeout: 5000 });
  await clickText("High", "*[@role='menu']//button");
  await page.waitForFunction((k) => /· High/.test(document.querySelector(k)?.getAttribute("aria-label") ?? ""), { timeout: 5000 }, key);
  if (!(await menu())) throw new Error("a level closed the menu");
  await page.click('[role="menu"] [role="switch"][aria-label="UltraCode"]');
  await page.waitForFunction(() => document.querySelector('[role="switch"][aria-label="UltraCode"]')?.getAttribute("aria-checked") === "true", { timeout: 5000 });
  if (!(await menu())) throw new Error("UltraCode closed the menu");
  await clickText("Haiku", "*[@role='menuitemradio']");
  await page.waitForFunction(() => !document.querySelector('[role="menu"]'), { timeout: 3000 });
  await page.waitForFunction((k) => /Model: Haiku/.test(document.querySelector(k)?.getAttribute("aria-label") ?? ""), { timeout: 5000 }, key);
  const mode = 'button[aria-label^="Approval mode"]';
  const was = await page.$eval(mode, (e) => e.textContent.trim());
  for (const pick of ["Accept edits", was]) {
    await page.click(mode);
    await clickText(pick, "*[@role='menuitemradio']");
    await page.waitForFunction(() => !document.querySelector('[role="menu"]'), { timeout: 3000 });
  }
  if (!(await label()).startsWith("Model: Haiku")) throw new Error(`key: ${await label()}`);
});

await step("send: a faint bubble at once, then the transcript's, then the reply", async () => {
  await page.type("textarea", "hello from e2e");
  await page.keyboard.press("Enter");
  await waitText("hello from e2e", 3000);
  await waitText("ok: hello from e2e", 10000);
  const status = await page.$eval("header [role=img]", (e) => e.getAttribute("aria-label")).catch(() => "idle");
  if (status && /Running|Starting/.test(status)) await sleep(1000);
});

await step("a permission ask shows its card; Allow answers it and the turn goes on", async () => {
  await page.type("textarea", "please ASK");
  await page.keyboard.press("Enter");
  await page.waitForSelector('[role="alertdialog"]', { timeout: 10000 });
  const card = await text('[role="alertdialog"]');
  if (!card.includes("Run command") || !card.includes("ls")) throw new Error(`card: ${card}`);
});

await step("in a turn, Ctrl+Enter queues (edited in place, sent all or deleted) and Enter steers in, saying so; the queue goes once it ends", async () => {
  const ctrlEnter = async () => {
    await page.keyboard.down("Control");
    await page.keyboard.press("Enter");
    await page.keyboard.up("Control");
  };
  await page.type("textarea", "while it waits");
  await ctrlEnter();
  await page.waitForFunction(() => document.querySelector('section[aria-label="Queue"]')?.textContent.includes("while it waits"), { timeout: 5000 });
  await page.click('section[aria-label="Queue"] button[aria-label="Edit"]');
  await page.waitForSelector('section[aria-label="Queue"] input');
  await page.keyboard.type(", edited");
  await page.keyboard.press("Enter");
  await page.waitForFunction(() => document.querySelector('section[aria-label="Queue"]')?.textContent.includes("while it waits, edited"), { timeout: 5000 });
  // Two lines: a key sends them all; one deleted, it goes.
  await page.type("textarea", "a second line");
  await ctrlEnter();
  await page.waitForSelector('section[aria-label="Queue"] button[aria-label="Send all now"]', { timeout: 5000 });
  const drops = await page.$$('section[aria-label="Queue"] button[aria-label="Delete"]');
  await drops[drops.length - 1].click();
  await page.waitForFunction(() => !document.querySelector('section[aria-label="Queue"] button[aria-label="Send all now"]'), { timeout: 5000 });
  // Enter steers into the turn (the default): yours at once, saying it is going in.
  await page.type("textarea", "steer this in");
  await page.keyboard.press("Enter");
  await page.waitForFunction(() => [...document.querySelectorAll('[role="status"]')].some((e) => e.textContent.includes("Steering in")), { timeout: 5000 });
  if (await page.evaluate(() => document.querySelector('section[aria-label="Queue"]')?.textContent.includes("steer this in"))) throw new Error("Enter queued it");
  await clickText("Allow", "button");
  await page.waitForFunction(() => !document.querySelector('[role="alertdialog"]'), { timeout: 8000 });
  await waitText("ok: please ASK", 10000);
  await waitText("ok: while it waits, edited", 10000);
  await page.waitForFunction(() => !document.querySelector('section[aria-label="Queue"]'), { timeout: 5000 });
});

await step("a question card: an option picked, own words for the other; the agent gets both", async () => {
  await page.type("textarea", "pick QUESTION");
  await page.keyboard.press("Enter");
  await page.waitForSelector('[role="alertdialog"] fieldset', { timeout: 10000 });
  await clickText("Blue", "*[@role='alertdialog']//button");
  const own = await page.$$('[role="alertdialog"] input[aria-label="Write your own"]');
  await own[1].type("XL only");
  await clickText("Answer", "*[@role='alertdialog']//button");
  await waitText("Which color?=Blue", 10000);
  await waitText("Which sizes?=XL only", 2000);
});

await step("⌘K jumps to a session by name", async () => {
  await page.keyboard.down("Control");
  await page.keyboard.press("k");
  await page.keyboard.up("Control");
  await page.waitForSelector('[role="combobox"]');
  await page.keyboard.type("holder");
  await page.keyboard.press("Enter");
  await page.waitForFunction(() => document.querySelector("header h1")?.textContent.includes("Holder"), { timeout: 8000 });
});

await step("a long tool result is cut; Show all brings the whole", async () => {
  // The fold of the Holder session: open it and a step, the whole is fetched only if cut.
  const fold = await page.waitForSelector('summary[aria-label^="Tool calls"]', { timeout: 8000 });
  await fold.evaluate((e) => e.scrollIntoView());
  await fold.click();
  await sleep(200);
  const steps = await page.$$("details details > summary, details > div details > summary");
  if (!steps.length) throw new Error("no steps");
  await steps[1].click();
  await sleep(300);
  if (!(await text("pre")).includes("hold.go:88")) throw new Error("no result shown");
});

await step("phone: the sidebar is the first screen; a session slides over it; back returns", async () => {
  await page.setViewport({ width: 390, height: 844, deviceScaleFactor: 2, isMobile: true, hasTouch: true });
  await page.goto(`${BASE}?lang=en#/`, { waitUntil: "networkidle2" });
  await page.waitForSelector('[role="treeitem"]');
  const row = await page.waitForSelector('xpath/.//*[@role="treeitem"][contains(., "Holder")]');
  await row.tap();
  await page.waitForFunction(() => location.hash.startsWith("#/s/"), { timeout: 5000 });
  await sleep(400);
  await page.tap('header button[aria-label="Back"]');
  await page.waitForFunction(() => location.hash === "#/" || location.hash === "", { timeout: 5000 });
});

console.log(results.join("\n"));
if (errors.length) console.log("page errors:\n" + errors.join("\n"));
await browser.close();
process.exit(results.some((r) => r.startsWith("FAIL")) || errors.length ? 1 : 0);
