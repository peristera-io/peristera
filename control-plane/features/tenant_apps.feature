Feature: Tenant applications
  Tenant creation deploys the catalog apps into the tenant's namespace
  (ADR-0008: hardcoded catalog until a second app exists). Apps live at
  <app>.<slug>.<base-domain> and authenticate against the tenant's own
  issuer. Users are created explicitly by the operator (the API returns the
  one-time password), not silently provisioned into a Secret.

  Scenario: The catalog app runs on a fresh tenant
    Given a tenant "bdd" exists
    Then the app "stub" of tenant "bdd" answers on its own domain within 3 minutes
    And the app "stub" of tenant "bdd" sends logins to the tenant's issuer
    And creating an admin user for tenant "bdd" returns login credentials
