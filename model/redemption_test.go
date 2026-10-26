package model

import (
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSearchRedemptionsFiltersAndPaginates(t *testing.T) {
	require.NoError(t, DB.AutoMigrate(&Redemption{}))
	require.NoError(t, DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Unscoped().Delete(&Redemption{}).Error)
	t.Cleanup(func() {
		require.NoError(t, DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Unscoped().Delete(&Redemption{}).Error)
	})

	now := common.GetTimestamp()
	redemptions := []Redemption{
		{Id: 1, Name: "alpha-active", Key: "00000000000000000000000000000001", Status: common.RedemptionCodeStatusEnabled, Type: RedemptionTypeSubscription, PlanId: 99, ExpiredTime: 0},
		{Id: 2, Name: "alpha-future", Key: "00000000000000000000000000000002", Status: common.RedemptionCodeStatusEnabled, ExpiredTime: now + 3600},
		{Id: 3, Name: "alpha-expired", Key: "00000000000000000000000000000003", Status: common.RedemptionCodeStatusEnabled, ExpiredTime: now - 10},
		{Id: 4, Name: "beta-disabled", Key: "00000000000000000000000000000004", Status: common.RedemptionCodeStatusDisabled, ExpiredTime: 0},
		{Id: 5, Name: "beta-used", Key: "00000000000000000000000000000005", Status: common.RedemptionCodeStatusUsed, ExpiredTime: 0},
		{Id: 6, Name: "welfare-active", Key: "00000000000000000000000000000006", Status: common.RedemptionCodeStatusEnabled, Type: RedemptionTypeWelfare, Quota: 200, ExpiredTime: 0},
	}
	require.NoError(t, DB.Create(&redemptions).Error)

	tests := []struct {
		name      string
		keyword   string
		status    string
		startIdx  int
		num       int
		wantTotal int64
		wantIds   []int
	}{
		{
			name:      "no filters returns all rows",
			num:       10,
			wantTotal: 6,
			wantIds:   []int{6, 5, 4, 3, 2, 1},
		},
		{
			name:      "keyword filters by name prefix",
			keyword:   "alpha",
			num:       10,
			wantTotal: 3,
			wantIds:   []int{3, 2, 1},
		},
		{
			name:      "enabled status excludes expired rows",
			status:    "1",
			num:       10,
			wantTotal: 3,
			wantIds:   []int{6, 2, 1},
		},
		{
			name:      "expired status returns enabled expired rows",
			status:    "expired",
			num:       10,
			wantTotal: 1,
			wantIds:   []int{3},
		},
		{
			name:      "disabled status",
			status:    "2",
			num:       10,
			wantTotal: 1,
			wantIds:   []int{4},
		},
		{
			name:      "used status",
			status:    "3",
			num:       10,
			wantTotal: 1,
			wantIds:   []int{5},
		},
		{
			name:      "pagination keeps unpaged total",
			startIdx:  1,
			num:       2,
			wantTotal: 6,
			wantIds:   []int{5, 4},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rows, total, err := SearchRedemptions(tt.keyword, tt.status, tt.startIdx, tt.num)
			require.NoError(t, err)
			assert.Equal(t, tt.wantTotal, total)
			gotIds := make([]int, 0, len(rows))
			for _, row := range rows {
				gotIds = append(gotIds, row.Id)
			}
			assert.Equal(t, tt.wantIds, gotIds)
		})
	}

	t.Run("type filter returns only subscription redemption codes", func(t *testing.T) {
		rows, total, err := SearchRedemptionsWithType("", "", RedemptionTypeSubscription, 0, 10)
		require.NoError(t, err)
		assert.EqualValues(t, 1, total)
		require.Len(t, rows, 1)
		assert.Equal(t, 1, rows[0].Id)
	})

	t.Run("quota type filter includes legacy empty values", func(t *testing.T) {
		require.NoError(t, DB.Model(&Redemption{}).Where("id = ?", 2).Update("type", "").Error)
		rows, total, err := SearchRedemptionsWithType("", "", RedemptionTypeQuota, 0, 10)
		require.NoError(t, err)
		assert.EqualValues(t, 4, total)
		assert.Len(t, rows, 4)
	})

	t.Run("welfare type filter returns only welfare redemption codes", func(t *testing.T) {
		rows, total, err := SearchRedemptionsWithType("", "", RedemptionTypeWelfare, 0, 10)
		require.NoError(t, err)
		assert.EqualValues(t, 1, total)
		require.Len(t, rows, 1)
		assert.Equal(t, 6, rows[0].Id)
	})

	t.Run("export returns every matching key without pagination", func(t *testing.T) {
		rows, err := SearchRedemptionsForExport("alpha", "")
		require.NoError(t, err)
		gotKeys := make([]string, 0, len(rows))
		for _, row := range rows {
			gotKeys = append(gotKeys, row.Key)
		}
		assert.Equal(t, []string{
			"00000000000000000000000000000003",
			"00000000000000000000000000000002",
			"00000000000000000000000000000001",
		}, gotKeys)

		expiredRows, err := SearchRedemptionsForExport("", "expired")
		require.NoError(t, err)
		require.Len(t, expiredRows, 1)
		assert.Equal(t, "00000000000000000000000000000003", expiredRows[0].Key)
	})
}

