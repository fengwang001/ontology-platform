package ncdengine

import "errors"

var (
	ErrInvalidArgument      = errors.New("参数非法")
	ErrCustomerNotFound     = errors.New("被保人不存在")
	ErrClockMovedBack       = errors.New("时钟回退")
	ErrActivePolicyExists   = errors.New("已有在保保单")
	ErrClaimExists          = errors.New("事故已存在")
	ErrClaimNotFound        = errors.New("事故不存在")
	ErrAccidentUncovered    = errors.New("事故日未承保")
	ErrLevelTooLow          = errors.New("等级不足")
	ErrProtectionPurchased  = errors.New("保护已购买")
	ErrOutsideRenewalWindow = errors.New("续保窗口外")
)
