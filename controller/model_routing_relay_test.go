package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModelRoutingRepricesFallbackBeforeForwarding(t *testing.T) {
	require.NoError(t, i18n.Init())
	user, token := setupResponsesWSRequestTest(t)
	db := model.DB
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Ability{}, &model.Option{}, &model.SystemTask{}))
	require.NoError(t, model.LOG_DB.AutoMigrate(&model.Log{}))
	t.Cleanup(func() { require.NoError(t, model.LOG_DB.Where("token_id = ?", token.Id).Delete(&model.Log{}).Error) })
	require.NoError(t, db.Model(user).Update("setting", `{"billing_preference":"wallet_only"}`).Error)
	require.NoError(t, db.Model(token).Update("remain_quota", 100).Error)
	previousRetry, previousBatch, previousLogs, previousCount, previousQuota := common.RetryTimes, common.BatchUpdateEnabled, common.LogConsumeEnabled, constant.CountToken, common.QuotaPerUnit
	common.RetryTimes, common.BatchUpdateEnabled, common.LogConsumeEnabled, constant.CountToken, common.QuotaPerUnit = 1, false, true, false, 500000
	previousStreamTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	common.OptionMapRWMutex.Lock()
	previousOptions := common.OptionMap
	common.OptionMap = make(map[string]string)
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.RetryTimes, common.BatchUpdateEnabled, common.LogConsumeEnabled, constant.CountToken, common.QuotaPerUnit = previousRetry, previousBatch, previousLogs, previousCount, previousQuota
		constant.StreamingTimeout = previousStreamTimeout
	})
	pricing := map[string]string{
		"billing_setting.billing_mode":    `{"route-primary":"tiered_expr","route-backup":"tiered_expr"}`,
		"billing_setting.billing_expr":    `{"route-primary":"tier(\"first\",fixed(0.00002))","route-backup":"tier(\"backup\",fixed(0.0001))"}`,
		"group_ratio_setting.group_ratio": `{"default":1}`,
	}
	saved := map[string]string{}
	require.NoError(t, config.GlobalConfig.SaveToDB(func(key, value string) error {
		if _, used := pricing[key]; used {
			saved[key] = value
		}
		return nil
	}))
	t.Cleanup(func() { require.NoError(t, config.GlobalConfig.LoadFromDB(saved)) })
	require.NoError(t, config.GlobalConfig.LoadFromDB(pricing))
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		common.OptionMap = previousOptions
		common.OptionMapRWMutex.Unlock()
	})
	previousPolicy := *model.CurrentModelRouting()
	primary := model.ModelRoutingCandidate{Model: "route-primary", Endpoints: []string{"chat", "responses"}, Vision: true}
	backup := model.ModelRoutingCandidate{Model: "route-backup", Endpoints: []string{"chat", "responses"}, Vision: true, Tools: true}
	policy := model.ModelRoutingPolicy{Enabled: true, Revision: previousPolicy.Revision + 1, Rules: []model.ModelRoutingRule{
		{Name: "images", Image: "present", Tools: "any", Candidates: []model.ModelRoutingCandidate{primary, backup}},
		{Name: "default", Image: "any", Tools: "any", Candidates: []model.ModelRoutingCandidate{primary, backup}},
	}}
	data, err := common.Marshal(policy)
	require.NoError(t, err)
	option := model.Option{Key: "model_routing.policy", Value: string(data)}
	require.NoError(t, db.Create(&option).Error)
	t.Cleanup(func() {
		previousPolicy.Revision = policy.Revision
		_, err := model.SaveModelRouting(previousPolicy)
		require.NoError(t, err)
		require.NoError(t, db.Delete(&option).Error)
	})
	require.NoError(t, model.LoadOptionsFromDatabase())
	service.InitHttpClient()
	var callsMu sync.Mutex
	var calls []string
	takeCalls := func() []string {
		callsMu.Lock()
		defer callsMu.Unlock()
		seen := calls
		calls = nil
		return seen
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Model  string `json:"model"`
			Stream bool   `json:"stream"`
			Input  string `json:"input"`
		}
		if !assert.NoError(t, common.DecodeJson(r.Body, &input)) {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		callsMu.Lock()
		calls = append(calls, input.Model)
		callsMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if input.Model == "route-primary" {
			switch input.Input {
			case "empty upstream stream":
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)
				return
			case "upstream credentials rejected":
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":{"message":"authentication failed","type":"invalid_request_error"}}`))
				return
			}
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"message":"rate limited","type":"rate_limit_error"}}`))
			return
		}
		var reserved model.Token
		assert.NoError(t, db.First(&reserved, token.Id).Error)
		assert.Equal(t, 50, reserved.RemainQuota, "fallback is priced and reserved before dispatch")
		if r.URL.Path == "/v1/responses" {
			response := `{"id":"resp_answer","object":"response","model":"route-backup","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`
			if input.Stream {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = w.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\ndata: {\"type\":\"response.completed\",\"response\":" + response + "}\n\n"))
			} else {
				_, _ = w.Write([]byte(response))
			}
			return
		}
		_, _ = w.Write([]byte(`{"id":"answer","object":"chat.completion","created":1,"model":"route-backup","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	t.Cleanup(upstream.Close)
	ch := &model.Channel{Name: "model-route-upstream", Key: "test-key", Status: common.ChannelStatusEnabled, Type: constant.ChannelTypeOpenAI, Group: "default", Models: "route-primary,route-backup", BaseURL: &upstream.URL}
	require.NoError(t, db.Create(ch).Error)
	for _, name := range []string{"route-primary", "route-backup"} {
		require.NoError(t, db.Create(&model.Ability{ChannelId: ch.Id, Model: name, Group: "default", Enabled: true}).Error)
	}
	t.Cleanup(func() {
		require.NoError(t, db.Where("channel_id = ?", ch.Id).Delete(&model.Ability{}).Error)
		require.NoError(t, db.Delete(ch).Error)
	})
	engine := gin.New()
	engine.Use(middleware.BodyStorageCleanup())
	engine.POST("/v1/chat/completions", middleware.TokenAuth(), middleware.Distribute(), func(c *gin.Context) { Relay(c, types.RelayFormatOpenAI) })
	engine.POST("/v1/responses", middleware.TokenAuth(), middleware.RouteTaskPluginEndpoint(), middleware.PrepareTaskPluginEndpoint(), middleware.Distribute(), func(c *gin.Context) {
		RelayTaskPluginEndpoint(c, func(c *gin.Context) { Relay(c, types.RelayFormatOpenAIResponses) })
	})
	engine.GET("/v1/models", middleware.TokenAuth(), func(c *gin.Context) { ListModels(c, constant.ChannelTypeOpenAI) })
	send := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Authorization", "Bearer sk-"+token.Key)
		engine.ServeHTTP(recorder, request)
		return recorder
	}
	const chat = `{"model":"auto","messages":[{"role":"user","content":"hello"}]}`
	const chatPath = "/v1/chat/completions"
	recorder := send(http.MethodPost, chatPath, chat)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	assert.Equal(t, []string{"route-primary", "route-backup"}, takeCalls())
	var actual model.User
	require.NoError(t, db.First(&actual, user.Id).Error)
	assert.Equal(t, 950, actual.Quota)
	var logs []model.Log
	require.NoError(t, model.LOG_DB.Where("token_id = ? AND type = ?", token.Id, model.LogTypeConsume).Find(&logs).Error)
	require.Len(t, logs, 1)
	assert.Equal(t, "route-backup", logs[0].ModelName)
	assert.Equal(t, 50, logs[0].Quota)
	assert.Contains(t, logs[0].Other, `"requested_model":"auto"`)

	assert.Contains(t, send(http.MethodGet, "/v1/models", "").Body.String(), `"id":"auto"`)
	t.Run("stateful Responses is rejected before dispatch", func(t *testing.T) {
		response := send(http.MethodPost, "/v1/responses", `{"model":"auto","input":"hello","previous_response_id":"resp_1"}`)
		assert.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
		response = send(http.MethodPost, "/v1/responses", `{"model":"auto","input":"hello","conversation":null,"conversation":"conv_fixture"}`)
		assert.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
		assert.Empty(t, takeCalls())
	})
	t.Run("Responses stream can switch before output", func(t *testing.T) {
		require.NoError(t, db.Model(token).Update("remain_quota", 100).Error)
		response := send(http.MethodPost, "/v1/responses", `{"model":"auto","input":"hello","stream":true}`)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		assert.Contains(t, response.Body.String(), `response.completed`)
		assert.Equal(t, []string{"route-primary", "route-backup"}, takeCalls())
	})
	t.Run("accepted stream with no output is not replayed", func(t *testing.T) {
		require.NoError(t, db.Model(token).Update("remain_quota", 100).Error)
		response := send(http.MethodPost, "/v1/responses", `{"model":"auto","input":"empty upstream stream","stream":true}`)
		assert.Equal(t, http.StatusBadGateway, response.Code, response.Body.String())
		assert.Equal(t, []string{"route-primary"}, takeCalls())
	})
	t.Run("remapped authentication rejection does not select another provider", func(t *testing.T) {
		require.NoError(t, db.Model(token).Update("remain_quota", 100).Error)
		require.NoError(t, db.Model(ch).Update("status_code_mapping", `{"401":"503"}`).Error)
		t.Cleanup(func() { require.NoError(t, db.Model(ch).Update("status_code_mapping", "").Error) })
		response := send(http.MethodPost, "/v1/responses", `{"model":"auto","input":"upstream credentials rejected"}`)
		assert.Equal(t, http.StatusServiceUnavailable, response.Code, response.Body.String())
		assert.Equal(t, []string{"route-primary"}, takeCalls())
	})
	t.Run("global retry budget can disallow fallback", func(t *testing.T) {
		savedRetries := common.RetryTimes
		common.RetryTimes = 0
		t.Cleanup(func() { common.RetryTimes = savedRetries })
		require.NoError(t, db.Model(token).Update("remain_quota", 100).Error)
		response := send(http.MethodPost, chatPath, chat)
		assert.Equal(t, http.StatusTooManyRequests, response.Code, response.Body.String())
		assert.Equal(t, []string{"route-primary"}, takeCalls())
	})
	t.Run("explicit model bypasses routing", func(t *testing.T) {
		require.NoError(t, db.Model(token).Update("remain_quota", 100).Error)
		response := send(http.MethodPost, chatPath, strings.Replace(chat, `"auto"`, `"route-backup"`, 1))
		assert.Equal(t, http.StatusOK, response.Code, response.Body.String())
		assert.Equal(t, []string{"route-backup"}, takeCalls())
	})
	t.Run("image and tools require both capabilities", func(t *testing.T) {
		require.NoError(t, db.Model(token).Update("remain_quota", 100).Error)
		response := send(http.MethodPost, chatPath, `{"model":"auto","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"https://example.com/image.png"}}]}],"tools":[{"type":"function","function":{"name":"lookup"}}]}`)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		assert.Equal(t, []string{"route-backup"}, takeCalls())
	})
	t.Run("alias cannot bypass token model limits", func(t *testing.T) {
		require.NoError(t, db.Model(token).Updates(map[string]any{"model_limits_enabled": true, "model_limits": "not-allowed"}).Error)
		assert.Equal(t, http.StatusForbidden, send(http.MethodPost, chatPath, chat).Code)
		assert.NotContains(t, send(http.MethodGet, "/v1/models", "").Body.String(), `"id":"auto"`)
		assert.Empty(t, takeCalls())
		require.NoError(t, db.Model(token).Updates(map[string]any{"model_limits_enabled": false, "model_limits": ""}).Error)
	})
	t.Run("failed backup reservation prevents a send and refunds primary", func(t *testing.T) {
		require.NoError(t, db.Model(token).Update("remain_quota", 15).Error)
		var before model.User
		require.NoError(t, db.First(&before, user.Id).Error)
		response := send(http.MethodPost, chatPath, chat)
		assert.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
		assert.Equal(t, []string{"route-primary"}, takeCalls())
		var after model.User
		require.NoError(t, db.First(&after, user.Id).Error)
		assert.Equal(t, before.Quota, after.Quota)
		var storedToken model.Token
		require.NoError(t, db.First(&storedToken, token.Id).Error)
		assert.Equal(t, 15, storedToken.RemainQuota)
	})
	t.Run("fallback does not spend a subscription in an excluded group", func(t *testing.T) {
		require.NoError(t, db.AutoMigrate(&model.SubscriptionPlan{}, &model.UserSubscription{}, &model.SubscriptionPreConsumeRecord{}, &model.SubscriptionConcurrencyLease{}))
		groups, autoGroups := setting.UserUsableGroups2JSONString(), setting.AutoGroups2JsonString()
		t.Cleanup(func() {
			require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(groups))
			require.NoError(t, setting.UpdateAutoGroupsByJsonString(autoGroups))
			require.NoError(t, config.GlobalConfig.LoadFromDB(pricing))
		})
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default","routing-backup":"Backup","auto":"Auto"}`))
		require.NoError(t, setting.UpdateAutoGroupsByJsonString(`["default","routing-backup"]`))
		require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{"group_ratio_setting.group_ratio": `{"default":1,"routing-backup":1}`}))
		require.NoError(t, db.Model(token).Updates(map[string]any{"group": "auto", "remain_quota": 100}).Error)
		require.NoError(t, db.Model(&model.Ability{}).Where("channel_id = ? AND model = ?", ch.Id, "route-backup").Update("group", "routing-backup").Error)
		require.NoError(t, db.Model(user).Update("setting", `{"billing_preference":"subscription_only"}`).Error)
		plan := model.SubscriptionPlan{Title: "Default only", TotalAmount: 1000}
		require.NoError(t, db.Create(&plan).Error)
		now := model.GetDBTimestamp()
		subscription := model.UserSubscription{UserId: user.Id, PlanId: plan.Id, Status: "active", StartTime: now, EndTime: now + 3600, UsageGroup: "default", AmountTotal: 1000}
		require.NoError(t, db.Create(&subscription).Error)
		t.Cleanup(func() {
			require.NoError(t, db.Where("user_subscription_id = ?", subscription.Id).Delete(&model.SubscriptionPreConsumeRecord{}).Error)
			require.NoError(t, db.Delete(&subscription).Error)
			require.NoError(t, db.Delete(&plan).Error)
			model.InvalidateSubscriptionPlanCache(plan.Id)
		})
		response := send(http.MethodPost, chatPath, chat)
		assert.Equal(t, http.StatusTooManyRequests, response.Code, response.Body.String())
		assert.Equal(t, []string{"route-primary"}, takeCalls())
		require.NoError(t, db.First(&subscription, subscription.Id).Error)
		assert.Zero(t, subscription.AmountUsed, "primary reservation was refunded")
		var storedToken model.Token
		require.NoError(t, db.First(&storedToken, token.Id).Error)
		assert.Equal(t, 100, storedToken.RemainQuota)
	})
}
