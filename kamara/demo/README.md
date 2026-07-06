# Kamara end-to-end demo

Headless Playwright walk-through of the whole Kamara file UI against a live
tenant: real OIDC login, then browse → create folder → upload → open the
details drawer → download, with a screenshot at each step. This is the M4
acceptance artifact and a smoke test for the browser surface.

## Run

Point it at a deployed Kamara and pass the tenant's `initial-admin`
credentials (from the `initial-admin` Secret in the tenant namespace):

```sh
npm ci
npx playwright install --with-deps chromium   # first time
KAMARA_URL=http://kamara.kam.127.0.0.1.sslip.io:9080 \
KAMARA_USER=admin \
KAMARA_PASS="$(kubectl get secret initial-admin -n tenant-kam -o jsonpath='{.data.password}' | base64 -d)" \
npm run demo
```

Screenshots are written to `./screenshots/` (gitignored). Exit code is
non-zero on any step failure (an `99-error.png` is captured).
