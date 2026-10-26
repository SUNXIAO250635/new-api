package model

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func createWelfareQuotaTestUser(t *testing.T, quota int, expiresAt int64) User {
	t.Helper()
	user := User{
		Username:         "welfare-" + common.GetRandomString(8),
		Password:         "unused-password-hash",
		AffCode:          "welfare-aff-" + common.GetRandomString(8),
		WelfareQuota:     quota,
		WelfareExpiresAt: expiresAt,
	}
	require.NoError(t, DB.Create(&user).Error)
	return user
}

func TestTryReserveUserWelfareQuotaRequiresUnexpiredSufficientBalance(t *testing.T) {
	truncateTables(t)
	user := createWelfareQuotaTestUser(t, 100, time.Now().Add(time.Hour).Unix())

	reserved, err := TryReserveUserWelfareQuota(user.Id, 60)
	require.NoError(t, err)
	assert.True(t, reserved)

	state, err := GetUserWelfareQuota(user.Id)
	require.NoError(t, err)
	assert.Equal(t, 40, state.Quota)

	reserved, err = TryReserveUserWelfareQuota(user.Id, 41)
	require.NoError(t, err)
	assert.False(t, reserved)
}

func TestExpiredWelfareQuotaIsNotSpendableOrRefunded(t *testing.T) {
	truncateTables(t)
	user := createWelfareQuotaTestUser(t, 100, time.Now().Add(-time.Hour).Unix())

	state, err := GetUserWelfareQuota(user.Id)
	require.NoError(t, err)
	assert.Zero(t, state.Quota)

	reserved, err := TryReserveUserWelfareQuota(user.Id, 1)
	require.NoError(t, err)
	assert.False(t, reserved)
	require.ErrorIs(t, IncreaseUserWelfareQuota(user.Id, 20), ErrWelfareQuotaNotApplied)

	var stored int
	require.NoError(t, DB.Model(&User{}).Where("id = ?", user.Id).Select("welfare_quota").Scan(&stored).Error)
	assert.Equal(t, 100, stored)
}

func TestSetUserWelfareQuotaSupportsPermanentAndFutureExpiry(t *testing.T) {
	truncateTables(t)
	user := createWelfareQuotaTestUser(t, 0, 0)
	future := time.Now().Add(24 * time.Hour).Unix()

	require.NoError(t, SetUserWelfareQuota(user.Id, 200, future))
	state, err := GetUserWelfareQuota(user.Id)
	require.NoError(t, err)
	assert.Equal(t, WelfareQuotaState{Quota: 200, ExpiresAt: future, Version: 2}, state)

	require.Error(t, SetUserWelfareQuota(user.Id, 200, time.Now().Add(-time.Minute).Unix()))
	require.NoError(t, SetUserWelfareQuota(user.Id, 50, 0))
	state, err = GetUserWelfareQuota(user.Id)
	require.NoError(t, err)
	assert.Equal(t, WelfareQuotaState{Quota: 50, ExpiresAt: 0, Version: 3}, state)
}

func TestSetUserWelfareQuotaRejectsMissingUser(t *testing.T) {
	truncateTables(t)
	require.ErrorIs(t, SetUserWelfareQuota(999999, 100, 0), gorm.ErrRecordNotFound)
}

func TestWelfareQuotaAdministratorOverrideInvalidatesInFlightRefund(t *testing.T) {
	truncateTables(t)
	user := createWelfareQuotaTestUser(t, 100, time.Now().Add(time.Hour).Unix())

	version, reserved, err := TryReserveUserWelfareQuotaVersioned(user.Id, 60)
	require.NoError(t, err)
	require.True(t, reserved)
	require.Positive(t, version)

	require.NoError(t, SetUserWelfareQuota(user.Id, 10, 0))
	applied, err := IncreaseUserWelfareQuotaForVersion(user.Id, 60, version)
	require.NoError(t, err)
	assert.False(t, applied)

	state, err := GetUserWelfareQuota(user.Id)
	require.NoError(t, err)
	assert.Equal(t, 10, state.Quota)
	assert.Greater(t, state.Version, version)
}

func TestWelfareQuotaRejectsValuesAboveDatabaseBoundary(t *testing.T) {
	truncateTables(t)
	user := createWelfareQuotaTestUser(t, common.MaxQuota, 0)

	require.Error(t, SetUserWelfareQuota(user.Id, common.MaxQuota+1, 0))
	require.ErrorIs(t, IncreaseUserWelfareQuota(user.Id, 1), ErrWelfareQuotaNotApplied)

	state, err := GetUserWelfareQuota(user.Id)
	require.NoError(t, err)
	assert.Equal(t, common.MaxQuota, state.Quota)
}
