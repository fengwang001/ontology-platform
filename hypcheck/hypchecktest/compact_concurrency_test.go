package hypchecktest

import (
	"sync"
	"testing"

	"ontology/hypcheck"
)

// TestCompactedDifferential 压实历史后：切点之后的判定仍与朴素重演一致；
// 切点之前，已知类型未定义报 E1，不可重建情形报 E2，错误优先级保持不变。
func TestCompactedDifferential(t *testing.T) {
	audit := hypcheck.NewMemoryAuditor()
	e := hypcheck.NewEngine(hypcheck.Config{Auditor: audit})
	must(t, e.UpsertPrincipal(0, "u", true))
	must(t, e.ToggleGrant(5, "u", "A", true))
	must(t, e.DefineType(10, "A", hypcheck.TypeVersion{Schema: hypcheck.Schema{Fields: []hypcheck.Field{
		{Name: "p", Type: hypcheck.FieldType(hypcheck.KindStr), Required: true}}}}))
	must(t, e.WriteState(20, "s1", hypcheck.StrValue("v")))
	must(t, e.Compact(50))
	must(t, e.WriteState(60, "s1", hypcheck.StrValue("x")))

	// 切点之后：朴素模型只保留压实后的事件，引擎与其结论必须一致。
	naive := hypcheck.NewNaiveReplay()
	naive.LoadEvents(e.JournalEvents(), e.Manifest())
	for _, at := range []hypcheck.Timestamp{50, 55, 60, 61} {
		req := hypcheck.PrecheckRequest{At: at, TypeID: "A", Caller: "u",
			Params: hypcheck.Params{"p": hypcheck.StrValue("p")}, ObjectKeys: []string{"s1"}}
		if canonicalSig(e.Precheck(req)) != canonicalSig(naive.Precheck(req)) {
			t.Fatalf("post-compaction mismatch at %d", at)
		}
	}

	// 切点之前：类型 A 在 10 才定义 -> at=5 报 E1；未知类型 -> E2。
	if r := e.Precheck(hypcheck.PrecheckRequest{At: 5, TypeID: "A", Caller: "u"}); r.ErrorClass != hypcheck.ErrTypeUndefined {
		t.Fatalf("want E1 before horizon, got %s %s", r.Verdict, r.ErrorClass)
	}
	if r := e.Precheck(hypcheck.PrecheckRequest{At: 5, TypeID: "X", Caller: "u"}); r.ErrorClass != hypcheck.ErrHistoryGap {
		t.Fatalf("want E2 for unreconstructable type, got %s", r.ErrorClass)
	}
	if err := audit.Verify(); err != nil {
		t.Fatalf("audit chain broken after compaction: %v", err)
	}
}

// TestConcurrentLinearizable 并发写入版本演进与只读预检：不发生数据竞争，
// 每次预检都在某个线性化点取得自洽快照（结果只可能是三类合法裁决之一）。
func TestConcurrentLinearizable(t *testing.T) {
	e := hypcheck.NewEngine(hypcheck.Config{Auditor: hypcheck.NewMemoryAuditor()})
	must(t, e.DefineType(0, "A", hypcheck.TypeVersion{Schema: hypcheck.Schema{Fields: []hypcheck.Field{
		{Name: "p", Type: hypcheck.FieldType(hypcheck.KindStr), Required: false}}}}))
	must(t, e.UpsertPrincipal(0, "u", true))

	var wg sync.WaitGroup
	var clock int64
	var clockMu sync.Mutex
	var commitMu sync.Mutex
	nextAt := func() hypcheck.Timestamp {
		clockMu.Lock()
		clock++
		v := hypcheck.Timestamp(clock)
		clockMu.Unlock()
		return v
	}
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for k := 0; k < 200; k++ {
				commitMu.Lock()
				at := nextAt()
				_ = e.ToggleGrant(at, "u", "A", k%2 == 0)
				commitMu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	// 写入阶段结束后再并发只读预检，所有 at 都落在已提交历史内。
	finalAt := hypcheck.Timestamp(clock)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for k := 0; k < 200; k++ {
				at := finalAt - hypcheck.Timestamp((g*200+k)%int(finalAt))
				r := e.Precheck(hypcheck.PrecheckRequest{At: at, TypeID: "A", Caller: "u"})
				switch r.Verdict {
				case hypcheck.VerdictAllowed, hypcheck.VerdictDenied, hypcheck.VerdictError:
				default:
					t.Errorf("illegal verdict %q", r.Verdict)
				}
			}
		}(i)
	}
	wg.Wait()
}

// BenchmarkPrecheckProbeCost 给出单次预检在大历史下的成本可观察基准：
// 关注 Probes（版本探测）与 ns/op 均不随历史总变更次数线性增长（见设计文档复现方法）。
func BenchmarkPrecheck(b *testing.B) {
	bench := func(noise int) func(b *testing.B) {
		return func(b *testing.B) {
			e := hypcheck.NewEngine(hypcheck.Config{})
			_ = e.DefineType(0, "A", hypcheck.TypeVersion{Schema: hypcheck.Schema{Fields: []hypcheck.Field{
				{Name: "p", Type: hypcheck.FieldType(hypcheck.KindInt), Required: true}}}})
			_ = e.UpsertPrincipal(0, "u", true)
			_ = e.ToggleGrant(0, "u", "A", true)
			for i := 1; i <= noise; i++ {
				_ = e.WriteState(hypcheck.Timestamp(i), "n/"+itoa(i), hypcheck.IntValue(int64(i)))
			}
			req := hypcheck.PrecheckRequest{At: hypcheck.Timestamp(noise), TypeID: "A", Caller: "u",
				Params: hypcheck.Params{"p": hypcheck.IntValue(1)}}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				r := e.Precheck(req)
				if r.Verdict != hypcheck.VerdictAllowed {
					b.Fatalf("unexpected %s", r.Verdict)
				}
			}
		}
	}
	b.Run("noise_1000", bench(1000))
	b.Run("noise_100000", bench(100000))
}
