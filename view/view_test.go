package view_test

import (
	"errors"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/agg"
	"ontology/audit"
	"ontology/change"
	"ontology/view"
)

func ins(v uint64, id, g string, val float64) change.Change {
	return change.Change{Version: v, Op: change.Insert, HasGroup: true, Group: g, ID: id, Value: val}
}

func del(v uint64, id string) change.Change {
	return change.Change{Version: v, Op: change.Delete, ID: id}
}

// TestRecomputeBounds 10 万条记录 + 5000 次删除，仅 3 次删到当前 Min。
func TestRecomputeBounds(t *testing.T) {
	v, _ := view.Open("")
	ver := uint64(0)
	for i := 0; i < 100000; i++ {
		ver++
		v.Apply(ins(ver, fmt.Sprintf("a%06d", i), "A", float64(i)))
	}
	for i := 0; i < 10; i++ { // 另一组，验证重算不扫描它
		ver++
		v.Apply(ins(ver, fmt.Sprintf("b%d", i), "B", float64(i)))
	}
	ids := []string{"a000000"}
	for i := 50000; i < 54997; i++ {
		ids = append(ids, fmt.Sprintf("a%06d", i))
	}
	ids = append(ids, "a000001", "a000002")
	for _, id := range ids {
		ver++
		if err := v.Apply(del(ver, id)); err != nil {
			t.Fatal(err)
		}
	}
	cnt, vis := v.RecomputeStats()
	if cnt[agg.Min] != 3 || cnt[agg.Count] != 0 || cnt[agg.Sum] != 0 ||
		cnt[agg.Max] != 0 || cnt[agg.DistinctCount] != 0 {
		t.Fatalf("重算次数 %v", cnt)
	}
	if want := int64(99999 + 95001 + 95000); vis[agg.Min] != want {
		t.Fatalf("Min 重算访问成员 %d, 期望 %d(不超过组当前成员数)", vis[agg.Min], want)
	}
}

// TestAuditRandom 5 万条随机变更，每 1000 条与全量重算逐组位级比对。
func TestAuditRandom(t *testing.T) {
	v, _ := view.Open("")
	rng := rand.New(rand.NewSource(42))
	model := map[string]audit.Record{}
	live := []string{}
	seq := 0
	for i := 1; i <= 50000; i++ {
		g := fmt.Sprintf("g%d", rng.Intn(10))
		if i%37 == 0 {
			g = ""
		}
		val := rng.NormFloat64() * 1e6
		if i%101 == 0 {
			val = math.Copysign(0, -1)
		}
		var c change.Change
		switch {
		case len(live) == 0 || rng.Intn(2) == 0:
			seq++
			id := fmt.Sprintf("id%d", seq)
			c = ins(uint64(i), id, g, val)
			model[id] = audit.Record{Group: g, Value: val}
			live = append(live, id)
		case rng.Intn(2) == 0:
			j := rng.Intn(len(live))
			id := live[j]
			live = append(live[:j], live[j+1:]...)
			c = del(uint64(i), id)
			delete(model, id)
		default:
			id := live[rng.Intn(len(live))]
			c = change.Change{Version: uint64(i), Op: change.Update, HasGroup: true, Group: g, ID: id, Value: val}
			model[id] = audit.Record{Group: g, Value: val}
		}
		if err := v.Apply(c); err != nil {
			t.Fatalf("变更 %d: %v", i, err)
		}
		if i%1000 == 0 {
			if err := audit.Compare(v.Snapshot(), audit.Full(model)); err != nil {
				t.Fatalf("第 %d 条后: %v", i, err)
			}
		}
	}
}

