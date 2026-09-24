// Package drift 维护单个源的水位线状态与阈值边界判定。
// 不依赖任何其他包。
package drift

// Class 是一条水位线的分类结果，四类互斥。
type Class int

const (
	Normal Class = iota
	Drift
	Reorder
	Rollback
)

func (c Class) String() string {
	switch c {
	case Drift:
		return "Drift"
	case Reorder:
		return "Reorder"
	case Rollback:
		return "Rollback"
	default:
		return "Normal"
	}
}

// Source 是单源状态：最后接受的水位线 last 与两条阈值。
// last 只进不退：任何事件都不会让它减小。
type Source struct {
	last    int64
	hasLast bool
	drift   int64 // driftThreshold > 0
	tol     int64 // rollbackTolerance >= 0
}

// NewSource 构造单源状态。参数合法性由上层（wm）保证。
func NewSource(driftThreshold, rollbackTolerance int64) *Source {
	return &Source{drift: driftThreshold, tol: rollbackTolerance}
}

// Observe 用当前 last 按规则判类，并按规则推进 last。
// 边界：w == last+drift 是 Normal；last-w == tol 是 Reorder。
func (s *Source) Observe(w int64) Class {
	if !s.hasLast {
		s.last, s.hasLast = w, true
		return Normal
	}
	switch {
	case w > s.last+s.drift:
		s.last = w
		return Drift
	case w >= s.last:
		s.last = w
		return Normal
	case s.last-w <= s.tol:
		return Reorder // last 不变
	default:
		return Rollback // last 不变
	}
}

// Last 返回当前 last 与是否已有基线。
func (s *Source) Last() (int64, bool) { return s.last, s.hasLast }
