@kamara
Feature: Kamara storage API
  Kamara stores files behind an OpenFGA-authorized storage API (M4a). A
  caller holding a valid token from the tenant's own issuer round-trips a
  file through the deployed API — the storage-API-v0 acceptance (Q&A R41).
  Cross-app attachment (Ergonomos <-> Kamara) is deferred to M4b via the
  browser flow (option C), so this exercises the ordinary user-auth path,
  not service-to-service trust.

  Scenario: A file round-trips through the storage API
    Given a tenant "kam" exists
    And kamara of tenant "kam" is healthy within 3 minutes
    When I upload "hello kamara" as "hello.txt" to kamara of tenant "kam"
    Then the file is listed in kamara of tenant "kam"
    And downloading the file from kamara of tenant "kam" returns "hello kamara"
    And another user cannot reach the file in kamara of tenant "kam"
    And deleting the file from kamara of tenant "kam" succeeds
    And the file is not listed in kamara of tenant "kam"

  Scenario: Files organize into a folder hierarchy
    Given a tenant "kam" exists
    And kamara of tenant "kam" is healthy within 3 minutes
    When I create a folder "docs" in kamara of tenant "kam"
    And I upload "in folder" as "report.txt" into that folder in kamara of tenant "kam"
    Then that folder lists the file in kamara of tenant "kam"
    And another user cannot list that folder in kamara of tenant "kam"
    And moving the file to the root in kamara of tenant "kam" succeeds
    And deleting that folder in kamara of tenant "kam" succeeds
