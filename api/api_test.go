package api

import (
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/rec"
)

// genSeq 用确定性循环生成 n 条混合写（双写/单侧写/墓碑交替），Ver 严格递增。
func genSeq(n int) []op {
	ops := make([]op, 0, n)
	for i := 0; i < n; i++ {
		ver := int64(i + 1)
		key := fmt.Sprintf("k%d", i%7) // 键复用，制造覆盖与分歧
		switch i % 5 {
		case 0:
			ops = append(ops, op{2, i % 2, key, "v" + fmt.Sprint(i), ver})
		case 1:
			ops = append(ops, op{3, i % 2, key, "", ver})
		case 2:
			ops = append(ops, op{1, -1, key, "", ver})
		default:
			ops = append(ops, op{0, -1, key, "v" + fmt.Sprint(i), ver})
		}
	}
	return ops
}

func runSeq(t *testing.T, ops []op) *API {
	t.Helper()
	a := New()
	for i, o := range ops {
		if err := apply(a, o); err != nil {
			t.Fatalf("op %d: %v", i, err)
		}
	}
	return a
}

// TestViewMatchesBatchModel 钉住不变量1：对账后 View 与批量重算逐键相同。
func TestViewMatchesBatchModel(t *testing.T) {
	cases := []struct {
		name string
		ops  []op
	}{
		{"seven-writes", []op{
			{0, -1, "a", "a1", 1}, {0, -1, "b", "b1", 2}, {0, -1, "c", "c1", 3},
			{2, 0, "a", "a2", 4}, {3, 1, "c", "", 5}, {3, 0, "b", "", 6}, {2, 0, "d", "d1", 7},
		}},
	}
	for _, n := range []int{10, 100, 1000} {
		cases = append(cases, struct {
			name string
			ops  []op
		}{fmt.Sprintf("generated-%d", n), genSeq(n)})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := runSeq(t, tc.ops)
			a.Reconcile()
			if got, want := a.View(), batchModel(tc.ops); !reflect.DeepEqual(got, want) {
				t.Errorf("View=%v, 批量重算=%v", got, want)
			}
		})
	}
}

// TestRejectedWritesLeaveNoTrace 钉住不变量4：三类错误可判定且互不相同，被拒后状态不变。
func TestRejectedWritesLeaveNoTrace(t *testing.T) {
	a := New()
	if err := a.Put("k", "v", 1); err != nil {
		t.Fatal(err)
	}
	before := a.View()
	bads := []struct {
		name string
		err  error
		want error
	}{
		{"empty-key", a.Put("", "v", 2), rec.ErrBadKey},
		{"empty-val", a.Put("k2", "", 2), rec.ErrBadVal},
		{"ver-not-increasing", a.Put("k2", "v", 1), rec.ErrBadVer},
		{"ver-non-positive", a.Del("k2", 0), rec.ErrBadVer},
		{"empty-key-delone", a.DelOne(1, "", 2), rec.ErrBadKey},
	}
	seen := map[error]bool{}
	for _, b := range bads {
		if !reflect.DeepEqual(b.err, b.want) {
			t.Errorf("%s: err=%v, want %v", b.name, b.err, b.want)
		}
		seen[b.err] = true
	}
	// 三类故障（Key/Val/Ver）的哨兵错误必须互不相同。
	if !seen[rec.ErrBadKey] || !seen[rec.ErrBadVal] || !seen[rec.ErrBadVer] || len(seen) != 3 {
		t.Errorf("三类错误未做到互不相同: %v", seen)
	}
	if !reflect.DeepEqual(a.View(), before) {
		t.Errorf("被拒写改变了状态: %v -> %v", before, a.View())
	}
	if err := a.Put("k2", "v", 2); err != nil { // 全局最大版本未被污染，仍可用
		t.Errorf("被拒写后引擎不可用: %v", err)
	}
}

// TestConcurrentReadsConsistent 钉住并发：N 个 goroutine 只读同一已对账实例，视图逐字段相同。
func TestConcurrentReadsConsistent(t *testing.T) {
	a := runSeq(t, genSeq(200))
	a.Reconcile()
	want := a.View()
	var wg sync.WaitGroup
	var bad atomic.Bool
	start := make(chan struct{})
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i := 0; i < 200; i++ {
				if !reflect.DeepEqual(a.View(), want) {
					bad.Store(true)
				}
				a.Reconcile()
				if a.SelfCheck() != nil {
					bad.Store(true)
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	if bad.Load() {
		t.Error("并发只读拿到不一致的视图")
	}
}

// TestSelfCheck 自检方法本身必须通过。
func TestSelfCheck(t *testing.T) {
	if err := New().SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
