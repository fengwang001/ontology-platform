// Package api 对外提供 NULL 分组语义下的增量计数视图。
package api

import (
	"errors"
	"fmt"
	"sync"

	"ontology/agg"
	"ontology/grp"
)

// 哨兵错误，三者互不相同，可用 errors.Is 判定。
var (
	ErrBadParam = errors.New("api: maxGroupLen 必须为正整数")
	ErrTooLong  = errors.New("api: 组名长度超过 maxGroupLen")
)

// Event 一条上游变更：Group 为可空分组键（nil 即 NULL 组），Delta 为计数增量。
type Event struct {
	Group *string
	Delta int64
}

// GroupState 视图中的一行：Group 为 nil 表示 NULL 组。
type GroupState struct {
	Group *string
	Count int64
}

// View 计数视图，并发安全。
type View struct {
	mu     sync.RWMutex
	m      *agg.Map
	maxLen int
}

// New 创建视图；maxGroupLen 非正时返回 ErrBadParam。
func New(maxGroupLen int) (*View, error) {
	if maxGroupLen <= 0 {
		return nil, ErrBadParam
	}
	return &View{m: agg.New(), maxLen: maxGroupLen}, nil
}

// Feed 整批原子喂入：任一事件非法（组名过长、计数将变负）则整批不生效。
func (v *View) Feed(evs []Event) error {
	deltas := map[grp.Key]int64{}
	for _, e := range evs {
		if e.Group != nil && len(*e.Group) > v.maxLen {
			return ErrTooLong
		}
		deltas[grp.Of(e.Group)] += e.Delta
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.m.ApplyBatch(deltas)
}

// Count 查询某组当前计数；不存在（含已归零移除）的组返回 0。
func (v *View) Count(g *string) int64 {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.m.Count(grp.Of(g))
}

// Groups 枚举计数非 0 的组：NULL 组、空串组、其余按字典序。
func (v *View) Groups() []GroupState {
	v.mu.RLock()
	defer v.mu.RUnlock()
	ks := v.m.Keys()
	out := make([]GroupState, 0, len(ks))
	for _, k := range ks {
		var gp *string
		if k.Kind != grp.Null {
			s := k.Val
			gp = &s
		}
		out = append(out, GroupState{Group: gp, Count: v.m.Count(k)})
	}
	return out
}

// SelfCheck 用内置事件序列核验四条不变量，全部通过返回 nil。
// 只操作内部新建的实例，可并发调用。
func (v *View) SelfCheck() error {
	if _, err := New(0); !errors.Is(err, ErrBadParam) {
		return fmt.Errorf("selfcheck: New(0) 应报 ErrBadParam, got %v", err)
	}
	sp := func(s string) *string { return &s }
	steps := []Event{ // 第三节八步序列
		{nil, 2}, {sp(""), 1}, {sp("a"), 3}, {nil, 1},
		{sp("b"), 5}, {sp(""), -1}, {sp("a"), -2}, {nil, -3},
	}
	want := [][4]int64{ // 每步后 (NULL, "", a, b) 的朴素批量结果
		{2, 0, 0, 0}, {2, 1, 0, 0}, {2, 1, 3, 0}, {3, 1, 3, 0},
		{3, 1, 3, 5}, {3, 0, 3, 5}, {3, 0, 1, 5}, {0, 0, 1, 5},
	}
	w, _ := New(16)
	for i, e := range steps {
		if err := w.Feed([]Event{e}); err != nil {
			return fmt.Errorf("selfcheck: 第%d步 Feed 失败: %w", i+1, err)
		}
		got := [4]int64{w.Count(nil), w.Count(sp("")), w.Count(sp("a")), w.Count(sp("b"))}
		if got != want[i] { // 不变量 1、2：与批量一致，NULL/空串独立
			return fmt.Errorf("selfcheck: 第%d步计数 %v, 应 %v", i+1, got, want[i])
		}
		nz := 0
		for _, c := range want[i] {
			if c != 0 {
				nz++
			}
		}
		if len(w.Groups()) != nz { // 不变量 3：归零即删
			return fmt.Errorf("selfcheck: 第%d步存在组数应 %d", i+1, nz)
		}
	}
	snap := func() string { // 按值渲染存在集合，与指针地址无关
		s := ""
		for _, g := range w.Groups() {
			name := "NULL"
			if g.Group != nil {
				name = *g.Group
			}
			s += fmt.Sprintf("%s=%d;", name, g.Count)
		}
		return s
	}
	before := snap()
	if err := w.Feed([]Event{{nil, -1}}); !errors.Is(err, agg.ErrNegative) {
		return fmt.Errorf("selfcheck: 负计数应报 ErrNegative, got %v", err)
	}
	if err := w.Feed([]Event{{sp("0123456789abcdefg"), 1}}); !errors.Is(err, ErrTooLong) {
		return fmt.Errorf("selfcheck: 超长组名应报 ErrTooLong, got %v", err)
	}
	if snap() != before { // 不变量 4：失败不留痕
		return errors.New("selfcheck: 被拒操作改变了状态")
	}
	return nil
}
