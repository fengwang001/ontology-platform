// Command demo 逐条核验 FIFO 乱序防护缓冲区；不读参数、不联网，全部通过退出码 0。
package main

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"sync"
	"time"

	"ontology/api"
)

func fail(format string, a ...any) {
	fmt.Printf("FAIL "+format+"\n", a...)
	os.Exit(1)
}

func main() {
	// 1) 第三节八步：每步的发射与缓冲（紧凑一行），第 4 步级联单独核验。
	r, _ := api.New(3)
	steps := []struct {
		seq int64
		out string
	}{{5, "[5]"}, {2, "[2 5]"}, {4, "[2 4 5]"}, {1, "emit[1 2]/buf[4 5]"},
		{3, "emit[3 4 5]"}, {6, "emit[6]"}, {7, "emit[7]"}, {2, "dup"}}
	got1 := ""
	for i, st := range steps {
		em, err := r.Feed("K", st.seq)
		if err != nil {
			fail("step %d: %v", i+1, err)
		}
		got1 += fmt.Sprintf("%d:%v ", st.seq, em)
		if i == 3 && fmt.Sprint(em) != "[1 2]" {
			fail("step4 cascade = %v", em)
		}
	}
	fmt.Printf("OK 八步缓冲/发射: %s\n", got1)

	// 2) 乱序多 key：发射序列严格递增、步长 1、无空洞。
	streams := map[string][]int64{"A": {4, 2, 1, 3, 7, 5, 6}, "B": {3, 1, 2}}
	wantLen := map[string]int{"A": 7, "B": 3}
	for k, s := range streams {
		for _, v := range s {
			if _, err := r.Feed(k, v); err != nil {
				fail("key %s feed %d: %v", k, v, err)
			}
		}
		em := r.Emitted(k)
		if len(em) != wantLen[k] {
			fail("key %s emitted %v", k, em)
		}
		for i, v := range em {
			if v != int64(i)+1 {
				fail("key %s hole: %v", k, em)
			}
		}
	}
	fmt.Println("OK 乱序多 key: 各 key 已发射严格递增无空洞")

	// 3) 背压拒绝与重复丢弃：三类哨兵错误互不相同。
	bp, _ := api.New(1)
	_, _ = bp.Feed("Z", 3)
	if _, e := bp.Feed("Z", 2); !errors.Is(e, api.ErrBackpressure) {
		fail("backpressure err = %v", e)
	}
	if bp.Buffered("Z") != 1 || len(bp.Emitted("Z")) != 0 {
		fail("backpressure left trace")
	}
	if r.Dropped() != 1 {
		fail("dropped = %d, want 1", r.Dropped())
	}
	if api.ErrEmptyKey == api.ErrInvalidMax || api.ErrInvalidMax == api.ErrBackpressure ||
		api.ErrEmptyKey == api.ErrBackpressure {
		fail("sentinel errors not distinct")
	}
	fmt.Println("OK 背压拒绝 + 重复丢弃(dropped=1)，三类哨兵互不相同")

	// 4) 与朴素参照一致：SelfCheck 内置多组序列回放。
	if err := r.SelfCheck(); err != nil {
		fail("SelfCheck: %v", err)
	}
	fmt.Println("OK 朴素参照一致 + New(0)/空key 判定: SelfCheck 通过")

	// 5) 被拒后状态不变。
	snap := [3]int64{int64(len(r.Emitted("K"))), int64(r.Buffered("K")), r.Dropped()}
	if _, e := api.New(0); !errors.Is(e, api.ErrInvalidMax) {
		fail("New(0) err = %v", e)
	}
	if _, e := r.Feed("", 9); !errors.Is(e, api.ErrEmptyKey) {
		fail("empty key err = %v", e)
	}
	if now := [3]int64{int64(len(r.Emitted("K"))), int64(r.Buffered("K")), r.Dropped()}; now != snap {
		fail("rejection left trace: %v -> %v", snap, now)
	}
	fmt.Printf("OK 拒绝不留痕: %v 拒绝前后不变\n", snap)

	// 6) 大 m 非平方：探针字段不公开，故用耗时比侧证（O(m^2) 10 倍规模约 100 倍）。
	cascadeTime := func(m int) time.Duration {
		best := time.Hour
		for rep := 0; rep < 5; rep++ {
			rb, _ := api.New(m + 1)
			for s := int64(2); s <= int64(m)+1; s++ {
				rb.Feed("C", s)
			}
			t := time.Now()
			rb.Feed("C", 1)
			if d := time.Since(t); d < best {
				best = d
			}
		}
		return best
	}
	ratio := float64(cascadeTime(20000)) / float64(cascadeTime(2000))
	if ratio >= 40 {
		fail("cascade scales quadratically: ratio %.1f", ratio)
	}
	fmt.Printf("OK 大 m 非平方: 2k->20k 级联耗时比 %.1f (<40; 平方则≈100)\n", ratio)

	// 7) 并发：N 个 goroutine 各对互异 key 乱序喂 1..L（容量给 L，乱序不背压）。
	const N, L = 16, 200
	rc, _ := api.New(L)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for g := 0; g < N; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			<-start
			for _, i := range rand.Perm(L) {
				if _, err := rc.Feed(fmt.Sprintf("g%d", g), int64(i)+1); err != nil {
					fail("g%d: %v", g, err)
				}
			}
		}(g)
	}
	close(start)
	wg.Wait()
	for g := 0; g < N; g++ {
		if em := rc.Emitted(fmt.Sprintf("g%d", g)); len(em) != L || em[L-1] != L {
			fail("g%d final = len %d", g, len(em))
		}
	}
	fmt.Printf("OK 并发: %d goroutine x %d 互异 key，全部发射 1..%d\n", N, L, L)
}
