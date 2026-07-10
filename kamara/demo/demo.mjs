// End-to-end browser demo for Kamara (M4 acceptance artifact): drives the
// real OIDC login against the tenant issuer, then exercises the whole file
// UI — browse, create a folder, upload a file, open the details drawer, and
// download — capturing a screenshot at each step. Headless Playwright.
//
//   KAMARA_URL=http://kamara.<slug>.<base>:<port> \
//   KAMARA_USER=admin KAMARA_PASS=... \
//   node demo.mjs
//
// Credentials come from the tenant's `initial-admin` Secret. Screenshots land
// in DEMO_OUT (default ./screenshots).
import { chromium } from "playwright";
import { writeFileSync, mkdirSync } from "node:fs";

const base = process.env.KAMARA_URL || "http://kamara.kam.127.0.0.1.sslip.io:9080";
const user = process.env.KAMARA_USER || "admin";
const pass = process.env.KAMARA_PASS;
const out = process.env.DEMO_OUT || new URL("./screenshots", import.meta.url).pathname;
if (!pass) {
  console.error("KAMARA_PASS is required");
  process.exit(2);
}
mkdirSync(out, { recursive: true });
const uploadPath = out + "/demo-upload.txt";
writeFileSync(uploadPath, "Hello from the Kamara end-to-end demo!\n".repeat(20));

const shot = (p, n) => p.screenshot({ path: `${out}/${n}.png`, fullPage: true });
const step = (m) => console.log("•", m);

const browser = await chromium.launch();
const ctx = await browser.newContext({ acceptDownloads: true });
const p = await ctx.newPage();

try {
  step("navigate → OIDC login");
  await p.goto(base + "/", { waitUntil: "networkidle" });
  await shot(p, "01-login");

  step("sign in as " + user);
  await p.fill('input[name="loginName"]', user);
  await p.click('button:has-text("Continue")');
  await p.waitForSelector('input[type="password"]');
  await p.fill('input[type="password"]', pass);
  await p.click('button:has-text("Continue")');

  step("land on the file browser");
  await p.waitForSelector("text=New folder", { timeout: 20000 });
  await shot(p, "02-browser-empty");

  const folderName = "Demo-" + Date.now(); // unique per run (no accumulation)
  step('create folder "' + folderName + '"');
  const folderForm = p.locator('form[hx-post^="/folders?at"]');
  await folderForm.locator('input[name="name"]').fill(folderName);
  await folderForm.getByRole("button", { name: "Create" }).click();
  await p.getByRole("link", { name: folderName }).waitFor({ timeout: 10000 });
  await shot(p, "03-folder-created");

  step("descend into the folder");
  await p.getByRole("link", { name: folderName }).click();
  await p.locator("nav[aria-label] li", { hasText: folderName }).waitFor({ timeout: 10000 });
  await shot(p, "04-inside-folder");

  step("upload a file");
  const uploadForm = p.locator('form[hx-post^="/files?at"]');
  await uploadForm.locator('input[type="file"]').setInputFiles(uploadPath);
  await uploadForm.locator('button[type="submit"]').click();
  await p.getByText("demo-upload.txt").waitFor({ timeout: 15000 });
  await shot(p, "05-uploaded");

  step("open the details drawer");
  await p.getByRole("button", { name: /Details demo-upload/ }).click();
  await p.locator("[data-drawer]").waitFor({ timeout: 10000 });
  await shot(p, "06-details-drawer");

  step("download the file");
  const [download] = await Promise.all([
    p.waitForEvent("download"),
    p.getByRole("link", { name: "Download", exact: true }).first().click(),
  ]);
  step("downloaded as " + download.suggestedFilename());

  step("DONE — full flow passed; screenshots in " + out);
} catch (e) {
  await shot(p, "99-error");
  console.error("demo failed:", String(e).split("\n")[0]);
  process.exitCode = 1;
} finally {
  await browser.close();
}
