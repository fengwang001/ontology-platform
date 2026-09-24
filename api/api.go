// Package api 是 GROUPING SETS 增量聚合的对外入口，依赖 agg、gset，方向单向。
package api

import (
	"errors"
	"fmt"
	"reflect"
	"sync"

	"ontology/agg"
	"ontology/gset"
)

// 四类可判定哨兵错误，彼此互不相同。
var (
	ErrInvalidGroups = errors.New("api: invalid grouping sets (empty/duplicate/out-of-range)") // 空/重复/越界
	ErrEmptyDim      = errors.New("api: dimension values must be non-empty")                   // A/B/C 为空
	ErrZeroMeasure   = errors.New("api: measure M must be non-zero")                           // M==0
	ErrWithdrawn     = agg.ErrWithdrawn                                                        // 撤回使某 (组,键) 变负
)

// Fact 是上游事实流的一条。M>0 累加，M<0 撤回。
type Fact struct {
	A, B, C string
	M       int64
}

// View 是指定分组集上的求和物化视图，只由 Apply 增量更新。
type View struct {
	mu    sync.RWMutex
	table *agg.Table
}

// New 用分组集（每个元素是维度下标子集，nil/空切片表示 ()）建视图。
func New(groups [][]int) (*View, error) {
	if len(groups) == 0 {
		return nil, ErrInvalidGroups
	}
	gs := make([]gset.Group, 0, len(groups))
	seen := map[int]bool{}
	for _, dims := range groups {
		g, err := gset.NewGroup(dims)
		if err != nil || seen[g.ID()] {
			return nil, ErrInvalidGroups
		}
		seen[g.ID()] = true
		gs = append(gs, g)
	}
	return &View{table: agg.New(gs)}, nil
}

// Apply 增量更新视图。维度为空、M==0、撤回越界均被整体拒绝且状态不变。
func (v *View) Apply(f Fact) error {
	if f.A == "" || f.B == "" || f.C == "" {
		return ErrEmptyDim
	}
	if f.M == 0 {
		return ErrZeroMeasure
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.table.Apply([gset.NumDims]string{f.A, f.B, f.C}, f.M)
}

// View 返回深拷贝：组 ID → 键串 → SUM(M)；零值键不出现，可并发调用。
func (v *View) View() map[int]map[string]int64 {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.table.Snapshot()
}

var specGroups = [][]int{{0}, {0, 1}, nil}

// SelfCheck 用第三节内置八事实序列核验四条不变量与四类非法输入，通过返回 nil。
func SelfCheck() error {
	v, err := New(specGroups)
	if err != nil {
		return err
	}
	seq := []Fact{
		{"x", "p", "u", 10}, {"x", "p", "v", 20}, {"x", "q", "u", 5},
		{"y", "p", "u", 7}, {"x", "p", "v", -20}, {"y", "p", "u", 3},
		{"x", "p", "u", -10}, {"x", "q", "u", 2},
	}
	grand := []int64{10, 30, 35, 42, 22, 25, 15, 17}
	for i, f := range seq {
		if err := v.Apply(f); err != nil {
			return fmt.Errorf("selfcheck step %d: %w", i+1, err)
		}
		if v.View()[7][""] != grand[i] {
			return fmt.Errorf("selfcheck step %d: grand sum want %d", i+1, grand[i])
		}
	}
	got := v.View()
	if !reflect.DeepEqual(got, batch(seq)) { // 不变量 1
		return errors.New("selfcheck: view differs from batch recomputation")
	}
	ids := map[int]bool{}
	for id := range got {
		ids[id] = true
	}
	if !reflect.DeepEqual(ids, map[int]bool{1: true, 3: true, 7: true}) { // 不变量 2
		return errors.New("selfcheck: group ids not exactly {1,3,7}")
	}
	return rejectChecks(v, got) // 不变量 3、4
}

// batch 是独立参照实现：净事实按各组投影分组求和，零值键删除。
func batch(seq []Fact) map[int]map[string]int64 {
	out := map[int]map[string]int64{}
	for _, dims := range specGroups {
		g, _ := gset.NewGroup(dims)
		b := map[string]int64{}
		for _, f := range seq {
			b[g.Key([gset.NumDims]string{f.A, f.B, f.C})] += f.M
		}
		for k, s := range b {
			if s == 0 {
				delete(b, k)
			}
		}
		out[g.ID()] = b
	}
	return out
}

// rejectChecks 核验不变量 3（越界被拒）、4（拒绝不留痕）与构造期三类非法。
func rejectChecks(v *View, before map[int]map[string]int64) error {
	bad := []Fact{
		{"", "p", "u", 1},     // 维度为空
		{"x", "p", "u", 0},    // M==0
		{"x", "q", "u", -100}, // 撤回越界：(A) 的 x=7 会变负
	}
	want := []error{ErrEmptyDim, ErrZeroMeasure, ErrWithdrawn}
	for i, f := range bad {
		if err := v.Apply(f); !errors.Is(err, want[i]) {
			return fmt.Errorf("selfcheck reject %d: got %v want %v", i, err, want[i])
		}
		if !reflect.DeepEqual(v.View(), before) {
			return fmt.Errorf("selfcheck reject %d: state changed", i)
		}
	}
	for _, gs := range [][][]int{nil, {{0}, {0}}, {{5}}} {
		if _, err := New(gs); !errors.Is(err, ErrInvalidGroups) {
			return fmt.Errorf("selfcheck constructor %v: %v", gs, err)
		}
	}
	return nil
}
