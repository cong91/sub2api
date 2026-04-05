# sub2api invite-login usable API account

Date: 2026-04-05
Project: `sub2api`

## Objective

Fix the remaining product gap after invite-login so bootstrap users receive a **usable API account** instead of a zero-balance key that immediately fails `/v1/provider-catalog` with `INSUFFICIENT_BALANCE`.

The approved direction is:

1. invitation/redeem code generation can carry an optional per-code balance override (`bootstrap_balance`)
2. admin settings expose a default fallback balance for invitation bootstrap (`default_invitation_balance`)
3. invite-login resolves new user initial balance in this order:
   - code-specific `bootstrap_balance`
   - settings `default_invitation_balance`
   - fallback existing `default_balance`

This packet must include both backend and admin/frontend surfaces.

## Current verified behavior

### Invite-login creates bootstrap users with default balance
`AuthService.InviteLogin(...)` currently initializes bootstrap user balance from:
- `settingService.GetDefaultBalance(ctx)`
- or fallback `cfg.Default.UserBalance`

### Consequence
When `default_balance = 0`, the bootstrap user's API keys can be created but still fail runtime provider usage with:
- `403 INSUFFICIENT_BALANCE`

This has already been verified through the flow:
- invite-login success
- `/api/v1/keys` returns keys
- every returned key fails `/v1/provider-catalog` with `INSUFFICIENT_BALANCE`

## Product contract

### A. Per-code override
When admin generates invitation codes, the request may include:
- `bootstrap_balance`

If provided, this value becomes the new user's initial balance when the code is redeemed via invite-login.

### B. Default invitation bootstrap balance
Admin settings should expose:
- `default_invitation_balance`

This value is used for invitation bootstrap when the code does not define an explicit balance override.

### C. Balance resolution precedence
When invite-login consumes a valid invitation code, resolve initial balance as:
1. `redeem_code.bootstrap_balance` if present
2. `settings.default_invitation_balance` if configured
3. `settings/default_balance` fallback

## Scope

### Backend
Likely files:
- `backend/internal/service/auth_service.go`
- `backend/internal/service/redeem_service.go`
- `backend/internal/service/setting_service.go`
- `backend/internal/service/domain_constants.go`
- `backend/internal/handler/admin/redeem_handler.go`
- `backend/internal/handler/admin/setting_handler.go`
- repository / Ent schema / migration files if a new persisted field is required

### Frontend / admin UI
Likely files:
- admin redeem code generation UI
- admin settings UI/types/API client

Potential areas:
- `frontend/src/views/admin/...`
- `frontend/src/api/admin/...`
- `frontend/src/types/...`

OpenCode should locate the exact current admin redeem/settings surfaces before patching.

## Out of scope

- launcher/VCW changes
- provider plugin changes
- Cloudflare/public edge routing issues
- redesign of non-invitation redeem code types

## Required implementation direction

### 1) Backend persisted support for invitation bootstrap balance
Add support for invitation code balance override.

Preferred implementation:
- add a dedicated persisted field like `bootstrap_balance` to redeem codes

If the current schema strongly prefers metadata/JSON for extensions, OpenCode may use that pattern **only if** it is consistent with the current codebase and remains explicit and testable.

### 2) Backend admin generate path
Update admin redeem-code generation request handling so invitation generation can accept:
- `bootstrap_balance`

Validation rules:
- must be >= 0
- only meaningful for `type = invitation`
- should not break existing generation for other types

### 3) Backend settings support
Add setting:
- `default_invitation_balance`

Required changes:
- setting key constant
- getter/service support
- admin settings request/response DTOs
- update path persistence
- default initialization behavior

### 4) Invite-login balance resolution
In `AuthService.InviteLogin(...)`, when constructing the bootstrap user:
- resolve balance in the approved precedence order
- set `newUser.Balance` accordingly

### 5) Frontend admin surfaces
#### A. Redeem code generation UI
Admin must be able to input optional `bootstrap_balance` when generating invitation codes.

#### B. Admin settings UI
Admin must be able to view/edit `default_invitation_balance` in settings.

### 6) Verification
Must prove:
- invitation code with override creates bootstrap user with override balance
- invitation code without override but with settings default uses `default_invitation_balance`
- fallback to `default_balance` still works if invitation default is absent
- resulting API key(s) are no longer trivially blocked by zero balance in the common invite-login path

## Acceptance criteria

1. admin redeem code generation supports optional `bootstrap_balance` for invitation codes
2. admin settings expose and persist `default_invitation_balance`
3. invite-login resolves bootstrap user balance using the precedence:
   - code override -> invitation default -> default_balance
4. backend tests cover balance resolution behavior
5. frontend admin UI can set both per-code invitation balance and global default invitation balance
6. no regression to existing non-invitation redeem flows

## Suggested verification checklist

### Backend
- create invitation code with `bootstrap_balance = X`
- invite-login with that code
- assert bootstrap user balance = X

- set `default_invitation_balance = Y`
- create invitation code without override
- invite-login with that code
- assert bootstrap user balance = Y

- unset invitation default and override
- invite-login with invitation code
- assert fallback balance = existing `default_balance`

### Frontend/admin
- settings page shows `default_invitation_balance`
- redeem generation UI accepts `bootstrap_balance` for invitation type
- request payloads are correct

## Notes for OpenCode

- Keep the patch narrow and product-driven.
- Prefer explicit persisted fields over implicit hacks.
- Do not overgeneralize this into a broad billing redesign.
- If a migration is required, keep it minimal and safe.
