package api_test

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"

	"ontology/api"
)

// stream 用确定性伪随机序列（含乱序与重复，ID 数 < maxOpen 不涉淘汰）跑流式去重。
func stream(t *testing.T, w int64, maxID, n int, seed int64) ([]string, []int64, []bool) {
	d, err := api.New(w, maxID+10)
	if err != nil {
		t.Fatal(err)
	}
	r := rand.New(rand.NewSource(seed))
	ids, tss, oks := make([]string, n), make([]int64, n), make([]bool, n)
	for i := 0; i < n; i++ {
		ids[i], tss[i] = fmt.Sprintf("id%d", r.Intn(maxID)), r.Int63n(3000)
		if oks[i], err = d.Dedup(ids[i], tss[i]); err != nil {
			t.Fatal(err)
		}
	}
	return ids, tss, oks
}

// TestWindowNonOverlap 不变量 1：同一 ID 相邻两次接受的 TS 差 >= W。
func TestWindowNonOverlap(t *testing.T) {
	for _, w := range []int64{1, 5, 100} {
		ids, tss, oks := stream(t, w, 50, 5000, w)
		last := map[string]int64{}
		for i := range ids {
			if !oks[i] {
				continue
			}
			if prev, seen := last[ids[i]]; seen && tss[i]-prev < w {
				t.Fatalf("w=%d: %s accepted with gap %d < W", w, ids[i], tss[i]-prev)
			}
			last[ids[i]] = tss[i]
		}
	}
}

// TestNaiveReplayConsistency 不变量 2：流式分类与朴素逐事件重放完全一致。
func TestNaiveReplayConsistency(t *testing.T) {
	for _, w := range []int64{1, 7, 50} {
		ids, tss, oks := stream(t, w, 80, 5000, w*31)
		naive := map[string]int64{}
		for i := range ids {
			prev, seen := naive[ids[i]]
			want := !seen || tss[i]-prev >= w
			if oks[i] != want {
				t.Fatalf("w=%d step %d: got %v want %v", w, i, oks[i], want)
			}
			if want {
				naive[ids[i]] = tss[i]
			}
		}
	}
}

// TestEviction 不变量 3：八步序列，保留数 <= maxOpen，淘汰 last 最小者。
func TestEviction(t *testing.T) {
	d, _ := api.New(5, 3)
	seq := []struct {
		id   string
		ts   int64
		want bool
	}{{"X", 10, true}, {"X", 15, true}, {"Y", 20, true}, {"Z", 30, true},
		{"Y", 24, false}, {"W", 40, true}, {"Y", 25, true}, {"Z", 32, false}}
	for i, e := range seq {
		if got, _ := d.Dedup(e.id, e.ts); got != e.want {
			t.Fatalf("step %d (%s,%d): got %v want %v", i, e.id, e.ts, got, e.want)
		}
	}
	if v := d.View(); len(v) != 3 || v["Y"] != 25 || v["Z"] != 30 || v["W"] != 40 {
		t.Fatalf("view=%v, want X evicted and {Y:25,Z:30,W:40}", v)
	}
}

// TestEvictedIsNew 不变量 3：被淘汰的 ID 再出现一律按新事件。
func TestEvictedIsNew(t *testing.T) {
	d, _ := api.New(5, 2)
	d.Dedup("A", 10)
	d.Dedup("B", 20)
	d.Dedup("C", 30) // 淘汰 A(10)
	if ok, _ := d.Dedup("A", 11); !ok {
		t.Fatal("evicted ID must be treated as new")
	}
}

// TestRejectNoSideEffect 不变量 4：三类错误可判定且互不相同，被拒后状态不变。
func TestRejectNoSideEffect(t *testing.T) {
	d, _ := api.New(5, 3)
	d.Dedup("A", 10)
	d.Dedup("A", 12) // 重复一条，让两个计数器都非零
	acc, dup, view := d.Accepted(), d.Duplicated(), d.View()
	_, e1 := d.Dedup("", 99)
	_, e2 := api.New(0, 3)
	_, e3 := api.New(5, 0)
	if !errors.Is(e1, api.ErrEmptyID) || !errors.Is(e2, api.ErrNonPositiveWindow) ||
		!errors.Is(e3, api.ErrNonPositiveMaxOpen) || e1 == e2 || e2 == e3 {
		t.Fatalf("bad sentinel errors: %v %v %v", e1, e2, e3)
	}
	if d.Accepted() != acc || d.Duplicated() != dup || d.View()["A"] != view["A"] {
		t.Fatal("rejected op left trace")
	}
	if ok, _ := d.Dedup("A", 15); !ok {
		t.Fatal("deduper unusable after rejection")
	}
}

// TestConcurrent 并发：不同 ID 全接受；同一 ID 相同 TS 恰好一个接受。
func TestConcurrent(t *testing.T) {
	const n = 64
	d, _ := api.New(5, n+1)
	var wg sync.WaitGroup
	start, wins := make(chan struct{}), make(chan bool, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			d.Dedup(fmt.Sprintf("c%d", i), 100)
			ok, _ := d.Dedup("same", 100)
			wins <- ok
		}(i)
	}
	close(start)
	wg.Wait()
	one := 0
	for i := 0; i < n; i++ {
		if <-wins {
			one++
		}
	}
	if d.Accepted() != n+1 || one != 1 {
		t.Fatalf("accepted=%d sameWins=%d, want %d and 1", d.Accepted(), one, n+1)
	}
}

// TestSelfCheck 自检方法必须全部通过。
func TestSelfCheck(t *testing.T) {
	if d, err := api.New(5, 3); err != nil || d.SelfCheck() != nil {
		t.Fatal("SelfCheck failed:", err, d.SelfCheck())
	}
}
