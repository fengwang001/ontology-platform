// Package fview 维护源表与过滤视图，按规则产出变更日志并做源表一致性校验（依赖 fpred）。
package fview

import (
	"errors"
	"fmt"
	"maps"
	"sync"

	"ontology/fpred"
)

// 源表一致性哨兵错误（四类错误互不相同）。
var (
	ErrDuplicateKey   = errors.New("fview: duplicate key on insert")
	ErrKeyNotFound    = errors.New("fview: key not found")
	ErrBeforeMismatch = errors.New("fview: before does not match current row")
	errInternal       = errors.New("fview: internal inconsistency")
	errLookupCost     = errors.New("fview: per-change view lookups exceed constant")
)

// View 是源表 + 当前过滤视图。
type View struct {
	mu        sync.RWMutex
	pred      fpred.Pred
	src, cur  map[string]int64 // 源表 / 当前视图：ID -> Val
	inspected int              // 非导出：最近一条变更检查过的视图行数，主键直查计 1
}

// New 构造视图；lo >= hi 返回 fpred.ErrInvalidRange。
func New(lo, hi int64) (*View, error) {
	p, err := fpred.NewPred(lo, hi)
	if err != nil {
		return nil, err
	}
	return &View{pred: p, src: map[string]int64{}, cur: map[string]int64{}}, nil
}

// applyOne 在工作副本 s(源表)/c(视图) 上模拟一条变更，返回日志与视图查找次数 n。
// 先静态分类，再校验源表一致性并改 s，最后按主键直查 c 产出日志（不变量2在此保证）。
func (v *View) applyOne(s, c map[string]int64, ch fpred.Change) (outs []fpred.Out, n int, err error) {
	kase, err := fpred.Classify(v.pred, ch)
	if err != nil {
		return nil, 0, err
	}
	switch ch.Kind { // 源表一致性
	case fpred.Insert:
		if _, ok := s[ch.After.ID]; ok {
			return nil, 0, ErrDuplicateKey
		}
		s[ch.After.ID] = ch.After.Val
	case fpred.Delete, fpred.Update:
		val, ok := s[ch.Before.ID]
		if !ok {
			return nil, 0, ErrKeyNotFound
		}
		if val != ch.Before.Val {
			return nil, 0, ErrBeforeMismatch
		}
		delete(s, ch.Before.ID)
		if ch.Kind == fpred.Update {
			s[ch.Before.ID] = ch.After.Val
		}
	}
	switch kase { // 最小日志（不变量3：CaseNone 产出 nil）
	case fpred.CaseAdd:
		n, outs = 1, []fpred.Out{{Row: ch.After, Add: true}}
	case fpred.CaseRemove:
		n, outs = 1, []fpred.Out{{Row: ch.Before}}
	case fpred.CaseReplace:
		n, outs = 2, []fpred.Out{{Row: ch.Before}, {Row: ch.After, Add: true}}
	}
	for _, o := range outs { // + 时该 ID 必须缺席；- 时必须恰为现值（同 ID 顺序适用）
		got, ok := c[o.Row.ID]
		if o.Add == ok || (!o.Add && got != o.Row.Val) {
			return nil, 0, errInternal
		}
		if o.Add {
			c[o.Row.ID] = o.Row.Val
		} else {
			delete(c, o.Row.ID)
		}
	}
	return outs, n, nil
}

// Apply 全部变更先在工作副本模拟，任一被拒则整批夭折、状态不动；全成功才提交。
func (v *View) Apply(changes []fpred.Change) ([]fpred.Out, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	s, c, batch, last := maps.Clone(v.src), maps.Clone(v.cur), []fpred.Out{}, 0
	for _, ch := range changes {
		outs, n, err := v.applyOne(s, c, ch)
		if err != nil {
			return nil, err
		}
		batch, last = append(batch, outs...), n
	}
	v.src, v.cur = s, c
	if len(changes) > 0 {
		v.inspected = last
	}
	return append([]fpred.Out(nil), batch...), nil
}

// Snapshot 返回当前视图与源表的拷贝。
func (v *View) Snapshot() (view, source map[string]int64) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return maps.Clone(v.cur), maps.Clone(v.src)
}

func ins(id string, val int64) fpred.Change {
	return fpred.Change{Kind: fpred.Insert, After: fpred.Row{ID: id, Val: val}}
}
func upd(id string, b, a int64) fpred.Change {
	return fpred.Change{Kind: fpred.Update, Before: fpred.Row{ID: id, Val: b}, After: fpred.Row{ID: id, Val: a}}
}

// CheckLookupCost 在 m=100/1000/10000 下各执行四情形 Update，断言单条变更的
// 视图检查行数 ≤2（与 m 无关）。只返回成败错误，不泄露计数器数值。
func CheckLookupCost() error {
	for _, m := range []int{100, 1000, 10000} {
		v, _ := New(10, 20)
		chs := make([]fpred.Change, 0, m+2)
		for i := 0; i < m; i++ {
			chs = append(chs, ins(fmt.Sprintf("k%05d", i), int64(10+i%10)))
		}
		chs = append(chs, ins("o1", 5), ins("o2", 25))
		if _, err := v.Apply(chs); err != nil {
			return err
		}
		for _, u := range []fpred.Change{
			upd("k00000", 10, 11), upd("k00001", 11, 21), upd("o1", 5, 12), upd("o2", 25, 30),
		} {
			if _, err := v.Apply([]fpred.Change{u}); err != nil {
				return err
			}
			if v.inspected > 2 {
				return errLookupCost
			}
		}
	}
	return nil
}
