package model

import (
	"errors"
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestRefundSubscriptionPreConsumeRollsBackUsageWhenRecordUpdateFails(t *testing.T) {
	truncateTables(t)
	now := GetDBTimestamp()
	plan := createConcurrencyPlan(t, "Atomic refund", 1)
	subscription := createConcurrencySubscription(t, 8001, plan, now+3600)
	_, err := PreConsumeUserSubscription("atomic-refund", 8001, "model", 0, 100)
	require.NoError(t, err)

	callbackName := "test:fail_subscription_refund_record_update"
	require.NoError(t, DB.Callback().Update().Before("gorm:update").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Table == "subscription_pre_consume_records" {
			tx.AddError(fmt.Errorf("forced refund record update failure"))
		}
	}))
	t.Cleanup(func() {
		require.NoError(t, DB.Callback().Update().Remove(callbackName))
	})

	require.Error(t, RefundSubscriptionPreConsume("atomic-refund"))
	require.NoError(t, DB.First(subscription, subscription.Id).Error)
	assert.EqualValues(t, 100, subscription.AmountUsed)

	var record SubscriptionPreConsumeRecord
	require.NoError(t, DB.Where("request_id = ?", "atomic-refund").First(&record).Error)
	assert.Equal(t, "consumed", record.Status)
}

func TestRefundSubscriptionPreConsumeIncludesSettlementDelta(t *testing.T) {
	truncateTables(t)
	now := GetDBTimestamp()
	plan := createConcurrencyPlan(t, "Full request refund", 0)
	subscription := createConcurrencySubscription(t, 8002, plan, now+3600)
	_, err := PreConsumeUserSubscription("full-request-refund", 8002, "model", 0, 100)
	require.NoError(t, err)
	require.NoError(t, PostConsumeUserSubscriptionForRequest("full-request-refund", subscription.Id, 50))

	require.NoError(t, RefundSubscriptionPreConsume("full-request-refund"))
	require.NoError(t, RefundSubscriptionPreConsume("full-request-refund"))
	require.NoError(t, DB.First(subscription, subscription.Id).Error)
	assert.Zero(t, subscription.AmountUsed)
	var record SubscriptionPreConsumeRecord
	require.NoError(t, DB.Where("request_id = ?", "full-request-refund").First(&record).Error)
	assert.EqualValues(t, 50, record.PostConsumed)
	assert.Equal(t, "refunded", record.Status)
}

func TestSubscriptionPlanTransactionReadBypassesGlobalCache(t *testing.T) {
	truncateTables(t)
	plan := createConcurrencyPlan(t, "Cached title", 0)
	cached, err := GetSubscriptionPlanById(plan.Id)
	require.NoError(t, err)
	assert.Equal(t, "Cached title", cached.Title)

	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&SubscriptionPlan{}).Where("id = ?", plan.Id).Update("title", "Transactional title").Error; err != nil {
			return err
		}
		inside, err := getSubscriptionPlanByIdTx(tx, plan.Id)
		if err != nil {
			return err
		}
		assert.Equal(t, "Transactional title", inside.Title)
		return nil
	}))
}

func TestSubscriptionCleanupKeepsRecordsRequiredByPendingRefunds(t *testing.T) {
	truncateTables(t)
	old := GetDBTimestamp() - 10*24*3600
	record := SubscriptionPreConsumeRecord{
		RequestId:          "pending-refund-cleanup",
		UserId:             1,
		UserSubscriptionId: 1,
		PreConsumed:        100,
		Status:             "consumed",
		CreatedAt:          old,
		UpdatedAt:          old,
	}
	require.NoError(t, DB.Create(&record).Error)
	require.NoError(t, DB.Exec("UPDATE subscription_pre_consume_records SET created_at = ?, updated_at = ? WHERE id = ?", old, old, record.Id).Error)
	require.NoError(t, DB.Create(&BillingRefundRecord{
		RequestId:     "pending-refund-cleanup",
		UserId:        1,
		FundingSource: "subscription",
		FundingQuota:  100,
		TokenRefunded: true,
		Status:        BillingRefundStatusPending,
	}).Error)

	deleted, err := CleanupSubscriptionPreConsumeRecords(7 * 24 * 3600)
	require.NoError(t, err)
	assert.Zero(t, deleted)
	var count int64
	require.NoError(t, DB.Model(&SubscriptionPreConsumeRecord{}).Where("request_id = ?", record.RequestId).Count(&count).Error)
	assert.EqualValues(t, 1, count)
}

