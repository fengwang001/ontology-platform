package main

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/lru"
)

func main() {
	var fails int32
	rep := func(name, detail string, ok bool) {
		st := "OK"
		if !ok {
			st, fails = "FAIL", 1
		}
		fmt.Printf("%s %s: %s\n", st, name, detail)
	}

	// --- Section 3 eight-step trace (hotCap=2, maxCold=100) ---
	s, _ := api.New(2, 100)
	script := []string{"P A 1|A|", "P B 2|BA|", "G A 1|AB|", "P C 3|CA|B",
		"G B 2|BC|A", "P D 4|DB|AC", "G A 1|AD|BC", "P E 5|EA|BCD"}
	prev := map[string]bool{}
	var steps []string
	traceOK := true
	for i, q := range script {
		p := strings.Split(q, "|")
		f := strings.Fields(p[0])
		ret := "-"
		if f[0] == "P" {
			n, _ := strconv.ParseInt(f[2], 10, 64)
			traceOK = s.Put(f[1], n) == nil && traceOK
		} else {
			v, err := s.Get(f[1])
			traceOK, ret = err == nil && traceOK, fmt.Sprint(v)
		}
		cold := s.ColdKeys()
		ev := "-"
		for _, k := range cold {
			if !prev[k] {
				ev = k
			}
		}
		prev = map[string]bool{}
		for _, k := range cold {
			prev[k] = true
		}
		h := strings.Join(s.HotKeys(), "")
		c := strings.Join(cold, "")
		steps = append(steps, fmt.Sprintf("%d:h%s c%s e%s r%s", i+1, h, c, ev, ret))
		traceOK = traceOK && h == p[1] && c == p[2]
	}
	rep("trace", strings.Join(steps, " | "), traceOK)

	// --- Step4/step5 judgments, Get returns, final tiers, three traps ---
	bv, _ := s.Value("B") // B is cold after step 8; probe moves nothing
	traps := "step4 换出 B(FIFO错换 A, 冷成[A]); step2 不换出(>=错换 A,热[B]冷[A]); step5 Get(B)=2(丢值错返 0)"
	rep("traps", fmt.Sprintf("%s; 最终 h[EA] c[BCD], Value(B)=%d", traps, bv),
		bv == 2 && strings.Join(s.HotKeys(), "") == "EA" && strings.Join(s.ColdKeys(), "") == "BCD")

	// --- Four distinct sentinel errors + rejection leaves no trace ---
	errs := map[error]string{}
	_, e0 := api.New(-1, 1)
	errs[e0] = "BadCap"
	c, _ := api.New(1, 1)
	errs[c.Put("", 1)] = "EmptyKey"
	_, eNF := c.Get("nope")
	errs[eNF] = "NotFound"
	c.Put("a", 1)
	c.Put("b", 2) // evict a -> cold
	before := dump(c)
	eFull := c.Put("c", 3) // hot{b} full, cold{a} full
	errs[eFull] = "ColdFull"
	vb, eb := c.Get("b")
	rep("errors", "BadCap/EmptyKey/NotFound/ColdFull 互异、拒绝后状态不变且仍可用",
		len(errs) == 4 && errors.Is(eFull, api.ErrColdFull) && dump(c) == before && eb == nil && vb == 2)

	// --- LRU-locate cost must not grow with m ---
	costOK := true
	for _, m := range []int{100, 1000, 10000} {
		costOK = lru.VerifyEvictionCost(m) == nil && costOK
	}
	rep("evict-cost", "m=100/1000/10000 定位检查数恒<=1(链表尾节点,非整表扫描)", costOK)

	// --- Concurrency: N distinct Gets on a full hot tier ---
	const n = 200
	p, _ := api.New(n, n*2)
	for i := 0; i < n; i++ {
		p.Put(key(i), int64(i))
	}
	var bad int32
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if v, e := p.Get(key(i)); e != nil || v != int64(i) {
				atomic.StoreInt32(&bad, 1)
			}
		}(i)
	}
	wg.Wait()
	rep("concurrent", "200 并发 Get 值全对、热层<=cap、SelfCheck 通过",
		atomic.LoadInt32(&bad) == 0 && len(p.HotKeys()) <= n && p.SelfCheck() == nil && s.SelfCheck() == nil)

	// --- Concurrency: N Value probes of one key must all agree ---
	var same int32 = 1
	var wg2 sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg2.Add(1)
		go func() {
			defer wg2.Done()
			if v, _ := p.Value(key(5)); v != 5 {
				atomic.StoreInt32(&same, 0)
			}
		}()
	}
	wg2.Wait()
	rep("readonly", "64 并发 Value(同键 k0005) 全部=5", atomic.LoadInt32(&same) == 1)

	if atomic.LoadInt32(&fails) != 0 {
		os.Exit(1)
	}
}

func key(i int) string { return fmt.Sprintf("k%04d", i) }
func dump(s *api.Store) string {
	return strings.Join(s.HotKeys(), "") + "|" + strings.Join(s.ColdKeys(), "")
}
