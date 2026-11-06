// Screenshots of the real app, served by an installed test daemon (scripts/test-deploy.sh up, then
// node scripts/seed.mjs), nothing mocked:
//   CHROME=<chrome-headless-shell> FONT_DIR=<Inter + Noto Sans SC> node scripts/shoot.mjs [dir] [filter]
// dir is test-deploy.sh's (default $TMPDIR/iridium-test-deploy). The sessions are picked by title
// from the daemon's list. The last shots turn the Passkey gate on (public.origin
// http://localhost:<port>), join as a panel with a virtual authenticator, and turn it off again.
// The browser is muted.
import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { request } from "node:http";
import { tmpdir } from "node:os";
import puppeteer from "puppeteer-core";

const dir = process.argv[2] && !process.argv[2].startsWith("-") && process.argv[2].includes("/") ? process.argv[2] : `${process.env.TMPDIR ?? "/tmp"}/iridium-test-deploy`;
const only = process.argv.slice(2).find((a) => !a.includes("/"));
const env = Object.fromEntries(
  readFileSync(`${dir}/env`, "utf8")
    .split("\n")
    .filter((l) => l.includes("="))
    .map((l) => [l.slice(0, l.indexOf("=")), l.slice(l.indexOf("=") + 1)]),
);
const BASE = env.IRIDIUM_TEST_BASE;
const port = new URL(BASE).port;
const PUBLIC = `http://localhost:${port}/`;
const OUT = new URL("../shots/", import.meta.url).pathname;
mkdirSync(OUT, { recursive: true });

/** A request on the unix socket: 本机 as a person runs ir, or, with from, an agent's ir in that session. */
function sock(method, path, body, from) {
  return new Promise((resolve, reject) => {
    const b = body === undefined ? "" : JSON.stringify(body);
    const headers = { "content-type": "application/json", "content-length": Buffer.byteLength(b), ...(from ? { "x-iridium-agent": "1", "x-iridium-session": from } : {}) };
    const req = request({ socketPath: env.IRIDIUM_SOCKET, path, method, headers }, (res) => {
      let out = "";
      res.on("data", (d) => (out += d));
      res.on("end", () => (res.statusCode < 300 ? resolve(out ? JSON.parse(out) : {}) : reject(new Error(`${path}: ${res.statusCode} ${out}`))));
    });
    req.on("error", reject);
    req.end(b);
  });
}

const list = await sock("GET", "/api/sessions");
const by = (re) => list.find((s) => re.test(s.title ?? ""))?.id ?? list[0]?.id;
// The holder session: a long chat waiting on an approval; the Style sample asks for a token.
const ASK = by(/Holder/);
const PLAIN = ASK;
const STYLE = by(/Style sample/);
// What agents ask a panel for, so the cards have something to show: a plugin token from the Style
// sample's agent (an invite needs the gate on: asked for later).
const pending = (await sock("GET", "/api/confirms")).pending;
if (!pending.some((c) => c.kind === "token")) await sock("POST", "/api/terminals/tokens", { name: "qq-bot" }, STYLE);
// A plugin terminal of its own on the 终端 page, with a narrower table.
const terms = (await sock("GET", "/api/terminals")).terminals;
if (!terms.some((x) => x.name === "tg-bot")) await sock("POST", "/api/terminals/tokens", { name: "tg-bot" });

const desk = { width: 1440, height: 900, deviceScaleFactor: 1 };
const phone = { width: 390, height: 844, deviceScaleFactor: 2, isMobile: true, hasTouch: true };
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const chat = (id) => `#/s/${encodeURIComponent(id)}`;
const click = (sel) => async (p) => p.click(sel);
const clickText = (t, sel = "button") => async (p) => (await p.waitForSelector(`xpath/.//${sel}[contains(., "${t}")]`, { timeout: 5000 })).click();
/** The mouse over a sidebar row with these words (a workspace's shows its "+"). */
const hoverText = (t) => async (p) => (await p.waitForSelector(`xpath/.//*[@role='treeitem'][@aria-expanded][contains(., "${t}")]`, { timeout: 5000 })).hover();
/** A key in the open popover or dialog, not one of the same words elsewhere on the page. */
const inPop = (t) => clickText(t, "*[@role='dialog']//button");
const steps =
  (...fs) =>
  async (p) => {
    for (const f of fs) (await f(p), await sleep(350));
  };

