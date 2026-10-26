package controller

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func setupRedemptionBatchControllerTest(t *testing.T) *gorm.DB {
	t.Helper()
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousMainType := common.MainDatabaseType()
	previousLogType := common.LogDatabaseType()
	previousRedisEnabled := common.RedisEnabled
	previousBatchUpdateEnabled := common.BatchUpdateEnabled
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Redemption{}, &model.RedemptionBatchClaim{}, &model.User{}, &model.RegistrationIPClaim{}, &model.Log{}))
	model.DB, model.LOG_DB = db, db
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	common.RedisEnabled = false
	common.BatchUpdateEnabled = false
	require.NoError(t, db.Create(&model.User{
		Id:       2,
		Username: "admin",
		Password: "password",
		Role:     common.RoleRootUser,
		Status:   common.UserStatusEnabled,
		AffCode:  "redemption-batch-admin",
	}).Error)
	confirmPaymentComplianceForTest(t)

	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.SetDatabaseTypes(previousMainType, previousLogType)
		common.RedisEnabled = previousRedisEnabled
		common.BatchUpdateEnabled = previousBatchUpdateEnabled
		sqlDB, closeErr := db.DB()
		if closeErr == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

func performAddRedemptionRequest(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/redemption/", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("id", 2)
	c.Set("username", "admin")
	c.Set("role", common.RoleRootUser)
	AddRedemption(c)
	return recorder
}

func TestAddRedemptionAssignsOnePolicyToTheWholeBatch(t *testing.T) {
	db := setupRedemptionBatchControllerTest(t)
	recorder := performAddRedemptionRequest(t, `{
		"name":"batch-policy",
		"type":"quota",
		"quota":500,
		"count":3,
		"expired_time":0,
		"allow_same_user":true
	}`)

	assert.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Success bool     `json:"success"`
		Data    []string `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success)
	require.Len(t, response.Data, 3)

	var redemptions []model.Redemption
	require.NoError(t, db.Order("id asc").Find(&redemptions).Error)
	require.Len(t, redemptions, 3)
	require.NotEmpty(t, redemptions[0].BatchId)
	for _, redemption := range redemptions {
		assert.Equal(t, redemptions[0].BatchId, redemption.BatchId)
		assert.True(t, redemption.AllowSameUser)
	}
}

func TestAddRedemptionDefaultsToOneCodePerAccountForNewBatches(t *testing.T) {
	db := setupRedemptionBatchControllerTest(t)
	recorder := performAddRedemptionRequest(t, `{
		"name":"batch-default",
		"type":"welfare",
		"quota":500,
		"count":2,
		"expired_time":0
	}`)

	assert.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Success bool `json:"success"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success)

	var redemptions []model.Redemption
	require.NoError(t, db.Order("id asc").Find(&redemptions).Error)
	require.Len(t, redemptions, 2)
	require.NotEmpty(t, redemptions[0].BatchId)
	assert.False(t, redemptions[0].AllowSameUser)
	assert.False(t, redemptions[1].AllowSameUser)
}

func TestInsertRedemptionsRollsBackWholeBatchOnConstraintFailure(t *testing.T) {
	db := setupRedemptionBatchControllerTest(t)
	duplicateKey := common.GetUUID()
	redemptions := []model.Redemption{
		{Name: "atomic-batch", Key: duplicateKey, Status: common.RedemptionCodeStatusEnabled, Quota: 100, Type: model.RedemptionTypeQuota},
		{Name: "atomic-batch", Key: duplicateKey, Status: common.RedemptionCodeStatusEnabled, Quota: 100, Type: model.RedemptionTypeQuota},
	}

	require.Error(t, model.InsertRedemptions(redemptions))
	var count int64
	require.NoError(t, db.Model(&model.Redemption{}).Count(&count).Error)
	assert.Zero(t, count)
}

