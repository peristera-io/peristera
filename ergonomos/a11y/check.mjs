// Accessibility gate for the Ergonomos UI (README §4: automated a11y in
// CI; EN 301 549 / EAA as the bar). Loads the rendered page HTML, injects
// axe-core, runs the WCAG 2.1 A/AA ruleset, and exits non-zero on any
// violation. The HTML is produced headlessly by `go run ./cmd/a11y-render`
// so no cluster or login is needed.
//
//   node check.mjs <path-to-rendered.html>
import { chromium } from 'playwright';
import { readFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import { pathToFileURL } from 'node:url';

const htmlPath = process.argv[2] || 'rendered.html';
const require = createRequire(import.meta.url);
const axeSource = readFileSync(require.resolve('axe-core'), 'utf8');

const browser = await chromium.launch();
const page = await browser.newPage();
try {
  await page.goto(pathToFileURL(htmlPath).href);
  await page.addScriptTag({ content: axeSource });
  const results = await page.evaluate(async () =>
    await axe.run(document, { runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa'] } }));

  if (results.violations.length === 0) {
    console.log(`a11y: PASS — ${results.passes.length} checks passed, 0 violations (WCAG 2.1 AA)`);
  } else {
    console.error(`a11y: FAIL — ${results.violations.length} violation(s):`);
    for (const v of results.violations) {
      console.error(`\n[${v.impact}] ${v.id}: ${v.help}`);
      console.error(`  ${v.helpUrl}`);
      for (const n of v.nodes) console.error(`  → ${n.target.join(' ')}`);
    }
    process.exitCode = 1;
  }
} catch (e) {
  console.error('a11y check errored:', String(e).split('\n')[0]);
  process.exitCode = 2;
} finally {
  await browser.close();
}
