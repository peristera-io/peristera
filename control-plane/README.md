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
├── apis/v1alpha1/       ← Tenant types (slug immutable via CEL)
├── cmd/controller/      ← manager entry point (out-of-cluster dev)
├── internal/controller/ ← Tenant reconcile loop
└── deploy/crd/          ← generated CRD manifest (controller-gen)
```

## Dev loop

```sh
kubectl apply -f deploy/crd/peristera.io_tenants.yaml
go run ./cmd/controller   # against the current kubeconfig (k3d)
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
- ADR-0008 — this component's architecture and its post-M6 review rider
- `iam/README.md` — the proven System-API calls (the controller's spec)
