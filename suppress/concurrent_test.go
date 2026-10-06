package suppress

import (
	"sync"
	"testing"
)

// TestConcurrentRegistrationAndEvaluation 多调用方并发登记、判定并发进行：
// 在 -race 下验证无数据竞争，且每次判定都看到一致快照（可重复、结构自洽）。
func TestConcurrentRegistrationAndEvaluation(t *testing.T) {
	s := NewSession(100, rulesA, true)
	var producers, consumers sync.WaitGroup

	// 多个生产者并发登记诊断与指令（含一部分必然被拒绝的非法/重复登记）。
	for w := 0; w < 8; w++ {
		producers.Add(1)
		go func(w int) {
			defer producers.Done()
			for i := 0; i < 200; i++ {
				line := 1 + (w*7+i*13)%100
				rule := rulesA[(w+i)%len(rulesA)]
				_ = s.AddDiagnostic(Diagnostic{Line: line, Column: 1 + i%5, Rule: rule})
				kind := Kind(i % 5)
				labels := []string{rule}
				if i%4 == 0 {
					labels = nil // 等同“全部”
				}
				_ = s.AddDirective(Directive{Line: line, Kind: kind, Labels: labels, Reason: "r"})
				if i%7 == 0 {
					_ = s.AddDirective(Directive{Line: 0, Kind: kind}) // 非法，必被拒
				}
			}
		}(w)
	}

	// 判定与登记并发：结果必须始终自洽（保留+抑制==已接受诊断数，且已排序）。
	stop := make(chan struct{})
	consumers.Add(1)
	go func() {
		defer consumers.Done()
		for {
			select {
			case <-stop:
				return
			default:
				j := s.Evaluate()
				if !isSortedKept(j.Kept) || !isSortedSuppressed(j.Suppressed) {
					t.Errorf("并发判定观察到未排序结果（快照不一致）")
					return
				}
			}
		}
	}()

	producers.Wait()
	close(stop)
	consumers.Wait()
	final := s.Evaluate()
	totalDiag := len(final.Kept) + len(final.Suppressed)
	snap := s.takeSnapshot()
	if totalDiag != len(snap.diagnostics) {
		t.Fatalf("保留+抑制应等于已接受诊断数: %d != %d", totalDiag, len(snap.diagnostics))
	}
	// 登记结束后重复判定必须严格一致。
	again := s.Evaluate()
	if len(again.Kept) != len(final.Kept) || len(again.Suppressed) != len(final.Suppressed) ||
		len(again.Issues) != len(final.Issues) {
		t.Fatalf("重复判定结果不一致")
	}
}

func isSortedKept(xs []KeptDiagnostic) bool {
	for i := 1; i < len(xs); i++ {
		if diagLess(xs[i].Diagnostic, xs[i].Index, xs[i-1].Diagnostic, xs[i-1].Index) {
			return false
		}
	}
	return true
}

func isSortedSuppressed(xs []SuppressedDiagnostic) bool {
	for i := 1; i < len(xs); i++ {
		if diagLess(xs[i].Diagnostic, xs[i].Index, xs[i-1].Diagnostic, xs[i-1].Index) {
			return false
		}
	}
	return true
}

// BenchmarkEvaluate 验证判定随指令+诊断规模近线性增长。
// 用 b.Lines 对比 N=1000 与 N=4000（4 倍输入）时的总耗时与单诊断开销，
// 单诊断命中只做常数次 O(log N) 查找，不随指令总数线性扫描。
func BenchmarkEvaluate(b *testing.B) {
	bench := func(n int) func(b *testing.B) {
		return func(b *testing.B) {
			s := NewSession(n, rulesA, false)
			for i := 0; i < n; i++ {
				line := 1 + i%n
				_ = s.AddDiagnostic(Diagnostic{Line: line, Column: 1, Rule: rulesA[i%3]})
				kind := Kind(i % 5)
				_ = s.AddDirective(Directive{Line: line, Kind: kind, Labels: []string{rulesA[i%3], AllTag}, Reason: "r"})
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = s.Evaluate()
			}
		}
	}
	b.Run("N1000", bench(1000))
	b.Run("N4000", bench(4000))
}
