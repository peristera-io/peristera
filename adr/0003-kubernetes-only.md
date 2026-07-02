# ADR-0003: Kubernetes as the only deployment contract

- **Status:** accepted
- **Date:** 2026-07-02
- **Provenance:** Q&A.md Rounds 1–2 (Q12–13, R1)

## Context

Peristera is suite-first in messaging but platform-first in architecture: the
control plane provisions a namespace, a dedicated Postgres, and app pods per
tenant, and later clones whole tenants into staging environments. That
requires a controlled, uniform runtime. Supporting both docker-compose and
Kubernetes would double the support matrix and forfeit control. But the
Phase 0 audience — the self-hosting community — largely runs single VMs.

## Decision

The Kubernetes API is the **only** deployment contract. Tenant isolation is
namespace-per-tenant with a dedicated Postgres (CloudNativePG) and OpenFGA
instance per namespace. Exactly two documented installation paths:

1. **One VM:** a one-command **k3s installer** (single Hetzner-class machine
   is enough).
2. **Bring your own cluster** (MSPs and larger self-hosters).

Docker images are published, but running them outside Kubernetes is
explicitly unsupported.

## Consequences

- One runtime to test, secure, upgrade, and document; the control plane's
  namespace/clone/upgrade features are buildable at all.
- Clean per-tenant blast radius, data separation, off-boarding, and
  crypto-shredding boundaries.
- Higher entry barrier for casual self-hosters — mitigated by the k3s
  installer; accepted as a named risk (README §10).
- Local development runs k3s from M2 onward.

## Alternatives considered

- **docker-compose + k8s dual support:** double support matrix, uncontrolled
  environments, and the platform features (staging clones, catalog) don't
  map onto compose. Rejected.
- **Shared-instance multi-tenancy** (one big deployment, tenant column):
  weaker isolation, harder off-boarding and GDPR posture; rejected in favor
  of namespace-per-tenant.
