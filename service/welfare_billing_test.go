package service

import (
	"context"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWelfareBalanceTakesPriorityOverActiveSubscription(t *testing.T) {
	truncate(t)
	const userID = 9201
	user := &model.User{
		Id:               userID,
		Username:         "welfare-priority",
		Quota:            500,
		WelfareQuota:     100,
		WelfareExpiresAt: time.Now().Add(time.Hour).Unix(),
		Status:           common.UserStatusEnabled,
	}
	require.NoError(t, model.DB.Create(user).Error)
	seedSubscription(t, 9202, userID, 1_000, 0)

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(nil)
	relayInfo := &relaycommon.RelayInfo{
		UserId:       userID,
		IsPlayground: true,
		RequestId:    "welfare-priority-request",
		UserSetting: dto.UserSetting{
			BillingPreference: "subscription_first",
		},
	}

	session, apiErr := NewBillingSession(c, relayInfo, 40)
	require.Nil(t, apiErr)
	require.NotNil(t, session)
	assert.Equal(t, BillingSourceWelfare, relayInfo.BillingSource)

	welfare, err := model.GetUserWelfareQuota(userID)
	require.NoError(t, err)
	assert.Equal(t, 60, welfare.Quota)
	var subscription model.UserSubscription
	require.NoError(t, model.DB.First(&subscription, 9202).Error)
	assert.Zero(t, subscription.AmountUsed)

	require.NoError(t, session.Settle(25))
	welfare, err = model.GetUserWelfareQuota(userID)
	require.NoError(t, err)
	assert.Equal(t, 75, welfare.Quota)
}

func TestWelfareBalanceFallsBackWhenItCannotCoverRequest(t *testing.T) {
	truncate(t)
	const userID = 9211
	user := &model.User{
		Id:               userID,
		Username:         "welfare-fallback",
		Quota:            100,
		WelfareQuota:     20,
		WelfareExpiresAt: time.Now().Add(time.Hour).Unix(),
		Status:           common.UserStatusEnabled,
	}
	require.NoError(t, model.DB.Create(user).Error)

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(nil)
	relayInfo := &relaycommon.RelayInfo{
		UserId:       userID,
		IsPlayground: true,
		UserSetting: dto.UserSetting{
			BillingPreference: "wallet_only",
		},
	}

	_, apiErr := NewBillingSession(c, relayInfo, 40)
	require.Nil(t, apiErr)
	assert.Equal(t, BillingSourceWallet, relayInfo.BillingSource)
	welfare, err := model.GetUserWelfareQuota(userID)
	require.NoError(t, err)
	assert.Equal(t, 20, welfare.Quota)
	quota, err := model.GetUserQuota(userID, true)
	require.NoError(t, err)
	assert.Equal(t, 60, quota)
}

func TestTaskAdjustmentUsesWelfareFundingSource(t *testing.T) {
	truncate(t)
	const userID = 9221
	user := &model.User{
		Id:               userID,
		Username:         "welfare-task",
		WelfareQuota:     100,
		WelfareExpiresAt: time.Now().Add(time.Hour).Unix(),
	}
	require.NoError(t, model.DB.Create(user).Error)
	state, err := model.GetUserWelfareQuota(userID)
	require.NoError(t, err)
	task := &model.Task{
		UserId: userID,
		PrivateData: model.TaskPrivateData{
			BillingSource:  BillingSourceWelfare,
			WelfareVersion: state.Version,
		},
	}

	require.NoError(t, taskAdjustFunding(task, 30))
	require.NoError(t, taskAdjustFunding(task, -10))
	state, err = model.GetUserWelfareQuota(userID)
	require.NoError(t, err)
	assert.Equal(t, 80, state.Quota)
}

// A welfare task without a recorded grant version must not be adjusted at all:
// the unversioned helpers would resolve the currently active grant instead of
// the one that funded the request, so the adjustment is refused outright.
func TestTaskAdjustmentRefusesWelfareTaskWithoutVersion(t *testing.T) {
	truncate(t)
	const userID = 9222
	user := &model.User{
		Id:               userID,
		Username:         "welfare-task-noversion",
		WelfareQuota:     100,
		WelfareExpiresAt: time.Now().Add(time.Hour).Unix(),
	}
	require.NoError(t, model.DB.Create(user).Error)
	task := &model.Task{UserId: userID, PrivateData: model.TaskPrivateData{BillingSource: BillingSourceWelfare}}

	require.ErrorIs(t, taskAdjustFunding(task, 30), model.ErrWelfareQuotaNotApplied)
	require.ErrorIs(t, taskAdjustFunding(task, -10), model.ErrWelfareQuotaNotApplied)

	refunded, err := taskRefundFunding(task, 30)
	require.ErrorIs(t, err, model.ErrWelfareQuotaNotApplied)
	assert.False(t, refunded)

	state, err := model.GetUserWelfareQuota(userID)
	require.NoError(t, err)
	assert.Equal(t, 100, state.Quota, "quota must be untouched without a version fence")
}

func TestWelfareSettlementFailureKeepsExactCommittedQuota(t *testing.T) {
	truncate(t)
	const userID = 9231
	user := &model.User{
		Id:               userID,
		Username:         "welfare-settle-shortfall",
		WelfareQuota:     100,
		WelfareExpiresAt: time.Now().Add(time.Hour).Unix(),
		Status:           common.UserStatusEnabled,
	}
	require.NoError(t, model.DB.Create(user).Error)

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(nil)
	relayInfo := &relaycommon.RelayInfo{UserId: userID, IsPlayground: true}
	session, apiErr := NewBillingSession(c, relayInfo, 80)
	require.Nil(t, apiErr)
	require.NotNil(t, session)
	relayInfo.Billing = session

	require.ErrorIs(t, session.Settle(120), model.ErrWelfareQuotaNotApplied)
	fundingQuota, tokenQuota := session.GetCommittedQuotas()
	assert.Equal(t, 80, fundingQuota)
	assert.Zero(t, tokenQuota)
	assert.Equal(t, 80, committedFundingQuota(relayInfo, 120))

	state, err := model.GetUserWelfareQuota(userID)
	require.NoError(t, err)
	assert.Equal(t, 20, state.Quota)
}

func TestBillingSessionRestorePreConsumedReversesSettlement(t *testing.T) {
	truncate(t)
	const userID, tokenID = 9232, 9232
	seedUser(t, userID, 1000)
	seedToken(t, tokenID, userID, "sk-restore-pre-consumed", 500)

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(nil)
	relayInfo := &relaycommon.RelayInfo{
		UserId:    userID,
		TokenId:   tokenID,
		TokenKey:  "sk-restore-pre-consumed",
		UserQuota: 1000,
		UserSetting: dto.UserSetting{
			BillingPreference: "wallet_only",
		},
	}
	session, apiErr := NewBillingSession(c, relayInfo, 100)
	require.Nil(t, apiErr)
	require.NotNil(t, session)
	require.NoError(t, session.Settle(150))
	assert.Equal(t, 850, getUserQuota(t, userID))
	assert.Equal(t, 350, getTokenRemainQuota(t, tokenID))

	require.NoError(t, session.RestorePreConsumed())
	fundingQuota, tokenQuota := session.GetCommittedQuotas()
	assert.Equal(t, 100, fundingQuota)
	assert.Equal(t, 100, tokenQuota)
	assert.Equal(t, 900, getUserQuota(t, userID))
	assert.Equal(t, 400, getTokenRemainQuota(t, tokenID))
}

func TestWelfareSessionCannotRefundAcrossAdministratorOverride(t *testing.T) {
	truncate(t)
	const userID = 9241
	user := &model.User{
		Id:               userID,
		Username:         "welfare-admin-override",
		WelfareQuota:     100,
		WelfareExpiresAt: time.Now().Add(time.Hour).Unix(),
		Status:           common.UserStatusEnabled,
	}
	require.NoError(t, model.DB.Create(user).Error)

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(nil)
	relayInfo := &relaycommon.RelayInfo{UserId: userID, IsPlayground: true}
	session, apiErr := NewBillingSession(c, relayInfo, 60)
	require.Nil(t, apiErr)

	require.NoError(t, model.SetUserWelfareQuota(userID, 0, 0))
	funding := session.funding.(*WelfareFunding)
	require.ErrorIs(t, funding.Refund(), model.ErrWelfareQuotaNotApplied)

	state, err := model.GetUserWelfareQuota(userID)
	require.NoError(t, err)
	assert.Zero(t, state.Quota)
}

func TestLegacyMidjourneyUsesAndRefundsWelfareBalance(t *testing.T) {
	truncate(t)
	const userID, channelID = 9251, 9251
	seedUser(t, userID, 500)
	seedChannel(t, channelID)
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", userID).Updates(map[string]any{
		"welfare_quota":      100,
		"welfare_expires_at": time.Now().Add(time.Hour).Unix(),
	}).Error)

	relayInfo := &relaycommon.RelayInfo{
		UserId:       userID,
		IsPlayground: true,
		ChannelMeta:  &relaycommon.ChannelMeta{ChannelId: channelID},
	}
	task := &model.Midjourney{UserId: userID, ChannelId: channelID, MjId: "mj-welfare", Action: "IMAGINE"}
	prepared, err := PrepareMidjourneyTaskBilling(relayInfo, task, 60, true)
	require.NoError(t, err)
	require.True(t, prepared)
	assert.Equal(t, BillingSourceWelfare, task.BillingSource)
	require.Positive(t, task.WelfareVersion)
	require.NoError(t, task.Insert())

	billed, err := SettleMidjourneyTaskBilling(relayInfo, task, prepared)
	require.NoError(t, err)
	assert.True(t, billed)
	state, err := model.GetUserWelfareQuota(userID)
	require.NoError(t, err)
	assert.Equal(t, 40, state.Quota)
	assert.Equal(t, 500, getUserQuota(t, userID))

	seedChargedAccounting(t, userID, channelID, 0, 60, 1)
	assert.True(t, RefundMidjourneyQuota(context.Background(), task, "task failed"))
	state, err = model.GetUserWelfareQuota(userID)
	require.NoError(t, err)
	assert.Equal(t, 100, state.Quota)
	assert.Equal(t, 500, getUserQuota(t, userID))
}

