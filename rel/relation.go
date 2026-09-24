// Package rel 是单关系多重集：元组按副本计数，支持逐条 Insert/Delete。
package rel

// Tuple 是二元整数元组；具体含义由所在关系决定（R:(a,b)、S:(b,c)、T:(c,d)）。
type Tuple struct {
	X, Y int
}

// Relation 是一个多重集关系：同一 Tuple 可出现多次，值为副本计数（恒 >0）。
type Relation struct {
	m map[Tuple]int
}

// New 创建空关系。
func New() *Relation {
	return &Relation{m: make(map[Tuple]int)}
}

// Count 返回元组当前副本数（不存在为 0）。
func (r *Relation) Count(t Tuple) int {
	return r.m[t]
}

// Len 返回关系内不同元组的个数（非结果条数，供上层做批量重算/自检）。
func (r *Relation) Len() int {
	return len(r.m)
}

// Range 以只读方式遍历全部 (元组, 计数)；回调返回 false 可提前停止。
func (r *Relation) Range(fn func(t Tuple, n int) bool) {
	for t, n := range r.m {
		if !fn(t, n) {
			return
		}
	}
}

// Insert 把一条元组的计数 +1。
func (r *Relation) Insert(t Tuple) {
	r.m[t]++
}

// Delete 把一条元组的计数 -1（只减一份副本）；当前计数为 0 时拒绝。
func (r *Relation) Delete(t Tuple) error {
	if r.m[t] <= 0 {
		return ErrNotFound
	}
	if r.m[t] == 1 {
		delete(r.m, t) // 不存在的键不留零值，保证 Count/Range 语义干净
	} else {
		r.m[t]--
	}
	return nil
}

// ErrNotFound 表示删除了一条不存在的元组（哨兵，可 errors.Is 判定）。
var ErrNotFound = errNotFound{}

type errNotFound struct{}

func (errNotFound) Error() string { return "rel: tuple not found" }
