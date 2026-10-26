package service

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
)

const (
	BillingSourceWallet       = "wallet"
	BillingSourceWelfare      = "welfare"
	BillingSourceSubscription = "subscription"
)

// PreConsumeBilling 根据用户计费偏好创建 BillingSession 并执行预扣费。
// 会话存储在 relayInfo.Billing 上，供后续 Settle / Refund 使用。
// 会话已存在时（任务重试换到另一个渠道）把预扣补到 preConsumedQuota，
// 和首次预扣一样必须由余额覆盖。
func PreConsumeBilling(c *gin.Context, preConsumedQuota int, relayInfo *relaycommon.RelayInfo) *types.NewAPIError {
	if relayInfo != nil && relayInfo.QuotaClamp != nil {
		return types.NewErrorWithStatusCode(
			relayInfo.QuotaClamp,
			types.ErrorCodeModelPriceError,
			http.StatusBadRequest,
			types.ErrOptionWithSkipRetry(),
		)
	}
	if preConsumedQuota < 0 {
		return types.NewErrorWithStatusCode(
			fmt.Errorf("pre-consume quota cannot be negative: %d", preConsumedQuota),
			types.ErrorCodeModelPriceError,
			http.StatusBadRequest,
			types.ErrOptionWithSkipRetry(),
		)
	}
	if relayInfo != nil && relayInfo.Billing != nil {
		if err := relayInfo.Billing.ReserveBeforeSubmit(preConsumedQuota); err != nil {
			var apiErr *types.NewAPIError
			if errors.As(err, &apiErr) {
				return apiErr
			}
			return types.NewErrorWithStatusCode(err, types.ErrorCodeInsufficientUserQuota, http.StatusForbidden, types.ErrOptionWithSkipRetry())
		}
		return nil
	}
	session, apiErr := NewBillingSession(c, relayInfo, preConsumedQuota)
	if apiErr != nil {
		// Preserve any partially committed token marker so the request failure
		// defer can retry its refund. This is especially important when the
		// immediate rollback failed and funding fallback was intentionally
		// stopped.
		if session != nil {
			relayInfo.Billing = session
		}
		return apiErr
	}
	relayInfo.Billing = session
	return nil
}

// ---------------------------------------------------------------------------
// SettleBilling — 后结算辅助函数
// ---------------------------------------------------------------------------

// SettleBilling 执行计费结算。如果 RelayInfo 上有 BillingSession 则通过 session 结算，
// 否则回退到旧的 PostConsumeQuota 路径（兼容按次计费等场景）。
func SettleBilling(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, actualQuota int) error {
	if relayInfo.Billing != nil {
		preConsumed := relayInfo.Billing.GetPreConsumedQuota()
		delta := actualQuota - preConsumed

		if delta > 0 {
			logger.LogInfo(ctx, common.LogText("charging the difference after pre-consume: %s (actual %s, pre-consumed %s)",
				logger.FormatQuota(delta),
				logger.FormatQuota(actualQuota),
				logger.FormatQuota(preConsumed),
			))
		} else if delta < 0 {
			logger.LogInfo(ctx, common.LogText("refunding the difference after pre-consume: %s (actual %s, pre-consumed %s)",
				logger.FormatQuota(-delta),
				logger.FormatQuota(actualQuota),
				logger.FormatQuota(preConsumed),
			))
		} else {
			logger.LogInfo(ctx, common.LogText("pre-consumed quota matches the actual quota, no adjustment: %s (per-request billing)",
				logger.FormatQuota(actualQuota),
			))
		}

		if err := relayInfo.Billing.Settle(actualQuota); err != nil {
			return err
		}

		// 发送额度通知（订阅计费使用订阅剩余额度）
		if actualQuota != 0 {
			if relayInfo.BillingSource == BillingSourceSubscription {
				checkAndSendSubscriptionQuotaNotify(relayInfo)
			} else if relayInfo.BillingSource == BillingSourceWallet {
				checkAndSendQuotaNotify(relayInfo, actualQuota-preConsumed, preConsumed)
			}
		}
		return nil
	}

	// 回退：无 BillingSession 时使用旧路径
	quotaDelta := actualQuota - relayInfo.FinalPreConsumedQuota
	if quotaDelta != 0 {
		return PostConsumeQuota(relayInfo, quotaDelta, relayInfo.FinalPreConsumedQuota, true)
	}
	return nil
}

// GetCommittedBillingQuotas returns the amounts that have actually been
// deducted from the funding source and token. The values may differ when a
// multi-stage settlement partially fails.
func GetCommittedBillingQuotas(relayInfo *relaycommon.RelayInfo) (fundingQuota int, tokenQuota int) {
	if relayInfo == nil {
		return 0, 0
	}
	if session, ok := relayInfo.Billing.(interface {
		GetCommittedQuotas() (int, int)
	}); ok {
		return session.GetCommittedQuotas()
	}
	fundingQuota = relayInfo.FinalPreConsumedQuota
	if !relayInfo.IsPlayground {
		tokenQuota = relayInfo.FinalPreConsumedQuota
	}
	return fundingQuota, tokenQuota
}

// GetPendingBillingTokenSettlement exposes a failed token adjustment so async
// task persistence can retain the desired target for a later retry.
func GetPendingBillingTokenSettlement(relayInfo *relaycommon.RelayInfo) (target int, pending bool) {
	if relayInfo == nil {
		return 0, false
	}
	if session, ok := relayInfo.Billing.(*BillingSession); ok {
		return session.GetPendingTokenSettlement()
	}
	return 0, false
}

func committedFundingQuota(relayInfo *relaycommon.RelayInfo, fallback int) int {
	if relayInfo == nil || relayInfo.Billing == nil {
		return fallback
	}
	fundingQuota, _ := GetCommittedBillingQuotas(relayInfo)
	return fundingQuota
}

func RestorePreConsumedBilling(relayInfo *relaycommon.RelayInfo) error {
	if relayInfo == nil || relayInfo.Billing == nil {
		return nil
	}
	session, ok := relayInfo.Billing.(*BillingSession)
	if !ok {
		return fmt.Errorf("billing session does not support reservation restore")
	}
	return session.RestorePreConsumed()
}

// RestorePendingBillingSettlement rolls a synchronous request back to its
// durable pre-consumed reservation only when the token settlement itself is
// pending. Funding errors and release errors must not be mistaken for a token
// accounting failure.
func RestorePendingBillingSettlement(relayInfo *relaycommon.RelayInfo) error {
	if _, pending := GetPendingBillingTokenSettlement(relayInfo); !pending {
		return nil
	}
	return RestorePreConsumedBilling(relayInfo)
}