func TestExportRedemptionsStreamsAllFilteredKeys(t *testing.T) {
	db := setupRedemptionBatchControllerTest(t)
	redemptions := []model.Redemption{
		{Name: "export-match", Key: common.GetUUID(), Status: common.RedemptionCodeStatusEnabled, Quota: 100, Type: model.RedemptionTypeQuota},
		{Name: "export-match", Key: common.GetUUID(), Status: common.RedemptionCodeStatusEnabled, Quota: 100, Type: model.RedemptionTypeQuota},
		{Name: "other", Key: common.GetUUID(), Status: common.RedemptionCodeStatusEnabled, Quota: 100, Type: model.RedemptionTypeQuota},
	}
	require.NoError(t, db.Create(&redemptions).Error)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/redemption/export?keyword=export-match", nil)
	ExportRedemptions(c)

	assert.Equal(t, http.StatusOK, recorder.Code)
	lines := strings.Fields(recorder.Body.String())
	require.Len(t, lines, 2)
	assert.ElementsMatch(t, []string{redemptions[0].Key, redemptions[1].Key}, lines)
}

func TestTopUpLockEntryIsReleasedAfterLastReference(t *testing.T) {
	topUpCreateLock.Lock()
	topUpLocks = make(map[int]*topUpLockEntry)
	topUpCreateLock.Unlock()

	first, releaseFirst := getTopUpLock(88)
	second, releaseSecond := getTopUpLock(88)
	require.Same(t, first, second)
	require.True(t, first.TryLock())
	assert.False(t, second.TryLock())
	releaseSecond()
	first.Unlock()
	releaseFirst()

	topUpCreateLock.Lock()
	_, exists := topUpLocks[88]
	topUpCreateLock.Unlock()
	assert.False(t, exists)
}

