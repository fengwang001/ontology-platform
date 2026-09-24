// Package api 是全序广播定序器的对外门面，只依赖 tob。哨兵错误直接
// 透传 tob 的四个互不相同错误，errors.Is 可判定。
package api

import (
	"errors"
	"fmt"
	"sync"

	"ontology/tob"
)

// 透传四类哨兵错误，调用方可用 errors.Is 判定且彼此互不相同。
var (
	ErrEmptyPayload     = tob.ErrEmptyPayload
	ErrAlreadyDelivered = tob.ErrAlreadyDelivered
	ErrSeqOutOfRange    = tob.ErrSeqOutOfRange
	ErrSlotFilled       = tob.ErrSlotFilled
)

// Sequencer 是线程安全的定序器；全部状态在进程内存。
type Sequencer struct {
	mu sync.Mutex
	l  *tob.Log
}

func New() *Sequencer { return &Sequencer{l: tob.New()} }

// Propose 分配全局严格递增 seq（从 1 起）并记入日志。
func (s *Sequencer) Propose(payload string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.l.Propose(payload)
}

// Deliver 投递 seq==deliveredUpTo+1 的消息；槽位缺失则 ok=false，不产生空洞。
func (s *Sequencer) Deliver() (int, string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.l.Deliver()
}

// Crash 丢失所有 seq>deliveredUpTo 的内存消息，nextSeq 与游标保留。
func (s *Sequencer) Crash() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.l.Crash()
}

// RePropose 按原始 seq 回填崩溃丢失的空槽，绝不重新分配 seq。
func (s *Sequencer) RePropose(seq int, payload string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.l.RePropose(seq, payload)
}

// Delivered 返回已按序投递到第几个（初始 0）。
func (s *Sequencer) Delivered() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.l.Delivered()
}

// drain 投递当前可连续投递的全部消息，返回内容序列。
func drain(l *tob.Log) []string {
	out := []string{}
	for {
		_, p, ok := l.Deliver()
		if !ok {
			return out
		}
		out = append(out, p)
	}
}

// distinct 核验四个错误两两不同。
func distinct(errs ...error) bool {
	for i := range errs {
		for j := i + 1; j < len(errs); j++ {
			if errs[i] == errs[j] || errors.Is(errs[i], errs[j]) {
				return false
			}
		}
	}
	return true
}

// SelfCheck 在独立实例上对一组内置操作序列核验四条不变量（不触碰接收者），
// 因而可被多个 goroutine 并发调用。
func (s *Sequencer) SelfCheck() error {
	if err := tob.SelfCheck(); err != nil { // I1 连续前缀、I3 单调、O(1) 下标
		return err
	}
	if !distinct(ErrEmptyPayload, ErrAlreadyDelivered, ErrSeqOutOfRange, ErrSlotFilled) {
		return errors.New("sentinel errors not distinct")
	}
	l := tob.New()
	for _, p := range []string{"A", "B", "C", "D", "E"} {
		if _, err := l.Propose(p); err != nil {
			return err
		}
	}
	if _, _, ok := l.Deliver(); !ok { // A
		return errors.New("deliver A")
	}
	if _, _, ok := l.Deliver(); !ok { // B，d=2
		return errors.New("deliver B")
	}
	l.Crash() // 空洞 3,4,5
	// I4：四类拒绝均在写前失败，状态不变。
	if _, err := l.Propose(""); !errors.Is(err, ErrEmptyPayload) {
		return fmt.Errorf("empty payload err=%v", err)
	}
	bad := []error{
		l.RePropose(2, "x"), // 已投递
		l.RePropose(6, "x"), // seq>=nextSeq(6)
	}
	if !errors.Is(bad[0], ErrAlreadyDelivered) || !errors.Is(bad[1], ErrSeqOutOfRange) {
		return fmt.Errorf("repropose guard errs=%v", bad)
	}
	if err := l.RePropose(3, "C"); err != nil {
		return err
	}
	if err := l.RePropose(3, "X"); !errors.Is(err, ErrSlotFilled) { // 槽已填
		return fmt.Errorf("slot-filled err=%v", err)
	}
	// I2：乱序补发（乙）仍按 seq 投出 C,D,E；被拒覆盖未污染槽 3。
	if err := l.RePropose(5, "E"); err != nil {
		return err
	}
	if err := l.RePropose(4, "D"); err != nil { // 故意 5 先 4 后
		return err
	}
	got := drain(l)
	want := []string{"C", "D", "E"}
	if len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		return fmt.Errorf("after crash replay=%v want %v", got, want)
	}
	if l.Delivered() != 5 {
		return fmt.Errorf("delivered=%d want 5", l.Delivered())
	}
	return nil
}
