# Kamara e2e — office editing round-trip (M6 s3)

`edit-e2e.mjs` drives the browser office-editing acceptance against a live dev
cluster (ADR-0018): log in to Kamara, upload an `.odt`, open it in Collabora
(the `/edit` page), confirm the engine renders it (the WOPI open path —
`CheckFileInfo`/`GetFile`), then save an edit back and confirm Kamara wrote a
new version (the WOPI `PutFile` path).

The save is driven by presenting the same per-session token Collabora holds
directly to the WOPI host: Collabora itself issues `PutFile` on a human save
(WOPI-standard, unit-tested in `internal/api`), but reliably simulating a real
edit inside its canvas via automation is impractical, so the script exercises
the real deployed host with the real minted token instead.

## Run

Requires the dev cluster up (`hack/dev-cluster.sh`) with a tenant that has the
office app enabled (`Tenant.spec.apps: [office]`), and Playwright's Chromium
(vendored under `kamara/a11y/node_modules`).

```sh
# from kamara/e2e
ln -sfn ../a11y/node_modules node_modules          # ESM needs playwright-core resolvable
KAM_USER=admin KAM_PASS='<initial-admin password>' node edit-e2e.mjs
```

The admin password is in the tenant's `initial-admin` secret:
`kubectl get secret initial-admin -n tenant-<slug> -o jsonpath='{.data.password}' | base64 -d`.

Screenshots land in `/tmp/edit-*.png`. Expected tail:

```
EDITOR LOADED (Collabora rendered the document via WOPI open path)
PUTFILE status 200 X-WOPI-ItemVersion 1
REOPEN GetFile bytes match edited: true
```
