package lwwset

import (
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"testing"
)

// naiveModel 是朴素参照实现：直接对每条记录取最大时间戳，不做任何并发控制。
type naiveModel struct {
	add map[string]int64
	del map[string]int64
}

func newNaive() *naiveModel {
	return &naiveModel{add: map[string]int64{}, del: map[string]int64{}}
}

func (m *naiveModel) addAt(e string, ts int64) {
	if ts > m.add[e] {
		m.add[e] = ts
	}
}

func (m *naiveModel) removeAt(e string, ts int64) {
	if ts > m.del[e] {
		m.del[e] = ts
	}
}

func (m *naiveModel) records() map[string]Record {
	out := map[string]Record{}
	for e, a := range m.add {
		out[e] = Record{Add: a, Remove: m.del[e]}
	}
	for e, d := range m.del {
		if _, ok := out[e]; !ok {
			out[e] = Record{Remove: d}
		}
	}
	return out
}

func TestIncrementalEqualsFullMerge(t *testing.T) {
	src, _ := New("src", 64)
	inc, _ := New("inc", 64)
	full, _ := New("full", 64)

	// 分三轮制造变更，每轮后对 inc 做增量合并，最后轮结束后对 full 做一次整份合并。
	ops := [][]struct {
		elem string
		ts   int64
		del  bool
	}{
		{{"a", 5, false}, {"b", 3, false}, {"a", 7, true}},
		{{"a", 9, false}, {"c", 1, false}, {"b", 3, true}},
		{{"c", 2, true}, {"d", 4, false}, {"a", 2, false}},
	}
	for round, batch := range ops {
		for _, op := range batch {
			var err error
			if op.del {
				err = src.Remove(op.elem, op.ts)
			} else {
				err = src.Add(op.elem, op.ts)
			}
			if err != nil {
				t.Fatalf("op: %v", err)
			}
			t.Logf("轮次=%d 输入=%s(%s,%d) src记录=%v", round, map[bool]string{true: "Remove", false: "Add"}[op.del], op.elem, op.ts, src.Snapshot())
		}
		if err := inc.MergeIncremental(src); err != nil {
			t.Fatalf("MergeIncremental: %v", err)
		}
		t.Logf("轮次=%d 增量合并后 inc记录=%v 合并位置=%d", round, inc.Snapshot(), inc.MergePosition("src"))
	}
	if err := full.Merge(src); err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if got, want := inc.Snapshot(), full.Snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("增量合并与整份合并不一致: 增量=%v 整份=%v", got, want)
	}
	if got, want := inc.Elements(), full.Elements(); !reflect.DeepEqual(got, want) {
		t.Fatalf("存活元素不一致: 增量=%v 整份=%v", got, want)
	}
	// 再次增量合并没有新变更，记录与序号不变。
	seqBefore := inc.Seq()
	if err := inc.MergeIncremental(src); err != nil {
		t.Fatalf("MergeIncremental: %v", err)
	}
	if inc.Seq() != seqBefore {
		t.Fatal("无新变更时增量合并不应推进变更序号")
	}
	t.Logf("增量与整份合并结果一致 记录=%v 存活=%v", inc.Snapshot(), inc.Elements())
}

func TestMergeLaws(t *testing.T) {
	type op struct {
		elem string
		ts   int64
		del  bool
	}
	build := func(id string, ops []op) *Set {
		s, _ := New(id, 64)
		for _, o := range ops {
			if o.del {
				if err := s.Remove(o.elem, o.ts); err != nil {
					t.Fatalf("Remove: %v", err)
				}
			} else {
				if err := s.Add(o.elem, o.ts); err != nil {
					t.Fatalf("Add: %v", err)
				}
			}
		}
		return s
	}
	opsA := []op{{"x", 5, false}, {"y", 8, false}, {"x", 6, true}}
	opsB := []op{{"y", 8, true}, {"z", 2, false}, {"x", 4, false}}
	opsC := []op{{"x", 6, false}, {"z", 9, true}}

	// 可交换：A∪B == B∪A
	a1, b1 := build("a1", opsA), build("b1", opsB)
	a2, b2 := build("a2", opsA), build("b2", opsB)
	if err := a1.Merge(b1); err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if err := b2.Merge(a2); err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if !reflect.DeepEqual(a1.Snapshot(), b2.Snapshot()) {
		t.Fatalf("合并不可交换: A∪B=%v B∪A=%v", a1.Snapshot(), b2.Snapshot())
	}

	// 幂等：A∪B 再合并 B 不变
	snap := a1.Snapshot()
	if err := a1.Merge(b1); err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if !reflect.DeepEqual(a1.Snapshot(), snap) {
		t.Fatalf("合并不幂等: 前=%v 后=%v", snap, a1.Snapshot())
	}

	// 可结合：(A∪B)∪C == A∪(B∪C)
	ab, c1 := build("ab", opsA), build("c1", opsC)
	if err := ab.Merge(build("b3", opsB)); err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if err := ab.Merge(c1); err != nil {
		t.Fatalf("Merge: %v", err)
	}
	bc, a3 := build("bc", opsB), build("a3", opsA)
	if err := bc.Merge(build("c2", opsC)); err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if err := a3.Merge(bc); err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if !reflect.DeepEqual(ab.Snapshot(), a3.Snapshot()) {
		t.Fatalf("合不可结合: (A∪B)∪C=%v A∪(B∪C)=%v", ab.Snapshot(), a3.Snapshot())
	}
	t.Logf("合并律成立 交换/幂等/结合 结果=%v 存活=%v", ab.Snapshot(), ab.Elements())
}