/** name, viewport, query, hash, optional action before the shot, media features */
const shots = [];
for (const theme of ["light", "dark"])
  for (const lang of ["zh", "en"]) {
    shots.push([`desktop-${theme}-${lang}`, desk, { theme, lang }, chat(ASK)]);
    shots.push([`phone-${theme}-${lang}`, phone, { theme, lang }, chat(ASK)]);
    shots.push([`phone-list-${theme}-${lang}`, phone, { theme, lang }, "#/"]);
  }
shots.push(
  ["desktop-plain-light-en", desk, { theme: "light", lang: "en" }, chat(PLAIN)],
  ["desktop-approval-details-light-en", desk, { theme: "light", lang: "en" }, chat(ASK), click('[role="alertdialog"][aria-label="Run command"] button[aria-expanded]')],
  ["desktop-context-light-en", desk, { theme: "light", lang: "en" }, chat(PLAIN), click('button[aria-label^="Context"]')],
  ["desktop-context-dark-zh", desk, { theme: "dark", lang: "zh" }, chat(PLAIN), click('button[aria-label^="上下文"]')],
  ["desktop-mode-light-zh", desk, { theme: "light", lang: "zh" }, chat(PLAIN), click('button[aria-label^="审批模式"]')],
  ["desktop-model-light-en", desk, { theme: "light", lang: "en" }, chat(PLAIN), click('button[aria-label^="Model"]')],
  ["desktop-model-dark-zh", desk, { theme: "dark", lang: "zh" }, chat(PLAIN), click('button[aria-label^="模型"]')],
  ["desktop-menu-dark-en", desk, { theme: "dark", lang: "en" }, chat(PLAIN), click('header button[aria-label="More"]')],
  ["desktop-plus-light-zh", desk, { theme: "light", lang: "zh" }, chat(PLAIN), click('button[aria-label="新工作区"]')],
  ["desktop-newsession-light-en", desk, { theme: "light", lang: "en" }, chat(PLAIN), steps(hoverText("Iridium"), click('button[aria-label="New session in Iridium"]'))],
  ["desktop-picker-light-en", desk, { theme: "light", lang: "en" }, chat(PLAIN), steps(click('button[aria-label="New workspace"]'), click('button[aria-label="New folder"]'))],
  ["desktop-me-light-zh", desk, { theme: "light", lang: "zh" }, chat(PLAIN), click("aside button:has(> span + span.truncate)")],
  ["desktop-me-dark-en", desk, { theme: "dark", lang: "en" }, chat(PLAIN), click("aside button:has(> span + span.truncate)")],
  ["desktop-palette-light-en", desk, { theme: "light", lang: "en" }, chat(PLAIN), async (p) => (await p.focus("textarea"), await p.keyboard.type("/c"))],
  ["desktop-jump-light-en", desk, { theme: "light", lang: "en" }, chat(PLAIN), async (p) => p.keyboard.down("Control").then(() => p.keyboard.press("k")).then(() => p.keyboard.up("Control"))],
  ["desktop-settings-light-zh", desk, { theme: "light", lang: "zh" }, "#/settings"],
  ["desktop-settings-dark-en", desk, { theme: "dark", lang: "en" }, "#/settings"],
  ["desktop-home-light-en", desk, { theme: "light", lang: "en" }, "#/"],
  ["phone-context-dark-en", phone, { theme: "dark", lang: "en" }, chat(PLAIN), click('button[aria-label^="Context"]')],
  ["phone-model-light-en", phone, { theme: "light", lang: "en" }, chat(PLAIN), click('button[aria-label^="Model"]')],
  ["phone-plus-light-en", phone, { theme: "light", lang: "en" }, "#/", click('button[aria-label="New workspace"]')],
  ["phone-me-dark-zh", phone, { theme: "dark", lang: "zh" }, "#/", click("aside button:has(> span + span.truncate)")],
  ["phone-settings-light-zh", phone, { theme: "light", lang: "zh" }, "#/settings"],
  ["desktop-solid-light-en", desk, { theme: "light", lang: "en" }, chat(PLAIN), click('button[aria-label^="Context"]'), [{ name: "prefers-reduced-transparency", value: "reduce" }]],
  // The terminals, the confirmations agents ask for, a token shown once.
  ["desktop-terminals-light-zh", desk, { theme: "light", lang: "zh" }, "#/settings/terminals"],
  ["desktop-terminals-dark-en", desk, { theme: "dark", lang: "en" }, "#/settings/terminals"],
  ["desktop-terminal-rules-light-zh", desk, { theme: "light", lang: "zh" }, "#/settings/terminals", clickText("tg-bot")],
  ["desktop-terminal-owner-light-en", desk, { theme: "light", lang: "en" }, "#/settings/terminals", steps(clickText("tg-bot"), click('button[aria-label="Claude"][aria-pressed="false"]'))],
  ["phone-terminals-light-zh", phone, { theme: "light", lang: "zh" }, "#/settings/terminals"],
  ["desktop-confirm-light-zh", desk, { theme: "light", lang: "zh" }, chat(STYLE), click('[role="alertdialog"][aria-label="插件 TOKEN"] button[aria-expanded]')],
  ["phone-confirm-dark-en", phone, { theme: "dark", lang: "en" }, chat(STYLE)],
  ["desktop-token-light-en", desk, { theme: "light", lang: "en" }, "#/settings/terminals", steps(click('button[aria-label="New terminal"]'), inPop("Plugin token"), async (p) => p.keyboard.type("echo-im"), inPop("Make"))],
);
// With the gate on (public), as a panel that joined with a passkey.
const gated = [
  ["desktop-invite-qr-light-zh", desk, { theme: "light", lang: "zh" }, "#/settings/terminals", steps(click('button[aria-label="新终端"]'), inPop("邀请新面板"), async (p) => p.keyboard.type("kumo-phone"), inPop("生成"))],
  ["phone-invite-qr-dark-en", phone, { theme: "dark", lang: "en" }, "#/settings/terminals", steps(click('button[aria-label="New terminal"]'), inPop("Invite a panel"), inPop("Make"))],
  ["desktop-confirm-invite-dark-en", desk, { theme: "dark", lang: "en" }, "#/settings/terminals", click('[role="alertdialog"][aria-label="Invite a panel"] button[aria-expanded]')],
  ["desktop-me-panel-light-en", desk, { theme: "light", lang: "en" }, chat(PLAIN), click("aside button:has(> span + span.truncate)")],
  ["desktop-terminals-panel-light-zh", desk, { theme: "light", lang: "zh" }, "#/settings/terminals"],
];

