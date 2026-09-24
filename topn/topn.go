// Package topn 在 rank 有序存活行之上维护 Top-N 边界，产出变更日志。
// 依赖 rank，不依赖 api。
package topn

import (
	"errors"

	"ontology/rank"
)

// 四类可判定哨兵错误（互不相同）。
var (
	ErrInvalidParam = errors.New("topn: n must be positive and maxRows must be >= n")
	ErrKeyExists    = errors.New("topn: key already live")
	ErrRowMissing   = errors.New("topn: retract row missing or score mismatch")
	ErrOverLimit    = errors.New("topn: live row count would exceed maxRows")
)

// Op 是一条输入变更：Add=true 为 +(Key,Score)，否则为 −(Key,Score)。
type Op = rank.Change

// Change 是 rank.Change 的重导出，供上层引用。
type Change = rank.Change

// View 维护存活行与前 N 名边界。
type View struct {
	n       int
	maxRows int
	live    *rank.Ordered
	chlog   []rank.Change // 已输出的全部变更日志
}

// New 创建 Top-N 视图；n 非正或 maxRows<n 返回 ErrInvalidParam。
func New(n, maxRows int) (*View, error) {
	if n <= 0 || maxRows < n {
		return nil, ErrInvalidParam
	}
	return &View{n: n, maxRows: maxRows, live: rank.New()}, nil
}

// N / MaxRows / Live 返回配置与当前存活行数。
func (v *View) N() int                 { return v.n }
func (v *View) MaxRows() int           { return v.maxRows }
func (v *View) Live() int              { return v.live.Len() }
func (v *View) Ordered() *rank.Ordered { return v.live }

// topRows 返回当前排序后前 k 行的副本（k 自动截断到存活行数）。
func (v *View) topRows(k int) []rank.Row {
	if k > v.live.Len() {
		k = v.live.Len()
	}
	out := make([]rank.Row, k)
	for i := range out {
		out[i] = v.live.At(i)
	}
	return out
}

// diffTop 比较处理前后的 Top-N 集合：先离开(−)后进入(+)。
// 一次插入/删除至多改变边界两侧各一行，故每条输入至多一 − 一 +。
func diffTop(before, after []rank.Row) []rank.Change {
	oldSet := make(map[string]struct{}, len(before))
	for _, r := range before {
		oldSet[r.Key] = struct{}{}
	}
	newSet := make(map[string]struct{}, len(after))
	for _, r := range after {
		newSet[r.Key] = struct{}{}
	}
	var ch []Change
	for _, r := range before {
		if _, ok := newSet[r.Key]; !ok {
			ch = append(ch, Change{Row: r, Add: false})
		}
	}
	for _, r := range after {
		if _, ok := oldSet[r.Key]; !ok {
			ch = append(ch, Change{Row: r, Add: true})
		}
	}
	return ch
}

// applyOne 处理单条输入；任何拒绝都不修改 v。
func (v *View) applyOne(op Op) ([]Change, error) {
	before := v.topRows(v.n)
	if op.Add {
		if v.live.Has(op.Row.Key) {
			return nil, ErrKeyExists
		}
		if v.live.Len() >= v.maxRows {
			return nil, ErrOverLimit
		}
		v.live.Insert(op.Row)
	} else {
		cur, ok := v.live.Lookup(op.Row.Key)
		if !ok || cur.Score != op.Row.Score {
			return nil, ErrRowMissing
		}
		v.live.Remove(op.Row.Key)
	}
	ch := diffTop(before, v.topRows(v.n))
	v.chlog = append(v.chlog, ch...)
	return ch, nil
}

// Apply 顺序应用一批输入，返回本批产出的变更日志。
// 批内后一条看得到前一条的效果；任一条被拒则整批（含日志）不生效。
func (v *View) Apply(ops []Op) ([]Change, error) {
	w := v.Clone() // 在副本上试跑，成功才整体替换
	var out []Change
	for _, op := range ops {
		ch, err := w.applyOne(op)
		if err != nil {
			return nil, err // v 从未被触碰
		}
		out = append(out, ch...)
	}
	v.live = w.live
	v.chlog = w.chlog
	return out, nil
}

// View 返回当前 Top-N（按排序序）的副本。
func (v *View) View() []rank.Row { return v.topRows(v.n) }

// Changelog 返回已输出变更日志的副本。
func (v *View) Changelog() []Change {
	out := make([]Change, len(v.chlog))
	copy(out, v.chlog)
	return out
}

// Clone 返回深拷贝。
func (v *View) Clone() *View {
	ch := make([]rank.Change, len(v.chlog))
	copy(ch, v.chlog)
	return &View{n: v.n, maxRows: v.maxRows, live: v.live.Clone(), chlog: ch}
}
