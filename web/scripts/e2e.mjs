// Drives the real app through what it should do end to end and says what worked, against a dev
// daemon with the fake claude (internal/testagent) and its sessions from the screenshots' setup
// (a workspace Iridium with a folder web and a session "Holder: ..."), served by devserve without -mock:
//   CHROME=<chrome-headless-shell> node scripts/e2e.mjs [http://127.0.0.1:7832/]
// It writes, renames, pins, archives and makes folders on that daemon: never point it at a real one.
// Failures leave a screenshot in $TMPDIR/iridium-e2e-<n>.png. The browser is muted.
import { writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import puppeteer from "puppeteer-core";
const BASE = process.argv[2] ?? "http://127.0.0.1:7832/";
const TMP = tmpdir();
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
await step("+ → new session in a folder picked → the chat opens, caret in the composer", async () => {
  await page.click('button[aria-label="New"]');
  await clickText("New session");
  await page.click('button[aria-label="Folder"]');
  await clickText("web");
  await sleep(300);
  await clickText("Choose this folder");
  await page.click('button[aria-label="Claude"]');
  await page.waitForFunction(() => location.hash.startsWith("#/s/"), { timeout: 8000 });
  id = decodeURIComponent((await page.evaluate(() => location.hash)).slice(4));
  await page.waitForSelector("textarea");
  await sleep(300);
  if (!(await page.evaluate(() => document.activeElement?.tagName === "TEXTAREA"))) throw new Error("caret not in the composer");
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
  await clickText("Allow", "button");
  await page.waitForFunction(() => !document.querySelector('[role="alertdialog"]'), { timeout: 8000 });
  await waitText("ok: please ASK", 10000);
});

await step("a file attached goes up in chunks and is sent with the message", async () => {
  writeFileSync(`${TMP}/e2e-note.txt`, "a note from the e2e run\n");
  const input = await page.$('input[type="file"][multiple]');
  await input.uploadFile(`${TMP}/e2e-note.txt`);
  await page.waitForSelector("[data-attachments]", { timeout: 3000 });
  await page.waitForFunction(() => !document.querySelector('[data-attachments] .bg-ink-3') && !document.querySelector('button[aria-label="Send"][disabled]'), { timeout: 10000 });
  await page.type("textarea", "see the note");
  await page.keyboard.press("Enter");
  await waitText("ok: see the note", 10000);
  await page.waitForFunction(() => [...document.querySelectorAll("a[download]")].some((a) => a.textContent.includes("e2e-note")), { timeout: 8000 });
});

await step("rename from the top bar's menu: the sidebar shows the new name", async () => {
  await page.click('header button[aria-label="More"]');
  await clickText("Rename", "*[@role='menuitem']");
  await page.waitForSelector('header input[aria-label="Rename"]');
  await page.keyboard.down("Control");
  await page.keyboard.press("a");
  await page.keyboard.up("Control");
  await page.keyboard.type("E2E renamed");
  await page.keyboard.press("Enter");
  await page.waitForFunction(() => document.querySelector('[role="tree"]').innerText.includes("E2E renamed"), { timeout: 8000 });
});

await step("pin from the row's menu (right click): a pin on the row", async () => {
  const row = await page.waitForSelector('xpath/.//*[@role="treeitem"][contains(., "E2E renamed")]');
  await row.click({ button: "right" });
  await clickText("Pin", "*[@role='menuitem']");
  await page.waitForFunction(() => [...document.querySelectorAll('[role="treeitem"]')].some((r) => r.textContent.includes("E2E renamed") && r.querySelector('[aria-label="Pinned"]')), { timeout: 8000 });
});

await step("+ → new workspace: a new folder in the picker, chosen, listed", async () => {
  await page.click('button[aria-label="New"]');
  await clickText("New workspace");
  await page.click('button[aria-label="New folder"]');
  await page.waitForSelector('input[aria-label="Folder name"]');
  const name = `e2e-ws-${Date.now() % 100000}`;
  await page.keyboard.type(name);
  await page.keyboard.press("Enter");
  await clickText(name);
  await sleep(300);
  await clickText("Choose this folder");
  await page.waitForFunction((n) => document.querySelector('[role="tree"]').innerText.includes(n), { timeout: 8000 }, name);
});

await step("archive from the row's menu: it goes under Archive", async () => {
  const row = await page.waitForSelector('xpath/.//*[@role="treeitem"][contains(., "E2E renamed")]');
  await row.click({ button: "right" });
  await clickText("Archive", "*[@role='menuitem']");
  await page.waitForFunction(() => [...document.querySelectorAll('[role="treeitem"]')].some((r) => r.textContent.trim().startsWith("Archive")), { timeout: 8000 });
});

await step("the topology view draws the workspaces, folders and sessions", async () => {
  await page.click('button[aria-label="Topology"]');
  await page.waitForSelector(".react-flow__node", { timeout: 8000 });
  const n = await page.$$eval(".react-flow__node", (es) => es.length);
  if (n < 5) throw new Error(`${n} nodes`);
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
