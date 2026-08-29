# State

## Current focus
- Vietnamese locale coverage and translation quality are complete on a dedicated branch; local verification is complete, while push/PR, independent review, and CI remain pending.

## Repo
- Branch: `feat/vietnamese-locale-coverage`
- Remote: `https://github.com/cong91/sub2api.git`

## Active changes
- Vietnamese messages are split between `frontend/src/i18n/locales/vi.ts` and `vi-additions-a.ts` through `vi-additions-d.ts`.
- Locale regression tests cover zh-to-vi key coverage, message compilation, and en-to-vi placeholder parity.
- Technical literals such as endpoints, field names, model identifiers, environment variables, and code placeholders are preserved during translation cleanup.

## Verification
- Focused i18n contract suite passed: `npm run test:run -- src/i18n/__tests__/localeKeyParity.spec.ts src/i18n/__tests__/localesMessageCompile.spec.ts src/i18n/__tests__/localeRegistry.spec.ts src/i18n/__tests__/localesNoKeyCollision.spec.ts` (`4` files / `13` tests).
- Frontend lint: `npm run lint:check` passed.
- Frontend typecheck: `npm run typecheck` passed.
- Production build: `npm run build` passed; Vite emitted only the existing large-chunk warning.
- Targeted Model Plaza suite: `npm run test:run -- src/components/modelPlaza/__tests__/PlazaModelPricingTable.spec.ts` passed (`1` file / `25` tests).
- Full Vitest suite: `npm run test:run` passed (`251` files / `1762` tests).
- Locale audit passed: en/zh each have `8,079` leaf keys; vi has `8,686`; en-to-zh missing/extra `0/0`; zh-to-vi missing `0`; placeholder parity `0`; blank/non-string `0` for all locales; static consumer missing `0` for all locales.
- `git diff --check` passed.

## Model Plaza triage
- Both failures were test regressions introduced by commit `2838efa81` in `PlazaModelPricingTable.spec.ts`; `PlazaModelPricingTable.vue` was unchanged in that commit and still implements the documented table contract.
- The mobile-card assertion targeted a nonexistent `[data-testid="mobile-pricing-card"]`; the component renders a responsive overflow table and already exposes concrete platform badges for composite groups.
- The publication-order assertion contradicted `sortedModels`, which intentionally places token billing before image/per-request billing and then sorts by official output price/name. Backend `ListGroups` also documents and enforces deterministic group/model sorting rather than arbitrary admin publication order.
- The test file was restored to the pre-`2838efa81` contract; no component behavior was changed.

## Release blockers / risks
- No push, PR, merge, or production deployment has occurred for this locale branch.
- Local frontend verification is green, but CI and independent review remain pending; do not treat local green as release approval.
- Independent delegated review was unavailable because the configured reviewer model returned HTTP 404; self-review and local verification do not replace formal review/CI.

## Beads
- br-missing: install beads_rust; `.beads` not initialized.

## Bootstrap metadata
- mode: smart
- cwd: /home/ubuntu/workspaces/projects/sub2api
