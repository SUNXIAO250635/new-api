package model

import (
	"fmt"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type nativeLegacyUser struct {
	Id          int     `gorm:"primaryKey"`
	Username    string  `gorm:"type:varchar(12);uniqueIndex"`
	Password    string  `gorm:"not null"`
	AccessToken *string `gorm:"type:char(32);column:access_token;uniqueIndex"`
	Quota       int     `gorm:"type:int;default:0"`
	AffCode     string  `gorm:"type:varchar(32);column:aff_code;uniqueIndex"`
}

func (nativeLegacyUser) TableName() string { return "users" }

type nativeLegacyRedemption struct {
	Id          int    `gorm:"primaryKey"`
	Key         string `gorm:"type:char(32);uniqueIndex"`
	Status      int    `gorm:"default:1"`
	Name        string `gorm:"index"`
	Quota       int    `gorm:"default:100"`
	CreatedTime int64  `gorm:"bigint"`
}

func (nativeLegacyRedemption) TableName() string { return "redemptions" }

type nativeLegacySubscriptionPlan struct {
	Id                      int     `gorm:"primaryKey"`
	Title                   string  `gorm:"type:varchar(128);not null"`
	Subtitle                string  `gorm:"type:varchar(255);default:''"`
	PriceAmount             float64 `gorm:"type:decimal(10,6);not null;default:0"`
	Currency                string  `gorm:"type:varchar(8);not null;default:'USD'"`
	DurationUnit            string  `gorm:"type:varchar(16);not null;default:'month'"`
	DurationValue           int     `gorm:"type:int;not null;default:1"`
	CustomSeconds           int64   `gorm:"type:bigint;not null;default:0"`
	Enabled                 bool    `gorm:"default:true"`
	SortOrder               int     `gorm:"type:int;default:0"`
	AllowBalancePay         *bool
	AllowWalletOverflow     *bool
	StripePriceId           string `gorm:"type:varchar(128);default:''"`
	CreemProductId          string `gorm:"type:varchar(128);default:''"`
	WaffoPancakeProductId   string `gorm:"type:varchar(128);default:''"`
	MaxPurchasePerUser      int    `gorm:"type:int;default:0"`
	UpgradeGroup            string `gorm:"type:varchar(64);default:''"`
	DowngradeGroup          string `gorm:"type:varchar(64);default:''"`
	TotalAmount             int64  `gorm:"type:bigint;not null;default:0"`
	QuotaResetPeriod        string `gorm:"type:varchar(16);default:'never'"`
	QuotaResetCustomSeconds int64  `gorm:"type:bigint;default:0"`
	CreatedAt               int64  `gorm:"bigint"`
	UpdatedAt               int64  `gorm:"bigint"`
}

func (nativeLegacySubscriptionPlan) TableName() string { return "subscription_plans" }

type nativeLegacyUserSubscription struct {
	Id                  int    `gorm:"primaryKey"`
	UserId              int    `gorm:"index"`
	PlanId              int    `gorm:"index"`
	AmountTotal         int64  `gorm:"type:bigint;not null;default:0"`
	AmountUsed          int64  `gorm:"type:bigint;not null;default:0"`
	StartTime           int64  `gorm:"bigint"`
	EndTime             int64  `gorm:"bigint;index"`
	Status              string `gorm:"type:varchar(32);index"`
	Source              string `gorm:"type:varchar(32);default:'order'"`
	LastResetTime       int64  `gorm:"type:bigint;default:0"`
	NextResetTime       int64  `gorm:"type:bigint;default:0;index"`
	UpgradeGroup        string `gorm:"type:varchar(64);default:''"`
	PrevUserGroup       string `gorm:"type:varchar(64);default:''"`
	DowngradeGroup      string `gorm:"type:varchar(64);default:''"`
	AllowWalletOverflow bool
	CreatedAt           int64 `gorm:"bigint"`
	UpdatedAt           int64 `gorm:"bigint"`
}

func (nativeLegacyUserSubscription) TableName() string { return "user_subscriptions" }

type nativeLegacyTask struct {
	ID        int64  `gorm:"primary_key;AUTO_INCREMENT"`
	CreatedAt int64  `gorm:"index"`
	TaskID    string `gorm:"type:varchar(191);index"`
	UserId    int    `gorm:"index"`
	Quota     int
	Status    TaskStatus `gorm:"type:varchar(20);index"`
}

func (nativeLegacyTask) TableName() string { return "tasks" }

func TestNativeNewAPIDataUpgradesWithCompatibleDefaults(t *testing.T) {
	previousDB := DB
	previousType := common.MainDatabaseType()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	DB = db
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	t.Cleanup(func() {
		DB = previousDB
		common.SetMainDatabaseType(previousType)
		sqlDB, closeErr := db.DB()
		if closeErr == nil {
			_ = sqlDB.Close()
		}
	})

	require.NoError(t, db.AutoMigrate(
		&nativeLegacyUser{},
		&nativeLegacyRedemption{},
		&nativeLegacySubscriptionPlan{},
		&nativeLegacyUserSubscription{},
		&nativeLegacyTask{},
	))
	require.NoError(t, db.Create(&nativeLegacyUser{Id: 101, Username: "native-user", Password: "native-password-hash", Quota: 700}).Error)
	require.NoError(t, db.Create(&nativeLegacyRedemption{
		Id: 201, Key: "30000000000000000000000000000001", Status: common.RedemptionCodeStatusEnabled, Name: "native-code", Quota: 500,
	}).Error)
	require.NoError(t, db.Create(&nativeLegacySubscriptionPlan{
		Id: 301, Title: "Native plan", DurationUnit: SubscriptionDurationMonth, DurationValue: 1, TotalAmount: 1000,
	}).Error)
	require.NoError(t, db.Create(&nativeLegacyUserSubscription{
		Id: 401, UserId: 101, PlanId: 301, AmountTotal: 1000, AmountUsed: 125, Status: "active",
	}).Error)
	require.NoError(t, db.Create(&nativeLegacyTask{
		ID: 501, CreatedAt: 1_700_000_000, TaskID: "native-task", UserId: 101, Quota: 250, Status: TaskStatusSuccess,
	}).Error)

	require.NoError(t, migrateDB())

	var user User
	require.NoError(t, db.First(&user, 101).Error)
	assert.Equal(t, "native-user", user.Username)
	assert.Equal(t, 700, user.Quota)
	assert.Zero(t, user.WelfareQuota)
	assert.Zero(t, user.WelfareExpiresAt)
	assert.Equal(t, int64(1), user.WelfareVersion)
	assert.Empty(t, user.RegisterIp)
	assert.True(t, db.Migrator().HasTable(&RegistrationIPClaim{}))

	var redemption Redemption
	require.NoError(t, db.First(&redemption, 201).Error)
	assert.Equal(t, RedemptionTypeQuota, NormalizeRedemptionType(redemption.Type))
	assert.Zero(t, redemption.PlanId)
	assert.Empty(t, redemption.BatchId)
	assert.Nil(t, redemption.WelfareExpiresAt)
	assert.False(t, redemption.AllowSameUser)
	assert.Equal(t, 500, redemption.Quota)
	assert.True(t, db.Migrator().HasTable(&RedemptionBatchClaim{}))

	var plan SubscriptionPlan
	require.NoError(t, db.First(&plan, 301).Error)
	assert.Equal(t, "Native plan", plan.Title)
	assert.Empty(t, plan.UsageGroup)
	assert.Zero(t, plan.ConcurrencyLimit)

	var subscription UserSubscription
	require.NoError(t, db.First(&subscription, 401).Error)
	assert.EqualValues(t, 125, subscription.AmountUsed)
	assert.Empty(t, subscription.UsageGroup)
	assert.Zero(t, subscription.ConcurrencyLimit)
	assert.True(t, db.Migrator().HasTable(&SubscriptionConcurrencyLease{}))
	assert.True(t, db.Migrator().HasTable(&BillingRefundRecord{}))
	assert.True(t, db.Migrator().HasColumn(&SubscriptionPreConsumeRecord{}, "post_consumed"))

	var task Task
	require.NoError(t, db.First(&task, 501).Error)
	assert.Equal(t, "native-task", task.TaskID)
	assert.Equal(t, 250, task.Quota)
	assert.False(t, task.FundingRefundPending)
	assert.False(t, task.TokenSettlementPending)
	assert.Zero(t, task.TokenSettlementTarget)
	assert.True(t, db.Migrator().HasIndex(&Task{}, "idx_tasks_token_settlement_pending"))
	assert.True(t, db.Migrator().HasIndex(&Task{}, "idx_tasks_funding_refund_pending"))
}
