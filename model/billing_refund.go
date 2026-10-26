package model

import (
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
)

const (
	BillingRefundStatusPending   = "pending"
	BillingRefundStatusCompleted = "completed"
	BillingRefundStatusFailed    = "failed"
)

// BillingRefundMaxAttempts bounds the retry worker. A record that still fails
// after this many attempts is parked as "failed" instead of being retried
// forever, so a permanently impossible refund (for example a deleted user row)
// cannot spin the worker every interval.
const BillingRefundMaxAttempts = 20

var ErrBillingRefundNotApplied = errors.New("billing refund was not applied")

// BillingRefundRecord is a durable request-level compensation marker. Funding
// and token refunds are tracked separately so a transient failure can resume
// without repeating a completed component.
type BillingRefundRecord struct {
	Id              int    `json:"id"`
	RequestId       string `json:"request_id" gorm:"type:varchar(64);uniqueIndex"`
	UserId          int    `json:"user_id" gorm:"index"`
	FundingSource   string `json:"funding_source" gorm:"type:varchar(32);index"`
	FundingQuota    int    `json:"funding_quota"`
	WelfareVersion  int64  `json:"welfare_version" gorm:"type:bigint"`
	TokenId         int    `json:"token_id" gorm:"index"`
	TokenQuota      int    `json:"token_quota"`
	FundingRefunded bool   `json:"funding_refunded"`
	TokenRefunded   bool   `json:"token_refunded"`
	Status          string `json:"status" gorm:"type:varchar(32);index"`
	Attempts        int    `json:"attempts"`
	LastError       string `json:"last_error" gorm:"type:text"`
	CreatedAt       int64  `json:"created_at" gorm:"type:bigint"`
	UpdatedAt       int64  `json:"updated_at" gorm:"type:bigint;index"`
}

type BillingRefundParams struct {
	RequestId      string
	UserId         int
	FundingSource  string
	FundingQuota   int
	WelfareVersion int64
	TokenId        int
	TokenQuota     int
}

func (r *BillingRefundRecord) BeforeCreate(tx *gorm.DB) error {
	now := common.GetTimestamp()
	r.CreatedAt = now
	r.UpdatedAt = now
	return nil
}

func (r *BillingRefundRecord) BeforeUpdate(tx *gorm.DB) error {
	r.UpdatedAt = common.GetTimestamp()
	return nil
}

func CreateBillingRefundRecord(params BillingRefundParams) (*BillingRefundRecord, error) {
	params.RequestId = strings.TrimSpace(params.RequestId)
	if params.RequestId == "" || params.UserId <= 0 || params.FundingQuota < 0 || params.FundingQuota > common.MaxQuota || params.TokenQuota < 0 || params.TokenQuota > common.MaxQuota {
		return nil, errors.New("invalid billing refund parameters")
	}
	if params.FundingSource != "wallet" && params.FundingSource != "welfare" && params.FundingSource != "subscription" {
		return nil, errors.New("invalid billing refund source")
	}

	var existing BillingRefundRecord
	result := DB.Where("request_id = ?", params.RequestId).Limit(1).Find(&existing)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected > 0 {
		if existing.UserId != params.UserId || existing.FundingSource != params.FundingSource ||
			existing.FundingQuota != params.FundingQuota || existing.TokenId != params.TokenId ||
			existing.TokenQuota != params.TokenQuota || existing.WelfareVersion != params.WelfareVersion {
			return nil, errors.New("billing refund request parameters do not match existing record")
		}
		return &existing, nil
	}

	record := &BillingRefundRecord{
		RequestId:       params.RequestId,
		UserId:          params.UserId,
		FundingSource:   params.FundingSource,
		FundingQuota:    params.FundingQuota,
		WelfareVersion:  params.WelfareVersion,
		TokenId:         params.TokenId,
		TokenQuota:      params.TokenQuota,
		FundingRefunded: params.FundingQuota == 0,
		TokenRefunded:   params.TokenQuota == 0,
		Status:          BillingRefundStatusPending,
	}
	if record.FundingRefunded && record.TokenRefunded {
		record.Status = BillingRefundStatusCompleted
	}
	if err := DB.Create(record).Error; err != nil {
		// A concurrent creator may have won the unique request-id race.
		if fetchErr := DB.Where("request_id = ?", params.RequestId).First(&existing).Error; fetchErr == nil {
			return CreateBillingRefundRecord(params)
		}
		return nil, err
	}
	return record, nil
}

