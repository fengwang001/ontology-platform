package isr

// Clock 提供单调的时间读取（通常是 time.Now 的注入版本）。
type Clock func() int64

// Logger 记录每一步输入、同步副本集与判定依据。
type Logger interface {
	Printf(format string, args ...any)
}

// Replica 是单个副本的对外可观测状态快照。
type Replica struct {
	ID        string
	Progress  int64
	InISR     bool
	CaughtUpA int64
}

// Snapshot 是分区某一时刻的完整状态。
type Snapshot struct {
	Leader       string
	HighWatermark int64
	Replicas     []Replica
}

// Set 维护一个分区的同步副本集（ISR）与高水位。
// 所有方法可被多个 goroutine 并发调用。
type Set struct {
}

// New 创建同步副本集。初始时全部副本都在 ISR 中，位点均为 0。
func New(leader string, followers []string, lagToleranceNanos int64, now Clock, log Logger) (*Set, error) {
	return nil, ErrInvalidArgument
}

// Append 领导者追加 count 条记录，推进领导者日志结束位点。
func (s *Set) Append(leader string, count int64, nowNanos int64) (newEnd int64, err error) {
	return 0, ErrInvalidArgument
}

// Fetch 跟随者汇报自己已拥有到 offset 的日志。
// 追平时刷新追上时间；追上当前高水位且此前被移出的副本重新加入 ISR。
func (s *Set) Fetch(replica string, offset int64, nowNanos int64) error {
	return ErrInvalidArgument
}

// Reap 周期性检查：追上时间严格早于 nowNanos-lagTolerance 的副本被移出 ISR。
// 返回本次被移出的副本 ID。
func (s *Set) Reap(nowNanos int64) ([]string, error) {
	return nil, ErrInvalidArgument
}

// HighWatermark 返回当前高水位。
func (s *Set) HighWatermark() int64 { return 0 }

// InISR 判断副本是否在同步副本集中。
func (s *Set) InISR(replica string) (bool, error) { return false, ErrUnknownReplica }

// Committed 判断 offset 处（之前）的日志是否已提交：offset <= HW。
func (s *Set) Committed(offset int64) (bool, error) { return false, ErrInvalidOffset }

// Snapshot 返回完整状态副本（用于查询与测试对照）。
func (s *Set) Snapshot() Snapshot { return Snapshot{} }
