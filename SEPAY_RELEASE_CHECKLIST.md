# SePay Release Checklist

## Scope
- SePay provider now uses MBBank-compatible QR generation flow.
- Webhook handling is wired to the existing payment webhook surface.
- Admin/frontend exposes SePay configuration.
- Live QR generation was verified with the real SePay token and bank account.

## What was verified
- Backend unit tests for SePay provider pass.
- Frontend typecheck/build pass.
- Live QR URL generation returns a valid PNG from SePay QR service.
- The provider no longer depends on the hardcoded BIDV order API.

## Release checklist

### 1) Final code sanity
- [ ] Review the final diff for unrelated files outside SePay scope.
- [ ] Confirm SePay provider config keys in admin:
  - [ ] `apiToken`
  - [ ] `bankAccountId`
  - [ ] `webhookApiKey`
  - [ ] `notifyUrl`
  - [ ] `apiBase` (default `https://my.sepay.vn/userapi`)
  - [ ] `qrBase` (default `https://qr.sepay.vn/img`)
- [ ] Confirm payment method labels and translations include SePay.

### 2) Runtime verification
- [ ] Create a real test order in the app.
- [ ] Generate a SePay QR for the order.
- [ ] Scan/pay the QR from the MBBank account.
- [ ] Verify transaction appears in SePay transaction list.
- [ ] Verify order status transitions to paid.
- [ ] Verify fulfillment completes without double-processing.

### 3) Webhook setup
- [ ] Confirm SePay webhook URL points to the server webhook endpoint.
- [ ] Confirm webhook API key matches between SePay and admin config.
- [ ] Confirm webhook requests are accepted with `Authorization: Apikey <key>` when enabled.
- [ ] Confirm webhook retries are enabled for non-2xx responses.

### 4) Deployment checks
- [ ] Build backend and frontend artifacts.
- [ ] Deploy backend with the updated payment provider code.
- [ ] Deploy frontend with SePay admin/provider UI updates.
- [ ] Re-test payment flow on the deployed environment.

### 5) Post-release monitoring
- [ ] Watch webhook 4xx/5xx rates.
- [ ] Watch for pending orders that never settle.
- [ ] Watch for duplicate fulfillment events.
- [ ] Keep a rollback plan ready if SePay QR or webhook parsing regresses.

## Notes
- The current account used for verification is MBBank, not BIDV.
- The earlier BIDV-specific order API path was removed from the SePay provider flow.
- Live fulfillment should be validated on the real server/webhook domain once it is available.
