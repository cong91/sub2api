# [sub2api-r1h.3] Frontend UX/UI update for multi-access API keys

## Identity
- bead_id: sub2api-r1h.3
- jira_key: none
- jira_type: task
- parent_jira_key: sub2api-r1h
- repo: sub2api

## Objective
Replace one-key-one-group UX with multi-access API key management UX for user/admin surfaces, including enabled provider/group access and default provider/group semantics.

## Execution Digest
- summary: frontend slice after backend contract stabilizes
- desired outcome:
  - user/admin UI no longer assumes one key = one group
  - multi-access key state is visible and editable
  - wording is product-facing, not raw backend-centric where possible

## Acceptance Criteria
- create/edit/list flows support multiple granted groups/accesses
- default provider/group is visible/editable where required
- user-facing wording avoids leaking raw backend internals in primary UX
- frontend tests/build pass for scoped changes

## Constraints
- depends on slices 1 and 2
- do not redesign unrelated admin areas
- keep UX practical and incremental

## Repo Scope
- frontend/src/types/index.ts
- frontend/src/api/keys.ts
- frontend/src/views/user/KeysView.vue
- frontend/src/components/keys/UseKeyModal.vue
- relevant admin key management components
- related tests

## Dependencies
- depends on sub2api-r1h.2

## Status
ready

## Resume Notes
Do not start until backend contract is usable. This slice should consume the new multi-group/default-provider semantics instead of inventing parallel frontend-only state.
