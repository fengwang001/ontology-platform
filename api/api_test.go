package api_test

import (
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/api"
)

// TestBatchEquiv：随机写入序列的 View 必须等于朴素批量重算，且与到达顺序无关。
func TestBatchEquiv(t *testing.T) {
	type op struct {
		key, col string
		ts       int64
		val      string
		del      bool
	}
	type win struct {
		ts  int64
		val string
		tom bool
	}
	for seed := int64(0); seed < 20; seed++ {
		rng := rand.New(rand.NewSource(seed))
		ops := make([]op, 200)
		for i := range ops {
			ops[i] = op{fmt.Sprintf("k%d", rng.Intn(4)), fmt.Sprintf("c%d", rng.Intn(5)),
				int64(rng.Intn(10)), fmt.Sprintf("v%d", rng.Intn(4)), rng.Intn(3) == 0}
		}
		best := map[[2]string]win{} // 朴素批量：每列取最大 TS；平局墓碑胜，否则字典序最大
		for _, o := range ops {
			k := [2]string{o.key, o.col}
			if w, ok := best[k]; !ok || o.ts > w.ts ||
				(o.ts == w.ts && (o.del && !w.tom || !o.del && !w.tom && o.val > w.val)) {
				best[k] = win{o.ts, o.val, o.del}
			}
		}
		want := map[string]map[string]string{}
		for k, w := range best {
			if !w.tom {
				if want[k[0]] == nil {
					want[k[0]] = map[string]string{}
				}
				want[k[0]][k[1]] = w.val
			}
		}
		for rep := 0; rep < 2; rep++ { // 两种随机顺序重放，结果都必须等于批量结果
			s := api.New()
			for _, i := range rand.New(rand.NewSource(seed*10 + int64(rep))).Perm(len(ops)) {
				if o := ops[i]; o.del {
					_ = s.Del(o.key, o.col, o.ts)
				} else {
					_ = s.Put(o.key, o.col, o.ts, o.val)
				}
			}
			if got := s.View(); !reflect.DeepEqual(got, want) {
				t.Fatalf("seed %d rep %d: got %v want %v", seed, rep, got, want)
			}
		}
	}
}

// TestTombstone：墓碑压住 TS<=它的值（含平局），绝不压更新的值；被删列缺席。
func TestTombstone(t *testing.T) {
	s := api.New()
	_ = s.Put("R", "c", 5, "a")
	_ = s.Del("R", "c", 5)        // 平局墓碑胜
	_ = s.Put("R", "c", 4, "old") // 更旧的值压不住墓碑
	if v := s.View(); len(v) != 0 {
		t.Fatalf("tombstone must hide older/equal values: %v", v)
	}
	_ = s.Put("R", "c", 6, "new") // 更新的值压过墓碑
	if v := s.View()["R"]; v["c"] != "new" {
		t.Fatalf("newer val must beat tombstone: %v", v)
	}
}

// TestRejectNoTrace：四类拒绝互不相同、状态不变、之后可正常使用。
func TestRejectNoTrace(t *testing.T) {
	s := api.New()
	_ = s.Put("R", "c1", 5, "a")
	beforeV, beforeC := s.View(), s.Conflicted()
	bads := []error{
		s.Put("", "c", 1, "v"), s.Put("R", "", 1, "v"), s.Put("R", "c", -1, "v"),
		s.Put("R", "c", 1, ""), s.Del("", "c", 1), s.Del("R", "", 1), s.Del("R", "c", -1),
	}
	seen := map[error]bool{}
	for _, e := range bads {
		if e == nil {
			t.Fatal("invalid op accepted")
		}
		seen[e] = true
	}
	if len(seen) != 4 {
		t.Fatalf("want 4 distinct sentinels, got %d", len(seen))
	}
	if !reflect.DeepEqual(s.View(), beforeV) || !reflect.DeepEqual(s.Conflicted(), beforeC) {
		t.Fatal("rejected ops left trace")
	}
	if err := s.Put("R", "c1", 6, "b"); err != nil || s.View()["R"]["c1"] != "b" {
		t.Fatal("store unusable after rejections")
	}
}

// TestConcurrent：N 个 goroutine 写同一 Key 的不同列，读方校验值一致；无 sleep。
func TestConcurrent(t *testing.T) {
	s := api.New()
	const n = 64
	var wg sync.WaitGroup
	var bad atomic.Bool
	start, done := make(chan struct{}), make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			default:
				for col, v := range s.View()["K"] {
					if v != "v"+col[1:] {
						bad.Store(true)
					}
				}
			}
		}
	}()
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_ = s.Put("K", fmt.Sprintf("c%d", i), 1, fmt.Sprintf("v%d", i))
		}(i)
	}
	close(start)
	wg.Wait()
	close(done)
	v := s.View()["K"]
	for i := 0; i < n; i++ {
		if v[fmt.Sprintf("c%d", i)] != fmt.Sprintf("v%d", i) {
			t.Fatalf("col c%d wrong", i)
		}
	}
	if len(v) != n || bad.Load() {
		t.Fatalf("len=%d inconsistent=%v", len(v), bad.Load())
	}
}
