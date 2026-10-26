package controller

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	taskdto "github.com/QuantumNous/new-api/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestTaskRelayDoesNotRetryOrRewriteSkipRetry429(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	taskErr := &taskdto.TaskError{
		Code:       "update_data_error",
		Message:    "subscription concurrency limit reached",
		StatusCode: http.StatusTooManyRequests,
		SkipRetry:  true,
		Error:      errors.New("subscription concurrency limit reached"),
	}

	decision := decideTaskRetry(ctx, taskErr, 1)
	assert.Equal(t, "stop", decision.Action)
	respondTaskError(ctx, taskErr)
	assert.Equal(t, "subscription concurrency limit reached", taskErr.Message)
	assert.Contains(t, recorder.Body.String(), "subscription concurrency limit reached")
	// The retry path must surface the original error rather than the canned group message.
	taskAPIError := taskSubmissionAPIError(taskErr)
	assert.Equal(t, taskErr.StatusCode, taskAPIError.StatusCode)
	assert.Equal(t, "subscription concurrency limit reached", taskAPIError.Err.Error())
}
