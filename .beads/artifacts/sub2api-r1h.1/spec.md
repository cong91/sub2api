# [sub2api-r1h.1] Backend foundation for multi-group API keys

## Identity
- bead_id: sub2api-r1h.1
- jira_key: none
- jira_type: task
- parent_jira_key: sub2api-r1h
- repo: sub2api

## Objective
Lay backend foundation for one API key granted to multiple groups, including schema, repository, service layer, middleware/context compatibility, and legacy fallback for old one-group keys.

## Execution Digest
- summary: foundational backend slice for multi-group keys
- desired outcome:
  - api_key_groups mapping exists
  - auth/service layer can resolve granted groups for a key
  - legacy api_keys.group_id still works during transition

## Acceptance Criteria
- schema + repository support multi-group mapping
- service/middleware can expose granted groups or compatible derived context
- legacy one-group keys still work
- scoped backend verification passes

## Constraints
- do not overengineer a full routing rewrite here
- do not break existing routes
- keep changes reversible and compatibility-aware

## Repo Scope
- backend migration/schema files
- backend repository layer for api keys/groups
- backend/internal/service/api_key_service.go
- backend/internal/server/middleware/api_key_auth.go
- related backend tests

## Dependencies
- none

## Status
ready

## Resume Notes
This slice is foundation only. Do not jump to invite-login UX or frontend UI before the backend multi-group model exists.