// TestSemantics 表驱动：版本单调/幂等/拒绝计数与全部边界语义。
func TestSemantics(t *testing.T) {
	nan := math.NaN()
	cases := []struct {
		name     string
		changes  []change.Change
		errs     []error
		rejected uint64
		snap     map[string]view.GroupState
		gone     string
	}{
		{"空视图", nil, nil, 0, map[string]view.GroupState{}, "x"},
		{"单组单记录", []change.Change{ins(1, "a", "g", 5)}, nil, 0,
			map[string]view.GroupState{"g": {Count: 1, Sum: 5, Min: 5, Max: 5, Distinct: 1}}, ""},
		{"所有记录同组", []change.Change{ins(1, "a", "g", 1), ins(2, "b", "g", 2), ins(3, "c", "g", 3)}, nil, 0,
			map[string]view.GroupState{"g": {Count: 3, Sum: 6, Min: 1, Max: 3, Distinct: 3}}, ""},
		{"空串分组键合法", []change.Change{ins(1, "a", "", 7)}, nil, 0,
			map[string]view.GroupState{"": {Count: 1, Sum: 7, Min: 7, Max: 7, Distinct: 1}}, ""},
		{"缺失分组键拒绝", []change.Change{{Version: 1, Op: change.Insert, ID: "a", Value: 1}},
			[]error{view.ErrNoGroup}, 1, map[string]view.GroupState{}, ""},
		{"NaN拒绝", []change.Change{ins(1, "a", "g", nan)},
			[]error{view.ErrNaN}, 1, map[string]view.GroupState{}, ""},
		{"正负零相等", []change.Change{ins(1, "a", "g", 0), ins(2, "b", "g", math.Copysign(0, -1))}, nil, 0,
			map[string]view.GroupState{"g": {Count: 2, Sum: 0, Min: 0, Max: 0, Distinct: 1}}, ""},
		{"更新移出原组", []change.Change{ins(1, "a", "g1", 1), ins(2, "b", "g2", 2),
			{Version: 3, Op: change.Update, HasGroup: true, Group: "g2", ID: "a", Value: 3}}, nil, 0,
			map[string]view.GroupState{"g2": {Count: 2, Sum: 5, Min: 2, Max: 3, Distinct: 2}}, "g1"},
		{"删空组消失", []change.Change{ins(1, "a", "g", 1), del(2, "a")}, nil, 0,
			map[string]view.GroupState{}, "g"},
		{"重复投递幂等", []change.Change{ins(1, "a", "g", 1), ins(1, "a", "g", 1)}, nil, 0,
			map[string]view.GroupState{"g": {Count: 1, Sum: 1, Min: 1, Max: 1, Distinct: 1}}, ""},
		{"同版本冲突拒绝", []change.Change{ins(1, "a", "g", 1), ins(1, "b", "g", 2)},
			[]error{nil, view.ErrVersion}, 1,
			map[string]view.GroupState{"g": {Count: 1, Sum: 1, Min: 1, Max: 1, Distinct: 1}}, ""},
		{"版本回退拒绝", []change.Change{ins(5, "a", "g", 1), ins(3, "b", "g", 2)},
			[]error{nil, view.ErrVersion}, 1,
			map[string]view.GroupState{"g": {Count: 1, Sum: 1, Min: 1, Max: 1, Distinct: 1}}, ""},
		{"删除未知记录拒绝", []change.Change{del(1, "zz")}, []error{view.ErrNoRecord}, 1,
			map[string]view.GroupState{}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v, _ := view.Open("")
			for i, c := range tc.changes {
				err := v.Apply(c)
				var want error
				if tc.errs != nil {
					want = tc.errs[i]
				}
				if !errors.Is(err, want) {
					t.Fatalf("变更 %d 错误 %v, 期望 %v", i, err, want)
				}
			}
			if v.Rejected() != tc.rejected {
				t.Fatalf("拒绝数 %d, 期望 %d", v.Rejected(), tc.rejected)
			}
			if !reflect.DeepEqual(v.Snapshot(), tc.snap) {
				t.Fatalf("快照 %+v, 期望 %+v", v.Snapshot(), tc.snap)
			}
			if tc.gone != "" {
				if _, ok := v.Group(tc.gone); ok {
					t.Fatalf("组 %q 应不存在", tc.gone)
				}
			}
		})
	}
}

