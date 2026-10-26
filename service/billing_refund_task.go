package service

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"

	"github.com/bytedance/gopkg/util/gopool"
)

const billingRefundRetryInterval = 30 * time.Second

var (
	billingRefundRetryOnce    sync.Once
	billingRefundRetryRunning atomic.Bool
)

func StartBillingRefundRetryTask() {
	billingRefundRetryOnce.Do(func() {
		if !common.IsMasterNode {
			return
		}
		gopool.Go(func() {
			runPendingBillingRefunds(context.Background(), 100)
			ticker := time.NewTicker(billingRefundRetryInterval)
			defer ticker.Stop()
			for range ticker.C {
				runPendingBillingRefunds(context.Background(), 100)
			}
		})
	})
}

func runPendingBillingRefunds(ctx context.Context, limit int) int {
	if !billingRefundRetryRunning.CompareAndSwap(false, true) {
		return 0
	}
	defer billingRefundRetryRunning.Store(false)

	records, err := model.GetPendingBillingRefunds(limit)
	if err != nil {
		logger.LogWarn(ctx, "load pending billing refunds failed: "+err.Error())
		return 0
	}
	completed := 0
	for _, record := range records {
		done, processErr := model.ProcessBillingRefund(record.RequestId)
		if processErr != nil {
			model.RecordBillingRefundFailure(record.RequestId, processErr)
			logger.LogWarn(ctx, "retry billing refund failed: request_id="+record.RequestId+" error="+processErr.Error())
			continue
		}
		if done {
			completed++
		}
	}
	return completed
}
