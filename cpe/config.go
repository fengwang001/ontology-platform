package cpe

// Config 为初始化给定的核算参数。
type Config struct {
	CycleLengthDays      int // 周期长度（天），>0
	TotalRequired        int // 周期总学分要求，>=0
	MandatoryMin         int // 必修类最低要求，>=0
	MandatoryCap         int // 必修类计入上限，>=0
	ElectiveCap          int // 选修类计入上限，>=0
	GraceDays            int // 宽限天数，>=0
	CarryoverCap         int // 结转上限，>=0
	CorrectionWindowDays int // 更正/撤销固定天数窗口，>=0
}

// Validate 校验配置参数合法性。
func (c Config) Validate() error {
	if c.CycleLengthDays <= 0 {
		return newErr(ErrInvalidParam, "CycleLengthDays must be positive")
	}
	if c.TotalRequired < 0 || c.MandatoryMin < 0 || c.MandatoryCap < 0 ||
		c.ElectiveCap < 0 || c.GraceDays < 0 || c.CarryoverCap < 0 ||
		c.CorrectionWindowDays < 0 {
		return newErr(ErrInvalidParam, "config values must be non-negative")
	}
	return nil
}
