package model

import (
	"errors"
	"time"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
)

var (
	ErrInsufficientWelfareQuota = errors.New("welfare quota insufficient or expired")
	ErrWelfareQuotaNotApplied   = errors.New("welfare quota adjustment was not applied")
)

type WelfareQuotaState struct {
	Quota     int   `json:"quota"`
	ExpiresAt int64 `json:"expires_at"`
	Version   int64 `json:"version"`
}

func (user *User) AfterFind(_ *gorm.DB) error {
	if user.WelfareExpiresAt > 0 && user.WelfareExpiresAt <= common.GetTimestamp() {
		user.WelfareQuota = 0
	}
	return nil
}

func GetUserWelfareQuota(userId int) (WelfareQuotaState, error) {
	var state WelfareQuotaState
	result := DB.Model(&User{}).
		Where("id = ?", userId).
		Select("welfare_quota AS quota", "welfare_expires_at AS expires_at", "welfare_version AS version").
		Scan(&state)
	if result.Error != nil {
		return WelfareQuotaState{}, result.Error
	}
	if result.RowsAffected == 0 {
		return WelfareQuotaState{}, gorm.ErrRecordNotFound
	}
	if state.ExpiresAt > 0 && state.ExpiresAt <= common.GetTimestamp() {
		state.Quota = 0
	}
	return state, nil
}

// TryReserveUserWelfareQuota atomically deducts an unexpired welfare balance.
func TryReserveUserWelfareQuota(userId int, quota int) (bool, error) {
	_, reserved, err := TryReserveUserWelfareQuotaVersioned(userId, quota)
	return reserved, err
}

// TryReserveUserWelfareQuotaVersioned atomically reserves quota from the
// currently active grant and returns the grant version used by the reservation.
func TryReserveUserWelfareQuotaVersioned(userId int, quota int) (int64, bool, error) {
	if userId <= 0 || quota < 0 || quota > common.MaxQuota {
		return 0, false, errors.New("invalid welfare quota reservation")
	}
	if quota == 0 {
		state, err := GetUserWelfareQuota(userId)
		return state.Version, err == nil, err
	}

	state, err := GetUserWelfareQuota(userId)
	if err != nil {
		return 0, false, err
	}
	if state.Quota < quota {
		return state.Version, false, nil
	}
	result := DB.Model(&User{}).
		Where("id = ? AND welfare_version = ? AND welfare_quota >= ? AND (welfare_expires_at = 0 OR welfare_expires_at > ?)",
			userId, state.Version, quota, common.GetTimestamp()).
		Update("welfare_quota", gorm.Expr("welfare_quota - ?", quota))
	return state.Version, result.RowsAffected == 1, result.Error
}

// IncreaseUserWelfareQuotaForVersion refunds quota only to the grant batch
// that funded the request. A newer administrator override always wins.
func IncreaseUserWelfareQuotaForVersion(userId int, quota int, version int64) (bool, error) {
	if userId <= 0 || quota < 0 || quota > common.MaxQuota || version <= 0 {
		return false, errors.New("invalid welfare quota adjustment")
	}
	if quota == 0 {
		return true, nil
	}
	result := DB.Model(&User{}).
		Where("id = ? AND welfare_version = ? AND welfare_quota <= ? AND (welfare_expires_at = 0 OR welfare_expires_at > ?)",
			userId, version, common.MaxQuota-quota, common.GetTimestamp()).
		Update("welfare_quota", gorm.Expr("welfare_quota + ?", quota))
	return result.RowsAffected == 1, result.Error
}

func IncreaseUserWelfareQuota(userId int, quota int) error {
	if userId <= 0 || quota < 0 || quota > common.MaxQuota {
		return errors.New("invalid welfare quota adjustment")
	}
	if quota == 0 {
		return nil
	}
	state, err := GetUserWelfareQuota(userId)
	if err != nil {
		return err
	}
	applied, err := IncreaseUserWelfareQuotaForVersion(userId, quota, state.Version)
	if err != nil {
		return err
	}
	if !applied {
		return ErrWelfareQuotaNotApplied
	}
	return nil
}

func DecreaseUserWelfareQuotaForVersion(userId int, quota int, version int64) (bool, error) {
	if userId <= 0 || quota < 0 || quota > common.MaxQuota || version <= 0 {
		return false, errors.New("invalid welfare quota adjustment")
	}
	if quota == 0 {
		return true, nil
	}
	result := DB.Model(&User{}).
		Where("id = ? AND welfare_version = ? AND welfare_quota >= ? AND (welfare_expires_at = 0 OR welfare_expires_at > ?)",
			userId, version, quota, common.GetTimestamp()).
		Update("welfare_quota", gorm.Expr("welfare_quota - ?", quota))
	return result.RowsAffected == 1, result.Error
}

func DecreaseUserWelfareQuota(userId int, quota int) error {
	version, reserved, err := TryReserveUserWelfareQuotaVersioned(userId, quota)
	if err != nil {
		return err
	}
	if !reserved {
		return ErrInsufficientWelfareQuota
	}
	_ = version
	return nil
}

func SetUserWelfareQuota(userId int, quota int, expiresAt int64) error {
	if userId <= 0 || quota < 0 || quota > common.MaxQuota || expiresAt < 0 {
		return errors.New("invalid welfare quota settings")
	}
	if quota > 0 && expiresAt > 0 && expiresAt <= time.Now().Unix() {
		return errors.New("welfare quota expiry must be in the future")
	}
	result := DB.Model(&User{}).Where("id = ?", userId).Updates(map[string]interface{}{
		"welfare_quota":      quota,
		"welfare_expires_at": expiresAt,
		"welfare_version":    gorm.Expr("welfare_version + ?", 1),
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return gorm.ErrRecordNotFound
	}
	return nil
}
