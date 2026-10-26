package controller

import (
	"bufio"
	"net/http"
	"strconv"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
)

func GetAllRedemptions(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	redemptions, total, err := model.GetAllRedemptions(pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(redemptions)
	common.ApiSuccess(c, pageInfo)
	return
}

func SearchRedemptions(c *gin.Context) {
	keyword := c.Query("keyword")
	status := c.Query("status")
	redemptionType := c.Query("type")
	pageInfo := common.GetPageQuery(c)
	redemptions, total, err := model.SearchRedemptionsWithType(keyword, status, redemptionType, pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(redemptions)
	common.ApiSuccess(c, pageInfo)
	return
}

// ExportRedemptions writes one redemption key per line. The same keyword and
// status filters used by the management table are applied before exporting.
func ExportRedemptions(c *gin.Context) {
	c.Header("Content-Disposition", `attachment; filename="redemption-codes.txt"`)
	c.Header("Content-Type", "text/plain; charset=utf-8")
	writer := bufio.NewWriter(c.Writer)
	err := model.IterateRedemptionKeysForExport(c.Query("keyword"), c.Query("status"), c.Query("type"), 1000, func(batch []model.Redemption) error {
		for _, redemption := range batch {
			if _, err := writer.WriteString(redemption.Key + "\n"); err != nil {
				return err
			}
		}
		return writer.Flush()
	})
	if err != nil {
		common.SysError("failed to export redemptions: " + err.Error())
	}
}

func GetRedemption(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	redemption, err := model.GetRedemptionById(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    redemption,
	})
	return
}

func normalizeAndValidateRedemptionBenefit(c *gin.Context, redemption *model.Redemption) bool {
	redemption.Type = model.NormalizeRedemptionType(redemption.Type)
	switch redemption.Type {
	case model.RedemptionTypeQuota:
		if redemption.Quota <= 0 || redemption.Quota > common.MaxQuota {
			common.ApiErrorT(c, "Invalid parameters")
			return false
		}
		redemption.PlanId = 0
		redemption.WelfareExpiresAt = nil
		return true
	case model.RedemptionTypeWelfare:
		if redemption.Quota <= 0 || redemption.Quota > common.MaxQuota {
			common.ApiErrorT(c, "Invalid parameters")
			return false
		}
		redemption.PlanId = 0
		if redemption.WelfareExpiresAt == nil {
			return true
		}
		welfareExpiresAt := *redemption.WelfareExpiresAt
		if welfareExpiresAt < 0 || (welfareExpiresAt > 0 && welfareExpiresAt <= common.GetTimestamp()) {
			common.ApiErrorT(c, "Welfare balance expiration must be in the future")
			return false
		}
		if welfareExpiresAt > 0 && (redemption.ExpiredTime == 0 || redemption.ExpiredTime >= welfareExpiresAt) {
			common.ApiErrorT(c, "Welfare balance expiration must be after the redemption code expiration")
			return false
		}
		return true
	case model.RedemptionTypeSubscription:
		if redemption.PlanId <= 0 {
			common.ApiErrorMsg(c, "请选择订阅套餐")
			return false
		}
		plan, err := model.GetSubscriptionPlanById(redemption.PlanId)
		if err != nil {
			common.ApiErrorMsg(c, "订阅套餐不存在")
			return false
		}
		if !plan.Enabled {
			common.ApiErrorMsg(c, "订阅套餐已停用")
			return false
		}
		redemption.Quota = 0
		redemption.WelfareExpiresAt = nil
		redemption.PlanTitle = plan.Title
		return true
	default:
		common.ApiErrorMsg(c, "无效的兑换码类型")
		return false
	}
}

func AddRedemption(c *gin.Context) {
	if !operation_setting.IsPaymentComplianceConfirmed() {
		common.ApiErrorT(c, "Payment, redemption, subscription, and invitation reward features are disabled. The administrator must confirm compliance terms before enabling them.")
		return
	}

	redemption := model.Redemption{}
	err := c.ShouldBindJSON(&redemption)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if utf8.RuneCountInString(redemption.Name) == 0 || utf8.RuneCountInString(redemption.Name) > 20 {
		common.ApiErrorT(c, "Redemption code name length must be between 1-20")
		return
	}
	if redemption.Count <= 0 {
		common.ApiErrorT(c, "Redemption code count must be greater than 0")
		return
	}
	if redemption.Count > 100 {
		common.ApiErrorT(c, "Maximum 100 redemption codes can be generated at once")
		return
	}
	if redemption.Quota <= 0 {
		common.ApiErrorT(c, "Redemption quota must be positive")
		return
	}
	if err := common.ValidateWalletQuota(redemption.Quota); err != nil {
		common.ApiError(c, err)
		return
	}
	if err := validateExpiredTime(redemption.ExpiredTime); err != nil {
		common.ApiError(c, err)
		return
	}
	if !normalizeAndValidateRedemptionBenefit(c, &redemption) {
		return
	}
	batchId := common.GetUUID()
	keys := make([]string, 0, redemption.Count)
	redemptions := make([]model.Redemption, 0, redemption.Count)
	for i := 0; i < redemption.Count; i++ {
		key := common.GetUUID()
		redemptions = append(redemptions, model.Redemption{
			UserId:           c.GetInt("id"),
			Name:             redemption.Name,
			Key:              key,
			CreatedTime:      common.GetTimestamp(),
			Quota:            redemption.Quota,
			Type:             redemption.Type,
			PlanId:           redemption.PlanId,
			BatchId:          batchId,
			AllowSameUser:    redemption.AllowSameUser,
			WelfareExpiresAt: redemption.WelfareExpiresAt,
			ExpiredTime:      redemption.ExpiredTime,
		})
		keys = append(keys, key)
	}
	if err = model.InsertRedemptions(redemptions); err != nil {
		common.SysError(common.LogText("failed to insert redemption batch: %s", err.Error()))
		common.ApiErrorStatus(c, http.StatusOK, common.NewMessage("Failed to create redemption code, please try again later"), gin.H{"data": []string{}})
		return
	}
	recordManageAudit(c, "redemption.create", map[string]any{
		"name":               redemption.Name,
		"count":              redemption.Count,
		"type":               redemption.Type,
		"quota":              logger.FormatQuota(redemption.Quota),
		"plan_id":            redemption.PlanId,
		"batch_id":           batchId,
		"allow_same_user":    redemption.AllowSameUser,
		"welfare_expires_at": redemption.WelfareExpiresAt,
	})
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    keys,
	})
	return
}

