package api_test

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"

	"ontology/api"
)

// naive 是独立的朴素参考实现：按规则逐条处理，用于对比。
type naive struct {
	k    int64
	high map[string]int64
	acc  map[string]int64
	drop int64
}

func (n *naive) feed(key string, seq int64) {
	h, ok := n.high[key]
	if !ok {
		n.high[key], n.acc[key] = seq, 1
		return
	}
	if seq < h-n.k {
		n.drop++
		return
	}
	n.acc[key]++
	if seq > h {
		n.high[key] = seq
	}
}

// TestNaiveEquivalence 不变量 1：多档 Key 规模 × 随机乱序到达，逐 Key 对比朴素结果。
func TestNaiveEquivalence(t *testing.T) {
	for _, m := range []int{1, 7, 100, 1000} {
		t.Run(fmt.Sprintf("keys=%d", m), func(t *testing.T) {
			rng := rand.New(rand.NewSource(int64(m)))
			w, _ := api.New(3, m)
			nf := &naive{k: 3, high: map[string]int64{}, acc: map[string]int64{}}
			base := make([]int64, m)
			var evs []api.Event
			for i := 0; i < 2000; i++ {
				k := rng.Intn(m)
				base[k] += int64(rng.Intn(3))       // 源头单调推进
				seq := base[k] - int64(rng.Intn(6)) // 投递乱序，可能超窗
				key := fmt.Sprintf("k%d", k)
				evs = append(evs, api.Event{Key: key, Seq: seq})
				nf.feed(key, seq)
			}
			if err := w.Feed(evs); err != nil {
				t.Fatal(err)
			}
			for k := 0; k < m; k++ {
				key := fmt.Sprintf("k%d", k)
				if h, _ := w.High(key); w.Accepted(key) != nf.acc[key] || h != nf.high[key] {
					t.Fatalf("key %s: got acc=%d high=%d, want acc=%d high=%d",
						key, w.Accepted(key), h, nf.acc[key], nf.high[key])
				}
			}
			if w.Dropped() != nf.drop {
				t.Fatalf("dropped: got %d, want %d", w.Dropped(), nf.drop)
			}
		})
	}
}

// TestWindowBoundary 不变量 2：左闭边界、high 只进不退、丢弃不改 high。
func TestWindowBoundary(t *testing.T) {
	cases := []struct {
		name              string
		seqs              []int64
		high, acc, drop   int64
	}{
		{"boundary accepted", []int64{10, 7}, 10, 2, 0},   // 7 == 10-3 左闭接受
		{"below boundary", []int64{10, 6}, 10, 1, 1},      // 6 == 10-3-1 丢弃
		{"drop keeps high", []int64{10, 1, 9}, 10, 2, 1},  // 丢弃后 high 不变
		{"no retreat", []int64{10, 8, 9, 7}, 10, 4, 0},    // 乱序接受不推进
		{"advance", []int64{10, 13}, 13, 2, 0},            // 只有更大才推进
		{"first negative", []int64{-5, -8, -9}, -5, 2, 1}, // 负 Seq 同样成立
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, _ := api.New(3, 1)
			prev, has := int64(0), false
			for _, s := range tc.seqs {
				if err := w.Feed([]api.Event{{Key: "a", Seq: s}}); err != nil {
					t.Fatal(err)
				}
				h, _ := w.High("a")
				if has && h < prev {
					t.Fatalf("high retreated: %d -> %d", prev, h)
				}
				prev, has = h, true
			}
			if prev != tc.high || w.Accepted("a") != tc.acc || w.Dropped() != tc.drop {
				t.Fatalf("got high=%d acc=%d drop=%d, want %d/%d/%d",
					prev, w.Accepted("a"), w.Dropped(), tc.high, tc.acc, tc.drop)
			}
		})
	}
}

