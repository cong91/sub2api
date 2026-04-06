# sub2api invite-login usable API account v2

Date: 2026-04-06
Project: `sub2api`

## Objective

Finish the invite-login product flow so a fresh invitation login produces a **usable runtime account** for downstream VCW / provider usage.

This plan supersedes the earlier narrower plan by explicitly adding:

1. per-code invitation balance override (`bootstrap_balance`)
2. global admin setting (`default_invitation_balance`)
3. **one single multi-group bootstrap API key** instead of multiple provider-specific keys
4. trace/fix of `/api/v1/keys` auth contract after invite-login
5. trace/fix of runtime settings path so `default_invitation_balance` actually takes effect

## Current verified facts

### Verified from runtime
- Public/browser-context `invite-login` can succeed for a fresh invitation code.
- In a successful local/full-chain case, `/api/v1/keys` returned multiple keys such as `default-openai` and `default-anthropic`.
- Those keys failed `/v1/provider-catalog` with `403 INSUFFICIENT_BALANCE`.
- In browser-context testing, `default_invitation_balance = 10` was set successfully, but the newly created bootstrap user still showed `balance = 0`.
- In at least one public/browser-context path, `/api/v1/keys` returned `401` even after invite-login success.

### Verified from source
- `default_invitation_balance` support is already partially present in backend settings constants/getters/DTOs.
- `bootstrap_balance` support is already partially present in ent/schema/service foundations.
- `GenerateRedeemCodesRequest` and related UI were in the middle of being wired and later source-level verification showed handler/UI support entering source.
- `AuthService.InviteLogin(...)` balance precedence and bootstrap API key provisioning model are the critical remaining product logic areas.

## Product contract (final target)

After invite-login succeeds:

1. bootstrap user is created with correct initial balance using precedence:
   - `redeemCode.bootstrap_balance`
   - `default_invitation_balance`
   - `default_balance`
2. backend provisions **exactly one usable multi-group API key** for bootstrap use
3. launcher can call `/api/v1/keys`, receive that usable key deterministically, and continue to `/v1/provider-catalog`
4. runtime provider flow no longer depends on guessing between multiple provider-specific default keys

## Scope

### Backend
Likely files:
- `backend/internal/service/auth_service.go`
- `backend/internal/service/admin_service.go`
- `backend/internal/service/setting_service.go`
- `backend/internal/service/domain_constants.go`
- `backend/internal/handler/admin/redeem_handler.go`
- `backend/internal/handler/admin/setting_handler.go`
- `backend/internal/repository/*` only if required
- generated ent files / schema if needed

### Frontend admin UI
Likely files:
- `frontend/src/views/admin/RedeemView.vue`
- `frontend/src/views/admin/SettingsView.vue`
- `frontend/src/api/admin/redeem.ts`
- `frontend/src/api/admin/settings.ts`
- `frontend/src/types/index.ts`

## Out of scope

- VCW launcher changes
- provider plugin changes
- Cloudflare/public edge routing issues
- broad billing redesign beyond invitation bootstrap flow

## Required implementation areas

---

## A. Trace and fix `/api/v1/keys` auth contract after invite-login

### Problem
At least one observed full-chain path showed:
- `invite-login` success
- but `/api/v1/keys` returned `401`

### Required investigation
OpenCode must trace why a token returned from `invite-login` success may fail on `/api/v1/keys`:
- auth middleware mismatch?
- token type mismatch?
- public/local runtime divergence?
- missing user status/claims/session persistence?

### Deliverable
- identify exact cause
- patch the narrowest correct layer so `invite-login` success is followed by a valid authenticated `/api/v1/keys` call

---

## B. Trace and fix runtime settings path for invitation balances

### Problem
Observed browser-context sequence:
- admin setting `default_invitation_balance = 10` saved successfully
- fresh invitation code generated successfully
- invite-login success still created bootstrap user with `balance = 0`

