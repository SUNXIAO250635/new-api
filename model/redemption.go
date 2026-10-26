package model

import (
	"errors"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	RedemptionTypeQuota        = "quota"
	RedemptionTypeWelfare      = "welfare"
	RedemptionTypeSubscription = "subscription"
)

type Redemption struct {
	Id               int            `json:"id"`
	UserId           int            `json:"user_id"`
	Key              string         `json:"key" gorm:"type:char(32);uniqueIndex"`
	Status           int            `json:"status" gorm:"default:1"`
	Name             string         `json:"name" gorm:"index"`
	Quota            int            `json:"quota" gorm:"default:100"`
	Type             string         `json:"type" gorm:"type:varchar(32);not null;default:'quota';index"`
	PlanId           int            `json:"plan_id" gorm:"default:0;index"`
	PlanTitle        string         `json:"plan_title" gorm:"-"`
	BatchId          string         `json:"batch_id" gorm:"type:varchar(32);index"`
	AllowSameUser    bool           `json:"allow_same_user"`
	WelfareExpiresAt *int64         `json:"welfare_expires_at" gorm:"bigint"`
	CreatedTime      int64          `json:"created_time" gorm:"bigint"`
	RedeemedTime     int64          `json:"redeemed_time" gorm:"bigint"`
	Count            int            `json:"count" gorm:"-:all"` // only for api request
	UsedUserId       int            `json:"used_user_id"`
	DeletedAt        gorm.DeletedAt `gorm:"index"`
	ExpiredTime      int64          `json:"expired_time" gorm:"bigint"` // 过期时间，0 表示不过期
}

type RedemptionBatchClaim struct {
	Id           int    `json:"id"`
	BatchId      string `json:"batch_id" gorm:"type:varchar(32);not null;uniqueIndex:idx_redemption_batch_user,priority:1"`
	UserId       int    `json:"user_id" gorm:"not null;uniqueIndex:idx_redemption_batch_user,priority:2"`
	RedemptionId int    `json:"redemption_id" gorm:"not null;index"`
	CreatedTime  int64  `json:"created_time" gorm:"bigint;not null"`
}

func NormalizeRedemptionType(redemptionType string) string {
	switch strings.ToLower(strings.TrimSpace(redemptionType)) {
	case "", RedemptionTypeQuota:
		return RedemptionTypeQuota
	case RedemptionTypeWelfare:
		return RedemptionTypeWelfare
	case RedemptionTypeSubscription:
		return RedemptionTypeSubscription
	default:
		return ""
	}
}

func attachRedemptionPlanTitles(tx *gorm.DB, redemptions []*Redemption) error {
	planIds := make([]int, 0)
	seen := make(map[int]struct{})
	for _, redemption := range redemptions {
		redemption.Type = NormalizeRedemptionType(redemption.Type)
		if redemption.Type != RedemptionTypeSubscription || redemption.PlanId <= 0 {
			continue
		}
		if _, ok := seen[redemption.PlanId]; ok {
			continue
		}
		seen[redemption.PlanId] = struct{}{}
		planIds = append(planIds, redemption.PlanId)
	}
	if len(planIds) == 0 {
		return nil
	}
	var plans []SubscriptionPlan
	if err := tx.Select("id", "title").Where("id IN ?", planIds).Find(&plans).Error; err != nil {
		return err
	}
	titles := make(map[int]string, len(plans))
	for _, plan := range plans {
		titles[plan.Id] = plan.Title
	}
	for _, redemption := range redemptions {
		redemption.PlanTitle = titles[redemption.PlanId]
	}
	return nil
}

