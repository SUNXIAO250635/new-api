package service

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
)

// ---------------------------------------------------------------------------
// BillingSession — 统一计费会话
// ---------------------------------------------------------------------------

// BillingSession 封装单次请求的预扣费/结算/退款生命周期。
// 实现 relaycommon.BillingSettler 接口。
type BillingSession struct {
	tokenSettlementPending bool
	tokenSettlementTarget  int
	relayInfo              *relaycommon.RelayInfo
	funding                FundingSource
	preConsumedQuota       int    // 实际预扣额度（信任用户可能为 0）
	fundingConsumed        int    // 资金来源实际已提交额度
	tokenConsumed          int    // 令牌额度实际扣减量
	extraReserved          int    // 发送前补充预扣的额度（订阅退款时需要单独回滚）
	trusted                bool   // 是否命中信任额度旁路
	settled                bool   // Settle 全部完成（资金 + 令牌）
	refunded               bool   // Refund 已调用
	lang                   string // 请求声明的语言，为空表示未声明
	mu                     sync.Mutex
}

// errTokenQuotaRollback marks a funding reservation failure whose token
// rollback also failed. Callers must surface this as a terminal accounting
// error instead of falling back to another funding source, which would charge
// the token twice.
var errTokenQuotaRollback = errors.New("token quota rollback failed")

// Settle 根据实际消耗额度进行结算。
// 资金来源和令牌额度分两步提交，并分别记录实际成功额度。
func (s *BillingSession) Settle(actualQuota int) (settleErr error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if actualQuota < 0 {
		return fmt.Errorf("actual quota cannot be negative: %d", actualQuota)
	}
	defer func() {
		if err := s.funding.Release(); err != nil {
			common.SysLog("error releasing billing source: " + err.Error())
			if settleErr == nil {
				settleErr = err
			}
		}
	}()
	if s.settled {
		return nil
	}
	fundingDelta := actualQuota - s.fundingConsumed
	if fundingDelta != 0 {
		if err := s.funding.Settle(fundingDelta); err != nil {
			return err
		}
		s.fundingConsumed = actualQuota
	}

	tokenDelta := actualQuota - s.tokenConsumed
	var tokenErr error
	if !s.relayInfo.IsPlayground && tokenDelta != 0 {
		if tokenDelta > 0 {
			tokenErr = model.DecreaseTokenQuota(s.relayInfo.TokenId, s.relayInfo.TokenKey, tokenDelta)
		} else {
			tokenErr = model.IncreaseTokenQuota(s.relayInfo.TokenId, s.relayInfo.TokenKey, -tokenDelta)
		}
		if tokenErr != nil {
			s.tokenSettlementPending = true
			s.tokenSettlementTarget = actualQuota
			common.SysLog(common.LogText("error adjusting token quota after funding settled (userId=%d, tokenId=%d, delta=%d): %s",
				s.relayInfo.UserId, s.relayInfo.TokenId, tokenDelta, tokenErr.Error()))
		} else {
			s.tokenConsumed = actualQuota
			s.tokenSettlementPending = false
			s.tokenSettlementTarget = 0
		}
	}
	if s.relayInfo.IsPlayground {
		s.tokenConsumed = 0
		s.tokenSettlementPending = false
		s.tokenSettlementTarget = 0
	}
	if s.funding.Source() == BillingSourceSubscription {
		s.relayInfo.SubscriptionPostDelta += int64(fundingDelta)
	}
	s.settled = tokenErr == nil
	return tokenErr
}

