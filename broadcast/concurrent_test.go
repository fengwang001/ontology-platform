package broadcast

import (
	"strings"
	"sync"
	"testing"
)

// TestConcurrentAccess 在 -race 下验证发布、投递、发送与查询可被多个
// 执行体并发调用且结果自洽：最终所有实例追平后无残留缓冲，所有已发送
// 数据都恰好按其标签版本处理。
func TestConcurrentAccess(t *testing.T) {
	const instances, capacity, publishers = 4, 200, 5
	e, _ := newTestEngine(t, instances, capacity)

	const totalVersions = publishers * 4 // 每个发布者发布 4 个版本
	var wg sync.WaitGroup

	// 发布者：各自发布不同标识的规则。
	for p := 0; p < publishers; p++ {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			for k := 0; k < 4; k++ {
				ruleID := "r" + string(rune('a'+p))
				_, err := e.Publish([]Change{{
					Op:   OpUpsert,
					Rule: Rule{ID: ruleID, Threshold: 1 << 30}, // 默认不命中
				}})
				if err != nil {
					t.Errorf("publish: %v", err)
					return
				}
			}
		}(p)
	}

	// 发送者：持续发送非负键数据（阈值极高，预期无命中）。
	const senders = 8
	for s := 0; s < senders; s++ {
		wg.Add(1)
		go func(s int) {
			defer wg.Done()
			for k := s; k < 400; k += senders {
				if _, _, err := e.Send(k, 1); err != nil {
					t.Errorf("send: %v", err)
					return
				}
			}
		}(s)
	}

	// 投递者：反复把各实例追平到当前全局版本。
	const deliverers = 3
	for d := 0; d < deliverers; d++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				gv := e.GlobalVersion()
				caughtUp := true
				for i := 0; i < instances; i++ {
					av, _ := e.AppliedVersion(i)
					if av < gv {
						caughtUp = false
						_, _, _ = e.Deliver(i)
					}
				}
				if gv == totalVersions && caughtUp {
					return
				}
			}
		}()
	}

	// 查询者：并发只读查询，不应 panic 或产生竞态。
	const queriers = 2
	for q := 0; q < queriers; q++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				_, _ = e.Hits(i % instances)
				_ = e.AllHits()
				_, _ = e.BufferLen(i % instances)
			}
		}()
	}

	wg.Wait()

	if gv := e.GlobalVersion(); gv != totalVersions {
		t.Fatalf("global version = %d, want %d", gv, totalVersions)
	}
	for i := 0; i < instances; i++ {
		av, _ := e.AppliedVersion(i)
		if av != totalVersions {
			t.Fatalf("instance %d applied = %d, want %d", i, av, totalVersions)
		}
		n, _ := e.BufferLen(i)
		if n != 0 {
			t.Fatalf("instance %d buffer = %d, want 0 after catch-up", i, n)
		}
	}
	// 阈值为 1<<30，所有 value=1 的数据都不应命中。
	for i, hits := range e.AllHits() {
		if len(hits) != 0 {
			t.Fatalf("instance %d unexpected hits: %v", i, hits)
		}
	}
}

// TestLogging 校验日志中打印每步输入、版本、缓冲与判定依据，
// 且拒绝记录包含互不相同的可区分原因。
func TestLogging(t *testing.T) {
	e, logs := newTestEngine(t, 2, 1)

	mustPublish(t, e, []Change{{Op: OpUpsert, Rule: Rule{ID: "r", Threshold: 1}}})
	if _, _, err := e.Send(0, 5); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.Deliver(0); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.Send(-3, 1); err == nil {
		t.Fatal("expected negative key error")
	}
	if _, _, err := e.Deliver(9); err == nil {
		t.Fatal("expected invalid instance error")
	}
	if _, err := e.Hits(0); err != nil {
		t.Fatal(err)
	}

	out := logs.String()
	for _, want := range []string{
		`op=publish`,
		`publishedVersion=1`,
		`decision="publish advances global version only`,
		`op=send`,
		`tagVersion=1`,
		`appliedVersion=0`,
		`bufferLen=1`,
		`decision="applied version differs from tag: buffered in arrival order"`,
		`op=deliver`,
		`flushed=1`,
		`op=query`,
		`rejected=true`,
		ErrNegativeKey.Error(),
		ErrInvalidInstance.Error(),
	} {
		if !strings.Contains(out, want) {
			t.Errorf("log missing %q\n--- logs ---\n%s", want, out)
		}
	}
}
