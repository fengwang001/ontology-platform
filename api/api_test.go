package api_test

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"

	"ontology/api"
)

// TestEightStepSequence 钉住第三节八步序列：逐步 gap、最终主/侧条数与 view，并跑 SelfCheck。
func TestEightStepSequence(t *testing.T) {
	cases := []struct {
		k       string
		ts, gap int64
	}{{"A", 10, 0}, {"B", 20, 0}, {"A", 15, 0}, {"A", 15, 0}, {"B", 20, 0}, {"B", 25, 0}, {"A", 12, 3}, {"A", 10, 5}}
	d := api.New(8)
	for i, c := range cases {
		if err := d.Feed(c.k, c.ts); err != nil {
			t.Fatalf("step %d: %v", i+1, err)
		}
		if c.gap > 0 && d.Side()[len(d.Side())-1].Gap != c.gap {
			t.Fatalf("step %d gap want %d", i+1, c.gap)
		}
	}
	v := d.View()
	if m, s := len(d.Main()), len(d.Side()); m != 6 || s != 2 || v["A"] != 15 || v["B"] != 25 {
		t.Fatalf("main=%d side=%d view=%v", m, s, v)
	}
	if err := api.New(0).SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

// TestBatchConsistency 多档随机乱序：view==含迟到批量最大，主+侧==总数且主路为非递减前缀。
func TestBatchConsistency(t *testing.T) {
	for _, n := range []int{1, 5, 50, 500} {
		t.Run(fmt.Sprintf("n=%d", n), func(t *testing.T) {
			rng := rand.New(rand.NewSource(int64(n * 7919)))
			d := api.New(n * n)
			batch, cur := map[string]int64{}, map[string]int64{}
			wantMain := 0
			for i := 0; i < n*n; i++ {
				key := fmt.Sprintf("k%d", rng.Intn(n))
				ts := rng.Int63n(40) + 1 // 1..40，首见即 > 零值水位
				d.Feed(key, ts)
				batch[key] = max(batch[key], ts)
				if ts >= cur[key] { // 主路：相等也算
					wantMain++
					cur[key] = ts
				}
			}
			view := d.View()
			for k, want := range batch {
				if view[k] != want {
					t.Fatalf("view[%s]=%d want batch max %d", k, view[k], want)
				}
			}
			if len(d.Main()) != wantMain || len(d.Main())+len(d.Side()) != n*n {
				t.Fatal("main/total counts wrong")
			}
			for _, s := range d.Side() {
				if s.Gap <= 0 || s.TS >= view[s.Key] {
					t.Fatalf("bad side %+v", s)
				}
			}
		})
	}
}

// TestRejectionNoTrace 三类拒绝互异、整体失败、不留痕、拒绝后仍可继续使用。
func TestRejectionNoTrace(t *testing.T) {
	if api.ErrEmptyKey == api.ErrNegativeTS || api.ErrNegativeTS == api.ErrSideFull || api.ErrEmptyKey == api.ErrSideFull {
		t.Fatal("sentinel errors must be distinct")
	}
	d := api.New(4)
	d.Feed("k", 5)
	for i, c := range []struct {
		key  string
		ts   int64
		want error
	}{{"", 1, api.ErrEmptyKey}, {"k", -1, api.ErrNegativeTS}} {
		v, m, s := len(d.View()), len(d.Main()), len(d.Side())
		if err := d.Feed(c.key, c.ts); !errors.Is(err, c.want) ||
			len(d.View()) != v || len(d.Main()) != m || len(d.Side()) != s {
			t.Fatalf("case %d err=%v left trace", i, err)
		}
	}
	d2 := api.New(1)
	d2.Feed("X", 5)
	d2.Feed("X", 3)
	v, m, s := len(d2.View()), len(d2.Main()), len(d2.Side())
	if err := d2.Feed("X", 1); !errors.Is(err, api.ErrSideFull) ||
		len(d2.View()) != v || len(d2.Main()) != m || len(d2.Side()) != s {
		t.Fatalf("overflow err=%v left trace", err)
	}
	if err := d2.Feed("Z", 9); err != nil || d2.View()["Z"] != 9 {
		t.Fatalf("unusable after rejection: %v", err)
	}
}

// TestConcurrentDifferentKeys：N goroutine 各打不同 Key（ts=j%K，无 sleep）并发读者；最大 K-1、主路 M/K+K-1。
func TestConcurrentDifferentKeys(t *testing.T) {
	const N, M, K = 24, 700, 7
	d := api.New(N * M)
	var wg sync.WaitGroup
	for g := 0; g < N; g++ {
		wg.Add(1)
		key := fmt.Sprintf("k%02d", g)
		go func() {
			defer wg.Done()
			for j := 0; j < M; j++ {
				d.Feed(key, int64(j%K))
			}
		}()
	}
	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
				d.View()
				d.Main()
				d.Side()
				d.SelfCheck()
			}
		}
	}()
	wg.Wait()
	close(stop)
	mainByKey := map[string]int{}
	for _, e := range d.Main() {
		mainByKey[e.Key]++
	}
	wantMain := M/K + K - 1
	for g := 0; g < N; g++ {
		key := fmt.Sprintf("k%02d", g)
		if d.View()[key] != K-1 || mainByKey[key] != wantMain {
			t.Fatalf("%s view=%d main=%d", key, d.View()[key], mainByKey[key])
		}
	}
	if len(d.Main())+len(d.Side()) != N*M {
		t.Fatal("total event count wrong")
	}
}
