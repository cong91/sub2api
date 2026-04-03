# Tasks

- [x] Backend foundation: multi-group API key schema + repository + service + middleware compatibility
- [x] Invite-login: bootstrap one key with multiple granted groups
- [x] Provider-catalog: aggregate provider rows from all granted groups
- [x] Frontend user UX: replace single-group key flow with multi-access flow
- [x] Frontend admin UX: manage multiple granted groups and default provider/group
- [x] Runtime verification: prove one key can drive multiple provider rows end-to-end

## Closeout note

Frontend slice 3 closeout is based on:
- frontend multi-access UX patch landed in source
- frontend build PASS
- remaining `pnpm vitest run` failures were classified/documented as baseline or unrelated in:
  - `docs/artifacts/2026-04-03-sub2api-multi-group-api-key-and-subprovider-ux.md`
- this closeout does **not** claim that the entire frontend suite is fully green
