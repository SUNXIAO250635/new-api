package service

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/require"
)

func ensureSubscriptionConcurrencyLeaseTable(t *testing.T) {
	t.Helper()
	require.NoError(t, model.DB.AutoMigrate(&model.SubscriptionConcurrencyLease{}))
}

func TestBillingSessionSettleReleasesSubscriptionConcurrencyLease(t *testing.T) {
	ensureSubscriptionConcurrencyLeaseTable(t)
	requestID := "service-settle-release"
	model.DB.Where("request_id = ?", requestID).Delete(&model.SubscriptionConcurrencyLease{})
	require.NoError(t, model.DB.Create(&model.SubscriptionConcurrencyLease{
		RequestId:          requestID,
		UserId:             1,
		UserSubscriptionId: 1,
		ExpiresAt:          model.GetDBTimestamp() + 900,
	}).Error)

	session := &BillingSession{
		relayInfo: &relaycommon.RelayInfo{UserId: 1},
		funding: &SubscriptionFunding{
			requestId:        requestID,
			concurrencyLimit: 1,
		},
	}
	require.NoError(t, session.Settle(0))

	var count int64
	require.NoError(t, model.DB.Model(&model.SubscriptionConcurrencyLease{}).
		Where("request_id = ?", requestID).Count(&count).Error)
	require.Zero(t, count)
}

func TestBillingSessionRefundReleasesSubscriptionConcurrencyLeaseSynchronously(t *testing.T) {
	ensureSubscriptionConcurrencyLeaseTable(t)
	requestID := "service-refund-release"
	model.DB.Where("request_id = ?", requestID).Delete(&model.SubscriptionConcurrencyLease{})
	require.NoError(t, model.DB.Create(&model.SubscriptionConcurrencyLease{
		RequestId:          requestID,
		UserId:             2,
		UserSubscriptionId: 2,
		ExpiresAt:          model.GetDBTimestamp() + 900,
	}).Error)

	session := &BillingSession{
		relayInfo: &relaycommon.RelayInfo{UserId: 2},
		funding: &SubscriptionFunding{
			requestId:        requestID,
			concurrencyLimit: 1,
		},
	}
	session.Refund(nil)

	var count int64
	require.NoError(t, model.DB.Model(&model.SubscriptionConcurrencyLease{}).
		Where("request_id = ?", requestID).Count(&count).Error)
	require.Zero(t, count)
	// Repeated refund remains a no-op and does not recreate the lease.
	session.Refund(nil)
}