func setupRedeemFixture(t *testing.T, quota int) (userId int, key string) {
	t.Helper()
	require.NoError(t, DB.AutoMigrate(&Redemption{}))
	require.NoError(t, DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Unscoped().Delete(&Redemption{}).Error)
	t.Cleanup(func() {
		require.NoError(t, DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Unscoped().Delete(&Redemption{}).Error)
		DB.Exec("DELETE FROM users")
		DB.Exec("DELETE FROM logs")
	})

	user := &User{Username: "redeem-user", Password: "password", Status: common.UserStatusEnabled, Quota: 0}
	require.NoError(t, DB.Create(user).Error)

	key = "10000000000000000000000000000001"
	redemption := &Redemption{
		Name:        "redeem-test",
		Key:         key,
		Status:      common.RedemptionCodeStatusEnabled,
		Quota:       quota,
		CreatedTime: common.GetTimestamp(),
	}
	require.NoError(t, DB.Create(redemption).Error)
	return user.Id, key
}

func TestRedeemCreditsQuotaExactlyOnce(t *testing.T) {
	userId, key := setupRedeemFixture(t, 500)

	quota, err := Redeem(key, userId)
	require.NoError(t, err)
	assert.Equal(t, 500, quota)

	var user User
	require.NoError(t, DB.First(&user, "id = ?", userId).Error)
	assert.Equal(t, 500, user.Quota)

	var redemption Redemption
	require.NoError(t, DB.First(&redemption, "name = ?", "redeem-test").Error)
	assert.Equal(t, common.RedemptionCodeStatusUsed, redemption.Status)
	assert.Equal(t, userId, redemption.UsedUserId)

	// Redeeming the same code again must fail and must not credit quota.
	_, err = Redeem(key, userId)
	require.Error(t, err)
	require.NoError(t, DB.First(&user, "id = ?", userId).Error)
	assert.Equal(t, 500, user.Quota)
}

func TestRedeemInvalidQuotaKeepsCodeAvailable(t *testing.T) {
	userId, key := setupRedeemFixture(t, -1)

	_, err := RedeemWithResult(key, userId)
	require.Error(t, err)

	var redemption Redemption
	require.NoError(t, DB.Where("key = ?", key).First(&redemption).Error)
	assert.Equal(t, common.RedemptionCodeStatusEnabled, redemption.Status)
	assert.Zero(t, redemption.UsedUserId)
	assert.Zero(t, getUserQuotaFromDB(t, userId))
}

func TestRedeemRejectsWalletOverflow(t *testing.T) {
	userId, key := setupRedeemFixture(t, 11)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", userId).Update("quota", common.MaxWalletQuota-10).Error)

	_, err := Redeem(key, userId)
	require.ErrorIs(t, err, ErrRedeemFailed)

	var user User
	require.NoError(t, DB.First(&user, "id = ?", userId).Error)
	assert.Equal(t, common.MaxWalletQuota-10, user.Quota)

	var redemption Redemption
	require.NoError(t, DB.First(&redemption, "key = ?", key).Error)
	assert.Equal(t, common.RedemptionCodeStatusEnabled, redemption.Status)
}

func TestRedemptionQuotaRejectsWalletOverflow(t *testing.T) {
	setupRedeemFixture(t, 500)

	redemption := &Redemption{
		Name:        "overflow-redemption",
		Key:         "10000000000000000000000000000002",
		Status:      common.RedemptionCodeStatusEnabled,
		Quota:       common.MaxWalletQuota + 1,
		CreatedTime: common.GetTimestamp(),
	}
	require.Error(t, redemption.Insert())
}