func createConcurrencyPlan(t *testing.T, title string, concurrencyLimit int) *SubscriptionPlan {
	t.Helper()
	plan := &SubscriptionPlan{
		Title:            title,
		DurationUnit:     SubscriptionDurationMonth,
		DurationValue:    1,
		TotalAmount:      1000,
		ConcurrencyLimit: concurrencyLimit,
	}
	require.NoError(t, DB.Create(plan).Error)
	InvalidateSubscriptionPlanCache(plan.Id)
	return plan
}

func createConcurrencySubscription(t *testing.T, userID int, plan *SubscriptionPlan, endTime int64) *UserSubscription {
	t.Helper()
	subscription := &UserSubscription{
		UserId:              userID,
		PlanId:              plan.Id,
		AmountTotal:         plan.TotalAmount,
		StartTime:           endTime - 3600,
		EndTime:             endTime,
		Status:              "active",
		ConcurrencyLimit:    plan.ConcurrencyLimit,
		AllowWalletOverflow: true,
	}
	require.NoError(t, DB.Create(subscription).Error)
	return subscription
}

func TestCreateUserSubscriptionSnapshotsConcurrencyLimit(t *testing.T) {
	truncateTables(t)
	user := &User{Username: "concurrency-snapshot", Group: "default", Status: common.UserStatusEnabled}
	require.NoError(t, DB.Create(user).Error)
	plan := createConcurrencyPlan(t, "Snapshot concurrency", 5)

	subscription, err := CreateUserSubscriptionFromPlanTx(DB, user.Id, plan, "test")
	require.NoError(t, err)
	assert.Equal(t, 5, subscription.ConcurrencyLimit)

	var stored UserSubscription
	require.NoError(t, DB.First(&stored, subscription.Id).Error)
	assert.Equal(t, 5, stored.ConcurrencyLimit)
}

func TestSubscriptionConcurrencyUnlimitedDoesNotCreateLease(t *testing.T) {
	truncateTables(t)
	now := GetDBTimestamp()
	plan := createConcurrencyPlan(t, "Unlimited concurrency", 0)
	subscription := createConcurrencySubscription(t, 8101, plan, now+3600)

	first, err := PreConsumeUserSubscription("concurrency-unlimited-1", 8101, "model", 0, 100)
	require.NoError(t, err)
	second, err := PreConsumeUserSubscription("concurrency-unlimited-2", 8101, "model", 0, 100)
	require.NoError(t, err)
	assert.Zero(t, first.ConcurrencyLimit)
	assert.Zero(t, second.ConcurrencyLimit)

	var leaseCount int64
	require.NoError(t, DB.Model(&SubscriptionConcurrencyLease{}).Count(&leaseCount).Error)
	assert.Zero(t, leaseCount)
	require.NoError(t, DB.First(subscription, subscription.Id).Error)
	assert.EqualValues(t, 200, subscription.AmountUsed)
}

func TestSubscriptionConcurrencyLimitRejectsSecondRequest(t *testing.T) {
	truncateTables(t)
	now := GetDBTimestamp()
	plan := createConcurrencyPlan(t, "One concurrent request", 1)
	subscription := createConcurrencySubscription(t, 8201, plan, now+3600)

	result, err := PreConsumeUserSubscription("concurrency-limit-1", 8201, "model", 0, 100)
	require.NoError(t, err)
	assert.Equal(t, 1, result.ConcurrencyLimit)
	_, err = PreConsumeUserSubscription("concurrency-limit-2", 8201, "model", 0, 100)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrSubscriptionConcurrencyLimit))

	var leaseCount int64
	require.NoError(t, DB.Model(&SubscriptionConcurrencyLease{}).Count(&leaseCount).Error)
	assert.EqualValues(t, 1, leaseCount)
	require.NoError(t, DB.First(subscription, subscription.Id).Error)
	assert.EqualValues(t, 100, subscription.AmountUsed)
}

