// Package api 对外提供热点键检测与再分片系统。依赖 route。
package api

import (
	"errors"
	"fmt"
	"sync"

	"ontology/route"
	"ontology/shard"
)

// 可判定的哨兵错误，四者互不相同。
var (
	ErrInvalidS       = errors.New("api: S must be >= 1")
	ErrInvalidT       = errors.New("api: T must be >= 1")
	ErrBaseOutOfRange = errors.New("api: event Base out of range [0, S)")
	ErrEmptyKey       = errors.New("api: event Key is empty")
)

// Event 是喂入系统的 CDC 事件。
type Event = shard.Event

// System 是热点键检测与再分片系统，并发安全。
type System struct {
	mu  sync.RWMutex
	eng *shard.Engine
	rt  *route.Router
}

// New 构造系统；S <= 0 或 T <= 0 时返回可判定错误，不产生任何状态。
func New(S, T int) (*System, error) {
	if S <= 0 {
		return nil, ErrInvalidS
	}
	if T <= 0 {
		return nil, ErrInvalidT
	}
	eng := shard.New(S, T)
	return &System{eng: eng, rt: route.New(eng)}, nil
}

// Feed 喂入一批事件。先整批校验，任一事件非法则整批不生效
// （计数、热点标记、专属分片全部不变）并返回可判定错误。
func (s *System) Feed(evs []Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range evs {
		if e.Key == "" {
			return ErrEmptyKey
		}
		if e.Base < 0 || e.Base >= s.eng.S {
			return ErrBaseOutOfRange
		}
	}
	for _, e := range evs {
		s.rt.Feed(e)
	}
	return nil
}

// Counts 返回各分片累计计数（按分片号升序）。
func (s *System) Counts() []int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.eng.Counts()
}

// IsHot 报告键是否已成为热点。
func (s *System) IsHot(key string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.eng.IsHot(key)
}

// Dedicated 返回键的专属分片号；未迁移时 ok 为 false。
func (s *System) Dedicated(key string) (int, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.rt.Dedicated(key)
}

// SelfCheck 对内置事件序列核验四条不变量，全部通过返回 nil。
// 只操作内部新建的实例，不触碰接收者状态，可并发调用。
func (s *System) SelfCheck() error {
	seq := []Event{
		{Key: "a", Base: 0}, {Key: "a", Base: 0}, {Key: "a", Base: 0}, {Key: "b", Base: 1},
		{Key: "a", Base: 0}, {Key: "a", Base: 0}, {Key: "c", Base: 1}, {Key: "a", Base: 0},
	}
	sys, err := New(2, 3)
	if err != nil {
		return err
	}
	if err := sys.Feed(seq); err != nil {
		return err
	}
	got := sys.Counts()
	// 不变量 1：总量守恒。
	var sum int64
	for _, c := range got {
		sum += c
	}
	if sum != int64(len(seq)) {
		return fmt.Errorf("selfcheck: conservation broken: sum=%d want=%d", sum, len(seq))
	}
	// 不变量 2：与朴素参照一致（内置序列的暴力模拟结果即第三节推导的 [3,2,3]）。
	want := []int64{3, 2, 3}
	if !equal(got, want) {
		return fmt.Errorf("selfcheck: counts=%v want=%v", got, want)
	}
	// 不变量 3：迁移原子性——触发事件落基础分片，a 在基础分片恰计满 T=3，
	// 其余 3 个 a 事件全落专属分片 2。
	if d, ok := sys.Dedicated("a"); !ok || d != 2 || got[0] != 3 || got[2] != 3 {
		return fmt.Errorf("selfcheck: migration atomicity broken: counts=%v dedicated=%d,%v", got, d, ok)
	}
	// 不变量 4：失败不留痕——被拒批次不改变任何状态，之后仍可正常使用。
	before := sys.Counts()
	if err := sys.Feed([]Event{{Key: "x", Base: 9}}); !errors.Is(err, ErrBaseOutOfRange) {
		return fmt.Errorf("selfcheck: reject err=%v", err)
	}
	if !equal(sys.Counts(), before) {
		return fmt.Errorf("selfcheck: rejected batch mutated state")
	}
	if err := sys.Feed([]Event{{Key: "d", Base: 1}}); err != nil || sys.Counts()[1] != 3 {
		return fmt.Errorf("selfcheck: unusable after reject")
	}
	return nil
}

func equal(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