const fontEnv = { ...process.env };
if (process.env.FONT_DIR) {
  const fc = `${tmpdir()}/iridium-web-fc`;
  mkdirSync(fc, { recursive: true });
  const prefer = "<prefer><family>Inter</family><family>Noto Sans SC</family></prefer>";
  writeFileSync(
    `${fc}/fonts.conf`,
    `<?xml version="1.0"?><!DOCTYPE fontconfig SYSTEM "fonts.dtd"><fontconfig><include ignore_missing="yes">/etc/fonts/fonts.conf</include><dir>${process.env.FONT_DIR}</dir><cachedir>${fc}/cache</cachedir><alias binding="strong"><family>sans-serif</family>${prefer}</alias><alias binding="strong"><family>system-ui</family>${prefer}</alias></fontconfig>`,
  );
  fontEnv.FONTCONFIG_FILE = `${fc}/fonts.conf`;
}
const browser = await puppeteer.launch({
  env: fontEnv,
  executablePath: process.env.CHROME,
  headless: "shell",
  args: ["--mute-audio", "--hide-scrollbars", "--font-render-hinting=none", "--force-color-profile=srgb"],
});
const page = await browser.newPage();
page.on("pageerror", (e) => console.error("pageerror:", e.message));
page.on("console", (m) => m.type() === "error" && !/401|Unauthorized/.test(m.text()) && console.error("console:", m.text()));