func TestRedeemBatchAccountPolicy(t *testing.T) {
	tests := []struct {
		name             string
		batchId          string
		allowSameUser    bool
		wantSecondRedeem bool
		wantQuota        int
		wantClaims       int64
	}{
		{
			name:       "new batch limits one redemption per account by default",
			batchId:    common.GetUUID(),
			wantQuota:  125,
			wantClaims: 1,
		},
		{
			name:             "administrator can allow repeated redemption within a batch",
			batchId:          common.GetUUID(),
			allowSameUser:    true,
			wantSecondRedeem: true,
			wantQuota:        250,
		},
		{
			name:             "legacy codes without a batch keep original behavior",
			wantSecondRedeem: true,
			wantQuota:        250,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.NoError(t, DB.AutoMigrate(&Redemption{}, &RedemptionBatchClaim{}))
			require.NoError(t, DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Unscoped().Delete(&Redemption{}).Error)
			require.NoError(t, DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&RedemptionBatchClaim{}).Error)

			user := &User{
				Username: "batch-redeem-" + common.GetRandomString(8),
				Password: "password",
				Status:   common.UserStatusEnabled,
				AffCode:  "batch-aff-" + common.GetRandomString(8),
			}
			require.NoError(t, DB.Create(user).Error)
			t.Cleanup(func() {
				require.NoError(t, DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&RedemptionBatchClaim{}).Error)
				require.NoError(t, DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Unscoped().Delete(&Redemption{}).Error)
				require.NoError(t, DB.Unscoped().Delete(&User{}, user.Id).Error)
			})

			codes := []Redemption{
				{
					Name:          "batch-code-a",
					Key:           common.GetUUID(),
					Status:        common.RedemptionCodeStatusEnabled,
					Quota:         125,
					Type:          RedemptionTypeQuota,
					BatchId:       tt.batchId,
					AllowSameUser: tt.allowSameUser,
					CreatedTime:   common.GetTimestamp(),
				},
				{
					Name:          "batch-code-b",
					Key:           common.GetUUID(),
					Status:        common.RedemptionCodeStatusEnabled,
					Quota:         125,
					Type:          RedemptionTypeQuota,
					BatchId:       tt.batchId,
					AllowSameUser: tt.allowSameUser,
					CreatedTime:   common.GetTimestamp(),
				},
			}
			require.NoError(t, DB.Create(&codes).Error)

			_, err := RedeemWithResult(codes[0].Key, user.Id)
			require.NoError(t, err)
			_, err = RedeemWithResult(codes[1].Key, user.Id)
			if tt.wantSecondRedeem {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}

			var updatedUser User
			require.NoError(t, DB.First(&updatedUser, user.Id).Error)
			assert.Equal(t, tt.wantQuota, updatedUser.Quota)
			var secondCode Redemption
			require.NoError(t, DB.First(&secondCode, codes[1].Id).Error)
			if tt.wantSecondRedeem {
				assert.Equal(t, common.RedemptionCodeStatusUsed, secondCode.Status)
			} else {
				assert.Equal(t, common.RedemptionCodeStatusEnabled, secondCode.Status)
			}
			var claimCount int64
			require.NoError(t, DB.Model(&RedemptionBatchClaim{}).Where("batch_id = ? AND user_id = ?", tt.batchId, user.Id).Count(&claimCount).Error)
			assert.Equal(t, tt.wantClaims, claimCount)
		})
	}
}

// Exactly one of several concurrent redeems of the same code may win, and
// quota must be credited exactly once.
func TestRedeemConcurrentSingleSuccess(t *testing.T) {
	userId, key := setupRedeemFixture(t, 300)

	const goroutines = 5
	successes := make([]bool, goroutines)
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := range goroutines {
		go func(idx int) {
			defer wg.Done()
			if _, err := Redeem(key, userId); err == nil {
				successes[idx] = true
			}
		}(i)
	}
	wg.Wait()

	successCount := 0
	for _, ok := range successes {
		if ok {
			successCount++
		}
	}
	assert.Equal(t, 1, successCount, "exactly one concurrent redeem should succeed")

	var user User
	require.NoError(t, DB.First(&user, "id = ?", userId).Error)
	assert.Equal(t, 300, user.Quota, "quota must be credited exactly once")
}

