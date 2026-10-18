// Run with Node's test runner and Playwright on the module path; see README.md.
// #2392: a Claude account added by its setup-token runs beside Claude
// Code's own sign-in and is never switched in. Its row's plan says
// Setup-token and it has no Use or Make first; refused, it has no Sign in
// again (a new token is added instead); the Keep signed in pick leaves it
// out. Add by setup-token…, beside Add another, asks a name and the
// token in fields of their own, the token unseen, and posts them as one line of an import.
// The import box says tokens can be pasted there too. In every language,
// at 900px and 380px, Chromium and WebKit.
const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const path = require("node:path");
const { test } = require("node:test");
const { chromium, webkit } = require("playwright");

const assets = path.resolve(__dirname, "../assets");
const lapse = "Anthropic refused this setup-token; run `claude setup-token` and add the new token in magpie";
const claude = {
  id: "claude", name: "Claude Code", icon: "claudecode-color", chat: "", responses: "", anthropic: "https://api.example.test", catalog: "",
  models: [{ id: "claude-opus-5-5", name: "Opus 5.5", on: true }], agents: [], fallback: [], headers: {}, keyList: [],
  account: {
    agent: "claude", agentName: "Claude Code", user: "pro@example.com", plan: "pro",
    logins: [
      { user: "pro@example.com", plan: "pro", active: true, on: true },
      { user: "max@example.com", plan: "max", on: true },
      { user: "headless", plan: "setup-token", on: true, token: true },
      { user: "old box", plan: "setup-token", on: true, token: true, lapsed: lapse },
    ],
  },
};

function serve(lang, posted) {
  const state = { agents: [], profiles: [], settings: { lang, theme: "light" } };
  const providers = { providers: [claude], presets: [], excluded: [], gateway: { running: true, window: true } };
  return async (route) => {
    const url = new URL(route.request().url());
    const json = (data) => route.fulfill({ json: data });
    if (url.pathname === "/boot.js") return route.fulfill({ contentType: "text/javascript", body: `window.bootPrefs = {lang:"${lang}",theme:"light",web:true};` });
    if (url.pathname === "/wails/runtime.js") return route.fulfill({ contentType: "text/javascript", body: "export const Window = {};" });
    if (url.pathname === "/api/state") return json(state);
    if (url.pathname === "/api/providers") return json(providers);
    if (url.pathname === "/api/groups") return json({ groups: [] });
    if (url.pathname === "/api/plugins") return json({ plugins: [] });
    if (url.pathname === "/api/signin/import") {
      posted.push(route.request().postDataJSON());
      return json({ providers, results: [{ user: "work box", status: "added" }] });
    }
    if (url.pathname.startsWith("/api/")) return json({});
    const file = path.join(assets, url.pathname === "/" ? "index.html" : url.pathname);
    const contentType = { ".html": "text/html", ".js": "text/javascript", ".css": "text/css", ".svg": "image/svg+xml", ".png": "image/png" }[path.extname(file)];
    await route.fulfill({ body: await fs.readFile(file), contentType });
  };
}

const strings = [
  "Choose or paste CLIProxyAPI's auth files (JSON) or {own}, or tokens from claude setup-token, one a line.",
  "A file's account is kept as it is; a token is tried first.",
  "Add by setup-token…",
  "Add a Claude account by its setup-token",
  "Run {cmd} where Claude Code is signed in, and paste the token it prints. magpie tries it once through Claude Code before adding it.",
  "Name (optional)",
];

