package service

import (
	"context"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBillingSessionRefundIsDurableAndIdempotent(t *testing.T) {
	truncate(t)
	user := model.User{Id: 7101, Username: "durable-refund", Status: common.UserStatusEnabled, Quota: 940}
	token := model.Token{Id: 7101, UserId: user.Id, Key: "durable-refund-token", RemainQuota: 940, UsedQuota: 60}
	require.NoError(t, model.DB.Create(&user).Error)
	require.NoError(t, model.DB.Create(&token).Error)

	session := &BillingSession{
		relayInfo:       &relaycommon.RelayInfo{RequestId: "durable-refund-request", UserId: user.Id, TokenId: token.Id, TokenKey: token.Key},
		funding:         &WalletFunding{userId: user.Id, consumed: 60},
		fundingConsumed: 60,
		tokenConsumed:   60,
	}
	session.Refund(nil)
	session.Refund(nil)

	require.NoError(t, model.DB.First(&user, user.Id).Error)
	require.NoError(t, model.DB.First(&token, token.Id).Error)
	assert.Equal(t, 1000, user.Quota)
	assert.Equal(t, 1000, token.RemainQuota)
	assert.Zero(t, token.UsedQuota)
	var record model.BillingRefundRecord
	require.NoError(t, model.DB.Where("request_id = ?", "durable-refund-request").First(&record).Error)
	assert.Equal(t, model.BillingRefundStatusCompleted, record.Status)
	assert.True(t, record.FundingRefunded)
	assert.True(t, record.TokenRefunded)
}

func TestPendingBillingRefundRetriesAfterDependencyRecovers(t *testing.T) {
	truncate(t)
	_, err := model.CreateBillingRefundRecord(model.BillingRefundParams{
		RequestId:     "pending-refund-request",
		UserId:        7201,
		FundingSource: BillingSourceWallet,
		FundingQuota:  50,
	})
	require.NoError(t, err)
	_, err = model.ProcessBillingRefund("pending-refund-request")
	require.Error(t, err)
	model.RecordBillingRefundFailure("pending-refund-request", err)

	user := model.User{Id: 7201, Username: "pending-refund", Status: common.UserStatusEnabled, Quota: 100}
	require.NoError(t, model.DB.Create(&user).Error)
	assert.Equal(t, 1, runPendingBillingRefunds(context.Background(), 10))

	require.NoError(t, model.DB.First(&user, user.Id).Error)
	assert.Equal(t, 150, user.Quota)
	var record model.BillingRefundRecord
	require.NoError(t, model.DB.Where("request_id = ?", "pending-refund-request").First(&record).Error)
	assert.Equal(t, model.BillingRefundStatusCompleted, record.Status)
}