func GetAllRedemptions(startIdx int, num int) (redemptions []*Redemption, total int64, err error) {
	// 开始事务
	tx := DB.Begin()
	if tx.Error != nil {
		return nil, 0, tx.Error
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	// 获取总数
	err = tx.Model(&Redemption{}).Count(&total).Error
	if err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	// 获取分页数据
	err = tx.Order("id desc").Limit(num).Offset(startIdx).Find(&redemptions).Error
	if err != nil {
		tx.Rollback()
		return nil, 0, err
	}
	if err = attachRedemptionPlanTitles(tx, redemptions); err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	// 提交事务
	if err = tx.Commit().Error; err != nil {
		return nil, 0, err
	}

	return redemptions, total, nil
}

func SearchRedemptions(keyword string, status string, startIdx int, num int) (redemptions []*Redemption, total int64, err error) {
	return SearchRedemptionsWithType(keyword, status, "", startIdx, num)
}

func SearchRedemptionsWithType(keyword string, status string, redemptionType string, startIdx int, num int) (redemptions []*Redemption, total int64, err error) {
	tx := DB.Begin()
	if tx.Error != nil {
		return nil, 0, tx.Error
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	query := applyRedemptionFilters(tx.Model(&Redemption{}), keyword, status, redemptionType)

	// Get total count
	err = query.Count(&total).Error
	if err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	// Get paginated data
	err = query.Order("id desc").Limit(num).Offset(startIdx).Find(&redemptions).Error
	if err != nil {
		tx.Rollback()
		return nil, 0, err
	}
	if err = attachRedemptionPlanTitles(tx, redemptions); err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	if err = tx.Commit().Error; err != nil {
		return nil, 0, err
	}

	return redemptions, total, nil
}

func applyRedemptionFilters(query *gorm.DB, keyword string, status string, redemptionType string) *gorm.DB {
	if keyword != "" {
		if id, err := strconv.Atoi(keyword); err == nil {
			query = query.Where("id = ? OR name LIKE ?", id, keyword+"%")
		} else {
			query = query.Where("name LIKE ?", keyword+"%")
		}
	}

	if status != "" {
		now := common.GetTimestamp()
		switch status {
		case "expired":
			query = query.Where(
				"status = ? AND expired_time != 0 AND expired_time < ?",
				common.RedemptionCodeStatusEnabled,
				now,
			)
		case strconv.Itoa(common.RedemptionCodeStatusEnabled):
			query = query.Where(
				"status = ? AND (expired_time = 0 OR expired_time >= ?)",
				common.RedemptionCodeStatusEnabled,
				now,
			)
		case strconv.Itoa(common.RedemptionCodeStatusDisabled):
			query = query.Where("status = ?", common.RedemptionCodeStatusDisabled)
		case strconv.Itoa(common.RedemptionCodeStatusUsed):
			query = query.Where("status = ?", common.RedemptionCodeStatusUsed)
		}
	}

	if normalizedType := NormalizeRedemptionType(redemptionType); strings.TrimSpace(redemptionType) != "" && normalizedType != "" {
		if normalizedType == RedemptionTypeQuota {
			query = query.Where("(type = ? OR type = '' OR type IS NULL)", normalizedType)
		} else {
			query = query.Where("type = ?", normalizedType)
		}
	}
	return query
}

func SearchRedemptionsForExport(keyword string, status string) ([]*Redemption, error) {
	return SearchRedemptionsForExportWithType(keyword, status, "")
}

func SearchRedemptionsForExportWithType(keyword string, status string, redemptionType string) ([]*Redemption, error) {
	redemptions, _, err := SearchRedemptionsWithType(keyword, status, redemptionType, 0, -1)
	return redemptions, err
}

// IterateRedemptionKeysForExport scans matching keys in bounded batches. The
// callback must consume the slice before returning.
func IterateRedemptionKeysForExport(keyword string, status string, redemptionType string, batchSize int, fn func([]Redemption) error) error {
	if batchSize <= 0 {
		batchSize = 1000
	}
	if fn == nil {
		return errors.New("export callback is required")
	}
	lastID := 0
	for {
		var batch []Redemption
		query := applyRedemptionFilters(DB.Model(&Redemption{}), keyword, status, redemptionType)
		if lastID > 0 {
			query = query.Where("id < ?", lastID)
		}
		if err := query.Select("id", "key").Order("id desc").Limit(batchSize).Find(&batch).Error; err != nil {
			return err
		}
		if len(batch) == 0 {
			return nil
		}
		if err := fn(batch); err != nil {
			return err
		}
		lastID = batch[len(batch)-1].Id
	}
}

func GetRedemptionById(id int) (*Redemption, error) {
	if id == 0 {
		return nil, common.NewMessage("ID is empty")
	}
	redemption := Redemption{Id: id}
	var err error = nil
	err = DB.First(&redemption, "id = ?", id).Error
	if err == nil {
		err = attachRedemptionPlanTitles(DB, []*Redemption{&redemption})
	}
	return &redemption, err
}

type RedemptionResult struct {
	Type               string `json:"type"`
	Quota              int    `json:"quota"`
	PlanId             int    `json:"plan_id,omitempty"`
	PlanTitle          string `json:"plan_title,omitempty"`
	UserSubscriptionId int    `json:"user_subscription_id,omitempty"`
}

func creditRedeemedWelfareQuotaTx(tx *gorm.DB, userId int, quota int, configuredExpiresAt *int64) error {
	if userId <= 0 || quota <= 0 || quota > common.MaxQuota {
		return errors.New("invalid welfare redemption quota")
	}

	var user User
	if err := lockForUpdate(tx).
		Select("id", "welfare_quota", "welfare_expires_at", "welfare_version").
		Where("id = ?", userId).
		First(&user).Error; err != nil {
		return err
	}

	now := common.GetTimestamp()
	targetExpiresAt := user.WelfareExpiresAt
	if configuredExpiresAt != nil {
		if *configuredExpiresAt < 0 || (*configuredExpiresAt > 0 && *configuredExpiresAt <= now) {
			return ErrWelfareRedemptionExpired
		}
		targetExpiresAt = *configuredExpiresAt
	}
	version := user.WelfareVersion
	if version <= 0 {
		version = 1
	}
	expired := user.WelfareExpiresAt > 0 && user.WelfareExpiresAt <= now
	if configuredExpiresAt == nil && expired {
		targetExpiresAt = 0
	}
	if expired || user.WelfareQuota == 0 {
		result := tx.Model(&User{}).
			Where("id = ? AND welfare_version = ? AND (welfare_quota = 0 OR (welfare_expires_at != 0 AND welfare_expires_at <= ?))", userId, user.WelfareVersion, now).
			Updates(map[string]interface{}{
				"welfare_quota":      quota,
				"welfare_expires_at": targetExpiresAt,
				"welfare_version":    version + 1,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrWelfareQuotaNotApplied
		}
		return nil
	}
	if configuredExpiresAt != nil && user.WelfareExpiresAt != targetExpiresAt {
		return ErrWelfareQuotaExpiryConflict
	}
	if user.WelfareQuota > common.MaxQuota-quota {
		return ErrWelfareQuotaNotApplied
	}

	result := tx.Model(&User{}).
		Where("id = ? AND welfare_version = ? AND welfare_quota <= ? AND welfare_expires_at = ? AND (welfare_expires_at = 0 OR welfare_expires_at > ?)",
			userId, user.WelfareVersion, common.MaxQuota-quota, targetExpiresAt, now).
		Updates(map[string]interface{}{
			"welfare_quota":   gorm.Expr("welfare_quota + ?", quota),
			"welfare_version": version,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrWelfareQuotaNotApplied
	}
	return nil
}

func claimRedemptionBatch(tx *gorm.DB, redemption *Redemption, userId int) error {
	if redemption.BatchId == "" || redemption.AllowSameUser {
		return nil
	}
	claim := RedemptionBatchClaim{
		BatchId:      redemption.BatchId,
		UserId:       userId,
		RedemptionId: redemption.Id,
		CreatedTime:  common.GetTimestamp(),
	}
	result := tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "batch_id"}, {Name: "user_id"}},
		DoNothing: true,
	}).Create(&claim)
	if result.Error != nil {
		return result.Error
	}
	var persistedClaim RedemptionBatchClaim
	if err := tx.Where("batch_id = ? AND user_id = ?", redemption.BatchId, userId).First(&persistedClaim).Error; err != nil {
		return err
	}
	if persistedClaim.RedemptionId != redemption.Id {
		return ErrRedemptionBatchAlreadyClaimed
	}
	return nil
}

