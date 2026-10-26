package service

import (
	"errors"
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTaskErrorFromAPIErrorPreservesSkipRetry(t *testing.T) {
	apiErr := types.NewErrorWithStatusCode(
		errors.New("subscription concurrency limit reached"),
		types.ErrorCodeUpdateDataError,
		http.StatusTooManyRequests,
		types.ErrOptionWithSkipRetry(),
	)

	taskErr := TaskErrorFromAPIError(apiErr)
	require.NotNil(t, taskErr)
	assert.True(t, taskErr.SkipRetry)
	assert.Equal(t, http.StatusTooManyRequests, taskErr.StatusCode)
	assert.Equal(t, "subscription concurrency limit reached", taskErr.Message)
}