// RestorePreConsumed restores committed funding and token quota to the durable
// reservation recorded before settlement. It is used when persisting the
// settled task markers fails, so a later poll cannot charge the same delta
// again from stale database state.
func (s *BillingSession) RestorePreConsumed() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	targetFunding := s.preConsumedQuota
	targetToken := targetFunding
	if s.relayInfo.IsPlayground {
		targetToken = 0
	}

	var restoreErr error
	fundingDelta := targetFunding - s.fundingConsumed
	if fundingDelta != 0 {
		if err := s.funding.Settle(fundingDelta); err != nil {
			restoreErr = errors.Join(restoreErr, fmt.Errorf("restore funding quota: %w", err))
		} else {
			s.fundingConsumed = targetFunding
			if s.funding.Source() == BillingSourceSubscription {
				s.relayInfo.SubscriptionPostDelta += int64(fundingDelta)
			}
		}
	}

	tokenDelta := targetToken - s.tokenConsumed
	if tokenDelta != 0 {
		var err error
		if tokenDelta > 0 {
			err = model.DecreaseTokenQuota(s.relayInfo.TokenId, s.relayInfo.TokenKey, tokenDelta)
		} else {
			err = model.IncreaseTokenQuota(s.relayInfo.TokenId, s.relayInfo.TokenKey, -tokenDelta)
		}
		if err != nil {
			restoreErr = errors.Join(restoreErr, fmt.Errorf("restore token quota: %w", err))
		} else {
			s.tokenConsumed = targetToken
		}
	}
	if restoreErr == nil {
		s.tokenSettlementPending = false
		s.tokenSettlementTarget = 0
	}

	s.settled = false
	return restoreErr
}

// Refund durably records and applies all pre-consume compensation. A periodic
// worker retries pending records after transient failures or process restarts.
func (s *BillingSession) Refund(c *gin.Context) {
	s.mu.Lock()
	if s.settled {
		s.mu.Unlock()
		return
	}
	// Release request-scoped resources synchronously so a failed request does
	// not hold a subscription concurrency slot while quota refunds run.
	if err := s.funding.Release(); err != nil {
		common.SysLog("error releasing billing source: " + err.Error())
	}
	if s.refunded {
		s.mu.Unlock()
		return
	}
	needsRefund := s.needsRefundLocked()
	if s.relayInfo.RequestId == "" {
		s.relayInfo.RequestId = common.NewRequestId()
	}
	requestId := s.relayInfo.RequestId
	userId := s.relayInfo.UserId
	tokenId := s.relayInfo.TokenId
	tokenConsumed := s.tokenConsumed
	fundingConsumed := s.fundingConsumed
	fundingSource := s.funding.Source()
	welfareVersion := int64(0)
	if welfare, ok := s.funding.(*WelfareFunding); ok {
		welfareVersion = welfare.version
	}
	s.mu.Unlock()
	if !needsRefund {
		return
	}

	refundMessage := common.LogText("user %d request failed, refunding the pre-consumed quota (token_quota=%s, funding=%s)",
		s.relayInfo.UserId,
		logger.FormatQuota(s.tokenConsumed),
		s.funding.Source(),
	)
	if c == nil {
		common.SysLog(refundMessage)
	} else {
		logger.LogInfo(c, refundMessage)
	}

	if s.relayInfo.IsPlayground {
		tokenConsumed = 0
	}
	var record *model.BillingRefundRecord
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		record, err = model.CreateBillingRefundRecord(model.BillingRefundParams{
			RequestId:      requestId,
			UserId:         userId,
			FundingSource:  fundingSource,
			FundingQuota:   fundingConsumed,
			WelfareVersion: welfareVersion,
			TokenId:        tokenId,
			TokenQuota:     tokenConsumed,
		})
		if err == nil {
			break
		}
	}
	if err != nil {
		common.SysLog("error persisting billing refund: " + err.Error())
		return
	}
	s.mu.Lock()
	s.refunded = true
	s.mu.Unlock()
	if record.Status == model.BillingRefundStatusCompleted {
		return
	}
	if _, err := model.ProcessBillingRefund(requestId); err != nil {
		model.RecordBillingRefundFailure(requestId, err)
		common.SysLog("billing refund queued for retry: " + err.Error())
	}
}

// NeedsRefund 返回是否存在需要退还的预扣状态。
func (s *BillingSession) NeedsRefund() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.needsRefundLocked()
}

