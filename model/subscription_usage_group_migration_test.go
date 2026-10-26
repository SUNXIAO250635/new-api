package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestMigrateSubscriptionUsageGroupColumnSizeSQLitePreservesLegacySchema(t *testing.T) {
	previousDB := DB
	previousType := common.MainDatabaseType()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	DB = db
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	t.Cleanup(func() {
		DB = previousDB
		common.SetMainDatabaseType(previousType)
	})

	require.NoError(t, db.Exec("CREATE TABLE subscription_plans (id integer PRIMARY KEY, usage_group varchar(64) DEFAULT '')").Error)
	require.NoError(t, db.Exec("CREATE TABLE user_subscriptions (id integer PRIMARY KEY, usage_group varchar(64) DEFAULT '')").Error)
	longUsageGroup := "group-a,group-b,group-c,group-d,group-e,group-f,group-g,group-h"
	require.NoError(t, db.Exec("INSERT INTO subscription_plans (id, usage_group) VALUES (?, ?)", 1, longUsageGroup).Error)
	require.NoError(t, db.Exec("INSERT INTO user_subscriptions (id, usage_group) VALUES (?, ?)", 1, longUsageGroup).Error)

	require.NoError(t, migrateSubscriptionUsageGroupColumnSize())
	var planValue string
	var subscriptionValue string
	require.NoError(t, db.Raw("SELECT usage_group FROM subscription_plans WHERE id = 1").Scan(&planValue).Error)
	require.NoError(t, db.Raw("SELECT usage_group FROM user_subscriptions WHERE id = 1").Scan(&subscriptionValue).Error)
	assert.Equal(t, longUsageGroup, planValue)
	assert.Equal(t, longUsageGroup, subscriptionValue)
}
