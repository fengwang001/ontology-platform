package api_test

import (
	"errors"
	"fmt"
	"math/rand"
	"ontology/api"
	"ontology/order"
	"slices"
	"sync"
	"testing"
)

// reference 是朴素参照：旁路=按到达顺序 TS<=此前最大TS-delay；主输出=其余按 (TS,Seq) 稳定排序。
func reference(evs []api.Out, delay int64) (main, side []api.Out) {
	maxTS := int64(-1<<63) + delay // 尚无被接受事件时阈值=-∞，永不迟到
	for i, e := range evs {
		e.Seq = int64(i)
		if e.TS <= maxTS-delay {
			side = append(side, e)
		} else {
			main = append(main, e)
		}
		maxTS = max(maxTS, e.TS)
	}
	slices.SortStableFunc(main, func(a, b api.Out) int { return int(a.TS - b.TS) })
	return main, side
}

func genEvents(n int, rng *rand.Rand) []api.Out {
	evs := make([]api.Out, n)
	for i := range evs {
		evs[i] = api.Out{ID: fmt.Sprintf("e%04d", i), TS: int64(rng.Intn(n/2 + 2))}
	}
	return evs
}

// checkAll 核验不变量1（参照一致）、2（严格有序）、3（恰好一次）。
func checkAll(t *testing.T, r *api.Reorder, evs []api.Out, delay int64) {
	main, side := r.Main(), r.Side()
	wantMain, wantSide := reference(evs, delay)
	if !slices.Equal(main, wantMain) || !slices.Equal(side, wantSide) {
		t.Fatal("与朴素参照不一致")
	}
	for i := 1; i < len(main); i++ {
		a, b := main[i-1], main[i]
		if !order.Less(order.Key{TS: a.TS, Seq: a.Seq}, order.Key{TS: b.TS, Seq: b.Seq}) {
			t.Fatalf("主输出非严格递增@%d", i)
		}
	}
	seen := map[string]int{}
	for _, o := range slices.Concat(main, side) {
		seen[o.ID]++
	}
	if len(main)+len(side) != len(evs) || len(seen) != len(evs) {
		t.Fatalf("恰好一次违反: %d+%d/%d != %d", len(main), len(side), len(seen), len(evs))
	}
}

func TestReferenceEquivalence(t *testing.T) {
	for _, n := range []int{1, 7, 50, 300} {
		for _, seed := range []int64{1, 2, 3} {
			evs := genEvents(n, rand.New(rand.NewSource(seed)))
			delay := int64(n/8 + 1)
			r, _ := api.New(delay, n+2)
			for _, e := range evs {
				r.Push(e.ID, e.TS) // ID 唯一、缓冲够大，不会失败
			}
			r.Flush()
			checkAll(t, r, evs, delay)
		}
	}
}

// TestMainOutputOrder 钉不变量2 的另一半：每条主输出在输出时 TS<=当时的 wm。
func TestMainOutputOrder(t *testing.T) {
	evs := genEvents(200, rand.New(rand.NewSource(7)))
	const delay = int64(9)
	r, _ := api.New(delay, 500)
	var maxTS int64
	for i, e := range evs {
		main, _, _ := r.Push(e.ID, e.TS)
		maxTS = max(maxTS, e.TS)
		if n := len(main); n > 0 && main[n-1].TS > maxTS-delay { // 批次升序，查末个即可
			t.Fatalf("step %d: 输出 TS=%d 超过当时 wm=%d", i, main[n-1].TS, maxTS-delay)
		}
	}
	r.Flush()
	checkAll(t, r, evs, delay)
}

func TestRejectionNoTrace(t *testing.T) {
	sents := map[error]bool{api.ErrInvalidParam: true, api.ErrEmptyID: true, api.ErrDuplicateID: true, api.ErrBufferFull: true}
	if len(sents) != 4 {
		t.Fatal("哨兵错误不互异")
	}
	r, _ := api.New(10, 2)
	r.Push("a", 100) // wm=90，滞留缓冲
	r.Push("b", 101) // wm=91，滞留缓冲 → 满
	main0, side0 := r.Main(), r.Side()
	_, e1 := api.New(-1, 1)
	_, e2 := api.New(1, 0)
	_, _, e3 := r.Push("", 200)
	_, _, e4 := r.Push("a", 200)
	_, _, e5 := r.Push("c", 102)
	got := []error{e1, e2, e3, e4, e5}
	want := []error{api.ErrInvalidParam, api.ErrInvalidParam, api.ErrEmptyID, api.ErrDuplicateID, api.ErrBufferFull}
	for i := range got {
		if !errors.Is(got[i], want[i]) {
			t.Fatalf("case %d: got %v, want %v", i, got[i], want[i])
		}
	}
	if !slices.Equal(r.Main(), main0) || !slices.Equal(r.Side(), side0) {
		t.Fatal("被拒操作改变了输出状态")
	}
	if _, _, err := r.Push("h", 92); !errors.Is(err, api.ErrBufferFull) {
		t.Fatal("水位线被拒操作改动：92 应非迟到并撞上满缓冲")
	}
	_, side, err := r.Push("d", 50) // 迟到不占缓冲；Seq 应紧接 b 的 1
	if err != nil || len(side) != 1 || side[0].Seq != 2 {
		t.Fatalf("拒绝消耗了到达序号: side=%v err=%v", side, err)
	}
}

func TestConcurrentPush(t *testing.T) {
	const G, K = 8, 40
	r, _ := api.New(7, G*K)
	var wg sync.WaitGroup
	for g := 0; g < G; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for k := 0; k < K; k++ {
				if _, _, err := r.Push(fmt.Sprintf("g%02d-%03d", g, k), int64((g*7+k*13)%(G*K/2))); err != nil {
					t.Errorf("Push: %v", err)
				}
			}
		}(g)
	}
	wg.Wait()
	r.Flush()
	bySeq := make([]api.Out, G*K) // 按 Seq 还原真实到达顺序
	for _, o := range slices.Concat(r.Main(), r.Side()) {
		bySeq[o.Seq] = o
	}
	checkAll(t, r, bySeq, 7)
}