func setupSubscriptionRedemptionTest(t *testing.T) {
	t.Helper()
	truncateTables(t)
	require.NoError(t, DB.AutoMigrate(&Redemption{}))
	require.NoError(t, DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Unscoped().Delete(&Redemption{}).Error)
	t.Cleanup(func() {
		require.NoError(t, DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Unscoped().Delete(&Redemption{}).Error)
	})
}

func TestRedeemSubscriptionCodeCreatesSubscription(t *testing.T) {
	setupSubscriptionRedemptionTest(t)
	user := &User{
		Username: "subscription-redeem-user",
		Password: "password",
		Status:   common.UserStatusEnabled,
		Group:    "default",
		Quota:    77,
	}
	require.NoError(t, DB.Create(user).Error)
	plan := &SubscriptionPlan{
		Title:         "Redeemed Pro",
		DurationUnit:  SubscriptionDurationMonth,
		DurationValue: 1,
		TotalAmount:   1200,
		UpgradeGroup:  "pro",
		UsageGroup:    "vip",
		Enabled:       true,
	}
	require.NoError(t, DB.Create(plan).Error)
	redemption := &Redemption{
		Name:        "subscription-code",
		Key:         "20000000000000000000000000000001",
		Status:      common.RedemptionCodeStatusEnabled,
		Type:        RedemptionTypeSubscription,
		PlanId:      plan.Id,
		CreatedTime: common.GetTimestamp(),
	}
	require.NoError(t, DB.Create(redemption).Error)

	result, err := RedeemWithResult(redemption.Key, user.Id)
	require.NoError(t, err)
	assert.Equal(t, RedemptionTypeSubscription, result.Type)
	assert.Zero(t, result.Quota)
	assert.Equal(t, plan.Id, result.PlanId)
	assert.Equal(t, plan.Title, result.PlanTitle)
	assert.Positive(t, result.UserSubscriptionId)

	var subscription UserSubscription
	require.NoError(t, DB.First(&subscription, result.UserSubscriptionId).Error)
	assert.Equal(t, "redemption", subscription.Source)
	assert.Equal(t, plan.Id, subscription.PlanId)
	assert.Equal(t, "vip", subscription.UsageGroup)
	assert.EqualValues(t, 1200, subscription.AmountTotal)

	var updatedUser User
	require.NoError(t, DB.First(&updatedUser, user.Id).Error)
	assert.Equal(t, 77, updatedUser.Quota)
	assert.Equal(t, "pro", updatedUser.Group)
	require.NoError(t, DB.First(redemption, redemption.Id).Error)
	assert.Equal(t, common.RedemptionCodeStatusUsed, redemption.Status)
	assert.Equal(t, user.Id, redemption.UsedUserId)
}

func TestRedeemSubscriptionFailureKeepsCodeAvailable(t *testing.T) {
	setupSubscriptionRedemptionTest(t)
	user := &User{Username: "subscription-limit-user", Password: "password", Status: common.UserStatusEnabled}
	require.NoError(t, DB.Create(user).Error)
	plan := &SubscriptionPlan{
		Title:              "Limited",
		DurationUnit:       SubscriptionDurationMonth,
		DurationValue:      1,
		TotalAmount:        100,
		MaxPurchasePerUser: 1,
		Enabled:            true,
	}
	require.NoError(t, DB.Create(plan).Error)
	now := common.GetTimestamp()
	require.NoError(t, DB.Create(&UserSubscription{
		UserId:      user.Id,
		PlanId:      plan.Id,
		AmountTotal: plan.TotalAmount,
		StartTime:   now,
		EndTime:     now + 3600,
		Status:      "active",
	}).Error)
	redemption := &Redemption{
		Name:        "limited-subscription-code",
		Key:         "20000000000000000000000000000002",
		Status:      common.RedemptionCodeStatusEnabled,
		Type:        RedemptionTypeSubscription,
		PlanId:      plan.Id,
		CreatedTime: now,
	}
	require.NoError(t, DB.Create(redemption).Error)

	_, err := RedeemWithResult(redemption.Key, user.Id)
	require.Error(t, err)
	require.NoError(t, DB.First(redemption, redemption.Id).Error)
	assert.Equal(t, common.RedemptionCodeStatusEnabled, redemption.Status)
	assert.Zero(t, redemption.UsedUserId)

	var subscriptionCount int64
	require.NoError(t, DB.Model(&UserSubscription{}).Where("user_id = ? AND plan_id = ?", user.Id, plan.Id).Count(&subscriptionCount).Error)
	assert.EqualValues(t, 1, subscriptionCount)
}