// TestConvergesWithNaiveModel 随机操作多个副本，两两合并至收敛后与朴素参照比对。
func TestConvergesWithNaiveModel(t *testing.T) {
	const replicas = 4
	rng := rand.New(rand.NewSource(42))
	sets := make([]*Set, replicas)
	for i := range sets {
		sets[i], _ = New(fmt.Sprintf("r%d", i), 256)
	}
	model := newNaive()
	elems := []string{"a", "b", "c", "d", "e", "f"}
	for step := 0; step < 200; step++ {
		s := sets[rng.Intn(replicas)]
		e := elems[rng.Intn(len(elems))]
		ts := int64(rng.Intn(50) + 1)
		if rng.Intn(2) == 0 {
			if err := s.Add(e, ts); err != nil {
				t.Fatalf("Add: %v", err)
			}
			model.addAt(e, ts)
			t.Logf("步骤=%d 输入=Add(%s,%d) 副本=%s", step, e, ts, s.ID())
		} else {
			if err := s.Remove(e, ts); err != nil {
				t.Fatalf("Remove: %v", err)
			}
			model.removeAt(e, ts)
			t.Logf("步骤=%d 输入=Remove(%s,%d) 副本=%s", step, e, ts, s.ID())
		}
	}
	// 随机顺序两两合并（混合整份与增量）直至收敛。
	for round := 0; round < 8; round++ {
		for i := 0; i < replicas; i++ {
			for j := 0; j < replicas; j++ {
				if i == j {
					continue
				}
				var err error
				if (i+j+round)%2 == 0 {
					err = sets[i].Merge(sets[j])
				} else {
					err = sets[i].MergeIncremental(sets[j])
				}
				if err != nil {
					t.Fatalf("merge r%d<-r%d: %v", i, j, err)
				}
			}
		}
	}
	want := model.records()
	for i, s := range sets {
		if got := s.Snapshot(); !reflect.DeepEqual(got, want) {
			t.Fatalf("副本 r%d 未收敛到朴素参照: got=%v want=%v", i, got, want)
		}
		if err := s.Check(); err != nil {
			t.Fatalf("副本 r%d 自检失败: %v", i, err)
		}
		t.Logf("副本=r%d 收敛一致 记录=%v 存活=%v", i, s.Snapshot(), s.Elements())
	}
}

// TestConcurrentMergeNoDeadlock 多执行体并发增删、双向合并、查询与自检。
func TestConcurrentMergeNoDeadlock(t *testing.T) {
	const replicas = 3
	sets := make([]*Set, replicas)
	for i := range sets {
		sets[i], _ = New(fmt.Sprintf("r%d", i), 1024)
	}
	var wg sync.WaitGroup
	for w := 0; w < 6; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(w)))
			for i := 0; i < 300; i++ {
				a := rng.Intn(replicas)
				b := rng.Intn(replicas)
				e := fmt.Sprintf("e%d", rng.Intn(20))
				ts := int64(rng.Intn(1000) + 1)
				switch rng.Intn(6) {
				case 0:
					_ = sets[a].Add(e, ts)
				case 1:
					_ = sets[a].Remove(e, ts)
				case 2:
					if a != b {
						_ = sets[a].Merge(sets[b])
					}
				case 3:
					if a != b {
						_ = sets[a].MergeIncremental(sets[b])
					}
				case 4:
					_ = sets[a].Contains(e)
					_, _ = sets[a].Lookup(e)
				case 5:
					_ = sets[a].Check()
				}
			}
		}(w)
	}
	// 互逆方向合并同时进行，验证不死锁。
	for i := 0; i < replicas; i++ {
		for j := 0; j < replicas; j++ {
			if i >= j {
				continue
			}
			wg.Add(2)
			go func(i, j int) { defer wg.Done(); _ = sets[i].Merge(sets[j]) }(i, j)
			go func(i, j int) { defer wg.Done(); _ = sets[j].Merge(sets[i]) }(i, j)
		}
	}
	wg.Wait()

	// 全部完成后合并至收敛，各副本集合一致。
	for round := 0; round < 4; round++ {
		for i := 0; i < replicas; i++ {
			for j := 0; j < replicas; j++ {
				if i != j {
					if err := sets[i].Merge(sets[j]); err != nil {
						t.Fatalf("merge: %v", err)
					}
				}
			}
		}
	}
	base := sets[0].Snapshot()
	for i := 1; i < replicas; i++ {
		if got := sets[i].Snapshot(); !reflect.DeepEqual(got, base) {
			t.Fatalf("并发后副本未收敛: r0=%v r%d=%v", base, i, got)
		}
		if err := sets[i].Check(); err != nil {
			t.Fatalf("副本 r%d 自检失败: %v", i, err)
		}
	}
	t.Logf("并发完成且收敛一致 存活=%v", sets[0].Elements())
}
