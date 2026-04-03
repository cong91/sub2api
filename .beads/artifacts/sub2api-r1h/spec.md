# [sub2api-r1h] sub2api multi-group API key + SubProvider UX

## Identity
- bead_id: sub2api-r1h
- jira_key: none
- jira_type: feature
- parent_jira_key: none
- repo: sub2api

## Objective
Implement the approved product/system shift from one-group-per-key to multi-group-per-key so one Linkto/SubProvider API key can expose multiple provider families safely and clearly.

## Execution Digest
- summary: backend foundation first, then invite-login/provider-catalog aggregation, then frontend UX/UI
- desired outcome:
  - one API key can grant multiple groups
  - invite bootstrap creates one multi-group usable key
  - frontend no longer assumes one key = one group

## Acceptance Criteria
- work is split into three sequential slices with explicit dependencies
- each child slice is execution-ready without reading Jira
- runtime verification is possible after slices complete

## Constraints
- preserve backward compatibility for old keys
- avoid overengineering or full routing rewrite
- keep /v1/models as fallback/live availability, not primary metadata source

## Repo Scope
- backend/**
- frontend/**
- docs/**
- tasks.md
- implement.md

## Dependencies
- none

## Status
ready

## Resume Notes
Execute sequentially. Slice 1 must land before slice 2; slice 2 before slice 3. Do not parallelize because auth/domain changes can ripple into later slices.
