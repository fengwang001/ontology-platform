package ddl

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"

	"ontology/phase"
)

// 回填复杂度：第一次 Backfill 检查约 m 个键，紧接着的第二次不随 m 增长（为 0）。
func TestBackfillSkipCounter(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		t.Run(fmt.Sprint(m), func(t *testing.T) {
			s := New(1 << 30)
			for i := 0; i < m; i++ {
				if err := s.Put(fmt.Sprintf("k%d", i), i+1); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.BeginMigration(); err != nil {
				t.Fatal(err)
			}
			if err := s.Backfill(); err != nil {
				t.Fatal(err)
			}
			if s.lastChecked != m {
				t.Fatalf("first backfill checked %d, want %d", s.lastChecked, m)
			}
			if err := s.Backfill(); err != nil {
				t.Fatal(err)
			}
			if s.lastChecked != 0 {
				t.Fatalf("second backfill checked %d, want 0", s.lastChecked)
			}
			if err := s.Put("new", 1); err != nil { // DualWrite 新键已双写，回填应跳过
				t.Fatal(err)
			}
			if err := s.Backfill(); err != nil {
				t.Fatal(err)
			}
			if s.lastChecked != 0 {
				t.Fatalf("third backfill checked %d, want 0", s.lastChecked)
			}
		})
	}
}

// 不变量 2：DualWrite 成功 Put 后 v2[k]==2*v1[k]；故障下两表同时不变。
func TestDualWriteAtomic(t *testing.T) {
	s := New(100)
	s.Put("a", 3)
	s.BeginMigration()
	s.Put("b", 4)
	_, v1, v2 := s.Snapshot()
	if v2["b"] != 2*v1["b"] {
		t.Fatalf("v2[b]=%d != 2*v1[b]=%d", v2["b"], v1["b"])
	}
	s.InjectDualWriteFault()
	if err := s.Put("c", 1); err != ErrDualWrite {
		t.Fatalf("err=%v, want ErrDualWrite", err)
	}
	_, w1, w2 := s.Snapshot()
	if !reflect.DeepEqual(v1, w1) || !reflect.DeepEqual(v2, w2) {
		t.Fatalf("fault changed tables: v1 %v->%v v2 %v->%v", v1, w1, v2, w2)
	}
}

// 不变量 3：Backfill 任意次，v2 恒等于 2*v1（循环生成多档次数）。
func TestBackfillIdempotent(t *testing.T) {
	var base map[string]int
	for n := 1; n <= 4; n++ {
		s := New(1000)
		s.Put("a", 3)
		s.Put("b", 5)
		s.BeginMigration()
		for i := 0; i < n; i++ {
			if err := s.Backfill(); err != nil {
				t.Fatal(err)
			}
		}
		_, v1, v2 := s.Snapshot()
		for k, v := range v1 {
			if v2[k] != 2*v {
				t.Fatalf("n=%d: v2[%q]=%d != 2*%d", n, k, v2[k], v)
			}
		}
		if base == nil {
			base = v2
		} else if !reflect.DeepEqual(base, v2) {
			t.Fatalf("n=%d changed v2: %v vs %v", n, v2, base)
		}
	}
}

// 不变量 1（随机到达顺序）：任意操作序列后，Get 等于按最终阶段取表的批量结果。
func TestRandomSequencesBatchEquivalent(t *testing.T) {
	keys := []string{"a", "b", "c", "d", "e"}
	for seed := int64(0); seed < 20; seed++ {
		r := rand.New(rand.NewSource(seed))
		s := New(1 << 20)
		for i := 0; i < 50; i++ {
			switch r.Intn(4) {
			case 0:
				s.Put(keys[r.Intn(len(keys))], r.Intn(1000))
			case 1:
				s.BeginMigration()
			case 2:
				s.Backfill()
			case 3:
				s.Switch()
			}
		}
		ph, v1, v2 := s.Snapshot()
		want := v1
		if ph == phase.Switched {
			want = v2
		}
		for _, k := range keys {
			if got := s.Get(k); got != want[k] {
				t.Fatalf("seed %d Get(%q)=%d, batch=%d", seed, k, got, want[k])
			}
		}
	}
}
