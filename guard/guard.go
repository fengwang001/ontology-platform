// Package guard 维护单个分区的批量删除熔断状态（Suspect）与人工放行判定。
// 本包不加锁：所有方法都假定调用方（scan）持有该分区的分片锁。
package guard

import "errors"

var (
	// ErrNotSuspect 表示对非 Suspect 分区请求放行。
	ErrNotSuspect = errors.New("guard: partition is not suspect")
	// ErrUnauthorized 表示放行角色不等于 2。
	ErrUnauthorized = errors.New("guard: approve role must be 2")
)

// Guard 是单分区熔断状态机。
type Guard struct {
	suspect bool
}

// New 创建一个初始非熔断的 Guard。
func New() *Guard {
	return &Guard{}
}

// Suspected 返回当前是否处于熔断状态。
func (g *Guard) Suspected() bool {
	return g.suspect
}

// ShouldTrip 按 |C|·100 > X·n0 严格判定是否熔断；n0=0 时恒为假。
func (g *Guard) ShouldTrip(candCount, n0, xPercent int) bool {
	if n0 <= 0 {
		return false
	}
	return int64(candCount)*100 > int64(xPercent)*int64(n0)
}

// Trip 置熔断标志。
func (g *Guard) Trip() {
	g.suspect = true
}

// Release 校验放行：role 必须为 2（否则 ErrUnauthorized），分区必须为
// Suspect（否则 ErrNotSuspect）；通过则清除 Suspect，调用方随后执行删除。
func (g *Guard) Release(role int) error {
	if role != 2 {
		return ErrUnauthorized
	}
	if !g.suspect {
		return ErrNotSuspect
	}
	g.suspect = false
	return nil
}
