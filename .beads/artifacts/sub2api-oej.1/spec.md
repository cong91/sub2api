# [sub2api-oej.1] Implement provider-catalog endpoint for linkto-provider

## Identity
- bead_id: sub2api-oej.1
- jira_key: none
- jira_type: task
- parent_jira_key: sub2api-oej
- repo: sub2api

## Objective
Add one narrow backend endpoint that returns normalized provider metadata sufficient for linkto-provider: provider_id, provider_name, api_style, base_url, default_model, models.

## Execution Digest
- summary: implement provider-catalog endpoint with focused tests and docs
- desired outcome:
  - OpenAI and Antigravity contexts are represented in a normalized contract
  - /v1/models remains unchanged and secondary in docs/design
  - implementation avoids overengineering

## Acceptance Criteria
- endpoint returns normalized provider metadata
- response is useful as primary source for linkto-provider
- /v1/models semantics unchanged
- focused verification commands pass

## Constraints
- do not overengineer discovery/runtime unification
- do not rewrite unrelated gateway behavior
- keep code diff tight and explain any assumptions in docs

## Repo Scope
- backend/internal/server/routes/gateway.go
- backend/internal/handler/gateway_handler.go
- backend/internal/service/gateway_service.go
- backend/internal/service/provider_catalog_service.go
- backend/internal/handler/dto/provider_catalog.go
- backend/internal/handler/service tests as needed
- docs/artifacts/2026-04-03-sub2api-provider-catalog-endpoint.md
- tasks.md
- implement.md

## Dependencies
- depends on sub2api-oej

## Status
ready

## Resume Notes
Do exactly these three things and no more: create provider-catalog endpoint, make it the primary intended source for linkto-provider, keep /v1/models as fallback/live availability check only. Be explicit in code/docs so no one mistakes /v1/models for the main catalog later.
