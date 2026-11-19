package controller

import (
	"errors"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relay/requestplugin"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// prepareRelayInput parses the body after ingress. Model-scoped ingress checks
// run for each routed candidate against the untouched request, never against
// the previous model's edited body.
func prepareRelayInput(c *gin.Context, format types.RelayFormat, ws *websocket.Conn, route *middleware.ModelRoutingRequest) (*relaycommon.RelayInfo, []relaycommon.RequestPluginRecord, *types.NewAPIError) {
	var steps []relaycommon.RequestPluginStep
	var record []relaycommon.RequestPluginRecord
	var apiErr *types.NewAPIError
	if format == types.RelayFormatOpenAIRealtime {
		requestplugin.StartRealtime(c)
	} else if steps, record, apiErr = requestplugin.Ingress(c, format); apiErr != nil {
		info := &relaycommon.RelayInfo{}
		info.SetRequestPluginIngress(steps, record)
		if route != nil {
			info.RequestedModelName = model.ModelRoutingAlias
		}
		service.RecordRelayErrorLog(c, 0, apiErr, info)
		return nil, record, apiErr
	}
	if route != nil {
		if apiErr := route.ValidateSelectedRequest(c); apiErr != nil {
			return nil, record, apiErr
		}
	}
	request, err := helper.GetAndValidateRequest(c, format)
	if err != nil {
		if common.IsRequestBodyTooLargeError(err) || errors.Is(err, common.ErrRequestBodyTooLarge) {
			return nil, record, types.NewErrorWithStatusCode(err, types.ErrorCodeReadRequestBodyFailed, http.StatusRequestEntityTooLarge, types.ErrOptionWithSkipRetry())
		}
		return nil, record, types.NewError(err, types.ErrorCodeInvalidRequest, types.ErrOptionWithStatusCode(http.StatusBadRequest), types.ErrOptionWithSkipRetry())
	}
	info, err := relaycommon.GenRelayInfo(c, format, request, ws)
	if err != nil {
		return nil, record, types.NewError(err, types.ErrorCodeGenRelayInfoFailed)
	}
	info.SetRequestPluginIngress(steps, record)
	if route != nil {
		info.RequestedModelName = model.ModelRoutingAlias
		info.ForcePreConsume = true
	}
	return info, record, nil
}

func modelFallbackDecision(c *gin.Context, info *relaycommon.RelayInfo, err *types.NewAPIError, remaining int) service.PolicyDecision {
	decision := service.PolicyDecision{Action: "stop", Source: "model_routing"}
	switch {
	case c.Request.Context().Err() != nil:
		decision.Reason = "request_cancelled"
	case c.Writer.Written() || info.HasSendResponse():
		decision.Reason = "response_committed"
	case middleware.RequestAffinityIsStrict(c):
		decision.Reason = "strict_session"
	case remaining <= 0:
		decision.Reason = "attempt_budget_exhausted"
	case types.IsSkipRetryError(err) || service.RelayErrorSource(err) == "local":
		decision.Reason = "local_rejection"
	case err.StatusCode != http.StatusTooManyRequests && err.StatusCode/100 != 5:
		decision.Reason = "status_not_retryable"
	case info.RoutingUpstreamStatus != http.StatusTooManyRequests && info.RoutingUpstreamStatus/100 != 5:
		decision.Reason = "upstream_outcome_not_retryable"
	default:
		// A channel can remap errors for clients. Both the mapped and the
		// actual status must pass the server's retry policy.
		actual := *err
		actual.StatusCode = info.RoutingUpstreamStatus
		if service.DecideRelayRetry(c, &actual, remaining).Action != "retry" {
			decision.Reason = "upstream_outcome_not_retryable"
			return decision
		}
		decision.Action, decision.Reason = "retry", "model_fallback"
	}
	return decision
}