async function shoot(base, [name, vp, q, hash, act, media]) {
  if (only && !name.includes(only)) return;
  await page.emulateMediaFeatures(media ?? []);
  await page.setViewport(vp);
  const qs = new URLSearchParams({ still: "1", ...q });
  // A fresh page each time, nothing kept from the last (drafts, open groups): the address's query
  // decides the theme and language.
  await page.goto(new URL("api/health", base).href);
  await page.evaluate(() => (localStorage.clear(), sessionStorage.clear()));
  await page.goto(`${base}?${qs}${hash}`, { waitUntil: "networkidle2" });
  await page.evaluate(() => document.fonts.ready);
  await sleep(900);
  if (act) {
    try {
      await act(page);
    } catch (e) {
      console.error(name, "action:", e.message);
    }
    await sleep(800);
  }
  await page.screenshot({ path: `${OUT}${name}.png` });
  console.log(name);
}

for (const s of shots) await shoot(BASE, s);

if (!only || gated.some(([n]) => n.includes(only))) {
  // The gate on: the first panel joins through the setup link with a passkey.
  await sock("PUT", "/api/public", { origin: PUBLIC.replace(/\/$/, "") });
  try {
    await sleep(300);
    // The first-run link while no panel has a passkey; after, an invite (a run before joined).
    let link;
    try {
      link = readFileSync(`${dir}/state/setup-token`, "utf8").trim();
    } catch {
      link = (await sock("POST", "/api/terminals/invites", { name: `mac-${Date.now() % 1000}` })).url;
    }
    const cdp = await page.createCDPSession();
    await cdp.send("WebAuthn.enable");
    await cdp.send("WebAuthn.addVirtualAuthenticator", { options: { protocol: "ctap2", transport: "internal", hasResidentKey: true, hasUserVerification: true, isUserVerified: true, automaticPresenceSimulation: true } });
    await page.setViewport(desk);
    await page.goto(link, { waitUntil: "networkidle2" });
    await page.waitForSelector("#name", { visible: true });
    if (!(await page.$eval("#name", (e) => e.value))) await page.type("#name", "kumo-m2-mac");
    await page.waitForFunction(() => !document.getElementById("join").disabled, { timeout: 5000 });
    await page.click("#join");
    await page.waitForFunction((b) => location.href.startsWith(b) && !location.pathname.startsWith("/_ir/"), { timeout: 10000 }, PUBLIC);
    // The owner's panel: Claude too, as the owner says on its 终端 page (here 本机 says it).
    const me = (await sock("GET", "/api/terminals")).terminals.filter((x) => x.kind === "panel").sort((a, b) => b.createdAt - a.createdAt)[0];
    await sock("PUT", `/api/terminals/${me.id}/rules`, { rules: { ...me.rules, agents: ["claude", ...me.rules.agents.filter((a) => a !== "claude")] } });
    // An agent's ir asks for an invite: a card for the panel.
    if (!(await sock("GET", "/api/confirms")).pending.some((c) => c.kind === "invite")) await sock("POST", "/api/terminals/invites", { name: "ipad" }, STYLE);
    for (const s of gated) await shoot(PUBLIC, s);
  } finally {
    await sock("PUT", "/api/public", { origin: "" });
  }
}
await browser.close();
