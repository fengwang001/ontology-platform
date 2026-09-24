// Package api 对外提供左外连接物化视图接口。依赖 join。
package api

import (
	"errors"
	"fmt"
	"reflect"
	"sort"

	"ontology/join"
)

// Op 与 Row 是 join 包类型的别名，日志与宽表行语义见 join。
type (
	Op  = join.Op
	Row = join.Row
)

var (
	ErrEmptyKey = join.ErrEmptyKey
	ErrNoLeft   = join.ErrNoLeft
	ErrNoRight  = join.ErrNoRight
)

// View 是左外连接物化视图的对外句柄，并发安全。
type View struct{ j *join.View }

func New() *View { return &View{j: join.New()} }

// PutL/PutR/DelL/DelR 返回本次输出的变更日志（先撤回再插入）；被拒时返回哨兵错误且状态不变。
func (v *View) PutL(k string, lv int64) ([]Op, error) { return v.j.PutL(k, lv) }
func (v *View) PutR(k string, rv int64) ([]Op, error) { return v.j.PutR(k, rv) }
func (v *View) DelL(k string) ([]Op, error)           { return v.j.DelL(k) }
func (v *View) DelR(k string) ([]Op, error)           { return v.j.DelR(k) }

// View 返回宽表全量快照，按 K 升序，RV 缺席为 nil。
func (v *View) View() []Row { return v.j.View() }

// SelfCheck 对内置操作序列核验四条不变量（批量重算一致、日志自洽、右记录留存、失败不留痕）。
func (v *View) SelfCheck() error {
	j := join.New()
	lefts := map[string]int64{}
	down := map[string]Row{} // 下游按顺序应用日志得到的表
	consistent := func() error {
		rights := j.Rights()
		keys := make([]string, 0, len(lefts))
		for k := range lefts {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		want := make([]Row, 0, len(keys))
		for _, k := range keys {
			var rv *int64
			if x, ok := rights[k]; ok {
				rv = &x
			}
			want = append(want, Row{K: k, LV: lefts[k], RV: rv})
		}
		if got := j.View(); !reflect.DeepEqual(got, want) {
			return fmt.Errorf("view %v != batch %v", got, want)
		}
		dkeys := make([]string, 0, len(down))
		for k := range down {
			dkeys = append(dkeys, k)
		}
		sort.Strings(dkeys)
		drows := make([]Row, 0, len(dkeys))
		for _, k := range dkeys {
			drows = append(drows, down[k])
		}
		if !reflect.DeepEqual(drows, want) {
			return fmt.Errorf("downstream %v != batch %v", drows, want)
		}
		return nil
	}
	apply := func(ops []Op) error { // 不变量 2：逐条应用日志
		for _, o := range ops {
			if o.Insert {
				if _, dup := down[o.K]; dup {
					return fmt.Errorf("insert duplicates %q", o.K)
				}
				down[o.K] = Row{K: o.K, LV: o.LV, RV: o.RV}
			} else {
				if cur, ok := down[o.K]; !ok || !reflect.DeepEqual(cur, Row{K: o.K, LV: o.LV, RV: o.RV}) {
					return fmt.Errorf("retract mismatches %q", o.K)
				}
				delete(down, o.K)
			}
		}
		return consistent()
	}
	steps := []struct {
		run func() ([]Op, error)
		adj func()
	}{
		{func() ([]Op, error) { return j.PutL("k1", 10) }, func() { lefts["k1"] = 10 }},
		{func() ([]Op, error) { return j.PutL("k2", 20) }, func() { lefts["k2"] = 20 }},
		{func() ([]Op, error) { return j.PutR("k2", 200) }, func() {}},
		{func() ([]Op, error) { return j.PutR("k1", 100) }, func() {}},
		{func() ([]Op, error) { return j.PutR("k3", 300) }, func() {}}, // 无左，仅存
		{func() ([]Op, error) { return j.PutL("k3", 30) }, func() { lefts["k3"] = 30 }},
		{func() ([]Op, error) { return j.PutR("k2", 250) }, func() {}},
		{func() ([]Op, error) { return j.DelL("k1") }, func() { delete(lefts, "k1") }},
		{func() ([]Op, error) { return j.PutL("k1", 15) }, func() { lefts["k1"] = 15 }}, // 不变量 3：RV 须为留存的 100
		{func() ([]Op, error) { return j.DelR("k2") }, func() {}},
		{func() ([]Op, error) { return j.PutR("k2", 250) }, func() {}},
		{func() ([]Op, error) { return j.PutL("k4", 40) }, func() { lefts["k4"] = 40 }}, // k4 无右记录
	}
	for i, st := range steps {
		ops, err := st.run()
		if err != nil {
			return fmt.Errorf("step %d: %w", i+1, err)
		}
		st.adj()
		if err := apply(ops); err != nil {
			return fmt.Errorf("step %d: %w", i+1, err)
		}
	}
	sents := []error{ErrEmptyKey, ErrNoLeft, ErrNoRight}
	for i, a := range sents {
		for _, b := range sents[i+1:] {
			if errors.Is(a, b) {
				return errors.New("sentinel errors not distinct")
			}
		}
	}
	before, beforeR := j.View(), j.Rights()
	bad := []struct {
		run  func() error
		want error
	}{
		{func() error { _, e := j.PutL("", 1); return e }, ErrEmptyKey},
		{func() error { _, e := j.PutR("", 1); return e }, ErrEmptyKey},
		{func() error { _, e := j.DelL("ghost"); return e }, ErrNoLeft},
		{func() error { _, e := j.DelR("ghost"); return e }, ErrNoRight},
		{func() error { _, e := j.DelR("k4"); return e }, ErrNoRight}, // k4 有左但 RV 缺席
	}
	for i, b := range bad {
		if err := b.run(); !errors.Is(err, b.want) {
			return fmt.Errorf("bad op %d: got %v, want %v", i, err, b.want)
		}
	}
	if !reflect.DeepEqual(j.View(), before) || !reflect.DeepEqual(j.Rights(), beforeR) {
		return errors.New("rejected op changed state")
	}
	if _, err := j.PutL("k9", 9); err != nil {
		return fmt.Errorf("usable after rejection: %w", err)
	}
	return nil
}
