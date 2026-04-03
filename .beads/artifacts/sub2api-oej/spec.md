# [sub2api-oej] sub2api unified provider-catalog endpoint

## Identity
- bead_id: sub2api-oej
- jira_key: none
- jira_type: feature
- parent_jira_key: none
- repo: sub2api

## Objective
Implement a unified provider-catalog endpoint in sub2api so linkto-provider can use it as the primary metadata source instead of relying on contextual flat model lists.

## Execution Digest
- summary: backend-only contract work for provider catalog
- desired outcome:
  - endpoint exists
  - linkto-provider can consume it as primary source
  - /v1/models remains fallback/live availability only

## Acceptance Criteria
- endpoint exists and returns normalized provider metadata
- no rewrite of current gateway route semantics
- scope remains narrow and non-overengineered

## Constraints
- do not overengineer
- do not make /v1/models the primary source
- keep work inside repo sub2api

## Repo Scope
- backend/internal/server/routes/gateway.go
- backend/internal/handler/gateway_handler.go
- backend/internal/service/**
- docs/**
- tasks.md
- implement.md

## Dependencies
- none

## Status
ready

## Resume Notes
Three approved rules are mandatory: (1) design provider-catalog endpoint in sub2api, (2) linkto-provider will use it as primary source, (3) /v1/models is only fallback signal/live availability check. Avoid framework-y overengineering.