func Redeem(key string, userId int) (quota int, err error) {
	result, err := RedeemWithResult(key, userId)
	return result.Quota, err
}

func RedeemWithResult(key string, userId int) (redeemResult RedemptionResult, err error) {
	if key == "" {
		return redeemResult, errors.New("redemption code not provided")
	}
	if userId == 0 {
		return redeemResult, errors.New("invalid user id")
	}
	redemption := &Redemption{}
	groupChanged := false

	keyCol := "`key`"
	if common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		keyCol = `"key"`
	}
	common.RandomSleep()
	err = DB.Transaction(func(tx *gorm.DB) error {
		err := lockForUpdate(tx).Where(keyCol+" = ?", key).First(redemption).Error
		if err != nil {
			return errors.New("invalid redemption code")
		}
		if redemption.Status != common.RedemptionCodeStatusEnabled {
			return errors.New("redemption code already used")
		}
		if redemption.ExpiredTime != 0 && redemption.ExpiredTime < common.GetTimestamp() {
			return errors.New("redemption code expired")
		}
		redemption.Type = NormalizeRedemptionType(redemption.Type)
		if redemption.Type == "" {
			return errors.New("无效的兑换码类型")
		}
		if redemption.Type != RedemptionTypeSubscription && (redemption.Quota <= 0 || redemption.Quota > common.MaxQuota) {
			return errors.New("无效的兑换码额度")
		}
		var plan SubscriptionPlan
		if redemption.Type == RedemptionTypeSubscription {
			if redemption.PlanId <= 0 {
				return errors.New("兑换码未绑定订阅套餐")
			}
			if err := tx.Where("id = ?", redemption.PlanId).First(&plan).Error; err != nil {
				return errors.New("兑换码绑定的订阅套餐不存在")
			}
			// A disabled plan must not be grantable: the operator withdrew it from
			// sale, and the redemption path is the only grant path that skips this.
			if !plan.Enabled {
				return errors.New("兑换码绑定的订阅套餐已停用")
			}
			plan.NormalizeDefaults()
		}
		if err := claimRedemptionBatch(tx, redemption, userId); err != nil {
			return err
		}
		// Compare-and-swap on status: only the transaction that flips
		// enabled -> used may credit quota, so a concurrent redeem of the
		// same code loses here even without a row lock (e.g. on SQLite).
		result := tx.Model(&Redemption{}).
			Where("id = ? AND status = ?", redemption.Id, common.RedemptionCodeStatusEnabled).
			Updates(map[string]any{
				"redeemed_time": common.GetTimestamp(),
				"status":        common.RedemptionCodeStatusUsed,
				"used_user_id":  userId,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return errors.New("redemption code already used")
		}

		redeemResult.Type = redemption.Type
		if redemption.Type == RedemptionTypeSubscription {
			subscription, err := CreateUserSubscriptionFromPlanTx(tx, userId, &plan, "redemption")
			if err != nil {
				return err
			}
			redeemResult.PlanId = plan.Id
			redeemResult.PlanTitle = plan.Title
			redeemResult.UserSubscriptionId = subscription.Id
			groupChanged = subscription.PrevUserGroup != ""
			return nil
		}

		redeemResult.Quota = redemption.Quota
		if redemption.Type == RedemptionTypeWelfare {
			return creditRedeemedWelfareQuotaTx(tx, userId, redemption.Quota, redemption.WelfareExpiresAt)
		}
		return creditTopUpQuota(tx, userId, redemption.Quota, nil)
	})
	if err != nil {
		common.SysError(common.LogText("redemption failed: %s", err.Error()))
		if errors.Is(err, ErrRedemptionBatchAlreadyClaimed) {
			return RedemptionResult{}, ErrRedemptionBatchAlreadyClaimed
		}
		if errors.Is(err, ErrWelfareQuotaExpiryConflict) {
			return RedemptionResult{}, ErrWelfareQuotaExpiryConflict
		}
		if errors.Is(err, ErrWelfareRedemptionExpired) {
			return RedemptionResult{}, ErrWelfareRedemptionExpired
		}
		return RedemptionResult{}, ErrRedeemFailed
	}
	if groupChanged {
		refreshSubscriptionUserGroupCache(userId, "subscription redemption")
	}
	if redeemResult.Type == RedemptionTypeSubscription {
		RecordLog(userId, LogTypeTopup, common.NewMessage("Redeemed subscription plan {{plan}} with a redemption code, code ID {{id}}, subscription ID {{subscription_id}}", map[string]any{"plan": redeemResult.PlanTitle, "id": redemption.Id, "subscription_id": redeemResult.UserSubscriptionId}))
		return redeemResult, nil
	}
	if redeemResult.Type == RedemptionTypeWelfare {
		RecordLog(userId, LogTypeTopup, common.NewMessage("Added {{quota}} to welfare balance with a redemption code, code ID {{id}}", map[string]any{"quota": logger.FormatQuota(redemption.Quota), "id": redemption.Id}))
		return redeemResult, nil
	}
	syncCreditUserQuotaCache(userId, redemption.Quota, common.LogText("redemption"))
	RecordLog(userId, LogTypeTopup, common.NewMessage("Topped up {{quota}} with a redemption code, code ID {{id}}", map[string]any{"quota": logger.FormatQuota(redemption.Quota), "id": redemption.Id}))
	return redeemResult, nil
}

