Feature: Tenant lifecycle
  The control plane converges a Tenant resource to a working, isolated
  tenant stack — namespace, database, IAM with its own OIDC issuer — and
  removes it cleanly (ADR-0008; IAM sequence ADR-0006 §6).

  Scenario: Provisioning a tenant
    When I create a tenant "bdd" with display name "BDD GmbH"
    Then the tenant "bdd" reaches phase "Ready" within 3 minutes
    And the namespace "tenant-bdd" exists
    And the tenant "bdd" status reports an issuer and a client ID
    And OIDC discovery on the issuer of tenant "bdd" answers with the same issuer

  Scenario: The slug is immutable
    Given a tenant "bdd" exists
    When I try to change the slug of tenant "bdd" to "other"
    Then the change is rejected with message "slug is immutable"

  Scenario: Off-boarding a tenant
    Given a tenant "bdd" exists
    When I delete the tenant "bdd"
    Then the tenant "bdd" is gone within 2 minutes
    And the namespace "tenant-bdd" is gone within 2 minutes
    And OIDC discovery on the former issuer of tenant "bdd" stops answering

  # Deleting soon after creation is the projection-lag window where the
  # System API can 404 while the instance still exists — off-boarding must
  # leave no orphaned Zitadel instance regardless (ADR-0006).
  Scenario: Off-boarding leaves no orphaned instance
    When I create a tenant "orphan" with display name "Orphan GmbH"
    And I delete the tenant "orphan" once it has an instance
    Then no Zitadel instance for tenant "orphan" remains within 3 minutes
