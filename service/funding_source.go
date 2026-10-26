package service

import (
	"errors"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

// ---------------------------------------------------------------------------
// FundingSource — 资金来源接口（钱包 or 订阅）
// ---------------------------------------------------------------------------

// FundingSource 抽象了预扣费的资金来源。
type FundingSource interface {
	// Source 返回资金来源标识："wallet" 或 "subscription"
	Source() string
	// PreConsume 从该资金来源预扣 amount 额度
	PreConsume(amount int) error
	// Settle 根据差额调整资金来源（正数补扣，负数退还）
	Settle(delta int) error
	// Refund 退还所有预扣费
	Refund() error
	// Release 释放请求级资源（例如订阅并发租约）。钱包资金源无此资源。
	Release() error
}

// ---------------------------------------------------------------------------
// WalletFunding — 钱包资金来源实现
// ---------------------------------------------------------------------------

type WalletFunding struct {
	userId   int
	consumed int // 实际预扣的用户额度
}

func (w *WalletFunding) Source() string { return BillingSourceWallet }

func (w *WalletFunding) PreConsume(amount int) error {
	if amount <= 0 {
		return nil
	}
	reserved, err := model.TryReserveUserQuota(w.userId, amount)
	if err != nil {
		return err
	}
	if !reserved {
		return ErrInsufficientWalletQuota
	}
	// 发送前补扣会再次预扣，累加后退款才能退回全部预扣。
	w.consumed += amount
	return nil
}

// ErrInsufficientWalletQuota 钱包原子预扣失败（余额不足），未发生任何扣减。
// BillingSession 据此映射为 ErrorCodeInsufficientUserQuota，
// 使 wallet_first 等计费偏好可以回退到订阅。
var ErrInsufficientWalletQuota = errors.New("wallet quota insufficient")

func (w *WalletFunding) Settle(delta int) error {
	if delta == 0 {
		return nil
	}
	if delta > 0 {
		if err := model.DecreaseUserQuota(w.userId, delta, false); err != nil {
			return err
		}
		w.consumed += delta
		return nil
	}
	if err := model.IncreaseUserQuota(w.userId, -delta, false); err != nil {
		return err
	}
	w.consumed += delta
	return nil
}

func (w *WalletFunding) Refund() error {
	if w.consumed <= 0 {
		return nil
	}
	// IncreaseUserQuota 是 quota += N 的非幂等操作，不能重试，否则会多退额度。
	// 订阅的 RefundSubscriptionPreConsume 有 requestId 幂等保护所以可以重试。
	return model.IncreaseUserQuota(w.userId, w.consumed, false)
}

func (w *WalletFunding) Release() error { return nil }

// WelfareFunding is an administrator-granted permanent or time-limited balance.
type WelfareFunding struct {
	userId   int
	consumed int
	version  int64
}

func (w *WelfareFunding) Source() string { return BillingSourceWelfare }

func (w *WelfareFunding) PreConsume(amount int) error {
	if amount <= 0 {
		return nil
	}
	version, reserved, err := model.TryReserveUserWelfareQuotaVersioned(w.userId, amount)
	if err != nil {
		return err
	}
	if !reserved {
		return model.ErrInsufficientWelfareQuota
	}
	w.consumed = amount
	w.version = version
	return nil
}

func (w *WelfareFunding) Settle(delta int) error {
	if delta == 0 {
		return nil
	}
	if delta > 0 {
		applied, err := model.DecreaseUserWelfareQuotaForVersion(w.userId, delta, w.version)
		if err != nil {
			return err
		}
		if !applied {
			return model.ErrWelfareQuotaNotApplied
		}
		w.consumed += delta
		return nil
	}
	refund := -delta
	applied, err := model.IncreaseUserWelfareQuotaForVersion(w.userId, refund, w.version)
	if err != nil {
		return err
	}
	if !applied {
		return model.ErrWelfareQuotaNotApplied
	}
	w.consumed -= refund
	if w.consumed < 0 {
		w.consumed = 0
	}
	return nil
}

func (w *WelfareFunding) Refund() error {
	if w.consumed <= 0 {
		return nil
	}
	applied, err := model.IncreaseUserWelfareQuotaForVersion(w.userId, w.consumed, w.version)
	if err != nil {
		return err
	}
	if !applied {
		return model.ErrWelfareQuotaNotApplied
	}
	return nil
}

func (w *WelfareFunding) Release() error { return nil }

// ---------------------------------------------------------------------------
// SubscriptionFunding — 订阅资金来源实现
// ---------------------------------------------------------------------------

type SubscriptionFunding struct {
	requestId      string
	userId         int
	modelName      string
	usageGroup     string
	amount         int64 // 预扣的订阅额度（subConsume）
	subscriptionId int
	preConsumed    int64
	postConsumed   int64
	// 以下字段在 PreConsume 成功后填充，供 RelayInfo 同步使用
	AmountTotal      int64
	AmountUsedAfter  int64
	PlanId           int
	PlanTitle        string
	concurrencyLimit int
	renewStop        chan struct{}
	renewStartOnce   sync.Once
	renewOnce        sync.Once
}

func (s *SubscriptionFunding) Source() string { return BillingSourceSubscription }

func (s *SubscriptionFunding) PreConsume(_ int) error {
	// amount 参数被忽略，使用内部 s.amount（已在构造时根据 preConsumedQuota 计算）
	res, err := model.PreConsumeUserSubscriptionForGroup(s.requestId, s.userId, s.modelName, 0, s.amount, s.usageGroup)
	if err != nil {
		return err
	}
	s.subscriptionId = res.UserSubscriptionId
	s.preConsumed = res.PreConsumed
	s.AmountTotal = res.AmountTotal
	s.AmountUsedAfter = res.AmountUsedAfter
	s.concurrencyLimit = res.ConcurrencyLimit
	if s.concurrencyLimit > 0 {
		s.startConcurrencyLeaseRenewal()
	}
	// 获取订阅计划信息
	if planInfo, err := model.GetSubscriptionPlanInfoByUserSubscriptionId(res.UserSubscriptionId); err == nil && planInfo != nil {
		s.PlanId = planInfo.PlanId
		s.PlanTitle = planInfo.PlanTitle
	}
	return nil
}

func (s *SubscriptionFunding) Settle(delta int) error {
	if delta == 0 {
		return nil
	}
	if err := model.PostConsumeUserSubscriptionForRequest(s.requestId, s.subscriptionId, int64(delta)); err != nil {
		return err
	}
	s.postConsumed += int64(delta)
	return nil
}

func (s *SubscriptionFunding) Refund() error {
	if s.preConsumed <= 0 && s.postConsumed == 0 {
		return nil
	}
	return refundWithRetry(func() error {
		if s.preConsumed > 0 {
			if err := model.RefundSubscriptionPreConsume(s.requestId); err != nil {
				return err
			}
		}
		s.postConsumed = 0
		return nil
	})
}

const subscriptionConcurrencyLeaseRenewInterval = 5 * time.Minute

func (s *SubscriptionFunding) startConcurrencyLeaseRenewal() {
	if s.concurrencyLimit <= 0 || s.requestId == "" {
		return
	}
	s.renewStartOnce.Do(func() {
		s.renewStop = make(chan struct{})
		go func(stop <-chan struct{}, requestID string) {
			ticker := time.NewTicker(subscriptionConcurrencyLeaseRenewInterval)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					if err := model.RenewSubscriptionConcurrencyLease(requestID); err != nil {
						// Lease expiry is the recovery path for an interrupted request;
						// keep retrying on the next tick for transient database failures.
						common.SysLog("error renewing subscription concurrency lease: " + err.Error())
					}
				case <-stop:
					return
				}
			}
		}(s.renewStop, s.requestId)
	})
}

// Release stops lease renewal and releases the request lease. It is safe to
// call from both settle and refund paths, including repeated calls.
func (s *SubscriptionFunding) Release() error {
	if s.renewStop != nil {
		s.renewOnce.Do(func() { close(s.renewStop) })
	}
	if s.concurrencyLimit <= 0 || s.requestId == "" {
		return nil
	}
	return model.ReleaseSubscriptionConcurrencyLease(s.requestId)
}

// refundWithRetry 尝试多次执行退款操作以提高成功率，只能用于基于事务的退款函数！！！！！！
// try to refund with retries, only for refund functions based on transactions!!!
func refundWithRetry(fn func() error) error {
	if fn == nil {
		return nil
	}
	const maxAttempts = 3
	var lastErr error
	for i := range maxAttempts {
		if err := fn(); err == nil {
			return nil
		} else {
			lastErr = err
		}
		if i < maxAttempts-1 {
			time.Sleep(time.Duration(200*(i+1)) * time.Millisecond)
		}
	}
	return lastErr
}