for (const engine of (process.env.BROWSER ? [process.env.BROWSER] : ["chromium", "webkit"])) {
  for (const lang of ["en", "zh", "zh-TW", "ja", "de"]) {
    for (const width of [900, 380]) {
      test(`${engine} ${lang} ${width}: a setup-token account runs beside Claude Code's own`, async (t) => {
        const browser = await (engine === "webkit" ? webkit.launch() : chromium.launch({ channel: "chromium" }));
        t.after(() => browser.close());
        const page = await (await browser.newContext({ viewport: { width, height: 900 }, reducedMotion: "reduce" })).newPage();
        page.setDefaultTimeout(5000);
        const errors = [], posted = [];
        page.on("pageerror", (e) => errors.push(e.message));
        await page.route("**/*", serve(lang, posted));
        await page.goto("http://magpie.test/?view=providers");
        await page.locator(".row.provider", { hasText: "Claude Code" }).first().click();
        await page.locator(".editor .acc").first().waitFor();

        const tr = await page.evaluate(([lang, keys]) => {
          const d = lang === "en" ? {} : I18N[lang];
          return Object.fromEntries(["Use", "Make first", "Sign in again", "Sign-in required", "Remove", "Add", "Cancel", "Add another {name} account", "Sign in anyway", ...keys].map((k) => [k, d[k] ?? (lang === "en" ? k : null)]));
        }, [lang, strings]);
        assert.deepEqual(strings.filter((k) => !tr[k]), [], `every new string has its ${lang}`);
        if (lang !== "en") {
          const slots = (s) => (s.match(/\{\w+\}/g) || []).sort().join();
          assert.deepEqual(strings.filter((k) => slots(k) !== slots(tr[k])), [], "the same placeholders");
        }

        const row = (user) => page.locator(".editor .accts .acc", { has: page.locator(`text="${user}"`) });
        const buttons = async (user) => (await row(user).locator("button.text").allTextContents()).map((s) => s.trim());
        const token = row("headless");
        assert.equal(await token.count(), 1);
        assert.equal((await token.locator(".plan").textContent()).trim(), "Claude Setup-token");
        assert.deepEqual(await buttons("headless"), [tr["Remove"]], "no Use or Make first");
        assert.ok((await buttons("max@example.com")).includes(tr["Make first"]), "a signed-in account keeps Make first");

        const lapsed = row("old box");
        assert.equal((await lapsed.locator(".using").textContent()).trim(), tr["Sign-in required"]);
        assert.deepEqual(await buttons("old box"), [tr["Remove"]], "no Sign in again");
        assert.match(await lapsed.locator(".aq").getAttribute("title"), /claude setup-token/, "it says what mends it");

        const pick = page.locator(".editor .accts .keep-login button.keep-as");
        await pick.click();
        const items = (await page.locator(".proto-menu .pm-item").allTextContents()).join("\n");
        assert.match(items, /max@example\.com/);
        assert.doesNotMatch(items, /headless|old box/, "the pick leaves setup-token accounts out");
        await page.keyboard.press("Escape");
        await page.locator(".proto-menu").waitFor({ state: "detached" });

        // its own way in, beside Add another
        const adds = (await page.locator(".editor .accts .acc.add").allTextContents()).map((x) => x.trim());
        assert.equal(adds[1], tr["Add by setup-token…"]);
        const form = page.locator(".signing.import");
        const fill = async (name, token) => {
          await form.waitFor();
          assert.equal((await form.locator(".n").first().textContent()).trim(), tr["Add a Claude account by its setup-token"]);
          assert.match(await form.textContent(), /claude setup-token/);
          const [n, k] = [form.locator("input").nth(0), form.locator("input").nth(1)];
          assert.equal(await n.getAttribute("placeholder"), tr["Name (optional)"]);
          assert.equal(await k.getAttribute("type"), "password", "the token is typed unseen");
          const add = form.locator("button.primary");
          assert.equal((await add.textContent()).trim(), tr["Add"]);
          assert.equal(await add.isDisabled(), true);
          await k.fill("not a token");
          assert.equal(await add.isDisabled(), true, "only a setup-token is taken");
          await n.fill(name);
          await k.fill(token);
          assert.equal(await add.isDisabled(), false);
          return add;
        };
        const top = await page.evaluate(() => document.scrollingElement.scrollTop);
        await page.locator(".editor .accts .acc.add").nth(1).click();
        await (await fill("work box", "sk-ant-oat01-test")).click();
        await page.locator(".signing.import .results").waitFor();
        assert.deepEqual(posted.pop(), { agent: "claude", files: ["work box sk-ant-oat01-test"] }, "the name and the token, one line");
        await page.locator(".signing.import button.text").first().click();
        await page.locator(".signing.import").waitFor({ state: "detached" });

        // the box Add another opens keeps its three ways
        await page.locator(".editor .accts .acc.add", { hasText: tr["Add another {name} account"].replace("{name}", "Claude") }).click();
        const risk = page.locator(".editor .accts .signing");
        await risk.locator("button", { hasText: tr["Sign in anyway"] }).waitFor();
        assert.equal(await risk.locator("button", { hasText: tr["Add by setup-token…"] }).count(), 0);
        await risk.locator("button", { hasText: tr["Cancel"] }).click();
        await risk.waitFor({ state: "detached" });

        const imp = page.locator(".editor .accts .acc.add").nth(2);
        await imp.click();
        const box = page.locator(".signing.import");
        await box.waitFor();
        assert.match(await box.textContent(), /claude setup-token/);
        const text = "work box sk-ant-oat01-test";
        await box.locator("textarea").fill(text);
        await box.locator("button.primary").click();
        await page.locator(".signing.import .results").waitFor();
        assert.deepEqual(posted, [{ agent: "claude", files: [text] }], "what is pasted is posted as it is");
        if (width === 900) assert.equal(await page.evaluate(() => document.scrollingElement.scrollTop), top, "the page doesn't move");
        const over = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
        assert.ok(over <= 0, `no sideways scroll (${over}px)`);
        assert.deepEqual(errors, []);
      });
    }
  }
}
