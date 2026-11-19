package controller

import (
	"errors"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

func GetModelRouting(c *gin.Context) {
	policy, err := model.LoadModelRouting()
	if err != nil {
		logger.LogError(c.Request.Context(), common.LogText("failed to load model routing policy: %v", err))
		common.ApiErrorStatus(c, http.StatusInternalServerError, common.NewMessage("Failed to load model routing settings"))
		return
	}
	common.ApiSuccess(c, policy)
}

func UpdateModelRouting(c *gin.Context) {
	var request struct {
		Enabled  *bool                    `json:"enabled"`
		Revision *int64                   `json:"revision"`
		Rules    []model.ModelRoutingRule `json:"rules"`
	}
	body := http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
	if err := common.DecodeJsonBody(body, &request); err != nil || request.Enabled == nil || request.Revision == nil || request.Rules == nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid model routing request"))
		return
	}
	policy, err := model.SaveModelRouting(model.ModelRoutingPolicy{
		Enabled: *request.Enabled, Revision: *request.Revision, Rules: request.Rules,
	})
	var message *common.Message
	switch {
	case err == nil:
		common.ApiSuccess(c, policy)
	case errors.Is(err, model.ErrModelRoutingConflict):
		common.ApiErrorStatus(c, http.StatusConflict, err)
	case errors.As(err, &message):
		common.ApiErrorStatus(c, http.StatusBadRequest, message)
	default:
		logger.LogError(c.Request.Context(), common.LogText("failed to save model routing policy: %v", err))
		common.ApiErrorStatus(c, http.StatusInternalServerError, common.NewMessage("Failed to save model routing settings"))
	}
}
