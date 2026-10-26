package model

import "errors"

// Common errors
var (
	ErrDatabase = errors.New("database error")
)

// User auth errors
var (
	ErrInvalidCredentials   = errors.New("invalid credentials")
	ErrUserEmptyCredentials = errors.New("empty credentials")
	ErrEmailAlreadyTaken    = errors.New("email already taken")
	ErrRegistrationIPUsed   = errors.New("registration IP already used")
	ErrEmailNotFound        = errors.New("email not found")
	ErrEmailAmbiguous       = errors.New("email matches multiple users")
)

// Token auth errors
var (
	ErrTokenNotProvided = errors.New("token not provided")
	ErrTokenInvalid     = errors.New("token invalid")
)

// Redemption errors
var (
	ErrRedeemFailed                  = errors.New("redeem.failed")
	ErrRedemptionBatchAlreadyClaimed = errors.New("redemption batch already claimed by user")
	ErrWelfareQuotaExpiryConflict    = errors.New("welfare quota expiration conflicts with active balance")
	ErrWelfareRedemptionExpired      = errors.New("welfare redemption balance expiration has passed")
)

// 2FA errors
var ErrTwoFANotEnabled = errors.New("2fa not enabled")
var ErrTwoFAAlreadyEnabled = errors.New("2fa already enabled")
