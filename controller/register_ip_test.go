package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupRegisterIpTestState(t *testing.T) *gorm.DB {
	t.Helper()
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousRedisEnabled := common.RedisEnabled
	previousBatchUpdateEnabled := common.BatchUpdateEnabled
	previousRegisterEnabled := common.RegisterEnabled
	previousPasswordRegisterEnabled := common.PasswordRegisterEnabled
	previousEmailVerificationEnabled := common.EmailVerificationEnabled
	previousForceIpLogEnabled := common.ForceIpLogEnabled
	previousRestrictSameIpRegistrationEnabled := common.RestrictSameIpRegistrationEnabled
	previousGenerateDefaultToken := constant.GenerateDefaultToken
	previousQuotaForNewUser := common.QuotaForNewUser

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.RegistrationIPClaim{}, &model.Log{}))
	model.DB, model.LOG_DB = db, db
	common.RedisEnabled = false
	common.BatchUpdateEnabled = false
	common.RegisterEnabled = true
	common.PasswordRegisterEnabled = true
	common.EmailVerificationEnabled = false
	common.RestrictSameIpRegistrationEnabled = false
	constant.GenerateDefaultToken = false
	common.QuotaForNewUser = 0

	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.RedisEnabled = previousRedisEnabled
		common.BatchUpdateEnabled = previousBatchUpdateEnabled
		common.RegisterEnabled = previousRegisterEnabled
		common.PasswordRegisterEnabled = previousPasswordRegisterEnabled
		common.EmailVerificationEnabled = previousEmailVerificationEnabled
		common.ForceIpLogEnabled = previousForceIpLogEnabled
		common.RestrictSameIpRegistrationEnabled = previousRestrictSameIpRegistrationEnabled
		constant.GenerateDefaultToken = previousGenerateDefaultToken
		common.QuotaForNewUser = previousQuotaForNewUser
		sqlDB, closeErr := db.DB()
		if closeErr == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

func performRegisterIpRequest(t *testing.T, username string, remoteAddr string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := fmt.Sprintf(`{"username":%q,"password":"register-password"}`, username)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/user/register", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.RemoteAddr = remoteAddr
	Register(c)
	return recorder
}

func TestRegisterStoresIpOnlyWhenAdministratorForcesIpLogging(t *testing.T) {
	db := setupRegisterIpTestState(t)

	common.ForceIpLogEnabled = true
	forcedResponse := performRegisterIpRequest(t, "forced-ip-user", "198.51.100.10:4321")
	assert.Equal(t, http.StatusOK, forcedResponse.Code)
	var forcedPayload struct {
		Success bool `json:"success"`
	}
	require.NoError(t, common.Unmarshal(forcedResponse.Body.Bytes(), &forcedPayload))
	assert.True(t, forcedPayload.Success)
	var forcedUser model.User
	require.NoError(t, db.Where("username = ?", "forced-ip-user").First(&forcedUser).Error)
	assert.Equal(t, "198.51.100.10", forcedUser.RegisterIp)

	common.ForceIpLogEnabled = false
	optionalResponse := performRegisterIpRequest(t, "optional-ip-user", "203.0.113.20:4321")
	assert.Equal(t, http.StatusOK, optionalResponse.Code)
	var optionalPayload struct {
		Success bool `json:"success"`
	}
	require.NoError(t, common.Unmarshal(optionalResponse.Body.Bytes(), &optionalPayload))
	assert.True(t, optionalPayload.Success)
	var optionalUser model.User
	require.NoError(t, db.Where("username = ?", "optional-ip-user").First(&optionalUser).Error)
	assert.Empty(t, optionalUser.RegisterIp)
}

func TestRegisterRejectsSecondAccountFromSameIpWhenRestricted(t *testing.T) {
	db := setupRegisterIpTestState(t)
	common.ForceIpLogEnabled = false
	common.RestrictSameIpRegistrationEnabled = true

	firstResponse := performRegisterIpRequest(t, "same-ip-first", "198.51.100.40:4321")
	assert.Equal(t, http.StatusOK, firstResponse.Code)
	var firstPayload struct {
		Success bool `json:"success"`
	}
	require.NoError(t, common.Unmarshal(firstResponse.Body.Bytes(), &firstPayload))
	require.True(t, firstPayload.Success)

	secondResponse := performRegisterIpRequest(t, "same-ip-second", "198.51.100.40:9876")
	assert.Equal(t, http.StatusOK, secondResponse.Code)
	var secondPayload struct {
		Success bool `json:"success"`
	}
	require.NoError(t, common.Unmarshal(secondResponse.Body.Bytes(), &secondPayload))
	assert.False(t, secondPayload.Success)

	var users []model.User
	require.NoError(t, db.Order("id asc").Find(&users).Error)
	require.Len(t, users, 1)
	assert.Equal(t, "198.51.100.40", users[0].RegisterIp)
}

func TestRegisterAllowsSameIpWhenRestrictionIsDisabled(t *testing.T) {
	db := setupRegisterIpTestState(t)
	common.ForceIpLogEnabled = false
	common.RestrictSameIpRegistrationEnabled = false

	firstResponse := performRegisterIpRequest(t, "open-ip-first", "203.0.113.50:4321")
	secondResponse := performRegisterIpRequest(t, "open-ip-second", "203.0.113.50:9876")
	assert.Equal(t, http.StatusOK, firstResponse.Code)
	assert.Equal(t, http.StatusOK, secondResponse.Code)

	var count int64
	require.NoError(t, db.Model(&model.User{}).Count(&count).Error)
	assert.EqualValues(t, 2, count)
}
