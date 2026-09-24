// Package api 对外提供 NULL 分组语义下的增量计数视图。
package api

import (
	"errors"
	"fmt"
	"sync"

	"ontology/agg"
	"ontology/grp"
)

// 可判定的哨兵错误，三者互不相同。
var (
	ErrNegativeCount = errors.New("delta 会使组计数为负")
	ErrGroupTooLong  = errors.New("组名长度超过 maxGroupLen")
	ErrInvalidParam  = errors.New("maxGroupLen 必须为正整数")
)

// Event 是一条上游变更事件：Group 为可空分组键（nil 表示 NULL 组），Delta 为计数增量。
type Event struct {
	Group *string
	Delta int64
}

// GroupState 是视图中的一行：Group 为 nil 表示 NULL 组。
type GroupState struct {
	Group *string
	Count int64
}

// View 是并发安全的增量计数物化视图。
type View struct {
	mu  sync.RWMutex
	agg *agg.Agg
	max int
}

// New 创建视图；maxGroupLen 非正时返回 ErrInvalidParam。
func New(maxGroupLen int) (*View, error) {
	if maxGroupLen <= 0 {
		return nil, ErrInvalidParam
	}
	return &View{agg: agg.New(), max: maxGroupLen}, nil
}

// Feed 应用一批事件。任一条非法（组名过长、计数将变负）则整批不生效，
// 返回对应哨兵错误，所有组的计数与存在集合不变。
func (v *View) Feed(evs []Event) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	for _, e := range evs {
		if e.Group != nil && len(*e.Group) > v.max {
			return ErrGroupTooLong
		}
	}
	next := v.agg.Clone() // 先在克隆上试算，全部通过才换入：失败不留痕
	for _, e := range evs {
		if !next.Apply(grp.Normalize(e.Group), e.Delta) {
			return ErrNegativeCount
		}
	}
	v.agg = next
	return nil
}

// Count 返回组 g 的当前计数；nil 查 NULL 组，不存在的组返回 0。
func (v *View) Count(g *string) int64 {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.agg.Count(grp.Normalize(g))
}

// Groups 枚举计数非 0 的组：NULL 组最前，空串次之，其余按字典序。
func (v *View) Groups() []GroupState {
	v.mu.RLock()
	defer v.mu.RUnlock()
	ks := v.agg.Keys()
	out := make([]GroupState, 0, len(ks))
	for _, k := range ks {
		gs := GroupState{Count: v.agg.Count(k)}
		if !k.IsNull() {
			s := k.Str()
			gs.Group = &s
		}
		out = append(out, gs)
	}
	return out
}

// SelfCheck 用内置事件序列核验四条不变量，全部通过返回 nil。
// 只操作自己新建的实例，不触碰调用方状态，可并发调用。
func (v *View) SelfCheck() error {
	sp := func(s string) *string { return &s }
	// 不变量 2+3：NULL 与空串独立；归零即删、负值拒绝。
	w, err := New(16)
	if err != nil {
		return err
	}
	if err := w.Feed([]Event{{nil, 2}, {sp(""), 1}, {nil, -2}}); err != nil {
		return fmt.Errorf("selfcheck 喂事件失败: %w", err)
	}
	if w.Count(nil) != 0 || w.Count(sp("")) != 1 || len(w.Groups()) != 1 {
		return errors.New("selfcheck: NULL 与空串混组或归零未删")
	}
	if err := w.Feed([]Event{{sp(""), -2}}); !errors.Is(err, ErrNegativeCount) {
		return errors.New("selfcheck: 负计数未报 ErrNegativeCount")
	}
	if w.Count(sp("")) != 1 { // 不变量 4：失败不留痕
		return errors.New("selfcheck: 被拒事件留下了痕迹")
	}
	// 不变量 1：与朴素批量重算一致（NOTES.md 八步序列，终态 NULL=0,""=0,a=1,b=5）。
	u, _ := New(16)
	if err := u.Feed([]Event{{nil, 2}, {sp(""), 1}, {sp("a"), 3}, {nil, 1},
		{sp("b"), 5}, {sp(""), -1}, {sp("a"), -2}, {nil, -3}}); err != nil {
		return fmt.Errorf("selfcheck 八步序列失败: %w", err)
	}
	if u.Count(nil) != 0 || u.Count(sp("")) != 0 || u.Count(sp("a")) != 1 || u.Count(sp("b")) != 5 {
		return errors.New("selfcheck: 与批量重算结果不一致")
	}
	// 不变量 4：非法参数。
	if _, err := New(0); !errors.Is(err, ErrInvalidParam) {
		return errors.New("selfcheck: 非法参数未报 ErrInvalidParam")
	}
	return nil
}
