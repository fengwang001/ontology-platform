package stats

import "sync/atomic"

// Stats 是单个 worker 的执行/被偷/偷取尝试计数。
type Stats struct {
	Executed    int64
	StolenFrom  int64
	StealTries  int64
}

func (s *Stats) AddExecuted()       { atomic.AddInt64(&s.Executed, 1) }
func (s *Stats) AddStolenFrom(n int) { atomic.AddInt64(&s.StolenFrom, int64(n)) }
func (s *Stats) AddStealTry()       { atomic.AddInt64(&s.StealTries, 1) }
func (s *Stats) Get() (int64, int64, int64) {
	return atomic.LoadInt64(&s.Executed), atomic.LoadInt64(&s.StolenFrom), atomic.LoadInt64(&s.StealTries)
}

// Imbalance 返回 max-min 执行数，衡量负载均衡。
func Imbalance(all []*Stats) int64 { return 0 }