func TestStaleWelfareTaskRefundStillRestoresTokenAndClearsMarkers(t *testing.T) {
	truncate(t)
	const userID, tokenID, channelID = 9261, 9261, 9261
	const chargedQuota = 60
	seedUser(t, userID, 500)
	seedToken(t, tokenID, userID, "sk-stale-welfare", 200)
	seedChannel(t, channelID)
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", userID).Updates(map[string]any{
		"welfare_quota":      100,
		"welfare_expires_at": time.Now().Add(time.Hour).Unix(),
	}).Error)
	state, err := model.GetUserWelfareQuota(userID)
	require.NoError(t, err)
	oldVersion := state.Version

	reserved, err := model.DecreaseUserWelfareQuotaForVersion(userID, chargedQuota, oldVersion)
	require.NoError(t, err)
	require.True(t, reserved)
	require.NoError(t, model.SetUserWelfareQuota(userID, 0, 0))
	require.NoError(t, model.DB.Model(&model.Token{}).Where("id = ?", tokenID).Updates(map[string]any{
		"remain_quota": 200 - chargedQuota,
		"used_quota":   chargedQuota,
	}).Error)
	seedChargedAccounting(t, userID, channelID, tokenID, chargedQuota, 1)

	task := makeTask(userID, channelID, chargedQuota, tokenID, BillingSourceWelfare, 0)
	task.PrivateData.WelfareVersion = oldVersion
	task.SetTokenQuota(chargedQuota)
	require.NoError(t, task.Insert())

	assert.True(t, RefundTaskQuota(context.Background(), task, "stale welfare grant"))
	state, err = model.GetUserWelfareQuota(userID)
	require.NoError(t, err)
	assert.Zero(t, state.Quota)
	assert.Equal(t, 200, getTokenRemainQuota(t, tokenID))
	assert.Zero(t, getTokenUsedQuota(t, tokenID))

	var persisted model.Task
	require.NoError(t, model.DB.First(&persisted, task.ID).Error)
	assert.Zero(t, persisted.Quota)
	assert.Zero(t, persisted.GetTokenQuota())
	usedQuota, requestCount := getUserUsageAccounting(t, userID)
	assert.Zero(t, usedQuota)
	assert.Equal(t, 1, requestCount)
	assert.Zero(t, getChannelUsedQuota(t, channelID))
}

