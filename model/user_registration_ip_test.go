package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestInsertWithTxEnforcesRegistrationIpRestriction(t *testing.T) {
	truncateTables(t)
	previousRestriction := common.RestrictSameIpRegistrationEnabled
	common.RestrictSameIpRegistrationEnabled = true
	t.Cleanup(func() {
		common.RestrictSameIpRegistrationEnabled = previousRestriction
	})

	first := &User{
		Username:   "registration-ip-first",
		Password:   "registration-password",
		RegisterIp: "198.51.100.60",
	}
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		return first.InsertWithTx(tx, 0)
	}))

	second := &User{
		Username:   "registration-ip-second",
		Password:   "registration-password",
		RegisterIp: "198.51.100.60",
	}
	err := DB.Transaction(func(tx *gorm.DB) error {
		return second.InsertWithTx(tx, 0)
	})
	require.ErrorIs(t, err, ErrRegistrationIPUsed)

	var count int64
	require.NoError(t, DB.Model(&User{}).Where("register_ip = ?", "198.51.100.60").Count(&count).Error)
	assert.EqualValues(t, 1, count)
}

func TestInsertWithTxAllowsDuplicateRegistrationIpWhenRestrictionIsDisabled(t *testing.T) {
	truncateTables(t)
	previousRestriction := common.RestrictSameIpRegistrationEnabled
	common.RestrictSameIpRegistrationEnabled = false
	t.Cleanup(func() {
		common.RestrictSameIpRegistrationEnabled = previousRestriction
	})

	for _, username := range []string{"registration-ip-open-first", "registration-ip-open-second"} {
		user := &User{
			Username:   username,
			Password:   "registration-password",
			RegisterIp: "203.0.113.60",
		}
		require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
			return user.InsertWithTx(tx, 0)
		}))
	}

	var count int64
	require.NoError(t, DB.Model(&User{}).Where("register_ip = ?", "203.0.113.60").Count(&count).Error)
	assert.EqualValues(t, 2, count)
}