### Required investigation
OpenCode must trace why runtime still resolves zero balance:
- setting write goes to one repo/db path while invite-login reads another?
- stale cache?
- public/local instance divergence?
- invite-login path still using `GetDefaultBalance()` instead of `GetDefaultInvitationBalance()` in the effective runtime branch?

### Deliverable
- identify exact reason
- patch narrowest correct layer so runtime behavior matches configured `default_invitation_balance`

---

## C. Invitation balance support (product fields)

### 1. Per-code override
Admin invitation generation must support optional:
- `bootstrap_balance`

### 2. Global default
Admin settings must expose and persist:
- `default_invitation_balance`

### 3. Runtime precedence
`AuthService.InviteLogin(...)` must resolve bootstrap balance in this exact order:
1. `redeemCode.BootstrapBalance`
2. `settingService.GetDefaultInvitationBalance(ctx)`
3. `settingService.GetDefaultBalance(ctx)` or config fallback

---

## D. Replace multi-key bootstrap provisioning with one multi-group bootstrap API key

### Current product problem
Current bootstrap provisioning model creates multiple keys like:
- `default-openai`
- `default-anthropic`

This makes downstream runtime/launcher key selection non-deterministic and can still produce unusable provider behavior.

### Required behavior
After invite-login, backend should provision **one canonical bootstrap API key**:
- name suggestion: `default-v-claw` or equivalent single canonical name
- `granted_group_ids` = all chosen bootstrap groups
- `default_group_id` / `group_id` = chosen default group

### Required investigation
OpenCode must inspect:
- `ensureInviteBootstrapAPIAccess(...)`
- target resolution for cheapest representative groups
- current loop that creates one key per provider target

### Required refactor
Replace provider-specific key fan-out with:
- collect entitled/bootstrap groups first
- create **one** multi-group API key
- return/bootstrap reference should point to this single key

### Acceptance for this section
- `/api/v1/keys` after invite-login should expose a deterministic single bootstrap key for launcher/provider use

---

## E. Frontend admin UI

### Settings UI must expose
- `default_invitation_balance`

### Redeem/invitation UI must expose
- `bootstrap_balance`
for `type = invitation`

### UI contract
- no regression for non-invitation redeem code generation
- request payloads remain narrow and explicit

---

## Acceptance criteria (full packet)

1. admin settings expose and persist `default_invitation_balance`
2. invitation code generation supports optional `bootstrap_balance`
3. runtime invite-login balance resolution actually uses:
   - code override -> invitation default -> default balance
4. invite-login success can be followed by authenticated `/api/v1/keys` successfully
5. invite-login provisions **one usable multi-group bootstrap API key**, not multiple provider-specific default keys
6. frontend admin UI exposes both balance controls
7. existing non-invitation redeem flows do not regress
8. runtime proof should show the resulting bootstrap key is usable enough to proceed to provider-catalog in the intended path

## Suggested verification checklist

### Backend/runtime
- set `default_invitation_balance = 10`
- generate invitation code without override
- invite-login with fresh code
- verify created bootstrap user balance = 10
- call `/api/v1/keys` with returned auth token and confirm success
- verify key list shape
- confirm exactly one canonical bootstrap key (or one launcher-intended usable key) with expected granted groups
- call `/v1/provider-catalog` using that key and confirm it no longer fails purely due to zero bootstrap balance in the common path

### Per-code override
- generate invitation code with `bootstrap_balance = 25`
- invite-login with that code
- verify created bootstrap user balance = 25

### Frontend/admin
- settings page renders `default_invitation_balance`
- redeem generation UI renders `bootstrap_balance` for invitation type
- request payloads include fields correctly

## Notes for OpenCode

- Keep this packet narrow but complete.
- Do not redesign billing in general.
- The critical business change is determinism and usability of the bootstrap account after invite-login.
- If runtime/public/local divergence is discovered while tracing A/B, document it clearly, but only patch the minimal necessary application-side logic within this packet.