func (s *BillingSession) needsRefundLocked() bool {
	if s.settled || s.refunded {
		return false
	}
	if s.tokenConsumed > 0 || s.fundingConsumed > 0 {
		return true
	}
	// 订阅可能在 tokenConsumed=0 时仍预扣了额度
	if sub, ok := s.funding.(*SubscriptionFunding); ok && sub.preConsumed > 0 {
		return true
	}
	if welfare, ok := s.funding.(*WelfareFunding); ok && welfare.consumed > 0 {
		return true
	}
	return false
}

// GetPreConsumedQuota 返回实际预扣的额度。
func (s *BillingSession) GetPreConsumedQuota() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.preConsumedQuota
}

func (s *BillingSession) GetCommittedQuotas() (fundingQuota int, tokenQuota int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.fundingConsumed, s.tokenConsumed
}

// GetPendingTokenSettlement reports a token adjustment that did not commit
// during settlement. The target is the desired final token quota for the task
// marker persisted by the caller.
func (s *BillingSession) GetPendingTokenSettlement() (target int, pending bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tokenSettlementTarget, s.tokenSettlementPending
}

func (s *BillingSession) Reserve(targetQuota int) error {
	return s.reserve(targetQuota, false)
}

// ReserveBeforeSubmit 在发起上游请求前补扣，钱包余额必须覆盖差额。
func (s *BillingSession) ReserveBeforeSubmit(targetQuota int) error {
	return s.reserve(targetQuota, true)
}

func (s *BillingSession) reserve(targetQuota int, requireAvailableQuota bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.relayInfo != nil {
		_, imageRequest := s.relayInfo.Request.(*dto.ImageRequest)
		requireAvailableQuota = requireAvailableQuota || imageRequest || s.relayInfo.ImageRequestCount > 0
	}
	if s.settled || s.refunded || s.trusted && !requireAvailableQuota || targetQuota <= s.preConsumedQuota {
		return nil
	}

	delta := targetQuota - s.preConsumedQuota
	if delta <= 0 {
		return nil
	}

	if err := s.reserveFunding(delta, requireAvailableQuota); err != nil {
		return err
	}
	if err := s.reserveToken(delta); err != nil {
		s.rollbackFundingReserve(delta)
		return err
	}

	s.preConsumedQuota += delta
	s.fundingConsumed += delta
	if !s.relayInfo.IsPlayground {
		s.tokenConsumed += delta
	}
	s.extraReserved += delta
	if requireAvailableQuota {
		s.trusted = false
	}
	s.syncRelayInfo()
	return nil
}

// ---------------------------------------------------------------------------
// PreConsume — 统一预扣费入口（含信任额度旁路）
// ---------------------------------------------------------------------------

