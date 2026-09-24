// Package api 是全序广播定序器的对外门面：固定方法签名，内部委托 tob。
// 依赖 ontology/tob（传递依赖 seq），无反向依赖。
package api

import "ontology/tob"

// Sequencer 对并发到达的消息分配全局严格递增 seq，并按 seq 无空洞投递。
type Sequencer struct {
	log *tob.Log
}

// New 返回初始定序器：nextSeq=1，deliveredUpTo=0。
func New() *Sequencer { return &Sequencer{log: tob.New()} }

// Propose 分配下一个 seq 并记入日志。空 payload 返回哨兵错误且不改状态。
func (s *Sequencer) Propose(payload string) (int, error) {
	return s.log.Propose(payload)
}

// Deliver 按序投递 seq==deliveredUpTo+1 的消息；空洞未补时 ok=false，不投递任何消息。
func (s *Sequencer) Deliver() (seq int, payload string, ok bool) {
	return s.log.Deliver()
}

// Crash 模拟崩溃：丢失 seq>deliveredUpTo 的内存条目，保留 nextSeq 与游标。
func (s *Sequencer) Crash() { s.log.Crash() }

// RePropose 按原始 seq 补发崩溃丢失的消息，绝不分配新 seq。
func (s *Sequencer) RePropose(seq int, payload string) error {
	return s.log.RePropose(seq, payload)
}

// Delivered 返回 deliveredUpTo，可被多 goroutine 并发调用。
func (s *Sequencer) Delivered() int { return s.log.Delivered() }

// SelfCheck 对内置操作序列核验四条不变量（含 O(1) 投递与大 m 档位）。
func (s *Sequencer) SelfCheck() {
	if err := s.log.SelfCheck(); err != nil {
		panic(err)
	}
}

// 透传哨兵错误，调用方可用 errors.Is 判定。
var (
	ErrEmptyPayload     = tob.ErrEmptyPayload
	ErrAlreadyDelivered = tob.ErrAlreadyDelivered
	ErrSeqOutOfRange    = tob.ErrSeqOutOfRange
	ErrSlotFilled       = tob.ErrSlotFilled
)
