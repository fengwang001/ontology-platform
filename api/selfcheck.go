package api

import (
	"errors"
	"fmt"
	"maps"
	"math/rand"
	"reflect"
	"slices"

	"ontology/view"
)

type refRow struct{ a, b, c int64 }

var (
	colsAll = []view.Col{view.A, view.B, view.C}
	rows3   = [3][3]int64{{10, 20, 30}, {1, 2, 3}, {100, 200, 300}}
)

func i64(z ...int64) []int64 { return z }

// lay 把当前逐格状态打包成可整体比较的五元组。
func lay(v *view.View) [5]any {
	a, b, c, l, s := v.Snapshot()
	return [5]any{a, b, c, l, s}
}

// eightStep 重放第三节八步并逐格比对三列/alive/slotOf；(丙)(乙) 在第 4 步、
// (甲) 在第 8 步断言。这是 SelfCheck 的内置操作序列。
func eightStep() error {
	v := view.New()
	wants := [][5]any{
		{i64(10), i64(20), i64(30), []bool{true}, []int{0}},
		{i64(10, 1), i64(20, 2), i64(30, 3), []bool{true, true}, []int{0, 1}},
		{i64(10, 1, 100), i64(20, 2, 200), i64(30, 3, 300), []bool{true, true, true}, []int{0, 1, 2}},
		{i64(10, 1, 100), i64(20, 2, 200), i64(30, 3, 300), []bool{false, true, true}, []int{-1, 1, 2}},
		{i64(10, 1, 100, 7), i64(20, 2, 200, 8), i64(30, 3, 300, 9), []bool{false, true, true, true}, []int{-1, 1, 2, 3}},
		{i64(1, 100, 7), i64(2, 200, 8), i64(3, 300, 9), []bool{true, true, true}, []int{-1, 0, 1, 2}},
		{i64(1, 100, 7), i64(2, 200, 8), i64(999, 300, 9), []bool{true, true, true}, []int{-1, 0, 1, 2}},
		{i64(1, 100, 7), i64(2, 200, 8), i64(999, 300, 9), []bool{true, true, true}, []int{-1, 0, 1, 2}},
	}
	for i := range 8 {
		switch i {
		case 0, 1, 2:
			r := rows3[i]
			v.Insert(r[0], r[1], r[2])
		case 3: // (丙) ErrDeleted；(乙) Project([B])=[2,200]，不多出 20
			v.Delete(0)
			_, _, _, ge := v.Get(0)
			p, _ := v.Project([]view.Col{view.B})
			if !errors.Is(ge, view.ErrDeleted) || !slices.Equal(p[view.B], i64(2, 200)) {
				return fmt.Errorf("step4 get=%v B=%v", ge, p[view.B])
			}
		case 4:
			v.Insert(7, 8, 9)
		case 5:
			v.Compact()
		case 6:
			if e := v.Update(1, view.C, 999); e != nil {
				return e
			}
		case 7: // (甲) Get(rowID2)=(1,2,999)
			a, b, c, e := v.Get(1)
			if e != nil || a != 1 || b != 2 || c != 999 {
				return fmt.Errorf("step8 (%d,%d,%d)%v", a, b, c, e)
			}
		}
		if g := lay(v); !reflect.DeepEqual(g, wants[i]) {
			return fmt.Errorf("step %d %+v", i+1, g)
		}
	}
	return nil
}

// verify 对照行式模型：存活行 Get 逐字段相等、已删报 ErrDeleted（钉稳定 rowID）；
// 全列投影各列等长且同一下标来自同一行（列对齐，长度即存活数）；next 越界报
// ErrNoSuchRow。
func verify(v *view.View, ref map[int]refRow, next int) error {
	for id := range next {
		w, live := ref[id]
		a, b, c, e := v.Get(id)
		if live != (e == nil) || live && (a != w.a || b != w.b || c != w.c) ||
			!live && !errors.Is(e, view.ErrDeleted) {
			return fmt.Errorf("row %d (%d,%d,%d)%v", id, a, b, c, e)
		}
	}
	ids := slices.Sorted(maps.Keys(ref))
	got, _ := v.Project(colsAll) // 调用方 sel 恒非空
	for i, id := range ids {
		w := [3]int64{ref[id].a, ref[id].b, ref[id].c}
		for k := 0; k < 3; k++ { // i 在三列来自同一行：列对齐，绝不串行
			if col := got[colsAll[k]]; len(col) != len(ids) || col[i] != w[k] {
				return fmt.Errorf("project col %d row %d", k, id)
			}
		}
	}
	if _, _, _, e := v.Get(next); !errors.Is(e, view.ErrNoSuchRow) {
		return fmt.Errorf("unallocated %d %v", next, e)
	}
	return nil
}

// replayRandom 确定性随机混合五类操作；id==next 即从未分配行；每次被拒后末尾
// verify 仍须通过：失败不留痕、实例可继续正常使用。
func replayRandom(n int, seed int64) error {
	v, rng := view.New(), rand.New(rand.NewSource(seed))
	ref := map[int]refRow{}
	next := 0
	for range n {
		id := rng.Intn(next + 1)
		want := map[bool]error{true: view.ErrDeleted, false: view.ErrNoSuchRow}[id < next]
		switch rng.Intn(5) {
		case 0:
			r := refRow{int64(rng.Intn(1000)), int64(rng.Intn(1000)), int64(rng.Intn(1000))}
			if v.Insert(r.a, r.b, r.c) != next {
				return errors.New("nonmonotonic id")
			}
			ref[next], next = r, next+1
		case 1:
			k, val := colsAll[rng.Intn(3)], int64(rng.Intn(100000))
			r, ok := ref[id]
			if e := v.Update(id, k, val); (e == nil) != ok || e != nil && !errors.Is(e, want) {
				return fmt.Errorf("update %d: %v", id, e)
			}
			if ok {
				*[3]*int64{&r.a, &r.b, &r.c}[k] = val
				ref[id] = r
			}
		case 2:
			_, ok := ref[id]
			if e := v.Delete(id); (e == nil) != ok || e != nil && !errors.Is(e, want) {
				return fmt.Errorf("delete %d: %v", id, e)
			}
			delete(ref, id)
		case 3:
			v.Compact()
		default:
			if _, e := v.Project(nil); !errors.Is(e, view.ErrNoColumns) {
				return fmt.Errorf("empty project %v", e)
			}
		}
		if e := verify(v, ref, next); e != nil {
			return e
		}
	}
	return nil
}
