// Package api 对外暴露 SpaceSaving 频繁项近似计数。依赖 ss。
package api

import (
	"errors"
	"fmt"
	"sync"

	"ontology/ss"
)

// 可判定的哨兵错误，三者互不相同。
var (
	ErrInvalidK    = errors.New("api: 计数器个数非法 (k < 1)")
	ErrNegativeKey = errors.New("api: 键非法 (负数 Key)")
	ErrNilFeed     = errors.New("api: 空输入 (nil 切片)")
)

// Key 是事件键，必须 >= 0。
type Key int

// Entry 是一个计数器的快照：(键, 估计计数, 误差)。
type Entry = ss.Entry

// Summary 是并发安全的 SpaceSaving 实例。
type Summary struct {
	mu sync.RWMutex
	s  *ss.Summary
}

// New 创建容量为 k 的实例；k < 1 整体失败。
func New(k int) (*Summary, error) {
	if k < 1 {
		return nil, ErrInvalidK
	}
	return &Summary{s: ss.New(k)}, nil
}

// Feed 喂入一批事件；nil 切片或含负数 Key 时整批不生效，状态不变。
func (m *Summary) Feed(keys []Key) error {
	if keys == nil {
		return ErrNilFeed
	}
	for _, x := range keys {
		if x < 0 {
			return ErrNegativeKey
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, x := range keys {
		m.s.Add(int(x))
	}
	return nil
}

// Query 返回 x 的估计计数：有计数器返回其 count，否则返回 0。
func (m *Summary) Query(x Key) int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.s.Query(int(x))
}

// TopK 返回全部计数器，按 Count 降序、并列按 Key 升序。
func (m *Summary) TopK() []Entry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.s.Entries()
}

// SelfCheck 对一组内置事件流核验四条不变量，全部通过返回 nil。
// 只使用新建的临时实例，不触碰接收者状态。
func (m *Summary) SelfCheck() error {
	streams := []struct {
		k      int
		events []int
	}{
		{3, []int{3, 1, 3, 2, 4, 1, 3, 5}}, // NOTES.md 八行表
		{1, []int{7, 7, 9, 7, 9, 9, 7}},
		{5, gen(2000, 50, 17)}, // 偏斜流
		{3, gen(999, 7, 1)},    // 近均匀流
	}
	for _, c := range streams {
		if err := checkInvariants(c.k, c.events); err != nil {
			return err
		}
	}
	return checkNoTrace()
}

// gen 生成确定性事件流：i -> (i*i+salt) % mod。
func gen(n, mod, salt int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = (i*i + salt) % mod
	}
	return out
}

// checkInvariants 核验不变量 1（不低估/误差界）、2（最小计数下界）、3（容量恒定）。
func checkInvariants(k int, events []int) error {
	s := ss.New(k)
	trueCnt := map[int]int{}
	for _, x := range events {
		s.Add(x)
		trueCnt[x]++
		if s.Len() > k { // 不变量 3：容量恒定
			return fmt.Errorf("selfcheck: 计数器个数 %d 超过 k=%d", s.Len(), k)
		}
	}
	minCount := -1
	monitored := map[int]bool{}
	for _, e := range s.Entries() {
		monitored[e.Key] = true
		if e.Count < trueCnt[e.Key] || e.Count-e.Err > trueCnt[e.Key] { // 不变量 1
			return fmt.Errorf("selfcheck: Key %d 违反 count-error <= true <= count", e.Key)
		}
		if minCount < 0 || e.Count < minCount {
			minCount = e.Count
		}
	}
	for key, tc := range trueCnt {
		if !monitored[key] && tc > minCount { // 不变量 2
			return fmt.Errorf("selfcheck: 未监控 Key %d 的真实计数 %d 超过最小 count %d", key, tc, minCount)
		}
	}
	return nil
}

// checkNoTrace 核验不变量 4：失败不留痕。
func checkNoTrace() error {
	if _, err := New(0); !errors.Is(err, ErrInvalidK) {
		return fmt.Errorf("selfcheck: New(0) 应报 ErrInvalidK，得到 %v", err)
	}
	m, _ := New(3)
	if err := m.Feed([]Key{1, 2, 2}); err != nil {
		return err
	}
	before := fmt.Sprint(m.TopK())
	if err := m.Feed(nil); !errors.Is(err, ErrNilFeed) {
		return fmt.Errorf("selfcheck: Feed(nil) 应报 ErrNilFeed，得到 %v", err)
	}
	if err := m.Feed([]Key{5, -1}); !errors.Is(err, ErrNegativeKey) {
		return fmt.Errorf("selfcheck: Feed(负数) 应报 ErrNegativeKey，得到 %v", err)
	}
	if fmt.Sprint(m.TopK()) != before {
		return errors.New("selfcheck: 被拒操作改变了状态")
	}
	return m.Feed([]Key{3}) // 被拒后仍可正常使用
}