func (redemption *Redemption) Insert() error {
	if redemption.Quota <= 0 {
		return errors.New("redemption quota must be positive")
	}
	if err := common.ValidateWalletQuota(redemption.Quota); err != nil {
		return err
	}
	var err error
	err = DB.Create(redemption).Error
	return err
}

func InsertRedemptions(redemptions []Redemption) error {
	if len(redemptions) == 0 {
		return errors.New("redemptions are empty")
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		return tx.Create(&redemptions).Error
	})
}

func (redemption *Redemption) SelectUpdate() error {
	// This can update zero values
	return DB.Model(redemption).Select("redeemed_time", "status").Updates(redemption).Error
}

// Update Make sure your token's fields is completed, because this will update non-zero values
func (redemption *Redemption) Update() error {
	if redemption.Quota <= 0 {
		return errors.New("redemption quota must be positive")
	}
	if err := common.ValidateWalletQuota(redemption.Quota); err != nil {
		return err
	}
	var err error
	err = DB.Model(redemption).Select("name", "status", "quota", "type", "plan_id", "welfare_expires_at", "redeemed_time", "expired_time").Updates(redemption).Error
	return err
}

func (redemption *Redemption) Delete() error {
	var err error
	err = DB.Delete(redemption).Error
	return err
}

func DeleteRedemptionById(id int) (err error) {
	if id == 0 {
		return common.NewMessage("ID is empty")
	}
	redemption := Redemption{Id: id}
	err = DB.Where(redemption).First(&redemption).Error
	if err != nil {
		return err
	}
	return redemption.Delete()
}

func DeleteInvalidRedemptions() (int64, error) {
	now := common.GetTimestamp()
	result := DB.Where("status IN ? OR (status = ? AND expired_time != 0 AND expired_time < ?)", []int{common.RedemptionCodeStatusUsed, common.RedemptionCodeStatusDisabled}, common.RedemptionCodeStatusEnabled, now).Delete(&Redemption{})
	return result.RowsAffected, result.Error
}

// BatchDeleteRedemptions soft-deletes the selected codes in one statement.
func BatchDeleteRedemptions(ids []int) (int64, error) {
	if len(ids) == 0 || len(ids) > 1000 {
		return 0, errors.New("select between 1 and 1000 redemption codes")
	}
	for _, id := range ids {
		if id <= 0 {
			return 0, errors.New("redemption IDs must be positive")
		}
	}
	result := DB.Where("id IN ?", ids).Delete(&Redemption{})
	return result.RowsAffected, result.Error
}