func TestSubscriptionConcurrencyReleaseAllowsNextRequest(t *testing.T) {
	truncateTables(t)
	now := GetDBTimestamp()
	plan := createConcurrencyPlan(t, "Released concurrency", 1)
	createConcurrencySubscription(t, 8301, plan, now+3600)

	_, err := PreConsumeUserSubscription("concurrency-release-1", 8301, "model", 0, 100)
	require.NoError(t, err)
	require.NoError(t, ReleaseSubscriptionConcurrencyLease("concurrency-release-1"))
	require.NoError(t, ReleaseSubscriptionConcurrencyLease("concurrency-release-1"))
	_, err = PreConsumeUserSubscription("concurrency-release-2", 8301, "model", 0, 100)
	require.NoError(t, err)

	var lease SubscriptionConcurrencyLease
	require.NoError(t, DB.Where("request_id = ?", "concurrency-release-2").First(&lease).Error)
	assert.Greater(t, lease.ExpiresAt, now)
}

func TestSubscriptionConcurrencyExpiredLeaseIsReclaimed(t *testing.T) {
	truncateTables(t)
	now := GetDBTimestamp()
	plan := createConcurrencyPlan(t, "Expired concurrency", 1)
	subscription := createConcurrencySubscription(t, 8401, plan, now+3600)
	require.NoError(t, DB.Create(&SubscriptionConcurrencyLease{
		RequestId:          "concurrency-expired-old",
		UserId:             8401,
		UserSubscriptionId: subscription.Id,
		ExpiresAt:          now - 1,
	}).Error)

	_, err := PreConsumeUserSubscription("concurrency-expired-new", 8401, "model", 0, 100)
	require.NoError(t, err)

	var leases []SubscriptionConcurrencyLease
	require.NoError(t, DB.Find(&leases).Error)
	require.Len(t, leases, 1)
	assert.Equal(t, "concurrency-expired-new", leases[0].RequestId)
}

func TestSubscriptionConcurrencySelectsNextAvailableSubscription(t *testing.T) {
	truncateTables(t)
	now := GetDBTimestamp()
	firstPlan := createConcurrencyPlan(t, "First concurrency", 1)
	secondPlan := createConcurrencyPlan(t, "Second concurrency", 1)
	first := createConcurrencySubscription(t, 8501, firstPlan, now+1800)
	second := createConcurrencySubscription(t, 8501, secondPlan, now+3600)
	require.NoError(t, DB.Create(&SubscriptionConcurrencyLease{
		RequestId:          "concurrency-next-existing",
		UserId:             8501,
		UserSubscriptionId: first.Id,
		ExpiresAt:          now + 600,
	}).Error)

	result, err := PreConsumeUserSubscription("concurrency-next-new", 8501, "model", 0, 100)
	require.NoError(t, err)
	assert.Equal(t, second.Id, result.UserSubscriptionId)

	require.NoError(t, DB.First(first, first.Id).Error)
	require.NoError(t, DB.First(second, second.Id).Error)
	assert.Zero(t, first.AmountUsed)
	assert.EqualValues(t, 100, second.AmountUsed)
}

func TestSubscriptionConcurrencyRequestIdIsIdempotent(t *testing.T) {
	truncateTables(t)
	now := GetDBTimestamp()
	plan := createConcurrencyPlan(t, "Idempotent concurrency", 2)
	subscription := createConcurrencySubscription(t, 8601, plan, now+3600)

	first, err := PreConsumeUserSubscription("concurrency-idempotent", 8601, "model", 0, 100)
	require.NoError(t, err)
	second, err := PreConsumeUserSubscription("concurrency-idempotent", 8601, "other-model", 1, 200)
	require.NoError(t, err)
	assert.Equal(t, first.UserSubscriptionId, second.UserSubscriptionId)
	assert.Equal(t, first.PreConsumed, second.PreConsumed)
	assert.Equal(t, 2, second.ConcurrencyLimit)

	var leaseCount int64
	require.NoError(t, DB.Model(&SubscriptionConcurrencyLease{}).
		Where("request_id = ?", "concurrency-idempotent").Count(&leaseCount).Error)
	assert.EqualValues(t, 1, leaseCount)
	require.NoError(t, DB.First(subscription, subscription.Id).Error)
	assert.EqualValues(t, 100, subscription.AmountUsed)
}

