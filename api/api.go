// Package api 对外提供三表增量连接服务，依赖 join（单向依赖）。
package api

import (
	"errors"
	"strconv"

	"ontology/join"
	"ontology/rel"
)

// View 是对外的结果多重集快照：(a,b,c,d) → 多重度。
type View map[[4]int]int

// API 是三表增量连接的对外句柄。
type API struct{ j *join.Join }

// New 创建结果条数上限为 maxResults 的服务（<=0 表示不限）。
func New(maxResults int) *API { return &API{j: join.New(maxResults)} }

// Insert 向 tab("R"/"S"/"T") 逐条插入元组 (x,y)。
func (a *API) Insert(tab string, x, y int) error {
	return a.j.Insert(tab, rel.Tuple{X: x, Y: y})
}

// Delete 从 tab 逐条删除元组 (x,y)；计数为 0 返回 ErrNotFound。
func (a *API) Delete(tab string, x, y int) error {
	return a.j.Delete(tab, rel.Tuple{X: x, Y: y})
}

// Result 返回当前连接结果多重集快照。
func (a *API) Result() View {
	v := View{}
	for q, n := range a.j.Result() {
		v[[4]int{q.A, q.B, q.C, q.D}] = n
	}
	return v
}

// 三类互不相同的哨兵错误（join/rel 直接透出，errors.Is 可判定）。
var (
	ErrBadTable = join.ErrBadTable // 表名非法
	ErrTooMany  = join.ErrTooMany  // 结果条数将超限
	ErrNotFound = rel.ErrNotFound  // 删除不存在的元组
)

// SelfCheck 在全新实例上跑内置序列核验四条不变量；参考结果用独立朴素重连计算，通过返回 nil。
func (a *API) SelfCheck() error {
	v := New(0)
	tables := map[string]map[[2]int]int{"R": {}, "S": {}, "T": {}}
	bump := func(tab string, k [2]int, d int) {
		tables[tab][k] += d
		if tables[tab][k] == 0 {
			delete(tables[tab], k)
		}
	}
	type op struct {
		tab  string
		x, y int
		del  bool
	}
	ops := []op{
		{"S", 1, 10, false}, {"T", 10, 100, false}, {"R", 5, 1, false},
		{"S", 1, 10, false}, {"R", 6, 1, false}, {"R", 5, 1, true},
		{"S", 1, 10, true}, {"S", 1, 10, true},
	}
	want := []View{
		{}, {}, {{5, 1, 10, 100}: 1}, {{5, 1, 10, 100}: 2},
		{{5, 1, 10, 100}: 2, {6, 1, 10, 100}: 2},
		{{6, 1, 10, 100}: 2}, {{6, 1, 10, 100}: 1}, {},
	}
	for i, o := range ops {
		k := [2]int{o.x, o.y}
		var err error
		if o.del {
			err = v.Delete(o.tab, o.x, o.y)
			bump(o.tab, k, -1)
		} else {
			err = v.Insert(o.tab, o.x, o.y)
			bump(o.tab, k, 1)
		}
		if err != nil || !equalView(v.Result(), want[i]) ||
			!equalView(v.Result(), batchRecompute(tables)) {
			return errors.New("selfcheck: eight-step invariant failed at step " + strconv.Itoa(i+1))
		}
	}
	// 三类哨兵必须各自可判定；被拒操作前后状态不变，且之后实例仍可正常使用。
	if e := v.Insert("X", 1, 1); !errors.Is(e, ErrBadTable) {
		return errors.New("selfcheck: want ErrBadTable")
	}
	if e := v.Delete("R", 9, 9); !errors.Is(e, ErrNotFound) {
		return errors.New("selfcheck: want ErrNotFound")
	}
	z := New(1)
	_ = z.Insert("S", 1, 2)
	_ = z.Insert("T", 2, 3)
	if err := z.Insert("R", 1, 1); err != nil { // 结果恰 1 条，达上限
		return err
	}
	before := z.Result()
	if e := z.Insert("R", 2, 1); !errors.Is(e, ErrTooMany) {
		return errors.New("selfcheck: want ErrTooMany")
	}
	if !equalView(z.Result(), before) {
		return errors.New("selfcheck: rejected insert left a trace")
	}
	if e := z.Delete("R", 9, 9); !errors.Is(e, ErrNotFound) || !equalView(z.Result(), before) {
		return errors.New("selfcheck: rejected delete left a trace")
	}
	_ = z.Delete("R", 1, 1)
	if len(z.Result()) != 0 {
		return errors.New("selfcheck: instance unusable after rejection")
	}
	return nil
}

// batchRecompute 是与增量实现无关的朴素三重循环批量重连（零计数键已由调用方删除）。
func batchRecompute(m map[string]map[[2]int]int) map[[4]int]int {
	out := map[[4]int]int{}
	for r, cr := range m["R"] {
		for s, cs := range m["S"] {
			if r[1] != s[0] {
				continue
			}
			for t, ct := range m["T"] {
				if s[1] == t[0] {
					out[[4]int{r[0], r[1], s[1], t[1]}] += cr * cs * ct
				}
			}
		}
	}
	return out
}

func equalView(a, b map[[4]int]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, n := range a {
		if b[k] != n {
			return false
		}
	}
	return true
}
