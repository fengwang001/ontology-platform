package api_test

import (
	"errors"
	"fmt"
	"maps"
	"math/rand"
	"sync"
	"testing"

	"ontology/api"
)

func batchMin(ws []api.Write) map[string]string {
	best, out := map[string]int64{}, map[string]string{}
	for _, w := range ws {
		if s, ok := best[w.Key]; !ok || w.Seq < s {
			best[w.Key], out[w.Key] = w.Seq, w.Val
		}
	}
	return out
}

func cases() map[string][]api.Write {
	out := map[string][]api.Write{
		"six": {
			{Key: "K", Seq: 7, Val: "a"}, {Key: "K", Seq: 3, Val: "b"}, {Key: "K", Seq: 10, Val: "c"},
			{Key: "K", Seq: 1, Val: "d"}, {Key: "K", Seq: 5, Val: "e"}, {Key: "K", Seq: 8, Val: "f"},
		},
		"multi": {
			{Key: "a", Seq: 2, Val: "v2"}, {Key: "b", Seq: 5, Val: "w5"}, {Key: "a", Seq: 1, Val: "v1"}, {Key: "b", Seq: 3, Val: "w3"}, {Key: "a", Seq: 4, Val: "v4"}, {Key: "b", Seq: 1, Val: "w1"},
		},
	}
	rng := rand.New(rand.NewSource(3))
	base := []api.Write{
		{Key: "a", Seq: 1, Val: "v1"}, {Key: "a", Seq: 4, Val: "v4"}, {Key: "a", Seq: 2, Val: "v2"}, {Key: "b", Seq: 3, Val: "w3"}, {Key: "b", Seq: 1, Val: "w1"}, {Key: "a", Seq: 6, Val: "v6"},
	}
	for p := 0; p < 6; p++ {
		sh := make([]api.Write, len(base))
		for i, j := range rng.Perm(len(base)) {
			sh[i] = base[j]
		}
		out[fmt.Sprintf("rand%d", p)] = sh
	}
	return out
}

// 不变量 1：逐条喂与整批喂的 View 都等于「每 Key 最小 Seq」的批量重算。
func TestViewMatchesBatch(t *testing.T) {
	for name, ws := range cases() {
		t.Run(name, func(t *testing.T) {
			one, all := api.New(), api.New()
			for _, w := range ws {
				if _, err := one.Feed([]api.Write{w}); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := all.Feed(ws); err != nil {
				t.Fatal(err)
			}
			if want := batchMin(ws); !maps.Equal(one.View(), want) || !maps.Equal(all.View(), want) {
				t.Fatalf("view %v / %v, want %v", one.View(), all.View(), want)
			}
		})
	}
}

// 不变量 4：任一条被拒整批不留痕，之后仍可正常使用。
func TestFailureAtomic(t *testing.T) {
	bads := []struct {
		name string
		ws   []api.Write
		want error
	}{
		{"empty key", []api.Write{{Key: "", Seq: 1, Val: "x"}}, api.ErrEmptyKey},
		{"seq zero", []api.Write{{Key: "K", Seq: 0, Val: "x"}}, api.ErrBadSeq},
		{"seq negative", []api.Write{{Key: "K", Seq: -3, Val: "x"}}, api.ErrBadSeq},
		{"dup seq", []api.Write{{Key: "K", Seq: 5, Val: "x"}}, api.ErrDupSeq},
		{"bad tail", []api.Write{{Key: "ok", Seq: 1, Val: "y"}, {Key: "K", Seq: 5, Val: "x"}}, api.ErrDupSeq},
	}
	for _, tc := range bads {
		t.Run(tc.name, func(t *testing.T) {
			r := api.New()
			if _, err := r.Feed([]api.Write{{Key: "K", Seq: 5, Val: "a"}}); err != nil {
				t.Fatal(err)
			}
			before, beforeDrop := r.View(), r.Dropped()
			if _, err := r.Feed(tc.ws); !errors.Is(err, tc.want) {
				t.Fatalf("err=%v, want %v", err, tc.want)
			}
			if !maps.Equal(r.View(), before) || r.Dropped() != beforeDrop {
				t.Fatal("rejected batch left trace")
			}
			if _, err := r.Feed([]api.Write{{Key: "K", Seq: 1, Val: "z"}}); err != nil {
				t.Fatalf("unusable after rejection: %v", err)
			}
		})
	}
}

// 三类故障的哨兵错误互不相同。
func TestErrorsDistinct(t *testing.T) {
	r := api.New()
	_, _ = r.Feed([]api.Write{{Key: "K", Seq: 5, Val: "a"}})
	_, e1 := r.Feed([]api.Write{{Key: "", Seq: 1, Val: "x"}})
	_, e2 := r.Feed([]api.Write{{Key: "K", Seq: 0, Val: "x"}})
	_, e3 := r.Feed([]api.Write{{Key: "K", Seq: 5, Val: "x"}})
	for i, e := range []error{e1, e2, e3} {
		if e == nil {
			t.Fatalf("err %d is nil", i)
		}
	}
	if e1 == e2 || e2 == e3 || e1 == e3 {
		t.Fatal("sentinel errors not distinct")
	}
}

// 并发只读：N 个 goroutine 的视图逐字段相同（无 sleep，用屏障对齐起跑）。
func TestConcurrentReaders(t *testing.T) {
	r := api.New()
	if _, err := r.Feed(cases()["six"]); err != nil {
		t.Fatal(err)
	}
	base := r.View()
	start := make(chan struct{})
	errs := make(chan string, 32)
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for j := 0; j < 50; j++ {
				if !maps.Equal(r.View(), base) {
					errs <- "view mismatch"
				}
				if r.Dropped() != 3 || r.SelfCheck() != nil {
					errs <- "dropped/selfcheck mismatch"
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	select {
	case msg := <-errs:
		t.Fatal(msg)
	default:
	}
}
