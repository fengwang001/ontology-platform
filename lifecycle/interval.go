package lifecycle

// Interval 表示对象的一段存活区间。
// 每段区间拥有自身标识；审计事件通过 IntervalID 精确归属到某一段。
type Interval struct {
	ID        string
	ObjectID  string
	Seq       int64 // 同一对象内从 1 单调递增
	StartedAt int64
	EndedAt   int64 // 0 表示当前仍存活；否则为关闭该区间的关闭时点
}

// AliveAt 报告该区间是否覆盖逻辑时钟 tick。
func (in *Interval) AliveAt(tick int64) bool {
	return tick >= in.StartedAt && (in.EndedAt == 0 || tick <= in.EndedAt)
}