func TestSubscriptionConcurrencyIdempotencyRestoresMissingLease(t *testing.T) {
	truncateTables(t)
	now := GetDBTimestamp()
	plan := createConcurrencyPlan(t, "Restore idempotent lease", 1)
	subscription := createConcurrencySubscription(t, 8651, plan, now+3600)

	first, err := PreConsumeUserSubscription("concurrency-restore", 8651, "model", 0, 100)
	require.NoError(t, err)
	require.NoError(t, ReleaseSubscriptionConcurrencyLease("concurrency-restore"))
	second, err := PreConsumeUserSubscription("concurrency-restore", 8651, "model", 0, 100)
	require.NoError(t, err)
	assert.Equal(t, first.PreConsumed, second.PreConsumed)

	var leaseCount int64
	require.NoError(t, DB.Model(&SubscriptionConcurrencyLease{}).
		Where("request_id = ?", "concurrency-restore").Count(&leaseCount).Error)
	assert.EqualValues(t, 1, leaseCount)
	require.NoError(t, DB.First(subscription, subscription.Id).Error)
	assert.EqualValues(t, 100, subscription.AmountUsed)
}

func TestSubscriptionConcurrencyMaintenanceRemovesExpiredAndInvalidatedLeases(t *testing.T) {
	truncateTables(t)
	now := GetDBTimestamp()
	plan := createConcurrencyPlan(t, "Lease cleanup", 2)
	expiredSubscription := createConcurrencySubscription(t, 8661, plan, now-1)
	invalidatedSubscription := createConcurrencySubscription(t, 8662, plan, now+3600)
	require.NoError(t, DB.Create(&[]SubscriptionConcurrencyLease{
		{RequestId: "lease-expired-subscription", UserId: 8661, UserSubscriptionId: expiredSubscription.Id, ExpiresAt: now + 600},
		{RequestId: "lease-invalidated-subscription", UserId: 8662, UserSubscriptionId: invalidatedSubscription.Id, ExpiresAt: now + 600},
	}).Error)

	count, err := ExpireDueSubscriptions(10)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
	_, err = AdminInvalidateUserSubscription(invalidatedSubscription.Id)
	require.NoError(t, err)

	var leaseCount int64
	require.NoError(t, DB.Model(&SubscriptionConcurrencyLease{}).Count(&leaseCount).Error)
	assert.Zero(t, leaseCount)
}

func TestCleanupExpiredSubscriptionConcurrencyLeasesKeepsActiveLease(t *testing.T) {
	truncateTables(t)
	now := GetDBTimestamp()
	require.NoError(t, DB.Create(&[]SubscriptionConcurrencyLease{
		{RequestId: "lease-cleanup-expired", UserId: 8671, UserSubscriptionId: 1, ExpiresAt: now - 1},
		{RequestId: "lease-cleanup-active", UserId: 8671, UserSubscriptionId: 1, ExpiresAt: now + 600},
	}).Error)

	deleted, err := CleanupExpiredSubscriptionConcurrencyLeases()
	require.NoError(t, err)
	assert.EqualValues(t, 1, deleted)

	var leases []SubscriptionConcurrencyLease
	require.NoError(t, DB.Find(&leases).Error)
	require.Len(t, leases, 1)
	assert.Equal(t, "lease-cleanup-active", leases[0].RequestId)
}

func TestRenewSubscriptionConcurrencyLease(t *testing.T) {
	truncateTables(t)
	now := GetDBTimestamp()
	require.NoError(t, DB.Create(&SubscriptionConcurrencyLease{
		RequestId:          "concurrency-renew",
		UserId:             8701,
		UserSubscriptionId: 8702,
		ExpiresAt:          now + 1,
	}).Error)

	require.NoError(t, RenewSubscriptionConcurrencyLease("concurrency-renew"))
	require.NoError(t, RenewSubscriptionConcurrencyLease("concurrency-missing"))
	var lease SubscriptionConcurrencyLease
	require.NoError(t, DB.Where("request_id = ?", "concurrency-renew").First(&lease).Error)
	assert.GreaterOrEqual(t, lease.ExpiresAt, now+subscriptionConcurrencyLeaseTTLSeconds)
}
