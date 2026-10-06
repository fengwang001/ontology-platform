// Package aml 实现反洗钱现金存款结构化拆分检测与报告。
package aml

import "fmt"

// Config 为系统配置。金额与阈值为正整数，天数为正整数天。
type Config struct {
	Low  int64 // 小额下限 L：金额不小于 L 且小于 High 的存款参与结构化判定
	High int64 // 大额阈值 H：单笔不小于 H 立即产生大额报告
	K    int   // 结构化判定所需的最少笔数，须不小于 2
	D    int64 // 窗口天数：存款日期与 now 之差小于 D 视为在窗口内
}

// Validate 校验配置，不满足 0 < L < H、K >= 2、D >= 1 时返回 ErrInvalidParam。
func (c Config) Validate() error {
	if c.Low <= 0 || c.High <= 0 || c.Low >= c.High {
		return fmt.Errorf("%w: 须满足 0 < L < H (L=%d, H=%d)", ErrInvalidParam, c.Low, c.High)
	}
	if c.K < 2 {
		return fmt.Errorf("%w: K 须不小于 2 (K=%d)", ErrInvalidParam, c.K)
	}
	if c.D < 1 {
		return fmt.Errorf("%w: D 须不小于 1 (D=%d)", ErrInvalidParam, c.D)
	}
	return nil
}