// TestCrashRecovery 三个崩溃点恢复后视图与不崩溃时逐字段相同。
func TestCrashRecovery(t *testing.T) {
	base := []change.Change{}
	for i := 0; i < 40; i++ {
		base = append(base, ins(uint64(i+1), fmt.Sprintf("i%d", i), fmt.Sprintf("g%d", i%3), float64(i)))
	}
	for i := 0; i < 10; i++ { // 含删到 g0 当前 Min(i0=0) 的删除
		base = append(base, del(uint64(41+i), fmt.Sprintf("i%d", i)))
	}
	last := ins(51, "i40", "g0", 99)
	for _, stage := range []view.Stage{view.StageApply, view.StageRecompute, view.StageCommit} {
		path := filepath.Join(t.TempDir(), "v.jrn")
		v, _ := view.Open(path)
		for _, c := range base {
			v.Apply(c)
		}
		v.InjectCrash(stage)
		if err := v.Apply(last); !errors.Is(err, view.ErrCrash) {
			t.Fatalf("stage %d: %v", stage, err)
		}
		v.Close()
		got, _ := view.Open(path)
		ref, _ := view.Open("")
		for _, c := range base {
			ref.Apply(c)
		}
		if stage != view.StageApply { // 日志已追加，恢复后包含该变更
			ref.Apply(last)
		}
		if !reflect.DeepEqual(got.Snapshot(), ref.Snapshot()) ||
			got.MaxVersion() != ref.MaxVersion() {
			t.Fatalf("stage %d 恢复后不一致", stage)
		}
		got.Close()
	}
}

// TestConcurrency 并发提交不丢写、读者不见半更新状态，最终与全量一致。
func TestConcurrency(t *testing.T) {
	v, _ := view.Open("")
	var ver uint64
	var wg sync.WaitGroup
	var mu sync.Mutex
	model := map[string]audit.Record{}
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 250; i++ {
				id := fmt.Sprintf("w%d-%d", w, i)
				g := fmt.Sprintf("g%d", i%5)
				for {
					c := ins(atomic.AddUint64(&ver, 1), id, g, float64(i))
					if v.Apply(c) == nil {
						mu.Lock()
						model[id] = audit.Record{Group: g, Value: float64(i)}
						mu.Unlock()
						break
					}
				}
			}
		}(w)
	}
	stop := make(chan struct{})
	go func() { // 读者：任何时刻不得见半更新状态
		for {
			select {
			case <-stop:
				return
			default:
			}
			for _, gs := range v.Snapshot() {
				if gs.Count < gs.Distinct || (gs.Count > 0 && gs.Min > gs.Max) {
					t.Errorf("半更新状态: %+v", gs)
				}
			}
		}
	}()
	wg.Wait()
	close(stop)
	if err := audit.Compare(v.Snapshot(), audit.Full(model)); err != nil {
		t.Fatal(err)
	}
}

// TestTruncatedReplay 逐字节截断的日志恢复后，视图等于完整前缀的全量重算。
func TestTruncatedReplay(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "t.jrn")
	v, _ := view.Open(path)
	all := []change.Change{}
	for i := 0; i < 200; i++ {
		c := ins(uint64(i+1), fmt.Sprintf("id%06d", i), fmt.Sprintf("g%05d", i%50), float64(i))
		all = append(all, c)
		v.Apply(c)
	}
	v.Close()
	data, _ := os.ReadFile(path)
	for cut := 1; cut < len(data); cut++ {
		p := filepath.Join(dir, "c.jrn")
		os.WriteFile(p, data[:cut], 0o644)
		got, err := view.Open(p)
		if err != nil {
			t.Fatalf("cut=%d: %v", cut, err)
		}
		n := 0
		if cut >= 8 {
			n = (cut - 8) / 44
		}
		model := map[string]audit.Record{}
		for _, c := range all[:n] {
			model[c.ID] = audit.Record{Group: c.Group, Value: c.Value}
		}
		if err := audit.Compare(got.Snapshot(), audit.Full(model)); err != nil {
			t.Fatalf("cut=%d: %v", cut, err)
		}
		got.Close()
	}
}
