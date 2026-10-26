package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestShouldRecordIpHonorsAdministratorPolicyAndUserPreference(t *testing.T) {
	truncateTables(t)
	previousForceIpLogEnabled := common.ForceIpLogEnabled
	previousRedisEnabled := common.RedisEnabled
	common.ForceIpLogEnabled = false
	common.RedisEnabled = false
	t.Cleanup(func() {
		common.ForceIpLogEnabled = previousForceIpLogEnabled
		common.RedisEnabled = previousRedisEnabled
	})

	optedOut := User{Username: "ip-log-opted-out", Password: "unused", AffCode: "ip-log-out"}
	optedOut.SetSetting(dto.UserSetting{RecordIpLog: false})
	require.NoError(t, DB.Create(&optedOut).Error)

	optedIn := User{Username: "ip-log-opted-in", Password: "unused", AffCode: "ip-log-in"}
	optedIn.SetSetting(dto.UserSetting{RecordIpLog: true})
	require.NoError(t, DB.Create(&optedIn).Error)

	assert.False(t, ShouldRecordIp(optedOut.Id))
	assert.True(t, ShouldRecordIp(optedIn.Id))

	common.ForceIpLogEnabled = true
	assert.True(t, ShouldRecordIp(optedOut.Id))
	assert.True(t, ShouldRecordIp(999999), "forced logging must not depend on loading user settings")
}

func TestUpdateOptionMapAppliesForceIpLoggingImmediately(t *testing.T) {
	previousForceIpLogEnabled := common.ForceIpLogEnabled
	common.OptionMapRWMutex.Lock()
	previousOptionMap := common.OptionMap
	common.OptionMap = map[string]string{}
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.ForceIpLogEnabled = previousForceIpLogEnabled
		common.OptionMapRWMutex.Lock()
		common.OptionMap = previousOptionMap
		common.OptionMapRWMutex.Unlock()
	})

	require.NoError(t, updateOptionMap("ForceIpLogEnabled", "true"))
	assert.True(t, common.ForceIpLogEnabled)
	assert.Equal(t, "true", common.OptionMap["ForceIpLogEnabled"])
}
