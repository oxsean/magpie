// Run with Node's test runner and Playwright on the module path; see README.md.
// A subscription's risk warning (Claude: "Claude accounts can be
// suspended") is read before its account is added, so its text keeps at
// least 320px (or the box's width, less the mark, when that is less), and
// where Cancel, Import instead… and Sign in anyway don't fit beside it they
// go on a line under it, at its right. They squeezed the text into a narrow
// column beside them, one or two words a line. In the provider's
// accounts (Add another) and on the Add page (a first account), in every
// language, at 1100px and 420px, Chromium and WebKit.
const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const path = require("node:path");
const { test } = require("node:test");
const { chromium, webkit } = require("playwright");

const assets = process.env.MAGPIE_RISK_ASSETS || path.resolve(__dirname, "../assets");
const claude = {
  id: "claude", name: "Claude Code", icon: "claudecode-color", chat: "", responses: "", anthropic: "https://api.example.test", catalog: "",
  models: [{ id: "claude-opus-5-5", name: "Opus 5.5", on: true }], agents: [], fallback: [], headers: {}, keyList: [],
  account: { agent: "claude", agentName: "Claude Code", user: "pro@example.com", plan: "pro", logins: [{ user: "pro@example.com", plan: "pro", active: true, on: true }] },
};

function serve(lang, list) {
  return async (route) => {
    const url = new URL(route.request().url());
    const json = (data) => route.fulfill({ json: data });
    if (url.pathname === "/boot.js") return route.fulfill({ contentType: "text/javascript", body: `window.bootPrefs = {lang:"${lang}",theme:"light",web:true};` });
    if (url.pathname === "/wails/runtime.js") return route.fulfill({ contentType: "text/javascript", body: "export const Window = {};" });
    if (url.pathname === "/api/state") return json({ agents: [], profiles: [], settings: { lang, theme: "light" } });
    if (url.pathname === "/api/providers") return json({ providers: list, presets: [], excluded: [], gateway: { running: true, window: true } });
    if (url.pathname === "/api/groups") return json({ groups: [] });
    if (url.pathname === "/api/plugins") return json({ plugins: [] });
    if (url.pathname.startsWith("/api/")) return json({});
    const file = path.join(assets, url.pathname === "/" ? "index.html" : url.pathname);
    const contentType = { ".html": "text/html", ".js": "text/javascript", ".css": "text/css", ".svg": "image/svg+xml", ".png": "image/png" }[path.extname(file)];
    await route.fulfill({ body: await fs.readFile(file), contentType });
  };
}

for (const engine of (process.env.BROWSER ? [process.env.BROWSER] : ["chromium", "webkit"])) {
  for (const lang of ["en", "zh", "zh-TW", "ja", "de"]) {
    for (const width of [1100, 420]) {
      for (const where of ["accounts", "add"]) {
        test(`${engine} ${lang} ${width} ${where}: a risk warning takes the box's width, its actions under it`, async (t) => {
          const browser = await (engine === "webkit" ? webkit.launch() : chromium.launch({ channel: "chromium" }));
          t.after(() => browser.close());
          const page = await (await browser.newContext({ viewport: { width, height: 900 }, reducedMotion: "reduce" })).newPage();
          page.setDefaultTimeout(5000);
          const errors = [];
          page.on("pageerror", (e) => errors.push(e.message));
          await page.route("**/*", serve(lang, where === "add" ? [] : [claude]));
          await page.goto("http://magpie.test/?view=providers");
          const words = await page.evaluate((lang) => {
            const d = lang === "en" ? {} : I18N[lang];
            return Object.fromEntries(["Sign in anyway", "Cancel", "Import instead…"].map((k) => [k, d[k] || k]));
          }, lang);
          if (where === "accounts") {
            await page.locator(".row.provider", { hasText: "Claude Code" }).first().click();
            await page.locator(".editor .accts .acc.add").first().click();
          } else {
            await page.locator("button:visible", { hasText: /^\s*Claude\s*$/ }).first().click();
          }
          const box = page.locator(".signing", { has: page.locator("button.primary", { hasText: words["Sign in anyway"] }) });
          await box.locator("button.primary").waitFor();
          const boxR = await box.boundingBox();
          const ttR = await box.locator(".tt").boundingBox();
          const acts = await box.locator("button").evaluateAll((bs) => bs.map((b) => { const r = b.getBoundingClientRect(); return { text: b.textContent.trim(), top: r.top, right: r.right }; }));
          assert.deepEqual(acts.map((a) => a.text), [words["Cancel"], words["Import instead…"], words["Sign in anyway"]]);
          assert.ok(ttR.width >= Math.min(320, boxR.width - 60), `the warning keeps room to be read: ${Math.round(ttR.width)} of ${Math.round(boxR.width)}`);
          const beside = acts.every((a) => a.top < ttR.y + ttR.height), under = acts.every((a) => a.top >= ttR.y + ttR.height - 1);
          assert.ok(beside || under, "the actions are all beside the warning or all under it");
          if (under) assert.ok(ttR.width >= boxR.width * 0.8, "under it, the warning has the box's width");
          const last = acts[acts.length - 1];
          assert.ok(boxR.x + boxR.width - last.right < 24, "the actions end at the box's right");
          const over = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
          assert.ok(over <= 0, `no sideways scroll (${over}px)`);
          assert.deepEqual(errors, []);
        });
      }
    }
  }
}
