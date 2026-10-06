package rollout

import "errors"

// Config 是灰度放量的静态配置，Reset 不会清除它。
//
//	Ratios       放量阶段比例（万分之一），严格递增，每项属于 [1,10000]，至少一项
//	DwellMs      每阶段最短驻留时间 D（毫秒）
//	MinCanary    评估所需灰度最少请求数 G（>= 1）
//	ToleranceBP  错误率容差 T（万分之一，[0,10000]）
//	MaxFailures  连续失败次数上限 F（>= 1）
//	StickyMs     会话粘性时长 L（毫秒，0 表示粘性立即过期）
//	MaxSticky    粘性记录条数上限 P（>= 0，0 表示不保留任何粘性记录）
type Config struct {
	Ratios      []int
	DwellMs     int64
	MinCanary   int
	ToleranceBP int
	MaxFailures int
	StickyMs    int64
	MaxSticky   int
}

// 错误优先级固定：参数非法 > 时钟回退 > 状态不允许。
// 被拒绝的操作不改变任何状态（包括已注入时钟）。
var (
	ErrInvalidArgument = errors.New("rollout: invalid argument")
	ErrClockRewound    = errors.New("rollout: clock moved backwards")
	ErrIllegalState    = errors.New("rollout: operation not allowed in current state")
)

func (c Config) validate() error {
	if len(c.Ratios) == 0 {
		return ErrInvalidArgument
	}
	prev := 0
	for _, r := range c.Ratios {
		if r < 1 || r > 10000 || r <= prev {
			return ErrInvalidArgument
		}
		prev = r
	}
	if c.DwellMs < 0 || c.StickyMs < 0 {
		return ErrInvalidArgument
	}
	if c.MinCanary < 1 || c.MaxFailures < 1 {
		return ErrInvalidArgument
	}
	if c.ToleranceBP < 0 || c.ToleranceBP > 10000 {
		return ErrInvalidArgument
	}
	if c.MaxSticky < 0 {
		return ErrInvalidArgument
	}
	return nil
}