func TestRedeemWelfareCodeCreditsActiveGrantWithoutExtendingExpiry(t *testing.T) {
	setupSubscriptionRedemptionTest(t)
	expiresAt := common.GetTimestamp() + 3600
	user := &User{
		Username:         "welfare-redeem-user",
		Password:         "password",
		Status:           common.UserStatusEnabled,
		Quota:            77,
		WelfareQuota:     100,
		WelfareExpiresAt: expiresAt,
		WelfareVersion:   4,
	}
	require.NoError(t, DB.Create(user).Error)
	redemption := &Redemption{
		Name:        "welfare-code",
		Key:         "30000000000000000000000000000001",
		Status:      common.RedemptionCodeStatusEnabled,
		Type:        RedemptionTypeWelfare,
		Quota:       250,
		CreatedTime: common.GetTimestamp(),
	}
	require.NoError(t, DB.Create(redemption).Error)

	result, err := RedeemWithResult(redemption.Key, user.Id)
	require.NoError(t, err)
	assert.Equal(t, RedemptionTypeWelfare, result.Type)
	assert.Equal(t, 250, result.Quota)

	var updated User
	require.NoError(t, DB.First(&updated, user.Id).Error)
	assert.Equal(t, 77, updated.Quota)
	assert.Equal(t, 350, updated.WelfareQuota)
	assert.Equal(t, expiresAt, updated.WelfareExpiresAt)
	assert.EqualValues(t, 4, updated.WelfareVersion)
	require.NoError(t, DB.First(redemption, redemption.Id).Error)
	assert.Equal(t, common.RedemptionCodeStatusUsed, redemption.Status)
	assert.Equal(t, user.Id, redemption.UsedUserId)
}

func TestRedeemWelfareCodeSetsConfiguredBalanceExpiration(t *testing.T) {
	setupSubscriptionRedemptionTest(t)
	welfareExpiresAt := common.GetTimestamp() + 7200
	user := &User{
		Username: "timed-welfare-redeem-user",
		Password: "password",
		Status:   common.UserStatusEnabled,
	}
	require.NoError(t, DB.Create(user).Error)
	redemption := &Redemption{
		Name:             "timed-welfare-code",
		Key:              "30000000000000000000000000000004",
		Status:           common.RedemptionCodeStatusEnabled,
		Type:             RedemptionTypeWelfare,
		Quota:            250,
		WelfareExpiresAt: &welfareExpiresAt,
		CreatedTime:      common.GetTimestamp(),
	}
	require.NoError(t, DB.Create(redemption).Error)

	_, err := RedeemWithResult(redemption.Key, user.Id)
	require.NoError(t, err)
	state, err := GetUserWelfareQuota(user.Id)
	require.NoError(t, err)
	assert.Equal(t, WelfareQuotaState{Quota: 250, ExpiresAt: welfareExpiresAt, Version: 2}, state)
}

func TestRedeemWelfareCodeRejectsDifferentActiveBalanceExpiration(t *testing.T) {
	setupSubscriptionRedemptionTest(t)
	currentExpiresAt := common.GetTimestamp() + 3600
	codeExpiresAt := currentExpiresAt + 3600
	user := &User{
		Username:         "welfare-expiry-conflict-user",
		Password:         "password",
		Status:           common.UserStatusEnabled,
		WelfareQuota:     100,
		WelfareExpiresAt: currentExpiresAt,
		WelfareVersion:   4,
	}
	require.NoError(t, DB.Create(user).Error)
	redemption := &Redemption{
		Name:             "conflicting-welfare-code",
		Key:              "30000000000000000000000000000005",
		Status:           common.RedemptionCodeStatusEnabled,
		Type:             RedemptionTypeWelfare,
		Quota:            250,
		WelfareExpiresAt: &codeExpiresAt,
		CreatedTime:      common.GetTimestamp(),
	}
	require.NoError(t, DB.Create(redemption).Error)

	_, err := RedeemWithResult(redemption.Key, user.Id)
	require.ErrorIs(t, err, ErrWelfareQuotaExpiryConflict)
	state, stateErr := GetUserWelfareQuota(user.Id)
	require.NoError(t, stateErr)
	assert.Equal(t, WelfareQuotaState{Quota: 100, ExpiresAt: currentExpiresAt, Version: 4}, state)
	require.NoError(t, DB.First(redemption, redemption.Id).Error)
	assert.Equal(t, common.RedemptionCodeStatusEnabled, redemption.Status)
}

