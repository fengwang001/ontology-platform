package api

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
)

// TestNaiveReference 钉住不变量 2、3：随机序列下流式与朴素重放逐事件一致。
// 每行 {w, maxOpen, n, seed}。
func TestNaiveReference(t *testing.T) {
	for _, cfg := range [][4]int64{{5, 3, 500, 1}, {1, 1, 300, 2}, {17, 9, 800, 3}, {100, 50, 1000, 4}} {
		w, maxOpen, n := cfg[0], int(cfg[1]), int(cfg[2])
		rng := rand.New(rand.NewSource(cfg[3]))
		var evs []event
		for i := 0; i < n; i++ {
			evs = append(evs, event{fmt.Sprintf("id%d", rng.Intn(2*maxOpen+2)), rng.Int63n(400) - 200 + int64(i)})
		}
		d, err := New(w, maxOpen)
		if err != nil {
			t.Fatal(err)
		}
		want := naive(w, maxOpen, evs)
		lastAccept := map[string]int64{}
		for i, e := range evs {
			_, present := d.View()[e.id]
			got, err := d.Dedup(e.id, e.ts)
			if err != nil || got != want[i] {
				t.Fatalf("cfg=%v event %d (%s,%d): got=%v,%v want=%v", cfg, i, e.id, e.ts, got, err, want[i])
			}
			if got {
				if present && e.ts-lastAccept[e.id] < w {
					t.Fatalf("cfg=%v: overlapping window for %s at event %d", cfg, e.id, i)
				}
				lastAccept[e.id] = e.ts
			}
			if len(d.View()) > maxOpen {
				t.Fatalf("cfg=%v: kept %d > maxOpen %d", cfg, len(d.View()), maxOpen)
			}
		}
	}
}

// TestEviction 钉住不变量 3：淘汰 last 最小者；被淘汰者按新事件；重复不刷新 last。
// 即 NOTES.md 的八步序列（W=5, maxOpen=3）。
func TestEviction(t *testing.T) {
	d, err := New(5, 3)
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{"X", "X", "Y", "Z", "Y", "W", "Y", "Z"}
	ts := []int64{10, 15, 20, 30, 24, 40, 25, 32}
	acc := []bool{true, true, true, true, false, true, true, false}
	last := []int64{10, 15, 20, 30, 20, 40, 25, 30}
	for i := range ids {
		got, err := d.Dedup(ids[i], ts[i])
		if err != nil || got != acc[i] || d.View()[ids[i]] != last[i] {
			t.Fatalf("step %d (%s,%d): got=%v,%v last=%v, want accepted=%v last=%d",
				i+1, ids[i], ts[i], got, err, d.View()[ids[i]], acc[i], last[i])
		}
	}
	if _, ok := d.View()["X"]; ok {
		t.Fatal("X (smallest last) should have been evicted at step 6")
	}
	if ok, _ := d.Dedup("X", 41); !ok { // 被淘汰的 X 再出现按新事件
		t.Fatal("evicted X should be treated as new")
	}
	if d.Accepted() != 7 || d.Duplicated() != 2 {
		t.Fatalf("counters: accepted=%d duplicated=%d", d.Accepted(), d.Duplicated())
	}
}

// TestFailureNoTrace 钉住不变量 4：三类错误可判定且互不相同，被拒后状态不变。
func TestFailureNoTrace(t *testing.T) {
	d, err := New(5, 3)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Dedup("a", 1); err != nil {
		t.Fatal(err)
	}
	view, acc, dup := d.View(), d.Accepted(), d.Duplicated()
	if _, err := d.Dedup("", 9); !errors.Is(err, ErrEmptyID) {
		t.Fatalf("empty id: %v", err)
	}
	ws := []int64{0, -7, 1, 1}
	mos := []int{1, 1, 0, -3}
	errs := []error{ErrNonPositiveWindow, ErrNonPositiveWindow, ErrNonPositiveMaxOpen, ErrNonPositiveMaxOpen}
	for i := range ws {
		if _, err := New(ws[i], mos[i]); !errors.Is(err, errs[i]) {
			t.Fatalf("New(%d,%d): %v, want %v", ws[i], mos[i], err, errs[i])
		}
	}
	if errors.Is(ErrEmptyID, ErrNonPositiveWindow) || errors.Is(ErrEmptyID, ErrNonPositiveMaxOpen) ||
		errors.Is(ErrNonPositiveWindow, ErrNonPositiveMaxOpen) {
		t.Fatal("sentinel errors not distinct")
	}
	if d.Accepted() != acc || d.Duplicated() != dup || len(d.View()) != len(view) || d.View()["a"] != view["a"] {
		t.Fatal("state changed after rejected ops")
	}
	if ok, _ := d.Dedup("b", 2); !ok { // 拒绝后仍可正常使用
		t.Fatal("deduper unusable after rejected ops")
	}
}

// TestConcurrentDedup 并发：不同 ID 全接受；同 ID 同 TS 恰好一个接受。
func TestConcurrentDedup(t *testing.T) {
	const n = 64
	d, err := New(5, n)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var accepted, same int64
	run := func(mk func(i int) (string, int64), counter *int64) {
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				id, ts := mk(i)
				if ok, _ := d.Dedup(id, ts); ok {
					atomic.AddInt64(counter, 1)
				}
			}(i)
		}
		wg.Wait()
	}
	run(func(i int) (string, int64) {
		_, _, _, _ = d.View(), d.Duplicated(), d.Accepted(), d.SelfCheck()
		return fmt.Sprintf("g%d", i), 100
	}, &accepted)
	if accepted != n {
		t.Fatalf("distinct ids: accepted=%d, want %d", accepted, n)
	}
	run(func(i int) (string, int64) { return "same", 7 }, &same)
	if same != 1 {
		t.Fatalf("same id+ts: accepted=%d, want exactly 1", same)
	}
}
