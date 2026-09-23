// Command demo 逐项演示并判定带租期的分布式序列号分配器的验收条件。
package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"ontology/alloc"
	"ontology/audit"
	"ontology/clock"
	"ontology/durable"
)

var t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func tmpDir() string {
	d, _ := os.MkdirTemp("", "demo")
	return d
}
func openCounter(dir string, start uint64) *durable.Counter {
	c, err := durable.Open(filepath.Join(dir, "counter"), start)
	if err != nil {
		panic(err)
	}
	return c
}

// nextN 分发 n 个号；任何错误返回 nil。
func nextN(a *alloc.Allocator, n int) []uint64 {
	ids := make([]uint64, 0, n)
	for i := 0; i < n; i++ {
		id, err := a.Next()
		if err != nil {
			return nil
		}
		ids = append(ids, id)
	}
	return ids
}

// runConcurrent 起 instances 个实例并发各分发 per 个号，返回自检报告。
func runConcurrent(instances, per int) audit.Report {
	ctr := openCounter(tmpDir(), 0)
	clk := clock.NewFake(t0)
	streams := make([][]uint64, instances)
	var wg sync.WaitGroup
	for g := 0; g < instances; g++ {
		a := alloc.New(clk, ctr, alloc.Config{TTL: time.Hour})
		wg.Add(1)
		go func(g int, a *alloc.Allocator) {
			defer wg.Done()
			streams[g] = nextN(a, per)
		}(g, a)
	}
	wg.Wait()
	return audit.Analyze(streams...)
}

// 崩溃后首号 >= 已租段末尾。
func checkCrash() bool {
	dir := tmpDir()
	cfg := alloc.Config{TTL: time.Hour, MinSeg: 100, MaxSeg: 100}
	a := alloc.New(clock.NewFake(t0), openCounter(dir, 100), cfg)
	nextN(a, 50)
	first, err := alloc.New(clock.NewFake(t0), openCounter(dir, 100), cfg).Next()
	return err == nil && first >= 200
}

// 旧段剩余号永不出现。
func checkExpiryVoid() bool {
	clk := clock.NewFake(t0)
	a := alloc.New(clk, openCounter(tmpDir(), 0), alloc.Config{TTL: time.Minute, MinSeg: 100, MaxSeg: 100})
	nextN(a, 10)
	clk.Advance(2 * time.Minute)
	seen := map[uint64]bool{}
	for _, id := range nextN(a, 100) {
		seen[id] = true
	}
	ok := seen[100]
	for v := uint64(10); v < 100; v++ {
		ok = ok && !seen[v]
	}
	return ok
}

// 时钟回拨被拒。
func checkRollback() bool {
	clk := clock.NewFake(t0)
	a := alloc.New(clk, openCounter(tmpDir(), 0), alloc.Config{TTL: time.Minute})
	nextN(a, 1)
	clk.Advance(-time.Second)
	_, err := a.Next()
	return errors.Is(err, alloc.ErrClockRollback)
}

// 10 万分发持久写 <= 120。
func checkPersistBound() bool {
	a := alloc.New(clock.NewFake(t0), openCounter(tmpDir(), 0), alloc.Config{TTL: time.Hour, MinSeg: 1000, MaxSeg: 1000})
	return nextN(a, 100000) != nil && a.PersistWrites() <= 120
}

// 高低频阶段段长差异。
func checkAdaptive() bool {
	clk := clock.NewFake(t0)
	a := alloc.New(clk, openCounter(tmpDir(), 0), alloc.Config{TTL: time.Hour, MinSeg: 64, MaxSeg: 4096})
	var hi, lo int64
	for i := 0; i < 30000; i++ {
		if a.Next(); i%1000 == 0 {
			hi += a.SegLen()
		}
	}
	for i := 0; i < 20; i++ {
		clk.Advance(2 * time.Hour)
		if _, err := a.Next(); err != nil {
			return false
		}
		lo += a.SegLen()
	}
	return hi/30 >= 4*(lo/20) && hi/30 >= 3000
}

// 4 实例 10 万号无重复且各自单调。
func checkUniqueness() bool {
	r := runConcurrent(4, 25000)
	return r.Total == 100000 && r.Unique && r.Monotonic
}

// 空洞长度等式精确成立。
func checkGapEquation() bool {
	dir := tmpDir()
	cfg := alloc.Config{TTL: time.Minute, MinSeg: 100, MaxSeg: 100}
	clk := clock.NewFake(t0)
	sa := nextN(alloc.New(clk, openCounter(dir, 0), cfg), 50) // 崩溃丢弃 50
	b := alloc.New(clk, openCounter(dir, 0), cfg)
	sb := nextN(b, 10)
	clk.Advance(2 * time.Minute) // 到期作废 90
	r := audit.Analyze(sa, append(sb, nextN(b, 100)...))
	return r.GapRanges == 2 && r.GapTotal == 140
}

// 三类截断全部拒绝启动而非回退到 0。
func checkTruncation() bool {
	dir := tmpDir()
	if openCounter(dir, 0).Advance(12345) != nil {
		return false
	}
	full, err := os.ReadFile(filepath.Join(dir, "counter"))
	if err != nil {
		return false
	}
	var saw [3]bool
	for n := 1; n < len(full); n++ {
		p := filepath.Join(tmpDir(), "counter")
		if os.WriteFile(p, full[:n], 0o600) != nil {
			return false
		}
		c, err := durable.Open(p, 0)
		if err == nil || c != nil { // 必须拒绝启动
			return false
		}
		saw[0] = saw[0] || errors.Is(err, durable.ErrHeaderIncomplete)
		saw[1] = saw[1] || errors.Is(err, durable.ErrValueIncomplete)
		saw[2] = saw[2] || errors.Is(err, durable.ErrCRC)
	}
	return saw[0] && saw[1] && saw[2]
}

// 并发租用无重叠。
func checkConcurrent() bool {
	r := runConcurrent(8, 5000)
	return r.Total == 40000 && r.Unique
}
func main() {
	checks := []struct {
		name string
		run  func() bool
	}{
		{"crash: first id >= leased end", checkCrash}, {"expiry: voided ids never reappear", checkExpiryVoid},
		{"clock rollback rejected", checkRollback}, {"100k ids, persist writes <= 120", checkPersistBound},
		{"adaptive seg len hi >> lo", checkAdaptive}, {"4 instances x 25k ids unique", checkUniqueness},
		{"gap total == voided + discarded", checkGapEquation}, {"all truncations refuse to start", checkTruncation},
		{"concurrent lease no overlap", checkConcurrent},
	}
	fails := 0
	for _, c := range checks {
		status := "OK  "
		if !c.run() {
			status, fails = "FAIL", fails+1
		}
		fmt.Printf("%s %s\n", status, c.name)
	}
	fmt.Printf("TOTAL %d checks, %d failed\n", len(checks), fails)
	if fails > 0 {
		os.Exit(1)
	}
}
