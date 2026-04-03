# [sub2api-r1h.2] Invite-login bootstrap + provider-catalog aggregation for multi-group keys

## Identity
- bead_id: sub2api-r1h.2
- jira_key: none
- jira_type: task
- parent_jira_key: sub2api-r1h
- repo: sub2api

## Objective
Adjust invite-login to provision one multi-group usable key and update provider-catalog aggregation so provider rows are built from all granted groups on the key.

## Execution Digest
- summary: bootstrap + provider-catalog slice on top of multi-group foundation
- desired outcome:
  - invite-login creates one key with multiple granted groups
  - provider-catalog aggregates across granted groups
  - default group/provider semantics exist where needed

## Acceptance Criteria
- invite-login no longer provisions only a single-group key in the new path
- provider-catalog aggregates provider rows from granted groups
- /v1/models remains secondary/fallback in design and behavior
- scoped tests/verification pass

## Constraints
- depends on backend foundation from slice 1
- do not do frontend UX in this slice
- keep behavior deterministic when config/default groups are missing

## Repo Scope
- backend/internal/service/auth_service.go
- provisioning services/repositories
- provider-catalog builder/service
- config/settings if needed for default granted groups
- related tests

## Dependencies
- depends on sub2api-r1h.1

## Status
ready

## Resume Notes
Build on slice 1. Keep focus on bootstrap and provider-catalog semantics, not frontend UX. If default group/provider policy needs config, implement the smallest viable configuration surface.