// ProcessBillingRefund applies each still-pending component in one database
// transaction with the record row locked. The returned bool reports whether the
// record is fully completed.
func ProcessBillingRefund(requestId string) (bool, error) {
	requestId = strings.TrimSpace(requestId)
	if requestId == "" {
		return false, errors.New("requestId is empty")
	}
	var userCacheDelta int
	var tokenCacheDelta int
	var tokenKey string
	var userId int
	var tokenId int
	completed := false
	err := DB.Transaction(func(tx *gorm.DB) error {
		var record BillingRefundRecord
		if err := lockForUpdate(tx).Where("request_id = ?", requestId).First(&record).Error; err != nil {
			return err
		}
		if record.Status == BillingRefundStatusCompleted {
			completed = true
			return nil
		}
		userId = record.UserId
		tokenId = record.TokenId

		if !record.FundingRefunded {
			switch record.FundingSource {
			case "wallet":
				result := tx.Model(&User{}).
					Where("id = ? AND quota <= ?", record.UserId, common.MaxQuota-record.FundingQuota).
					Update("quota", gorm.Expr("quota + ?", record.FundingQuota))
				if result.Error != nil {
					return result.Error
				}
				if result.RowsAffected != 1 {
					return ErrBillingRefundNotApplied
				}
				userCacheDelta = record.FundingQuota
			case "welfare":
				result := tx.Model(&User{}).
					Where("id = ? AND welfare_version = ? AND (welfare_expires_at = 0 OR welfare_expires_at > ?) AND welfare_quota <= ?",
						record.UserId, record.WelfareVersion, common.GetTimestamp(), common.MaxQuota-record.FundingQuota).
					Update("welfare_quota", gorm.Expr("welfare_quota + ?", record.FundingQuota))
				if result.Error != nil {
					return result.Error
				}
				if result.RowsAffected != 1 {
					return ErrBillingRefundNotApplied
				}
			case "subscription":
				if err := refundSubscriptionPreConsumeTx(tx, record.RequestId); err != nil {
					return err
				}
			default:
				return fmt.Errorf("unsupported billing refund source %q", record.FundingSource)
			}
			record.FundingRefunded = true
		}

		if !record.TokenRefunded {
			var token Token
			if err := lockForUpdate(tx).Where("id = ?", record.TokenId).First(&token).Error; err != nil {
				return err
			}
			result := tx.Model(&Token{}).
				Where("id = ? AND used_quota >= ? AND remain_quota <= ?", record.TokenId, record.TokenQuota, common.MaxQuota-record.TokenQuota).
				Updates(map[string]interface{}{
					"remain_quota":  gorm.Expr("remain_quota + ?", record.TokenQuota),
					"used_quota":    gorm.Expr("used_quota - ?", record.TokenQuota),
					"accessed_time": common.GetTimestamp(),
				})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return ErrBillingRefundNotApplied
			}
			tokenKey = token.Key
			tokenCacheDelta = record.TokenQuota
			record.TokenRefunded = true
		}

		record.LastError = ""
		if record.FundingRefunded && record.TokenRefunded {
			record.Status = BillingRefundStatusCompleted
			completed = true
		}
		return tx.Save(&record).Error
	})
	if err != nil {
		return false, err
	}
	if userCacheDelta > 0 {
		if err := cacheIncrUserQuota(userId, int64(userCacheDelta)); err != nil {
			common.SysLog("failed to sync billing refund to user cache: " + err.Error())
		}
	}
	if common.RedisEnabled && tokenCacheDelta > 0 && tokenKey != "" {
		if _, err := cacheApplyTokenQuotaDelta(tokenId, tokenKey, int64(tokenCacheDelta)); err != nil {
			common.SysLog("failed to sync billing refund to token cache: " + err.Error())
		}
	}
	return completed, nil
}

// RecordBillingRefundFailure counts a failed attempt and parks the record once
// it exhausts BillingRefundMaxAttempts. Parked records keep their quota so an
// administrator can reconcile them, but the retry worker no longer picks them up.
func RecordBillingRefundFailure(requestId string, processErr error) {
	if processErr == nil {
		return
	}
	DB.Model(&BillingRefundRecord{}).Where("request_id = ?", requestId).Updates(map[string]interface{}{
		"attempts":   gorm.Expr("attempts + ?", 1),
		"last_error": processErr.Error(),
		"updated_at": common.GetTimestamp(),
	})
	DB.Model(&BillingRefundRecord{}).
		Where("request_id = ? AND status = ? AND attempts >= ?", requestId, BillingRefundStatusPending, BillingRefundMaxAttempts).
		Update("status", BillingRefundStatusFailed)
}

func GetPendingBillingRefunds(limit int) ([]BillingRefundRecord, error) {
	if limit <= 0 {
		limit = 100
	}
	var records []BillingRefundRecord
	err := DB.Where("status = ?", BillingRefundStatusPending).Order("updated_at asc, id asc").Limit(limit).Find(&records).Error
	return records, err
}

func CleanupCompletedBillingRefunds(olderThan int64) (int64, error) {
	result := DB.Where("status = ? AND updated_at < ?", BillingRefundStatusCompleted, olderThan).Delete(&BillingRefundRecord{})
	return result.RowsAffected, result.Error
}
