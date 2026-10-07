package incremental

import (
	"fmt"
	"math/rand"
	"slices"
	"sync"
	"testing"
)

// chainScript 是一条链路的确定性操作脚本：若干周期，每个周期
// 声明 [start, end)，包含若干次提交（含重试）以及是否中断。
type chainScript struct {
	chainID string
	cycles  []cycleScript
}

type cycleScript struct {
	start, end Cursor
	submits    []Write // 按脚本顺序提交（含交错重试）
	abort      bool    // true 表示确认前中断
}

// genScript 生成确定性脚本：源写入位点固定，周期内随机穿插重试，
// 随机在确认前中断（中断的周期稍后以同一位点区间重新发起）。
func genScript(rng *rand.Rand, chainID string, src *MemSource, cycles int) chainScript {
	s := chainScript{chainID: chainID}
	start := GenesisCursor
	for i := 0; i < cycles; i++ {
		head, _ := src.Head()
		end := start + Cursor(1+rng.Intn(5))
		if end > head {
			end = head
		}
		ws, _ := src.Scan(start, end)
		makeCycle := func() cycleScript {
			cs := cycleScript{start: start, end: end}
			for _, w := range ws {
				cs.submits = append(cs.submits, w)
				// 随机插入对 Earlier 写入的重试（夹在其它写入之间）
				if rng.Intn(3) == 0 && len(cs.submits) > 0 {
					cs.submits = append(cs.submits, cs.submits[rng.Intn(len(cs.submits))])
				}
			}
			return cs
		}
		// 每个周期先随机中断 0~2 次，再完整执行一次。
		for r := 0; r < rng.Intn(3); r++ {
			cs := makeCycle()
			cs.abort = true
			// 中断点随机：只提交前一部分
			if len(cs.submits) > 0 {
				cs.submits = cs.submits[:rng.Intn(len(cs.submits)+1)]
			}
			s.cycles = append(s.cycles, cs)
		}
		s.cycles = append(s.cycles, makeCycle())
		start = end
	}
	return s
}

// runScript 在给定组件上顺序执行一条链路的脚本。
func runScript(t *testing.T, e *Exporter, s chainScript) error {
	t.Helper()
	for ci, cs := range s.cycles {
		c, err := e.BeginCycle(s.chainID, cs.start, cs.end)
		if err != nil {
			return fmt.Errorf("chain %s cycle %d begin: %w", s.chainID, ci, err)
		}
		for _, w := range cs.submits {
			if err := c.Submit(w); err != nil {
				return fmt.Errorf("chain %s cycle %d submit: %w", s.chainID, ci, err)
			}
		}
		if cs.abort {
			c.Abort()
			continue
		}
		if err := c.Commit(); err != nil {
			return fmt.Errorf("chain %s cycle %d commit: %w", s.chainID, ci, err)
		}
	}
	return nil
}

// TestConcurrentChainsIndependent 验证多条链路并发推进时各自位点
// 互不干扰，且每条链路的输出与各自单独串行执行完全一致。
func TestConcurrentChainsIndependent(t *testing.T) {
	const numChains = 8
	src := NewMemSource()
	for i := 0; i < 200; i++ {
		src.Append(Write{ID: fmt.Sprintf("w%03d", i), Payload: "p"})
	}

	// 为每条链路生成确定性脚本。
	scripts := make([]chainScript, numChains)
	for i := range scripts {
		scripts[i] = genScript(rand.New(rand.NewSource(int64(1000+i))), fmt.Sprintf("chain-%d", i), src, 12)
	}

	// 串行参照：同一批脚本在独立组件上逐条链路顺序执行。
	ref := NewExporter(src, NewMemCursorStore(), NewMemLedger(), Options{DedupWindow: 16})
	want := make(map[string][]string, numChains)
	for _, s := range scripts {
		if err := runScript(t, ref, s); err != nil {
			t.Fatal(err)
		}
		want[s.chainID] = writeIDs(ref.Published(s.chainID))
	}

	// 并发执行：共享同一组件、同一数据源。
	exp := NewExporter(src, NewMemCursorStore(), NewMemLedger(), Options{DedupWindow: 16})
	var wg sync.WaitGroup
	errs := make(chan error, numChains)
	for _, s := range scripts {
		wg.Add(1)
		go func(s chainScript) {
			defer wg.Done()
			if err := runScript(t, exp, s); err != nil {
				errs <- err
			}
		}(s)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	for _, s := range scripts {
		got := writeIDs(exp.Published(s.chainID))
		if !slices.Equal(got, want[s.chainID]) {
			t.Fatalf("chain %s: concurrent output = %v, serial = %v", s.chainID, got, want[s.chainID])
		}
		// 各链路确认的结束位点必须等于脚本最后一个周期的结束位点。
		last := s.cycles[len(s.cycles)-1]
		cur, err := exp.ConfirmedCursor(s.chainID)
		if err != nil || cur != last.end {
			t.Fatalf("chain %s: confirmed = %d, %v; want %d", s.chainID, cur, err, last.end)
		}
	}
}

// TestRepeatedInterruptsEqualUninterrupted 验证同一链路经历多次中断
// 与重新发起后，累计输出与假设从未中断的持续输出逐条完全一致。
func TestRepeatedInterruptsEqualUninterrupted(t *testing.T) {
	for seed := int64(0); seed < 20; seed++ {
		rng := rand.New(rand.NewSource(seed))
		src := NewMemSource()
		for i := 0; i < 60; i++ {
			src.Append(Write{ID: fmt.Sprintf("w%02d", i), Payload: "p"})
		}
		script := genScript(rng, "c", src, 10)

		interrupted := NewExporter(src, NewMemCursorStore(), NewMemLedger(), Options{})
		if err := runScript(t, interrupted, script); err != nil {
			t.Fatal(err)
		}

		// 从未中断的参照：同样的位点区间，一次执行到底。
		clean := NewExporter(src, NewMemCursorStore(), NewMemLedger(), Options{})
		start := GenesisCursor
		for _, cs := range script.cycles {
			if cs.abort || cs.end == start {
				continue
			}
			c, err := clean.BeginCycle("c", start, cs.end)
			if err != nil {
				t.Fatal(err)
			}
			ws, _ := src.Scan(start, cs.end)
			for _, w := range ws {
				if err := c.Submit(w); err != nil {
					t.Fatal(err)
				}
			}
			if err := c.Commit(); err != nil {
				t.Fatal(err)
			}
			start = cs.end
		}

		got := writeIDs(interrupted.Published("c"))
		want := writeIDs(clean.Published("c"))
		if !slices.Equal(got, want) {
			t.Fatalf("seed %d: interrupted = %v, uninterrupted = %v", seed, got, want)
		}
	}
}
