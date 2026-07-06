# Peristera Control Plane

The tenant lifecycle manager and the MSP product: new customer → isolated
tenant (namespace, dedicated Postgres, Zitadel virtual instance on its own
domain, app pods from a curated catalog) — created, upgraded, and deleted
as one operation. Architecture: `adr/0008` (Tenant CRD + controller-runtime;
tenant CRs are the source of truth, no database until billing/quotas).

**Status: M2 in progress** (`docs/m2-plan.md`). Read the monorepo
`README.md` first — it is the operating manual.

License: AGPL-3.0-or-later with the Peristera App Store distribution
exception (`LICENSE-EXCEPTION.md`); openness decided in README §7.

## Layout

```text
control-plane/
├── README.md            ← this file
├── api/openapi.yaml     ← the /api/v1 contract — authored FIRST (ADR-0007)
├── apis/v1alpha1/       ← Tenant types (slug immutable via CEL)
├── cmd/controller/      ← one binary: controller + UI/API server
├── features/            ← godog specs (the dev loop starts here)
├── internal/controller/ ← Tenant reconcile loop + app catalog
├── internal/server/     ← HTMX UI, /api/v1 handlers (gen/ = oapi-codegen)
├── internal/zitadel/    ← system-API client (ADR-0006 §5–6)
└── deploy/crd/          ← generated CRD manifest (controller-gen)
```

## UI and API

One process serves both (`CP_LISTEN_ADDR`, default `:8090`): the HTMX UI
for operators (OIDC login against the *default* Zitadel instance — the
server idempotently registers its own `control-plane` app there at
startup) and `/api/v1` per `api/openapi.yaml` (bearer tokens — a machine
user's PAT works; validated via userinfo). Regenerate API stubs after
editing the spec: `go generate ./internal/server/...`.

## Dev loop

The loop is spec-first (working agreement #2): change
`features/*.feature`, watch it fail, implement, watch it pass.

```sh
# BDD suite against the live dev cluster (controller must be running):
PERISTERA_E2E=1 go test -run TestFeatures -timeout 15m .

kubectl apply -f deploy/crd/peristera.io_tenants.yaml
# SYSTEM_USER_KEY enables IAM provisioning (ADR-0006 §6); without it the
# controller only manages namespace + database:
SYSTEM_USER_KEY=path/to/admin-client.key go run ./cmd/controller
kubectl apply -f - <<'EOF'
apiVersion: peristera.io/v1alpha1
kind: Tenant
metadata: {name: demo2}
spec: {slug: demo2, displayName: "Second Demo GmbH"}
EOF
kubectl get tenants -w    # Pending → Ready (namespace + CNPG Postgres)
kubectl delete tenant demo2   # finalizer + GC tear everything down
```

Regenerate after changing `apis/`:

```sh
go run sigs.k8s.io/controller-tools/cmd/controller-gen@latest object paths=./apis/...
go run sigs.k8s.io/controller-tools/cmd/controller-gen@latest crd paths=./apis/... output:crd:dir=deploy/crd
```

## Key references

- ADR-0006 — the tenant-IAM provisioning sequence the controller implements
- ADR-0007 — object identity: `spec.slug` immutable, tenant domain = issuer
- ADR-0008 — this component's architecture and its post-public-demo review rider
- `iam/README.md` — the proven System-API calls (the controller's spec)
