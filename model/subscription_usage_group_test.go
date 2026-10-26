package model

import (
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func seedUsageGroupSubscription(t *testing.T, id, userID, planID int, usageGroup string, allowWalletOverflow bool, endTime int64) {
	t.Helper()
	require.NoError(t, DB.Create(&UserSubscription{
		Id:                  id,
		UserId:              userID,
		PlanId:              planID,
		AmountTotal:         1000,
		StartTime:           endTime - 3600,
		EndTime:             endTime,
		Status:              "active",
		UsageGroup:          usageGroup,
		AllowWalletOverflow: allowWalletOverflow,
	}).Error)
}

func TestCreateUserSubscriptionSnapshotsUsageGroup(t *testing.T) {
	truncateTables(t)
	user := &User{Username: "usage-group-snapshot", Group: "default", Status: common.UserStatusEnabled}
	require.NoError(t, DB.Create(user).Error)
	plan := &SubscriptionPlan{
		Title:         "Group restricted",
		DurationUnit:  SubscriptionDurationMonth,
		DurationValue: 1,
		TotalAmount:   1000,
		UsageGroup:    " vip, default, vip ",
	}
	require.NoError(t, DB.Create(plan).Error)

	subscription, err := CreateUserSubscriptionFromPlanTx(DB, user.Id, plan, "test")
	require.NoError(t, err)
	assert.Equal(t, "vip,default", subscription.UsageGroup)

	var stored UserSubscription
	require.NoError(t, DB.First(&stored, subscription.Id).Error)
	assert.Equal(t, "vip,default", stored.UsageGroup)
}

func TestParseSubscriptionUsageGroupsNormalizesLegacyAndMultiValueInput(t *testing.T) {
	assert.Equal(t, []string{"vip"}, ParseSubscriptionUsageGroups(" vip "))
	assert.Equal(
		t,
		[]string{"vip", "default", "special"},
		ParseSubscriptionUsageGroups("vip, default, vip, special"),
	)
	assert.Equal(t, "vip,default,special", NormalizeSubscriptionUsageGroups(" vip,default,vip,special "))
}

func TestPreConsumeUserSubscriptionForGroupSelectsCompatibleSubscription(t *testing.T) {
	truncateTables(t)
	now := GetDBTimestamp()
	userID := 7101
	plans := []SubscriptionPlan{
		{Id: 7111, Title: "Restricted VIP", DurationUnit: SubscriptionDurationMonth, DurationValue: 1, TotalAmount: 1000},
		{Id: 7112, Title: "Restricted Default", DurationUnit: SubscriptionDurationMonth, DurationValue: 1, TotalAmount: 1000},
		{Id: 7113, Title: "All groups", DurationUnit: SubscriptionDurationMonth, DurationValue: 1, TotalAmount: 1000},
	}
	for i := range plans {
		require.NoError(t, DB.Create(&plans[i]).Error)
		InvalidateSubscriptionPlanCache(plans[i].Id)
	}
	seedUsageGroupSubscription(t, 7121, userID, plans[0].Id, "vip", true, now+100)
	seedUsageGroupSubscription(t, 7122, userID, plans[1].Id, "default", true, now+200)
	seedUsageGroupSubscription(t, 7123, userID, plans[2].Id, "", true, now+300)

	result, err := PreConsumeUserSubscriptionForGroup("usage-group-default", userID, "model", 0, 100, "default")
	require.NoError(t, err)
	assert.Equal(t, 7122, result.UserSubscriptionId)
	assert.EqualValues(t, 100, result.AmountUsedAfter)

	result, err = PreConsumeUserSubscriptionForGroup("usage-group-other", userID, "model", 0, 100, "other")
	require.NoError(t, err)
	assert.Equal(t, 7123, result.UserSubscriptionId)

	var vip UserSubscription
	require.NoError(t, DB.First(&vip, 7121).Error)
	assert.Zero(t, vip.AmountUsed)
}

func TestPreConsumeUserSubscriptionForGroupSelectsMultiGroupSubscription(t *testing.T) {
	truncateTables(t)
	now := GetDBTimestamp()
	plan := &SubscriptionPlan{Id: 7151, Title: "VIP and default", DurationUnit: SubscriptionDurationMonth, DurationValue: 1, TotalAmount: 1000}
	require.NoError(t, DB.Create(plan).Error)
	InvalidateSubscriptionPlanCache(plan.Id)
	seedUsageGroupSubscription(t, 7152, 7150, plan.Id, "vip,default", true, now+3600)

	result, err := PreConsumeUserSubscriptionForGroup("usage-group-multi-default", 7150, "model", 0, 100, "default")
	require.NoError(t, err)
	assert.Equal(t, 7152, result.UserSubscriptionId)

	hasVIP, err := HasActiveUserSubscriptionForGroup(7150, "vip")
	require.NoError(t, err)
	assert.True(t, hasVIP)
	hasOther, err := HasActiveUserSubscriptionForGroup(7150, "other")
	require.NoError(t, err)
	assert.False(t, hasOther)
}

func TestSubscriptionUsageGroupFilterEscapesLikeWildcards(t *testing.T) {
	truncateTables(t)
	now := GetDBTimestamp()
	seedUsageGroupSubscription(t, 7181, 7180, 7171, "vip_1,percent%group", true, now+3600)

	hasUnderscore, err := HasActiveUserSubscriptionForGroup(7180, "vip_1")
	require.NoError(t, err)
	assert.True(t, hasUnderscore)
	hasPercent, err := HasActiveUserSubscriptionForGroup(7180, "percent%group")
	require.NoError(t, err)
	assert.True(t, hasPercent)
	hasWildcardMatch, err := HasActiveUserSubscriptionForGroup(7180, "vipX1")
	require.NoError(t, err)
	assert.False(t, hasWildcardMatch)
}

func TestPreConsumeUserSubscriptionForGroupRejectsIncompatibleSubscription(t *testing.T) {
	truncateTables(t)
	now := GetDBTimestamp()
	plan := &SubscriptionPlan{Id: 7211, Title: "VIP only", DurationUnit: SubscriptionDurationMonth, DurationValue: 1, TotalAmount: 1000}
	require.NoError(t, DB.Create(plan).Error)
	InvalidateSubscriptionPlanCache(plan.Id)
	seedUsageGroupSubscription(t, 7221, 7201, plan.Id, "vip", true, now+3600)

	_, err := PreConsumeUserSubscriptionForGroup("usage-group-mismatch", 7201, "model", 0, 100, "default")
	require.ErrorContains(t, err, "no active subscription")

	var subscription UserSubscription
	require.NoError(t, DB.First(&subscription, 7221).Error)
	assert.Zero(t, subscription.AmountUsed)
}

func TestSubscriptionUsageGroupFiltersWalletOverflow(t *testing.T) {
	truncateTables(t)
	now := GetDBTimestamp()
	userID := 7301
	seedUsageGroupSubscription(t, 7321, userID, 7311, "vip", false, now+3600)

	hasDefault, err := HasActiveUserSubscriptionForGroup(userID, "default")
	require.NoError(t, err)
	assert.False(t, hasDefault)
	allowDefault, err := UserActiveSubscriptionsAllowWalletOverflowForGroup(userID, "default")
	require.NoError(t, err)
	assert.True(t, allowDefault)

	hasVIP, err := HasActiveUserSubscriptionForGroup(userID, " vip ")
	require.NoError(t, err)
	assert.True(t, hasVIP)
	allowVIP, err := UserActiveSubscriptionsAllowWalletOverflowForGroup(userID, "vip")
	require.NoError(t, err)
	assert.False(t, allowVIP)
}

func TestPreConsumeUserSubscriptionForGroupKeepsIdempotentSelection(t *testing.T) {
	truncateTables(t)
	now := GetDBTimestamp()
	plan := &SubscriptionPlan{Id: 7411, Title: "VIP only", DurationUnit: SubscriptionDurationMonth, DurationValue: 1, TotalAmount: 1000}
	require.NoError(t, DB.Create(plan).Error)
	InvalidateSubscriptionPlanCache(plan.Id)
	seedUsageGroupSubscription(t, 7421, 7401, plan.Id, "vip", true, now+3600)
	requestID := fmt.Sprintf("usage-group-idempotent-%d", now)

	first, err := PreConsumeUserSubscriptionForGroup(requestID, 7401, "model", 0, 100, "vip")
	require.NoError(t, err)
	second, err := PreConsumeUserSubscriptionForGroup(requestID, 7401, "model", 0, 100, "default")
	require.NoError(t, err)
	assert.Equal(t, first.UserSubscriptionId, second.UserSubscriptionId)
	assert.Equal(t, first.PreConsumed, second.PreConsumed)

	var subscription UserSubscription
	require.NoError(t, DB.First(&subscription, 7421).Error)
	assert.EqualValues(t, 100, subscription.AmountUsed)
}
