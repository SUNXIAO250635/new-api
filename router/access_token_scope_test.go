package router

import (
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// accessTokenExemptRoutes are /api routes that never reach dashboard
// authentication, so a personal access token has no rule to satisfy there.
var accessTokenExemptRoutes = []string{
	// Anonymous pages and setup.
	"GET /api/setup",
	"POST /api/setup",
	"GET /api/status",
	"GET /api/uptime/status",
	"GET /api/notice",
	"GET /api/user-agreement",
	"GET /api/privacy-policy",
	"GET /api/about",
	"GET /api/home_page_content",
	"GET /api/ratio_config",
	"GET /api/user/groups",

	// Anonymous account entry points.
	"GET /api/verification",
	"GET /api/reset_password",
	"POST /api/user/reset",
	"POST /api/user/register",
	"GET /api/user/login/encryption-key",
	"POST /api/user/login",
	"POST /api/user/login/2fa",
	"POST /api/user/login/verify",
	"POST /api/user/login/passkey/begin",
	"POST /api/user/login/passkey/finish",
	"POST /api/user/passkey/login/begin",
	"POST /api/user/passkey/login/finish",
	"GET /api/oauth/wechat",
	"GET /api/oauth/telegram/login",
	"GET /api/oauth/telegram/bind/:flow_token",

	// Browser session cookie endpoints.
	"POST /api/user/auth/refresh",
	"POST /api/user/auth/logout",

	// Payment callbacks.
	"POST /api/stripe/webhook",
	"POST /api/creem/webhook",
	"POST /api/waffo/webhook",
	"POST /api/waffo-pancake/webhook/:env",
	"GET /api/user/epay/notify",
	"POST /api/user/epay/notify",
	"GET /api/subscription/epay/notify",
	"POST /api/subscription/epay/notify",
	"GET /api/subscription/epay/return",
	"POST /api/subscription/epay/return",

	// API key authentication (TokenAuthReadOnly).
	"GET /api/usage/token/",
	"GET /api/log/token",
}

// accessTokenHelperRoutes are declared through handlePermissionRoute outside
// channelPermissionRoutes.
var accessTokenHelperRoutes = map[string]authz.Permission{
	"GET /api/task_plugin_options": authz.TaskPluginBind,
	"GET /api/audit":               authz.AuditRead,
}

func newAccessTokenScopeTestEngine(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	// A permission route that is also in the static table panics here.
	require.NotPanics(t, func() {
		SetApiRouter(engine)
		SetDashboardRouter(engine)
		SetRelayRouter(engine)
		SetTaskPluginProtocolRouter(engine)
		SetVideoRouter(engine)
		SetTaskRouter(engine)
	})
	return engine
}

func TestAccessTokenRouteRulesCoverEveryDashboardRoute(t *testing.T) {
	engine := newAccessTokenScopeTestEngine(t)
	exempt := map[string]bool{}
	for _, key := range accessTokenExemptRoutes {
		exempt[key] = true
	}
	registered := map[string]bool{}
	panel := map[string]bool{}
	for _, route := range engine.Routes() {
		if !strings.HasPrefix(route.Path, "/api/") && !strings.HasPrefix(route.Path, "/pg/") {
			continue
		}
		key := route.Method + " " + route.Path
		registered[key] = true
		if !exempt[key] {
			panel[key] = true
		}
	}
	for _, key := range accessTokenExemptRoutes {
		assert.True(t, registered[key], "exempt route %s is no longer registered", key)
	}

	declared := map[string]int{}
	for _, key := range middleware.AccessTokenRouteRuleKeys() {
		declared[key]++
	}
	for key := range panel {
		assert.Equal(t, 1, declared[key], "panel route %s must be declared exactly once", key)
	}
	for key := range declared {
		assert.True(t, panel[key], "declared route %s is not a registered panel route", key)
	}

	permissionRoutes := map[string]authz.Permission{}
	for _, route := range channelPermissionRoutes {
		permissionRoutes[route.method+" "+joinPaths("/api/channel", route.path)] = route.permission
	}
	for key, permission := range accessTokenHelperRoutes {
		permissionRoutes[key] = permission
	}
	for key, permission := range permissionRoutes {
		rule, ok := middleware.AccessTokenRouteRule(key)
		require.True(t, ok, key)
		assert.Equal(t, "scope", rule.Kind(), key)
		assert.Equal(t, service.AccessTokenScopeOf(permission), rule.Scope(), key)
	}
}

func TestAccessTokenCatalogCoversEveryPermission(t *testing.T) {
	catalog := service.AccessTokenCatalog(1, common.RoleRootUser)
	scopes := map[string]bool{}
	resources := map[string]int{}
	for _, group := range catalog {
		for _, resource := range group.Resources {
			resources[resource.Resource]++
			for _, action := range resource.Actions {
				scopes[resource.Resource+":"+action.Action] = true
			}
		}
	}
	for _, permission := range authz.AllPermissions() {
		assert.True(t, scopes[service.AccessTokenScopeOf(permission)], "root catalog lacks %s", service.AccessTokenScopeOf(permission))
	}
	for resource, count := range resources {
		assert.Equal(t, 1, count, "resource %s appears in more than one catalog entry", resource)
	}
}

func TestAccessTokenVerificationScopesAreAllListed(t *testing.T) {
	files, err := filepath.Glob("../service/*.go")
	require.NoError(t, err)
	fileSet := token.NewFileSet()
	var scopes []string
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(fileSet, file, nil, 0)
		require.NoError(t, err)
		for _, declaration := range parsed.Decls {
			general, ok := declaration.(*ast.GenDecl)
			if !ok || general.Tok != token.CONST {
				continue
			}
			for _, spec := range general.Specs {
				value := spec.(*ast.ValueSpec)
				for i, name := range value.Names {
					if !strings.HasPrefix(name.Name, "VerificationScope") || i >= len(value.Values) {
						continue
					}
					literal, ok := value.Values[i].(*ast.BasicLit)
					require.True(t, ok, name.Name)
					scope, err := strconv.Unquote(literal.Value)
					require.NoError(t, err)
					scopes = append(scopes, scope)
				}
			}
		}
	}
	require.NotEmpty(t, scopes)
	for _, scope := range scopes {
		_, listed := service.AccessTokenVerificationScope(scope)
		assert.True(t, listed, "verification scope %s has no access token rule", scope)
	}
}

