# Landing page (peristera.io)

The public "what/why" page for Peristera — a single, self-contained
`index.html` (inline CSS, no build, no external assets). Served on the cloud by
a tiny nginx from a ConfigMap; see `deploy/scaleway/manifests/landing.yaml` and
the `landing` step in `deploy/scaleway/bootstrap.sh`.

To change the page, edit `index.html` and re-run the landing step (or
`kubectl create configmap landing-html -n landing --from-file=index.html --dry-run=client -o yaml | kubectl apply -f -`
then `kubectl rollout restart deploy/landing -n landing`).

`peristera.io` must be delegated to Scaleway DNS (like `peristera.app`) so
external-dns publishes the record and cert-manager issues the TLS cert. This is
M7 s3 — a landing page, deliberately minimal; the full marketing/`.dev` sites
come later.
