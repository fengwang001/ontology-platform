// Package topk 实现计数型滑动窗口前 K 高分维护结构。
//
// 窗口保留最近 N 条变更，键的分值等于其在窗口内所有变更的分之和；
// 键在窗口内至少有一条变更才算存在。前 K 按分值降序排列，
// 分值并列时按键名字典序升序。
package topk

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// 可区分的非法输入原因。
var (
	ErrNonPositiveWindow = errors.New("topk: 窗口大小必须为正整数")
	ErrNonPositiveK      = errors.New("topk: 前 K 数必须为正整数")
	ErrKExceedsWindow    = errors.New("topk: 前 K 数不能超过窗口大小")
	ErrEmptyKey          = errors.New("topk: 键不能为空")
)

// Change 表示一条分值变更。
type Change struct {
	Key   string
	Delta int64
}

// Entry 表示前 K 结果中的一项。
type Entry struct {
	Key   string
	Score int64
}

type keyState struct {
	sum   int64
	count int
}

// Window 是计数型滑动窗口前 K 维护结构，并发安全。
type Window struct {
	mu     sync.RWMutex
	n      int
	k      int
	queue  []Change
	states map[string]keyState
	sorted []Entry // 分值降序、键名字典序升序
}

// NewWindow 创建窗口大小为 n、维护前 k 高分的结构。
func NewWindow(n, k int) (*Window, error) {
	if n <= 0 {
		return nil, ErrNonPositiveWindow
	}
	if k <= 0 {
		return nil, ErrNonPositiveK
	}
	if k > n {
		return nil, ErrKExceedsWindow
	}
	return &Window{
		n:      n,
		k:      k,
		states: make(map[string]keyState),
	}, nil
}

// Apply 原子地应用一批变更：任一条非法则整批拒绝，窗口与分值不变。
func (w *Window) Apply(batch []Change) error {
	for _, c := range batch {
		if c.Key == "" {
			return fmt.Errorf("%w: 批次中存在空键", ErrEmptyKey)
		}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, c := range batch {
		w.applyOne(c)
	}
	return nil
}

// TopK 返回当前前 K 高分，按分值降序、并列按键名字典序升序。
func (w *Window) TopK() []Entry {
	w.mu.RLock()
	defer w.mu.RUnlock()
	n := w.k
	if len(w.sorted) < n {
		n = len(w.sorted)
	}
	out := make([]Entry, n)
	copy(out, w.sorted[:n])
	return out
}

// SelfCheck 依据窗口队列重算分值，校验增量维护状态一致。
func (w *Window) SelfCheck() error {
	w.mu.RLock()
	defer w.mu.RUnlock()

	recomputed := make(map[string]keyState)
	for _, c := range w.queue {
		st := recomputed[c.Key]
		st.sum += c.Delta
		st.count++
		recomputed[c.Key] = st
	}
	if len(recomputed) != len(w.states) {
		return fmt.Errorf("topk: 自检失败: 键数量不一致 重算=%d 维护=%d", len(recomputed), len(w.states))
	}
	for key, want := range recomputed {
		got, ok := w.states[key]
		if !ok || got != want {
			return fmt.Errorf("topk: 自检失败: 键 %q 状态不一致 重算=%+v 维护=%+v", key, want, got)
		}
	}
	expected := make([]Entry, 0, len(recomputed))
	for key, st := range recomputed {
		expected = append(expected, Entry{Key: key, Score: st.sum})
	}
	sort.Slice(expected, func(i, j int) bool {
		if expected[i].Score != expected[j].Score {
			return expected[i].Score > expected[j].Score
		}
		return expected[i].Key < expected[j].Key
	})
	if len(expected) != len(w.sorted) {
		return fmt.Errorf("topk: 自检失败: 排序视图长度不一致 重算=%d 维护=%d", len(expected), len(w.sorted))
	}
	for i := range expected {
		if expected[i] != w.sorted[i] {
			return fmt.Errorf("topk: 自检失败: 排序视图第 %d 项不一致 重算=%+v 维护=%+v", i, expected[i], w.sorted[i])
		}
	}
	return nil
}

// Len 返回窗口内现存的变更条数。
func (w *Window) Len() int {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return len(w.queue)
}

func (w *Window) applyOne(c Change) {
	w.queue = append(w.queue, c)
	w.addDelta(c.Key, c.Delta, 1)
	if len(w.queue) > w.n {
		oldest := w.queue[0]
		w.queue = w.queue[1:]
		w.addDelta(oldest.Key, -oldest.Delta, -1)
	}
}

func (w *Window) addDelta(key string, delta int64, countDelta int) {
	st := w.states[key]
	if st.count > 0 {
		w.sorted = removeEntry(w.sorted, Entry{Key: key, Score: st.sum})
	}
	st.sum += delta
	st.count += countDelta
	if st.count == 0 {
		delete(w.states, key)
		return
	}
	w.states[key] = st
	w.sorted = insertEntry(w.sorted, Entry{Key: key, Score: st.sum})
}

func lessEntry(a, b Entry) bool {
	if a.Score != b.Score {
		return a.Score > b.Score
	}
	return a.Key < b.Key
}

func insertEntry(s []Entry, e Entry) []Entry {
	i := sort.Search(len(s), func(i int) bool { return !lessEntry(s[i], e) })
	s = append(s, Entry{})
	copy(s[i+1:], s[i:])
	s[i] = e
	return s
}

func removeEntry(s []Entry, e Entry) []Entry {
	i := sort.Search(len(s), func(i int) bool { return !lessEntry(s[i], e) })
	if i < len(s) && s[i] == e {
		copy(s[i:], s[i+1:])
		s = s[:len(s)-1]
	}
	return s
}