func TestModelRoutingAdminAccess(t *testing.T) {
	const path = "/api/model_routing"
	const valid = `{"enabled":false,"revision":0,"rules":[]}`
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.UserSession{}, &model.UserAccessToken{}, &model.Option{}, &model.AuditLog{}))
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousRedis, previousSecret := common.RedisEnabled, common.SessionSecret
	previousTypes, previousLogType := common.MainDatabaseType(), common.LogDatabaseType()
	model.DB, model.LOG_DB = db, db
	common.RedisEnabled, common.SessionSecret = false, "model-routing-admin-test-secret"
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.RedisEnabled, common.SessionSecret = previousRedis, previousSecret
		common.SetDatabaseTypes(previousTypes, previousLogType)
		assert.NoError(t, sqlDB.Close())
	})
	admin := model.User{Username: "routing-admin", Role: common.RoleAdminUser, Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1, AffCode: "rtadmin"}
	root := model.User{Username: "routing-root", Role: common.RoleRootUser, Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1, AffCode: "rtroot"}
	require.NoError(t, db.Create(&admin).Error)
	require.NoError(t, db.Create(&root).Error)
	adminSession, err := service.CreateLoginSession(admin.Id, "password", "127.0.0.1", "")
	require.NoError(t, err)
	rootSession, err := service.CreateLoginSession(root.Id, "password", "127.0.0.1", "")
	require.NoError(t, err)
	suffix, err := common.GenerateRandomCharsKey(43)
	require.NoError(t, err)
	pat := model.AccessTokenPrefix + suffix
	token := &model.UserAccessToken{Name: "routing token", TokenHash: model.AccessTokenFingerprint(pat), TokenHint: model.AccessTokenHint(pat)}
	require.NoError(t, token.SetScopes([]string{"option:read", "option:write"}))
	require.NoError(t, model.CreateUserAccessToken(root.Id, token, service.AccessTokenMaxPerUser))
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetApiRouter(engine)
	for _, tc := range []struct {
		name, method, bearer, body string
		cookie                     bool
		status                     int
	}{
		{name: "anonymous read", method: http.MethodGet, status: http.StatusUnauthorized},
		{name: "anonymous write", method: http.MethodPut, body: valid, status: http.StatusUnauthorized},
		{name: "admin read", method: http.MethodGet, bearer: adminSession.AccessToken, status: http.StatusForbidden},
		{name: "admin write", method: http.MethodPut, bearer: adminSession.AccessToken, body: valid, status: http.StatusForbidden},
		{name: "root PAT read", method: http.MethodGet, bearer: pat, status: http.StatusForbidden},
		{name: "root PAT write", method: http.MethodPut, bearer: pat, body: valid, status: http.StatusForbidden},
		{name: "cross-origin cookie-only write", method: http.MethodPut, body: valid, cookie: true, status: http.StatusUnauthorized},
		{name: "root read", method: http.MethodGet, bearer: rootSession.AccessToken, status: http.StatusOK},
		{name: "root malformed JSON", method: http.MethodPut, bearer: rootSession.AccessToken, body: valid + `{"ignored":true}`, status: http.StatusBadRequest},
		{name: "root invalid rules", method: http.MethodPut, bearer: rootSession.AccessToken, body: `{"enabled":true,"revision":0,"rules":[],"audit_marker":"must-not-be-audited"}`, status: http.StatusBadRequest},
		{name: "root stale revision", method: http.MethodPut, bearer: rootSession.AccessToken, body: `{"enabled":false,"revision":9,"rules":[]}`, status: http.StatusConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(tc.method, path, strings.NewReader(tc.body))
			request.Header.Set("Content-Type", "application/json")
			if tc.bearer != "" {
				request.Header.Set("Authorization", "Bearer "+tc.bearer)
			}
			if tc.cookie {
				request.Header.Set("Origin", "https://other.example")
				request.AddCookie(&http.Cookie{Name: service.RefreshCookieName, Value: rootSession.RefreshToken})
			}
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			require.Equal(t, tc.status, response.Code, response.Body.String())
			if tc.status == http.StatusOK {
				assert.Contains(t, response.Body.String(), `"rules":[]`)
			}
		})
	}
	var entries []model.AuditLog
	require.NoError(t, db.Where("route = ? AND category = ?", path, model.AuditCategoryOperation).Find(&entries).Error)
	require.Len(t, entries, 3)
	for _, entry := range entries {
		assert.Equal(t, root.Id, entry.UserId)
		assert.False(t, entry.Success)
		encoded, err := common.Marshal(entry)
		require.NoError(t, err)
		assert.NotContains(t, string(encoded), "must-not-be-audited")
		assert.NotContains(t, string(encoded), rootSession.AccessToken)
	}
}