func DeleteRedemption(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	err := model.DeleteRedemptionById(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
	return
}

func UpdateRedemption(c *gin.Context) {
	statusOnly := c.Query("status_only")
	redemption := model.Redemption{}
	err := c.ShouldBindJSON(&redemption)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	cleanRedemption, err := model.GetRedemptionById(redemption.Id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if statusOnly == "" {
		if redemption.Quota <= 0 {
			common.ApiErrorT(c, "Redemption quota must be positive")
			return
		}
		if err := common.ValidateWalletQuota(redemption.Quota); err != nil {
			common.ApiError(c, err)
			return
		}
		if err := validateExpiredTime(redemption.ExpiredTime); err != nil {
			common.ApiError(c, err)
			return
		}
		if !normalizeAndValidateRedemptionBenefit(c, &redemption) {
			return
		}
		// If you add more fields, please also update redemption.Update()
		cleanRedemption.Name = redemption.Name
		cleanRedemption.Quota = redemption.Quota
		cleanRedemption.Type = redemption.Type
		cleanRedemption.PlanId = redemption.PlanId
		cleanRedemption.PlanTitle = redemption.PlanTitle
		cleanRedemption.WelfareExpiresAt = redemption.WelfareExpiresAt
		cleanRedemption.ExpiredTime = redemption.ExpiredTime
	}
	if statusOnly != "" {
		cleanRedemption.Status = redemption.Status
	}
	err = cleanRedemption.Update()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    cleanRedemption,
	})
	return
}

func DeleteInvalidRedemption(c *gin.Context) {
	rows, err := model.DeleteInvalidRedemptions()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    rows,
	})
	return
}

func validateExpiredTime(expired int64) error {
	if expired != 0 && expired < common.GetTimestamp() {
		return common.NewMessage("Expiration time cannot be earlier than current time")
	}
	return nil
}

func DeleteRedemptionBatch(c *gin.Context) {
	var request struct {
		Ids []int `json:"ids" binding:"required,min=1,max=1000,dive,gt=0"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	count, err := model.BatchDeleteRedemptions(request.Ids)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "redemption.delete_batch", map[string]any{
		"count":                    count,
		"total":                    len(request.Ids),
		"requested_redemption_ids": request.Ids,
	})
	common.ApiSuccess(c, count)
}
