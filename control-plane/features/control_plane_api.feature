Feature: Control-plane API
  Everything the UI can do is a documented, versioned API (API-first,
  README §4; spec: api/openapi.yaml). Operators authenticate against the
  default Zitadel instance; requests without credentials are rejected.

  Scenario: Unauthenticated requests are rejected
    When I list tenants via the API without credentials
    Then the API answers 401

  Scenario: Tenant lifecycle through the API
    When I create tenant "api1" via the API
    Then the API shows tenant "api1" with phase "Ready" within 3 minutes
    When I delete tenant "api1" via the API
    Then the API answers 404 for tenant "api1" within 2 minutes
