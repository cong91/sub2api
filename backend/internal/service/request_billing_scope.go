package service

import infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"

var ErrBillingGroupNotResolved = infraerrors.Forbidden("GROUP_NOT_ASSIGNED", "api key has no effective billing group")

// RequestBillingScope captures billing context resolved from the effective routed group.
type RequestBillingScope struct {
	BillingGroup   *Group
	BillingGroupID *int64
	BillingType    int8
	Subscription   *UserSubscription
}

// ResolveRequestBillingScope resolves billing scope from effective group runtime context.
// Runtime model is granted_groups only; legacy group_id fields are compatibility mirrors.
func ResolveRequestBillingScope(apiKey *APIKey, subscription *UserSubscription) (*RequestBillingScope, error) {
	if apiKey == nil {
		return nil, ErrBillingGroupNotResolved
	}
	effectiveGroup := apiKey.EffectiveGroup()
	if effectiveGroup == nil || effectiveGroup.ID <= 0 {
		return nil, ErrBillingGroupNotResolved
	}
	groupID := effectiveGroup.ID
	scope := &RequestBillingScope{
		BillingGroup:   effectiveGroup,
		BillingGroupID: &groupID,
		BillingType:    BillingTypeBalance,
	}
	if effectiveGroup.IsSubscriptionType() {
		scope.BillingType = BillingTypeSubscription
		scope.Subscription = subscription
	}
	return scope, nil
}