func TestAddWelfareRedemptionStoresBalanceExpiration(t *testing.T) {
	db := setupRedemptionBatchControllerTest(t)
	codeExpiresAt := common.GetTimestamp() + 3600
	welfareExpiresAt := codeExpiresAt + 3600
	body := fmt.Sprintf(`{
		"name":"timed-welfare",
		"type":"welfare",
		"quota":500,
		"count":2,
		"expired_time":%d,
		"welfare_expires_at":%d
	}`, codeExpiresAt, welfareExpiresAt)
	recorder := performAddRedemptionRequest(t, body)

	assert.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Success bool `json:"success"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success)

	var redemptions []model.Redemption
	require.NoError(t, db.Order("id asc").Find(&redemptions).Error)
	require.Len(t, redemptions, 2)
	for _, redemption := range redemptions {
		require.NotNil(t, redemption.WelfareExpiresAt)
		assert.Equal(t, welfareExpiresAt, *redemption.WelfareExpiresAt)
	}
}

func TestAddWelfareRedemptionRejectsBalanceExpirationBeforeCodeExpiration(t *testing.T) {
	db := setupRedemptionBatchControllerTest(t)
	welfareExpiresAt := common.GetTimestamp() + 3600
	codeExpiresAt := welfareExpiresAt + 3600
	body := fmt.Sprintf(`{
		"name":"invalid-timed-welfare",
		"type":"welfare",
		"quota":500,
		"count":2,
		"expired_time":%d,
		"welfare_expires_at":%d
	}`, codeExpiresAt, welfareExpiresAt)
	recorder := performAddRedemptionRequest(t, body)

	assert.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.False(t, response.Success)
	assert.NotEmpty(t, response.Message)

	var count int64
	require.NoError(t, db.Model(&model.Redemption{}).Count(&count).Error)
	assert.Zero(t, count)
}

func TestTopUpExplainsWhenAccountAlreadyRedeemedFromBatch(t *testing.T) {
	setupRedemptionBatchControllerTest(t)
	require.NoError(t, i18n.Init())
	batchId := common.GetUUID()
	redemptions := []model.Redemption{
		{
			Name:        "limited-batch-first",
			Key:         common.GetUUID(),
			Status:      common.RedemptionCodeStatusEnabled,
			Quota:       100,
			Type:        model.RedemptionTypeQuota,
			BatchId:     batchId,
			CreatedTime: common.GetTimestamp(),
		},
		{
			Name:        "limited-batch-second",
			Key:         common.GetUUID(),
			Status:      common.RedemptionCodeStatusEnabled,
			Quota:       100,
			Type:        model.RedemptionTypeQuota,
			BatchId:     batchId,
			CreatedTime: common.GetTimestamp(),
		},
	}
	require.NoError(t, model.DB.Create(&redemptions).Error)
	_, err := model.RedeemWithResult(redemptions[0].Key, 2)
	require.NoError(t, err)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/user/topup", strings.NewReader(`{"key":"`+redemptions[1].Key+`"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("Accept-Language", "zh-CN")
	c.Set("id", 2)
	TopUp(c)

	assert.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Success    bool   `json:"success"`
		Message    string `json:"message"`
		MessageKey string `json:"message_key"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.False(t, response.Success)
	assert.Equal(t, "Each account can redeem only once from this batch", response.Message)
	assert.Equal(t, "Each account can redeem only once from this batch", response.MessageKey)
}

func TestDeleteRedemptionBatch(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			var driver, logDriver gorm.Dialector
			dbType := common.DatabaseTypeSQLite
			switch dialect {
			case "sqlite":
				driver = sqlite.Open(":memory:")
				logDriver = sqlite.Open(":memory:")
			case "mysql":
				dsn := os.Getenv("TEST_MYSQL_DSN")
				if dsn == "" {
					t.Skip("TEST_MYSQL_DSN is not configured")
				}
				driver = mysql.Open(dsn)
				logDSN := os.Getenv("TEST_MYSQL_LOG_DSN")
				if logDSN == "" {
					logDSN = dsn
				}
				logDriver = mysql.Open(logDSN)
				dbType = common.DatabaseTypeMySQL
			case "postgres":
				dsn := os.Getenv("TEST_POSTGRES_DSN")
				if dsn == "" {
					t.Skip("TEST_POSTGRES_DSN is not configured")
				}
				driver = postgres.Open(dsn)
				logDSN := os.Getenv("TEST_POSTGRES_LOG_DSN")
				if logDSN == "" {
					logDSN = dsn
				}
				logDriver = postgres.Open(logDSN)
				dbType = common.DatabaseTypePostgreSQL
			}
			db, err := gorm.Open(driver, &gorm.Config{})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			sqlDB.SetMaxOpenConns(1)
			t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
			var version string
			query := "SELECT version()"
			if dialect == "sqlite" {
				query = "SELECT sqlite_version()"
			}
			require.NoError(t, db.Raw(query).Scan(&version).Error)
			t.Logf("database version: %s", version)

			logDB, err := gorm.Open(logDriver, &gorm.Config{})
			require.NoError(t, err)
			logSQL, err := logDB.DB()
			require.NoError(t, err)
			logSQL.SetMaxOpenConns(1)
			t.Cleanup(func() { require.NoError(t, logSQL.Close()) })
			previousDB, previousLogDB := model.DB, model.LOG_DB
			previousMain, previousLog := common.MainDatabaseType(), common.LogDatabaseType()
			previousRedis := common.RedisEnabled
			model.DB, model.LOG_DB = db, logDB
			common.SetDatabaseTypes(dbType, dbType)
			common.RedisEnabled = false
			t.Cleanup(func() {
				model.DB, model.LOG_DB = previousDB, previousLogDB
				common.SetDatabaseTypes(previousMain, previousLog)
				common.RedisEnabled = previousRedis
			})
			for _, table := range []any{&model.User{}, &model.Redemption{}} {
				require.False(t, db.Migrator().HasTable(table), "use an empty test database")
				require.NoError(t, db.AutoMigrate(table))
				t.Cleanup(func() { require.NoError(t, db.Migrator().DropTable(table)) })
			}
			require.False(t, logDB.Migrator().HasTable(&model.AuditLog{}), "use an empty log database")
			require.NoError(t, logDB.AutoMigrate(&model.AuditLog{}))
			t.Cleanup(func() { require.NoError(t, logDB.Migrator().DropTable(&model.AuditLog{})) })
			token := "redemption-audit-test-token"
			admin := model.User{Username: "redemption-audit-admin", Password: "unused", Role: common.RoleAdminUser, Status: common.UserStatusEnabled, Group: "default", AccessToken: &token}
			require.NoError(t, db.Create(&admin).Error)
			codes := make([]model.Redemption, 16)
			for index := range codes {
				codes[index] = model.Redemption{Name: "selected", Key: fmt.Sprintf("%032d", index+1), Quota: 100, Status: common.RedemptionCodeStatusEnabled}
			}
			codes[1].Status = common.RedemptionCodeStatusUsed
			codes[15].Name = "unselected"
			codes[15].Status = common.RedemptionCodeStatusDisabled
			require.NoError(t, model.DB.Create(&codes).Error)
			router := gin.New()
			router.Use(middleware.RequestId())
			router.POST("/api/redemption/batch", middleware.AdminAuth(), DeleteRedemptionBatch)

			overLimit := make([]int, 1001)
			for index := range overLimit {
				overLimit[index] = codes[0].Id
			}
			oversized, err := common.Marshal(map[string]any{"ids": overLimit})
			require.NoError(t, err)
			for _, body := range []string{"{}", `{"ids":[]}`, `{"ids":null}`, `{"ids":[0]}`, `{"ids":[1,-1]}`, `{"ids":["1"]}`, "{", string(oversized)} {
				t.Run("invalid_"+body[:min(len(body), 30)], func(t *testing.T) {
					response := httptest.NewRecorder()
					request := httptest.NewRequest(http.MethodPost, "/api/redemption/batch", bytes.NewBufferString(body))
					request.Header.Set("Authorization", "Bearer "+token)
					router.ServeHTTP(response, request)
					var result struct {
						Success bool `json:"success"`
					}
					require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
					assert.False(t, result.Success)
					var count int64
					require.NoError(t, model.DB.Model(&model.Redemption{}).Count(&count).Error)
					assert.EqualValues(t, 16, count)
					var events []model.AuditLog
					require.NoError(t, logDB.Where("request_id = ? AND category = ?", response.Header().Get(common.RequestIdKey), model.AuditCategoryOperation).Find(&events).Error)
					require.Len(t, events, 1)
					assert.False(t, events[0].Success)
					assert.Equal(t, "redemption.delete_batch", events[0].Action)
				})
			}
			_, err = model.BatchDeleteRedemptions(nil)
			require.Error(t, err)
			requestedIDs := make([]int, 0, 17)
			for _, code := range codes[:15] {
				requestedIDs = append(requestedIDs, code.Id)
			}
			requestedIDs = append(requestedIDs, codes[0].Id, 999999)
			payload, err := common.Marshal(map[string]any{"ids": requestedIDs})
			require.NoError(t, err)
			for _, expectedCount := range []int64{15, 0} {
				response := httptest.NewRecorder()
				request := httptest.NewRequest(http.MethodPost, "/api/redemption/batch", bytes.NewReader(payload))
				request.Header.Set("Authorization", "Bearer "+token)
				router.ServeHTTP(response, request)
				assert.Equal(t, http.StatusOK, response.Code)
				var result struct {
					Success bool  `json:"success"`
					Data    int64 `json:"data"`
				}
				require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
				assert.True(t, result.Success)
				assert.Equal(t, expectedCount, result.Data)
				var events []model.AuditLog
				require.NoError(t, logDB.Where("request_id = ? AND category = ?", response.Header().Get(common.RequestIdKey), model.AuditCategoryOperation).Find(&events).Error)
				require.Len(t, events, 1, "one operation event, without a duplicate single-delete fallback")
				event := events[0]
				assert.Equal(t, "redemption.delete_batch", event.Action)
				assert.Equal(t, fmt.Sprintf("Batch deleted %d redemption codes", expectedCount), event.Content)
				assert.True(t, event.Success)
				assert.Equal(t, admin.Id, event.UserId)
				assert.Equal(t, "/api/redemption/batch", event.Route)
				require.NotNil(t, event.Other.Op)
				encoded, err := common.Marshal(event.Other.Op.Params)
				require.NoError(t, err)
				var params struct {
					Count int64 `json:"count"`
					Total int   `json:"total"`
					IDs   []int `json:"requested_redemption_ids"`
				}
				require.NoError(t, common.Unmarshal(encoded, &params))
				assert.Equal(t, expectedCount, params.Count)
				assert.Equal(t, len(requestedIDs), params.Total)
				assert.Equal(t, requestedIDs, params.IDs)
				encoded, err = common.Marshal(event)
				require.NoError(t, err)
				assert.NotContains(t, string(encoded), token)
				for _, code := range codes {
					assert.NotContains(t, string(encoded), code.Key)
				}
			}
			var active []model.Redemption
			require.NoError(t, model.DB.Find(&active).Error)
			require.Len(t, active, 1)
			assert.Equal(t, codes[15], active[0])
			var all []model.Redemption
			require.NoError(t, model.DB.Unscoped().Order("id").Find(&all).Error)
			require.Len(t, all, 16)
			for _, code := range all[:15] {
				assert.True(t, code.DeletedAt.Valid)
			}
			assert.False(t, all[15].DeletedAt.Valid)
		})
	}
}
