package api_test

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"reflect"
	"sync"
	"testing"

	"ontology/api"
	"ontology/khash"
)

func evs(ks []string) []api.Event {
	e := make([]api.Event, len(ks))
	for i := range ks {
		e[i] = api.Event{Key: ks[i], V: int64(i)}
	}
	return e
}
func mustFeed(t *testing.T, a *api.API, ev []api.Event, want error, msg string) {
	t.Helper()
	if _, err := a.Feed(ev); !errors.Is(err, want) {
		t.Fatalf("%s: got %v, want %v", msg, err, want)
	}
}
func TestFeedMatchesNaive(t *testing.T) {
	ks := []string{"gnj", "dzv", "gnk", "kcm", "dzv", "qjy", "a6m", "dzv"}
	for _, rate := range []int{0, 1, 2000, 2500, 5000, 10000} {
		a, _ := api.New(rate, 1000)
		in := evs(ks)
		out, err := a.Feed(in)
		if err != nil {
			t.Fatal(err)
		}
		var want []api.Event
		for _, e := range in {
			if khash.Sampled(e.Key, rate) {
				want = append(want, e)
			}
		}
		if len(out) != len(want) {
			t.Fatalf("rate %d: got %d want %d", rate, len(out), len(want))
		}
		for i := range want {
			if out[i] != want[i] {
				t.Fatalf("rate %d event %d mismatch", rate, i)
			}
		}
	}
}
func TestCrossInstanceConsistency(t *testing.T) {
	for tr := 0; tr < 4; tr++ {
		rng := rand.New(rand.NewPCG(uint64(tr), 9))
		rate, n := rng.IntN(10001), 150
		ks := make([]string, n)
		for i := range ks {
			ks[i] = fmt.Sprintf("c-%d-%05d", tr, i)
		}
		a, _ := api.New(rate, 1000)
		b, _ := api.New(rate, 1000)
		a.Feed(evs(ks))
		p := append([]string(nil), ks...)
		rng.Shuffle(n, func(i, j int) { p[i], p[j] = p[j], p[i] })
		b.Feed(append(evs(p), evs([]string{"prior-z", "prior-a"})...)) // 乱序且带无关历史
		for _, k := range ks {
			if a.Sampled(k) != b.Sampled(k) || a.Sampled(k) != khash.Sampled(k, rate) {
				t.Fatalf("trial %d rate %d key %q differs", tr, rate, k)
			}
		}
	}
}

// TestRejectedOpsNoTrace 钉不变量 4 + 第五节：三类哨兵互异可判定，被拒操作零写入（容量探针）。
func TestRejectedOpsNoTrace(t *testing.T) {
	for _, c := range []struct{ rate, max int }{{-1, 10}, {10001, 10}, {10, 0}, {10, -3}} {
		if _, err := api.New(c.rate, c.max); !errors.Is(err, api.ErrRateOutOfRange) {
			t.Fatalf("New(%d,%d)=%v", c.rate, c.max, err)
		}
	}
	if api.ErrRateOutOfRange == api.ErrEmptyKey || api.ErrEmptyKey == api.ErrTooManyKeys ||
		api.ErrRateOutOfRange == api.ErrTooManyKeys {
		t.Fatal("the three sentinel errors must be distinct")
	}
	a, _ := api.New(2500, 4) // 空键整批回滚，随后用容量探针证明零写入、且被拒后仍可用
	mustFeed(t, a, evs([]string{"ok1"}), nil, "seed")
	mustFeed(t, a, []api.Event{{Key: "ok2"}, {Key: ""}}, api.ErrEmptyKey, "empty-key batch")
	mustFeed(t, a, evs([]string{"n1", "n2", "n3"}), nil, "rejected batch consumed capacity")
	mustFeed(t, a, evs([]string{"n4"}), api.ErrTooManyKeys, "expected full after 3 fresh")
	mustFeed(t, a, evs([]string{"ok1"}), nil, "known key after rejection")
	b, _ := api.New(2500, 3) // 批内重复新键只计一次；超限整批回滚、不留痕
	mustFeed(t, b, evs([]string{"p"}), nil, "seed")
	mustFeed(t, b, evs([]string{"q", "q", "r"}), nil, "within-batch dup counts once")
	mustFeed(t, b, evs([]string{"s"}), api.ErrTooManyKeys, "should be full")
	mustFeed(t, b, evs([]string{"s", "t"}), api.ErrTooManyKeys, "multi-new overflow rejected")
	mustFeed(t, b, evs([]string{"q"}), nil, "known key works after rejection")
	c, _ := api.New(2500, 10) // 越界 SetRate 整体失败、采样率不变
	c.Feed(evs([]string{"qjy", "gnk"}))
	for _, bad := range []int{-1, 10001} {
		if _, _, e := c.SetRate(bad); !errors.Is(e, api.ErrRateOutOfRange) ||
			!c.Sampled("qjy") || c.Sampled("gnk") {
			t.Fatalf("SetRate(%d) changed state after rejection", bad)
		}
	}
}

// TestConcurrentFeeds 钉第六节：N goroutine 并发喂同批事件的不同随机排列，键集相同且等于朴素参照。
func TestConcurrentFeeds(t *testing.T) {
	for _, rate := range []int{0, 2500, 3333, 10000} {
		const N, nKey = 12, 200
		ks := make([]string, nKey)
		shared, _ := api.New(rate, nKey)
		want := map[string]struct{}{}
		for i := range ks {
			k := fmt.Sprintf("p-%05d", i)
			ks[i] = k
			if khash.Sampled(k, rate) {
				want[k] = struct{}{}
			}
		}
		res := make([]map[string]struct{}, N)
		var wg sync.WaitGroup
		for g := 0; g < N; g++ {
			wg.Add(1)
			go func(g int) {
				defer wg.Done()
				p := append([]string(nil), ks...)
				rand.New(rand.NewPCG(uint64(g), 7)).Shuffle(nKey,
					func(i, j int) { p[i], p[j] = p[j], p[i] })
				out, err := shared.Feed(evs(p))
				if err != nil {
					t.Errorf("g %d feed: %v", g, err)
					return
				}
				m := make(map[string]struct{}, len(out))
				for _, e := range out {
					m[e.Key] = struct{}{}
				}
				res[g] = m
			}(g)
		}
		wg.Wait()
		for g := 0; g < N; g++ {
			if !reflect.DeepEqual(res[g], want) {
				t.Fatalf("rate %d g %d set differs from naive", rate, g)
			}
		}
	}
}
