// Screenshots of the real app against a running daemon (web/sample/scripts/shoot.mjs's way): serve
// the build in front of it (go run ./internal/webui/devserve -api <daemon> -dir internal/webui/dist),
// then: CHROME=<chrome-headless-shell> FONT_DIR=<Inter + Noto Sans SC> BASE=http://127.0.0.1:7832/ node scripts/shoot.mjs [filter]
// The sessions are picked by title from the daemon's list. The browser is muted.
import { mkdirSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import puppeteer from "puppeteer-core";

const BASE = process.env.BASE ?? "http://127.0.0.1:7832/";
// TAG: added to each name, as "-nomock" for shots against a devserve without -mock.
const TAG = process.env.TAG ?? "";
const OUT = new URL("../shots/", import.meta.url).pathname;
const only = process.argv[2];
mkdirSync(OUT, { recursive: true });

const list = await (await fetch(new URL("api/sessions", BASE))).json();
const by = (re) => list.find((s) => re.test(s.title ?? ""))?.id ?? list[0]?.id;
const ASK = by(/Style sample/);
const PLAIN = by(/Holder/);

const desk = { width: 1440, height: 900, deviceScaleFactor: 1 };
const phone = { width: 390, height: 844, deviceScaleFactor: 2, isMobile: true, hasTouch: true };

const chat = (id) => `#/s/${encodeURIComponent(id)}`;
const click = (sel) => async (p) => p.click(sel);
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
  ["desktop-approval-details-light-en", desk, { theme: "light", lang: "en" }, chat(ASK), click('[role="alertdialog"] button[aria-expanded]')],
  ["desktop-context-light-en", desk, { theme: "light", lang: "en" }, chat(PLAIN), click('button[aria-label^="Context"]')],
  ["desktop-context-dark-zh", desk, { theme: "dark", lang: "zh" }, chat(PLAIN), click('button[aria-label^="上下文"]')],
  ["desktop-mode-light-zh", desk, { theme: "light", lang: "zh" }, chat(PLAIN), click('button[aria-label^="审批模式"]')],
  ["desktop-model-light-en", desk, { theme: "light", lang: "en" }, chat(PLAIN), click('button[aria-label^="Model"]')],
  ["desktop-menu-dark-en", desk, { theme: "dark", lang: "en" }, chat(PLAIN), click('header button[aria-label="More"]')],
  ["desktop-plus-light-zh", desk, { theme: "light", lang: "zh" }, chat(PLAIN), click('button[aria-label="新建"]')],
  ["desktop-newsession-light-en", desk, { theme: "light", lang: "en" }, chat(PLAIN), async (p) => (await p.click('button[aria-label="New"]'), await sleep(300), await p.click('xpath/.//button[contains(., "New session")]'))],
  ["desktop-picker-light-en", desk, { theme: "light", lang: "en" }, chat(PLAIN), async (p) => (await p.click('button[aria-label="New"]'), await sleep(300), await p.click('xpath/.//button[contains(., "New workspace")]'))],
  ["desktop-me-light-zh", desk, { theme: "light", lang: "zh" }, chat(PLAIN), click('button[aria-haspopup] img, aside button:has(> span + span.truncate)')],
  ["desktop-me-dark-en", desk, { theme: "dark", lang: "en" }, chat(PLAIN), click('aside button:has(> span + span.truncate)')],
  ["desktop-palette-light-en", desk, { theme: "light", lang: "en" }, chat(PLAIN), async (p) => (await p.focus("textarea"), await p.keyboard.type("/c"))],
  ["desktop-jump-light-en", desk, { theme: "light", lang: "en" }, chat(PLAIN), async (p) => p.keyboard.down("Control").then(() => p.keyboard.press("k")).then(() => p.keyboard.up("Control"))],
  ["desktop-settings-light-zh", desk, { theme: "light", lang: "zh" }, "#/settings"],
  ["desktop-settings-dark-en", desk, { theme: "dark", lang: "en" }, "#/settings"],
  ["desktop-topology-light-en", desk, { theme: "light", lang: "en" }, "#/topology"],
  ["desktop-topology-dark-zh", desk, { theme: "dark", lang: "zh" }, "#/topology"],
  ["desktop-home-light-en", desk, { theme: "light", lang: "en" }, "#/"],
  ["phone-context-dark-en", phone, { theme: "dark", lang: "en" }, chat(PLAIN), click('button[aria-label^="Context"]')],
  ["phone-plus-light-en", phone, { theme: "light", lang: "en" }, "#/", click('button[aria-label="New"]')],
  ["phone-me-dark-zh", phone, { theme: "dark", lang: "zh" }, "#/", click('aside button:has(> span + span.truncate)')],
  ["phone-settings-light-zh", phone, { theme: "light", lang: "zh" }, "#/settings"],
  ["desktop-solid-light-en", desk, { theme: "light", lang: "en" }, chat(PLAIN), click('button[aria-label^="Context"]'), [{ name: "prefers-reduced-transparency", value: "reduce" }]],
);

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const env = { ...process.env };
if (process.env.FONT_DIR) {
  const dir = `${tmpdir()}/iridium-web-fc`;
  mkdirSync(dir, { recursive: true });
  const prefer = "<prefer><family>Inter</family><family>Noto Sans SC</family></prefer>";
  writeFileSync(
    `${dir}/fonts.conf`,
    `<?xml version="1.0"?><!DOCTYPE fontconfig SYSTEM "fonts.dtd"><fontconfig><include ignore_missing="yes">/etc/fonts/fonts.conf</include><dir>${process.env.FONT_DIR}</dir><cachedir>${dir}/cache</cachedir><alias binding="strong"><family>sans-serif</family>${prefer}</alias><alias binding="strong"><family>system-ui</family>${prefer}</alias></fontconfig>`,
  );
  env.FONTCONFIG_FILE = `${dir}/fonts.conf`;
}
const browser = await puppeteer.launch({
  env,
  executablePath: process.env.CHROME,
  headless: "shell",
  args: ["--mute-audio", "--hide-scrollbars", "--font-render-hinting=none", "--force-color-profile=srgb"],
});
const page = await browser.newPage();
page.on("pageerror", (e) => console.error("pageerror:", e.message));
page.on("console", (m) => m.type() === "error" && console.error("console:", m.text()));

for (const [name, vp, q, hash, act, media] of shots) {
  if (only && !name.includes(only)) continue;
  await page.emulateMediaFeatures(media ?? []);
  await page.setViewport(vp);
  const qs = new URLSearchParams({ still: "1", ...q });
  // A fresh page each time: the address's query decides the theme and language.
  await page.goto("about:blank");
  await page.goto(`${BASE}?${qs}${hash}`, { waitUntil: "networkidle2" });
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
  await page.screenshot({ path: `${OUT}${name}${TAG}.png` });
  console.log(name + TAG);
}
await browser.close();