func TestBillingSessionStopsFallbackWhenTokenRollbackFails(t *testing.T) {
	truncate(t)
	const userID, tokenID, subscriptionID = 9271, 9271, 9272
	seedUser(t, userID, 1000)
	seedToken(t, tokenID, userID, "sk-token-rollback-failure", 1000)
	seedSubscription(t, subscriptionID, userID, 5000, 0)
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", userID).Updates(map[string]any{
		"welfare_quota":      200,
		"welfare_expires_at": time.Now().Add(time.Hour).Unix(),
	}).Error)

	require.NoError(t, model.DB.Exec(`
		CREATE TRIGGER fail_welfare_reservation_for_token_rollback
		BEFORE UPDATE OF welfare_quota ON users
		WHEN OLD.id = 9271
		BEGIN
			SELECT RAISE(ABORT, 'forced welfare reservation failure');
		END;
	`).Error)
	require.NoError(t, model.DB.Exec(`
		CREATE TRIGGER fail_token_refund_for_token_rollback
		BEFORE UPDATE OF remain_quota ON tokens
		WHEN OLD.id = 9271 AND NEW.remain_quota > OLD.remain_quota
		BEGIN
			SELECT RAISE(ABORT, 'forced token refund failure');
		END;
	`).Error)
	t.Cleanup(func() {
		model.DB.Exec("DROP TRIGGER IF EXISTS fail_welfare_reservation_for_token_rollback")
		model.DB.Exec("DROP TRIGGER IF EXISTS fail_token_refund_for_token_rollback")
	})

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(nil)
	relayInfo := &relaycommon.RelayInfo{
		UserId:    userID,
		TokenId:   tokenID,
		TokenKey:  "sk-token-rollback-failure",
		UserQuota: 1000,
		UserSetting: dto.UserSetting{
			BillingPreference: "wallet_first",
		},
	}
	apiErr := PreConsumeBilling(c, 100, relayInfo)
	require.Error(t, apiErr)
	require.NotNil(t, relayInfo.Billing)
	assert.Equal(t, types.ErrorCodeUpdateDataError, apiErr.GetErrorCode())
	assert.ErrorIs(t, apiErr.Err, errTokenQuotaRollback)
	assert.Equal(t, 1000, getUserQuota(t, userID))
	assert.Equal(t, 900, getTokenRemainQuota(t, tokenID))
	welfare, err := model.GetUserWelfareQuota(userID)
	require.NoError(t, err)
	assert.Equal(t, 200, welfare.Quota)
	var subscription model.UserSubscription
	require.NoError(t, model.DB.First(&subscription, subscriptionID).Error)
	assert.Zero(t, subscription.AmountUsed)
}
