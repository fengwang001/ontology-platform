package api_test

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"

	"ontology/api"
	"ontology/pagg"
)

func rec(p int, k string, v int64) pagg.Record { return pagg.Record{Partition: p, Key: k, Value: v} }

// batch 是单线程批量重算：按 key 分组求和、按 key 升序。
func batch(recs []pagg.Record) []api.Entry {
	sum := map[string]int64{}
	for _, r := range recs {
		sum[r.Key] += r.Value
	}
	out := make([]api.Entry, 0, len(sum))
	for _, k := range slices.Sorted(maps.Keys(sum)) {
		out = append(out, api.Entry{Key: k, Total: sum[k]})
	}
	return out
}

func mustAdd(t *testing.T, a *api.Aggregator, recs []pagg.Record) {
	for _, r := range recs {
		if err := a.Add(r); err != nil {
			t.Fatalf("Add(%+v): %v", r, err)
		}
	}
}

// 不变量 1：Merge 结果与批量重算逐 key 相同（与分区归属无关）。
func TestMergeEqualsBatchRecompute(t *testing.T) {
	cases := map[string][]pagg.Record{
		"spec-six": {rec(0, "a", 5), rec(1, "a", 1), rec(2, "b", 6), rec(0, "a", 3), rec(1, "c", 4), rec(2, "a", 2)},
	}
	for _, n := range []int{100, 1000, 5000} { // 多档规模、随机分区归属
		recs := make([]pagg.Record, n)
		for i := range recs {
			recs[i] = rec((i*31+7)%16, fmt.Sprintf("k%d", (i*13)%97), int64(i%11-5))
		}
		cases[fmt.Sprintf("rand-%d", n)] = recs
	}
	for name, recs := range cases {
		a := api.New(16)
		mustAdd(t, a, recs)
		if got, want := fmt.Sprint(a.Merge()), fmt.Sprint(batch(recs)); got != want {
			t.Errorf("%s: got %s, want %s", name, got, want)
		}
	}
}

// 不变量 3：每记录恰计一次——同 key 跨分区求和、每 key 恰好出现一次、总值守恒。
func TestEachRecordCountedOnce(t *testing.T) {
	cases := [][]pagg.Record{
		{rec(0, "a", 5), rec(1, "a", 1), rec(2, "a", 2), rec(0, "a", 3)},
	}
	for _, recs := range cases {
		a := api.New(3)
		mustAdd(t, a, recs)
		var totalIn, totalOut int64
		for _, r := range recs {
			totalIn += r.Value
		}
		got := a.Merge()
		for i, e := range got {
			if i > 0 && got[i-1].Key == e.Key {
				t.Fatalf("key %q appears twice", e.Key)
			}
			totalOut += e.Total
		}
		if totalIn != totalOut {
			t.Fatalf("total in=%d out=%d", totalIn, totalOut)
		}
	}
}

// 不变量 4：三类拒绝互不相同、不留痕，之后仍可正常使用。
func TestRejectedAddLeavesNoTrace(t *testing.T) {
	a := api.New(3)
	mustAdd(t, a, []pagg.Record{rec(0, "a", 5)})
	before := fmt.Sprint(a.Merge())
	cases := map[pagg.Record]error{
		rec(-1, "x", 1): api.ErrPartitionNegative,
		rec(3, "x", 1):  api.ErrPartitionTooLarge,
		rec(0, "", 1):   api.ErrEmptyKey,
	}
	for bad, want := range cases {
		if got := a.Add(bad); got != want {
			t.Fatalf("Add(%+v) = %v, want %v", bad, got, want)
		}
		if after := fmt.Sprint(a.Merge()); after != before {
			t.Fatalf("rejected Add(%+v) changed state", bad)
		}
	}
	mustAdd(t, a, []pagg.Record{rec(1, "b", 1)}) // 拒绝后仍可用
	if got := fmt.Sprint(a.Merge()); got != "[{a 5} {b 1}]" {
		t.Fatalf("after rejects, got %s", got)
	}
}

// 并发：N 个 goroutine 各向不同分区并发 Add，期间并发 Merge 读，结果须等于批量重算。
func TestConcurrentAddMerge(t *testing.T) {
	for _, G := range []int{2, 8, 64} {
		a := api.New(G)
		var recs []pagg.Record
		var wg, readers sync.WaitGroup
		readers.Add(1)
		go func() { // 并发读者：任一时刻读到的结果不得撕裂
			defer readers.Done()
			for k := 0; k < 500; k++ {
				got := a.Merge()
				if !slices.IsSortedFunc(got, func(x, y api.Entry) int { return strings.Compare(x.Key, y.Key) }) {
					t.Errorf("torn read: %v", got)
					return
				}
			}
		}()
		for g := 0; g < G; g++ {
			wg.Add(1)
			go func(g int) {
				defer wg.Done()
				for j := 0; j < 200; j++ {
					_ = a.Add(rec(g, fmt.Sprintf("k%d", j%9), int64(j%5-2)))
				}
			}(g)
			for j := 0; j < 200; j++ {
				recs = append(recs, rec(g, fmt.Sprintf("k%d", j%9), int64(j%5-2)))
			}
		}
		wg.Wait()
		readers.Wait()
		if got, want := fmt.Sprint(a.Merge()), fmt.Sprint(batch(recs)); got != want {
			t.Fatalf("G=%d: got %s, want %s", G, got, want)
		}
	}
}

func TestSelfCheck(t *testing.T) {
	if err := api.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
