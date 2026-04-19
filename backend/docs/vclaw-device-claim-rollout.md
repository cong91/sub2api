# V-Claw device-claim rollout notes

## Backend endpoints

### `POST /api/v1/vclaw/claim`
Used by the Electron client before invite-login.

Request body:

```json
{
  "claim_code": "DCL-AAAA-BBBB-CCCC",
  "device": {
    "device_hash": "64-char sha256 hex",
    "fingerprint_version": 1,
    "install_id": "uuid",
    "platform": "win32",
    "arch": "x64",
    "app_version": "1.2.3"
  }
}
```

Behavior:
- first claim with valid `device_claim` code:
  - bootstrap/create user
  - create durable `user_devices` binding
  - mint/reuse `device_login_code`
  - return `mode=first_claim`
- same device later with no `claim_code`:
  - return existing `device_login_code`
  - return `mode=resume`
- revoked/blocked binding:
  - reject with `DEVICE_REVOKED`

Response body shape:

```json
{
  "code": 0,
  "data": {
    "mode": "first_claim",
    "user_id": 123,
    "device_login_code": "DLG-AAAA-BBBB-CCCC"
  }
}
```

### `POST /api/v1/auth/invite-login`
Still supports the old invite-only flow.

Request body for normal invite/redeem login:

```json
{
  "invitation_code": "INVITE-XXXX-YYYY"
}
```

Request body for device resume login:

```json
{
  "invitation_code": "DLG-AAAA-BBBB-CCCC",
  "device_hash": "64-char sha256 hex",
  "install_id": "uuid"
}
```

Behavior:
- `invitation` / existing invite bootstrap codes still work without device fields
- `device_login` requires matching `device_hash`

## Client rollout sequence

1. Deploy backend migration + code first.
2. Verify server can create/read `user_devices`.
3. Verify `POST /api/v1/vclaw/claim` works from staging/Postman.
4. Ship Electron client update after backend is live.
5. Confirm three scenarios in staging:
   - normal invite code login without device fields
   - first claim with `DCL-*` then auto-login through returned `DLG-*`
   - reopen/reinstall same machine and auto-resume via claim endpoint without claim code

## Smoke test checklist

- old invite code still logs in successfully
- `device_claim` code cannot be used by a second machine
- same machine can resume and receive the same `device_login_code`
- clearing local app data does not create a second user for the same machine
- revoked device returns a clear error and does not mint a new login code

## Rollback notes

If the Electron client must be rolled back:
- backend can remain deployed safely
- old clients still use `/api/v1/auth/invite-login`
- new `device_claim` / `device_login` rows can remain in DB; they do not break old invite flow

If backend must be rolled back:
- roll back the Electron client feature flag / release first if possible
- clients depending on `/api/v1/vclaw/claim` will lose first-claim/auto-resume behavior
- normal invite-login with legacy invite codes should still work if old backend keeps `/api/v1/auth/invite-login`
- do **not** drop `user_devices` table during emergency rollback unless you intentionally want to destroy device bindings

## Operational cautions

- keep server-side one-machine-one-claim enforcement authoritative
- do not expose admin redeem endpoints to the Electron app
- do not require `device_hash` for legacy invite-only login
- monitor error codes:
  - `CLAIM_CODE_REQUIRED`
  - `CLAIM_CODE_INVALID`
  - `DEVICE_REVOKED`
  - `DEVICE_MISMATCH`