// TestMonotonic 不变量 3：Accepted 与 Dropped 只增不减（含丢弃事件）。
func TestMonotonic(t *testing.T) {
	w, _ := api.New(2, 2)
	prevA, prevD := int64(0), int64(0)
	for _, s := range []int64{10, 1, 12, 2, 9, 0, 11, 3} {
		if err := w.Feed([]api.Event{{Key: "a", Seq: s}}); err != nil {
			t.Fatal(err)
		}
		if a, d := w.Accepted("a"), w.Dropped(); a < prevA || d < prevD {
			t.Fatalf("counters retreated: acc %d->%d drop %d->%d", prevA, a, prevD, d)
		} else {
			prevA, prevD = a, d
		}
	}
}

// TestFeedAtomic 不变量 4：任一条被拒则整批不生效，错误可判定，之后可用。
func TestFeedAtomic(t *testing.T) {
	w, _ := api.New(3, 2)
	if err := w.Feed([]api.Event{{Key: "a", Seq: 10}}); err != nil {
		t.Fatal(err)
	}
	bad := [][]api.Event{
		{{Key: "b", Seq: 1}, {Key: "", Seq: 2}},                      // 空 Key 在批尾
		{{Key: "b", Seq: 1}, {Key: "c", Seq: 2}},                     // 新 Key 超限
		{{Key: "a", Seq: 11}, {Key: "b", Seq: 1}, {Key: "", Seq: 0}}, // 已有 Key 的推进也须回滚
	}
	wantErrs := []error{api.ErrEmptyKey, api.ErrTooManyKeys, api.ErrEmptyKey}
	for i, batch := range bad {
		if err := w.Feed(batch); !errors.Is(err, wantErrs[i]) {
			t.Fatalf("batch %d: got %v, want %v", i, err, wantErrs[i])
		}
		if w.Accepted("a") != 1 || w.Accepted("b") != 0 || w.Dropped() != 0 {
			t.Fatalf("batch %d left traces", i)
		}
		if h, _ := w.High("a"); h != 10 {
			t.Fatalf("batch %d moved high to %d", i, h)
		}
	}
	if err := w.Feed([]api.Event{{Key: "b", Seq: 1}}); err != nil || w.Accepted("b") != 1 {
		t.Fatal("unusable after rejected batches")
	}
}

// TestBadParams 参数非法可判定，且三类错误互不相同。
func TestBadParams(t *testing.T) {
	for _, p := range [][2]int64{{-1, 1}, {0, 0}, {3, -2}} {
		if _, err := api.New(p[0], int(p[1])); !errors.Is(err, api.ErrBadParam) {
			t.Fatalf("New(%d,%d): got %v", p[0], p[1], err)
		}
	}
	if api.ErrBadParam == api.ErrEmptyKey || api.ErrEmptyKey == api.ErrTooManyKeys || api.ErrBadParam == api.ErrTooManyKeys {
		t.Fatal("sentinel errors must be distinct")
	}
}

// TestConcurrent N 个 goroutine 喂互不相同的 Key，结果与串行等价；
// N 个 goroutine 并发读同一实例，结果逐字段相同。无 sleep。
func TestConcurrent(t *testing.T) {
	const n = 64
	w, _ := api.New(3, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			k := fmt.Sprintf("g%d", i)
			_ = w.Feed([]api.Event{{Key: k, Seq: 10}, {Key: k, Seq: 9}, {Key: k, Seq: 12}, {Key: k, Seq: 5}})
		}(i)
	}
	wg.Wait()
	for i := 0; i < n; i++ {
		if h, _ := w.High(fmt.Sprintf("g%d", i)); h != 12 || w.Accepted(fmt.Sprintf("g%d", i)) != 3 {
			t.Fatalf("key g%d: got high=%d acc=%d, want 12/3", i, h, w.Accepted(fmt.Sprintf("g%d", i)))
		}
	}
	if w.Dropped() != n {
		t.Fatalf("dropped: got %d, want %d", w.Dropped(), n)
	}
	errs := make(chan string, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h, _ := w.High("g0")
			if h != 12 || w.Accepted("g0") != 3 || w.Dropped() != n || w.SelfCheck() != nil {
				errs <- "mismatch"
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
}