// preConsume 执行预扣费：信任检查 -> 令牌预扣 -> 资金来源预扣。
// 任一步骤失败时原子回滚已完成的步骤。
func (s *BillingSession) preConsume(c *gin.Context, quota int) *types.NewAPIError {
	effectiveQuota := quota

	// ---- 信任额度旁路 ----
	if s.shouldTrust(c) {
		s.trusted = true
		effectiveQuota = 0
		logger.LogInfo(c, common.LogText("user %d has enough quota, trusted without pre-consume (funding=%s)", s.relayInfo.UserId, s.funding.Source()))
	} else if effectiveQuota > 0 {
		logger.LogInfo(c, common.LogText("user %d needs pre-consume %s (funding=%s)", s.relayInfo.UserId, logger.FormatQuota(effectiveQuota), s.funding.Source()))
	}

	// ---- 1) 预扣令牌额度 ----
	if effectiveQuota > 0 && !s.relayInfo.IsPlayground {
		if err := PreConsumeTokenQuota(s.relayInfo, effectiveQuota); err != nil {
			return types.NewErrorWithStatusCode(err, types.ErrorCodePreConsumeTokenQuotaFailed, http.StatusForbidden, types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
		}
		s.tokenConsumed = effectiveQuota
	}

	// ---- 2) 预扣资金来源 ----
	if err := s.funding.PreConsume(effectiveQuota); err != nil {
		// 预扣费失败，回滚令牌额度
		if s.tokenConsumed > 0 && !s.relayInfo.IsPlayground {
			if rollbackErr := model.IncreaseTokenQuota(s.relayInfo.TokenId, s.relayInfo.TokenKey, s.tokenConsumed); rollbackErr != nil {
				common.SysLog(common.LogText("error rolling back token quota (userId=%d, tokenId=%d, amount=%d, fundingErr=%s): %s",
					s.relayInfo.UserId, s.relayInfo.TokenId, s.tokenConsumed, err.Error(), rollbackErr.Error()))
				// Keep the token marker intact and stop all funding fallbacks. The
				// token was charged successfully and is now pending reconciliation;
				// treating this as an ordinary funding shortfall could charge a
				// second source as well.
				return types.NewError(
					errors.Join(errTokenQuotaRollback, err, fmt.Errorf("rollback token quota: %w", rollbackErr)),
					types.ErrorCodeUpdateDataError,
					types.ErrOptionWithSkipRetry(),
				)
			}
			s.tokenConsumed = 0
		}
		if errors.Is(err, model.ErrSubscriptionConcurrencyLimit) {
			return types.NewErrorWithStatusCode(err, types.ErrorCodeUpdateDataError, http.StatusTooManyRequests, types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
		}
		// TODO: model 层应定义哨兵错误（如 ErrNoActiveSubscription），用 errors.Is 替代字符串匹配
		if errors.Is(err, ErrInsufficientWalletQuota) {
			userQuota, quotaErr := model.GetUserQuota(s.relayInfo.UserId, false)
			if quotaErr != nil {
				userQuota = 0
			}
			return types.NewErrorWithStatusCode(
				errors.New(i18n.T(c, i18n.MsgQuotaUserInsufficient, map[string]any{"Remaining": logger.FormatQuota(userQuota)})),
				types.ErrorCodeInsufficientUserQuota, http.StatusForbidden,
				types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
		}
		if errors.Is(err, model.ErrInsufficientWelfareQuota) {
			return types.NewErrorWithStatusCode(
				err,
				types.ErrorCodeInsufficientUserQuota,
				http.StatusForbidden,
				types.ErrOptionWithSkipRetry(),
				types.ErrOptionWithNoRecordErrorLog(),
			)
		}
		errMsg := err.Error()
		if strings.Contains(errMsg, "no active subscription") || strings.Contains(errMsg, "subscription quota insufficient") {
			return types.NewErrorWithStatusCode(errors.New(i18n.T(c, i18n.MsgQuotaSubscriptionInsufficient, map[string]any{"Error": errMsg})), types.ErrorCodeInsufficientUserQuota, http.StatusForbidden, types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
		}
		return types.NewError(err, types.ErrorCodeUpdateDataError, types.ErrOptionWithSkipRetry())
	}

	s.preConsumedQuota = effectiveQuota
	s.fundingConsumed = effectiveQuota

	// ---- 同步 RelayInfo 兼容字段 ----
	s.syncRelayInfo()

	return nil
}

func (s *BillingSession) reserveFunding(delta int, requireAvailableQuota bool) error {
	switch funding := s.funding.(type) {
	case *WalletFunding:
		if requireAvailableQuota {
			// Image quantities and task retry prices are known before
			// submission. Reserve atomically instead of admitting wallet debt.
			if err := funding.PreConsume(delta); err != nil {
				if errors.Is(err, ErrInsufficientWalletQuota) {
					return types.NewErrorWithStatusCode(err, types.ErrorCodeInsufficientUserQuota, http.StatusForbidden, types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
				}
				return types.NewError(err, types.ErrorCodeUpdateDataError, types.ErrOptionWithSkipRetry())
			}
			return nil
		}
		// 与结算补扣（SettleBilling 正差额 → WalletFunding.Settle）语义一致：
		// 全额无条件扣减，余额不足的部分记为欠费（余额可为负），不中断请求，
		// 保证日志记录的预扣额度与用户余额的实际变动始终对账一致。
		// DecreaseUserQuota 仅在数据库错误时失败。
		if err := model.DecreaseUserQuota(funding.userId, delta, false); err != nil {
			return types.NewError(err, types.ErrorCodeUpdateDataError, types.ErrOptionWithSkipRetry())
		}
		funding.consumed += delta
		return nil
	case *WelfareFunding:
		applied, err := model.DecreaseUserWelfareQuotaForVersion(funding.userId, delta, funding.version)
		if err != nil || !applied {
			if err == nil {
				err = model.ErrWelfareQuotaNotApplied
			}
			return types.NewErrorWithStatusCode(
				err,
				types.ErrorCodeInsufficientUserQuota,
				http.StatusForbidden,
				types.ErrOptionWithSkipRetry(),
				types.ErrOptionWithNoRecordErrorLog(),
			)
		}
		funding.consumed += delta
		return nil
	case *SubscriptionFunding:
		if err := model.PostConsumeUserSubscriptionForRequest(funding.requestId, funding.subscriptionId, int64(delta)); err != nil {
			return types.NewErrorWithStatusCode(
				errors.New(i18n.Translate(s.lang, i18n.MsgQuotaSubscriptionInsufficient, map[string]any{"Error": err.Error()})),
				types.ErrorCodeInsufficientUserQuota,
				http.StatusForbidden,
				types.ErrOptionWithSkipRetry(),
				types.ErrOptionWithNoRecordErrorLog(),
			)
		}
		funding.postConsumed += int64(delta)
		return nil
	default:
		return types.NewError(fmt.Errorf("unsupported funding source: %s", s.funding.Source()), types.ErrorCodeUpdateDataError, types.ErrOptionWithSkipRetry())
	}
}

func (s *BillingSession) rollbackFundingReserve(delta int) {
	switch funding := s.funding.(type) {
	case *WalletFunding:
		if err := model.IncreaseUserQuota(funding.userId, delta, false); err != nil {
			common.SysLog(common.LogText("error rolling back wallet funding reserve: %s", err.Error()))
		} else {
			funding.consumed -= delta
		}
	case *WelfareFunding:
		applied, err := model.IncreaseUserWelfareQuotaForVersion(funding.userId, delta, funding.version)
		if err != nil || !applied {
			if err == nil {
				err = model.ErrWelfareQuotaNotApplied
			}
			common.SysLog("error rolling back welfare funding reserve: " + err.Error())
		} else {
			funding.consumed -= delta
		}
	case *SubscriptionFunding:
		if err := model.PostConsumeUserSubscriptionForRequest(funding.requestId, funding.subscriptionId, -int64(delta)); err != nil {
			common.SysLog(common.LogText("error rolling back subscription funding reserve: %s", err.Error()))
		} else {
			funding.postConsumed -= int64(delta)
		}
	}
}

func (s *BillingSession) reserveToken(delta int) error {
	if delta <= 0 || s.relayInfo.IsPlayground {
		return nil
	}
	if err := PreConsumeTokenQuota(s.relayInfo, delta); err != nil {
		return types.NewErrorWithStatusCode(err, types.ErrorCodePreConsumeTokenQuotaFailed, http.StatusForbidden, types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
	}
	return nil
}

// shouldTrust 统一信任额度检查，适用于钱包和订阅。
func (s *BillingSession) shouldTrust(c *gin.Context) bool {
	// 异步任务（ForcePreConsume=true）必须预扣全额，不允许信任旁路
	if s.relayInfo.ForcePreConsume {
		return false
	}

	trustQuota := operation_setting.GetQuotaSetting().TrustQuotaUSD * common.QuotaPerUnit
	if trustQuota <= 0 || math.IsNaN(trustQuota) || math.IsInf(trustQuota, 0) {
		return false
	}

	// 检查令牌是否充足
	tokenTrusted := s.relayInfo.TokenUnlimited
	if !tokenTrusted {
		tokenQuota := c.GetInt("token_quota")
		tokenTrusted = float64(tokenQuota) > trustQuota
	}
	if !tokenTrusted {
		return false
	}

	switch s.funding.Source() {
	case BillingSourceWallet:
		return float64(s.relayInfo.UserQuota) > trustQuota
	case BillingSourceWelfare:
		// Welfare quota may expire while a request is in flight, so it is always
		// reserved before the upstream request is sent.
		return false
	case BillingSourceSubscription:
		// 订阅不能启用信任旁路。原因：
		// 1. PreConsumeUserSubscription 要求 amount>0 来创建预扣记录并锁定订阅
		// 2. SubscriptionFunding.PreConsume 忽略参数，始终用 s.amount 预扣
		// 3. 若信任旁路将 effectiveQuota 设为 0，会导致 preConsumedQuota 与实际订阅预扣不一致
		return false
	default:
		return false
	}
}

// syncRelayInfo 将 BillingSession 的状态同步到 RelayInfo 的兼容字段上。
func (s *BillingSession) syncRelayInfo() {
	info := s.relayInfo
	info.FinalPreConsumedQuota = s.preConsumedQuota
	info.BillingSource = s.funding.Source()
	if welfare, ok := s.funding.(*WelfareFunding); ok {
		info.WelfareVersion = welfare.version
	} else {
		info.WelfareVersion = 0
	}

	if sub, ok := s.funding.(*SubscriptionFunding); ok {
		info.SubscriptionId = sub.subscriptionId
		info.SubscriptionPreConsumed = sub.preConsumed + int64(s.extraReserved)
		info.SubscriptionPostDelta = 0
		info.SubscriptionAmountTotal = sub.AmountTotal
		info.SubscriptionAmountUsedAfterPreConsume = sub.AmountUsedAfter + int64(s.extraReserved)
		info.SubscriptionPlanId = sub.PlanId
		info.SubscriptionPlanTitle = sub.PlanTitle
	} else {
		info.SubscriptionId = 0
		info.SubscriptionPreConsumed = 0
	}
}

// ---------------------------------------------------------------------------
// NewBillingSession 工厂 — 根据计费偏好创建会话并处理回退
// ---------------------------------------------------------------------------

// NewBillingSession 根据用户计费偏好创建 BillingSession，处理 subscription_first / wallet_first 的回退。
func NewBillingSession(c *gin.Context, relayInfo *relaycommon.RelayInfo, preConsumedQuota int) (*BillingSession, *types.NewAPIError) {
	if relayInfo == nil {
		return nil, types.NewError(fmt.Errorf("relayInfo is nil"), types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
	}

	pref := common.NormalizeBillingPreference(relayInfo.UserSetting.BillingPreference)
	usageGroup := strings.TrimSpace(relayInfo.UsingGroup)
	if usageGroup == "" {
		usageGroup = strings.TrimSpace(relayInfo.TokenGroup)
	}
	if usageGroup == "" {
		usageGroup = strings.TrimSpace(relayInfo.UserGroup)
	}

	// 钱包路径需要先检查用户额度
	tryWallet := func() (*BillingSession, *types.NewAPIError) {
		userQuota, err := model.GetUserQuota(relayInfo.UserId, false)
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeQueryDataError, types.ErrOptionWithSkipRetry())
		}
		if userQuota <= 0 {
			return nil, types.NewErrorWithStatusCode(
				errors.New(i18n.T(c, i18n.MsgQuotaUserInsufficient, map[string]any{"Remaining": logger.FormatQuota(userQuota)})),
				types.ErrorCodeInsufficientUserQuota, http.StatusForbidden,
				types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
		}
		if userQuota-preConsumedQuota < 0 {
			return nil, types.NewErrorWithStatusCode(
				errors.New(i18n.T(c, i18n.MsgQuotaPreConsumeInsufficient, map[string]any{"Remaining": logger.FormatQuota(userQuota), "Required": logger.FormatQuota(preConsumedQuota)})),
				types.ErrorCodeInsufficientUserQuota, http.StatusForbidden,
				types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
		}
		relayInfo.UserQuota = userQuota

		session := &BillingSession{
			relayInfo: relayInfo,
			funding:   &WalletFunding{userId: relayInfo.UserId},
			lang:      i18n.StatedLang(c),
		}
		if apiErr := session.preConsume(c, preConsumedQuota); apiErr != nil {
			return session, apiErr
		}
		return session, nil
	}

	tryWelfare := func() (*BillingSession, *types.NewAPIError) {
		session := &BillingSession{
			relayInfo: relayInfo,
			funding:   &WelfareFunding{userId: relayInfo.UserId},
		}
		if apiErr := session.preConsume(c, preConsumedQuota); apiErr != nil {
			return session, apiErr
		}
		return session, nil
	}

	trySubscription := func() (*BillingSession, *types.NewAPIError) {
		subConsume := int64(preConsumedQuota)
		if subConsume <= 0 {
			subConsume = 1
		}
		session := &BillingSession{
			relayInfo: relayInfo,
			lang:      i18n.StatedLang(c),
			funding: &SubscriptionFunding{
				requestId:  relayInfo.RequestId,
				userId:     relayInfo.UserId,
				modelName:  relayInfo.GetBillingModelName(),
				usageGroup: usageGroup,
				amount:     subConsume,
			},
		}
		// 必须传 subConsume 而非 preConsumedQuota，保证 SubscriptionFunding.amount、
		// preConsume 参数和 FinalPreConsumedQuota 三者一致，避免订阅多扣费。
		if apiErr := session.preConsume(c, int(subConsume)); apiErr != nil {
			return session, apiErr
		}
		return session, nil
	}

	if preConsumedQuota > 0 && model.DB != nil {
		welfare, err := model.GetUserWelfareQuota(relayInfo.UserId)
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeQueryDataError, types.ErrOptionWithSkipRetry())
		}
		if welfare.Quota >= preConsumedQuota {
			session, apiErr := tryWelfare()
			if apiErr == nil {
				return session, nil
			}
			if apiErr.GetErrorCode() != types.ErrorCodeInsufficientUserQuota || !errors.Is(apiErr, model.ErrInsufficientWelfareQuota) {
				return session, apiErr
			}
		}
	}

	switch pref {
	case "subscription_only":
		return trySubscription()
	case "wallet_only":
		return tryWallet()
	case "wallet_first":
		session, err := tryWallet()
		if err != nil {
			if err.GetErrorCode() == types.ErrorCodeInsufficientUserQuota {
				return trySubscription()
			}
			return session, err
		}
		return session, nil
	case "subscription_first":
		fallthrough
	default:
		hasSub, subCheckErr := model.HasActiveUserSubscriptionForGroup(relayInfo.UserId, usageGroup)
		if subCheckErr != nil {
			return nil, types.NewError(subCheckErr, types.ErrorCodeQueryDataError, types.ErrOptionWithSkipRetry())
		}
		if !hasSub {
			return tryWallet()
		}
		session, apiErr := trySubscription()
		if apiErr != nil {
			if errors.Is(apiErr, model.ErrSubscriptionConcurrencyLimit) {
				return session, apiErr
			}
			if apiErr.GetErrorCode() == types.ErrorCodeInsufficientUserQuota {
				// 仅当用户的活跃订阅允许钱包回退时才回退到钱包，否则返回订阅额度不足错误
				allowOverflow, overflowErr := model.UserActiveSubscriptionsAllowWalletOverflowForGroup(relayInfo.UserId, usageGroup)
				if overflowErr != nil {
					return nil, types.NewError(overflowErr, types.ErrorCodeQueryDataError, types.ErrOptionWithSkipRetry())
				}
				if allowOverflow {
					return tryWallet()
				}
				return session, apiErr
			}
			return session, apiErr
		}
		return session, nil
	}
}