func TestRedeemWelfareCodeAddsToBalanceWithSameExpiration(t *testing.T) {
	setupSubscriptionRedemptionTest(t)
	expiresAt := common.GetTimestamp() + 3600
	user := &User{
		Username:         "same-expiry-welfare-user",
		Password:         "password",
		Status:           common.UserStatusEnabled,
		WelfareQuota:     100,
		WelfareExpiresAt: expiresAt,
		WelfareVersion:   4,
	}
	require.NoError(t, DB.Create(user).Error)
	redemption := &Redemption{
		Name:             "same-expiry-welfare-code",
		Key:              "30000000000000000000000000000006",
		Status:           common.RedemptionCodeStatusEnabled,
		Type:             RedemptionTypeWelfare,
		Quota:            250,
		WelfareExpiresAt: &expiresAt,
		CreatedTime:      common.GetTimestamp(),
	}
	require.NoError(t, DB.Create(redemption).Error)

	_, err := RedeemWithResult(redemption.Key, user.Id)
	require.NoError(t, err)
	state, stateErr := GetUserWelfareQuota(user.Id)
	require.NoError(t, stateErr)
	assert.Equal(t, WelfareQuotaState{Quota: 350, ExpiresAt: expiresAt, Version: 4}, state)
	require.NoError(t, DB.First(redemption, redemption.Id).Error)
	assert.Equal(t, common.RedemptionCodeStatusUsed, redemption.Status)
}

func TestRedeemWelfareCodeReplacesExpiredGrantWithPermanentBalance(t *testing.T) {
	setupSubscriptionRedemptionTest(t)
	user := &User{
		Username:         "expired-welfare-redeem-user",
		Password:         "password",
		Status:           common.UserStatusEnabled,
		WelfareQuota:     900,
		WelfareExpiresAt: common.GetTimestamp() - 60,
		WelfareVersion:   7,
	}
	require.NoError(t, DB.Create(user).Error)
	redemption := &Redemption{
		Name:        "fresh-welfare-code",
		Key:         "30000000000000000000000000000002",
		Status:      common.RedemptionCodeStatusEnabled,
		Type:        RedemptionTypeWelfare,
		Quota:       150,
		CreatedTime: common.GetTimestamp(),
	}
	require.NoError(t, DB.Create(redemption).Error)

	_, err := RedeemWithResult(redemption.Key, user.Id)
	require.NoError(t, err)
	state, err := GetUserWelfareQuota(user.Id)
	require.NoError(t, err)
	assert.Equal(t, WelfareQuotaState{Quota: 150, ExpiresAt: 0, Version: 8}, state)
}

func TestRedeemWelfareOverflowKeepsCodeAvailable(t *testing.T) {
	setupSubscriptionRedemptionTest(t)
	user := &User{
		Username:       "welfare-overflow-user",
		Password:       "password",
		Status:         common.UserStatusEnabled,
		WelfareQuota:   common.MaxQuota - 5,
		WelfareVersion: 2,
	}
	require.NoError(t, DB.Create(user).Error)
	redemption := &Redemption{
		Name:        "overflow-welfare-code",
		Key:         "30000000000000000000000000000003",
		Status:      common.RedemptionCodeStatusEnabled,
		Type:        RedemptionTypeWelfare,
		BatchId:     common.GetUUID(),
		Quota:       10,
		CreatedTime: common.GetTimestamp(),
	}
	require.NoError(t, DB.Create(redemption).Error)

	_, err := RedeemWithResult(redemption.Key, user.Id)
	require.Error(t, err)
	require.NoError(t, DB.First(redemption, redemption.Id).Error)
	assert.Equal(t, common.RedemptionCodeStatusEnabled, redemption.Status)
	assert.Zero(t, redemption.UsedUserId)
	state, err := GetUserWelfareQuota(user.Id)
	require.NoError(t, err)
	assert.Equal(t, common.MaxQuota-5, state.Quota)
	var claimCount int64
	require.NoError(t, DB.Model(&RedemptionBatchClaim{}).Where("batch_id = ?", redemption.BatchId).Count(&claimCount).Error)
	assert.Zero(t, claimCount)
}
