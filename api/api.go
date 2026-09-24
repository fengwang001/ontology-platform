// Package api 是 SCD2 历史区间维护的对外入口：New/Apply/History/AsOf/SelfCheck。
// 只依赖 scd（后者依赖 interval），依赖方向单向。
package api

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"math"
	"math/rand"
	"ontology/interval"
	"ontology/scd"
	"reflect"
	"slices"
	"sync"
)

var (
	ErrInvalidMaxPoints = errors.New("api: maxPoints must be >= 1")
	ErrEmptyKey         = errors.New("api: event key must not be empty")
	ErrEffOutOfRange    = errors.New("api: effective time out of range [0, MaxInt64)")
	ErrTooManyPoints    = scd.ErrTooManyPoints
)

type Event struct {
	Key string
	Eff int64
	Op  interval.Op
	Val string
}
type Row = interval.Row
type DB struct {
	mu sync.RWMutex
	t  *scd.Table
}

func New(n int) (*DB, error) {
	if n < 1 {
		return nil, ErrInvalidMaxPoints
	}
	return &DB{t: scd.NewTable(n)}, nil
}
func mustNew(n int) *DB {
	d, err := New(n)
	if err != nil {
		panic(err)
	}
	return d
}
func pt(e Event) interval.Point {
	return interval.Point{Eff: e.Eff, Del: e.Op == interval.Delete, Val: e.Val}
}
func ev(eff int64, v string) Event { return Event{Key: "K", Eff: eff, Op: interval.Upsert, Val: v} }
func evd(eff int64) Event          { return Event{Key: "K", Eff: eff, Op: interval.Delete} }
func (d *DB) Apply(evs []Event) error {
	// 原子应用一批：先整批校验键/Eff（不过即原样返回、不留痕），再由 scd
	// 预检每键最终点数；任一条被拒整批不生效，之后 DB 仍可正常使用。
	s := make([]scd.Event, 0, len(evs))
	for _, e := range evs {
		switch {
		case e.Key == "":
			return ErrEmptyKey
		case !interval.ValidEff(e.Eff):
			return ErrEffOutOfRange
		}
		s = append(s, scd.Event{Key: e.Key, Point: pt(e)})
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.t.Apply(s)
}
func (d *DB) History(k string) []Row {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.t.History(k)
}
func (d *DB) AsOf(k string, at int64) (string, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.t.AsOf(k, at)
}
func wf(rs []Row) bool { // 良构：from<to、按 from 升序、两两不重叠
	for i := range rs {
		if rs[i].From >= rs[i].To || (i > 0 && rs[i-1].To > rs[i].From) {
			return false
		}
	}
	return true
}
func rowsMatch(g, want []Row) bool { return reflect.DeepEqual(g, want) && wf(g) }
func recompute(evs []Event) []Row { // 独立参照模型：同 Eff 后到覆盖、排序后一次性生成
	m := map[int64]interval.Point{}
	for _, e := range evs {
		m[e.Eff] = pt(e)
	}
	pp := slices.SortedFunc(maps.Values(m), func(a, b interval.Point) int { return cmp.Compare(a.Eff, b.Eff) })
	return interval.Build(pp)
}
func (d *DB) SelfCheck() error {
	// 用内置事件序列核验四不变量与二分定位复杂度，全部成立返回 nil。
	steps := []Event{ev(10, "a"), ev(30, "b"), evd(50), ev(20, "c"),
		ev(30, "d"), ev(5, "f"), evd(20), ev(40, "g")}
	c, seen := mustNew(1000), []Event{}
	for n, e := range steps { // 不变量 1、2：八步逐行等于独立重算且良构
		seen = append(seen, e)
		if err := c.Apply([]Event{e}); err != nil || !rowsMatch(c.History("K"), recompute(seen)) {
			return fmt.Errorf("selfcheck step %d err=%v rows=%v", n+1, err, c.History("K"))
		}
	}
	rnd := rand.New(rand.NewSource(1))
	base := make([]Event, 30)
	for k := range base { // 不变量 1、2、3：随机到达顺序彼此相同且等于批量重算
		base[k] = Event{Key: "R", Eff: int64(2*k + 1), Op: interval.Upsert, Val: fmt.Sprint(k)}
		if rnd.Intn(2) == 0 {
			base[k].Op, base[k].Val = interval.Delete, ""
		}
	}
	ref := recompute(base)
	for round := 0; round < 20; round++ {
		es := append([]Event(nil), base...)
		rnd.Shuffle(len(es), func(i, j int) { es[i], es[j] = es[j], es[i] })
		tmp := mustNew(1000)
		if err := tmp.Apply(es); err != nil || !rowsMatch(tmp.History("R"), ref) {
			return fmt.Errorf("selfcheck order round %d err=%v", round, err)
		}
	}
	return d.checkReject()
}
func (d *DB) checkReject() error {
	// 不变量 4：四类哨兵错误、整批拒绝不留痕、同 Eff 替换不占名额；外加对数定位。
	_, e0 := New(0)
	if !d.t.LocatingIsLogarithmic() || !errors.Is(e0, ErrInvalidMaxPoints) {
		return errors.New("selfcheck: preconditions (log-locate or maxPoints) failed")
	}
	c := mustNew(1)
	_ = c.Apply([]Event{ev(1, "a")}) // 合法种子；万一失败也会被下面逐行比对抓出
	snap := c.History("K")
	bad := [][]Event{
		{{Key: "", Eff: 1, Op: interval.Upsert, Val: "x"}},
		{ev(-1, "x")}, {evd(math.MaxInt64)}, {ev(2, "b")},
	}
	want := []error{ErrEmptyKey, ErrEffOutOfRange, ErrEffOutOfRange, ErrTooManyPoints}
	for i := range bad {
		if err := c.Apply(bad[i]); !errors.Is(err, want[i]) || !reflect.DeepEqual(c.History("K"), snap) {
			return fmt.Errorf("selfcheck reject %d err=%v", i, err)
		}
	}
	return c.Apply([]Event{ev(1, "z")}) // 同 Eff 替换不占名额：必须成功
}
