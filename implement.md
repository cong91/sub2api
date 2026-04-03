# Sub2API Multi-Group API Key + SubProvider UX Implementation Packet

## Objective
Implement the approved product/backend/frontend shift from one-group-per-key to multi-group-per-key so a single Linkto/SubProvider API key can expose multiple provider families.

## Scope
- Backend multi-group API key model
- Invite-login bootstrap adjustment
- Provider-catalog aggregation from granted groups
- Frontend user/admin UX update

## Constraints
- Preserve backward compatibility for old one-group keys
- Do not overengineer a full routing rewrite beyond what is needed
- Keep `/v1/models` as fallback/live availability, not primary metadata source
- Prefer ordered execution slices

## Acceptance Criteria
- One API key can be granted multiple groups
- Invite-login provisions one multi-group usable key
- Provider catalog aggregates across granted groups
- Frontend no longer assumes one key = one group
- Runtime verification proves the new model end-to-end

## Validation
- Focused backend tests for service/handler/middleware
- Focused frontend tests/build
- Runtime verification with real keys and provider-catalog response
