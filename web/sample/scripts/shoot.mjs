// Screenshots and motion clips of the sample. Serve the build first (npm run build && npm run preview),
// then: CHROME=<chrome-headless-shell> FONT_DIR=<dir with Inter + Noto Sans SC> node scripts/shoot.mjs [filter]
// FONT_DIR gives the browser a fontconfig that prefers those fonts, so the shots look like a system UI
// rather than the server's fallback fonts. The browser is muted.
import { execFileSync } from "node:child_process";
import { mkdirSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import puppeteer from "puppeteer-core";

const BASE = process.env.BASE ?? "http://127.0.0.1:7821/";
const OUT = new URL("../shots/", import.meta.url).pathname;
const only = process.argv[2];
mkdirSync(OUT, { recursive: true });

const desk = { width: 1440, height: 900, deviceScaleFactor: 1 };
const phone = { width: 390, height: 844, deviceScaleFactor: 2, isMobile: true, hasTouch: true };

/** name, viewport, query, optional action before the shot */
const shots = [];
for (const theme of ["light", "dark"])
  for (const lang of ["zh", "en"]) {
    shots.push([`desktop-${theme}-${lang}`, desk, { theme, lang }]);
    shots.push([`phone-${theme}-${lang}`, phone, { theme, lang }]);
    shots.push([`phone-list-${theme}-${lang}`, phone, { theme, lang, view: "list" }]);
  }
shots.push(
  ["desktop-approval-details-light-en", desk, { theme: "light", lang: "en", details: 1 }],
  ["desktop-context-light-en", desk, { theme: "light", lang: "en", open: "context" }],
  ["desktop-context-dark-zh", desk, { theme: "dark", lang: "zh", open: "context" }],
  ["desktop-mode-light-zh", desk, { theme: "light", lang: "zh", open: "mode" }],
  ["desktop-menu-dark-en", desk, { theme: "dark", lang: "en", open: "menu" }],
  ["desktop-plus-light-zh", desk, { theme: "light", lang: "zh", open: "plus" }],
  ["desktop-picker-light-en", desk, { theme: "light", lang: "en", open: "picker", newfolder: 1 }],
  ["desktop-picker-dark-zh", desk, { theme: "dark", lang: "zh", open: "picker", newfolder: 1 }],
  ["desktop-wsplus-light-en", desk, { theme: "light", lang: "en", open: "wsplus" }],
  ["desktop-me-light-zh", desk, { theme: "light", lang: "zh", open: "me" }],
  ["desktop-me-dark-en", desk, { theme: "dark", lang: "en", open: "me" }],
  ["desktop-perms-light-en", desk, { theme: "light", lang: "en", open: "perms" }],
  ["desktop-toast-light-zh", desk, { theme: "light", lang: "zh", toast: 1 }],
  ["desktop-quota-tip-light-en", desk, { theme: "light", lang: "en" }, async (p) => p.hover('[role="meter"]:nth-of-type(2)')],
  ["phone-plus-light-en", phone, { theme: "light", lang: "en", view: "list", open: "plus" }],
  ["phone-picker-light-zh", phone, { theme: "light", lang: "zh", view: "list", open: "picker", newfolder: 1 }],
  ["phone-me-dark-zh", phone, { theme: "dark", lang: "zh", view: "list", open: "me" }],
  ["phone-context-dark-en", phone, { theme: "dark", lang: "en", open: "context" }],
  ["desktop-solid-light-en", desk, { theme: "light", lang: "en", open: "context" }, null, [{ name: "prefers-reduced-transparency", value: "reduce" }]],
);

const env = { ...process.env };
if (process.env.FONT_DIR) {
  const dir = `${tmpdir()}/iridium-sample-fc`;
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

async function open(vp, q) {
  await page.setViewport(vp);
  const qs = new URLSearchParams({ still: "1", ...Object.fromEntries(Object.entries(q).map(([k, v]) => [k, String(v)])) });
  await page.goto(`${BASE}?${qs}`, { waitUntil: "networkidle0" });
  await page.evaluate(() => document.fonts.ready);
  await new Promise((r) => setTimeout(r, 600));
}

for (const [name, vp, q, act, media] of shots) {
  if (only && !name.includes(only)) continue;
  await page.emulateMediaFeatures(media ?? []);
  await open(vp, q);
  if (act) {
    await act(page);
    await new Promise((r) => setTimeout(r, 900));
  }
  await page.screenshot({ path: `${OUT}${name}.png` });
  console.log(name);
}

// Motion: record the send flight and a popover unfolding, then cut a strip of frames to look at.
async function clip(name, vp, q, run, crop = "iw:ih:0:0", start = 0.2) {
  if (only && !name.includes(only)) return;
  await open(vp, { ...q, approval: 0, still: 0 });
  const rec = await page.screencast({ path: `${OUT}${name}.webm` });
  await new Promise((r) => setTimeout(r, 200));
  await run();
  await new Promise((r) => setTimeout(r, 1200));
  await rec.stop();
  // 10 frames at 30 fps (33 ms apart) from the moment the action starts, cropped, tiled 5×2.
  execFileSync("ffmpeg", ["-loglevel", "error", "-y", "-ss", String(start), "-i", `${OUT}${name}.webm`, "-vf", `fps=30,crop=${crop},tile=5x2:padding=4:color=gray`, "-frames:v", "1", `${OUT}${name}-frames.png`]);
  console.log(name);
}

await clip(
  "motion-send",
  desk,
  { theme: "light", lang: "en", draft: "Ship it after the screenshots look right." },
  async () => {
    await page.focus("textarea");
    await page.keyboard.press("Enter");
  },
  "700:360:540:520",
);
await clip(
  "motion-popover",
  desk,
  { theme: "light", lang: "en" },
  async () => {
    await page.click('button[aria-label^="Context"]');
  },
  "420:330:820:540",
);
await clip(
  "motion-plus",
  desk,
  { theme: "light", lang: "en" },
  async () => {
    await page.click('button[aria-label="New"]');
  },
  "300:160:0:0",
);
await clip(
  "motion-phone-open",
  phone,
  { theme: "light", lang: "zh", view: "list" },
  async () => {
    await page.tap('[role="treeitem"][aria-label^="Base UI"]');
  },
  "iw/2:ih/2:0:0",
);

await browser.close();
