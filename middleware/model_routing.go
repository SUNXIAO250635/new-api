package middleware

import (
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"

	"github.com/MoeForge/moego/ojson"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relay/requestplugin"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

const modelRoutingContextKey = "model_routing_request"
const modelRoutingPolicyContextKey = "model_routing_policy"

// ModelRoutingRequest owns the ordered candidates and the unmodified client
// body for an explicitly routed request. It lives only until distribution ends.
type ModelRoutingRequest struct {
	original   common.BodyStorage
	body       []byte
	candidates []model.ModelRoutingCandidate
	selected   model.ModelRoutingCandidate
	prepared   common.BodyStorage
	group      string
	next       int
}

func CurrentModelRoutingRequest(c *gin.Context) *ModelRoutingRequest {
	route, _ := c.Get(modelRoutingContextKey)
	selection, _ := route.(*ModelRoutingRequest)
	return selection
}

func modelRoutingEndpoint(path string) string {
	switch path {
	case "/v1/chat/completions":
		return "chat"
	case "/v1/responses":
		return "responses"
	default:
		return ""
	}
}

func enabledModelRouting(c *gin.Context, name string) (*model.ModelRoutingPolicy, *types.NewAPIError) {
	if name != model.ModelRoutingAlias || c.Request.Method != http.MethodPost || modelRoutingEndpoint(c.Request.URL.Path) == "" {
		return nil, nil
	}
	if policy, checked := common.GetContextKeyType[*model.ModelRoutingPolicy](c, modelRoutingPolicyContextKey); checked {
		return policy, nil
	}
	policy := model.CurrentModelRouting()
	if !policy.Enabled {
		return nil, nil
	}
	conflict, err := model.ModelRoutingAliasConflict()
	if err != nil {
		return nil, modelRoutingError(c, http.StatusServiceUnavailable, i18n.MsgModelRoutingUnavailable)
	}
	if conflict {
		return nil, nil // A real model always keeps its original behavior.
	}
	return policy, nil
}

func modelRoutingError(c *gin.Context, status int, key string) *types.NewAPIError {
	code := types.ErrorCodeInvalidRequest
	if status == http.StatusForbidden {
		code = types.ErrorCodeModelNotFound
	} else if status == http.StatusServiceUnavailable {
		code = types.ErrorCodeGetChannelFailed
	}
	return types.NewErrorWithStatusCode(errors.New(i18n.T(c, key)), code, status, types.ErrOptionWithSkipRetry())
}

func newModelRoutingRequest(c *gin.Context, policy *model.ModelRoutingPolicy) (*ModelRoutingRequest, *types.NewAPIError) {
	if _, pinned, _ := service.GetChannelConstraints(c).ResolvedPin(); pinned {
		return nil, modelRoutingError(c, http.StatusBadRequest, i18n.MsgModelRoutingPinned)
	}
	storage, err := common.GetBodyStorage(c)
	if err != nil {
		return nil, modelRoutingError(c, invalidRequestStatus(err), i18n.MsgModelRoutingInvalidBody)
	}
	body, err := storage.Bytes()
	if err != nil {
		return nil, modelRoutingError(c, http.StatusBadRequest, i18n.MsgModelRoutingInvalidBody)
	}
	endpoint := modelRoutingEndpoint(c.Request.URL.Path)
	image, tools, messageID := modelRoutingInput(body, endpoint, model.ModelRoutingAlias)
	if messageID != "" {
		return nil, modelRoutingError(c, http.StatusBadRequest, messageID)
	}
	for _, rule := range policy.Rules {
		if !matchesRoutingCondition(rule.Image, image) || !matchesRoutingCondition(rule.Tools, tools) {
			continue
		}
		limitEnabled := common.GetContextKeyBool(c, constant.ContextKeyTokenModelLimitEnabled)
		limit, _ := common.GetContextKeyType[map[string]bool](c, constant.ContextKeyTokenModelLimit)
		route := &ModelRoutingRequest{original: storage, body: body, group: common.GetContextKeyString(c, constant.ContextKeyUsingGroup)}
		eligible, authorized := false, false
		for _, candidate := range rule.Candidates {
			if !slices.Contains(candidate.Endpoints, endpoint) || image && !candidate.Vision || tools && !candidate.Tools {
				continue
			}
			eligible = true
			if limitEnabled && !TokenModelLimitAllows(limit, candidate.Model) {
				continue
			}
			authorized = true
			if helper.HasConfiguredRelayModelPrice(candidate.Model) {
				route.candidates = append(route.candidates, candidate)
			}
		}
		if len(route.candidates) != 0 {
			return route, nil
		}
		if eligible && !authorized {
			return nil, modelRoutingError(c, http.StatusForbidden, i18n.MsgModelRoutingForbidden)
		}
		break
	}
	return nil, modelRoutingError(c, http.StatusServiceUnavailable, i18n.MsgModelRoutingUnavailable)
}

// modelRoutingInput inspects structured fields, not matches in user text.
func modelRoutingInput(body []byte, endpoint, modelName string) (image, tools bool, messageID string) {
	if !ojson.ValidBytes(body) {
		return false, false, i18n.MsgModelRoutingInvalidBody
	}
	root := ojson.GetBytesNoCopy(body, "@this")
	if !root.IsObject() || root.Get("model").Str != modelName ||
		!unambiguousRoutingFields(root, "model", "messages", "input", "tools", "functions", "background", "previous_response_id", "conversation", "prompt") ||
		!unambiguousRoutingFields(root.Get("prompt"), "id") {
		return false, false, i18n.MsgModelRoutingInvalidBody
	}
	items, imageType := root.Get("messages"), "image_url"
	responses := endpoint == "responses"
	if responses {
		items, imageType = root.Get("input"), "input_image"
		conversation := root.Get("conversation")
		if root.Get("background").Bool() || root.Get("previous_response_id").Str != "" ||
			conversation.Exists() && conversation.Raw != "null" || root.Get("prompt.id").Str != "" {
			return false, false, i18n.MsgModelRoutingStateful
		}
	}
	stateful, ambiguous := false, false
	if items.IsArray() {
		items.ForEach(func(_, item ojson.Result) bool {
			if !unambiguousRoutingFields(item, "type", "content", "file_id") {
				ambiguous = true
				return false
			}
			typ := item.Get("type").Str
			image = image || typ == imageType
			if responses && (typ == "item_reference" || item.Get("file_id").Str != "") {
				stateful = true
				return false
			}
			if content := item.Get("content"); content.IsArray() {
				content.ForEach(func(_, part ojson.Result) bool {
					if !unambiguousRoutingFields(part, "type", "file_id") {
						ambiguous = true
						return false
					}
					image = image || part.Get("type").Str == imageType
					stateful = responses && part.Get("file_id").Str != ""
					return !stateful
				})
			}
			return !stateful && !ambiguous
		})
	}
	if ambiguous {
		return false, false, i18n.MsgModelRoutingInvalidBody
	}
	if stateful {
		return false, false, i18n.MsgModelRoutingStateful
	}
	return image, root.Get("tools.0").Exists() || root.Get("functions.0").Exists(), ""
}

// Reject duplicate and alternate-case routing-sensitive names. The JSON
// selector and typed upstream decoders can otherwise choose different values.
func unambiguousRoutingFields(object ojson.Result, fields ...string) bool {
	if !object.IsObject() {
		return true
	}
	var seen uint16
	valid := true
	object.ForEach(func(key, _ ojson.Result) bool {
		for index, field := range fields {
			if !strings.EqualFold(key.Str, field) {
				continue
			}
			bit := uint16(1) << index
			if seen&bit != 0 || key.Str != field {
				valid = false
				return false
			}
			seen |= bit
			break
		}
		return true
	})
	return valid
}

// ValidateSelectedRequest rejects ingress transformations that would make a
// selected model incompatible, without charging or sending the request.
func (route *ModelRoutingRequest) ValidateSelectedRequest(c *gin.Context) *types.NewAPIError {
	storage, err := common.GetBodyStorage(c)
	if err != nil {
		return modelRoutingError(c, invalidRequestStatus(err), i18n.MsgModelRoutingInvalidBody)
	}
	if storage == route.prepared {
		return nil
	}
	body, err := storage.Bytes()
	if err != nil {
		return modelRoutingError(c, http.StatusBadRequest, i18n.MsgModelRoutingInvalidBody)
	}
	image, tools, messageID := modelRoutingInput(body, modelRoutingEndpoint(c.Request.URL.Path), route.selected.Model)
	if messageID != "" {
		return modelRoutingError(c, http.StatusBadRequest, messageID)
	}
	if image && !route.selected.Vision || tools && !route.selected.Tools {
		return modelRoutingError(c, http.StatusBadRequest, i18n.MsgModelRoutingInvalidBody)
	}
	return nil
}

func matchesRoutingCondition(condition string, value bool) bool {
	return condition == "any" || condition == "present" && value || condition == "absent" && !value
}

// SelectNext changes the model/channel together and rebuilds the body from the
// untouched input. It never invokes ingress or sends a request. Empty selection
// means all eligible candidates lacked a channel in the current groups.
func (route *ModelRoutingRequest) SelectNext(c *gin.Context, eligibleGroup func(string) (bool, error)) (bool, *types.NewAPIError) {
	constraints := service.GetChannelConstraints(c)
	if _, pinned, _ := constraints.ResolvedPin(); pinned {
		return false, modelRoutingError(c, http.StatusBadRequest, i18n.MsgModelRoutingPinned)
	}
	var allowedGroups []string
	if eligibleGroup != nil {
		groups := []string{route.group}
		if route.group == "auto" {
			groups = service.GetRequestAutoGroups(c, common.GetContextKeyString(c, constant.ContextKeyUserGroup))
		}
		allowedGroups = make([]string, 0, len(groups))
		for _, group := range groups {
			allowed, err := eligibleGroup(group)
			if err != nil {
				return false, modelRoutingError(c, http.StatusServiceUnavailable, i18n.MsgModelRoutingUnavailable)
			}
			if allowed {
				allowedGroups = append(allowedGroups, group)
			}
		}
		if len(allowedGroups) == 0 {
			return false, nil
		}
	}
	for route.next < len(route.candidates) {
		if c.Request.Context().Err() != nil {
			return false, nil
		}
		candidate := route.candidates[route.next]
		name := candidate.Model
		route.next++
		service.ResetModelRoutingSelection(c)
		common.SetContextKey(c, constant.ContextKeyUsingGroup, route.group)
		body, patchErr := ojson.SetBytes(route.body, "model", name)
		patched := ojson.GetBytesNoCopy(body, "@this")
		if patchErr != nil || patched.Get("model").Str != name || !unambiguousRoutingFields(patched, "model") {
			return false, modelRoutingError(c, http.StatusBadRequest, i18n.MsgModelRoutingInvalidBody)
		}
		if requestplugin.OverBodyLimit(body) {
			return false, modelRoutingError(c, http.StatusRequestEntityTooLarge, i18n.MsgModelRoutingInvalidBody)
		}
		storage, storeErr := common.CreateBodyStorage(body)
		if storeErr != nil {
			return false, modelRoutingError(c, http.StatusServiceUnavailable, i18n.MsgModelRoutingUnavailable)
		}
		if current, _ := c.Get(common.KeyBodyStorage); current != route.original {
			common.CleanupBodyStorage(c)
		}
		c.Set(common.KeyBodyStorage, storage)
		c.Set(common.KeyRequestBody, nil)
		c.Request.Body = io.NopCloser(storage)
		c.Request.ContentLength = int64(len(body))
		filterCount := len(constraints.Filters)
		constraints.Filters = appendModelRoutingEndpointFilter(constraints.Filters, c.Request.URL.Path, name)
		channel, _, err := service.SelectChannelForRequest(c, name, &service.RetryParam{
			Ctx: c, ModelName: name, TokenGroup: route.group, RequestPath: c.Request.URL.Path, EligibleAutoGroups: allowedGroups,
		})
		constraints.Filters = constraints.Filters[:filterCount]
		if err != nil {
			if RequestAffinityIsStrict(c) {
				return false, modelRoutingError(c, http.StatusServiceUnavailable, i18n.MsgModelRoutingUnavailable)
			}
			continue
		}
		if channel == nil {
			continue
		}
		for _, key := range []string{"api_version", "region", "plugin", "bot_id", "task_plugin_key", string(constant.ContextKeyChannelOrganization), common.UpstreamRequestIdKey} {
			c.Set(key, nil)
		}
		if apiErr := SetupContextForSelectedChannel(c, channel, name); apiErr != nil {
			if apiErr.GetErrorCode() == types.ErrorCodeChannelNoAvailableKey && !RequestAffinityIsStrict(c) {
				continue
			}
			return false, modelRoutingError(c, http.StatusServiceUnavailable, i18n.MsgModelRoutingUnavailable)
		}
		route.selected, route.prepared = candidate, storage
		return true, nil
	}
	return false, nil
}

func appendModelRoutingEndpointFilter(filters []dto.ChannelFilter, path, modelName string) []dto.ChannelFilter {
	if path != "/v1/responses" {
		return filters
	}
	generation := jsplugin.DefaultRegistry.Generation()
	if generation == nil {
		return filters
	}
	endpoint, found := lookupTaskPluginEndpoint(generation, http.MethodPost, path, modelName)
	if !found {
		return filters
	}
	filter := dto.ChannelFilter{Kind: dto.FilterTaskPluginEndpoint, RequestPath: path, TaskPluginRelayOnly: true}
	for _, candidate := range endpoint.Candidates {
		filter.TaskPluginKeys = append(filter.TaskPluginKeys, candidate.Plugin.Meta.Key)
		filter.TaskPluginChannelTypes = append(filter.TaskPluginChannelTypes, candidate.Plugin.Meta.ChannelTypes...)
	}
	return append(filters, filter)
}

func (route *ModelRoutingRequest) close() {
	_ = route.original.Close()
}

func RequestAffinityIsStrict(c *gin.Context) bool {
	return service.RequestPolicy(c).SessionMode == "strict"
}

// ModelRoutingDiscoverable reports whether the token's ordinary model list
// contains at least one priced candidate. This is not a routing attempt.
func ModelRoutingDiscoverable(c *gin.Context, ordinaryModels, groups []string) bool {
	policy := model.CurrentModelRouting()
	if !policy.Enabled || len(ordinaryModels) == 0 || slices.Contains(ordinaryModels, model.ModelRoutingAlias) {
		return false
	}
	if _, pinned, _ := service.GetChannelConstraints(c).ResolvedPin(); pinned {
		return false
	}
	conflict, err := model.ModelRoutingAliasConflict()
	if err != nil || conflict {
		return false
	}
	for _, rule := range policy.Rules {
		for _, candidate := range rule.Candidates {
			if !slices.Contains(ordinaryModels, candidate.Model) || !helper.HasConfiguredRelayModelPrice(candidate.Model) {
				continue
			}
			for _, endpoint := range candidate.Endpoints {
				path := "/v1/chat/completions"
				if endpoint == "responses" {
					path = "/v1/responses"
				}
				filters := appendModelRoutingEndpointFilter([]dto.ChannelFilter{
					{Kind: dto.FilterRequestPath, RequestPath: path},
					{Kind: dto.FilterTaskPluginIdentity, RequestPath: path},
				}, path, candidate.Model)
				for _, group := range groups {
					channel, err := model.GetRandomSatisfiedChannel(group, candidate.Model, 0, filters)
					if err == nil && channel != nil {
						return true
					}
				}
			}
		}
	}
	return false
}
